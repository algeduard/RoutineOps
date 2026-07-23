//go:build enterprise

package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// SCIM 2.0 provisioning — публичные эндпоинты /scim/v2/* (за FeatureSCIM + bearer-токен).
// Ставится в h.scim через WithSCIM (enterprise.go); базовый шов и делегат — scim.go.
// Управление токеном — админ-ручки SCIMRoutes (scim_handler.go).

const (
	scimContentType    = "application/scim+json"
	scimUserSchema     = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimListSchema     = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimErrorSchema    = "urn:ietf:params:scim:api:messages:2.0:Error"
	scimPatchOpSchema  = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimDefaultRole    = "viewer" // least privilege; маппинг ролей из SCIM-групп — follow-up
	scimActorEmail     = "scim"   // актор аудита: канал IdP, не человек
	scimMaxPageSize    = 200
	scimDefaultPageLen = 100
)

// filterUserNameRe парсит минимальный SCIM-фильтр `userName eq "value"` (единственный, что шлют
// Okta/Azure AD при поиске существующего юзера). Прочие фильтры → пустой матч (totalResults 0).
var filterUserNameRe = regexp.MustCompile(`(?i)^\s*userName\s+eq\s+"(.*)"\s*$`)

type scimProvider struct {
	db           *storage.DB
	mgr          *license.Manager
	publicWebURL string
}

// NewSCIMProvider создаёт SCIM-провайдер. Discovery IdP не требуется — провайдер stateless,
// вся авторизация по bearer-токену на каждый запрос.
func NewSCIMProvider(db *storage.DB, mgr *license.Manager, publicWebURL string) SCIMProvider {
	return &scimProvider{db: db, mgr: mgr, publicWebURL: publicWebURL}
}

// ServeHTTP — единая точка входа /scim/v2/*: гейт по лицензии (402), затем bearer (401), затем
// ручная маршрутизация (две формы пути — /Users и /Users/{id}). Ручная, а не вложенный chi:
// делегат монтируется в NewRouter как /scim/v2/* и получает полный путь; двух форм достаточно.
func (p *scimProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Лицензия ПЕРВОЙ (как прочие enterprise-хендлеры): без FeatureSCIM — 402, БД не трогаем.
	if !p.mgr.Has(license.FeatureSCIM) {
		p.scimError(w, http.StatusPaymentRequired, "SCIM requires an active Enterprise license")
		return
	}
	// Затем bearer: SCIM выключен (токен не сгенерирован) ИЛИ неверный токен → 401.
	if !p.authOK(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="scim"`)
		p.scimError(w, http.StatusUnauthorized, "invalid or missing bearer token")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/scim/v2")
	switch {
	case path == "/Users" || path == "/Users/":
		switch r.Method {
		case http.MethodGet:
			p.listUsers(w, r)
		case http.MethodPost:
			p.createUser(w, r)
		default:
			p.scimError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case strings.HasPrefix(path, "/Users/"):
		id := strings.TrimPrefix(path, "/Users/")
		if id == "" || strings.Contains(id, "/") {
			p.scimError(w, http.StatusNotFound, "resource not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			p.getUser(w, r, id)
		case http.MethodPut:
			p.putUser(w, r, id)
		case http.MethodPatch:
			p.patchUser(w, r, id)
		case http.MethodDelete:
			p.deleteUser(w, r, id)
		default:
			p.scimError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	default:
		p.scimError(w, http.StatusNotFound, "resource not found")
	}
}

// authOK сверяет presented bearer с хранимым sha256-хешем токена constant-time. "" в БД (SCIM
// выключен) → всегда false. Токен нигде не логируется.
func (p *scimProvider) authOK(r *http.Request) bool {
	hdr := r.Header.Get("Authorization")
	if !strings.HasPrefix(hdr, "Bearer ") {
		return false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(hdr, "Bearer "))
	if tok == "" {
		return false
	}
	stored, err := p.db.GetSCIMTokenHash(r.Context())
	if err != nil || stored == "" {
		return false
	}
	sum := sha256.Sum256([]byte(tok))
	presented := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(presented), []byte(stored)) == 1
}

// ── Хендлеры ─────────────────────────────────────────────────────────────────

func (p *scimProvider) listUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter := ""
	if f := strings.TrimSpace(q.Get("filter")); f != "" {
		m := filterUserNameRe.FindStringSubmatch(f)
		if m == nil {
			// Неподдерживаемый фильтр — пустой результат (не 400): IdP-дискаверинг устойчив.
			p.writeSCIM(w, http.StatusOK, p.listResponse(nil, 0, 1))
			return
		}
		filter = m[1]
	}

	startIndex := 1
	if s := q.Get("startIndex"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			startIndex = n
		}
	}
	count := scimDefaultPageLen
	if c := q.Get("count"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n >= 0 {
			count = n
		}
	}
	if count > scimMaxPageSize {
		count = scimMaxPageSize
	}

	users, total, err := p.db.ListSCIMUsers(r.Context(), filter, startIndex, count)
	if err != nil {
		p.scimError(w, http.StatusInternalServerError, "internal error")
		return
	}
	p.writeSCIM(w, http.StatusOK, p.listResponse(users, total, startIndex))
}

func (p *scimProvider) getUser(w http.ResponseWriter, r *http.Request, id string) {
	u, err := p.db.GetSCIMUserByID(r.Context(), id)
	if err != nil {
		p.scimError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if u == nil {
		p.scimError(w, http.StatusNotFound, "resource not found")
		return
	}
	p.writeSCIM(w, http.StatusOK, p.userResource(u))
}

func (p *scimProvider) createUser(w http.ResponseWriter, r *http.Request) {
	var body scimUserPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
		p.scimError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email := strings.TrimSpace(body.resolveEmail())
	if email == "" {
		p.scimError(w, http.StatusBadRequest, "userName (or a primary email) is required")
		return
	}
	active := true
	if body.Active != nil {
		active = *body.Active
	}
	given := strings.TrimSpace(body.Name.GivenName)
	family := strings.TrimSpace(body.Name.FamilyName)
	formatted := formatSCIMName(body.Name.Formatted, given, family, email)

	u, err := p.db.CreateSCIMUser(r.Context(), email, given, family, formatted, scimDefaultRole, active)
	if err != nil {
		if errors.Is(err, storage.ErrSCIMUserExists) {
			p.scimErrorTyped(w, http.StatusConflict, "uniqueness", "userName already exists")
			return
		}
		slog.Error("scim create user", "err", err)
		p.scimError(w, http.StatusInternalServerError, "internal error")
		return
	}
	p.audit(r.Context(), "scim_user_created", u.ID, map[string]any{"email": u.UserName, "active": u.Active})
	w.Header().Set("Location", p.location(u.ID))
	p.writeSCIM(w, http.StatusCreated, p.userResource(u))
}

// putUser — полная замена SCIM-управляемых атрибутов (active + имя). userName/email НЕ
// переименовываем (out of scope MVP), роль/пароль не трогаем (см. UpdateSCIMUser).
func (p *scimProvider) putUser(w http.ResponseWriter, r *http.Request, id string) {
	cur, err := p.db.GetSCIMUserByID(r.Context(), id)
	if err != nil {
		p.scimError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if cur == nil {
		p.scimError(w, http.StatusNotFound, "resource not found")
		return
	}
	var body scimUserPayload
	if derr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); derr != nil {
		p.scimError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	active := cur.Active
	if body.Active != nil {
		active = *body.Active
	}
	given := strings.TrimSpace(body.Name.GivenName)
	family := strings.TrimSpace(body.Name.FamilyName)
	formatted := formatSCIMName(body.Name.Formatted, given, family, cur.UserName)
	p.applyUpdate(w, r, cur, given, family, formatted, active)
}

// patchUser — частичное обновление (SCIM PatchOp). Критичный путь — active=false (деактивация):
// Okta/Azure AD шлют именно PATCH. Мерджим операции поверх текущих значений, чтобы active-only
// патч не затирал имя.
func (p *scimProvider) patchUser(w http.ResponseWriter, r *http.Request, id string) {
	cur, err := p.db.GetSCIMUserByID(r.Context(), id)
	if err != nil {
		p.scimError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if cur == nil {
		p.scimError(w, http.StatusNotFound, "resource not found")
		return
	}
	var body scimPatchPayload
	if derr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); derr != nil {
		p.scimError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	given, family, active := cur.GivenName, cur.FamilyName, cur.Active
	for _, op := range body.Operations {
		if op.opType() == "remove" {
			continue // remove active/name в MVP не поддерживаем
		}
		switch strings.ToLower(strings.TrimSpace(op.Path)) {
		case "active":
			if v, ok := parseSCIMBool(op.Value); ok {
				active = v
			}
		case "name.givenname":
			given = strings.TrimSpace(jsonString(op.Value))
		case "name.familyname":
			family = strings.TrimSpace(jsonString(op.Value))
		case "": // path отсутствует → value это объект атрибутов
			var obj scimUserPayload
			if json.Unmarshal(op.Value, &obj) == nil {
				if obj.Active != nil {
					active = *obj.Active
				}
				if obj.Name.GivenName != "" {
					given = strings.TrimSpace(obj.Name.GivenName)
				}
				if obj.Name.FamilyName != "" {
					family = strings.TrimSpace(obj.Name.FamilyName)
				}
			}
		}
	}
	formatted := formatSCIMName("", given, family, cur.UserName)
	p.applyUpdate(w, r, cur, given, family, formatted, active)
}

// deleteUser — SCIM DELETE трактуем как ДЕАКТИВАЦИЮ (is_active=false), не хард-удаление: аудит и
// восстановление важнее, а IdP всё равно чаще шлёт PATCH active=false. 204 No Content.
func (p *scimProvider) deleteUser(w http.ResponseWriter, r *http.Request, id string) {
	u, err := p.db.SetSCIMUserActive(r.Context(), id, false)
	if err != nil {
		p.scimError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if u == nil {
		p.scimError(w, http.StatusNotFound, "resource not found")
		return
	}
	p.audit(r.Context(), "scim_user_deactivated", u.ID, map[string]any{"email": u.UserName, "via": "delete"})
	w.WriteHeader(http.StatusNoContent)
}

// applyUpdate пишет обновление и отдаёт ресурс; аудитит деактивацию отдельным событием.
func (p *scimProvider) applyUpdate(w http.ResponseWriter, r *http.Request, cur *storage.SCIMUser, given, family, formatted string, active bool) {
	u, err := p.db.UpdateSCIMUser(r.Context(), cur.ID, given, family, formatted, active)
	if err != nil {
		p.scimError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if u == nil {
		p.scimError(w, http.StatusNotFound, "resource not found")
		return
	}
	if cur.Active && !u.Active {
		p.audit(r.Context(), "scim_user_deactivated", u.ID, map[string]any{"email": u.UserName, "via": "update"})
	} else {
		p.audit(r.Context(), "scim_user_updated", u.ID, map[string]any{"email": u.UserName, "active": u.Active})
	}
	p.writeSCIM(w, http.StatusOK, p.userResource(u))
}

// ── SCIM-сериализация ────────────────────────────────────────────────────────

func (p *scimProvider) location(id string) string { return p.publicWebURL + "/scim/v2/Users/" + id }

func (p *scimProvider) userResource(u *storage.SCIMUser) map[string]any {
	return map[string]any{
		"schemas":  []string{scimUserSchema},
		"id":       u.ID,
		"userName": u.UserName,
		"name": map[string]any{
			"givenName":  u.GivenName,
			"familyName": u.FamilyName,
			"formatted":  u.Formatted,
		},
		"emails": []map[string]any{
			{"value": u.UserName, "primary": true},
		},
		"active": u.Active,
		"meta": map[string]any{
			"resourceType": "User",
			"created":      u.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			"location":     p.location(u.ID),
		},
	}
}

func (p *scimProvider) listResponse(users []storage.SCIMUser, total, startIndex int) map[string]any {
	resources := make([]map[string]any, 0, len(users))
	for i := range users {
		resources = append(resources, p.userResource(&users[i]))
	}
	return map[string]any{
		"schemas":      []string{scimListSchema},
		"totalResults": total,
		"startIndex":   startIndex,
		"itemsPerPage": len(resources),
		"Resources":    resources,
	}
}

func (p *scimProvider) writeSCIM(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", scimContentType)
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (p *scimProvider) scimError(w http.ResponseWriter, code int, detail string) {
	p.scimErrorTyped(w, code, "", detail)
}

// scimErrorTyped — SCIM Error с опциональным scimType (напр. "uniqueness" для 409). status —
// строка per RFC 7644.
func (p *scimProvider) scimErrorTyped(w http.ResponseWriter, code int, scimType, detail string) {
	body := map[string]any{
		"schemas": []string{scimErrorSchema},
		"detail":  detail,
		"status":  strconv.Itoa(code),
	}
	if scimType != "" {
		body["scimType"] = scimType
	}
	w.Header().Set("Content-Type", scimContentType)
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func (p *scimProvider) audit(ctx context.Context, action, targetID string, details any) {
	// context.WithoutCancel: аудит переживает отмену запроса (как h.audit). Актор — "scim".
	if err := p.db.WriteAuditLog(context.WithoutCancel(ctx), "", scimActorEmail, action, "user", targetID, details); err != nil {
		slog.Error("scim audit write failed", "action", action, "target_id", targetID, "err", err)
	}
}

// ── Payload-типы и хелперы ───────────────────────────────────────────────────

type scimNamePayload struct {
	GivenName  string `json:"givenName"`
	FamilyName string `json:"familyName"`
	Formatted  string `json:"formatted"`
}

type scimUserPayload struct {
	UserName string          `json:"userName"`
	Name     scimNamePayload `json:"name"`
	Emails   []struct {
		Value   string `json:"value"`
		Primary bool   `json:"primary"`
	} `json:"emails"`
	Active *bool `json:"active"`
}

// resolveEmail — userName, иначе primary email, иначе первый email.
func (b scimUserPayload) resolveEmail() string {
	if strings.TrimSpace(b.UserName) != "" {
		return b.UserName
	}
	for _, e := range b.Emails {
		if e.Primary && strings.TrimSpace(e.Value) != "" {
			return e.Value
		}
	}
	for _, e := range b.Emails {
		if strings.TrimSpace(e.Value) != "" {
			return e.Value
		}
	}
	return ""
}

type scimPatchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

func (o scimPatchOp) opType() string { return strings.ToLower(strings.TrimSpace(o.Op)) }

type scimPatchPayload struct {
	Schemas    []string      `json:"schemas"`
	Operations []scimPatchOp `json:"Operations"`
}

// formatSCIMName — отображаемое имя: явное formatted, иначе "given family", иначе fallback (email).
func formatSCIMName(explicit, given, family, fallback string) string {
	if s := strings.TrimSpace(explicit); s != "" {
		return s
	}
	if s := strings.TrimSpace(strings.TrimSpace(given) + " " + strings.TrimSpace(family)); s != "" {
		return s
	}
	return fallback
}

// parseSCIMBool принимает bool ИЛИ строку ("true"/"False"/…): Azure AD шлёт active строкой.
func parseSCIMBool(raw json.RawMessage) (bool, bool) {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if v, err := strconv.ParseBool(strings.TrimSpace(s)); err == nil {
			return v, true
		}
	}
	return false, false
}

// jsonString — распаковка JSON-строки (для name.givenName и т.п.); "" если не строка.
func jsonString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}
