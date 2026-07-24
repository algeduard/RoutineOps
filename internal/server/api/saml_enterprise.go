//go:build enterprise

package api

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

// Конкретная SAML 2.0 SP-реализация SAMLProvider (enterprise) на github.com/crewjam/saml.
// Библиотека делает всю XML-крипту: подпись/canonicalization/валидацию ассершна (мы её САМИ НЕ
// пишем — там классические уязвимости signature-wrapping). Весь этот файл под //go:build
// enterprise, поэтому free-бинарь SAML-либу НЕ линкует (см. go list -deps).
//
// Матчинг идентичности — как у OIDC (sso_enterprise.go): по неизменяемой паре (issuer=IdP
// EntityID, subject=NameID), НИКОГДА по email. Провижининг/роли/коллизии — тот же контракт,
// те же ошибки ErrSSO*. Discovery IdP-метаданных ленивый (первый запрос, не старт): недоступный
// на буте IdP не роняет сервер.

const (
	samlACSPath       = "/api/v1/auth/saml/acs"
	samlMetadataPath  = "/api/v1/auth/saml/metadata"
	samlFlowCookieTTL = 10 * time.Minute
)

// samlHTTPClient — таймаутный клиент для разовой загрузки IdP-метаданных (ensure). Держим
// коротким, чтобы залипший metadata-URL не тормозил первый /login надолго.
var samlHTTPClient = &http.Client{Timeout: 10 * time.Second}

// Дефолтные имена SAML-атрибутов для email/имени (сверх настраиваемых SAML_EMAIL_ATTRIBUTE/
// SAML_NAME_ATTRIBUTE). Матчинг по Attribute.Name ИЛИ FriendlyName, case-insensitive.
var (
	defaultSAMLEmailAttrs = []string{
		"email", "emailaddress", "mail", "user.email",
		"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress",
		"urn:oid:0.9.2342.19200300.100.1.3",
	}
	defaultSAMLNameAttrs = []string{
		"name", "displayname", "cn",
		"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name",
		"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/displayname",
		"urn:oid:2.16.840.1.113730.3.1.241", "urn:oid:2.5.4.3",
	}
)

type samlConfig struct {
	MetadataURL string // SAML_IDP_METADATA_URL — предпочтительно (ADFS/Okta/Azure публикуют)
	IdPSSOURL   string // SAML_IDP_SSO_URL — альтернатива метадате (вместе с IdPCertPEM)
	IdPCertPEM  string // SAML_IDP_CERT — PEM/base64/путь к подписному серту IdP (для cert-режима)
	IdPEntityID string // SAML_IDP_ENTITY_ID — EntityID IdP в cert-режиме (по умолч. = IdPSSOURL)
	SPEntityID  string // SAML_SP_ENTITY_ID (по умолч. = PUBLIC_WEB_URL)
	EmailAttr   string // SAML_EMAIL_ATTRIBUTE — доп. имя атрибута email
	NameAttr    string // SAML_NAME_ATTRIBUTE — доп. имя атрибута отображаемого имени
	GroupsAttr  string // SAML_GROUPS_ATTRIBUTE — имя атрибута групп (по умолч. "groups")
	AdminValues map[string]bool
	DefaultRole string
	AllowJIT    bool
}

type samlProvider struct {
	db           *storage.DB
	mgr          *license.Manager
	publicWebURL string
	cookieSecure bool
	cfg          samlConfig

	mu sync.Mutex
	sp *saml.ServiceProvider
}

// NewSAMLProvider читает SAML_* env и возвращает провайдер (без сетевых обращений — discovery
// ленивый). Не сконфигурирован (ни SAML_IDP_METADATA_URL, ни пары SSO_URL+CERT) → Enabled()=false.
func NewSAMLProvider(db *storage.DB, mgr *license.Manager, publicWebURL string, cookieSecure bool) SAMLProvider {
	admin := map[string]bool{}
	for _, v := range strings.Split(os.Getenv("SAML_ADMIN_VALUES"), ",") {
		if v = strings.TrimSpace(v); v != "" {
			admin[v] = true
		}
	}
	defRole := strings.TrimSpace(os.Getenv("SAML_DEFAULT_ROLE"))
	if defRole == "it_admin" {
		// Инвариант «it_admin по умолчанию НИКОГДА» (как у OIDC): дефолт применяется к юзерам,
		// НЕ доказавшим admin-группу — значит не может быть it_admin. Клампим в viewer.
		slog.Warn("SAML_DEFAULT_ROLE=it_admin запрещён (it_admin только через SAML_ADMIN_VALUES) — использую viewer")
		defRole = "viewer"
	} else if !validRoles[defRole] {
		defRole = "viewer" // least privilege по умолчанию
	}
	allowJIT := true
	if v := strings.TrimSpace(os.Getenv("SAML_ALLOW_JIT")); v != "" {
		allowJIT = v == "true"
	}
	spEntityID := strings.TrimSpace(os.Getenv("SAML_SP_ENTITY_ID"))
	if spEntityID == "" {
		spEntityID = publicWebURL // стабильный дефолт: SP EntityID = адрес деплоя
	}
	groupsAttr := strings.TrimSpace(os.Getenv("SAML_GROUPS_ATTRIBUTE"))
	if groupsAttr == "" {
		groupsAttr = "groups"
	}
	return &samlProvider{
		db: db, mgr: mgr, publicWebURL: publicWebURL, cookieSecure: cookieSecure,
		cfg: samlConfig{
			MetadataURL: strings.TrimSpace(os.Getenv("SAML_IDP_METADATA_URL")),
			IdPSSOURL:   strings.TrimSpace(os.Getenv("SAML_IDP_SSO_URL")),
			IdPCertPEM:  os.Getenv("SAML_IDP_CERT"),
			IdPEntityID: strings.TrimSpace(os.Getenv("SAML_IDP_ENTITY_ID")),
			SPEntityID:  spEntityID,
			EmailAttr:   strings.TrimSpace(os.Getenv("SAML_EMAIL_ATTRIBUTE")),
			NameAttr:    strings.TrimSpace(os.Getenv("SAML_NAME_ATTRIBUTE")),
			GroupsAttr:  groupsAttr,
			AdminValues: admin,
			DefaultRole: defRole,
			AllowJIT:    allowJIT,
		},
	}
}

func (p *samlProvider) configured() bool {
	return p.cfg.MetadataURL != "" || (p.cfg.IdPSSOURL != "" && p.cfg.IdPCertPEM != "")
}

func (p *samlProvider) Enabled() bool {
	return p.configured() && p.mgr.Has(license.FeatureSSO)
}

// ensure лениво строит crewjam ServiceProvider (загрузка IdP-метаданных / сборка из серта). При
// ошибке (IdP недоступен) НЕ кэширует — повторит на следующем запросе (recover, когда IdP поднимется).
func (p *samlProvider) ensure(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sp != nil {
		return nil
	}
	var idpMeta *saml.EntityDescriptor
	if p.cfg.MetadataURL != "" {
		md, err := fetchIDPMetadata(ctx, p.cfg.MetadataURL)
		if err != nil {
			return err
		}
		idpMeta = md
	} else {
		cert, err := parseIDPCert(p.cfg.IdPCertPEM)
		if err != nil {
			return err
		}
		entityID := p.cfg.IdPEntityID
		if entityID == "" {
			entityID = p.cfg.IdPSSOURL
		}
		idpMeta = idpEntityDescriptorFromCert(entityID, p.cfg.IdPSSOURL, cert)
	}
	acsURL, err := url.Parse(p.publicWebURL + samlACSPath)
	if err != nil {
		return err
	}
	metaURL, err := url.Parse(p.publicWebURL + samlMetadataPath)
	if err != nil {
		return err
	}
	p.sp = &saml.ServiceProvider{
		EntityID:          p.cfg.SPEntityID,
		AcsURL:            *acsURL,
		MetadataURL:       *metaURL,
		IDPMetadata:       idpMeta,
		AuthnNameIDFormat: saml.UnspecifiedNameIDFormat, // максимально совместимо с IdP
		AllowIDPInitiated: false,                        // только SP-initiated (сверяем InResponseTo)
	}
	return nil
}

// fetchIDPMetadata загружает и парсит IdP-метаданные (samlsp.ParseMetadata: xrv-валидация +
// EntityDescriptor/EntitiesDescriptor). Тело ограничено 1 МБ.
func fetchIDPMetadata(ctx context.Context, metadataURL string) (*saml.EntityDescriptor, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := samlHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("IdP metadata: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return samlsp.ParseMetadata(data)
}

// parseIDPCert принимает подписной серт IdP как путь к файлу, PEM-строку или голый base64(DER).
func parseIDPCert(s string) (*x509.Certificate, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("SAML_IDP_CERT пуст")
	}
	if b, err := os.ReadFile(s); err == nil { // путь к файлу
		s = strings.TrimSpace(string(b))
	}
	if block, _ := pem.Decode([]byte(s)); block != nil {
		return x509.ParseCertificate(block.Bytes)
	}
	der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		return nil, fmt.Errorf("SAML_IDP_CERT: не PEM и не base64 DER: %w", err)
	}
	return x509.ParseCertificate(der)
}

// idpEntityDescriptorFromCert собирает минимальные IdP-метаданные из (entityID, SSO URL, серт)
// для cert-режима — эквивалент metadata-URL, но без сети.
func idpEntityDescriptorFromCert(entityID, ssoURL string, cert *x509.Certificate) *saml.EntityDescriptor {
	return &saml.EntityDescriptor{
		EntityID: entityID,
		IDPSSODescriptors: []saml.IDPSSODescriptor{{
			SSODescriptor: saml.SSODescriptor{
				RoleDescriptor: saml.RoleDescriptor{
					ProtocolSupportEnumeration: "urn:oasis:names:tc:SAML:2.0:protocol",
					KeyDescriptors: []saml.KeyDescriptor{{
						Use: "signing",
						KeyInfo: saml.KeyInfo{
							X509Data: saml.X509Data{
								X509Certificates: []saml.X509Certificate{{
									Data: base64.StdEncoding.EncodeToString(cert.Raw),
								}},
							},
						},
					}},
				},
			},
			SingleSignOnServices: []saml.Endpoint{
				{Binding: saml.HTTPRedirectBinding, Location: ssoURL},
				{Binding: saml.HTTPPostBinding, Location: ssoURL},
			},
		}},
	}
}

func (p *samlProvider) flowCookieName() string {
	if p.cookieSecure {
		return "__Host-saml_flow"
	}
	return "saml_flow"
}

// flowCookie: в проде SameSite=None+Secure — иначе кросс-сайтовый POST от IdP на ACS не доставит
// куку (Lax шлётся только на top-level GET). В dev (http) None+Secure браузер отверг бы → Lax.
func (p *samlProvider) flowCookie(value string, maxAge int) *http.Cookie {
	ss := http.SameSiteLaxMode
	if p.cookieSecure {
		ss = http.SameSiteNoneMode
	}
	return &http.Cookie{
		Name: p.flowCookieName(), Value: value, Path: "/",
		HttpOnly: true, Secure: p.cookieSecure, SameSite: ss, MaxAge: maxAge,
	}
}

func (p *samlProvider) redirectErr(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, p.publicWebURL+"/login?sso_error="+code, http.StatusFound)
}

// Metadata отдаёт SP EntityDescriptor (XML) для регистрации в IdP.
func (p *samlProvider) Metadata(w http.ResponseWriter, r *http.Request) {
	if err := p.ensure(r.Context()); err != nil {
		slog.Warn("saml: idp metadata unavailable", "err", err)
		p.redirectErr(w, r, "idp_unavailable")
		return
	}
	buf, err := xml.MarshalIndent(p.sp.Metadata(), "", "  ")
	if err != nil {
		slog.Error("saml: marshal SP metadata", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml")
	_, _ = w.Write(buf)
}

// Login: генерирует AuthnRequest (HTTP-Redirect binding), кладёт RelayState+request-id в
// sso_auth_flows (single-use) и редиректит на IdP.
func (p *samlProvider) Login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := p.ensure(ctx); err != nil {
		slog.Warn("saml: idp metadata unavailable", "err", err)
		p.redirectErr(w, r, "idp_unavailable")
		return
	}
	ssoURL := p.sp.GetSSOBindingLocation(saml.HTTPRedirectBinding)
	if ssoURL == "" {
		slog.Error("saml: IdP has no HTTP-Redirect SSO binding")
		p.redirectErr(w, r, "idp_unavailable")
		return
	}
	authnReq, err := p.sp.MakeAuthenticationRequest(ssoURL, saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		slog.Error("saml: make authn request", "err", err)
		p.redirectErr(w, r, "verify_failed")
		return
	}
	relayState := randToken()
	// Переиспользуем sso_auth_flows: state=RelayState, nonce=AuthnRequest ID (для сверки
	// InResponseTo), pkce_verifier='' (в SAML нет PKCE). Single-use как у OIDC.
	if err := p.db.InsertSSOFlow(ctx, relayState, authnReq.ID, ""); err != nil {
		slog.Error("saml: insert flow", "err", err)
		p.redirectErr(w, r, "verify_failed")
		return
	}
	http.SetCookie(w, p.flowCookie(relayState, int(samlFlowCookieTTL.Seconds())))
	redirectURL, err := authnReq.Redirect(relayState, p.sp)
	if err != nil {
		slog.Error("saml: build redirect", "err", err)
		p.redirectErr(w, r, "verify_failed")
		return
	}
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

// ACS принимает POST SAMLResponse, валидирует его через crewjam/saml и возвращает SSOResult.
func (p *samlProvider) ACS(w http.ResponseWriter, r *http.Request) (*SSOResult, error) {
	ctx := r.Context()
	defer http.SetCookie(w, p.flowCookie("", -1)) // гасим flow-куку при любом исходе
	if err := p.ensure(ctx); err != nil {
		return nil, ErrSSOIdPUnavailable
	}
	if err := r.ParseForm(); err != nil {
		return nil, ErrSSOVerifyFailed
	}
	relayState := r.PostForm.Get("RelayState")
	if relayState == "" {
		return nil, ErrSSOVerifyFailed
	}
	// CSRF/browser-binding (best-effort): в проде кука SameSite=None+Secure доезжает с cross-site
	// POST от IdP и ОБЯЗАНА совпасть с RelayState; в dev (Lax) её может не быть — тогда полагаемся
	// на single-use flow + подписанный InResponseTo (их достаточно против replay).
	if ck, err := r.Cookie(p.flowCookieName()); err == nil && ck.Value != "" && ck.Value != relayState {
		return nil, ErrSSOVerifyFailed
	}
	// Single-use: забираем строку флоу по RelayState → AuthnRequest ID (nonce-колонка). Replay/
	// гонка/протухание → !ok.
	requestID, _, ok, err := p.db.ConsumeSSOFlow(ctx, relayState)
	if err != nil || !ok {
		return nil, ErrSSOVerifyFailed
	}
	// crewjam/saml валидирует ВСЁ: XML-подпись ассершна (по IdP-серту из метаданных), Issuer==IdP
	// EntityID, NotBefore/NotOnOrAfter, Conditions, AudienceRestriction==SP EntityID, Recipient==
	// ACS URL и InResponseTo ∈ possibleRequestIDs. Свою XML-подпись/canonicalization НЕ пишем.
	assertion, err := p.sp.ParseResponse(r, []string{requestID})
	if err != nil {
		return nil, ErrSSOVerifyFailed
	}
	if assertion.Subject == nil || assertion.Subject.NameID == nil {
		return nil, ErrSSOVerifyFailed
	}
	subject := strings.TrimSpace(assertion.Subject.NameID.Value)
	if subject == "" {
		return nil, ErrSSOVerifyFailed
	}
	// issuer = IdP EntityID из метаданных (== assertion.Issuer.Value, это уже сверил crewjam).
	issuer := p.sp.IDPMetadata.EntityID
	email, name, groups := p.attributesFromAssertion(assertion)
	if email == "" {
		return nil, ErrSSOVerifyFailed
	}
	return p.provision(ctx, issuer, subject, email, name, groups)
}

// attributesFromAssertion достаёт email (display), имя и группы из ассершна. email: настраиваемый
// SAML_EMAIL_ATTRIBUTE + дефолтные имена, иначе NameID если он похож на email.
func (p *samlProvider) attributesFromAssertion(a *saml.Assertion) (email, name string, groups []string) {
	emailNames := append([]string{p.cfg.EmailAttr}, defaultSAMLEmailAttrs...)
	if v := attrLookup(a, emailNames...); len(v) > 0 {
		email = strings.ToLower(strings.TrimSpace(v[0]))
	}
	if email == "" && a.Subject != nil && a.Subject.NameID != nil {
		if nid := strings.TrimSpace(a.Subject.NameID.Value); strings.Contains(nid, "@") {
			email = strings.ToLower(nid)
		}
	}
	nameNames := append([]string{p.cfg.NameAttr}, defaultSAMLNameAttrs...)
	if v := attrLookup(a, nameNames...); len(v) > 0 {
		name = strings.TrimSpace(v[0])
	}
	if name == "" {
		name = email
	}
	groups = attrLookup(a, p.cfg.GroupsAttr)
	return email, name, groups
}

// attrLookup собирает значения всех атрибутов ассершна, чьё Name ИЛИ FriendlyName (lowercased)
// входит в names. Пустые имена/значения игнорируются.
func attrLookup(a *saml.Assertion, names ...string) []string {
	want := map[string]bool{}
	for _, n := range names {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			want[n] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	var out []string
	for _, stmt := range a.AttributeStatements {
		for _, attr := range stmt.Attributes {
			if want[strings.ToLower(strings.TrimSpace(attr.Name))] ||
				want[strings.ToLower(strings.TrimSpace(attr.FriendlyName))] {
				for _, v := range attr.Values {
					if s := strings.TrimSpace(v.Value); s != "" {
						out = append(out, s)
					}
				}
			}
		}
	}
	return out
}

// provision — тот же контракт, что OIDC-Callback (sso_enterprise.go): матч по (issuer,subject),
// отказ авто-линка при email-коллизии, JIT viewer по умолчанию.
func (p *samlProvider) provision(ctx context.Context, issuer, subject, email, name string, groups []string) (*SSOResult, error) {
	// (а) матч по неизменяемой (issuer=EntityID, subject=NameID).
	if existing, err := p.db.GetUserBySAMLIdentity(ctx, issuer, subject); err != nil {
		return nil, ErrSSOVerifyFailed
	} else if existing != nil {
		role := p.resolveRole(ctx, existing, groups)
		return &SSOResult{UserID: existing.ID, Email: existing.Email, Role: role}, nil
	}
	// (б) коллизия email с ЛЮБЫМ существующим аккаунтом → отказ авто-линка (case-insensitive).
	if collide, err := p.db.GetUserByEmailCI(ctx, email); err != nil {
		return nil, ErrSSOVerifyFailed
	} else if collide != nil {
		return nil, ErrSSOEmailConflict
	}
	// (в) JIT-провижининг.
	if !p.cfg.AllowJIT {
		return nil, ErrSSONoAccount
	}
	created, err := p.db.CreateSAMLUser(ctx, name, email, p.initialRole(groups), issuer, subject)
	if err != nil {
		if errors.Is(err, storage.ErrSSOEmailTaken) {
			return nil, ErrSSOEmailConflict // гонка на email → коллизия
		}
		return nil, ErrSSOVerifyFailed
	}
	if created == nil { // defense-in-depth: не разыменовываем nil
		return nil, ErrSSOVerifyFailed
	}
	return &SSOResult{UserID: created.ID, Email: created.Email, Role: created.Role}, nil
}

// roleManaged: SAML управляет ролью только когда задано имя атрибута групп И непустой allowlist
// admin-значений. Иначе роль SSO не пересчитывает (JIT получает DefaultRole).
func (p *samlProvider) roleManaged() bool {
	return p.cfg.GroupsAttr != "" && len(p.cfg.AdminValues) > 0
}

// roleFromGroups: значение ∈ AdminValues → it_admin, иначе viewer. present=false, если групп нет
// (fail-closed: не понижаем существующего юзера). it_admin по умолчанию НИКОГДА.
func (p *samlProvider) roleFromGroups(groups []string) (role string, present bool) {
	if len(groups) == 0 {
		return "", false
	}
	for _, g := range groups {
		if p.cfg.AdminValues[strings.TrimSpace(g)] {
			return "it_admin", true
		}
	}
	return "viewer", true
}

// resolveRole пересчитывает роль существующего SAML-юзера из групп. Fail-closed: управление
// включено, но групп в ассершне нет → НЕ понижаем (оставляем текущую).
func (p *samlProvider) resolveRole(ctx context.Context, u *storage.User, groups []string) string {
	if !p.roleManaged() {
		return u.Role
	}
	newRole, present := p.roleFromGroups(groups)
	if !present {
		slog.Warn("saml: groups attr configured but absent in assertion; keeping current role", "user", u.ID)
		return u.Role
	}
	if newRole != u.Role {
		if err := p.db.UpdateUserRole(ctx, u.ID, newRole); err != nil {
			slog.Error("saml: update role failed", "user", u.ID, "err", err)
			return u.Role
		}
	}
	return newRole
}

// initialRole — роль JIT-юзера: из групп, иначе SAML_DEFAULT_ROLE.
func (p *samlProvider) initialRole(groups []string) string {
	if !p.roleManaged() {
		return p.cfg.DefaultRole
	}
	if role, present := p.roleFromGroups(groups); present {
		return role
	}
	return p.cfg.DefaultRole
}
