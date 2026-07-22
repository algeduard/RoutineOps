//go:build enterprise

package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func issueFor(t *testing.T, priv ed25519.PrivateKey, c Claims) string {
	t.Helper()
	blob, err := Issue(c, priv)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

// Применённая через Apply лицензия ложится на диск и поднимается новым Manager на старте
// БЕЗ пароля (доверяем своему диску — активация уже состоялась).
func TestManagerPersistAndReload(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	salt, hash, _ := HashPassword("pw")
	blob := issueFor(t, priv, Claims{
		Licensee: "ACME", Edition: "enterprise",
		ExpiresAt: time.Now().Add(24 * time.Hour), PwSalt: salt, PwHash: hash,
	})
	path := filepath.Join(t.TempDir(), "lic.blob")

	m1 := NewManager(pub, 0, path)
	st, err := m1.Apply(blob, "pw")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if st.PersistWarning != "" {
		t.Fatalf("persist warning: %q", st.PersistWarning)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("файл лицензии не создан: %v", err)
	}

	// Новый Manager (как рестарт сервера) — грузит с диска без пароля.
	m2 := NewManager(pub, 0, path)
	m2.LoadInitial("", "", quietLogger())
	if s := m2.Status(); !s.Configured || !s.Valid || s.Licensee != "ACME" {
		t.Fatalf("reload: %+v", s)
	}
}

// Feature-gate: Has true только для покрытой фичи И только пока лицензия в сроке.
func TestManagerHasFeatureGate(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

	// Активная лицензия с ограниченным списком фич.
	blob := issueFor(t, priv, Claims{
		Features: []string{"sso"}, NotBefore: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	})
	m := NewManager(pub, 0, "")
	m.now = func() time.Time { return now }
	if _, err := m.Apply(blob, ""); err != nil {
		t.Fatal(err)
	}
	if !m.Has("sso") {
		t.Errorf("должна быть sso")
	}
	if m.Has("scim") {
		t.Errorf("scim не в лицензии — Has должен быть false")
	}

	// Та же лицензия, но часы за пределами срока → все фичи выключены.
	m.now = func() time.Time { return now.Add(2 * time.Hour) }
	if m.Has("sso") {
		t.Errorf("истёкшая лицензия не должна включать фичи")
	}

	// Без лицензии — ничего.
	empty := NewManager(pub, 0, "")
	if empty.Has("sso") {
		t.Errorf("без лицензии Has должен быть false")
	}
}

// Деактивация удаляет файл с диска (чтобы рестарт не вернул лицензию).
func TestManagerDeactivateRemovesFile(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	blob := issueFor(t, priv, Claims{Edition: "enterprise", ExpiresAt: time.Now().Add(24 * time.Hour)})
	path := filepath.Join(t.TempDir(), "lic.blob")

	m := NewManager(pub, 0, path)
	if _, err := m.Apply(blob, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("файл должен существовать: %v", err)
	}
	if st := m.Deactivate(); st.Configured || st.PersistWarning != "" {
		t.Fatalf("после деактивации: %+v", st)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("файл лицензии должен быть удалён, err=%v", err)
	}
}

// Без корня доверия (pub == nil) Apply отвергает любую лицензию.
func TestManagerNilPubKeyRejects(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	blob := issueFor(t, priv, Claims{Edition: "enterprise"})
	m := NewManager(nil, 0, "")
	if _, err := m.Apply(blob, ""); err == nil {
		t.Fatal("Apply без pub должен падать")
	}
	if m.Status().Configured {
		t.Fatal("статус не должен быть configured")
	}
}

// Регресс на находку ревью: при конкурентных Apply/Deactivate диск НЕ должен расходиться
// с памятью (иначе деактивированная лицензия воскресала бы после рестарта). Гонять под -race.
func TestManagerLifecycleRaceConsistency(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	blob := issueFor(t, priv, Claims{Edition: "enterprise", ExpiresAt: time.Now().Add(24 * time.Hour)})
	path := filepath.Join(t.TempDir(), "lic.blob")
	m := NewManager(pub, 0, path)

	for round := 0; round < 30; round++ {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			apply := i%2 == 0
			wg.Add(1)
			go func(apply bool) {
				defer wg.Done()
				if apply {
					_, _ = m.Apply(blob, "")
				} else {
					m.Deactivate()
				}
			}(apply)
		}
		wg.Wait()
		// Инвариант: файл на диске существует тогда и только тогда, когда в памяти лицензия.
		_, statErr := os.Stat(path)
		fileExists := statErr == nil
		if m.Status().Configured != fileExists {
			t.Fatalf("round %d: mem.Configured=%v, файл=%v — диск разошёлся с памятью",
				round, m.Status().Configured, fileExists)
		}
	}
}

// Персист выключен (path == "") → Apply работает live, но с persist_warning.
func TestManagerNoPersistPathWarns(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	blob := issueFor(t, priv, Claims{Edition: "enterprise", ExpiresAt: time.Now().Add(24 * time.Hour)})
	m := NewManager(pub, 0, "")
	st, err := m.Apply(blob, "")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Configured || st.PersistWarning == "" {
		t.Fatalf("ожидали configured + persist_warning: %+v", st)
	}
}
