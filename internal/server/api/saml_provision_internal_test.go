//go:build enterprise

package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crewjam/saml"
)

// samlProviderFor — минимальный провайдер для юнит-тестов чистой логики (без DB/сети).
func samlProviderFor(groupsAttr string, admin map[string]bool, defRole string) *samlProvider {
	return &samlProvider{cfg: samlConfig{
		GroupsAttr:  groupsAttr,
		AdminValues: admin,
		DefaultRole: defRole,
	}}
}

// Извлечение email/имени/групп из синтетического ассершна: email из атрибута (lowercased),
// имя из displayName, группы — все значения атрибута groups.
func TestSAMLAttributesFromAssertion(t *testing.T) {
	p := samlProviderFor("groups", nil, "viewer")
	a := &saml.Assertion{
		Subject: &saml.Subject{NameID: &saml.NameID{Value: "nameid-123"}},
		AttributeStatements: []saml.AttributeStatement{{
			Attributes: []saml.Attribute{
				{Name: "email", Values: []saml.AttributeValue{{Value: "Alice@Corp.Example"}}},
				{FriendlyName: "displayName", Name: "urn:oid:2.16.840.1.113730.3.1.241", Values: []saml.AttributeValue{{Value: "Alice A"}}},
				{Name: "groups", Values: []saml.AttributeValue{{Value: "routineops-admins"}, {Value: "staff"}}},
			},
		}},
	}
	email, name, groups := p.attributesFromAssertion(a)
	if email != "alice@corp.example" {
		t.Errorf("email = %q, want alice@corp.example (lowercased)", email)
	}
	if name != "Alice A" {
		t.Errorf("name = %q, want Alice A", name)
	}
	if len(groups) != 2 || groups[0] != "routineops-admins" || groups[1] != "staff" {
		t.Errorf("groups = %v", groups)
	}
}

// Нет атрибута email, но NameID похож на email → email берётся из NameID (fallback).
func TestSAMLEmailFallbackToNameID(t *testing.T) {
	p := samlProviderFor("groups", nil, "viewer")
	a := &saml.Assertion{
		Subject:             &saml.Subject{NameID: &saml.NameID{Value: "Bob@Corp.Example"}},
		AttributeStatements: []saml.AttributeStatement{{Attributes: []saml.Attribute{{Name: "groups", Values: []saml.AttributeValue{{Value: "staff"}}}}}},
	}
	email, name, _ := p.attributesFromAssertion(a)
	if email != "bob@corp.example" {
		t.Errorf("email fallback = %q, want bob@corp.example", email)
	}
	if name != email { // имя не задано → падаем на email
		t.Errorf("name = %q, want %q", name, email)
	}
}

// Маппинг ролей из групп: allowlist → it_admin, иначе viewer; пусто → present=false (fail-closed).
func TestSAMLRoleFromGroups(t *testing.T) {
	p := samlProviderFor("groups", map[string]bool{"routineops-admins": true}, "viewer")

	if role, present := p.roleFromGroups([]string{"staff", "routineops-admins"}); !present || role != "it_admin" {
		t.Errorf("admin group → (%q,%v), want (it_admin,true)", role, present)
	}
	if role, present := p.roleFromGroups([]string{"staff"}); !present || role != "viewer" {
		t.Errorf("non-admin group → (%q,%v), want (viewer,true)", role, present)
	}
	if _, present := p.roleFromGroups(nil); present {
		t.Error("пустые группы → present должен быть false (fail-closed)")
	}

	// initialRole: из групп, иначе DefaultRole.
	if r := p.initialRole([]string{"routineops-admins"}); r != "it_admin" {
		t.Errorf("initialRole(admin) = %q, want it_admin", r)
	}
	if r := p.initialRole(nil); r != "viewer" {
		t.Errorf("initialRole(none) = %q, want viewer (default)", r)
	}
}

// Пустой allowlist admin-значений → роль SSO не управляется (JIT всегда DefaultRole, даже с группой).
func TestSAMLRoleNotManagedWithoutAdminValues(t *testing.T) {
	p := samlProviderFor("groups", nil, "viewer")
	if p.roleManaged() {
		t.Error("roleManaged должен быть false при пустом AdminValues")
	}
	if r := p.initialRole([]string{"whatever"}); r != "viewer" {
		t.Errorf("initialRole без allowlist = %q, want viewer", r)
	}
}

// NewSAMLProvider: клампы и дефолты конфига из env.
func TestNewSAMLProviderConfig(t *testing.T) {
	t.Setenv("SAML_IDP_METADATA_URL", "https://idp.example/metadata")
	t.Setenv("SAML_DEFAULT_ROLE", "it_admin") // должен клампиться в viewer
	t.Setenv("SAML_ALLOW_JIT", "false")
	// SAML_SP_ENTITY_ID и SAML_GROUPS_ATTRIBUTE не заданы → дефолты.

	p, ok := NewSAMLProvider(nil, nil, "https://sp.example", true).(*samlProvider)
	if !ok {
		t.Fatal("NewSAMLProvider вернул не *samlProvider")
	}
	if p.cfg.DefaultRole != "viewer" {
		t.Errorf("SAML_DEFAULT_ROLE=it_admin должен клампиться в viewer, got %q", p.cfg.DefaultRole)
	}
	if p.cfg.SPEntityID != "https://sp.example" {
		t.Errorf("SPEntityID по умолчанию = publicWebURL, got %q", p.cfg.SPEntityID)
	}
	if p.cfg.GroupsAttr != "groups" {
		t.Errorf("GroupsAttr по умолчанию = groups, got %q", p.cfg.GroupsAttr)
	}
	if p.cfg.AllowJIT {
		t.Error("SAML_ALLOW_JIT=false → AllowJIT должен быть false")
	}
	if !p.configured() {
		t.Error("configured() должен быть true при заданном metadata URL")
	}
}

// parseIDPCert принимает PEM, голый base64(DER) и путь к файлу.
func TestParseIDPCert(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "idp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	b64 := base64.StdEncoding.EncodeToString(der)

	for name, in := range map[string]string{"pem": pemStr, "base64": b64} {
		got, err := parseIDPCert(in)
		if err != nil || got == nil {
			t.Fatalf("%s: parseIDPCert err=%v", name, err)
		}
		if !got.Equal(mustParseCert(t, der)) {
			t.Errorf("%s: серт не совпал", name)
		}
	}

	// Путь к файлу.
	f := filepath.Join(t.TempDir(), "idp.pem")
	if err := os.WriteFile(f, []byte(pemStr), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := parseIDPCert(f); err != nil || got == nil {
		t.Fatalf("file: parseIDPCert err=%v", err)
	}

	if _, err := parseIDPCert(""); err == nil {
		t.Error("пустой SAML_IDP_CERT должен давать ошибку")
	}
}

// idpEntityDescriptorFromCert собирает метаданные, из которых crewjam достаёт подписной серт и SSO URL.
func TestIDPEntityDescriptorFromCert(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "idp"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	cert := mustParseCert(t, der)

	ed := idpEntityDescriptorFromCert("https://idp.entity", "https://idp.example/sso", cert)
	if ed.EntityID != "https://idp.entity" {
		t.Errorf("EntityID = %q", ed.EntityID)
	}
	if len(ed.IDPSSODescriptors) != 1 {
		t.Fatalf("ожидали 1 IDPSSODescriptor, got %d", len(ed.IDPSSODescriptors))
	}
	sp := &saml.ServiceProvider{IDPMetadata: ed}
	if loc := sp.GetSSOBindingLocation(saml.HTTPRedirectBinding); loc != "https://idp.example/sso" {
		t.Errorf("SSO redirect binding location = %q", loc)
	}
	kd := ed.IDPSSODescriptors[0].KeyDescriptors
	if len(kd) != 1 || kd[0].KeyInfo.X509Data.X509Certificates[0].Data != base64.StdEncoding.EncodeToString(cert.Raw) {
		t.Errorf("подписной серт в метаданных не совпал")
	}
}

func mustParseCert(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
