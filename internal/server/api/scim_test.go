//go:build enterprise

package api_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/mailer"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// scimRouter собирает роутер с публичным SCIM-провайдером (WithSCIM), админ-ручками токена
// (WithAdminRoutes) и /capabilities — под заданный license.Manager.
func scimRouter(t *testing.T, mgr *license.Manager) (http.Handler, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, "https://test.local", t.TempDir(),
		mailer.New("", "", "", "", "", false), false,
		api.WithSCIM(api.NewSCIMProvider(db, mgr, "https://test.local")),
		api.WithAdminRoutes(api.SCIMRoutes(mgr)),
		api.WithRoutes(api.CapabilitiesRoutes(mgr)))
	return rtr, db
}

func unlicensedManager(t *testing.T) *license.Manager {
	t.Helper()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	return license.NewManager(pub, 0, "")
}

// rotateSCIMToken генерирует SCIM-токен через админ-ручку и возвращает сам токен.
func rotateSCIMToken(t *testing.T, rtr http.Handler, adminTok string) string {
	t.Helper()
	w := authedDo(t, rtr, http.MethodPost, "/api/v1/scim/token", []byte("{}"), adminTok)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /scim/token = %d, body %s", w.Code, w.Body)
	}
	var resp struct {
		Token   string `json:"token"`
		BaseURL string `json:"base_url"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Token == "" {
		t.Fatal("пустой SCIM-токен в ответе")
	}
	if resp.BaseURL != "https://test.local/scim/v2" {
		t.Fatalf("base_url = %q", resp.BaseURL)
	}
	return resp.Token
}

// ── Админ-ручки управления токеном ──────────────────────────────────────────

func TestSCIMAdminRequiresLicense(t *testing.T) {
	rtr, db := scimRouter(t, unlicensedManager(t))
	tok := authToken(t, rtr, db)

	if w := authedDo(t, rtr, http.MethodGet, "/api/v1/scim/config", nil, tok); w.Code != http.StatusPaymentRequired {
		t.Fatalf("GET /scim/config без лицензии = %d, want 402", w.Code)
	}
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/scim/token", []byte("{}"), tok); w.Code != http.StatusPaymentRequired {
		t.Fatalf("POST /scim/token без лицензии = %d, want 402", w.Code)
	}
}

func TestSCIMAdminRequiresAdmin(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, nil))
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")
	if w := authedDo(t, rtr, http.MethodPost, "/api/v1/scim/token", []byte("{}"), viewer); w.Code != http.StatusForbidden {
		t.Fatalf("viewer POST /scim/token = %d, want 403", w.Code)
	}
}

func TestSCIMAdminConfigAndRotate(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, nil))
	tok := authToken(t, rtr, db)

	// До генерации токена — enabled=false.
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/scim/config", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /scim/config = %d, body %s", w.Code, w.Body)
	}
	var cfg struct {
		Enabled bool   `json:"enabled"`
		BaseURL string `json:"base_url"`
	}
	json.NewDecoder(w.Body).Decode(&cfg)
	if cfg.Enabled {
		t.Fatal("ожидали enabled=false до генерации токена")
	}

	rotateSCIMToken(t, rtr, tok)

	// После генерации — enabled=true.
	w = authedDo(t, rtr, http.MethodGet, "/api/v1/scim/config", nil, tok)
	json.NewDecoder(w.Body).Decode(&cfg)
	if !cfg.Enabled {
		t.Fatal("ожидали enabled=true после генерации токена")
	}
}

// /capabilities отражает лицензию на SCIM.
func TestCapabilitiesReflectsSCIM(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, []string{license.FeatureSCIM}))
	tok := authToken(t, rtr, db)
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/capabilities", nil, tok)
	var caps map[string]bool
	json.NewDecoder(w.Body).Decode(&caps)
	if !caps[license.FeatureSCIM] {
		t.Fatalf("ожидали scim=true, got %+v", caps)
	}
}

// ── Публичные SCIM 2.0 эндпоинты ────────────────────────────────────────────

func TestSCIMPublicRequiresLicense(t *testing.T) {
	rtr, _ := scimRouter(t, unlicensedManager(t))
	w := authedDo(t, rtr, http.MethodGet, "/scim/v2/Users", nil, "Bearer anything")
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("SCIM без лицензии = %d, want 402", w.Code)
	}
}

func TestSCIMPublicBearerAuth(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, nil))

	// Лицензия есть, но токен не сгенерирован → 401.
	if w := authedDo(t, rtr, http.MethodGet, "/scim/v2/Users", nil, "Bearer none"); w.Code != http.StatusUnauthorized {
		t.Fatalf("без сгенерированного токена = %d, want 401", w.Code)
	}

	adminTok := authToken(t, rtr, db)
	scimTok := rotateSCIMToken(t, rtr, adminTok)

	// Неверный bearer → 401.
	if w := authedDo(t, rtr, http.MethodGet, "/scim/v2/Users", nil, "Bearer wrong-"+scimTok); w.Code != http.StatusUnauthorized {
		t.Fatalf("неверный bearer = %d, want 401", w.Code)
	}
	// Верный bearer → 200.
	if w := authedDo(t, rtr, http.MethodGet, "/scim/v2/Users", nil, "Bearer "+scimTok); w.Code != http.StatusOK {
		t.Fatalf("верный bearer = %d, want 200, body %s", w.Code, w.Body)
	}
}

// open-core (нет WithSCIM) → публичный /scim/v2/* даёт 404.
func TestSCIMPublicAbsentWithoutProvider(t *testing.T) {
	db := newTestDB(t)
	rtr := newRouterFull(t, db) // без opts, как Free
	if w := authedDo(t, rtr, http.MethodGet, "/scim/v2/Users", nil, "Bearer x"); w.Code != http.StatusNotFound {
		t.Fatalf("без провайдера = %d, want 404", w.Code)
	}
}

// Полный CRUD-цикл под верным bearer: create → get → list(filter) → duplicate(409) → deactivate.
func TestSCIMCRUD(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, nil))
	adminTok := authToken(t, rtr, db)
	scimTok := "Bearer " + rotateSCIMToken(t, rtr, adminTok)
	email := "scim-crud-" + t.Name() + "@example.com"

	// CREATE.
	payload := map[string]any{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": email,
		"name":     map[string]string{"givenName": "Alan", "familyName": "Turing"},
		"emails":   []map[string]any{{"value": email, "primary": true}},
		"active":   true,
	}
	body, _ := json.Marshal(payload)
	w := authedDo(t, rtr, http.MethodPost, "/scim/v2/Users", body, scimTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /Users = %d, body %s", w.Code, w.Body)
	}
	var created struct {
		ID       string `json:"id"`
		UserName string `json:"userName"`
		Active   bool   `json:"active"`
		Name     struct {
			GivenName string `json:"givenName"`
		} `json:"name"`
	}
	json.NewDecoder(w.Body).Decode(&created)
	if created.ID == "" || created.UserName != email || !created.Active || created.Name.GivenName != "Alan" {
		t.Fatalf("ресурс создан неверно: %+v", created)
	}
	// Провижининг завёл реального юзера с auth_source=scim и ролью viewer.
	full, _ := db.GetUserByID(context.Background(), created.ID)
	if full == nil || full.AuthSource != "scim" || full.Role != "viewer" {
		t.Fatalf("юзер в БД неверен: %+v", full)
	}

	// GET by id.
	if w := authedDo(t, rtr, http.MethodGet, "/scim/v2/Users/"+created.ID, nil, scimTok); w.Code != http.StatusOK {
		t.Fatalf("GET /Users/{id} = %d", w.Code)
	}

	// LIST с фильтром userName eq.
	filter := url.QueryEscape(fmt.Sprintf(`userName eq "%s"`, email))
	w = authedDo(t, rtr, http.MethodGet, "/scim/v2/Users?filter="+filter, nil, scimTok)
	if w.Code != http.StatusOK {
		t.Fatalf("LIST filter = %d, body %s", w.Code, w.Body)
	}
	var list struct {
		TotalResults int `json:"totalResults"`
		Resources    []struct {
			ID string `json:"id"`
		} `json:"Resources"`
	}
	json.NewDecoder(w.Body).Decode(&list)
	if list.TotalResults != 1 || len(list.Resources) != 1 || list.Resources[0].ID != created.ID {
		t.Fatalf("фильтр вернул неверное: %+v", list)
	}

	// DUPLICATE → 409.
	if w := authedDo(t, rtr, http.MethodPost, "/scim/v2/Users", body, scimTok); w.Code != http.StatusConflict {
		t.Fatalf("дубль POST = %d, want 409", w.Code)
	}

	// PATCH active=false → деактивация (строкой, как шлёт Azure AD).
	patch, _ := json.Marshal(map[string]any{
		"schemas":    []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"},
		"Operations": []map[string]any{{"op": "replace", "path": "active", "value": "False"}},
	})
	if w := authedDo(t, rtr, http.MethodPatch, "/scim/v2/Users/"+created.ID, patch, scimTok); w.Code != http.StatusOK {
		t.Fatalf("PATCH deactivate = %d, body %s", w.Code, w.Body)
	}
	if active, _ := db.IsUserActive(context.Background(), created.ID); active {
		t.Fatal("после PATCH active=false юзер должен быть неактивен")
	}
}

// Деактивация по SCIM отвергает и логин, и уже выданную сессию.
func TestSCIMDeactivationBlocksLoginAndSession(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, nil))
	adminTok := authToken(t, rtr, db)
	scimTok := "Bearer " + rotateSCIMToken(t, rtr, adminTok)

	// Локальный юзер с паролем логинится и получает живую сессию.
	email := "local-" + t.Name() + "@test.com"
	seedUser(t, db, email, "pass123", "viewer")
	sess := loginCookieToken(t, rtr, email, "pass123")

	// Живой токен работает.
	if w := authedDo(t, rtr, http.MethodGet, "/api/v1/me", nil, sess); w.Code != http.StatusOK {
		t.Fatalf("до деактивации /me = %d", w.Code)
	}

	// Деактивируем через SCIM (DELETE = деактивация).
	u, _ := db.GetUserByEmail(context.Background(), email)
	if w := authedDo(t, rtr, http.MethodDelete, "/scim/v2/Users/"+u.ID, nil, scimTok); w.Code != http.StatusNoContent {
		t.Fatalf("SCIM DELETE = %d, want 204", w.Code)
	}

	// Уже выданная сессия отвергается (jwtMiddleware гейтит is_active).
	if w := authedDo(t, rtr, http.MethodGet, "/api/v1/me", nil, sess); w.Code != http.StatusUnauthorized {
		t.Fatalf("после деактивации /me = %d, want 401", w.Code)
	}
	// Повторный логин отвергается (403 deactivated).
	if w := doLogin(t, rtr, email, "pass123"); w.Code != http.StatusForbidden {
		t.Fatalf("логин деактивированного = %d, want 403", w.Code)
	}
}

// loginCookieToken логинится и возвращает "Bearer <jwt>" из cookie.
func loginCookieToken(t *testing.T, rtr http.Handler, email, password string) string {
	t.Helper()
	w := doLogin(t, rtr, email, password)
	if w.Code != http.StatusOK {
		t.Fatalf("login %s = %d, body %s", email, w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "token" {
			return "Bearer " + c.Value
		}
	}
	t.Fatalf("нет token-cookie после логина")
	return ""
}
