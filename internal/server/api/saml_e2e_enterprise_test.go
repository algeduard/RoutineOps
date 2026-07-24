//go:build enterprise

package api_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/xml"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/crewjam/saml"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/Floodww/RoutineOps/internal/server/api"
	"github.com/Floodww/RoutineOps/internal/server/mailer"
	"github.com/Floodww/RoutineOps/internal/server/storage"
)

const samlTestPublicURL = "https://app.local"

func mustURL(t *testing.T, raw string) url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return *u
}

// newMockSAMLIdP поднимает httptest-IdP, отдающий валидные SAML-метаданные (crewjam
// IdentityProvider.Metadata) с самоподписанным подписным сертом. Возвращает URL метаданных.
func newMockSAMLIdP(t *testing.T) (metadataURL string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mock-saml-idp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	srv := httptest.NewUnstartedServer(mux)
	base := "http://" + srv.Listener.Addr().String()
	idp := &saml.IdentityProvider{
		Key:         priv,
		Certificate: cert,
		MetadataURL: mustURL(t, base+"/metadata"),
		SSOURL:      mustURL(t, base+"/sso"),
	}
	mux.HandleFunc("/metadata", func(w http.ResponseWriter, _ *http.Request) {
		buf, err := xml.MarshalIndent(idp.Metadata(), "", "  ")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/samlmetadata+xml")
		_, _ = w.Write(buf)
	})
	srv.Start()
	t.Cleanup(srv.Close)
	return base + "/metadata"
}

// samlRouter собирает роутер с SAML-провайдером под licensed-менеджером и заданным IdP.
func samlRouter(t *testing.T, metadataURL string) (http.Handler, api.SAMLProvider, *storage.DB) {
	t.Helper()
	t.Setenv("SAML_IDP_METADATA_URL", metadataURL)
	db := newTestDB(t)
	mgr := licensedManager(t, []string{license.FeatureSSO})
	p := api.NewSAMLProvider(db, mgr, samlTestPublicURL, false)
	rtr := api.NewRouter(db, nil, []byte("test-secret"), nil, samlTestPublicURL, t.TempDir(),
		mailer.New("", "", "", "", "", false), false, api.WithSAML(p))
	return rtr, p, db
}

// Статус включён, когда сконфигурирован IdP И покрыт лицензией.
func TestSAMLStatusEnabled(t *testing.T) {
	rtr, _, _ := samlRouter(t, newMockSAMLIdP(t))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/saml/status", nil)
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("status: code=%d body=%s", w.Code, w.Body)
	}
}

// SP-метаданные отдаются и содержат наш EntityID и ACS URL (exercises ensure→загрузка/парс
// IdP-метаданных + сборка ServiceProvider + маршалинг SP-метаданных).
func TestSAMLMetadataServed(t *testing.T) {
	rtr, _, _ := samlRouter(t, newMockSAMLIdP(t))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/saml/metadata", nil)
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("metadata: got %d, body %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if !strings.Contains(body, `entityID="`+samlTestPublicURL+`"`) {
		t.Errorf("SP metadata без нашего EntityID: %s", body)
	}
	if !strings.Contains(body, samlTestPublicURL+"/api/v1/auth/saml/acs") {
		t.Errorf("SP metadata без ACS URL: %s", body)
	}
	// Должен быть валидным XML (round-trip в EntityDescriptor).
	var ed saml.EntityDescriptor
	if err := xml.Unmarshal(w.Body.Bytes(), &ed); err != nil {
		t.Fatalf("SP metadata не парсится как EntityDescriptor: %v", err)
	}
}

// Login редиректит на SSO IdP c AuthnRequest, ставит flow-куку и кладёт single-use строку флоу
// (state=RelayState, nonce=AuthnRequest ID). Проверяем весь этот шов.
func TestSAMLLoginRedirect(t *testing.T) {
	rtr, _, db := samlRouter(t, newMockSAMLIdP(t))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/saml/login", nil)
	w := httptest.NewRecorder()
	rtr.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("login: got %d, want 302; body %s", w.Code, w.Body)
	}
	loc := w.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(u.Path, "/sso") {
		t.Errorf("redirect не на IdP SSO: %s", loc)
	}
	if u.Query().Get("SAMLRequest") == "" {
		t.Errorf("нет SAMLRequest в redirect: %s", loc)
	}
	relayState := u.Query().Get("RelayState")
	if relayState == "" {
		t.Fatalf("нет RelayState в redirect: %s", loc)
	}
	// flow-кука установлена.
	var hasFlowCookie bool
	for _, c := range w.Result().Cookies() {
		if strings.Contains(c.Name, "saml_flow") && c.Value == relayState {
			hasFlowCookie = true
		}
	}
	if !hasFlowCookie {
		t.Error("login не поставил flow-куку == RelayState")
	}
	// Строка флоу существует и single-use: nonce == AuthnRequest ID (непустой).
	reqID, _, ok, err := db.ConsumeSSOFlow(r.Context(), relayState)
	if err != nil || !ok {
		t.Fatalf("flow row по RelayState отсутствует: ok=%v err=%v", ok, err)
	}
	if reqID == "" {
		t.Error("nonce-колонка (AuthnRequest ID) пуста")
	}
}
