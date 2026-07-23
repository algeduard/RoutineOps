//go:build enterprise

package siem

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

func quietExporter() *Exporter {
	return NewExporter(nil, func() bool { return true }, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestExporterPostSignsAndDelivers(t *testing.T) {
	var gotSig, gotUA string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-RoutineOps-Signature")
		gotUA = r.Header.Get("User-Agent")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	e := quietExporter()
	entries := []storage.AuditEntry{{Seq: 1, Action: "apply_license", UserEmail: "a@b"}}
	if err := e.post(context.Background(), srv.URL, "sekret", entries); err != nil {
		t.Fatalf("post: %v", err)
	}
	// HMAC-SHA256 тела с секретом.
	mac := hmac.New(sha256.New, []byte("sekret"))
	mac.Write(gotBody)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != want {
		t.Fatalf("подпись %q != %q", gotSig, want)
	}
	if gotUA != "RoutineOps-SIEM-Export" {
		t.Errorf("User-Agent = %q", gotUA)
	}
	// Тело содержит событие.
	if !strings.Contains(string(gotBody), "apply_license") {
		t.Errorf("тело не содержит событие: %s", gotBody)
	}
}

func TestExporterPostNoSecretNoSignature(t *testing.T) {
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-RoutineOps-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := quietExporter().post(context.Background(), srv.URL, "", []storage.AuditEntry{{Seq: 1}}); err != nil {
		t.Fatal(err)
	}
	if gotSig != "" {
		t.Errorf("без секрета подписи быть не должно, got %q", gotSig)
	}
}

func TestExporterPostNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := quietExporter().post(context.Background(), srv.URL, "", []storage.AuditEntry{{Seq: 1}}); err == nil {
		t.Fatal("на 500 ожидали ошибку (курсор не двинется)")
	}
}
