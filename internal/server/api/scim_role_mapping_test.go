//go:build enterprise

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// resetSCIMTokenCleanup гасит singleton scim_config (token_hash → "") по завершении теста.
// Тесты пакета делят ОДНУ БД, а scim_config — singleton; иначе ротация токена в этих тестах
// протекла бы в TestSCIMAdminConfigAndRotate (тот ждёт enabled=false до первой ротации).
func resetSCIMTokenCleanup(t *testing.T, db *storage.DB) {
	t.Cleanup(func() { _ = db.SetSCIMTokenHash(context.Background(), "") })
}

// putRoleMapping настраивает маппинг групп→роли через админ-ручку и проверяет 200.
func putRoleMapping(t *testing.T, rtr http.Handler, adminTok, adminGroups, defaultRole string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"admin_group_values": adminGroups, "default_role": defaultRole})
	w := authedDo(t, rtr, http.MethodPut, "/api/v1/scim/role-mapping", body, adminTok)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /scim/role-mapping = %d, body %s", w.Code, w.Body)
	}
}

// scimCreateUser POST'ит SCIM-юзера с заданными группами (nil = поля groups нет) и возвращает id.
func scimCreateUser(t *testing.T, rtr http.Handler, scimTok, email string, groups []map[string]any) string {
	t.Helper()
	payload := map[string]any{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"userName": email,
		"name":     map[string]string{"givenName": "Test", "familyName": "User"},
		"emails":   []map[string]any{{"value": email, "primary": true}},
		"active":   true,
	}
	if groups != nil {
		payload["groups"] = groups
	}
	body, _ := json.Marshal(payload)
	w := authedDo(t, rtr, http.MethodPost, "/scim/v2/Users", body, scimTok)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /Users = %d, body %s", w.Code, w.Body)
	}
	var created struct {
		ID string `json:"id"`
	}
	json.NewDecoder(w.Body).Decode(&created)
	if created.ID == "" {
		t.Fatal("пустой id созданного SCIM-юзера")
	}
	return created.ID
}

func mustUserRole(t *testing.T, db *storage.DB, id string) string {
	t.Helper()
	full, err := db.GetUserByID(context.Background(), id)
	if err != nil || full == nil {
		t.Fatalf("GetUserByID(%s): %v %+v", id, err, full)
	}
	return full.Role
}

// ── Конфиг маппинга: гейты лицензии/роли, get/put ────────────────────────────

func TestSCIMRoleMappingRequiresLicense(t *testing.T) {
	rtr, db := scimRouter(t, unlicensedManager(t))
	tok := authToken(t, rtr, db)

	if w := authedDo(t, rtr, http.MethodGet, "/api/v1/scim/role-mapping", nil, tok); w.Code != http.StatusPaymentRequired {
		t.Fatalf("GET /scim/role-mapping без лицензии = %d, want 402", w.Code)
	}
	body, _ := json.Marshal(map[string]string{"admin_group_values": "Admins", "default_role": "viewer"})
	if w := authedDo(t, rtr, http.MethodPut, "/api/v1/scim/role-mapping", body, tok); w.Code != http.StatusPaymentRequired {
		t.Fatalf("PUT /scim/role-mapping без лицензии = %d, want 402", w.Code)
	}
}

func TestSCIMRoleMappingRequiresAdmin(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, nil))
	viewer := tokenForRole(t, rtr, db, "viewer", "viewer_")
	body, _ := json.Marshal(map[string]string{"admin_group_values": "Admins", "default_role": "viewer"})
	if w := authedDo(t, rtr, http.MethodPut, "/api/v1/scim/role-mapping", body, viewer); w.Code != http.StatusForbidden {
		t.Fatalf("viewer PUT /scim/role-mapping = %d, want 403", w.Code)
	}
}

func TestSCIMRoleMappingGetPut(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, nil))
	tok := authToken(t, rtr, db)

	// Дефолт до настройки: admin-групп нет, роль viewer.
	w := authedDo(t, rtr, http.MethodGet, "/api/v1/scim/role-mapping", nil, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /scim/role-mapping = %d, body %s", w.Code, w.Body)
	}
	var m storage.SCIMRoleMapping
	json.NewDecoder(w.Body).Decode(&m)
	if m.AdminGroupValues != "" || m.DefaultRole != "viewer" {
		t.Fatalf("дефолт маппинга неверен: %+v", m)
	}

	// PUT нормализует CSV (trim, дедуп, отбрасывает пустые).
	body, _ := json.Marshal(map[string]string{"admin_group_values": " Admins , Admins ,, Ops ", "default_role": "viewer"})
	w = authedDo(t, rtr, http.MethodPut, "/api/v1/scim/role-mapping", body, tok)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /scim/role-mapping = %d, body %s", w.Code, w.Body)
	}
	json.NewDecoder(w.Body).Decode(&m)
	if m.AdminGroupValues != "Admins,Ops" {
		t.Fatalf("нормализация CSV: got %q, want \"Admins,Ops\"", m.AdminGroupValues)
	}

	// GET отражает сохранённое.
	w = authedDo(t, rtr, http.MethodGet, "/api/v1/scim/role-mapping", nil, tok)
	json.NewDecoder(w.Body).Decode(&m)
	if m.AdminGroupValues != "Admins,Ops" || m.DefaultRole != "viewer" {
		t.Fatalf("GET после PUT: %+v", m)
	}
}

// default_role=it_admin запрещён: эскалация только явной группой (it_admin по умолчанию НИКОГДА).
func TestSCIMRoleMappingRejectsAdminDefault(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, nil))
	tok := authToken(t, rtr, db)
	body, _ := json.Marshal(map[string]string{"admin_group_values": "Admins", "default_role": "it_admin"})
	if w := authedDo(t, rtr, http.MethodPut, "/api/v1/scim/role-mapping", body, tok); w.Code != http.StatusBadRequest {
		t.Fatalf("default_role=it_admin = %d, want 400", w.Code)
	}
}

// ── Применение маппинга при провижининге ─────────────────────────────────────

// Create с admin-группой → it_admin; без admin-группы → viewer (default).
func TestSCIMProvisionRoleFromGroups(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, nil))
	resetSCIMTokenCleanup(t, db)
	adminTok := authToken(t, rtr, db)
	putRoleMapping(t, rtr, adminTok, "Admins", "viewer")
	scimTok := "Bearer " + rotateSCIMToken(t, rtr, adminTok)

	// Юзер в admin-группе → it_admin.
	adminID := scimCreateUser(t, rtr, scimTok, "scim-admin-"+t.Name()+"@ex.com",
		[]map[string]any{{"value": "Admins", "display": "Admins"}})
	if role := mustUserRole(t, db, adminID); role != "it_admin" {
		t.Fatalf("юзер с admin-группой: роль %q, want it_admin", role)
	}

	// Юзер без admin-группы → default viewer.
	plainID := scimCreateUser(t, rtr, scimTok, "scim-plain-"+t.Name()+"@ex.com",
		[]map[string]any{{"value": "Engineering", "display": "Engineering"}})
	if role := mustUserRole(t, db, plainID); role != "viewer" {
		t.Fatalf("юзер без admin-группы: роль %q, want viewer", role)
	}

	// Юзер вовсе без поля groups → default viewer (it_admin недостижим без явной группы).
	noneID := scimCreateUser(t, rtr, scimTok, "scim-none-"+t.Name()+"@ex.com", nil)
	if role := mustUserRole(t, db, noneID); role != "viewer" {
		t.Fatalf("юзер без groups: роль %q, want viewer", role)
	}
}

// Пустой allowlist admin-групп → it_admin через SCIM не выдаётся даже при совпадении названий.
func TestSCIMProvisionNoAdminWithoutAllowlist(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, nil))
	resetSCIMTokenCleanup(t, db)
	adminTok := authToken(t, rtr, db)
	// Явно ПУСТОЙ allowlist (default_role viewer): даже совпадение по названию группы не даёт
	// it_admin — эскалация только через непустой allowlist (fail-closed). Задаём явно, т.к.
	// scim_role_mapping — singleton, общий для тестов пакета.
	putRoleMapping(t, rtr, adminTok, "", "viewer")
	scimTok := "Bearer " + rotateSCIMToken(t, rtr, adminTok)
	id := scimCreateUser(t, rtr, scimTok, "scim-noallow-"+t.Name()+"@ex.com",
		[]map[string]any{{"value": "Admins", "display": "Admins"}})
	if role := mustUserRole(t, db, id); role != "viewer" {
		t.Fatalf("пустой allowlist: роль %q, want viewer", role)
	}
}

// Update (PUT) с УБРАННОЙ admin-группой → downgrade it_admin → viewer (отзыв группы в IdP).
func TestSCIMUpdateDowngradesOnGroupRemoval(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, nil))
	resetSCIMTokenCleanup(t, db)
	adminTok := authToken(t, rtr, db)
	putRoleMapping(t, rtr, adminTok, "Admins", "viewer")
	scimTok := "Bearer " + rotateSCIMToken(t, rtr, adminTok)

	id := scimCreateUser(t, rtr, scimTok, "scim-downgrade-"+t.Name()+"@ex.com",
		[]map[string]any{{"value": "Admins", "display": "Admins"}})
	if role := mustUserRole(t, db, id); role != "it_admin" {
		t.Fatalf("после create роль %q, want it_admin", role)
	}

	// PUT с ПУСТЫМ (но присутствующим) groups → пересчёт → default viewer.
	put, _ := json.Marshal(map[string]any{
		"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"active":  true,
		"groups":  []map[string]any{},
	})
	if w := authedDo(t, rtr, http.MethodPut, "/scim/v2/Users/"+id, put, scimTok); w.Code != http.StatusOK {
		t.Fatalf("PUT /Users/{id} = %d, body %s", w.Code, w.Body)
	}
	if role := mustUserRole(t, db, id); role != "viewer" {
		t.Fatalf("после отзыва группы роль %q, want viewer (downgrade)", role)
	}
}

// Fail-closed: PUT БЕЗ поля groups НЕ понижает роль (нет данных о группах → оставляем текущую).
func TestSCIMUpdateKeepsRoleWithoutGroups(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, nil))
	resetSCIMTokenCleanup(t, db)
	adminTok := authToken(t, rtr, db)
	putRoleMapping(t, rtr, adminTok, "Admins", "viewer")
	scimTok := "Bearer " + rotateSCIMToken(t, rtr, adminTok)

	id := scimCreateUser(t, rtr, scimTok, "scim-keep-"+t.Name()+"@ex.com",
		[]map[string]any{{"value": "Admins", "display": "Admins"}})

	// PUT БЕЗ groups (только смена имени) → роль сохраняется it_admin.
	put, _ := json.Marshal(map[string]any{
		"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:User"},
		"active":  true,
		"name":    map[string]string{"givenName": "Renamed", "familyName": "User"},
	})
	if w := authedDo(t, rtr, http.MethodPut, "/scim/v2/Users/"+id, put, scimTok); w.Code != http.StatusOK {
		t.Fatalf("PUT /Users/{id} = %d, body %s", w.Code, w.Body)
	}
	if role := mustUserRole(t, db, id); role != "it_admin" {
		t.Fatalf("PUT без groups не должен понижать: роль %q, want it_admin", role)
	}
}

// capabilities gate sanity: без FeatureSCIM в лицензии ручка маппинга отдаёт 402 (а не 500).
func TestSCIMRoleMappingLicenseFeatureGate(t *testing.T) {
	rtr, db := scimRouter(t, licensedManager(t, []string{license.FeatureSSO})) // лицензия есть, но НЕ SCIM
	tok := authToken(t, rtr, db)
	if w := authedDo(t, rtr, http.MethodGet, "/api/v1/scim/role-mapping", nil, tok); w.Code != http.StatusPaymentRequired {
		t.Fatalf("GET без FeatureSCIM = %d, want 402", w.Code)
	}
}
