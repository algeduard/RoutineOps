package lock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Контракт лок-экрана: служба пишет состояние (Manager.Lock), лок-экран читает его
// (ReadState) и после верного пароля снимает (ClearState) — всё через общий файл.
func TestReadStateAndClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.json")
	hash := bcryptHash(t, "pw")

	m := New(path, &fakeLocker{}, quietLog())
	if err := m.Lock("r1", hash, "Увольнение"); err != nil {
		t.Fatal(err)
	}

	st, err := ReadState(path)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if !st.Locked || st.Hash != hash || st.Reason != "Увольнение" || st.RequestID != "r1" {
		t.Fatalf("ReadState вернул не то: %+v", st)
	}

	if err := ClearState(path); err != nil {
		t.Fatalf("ClearState: %v", err)
	}
	st2, err := ReadState(path)
	if err != nil {
		t.Fatalf("ReadState после ClearState: %v", err)
	}
	if st2.Locked {
		t.Fatalf("после ClearState ожидали Locked=false, got %+v", st2)
	}
}

// ReadState на отсутствующем файле → os.ErrNotExist (вызывающий трактует как «не заблокировано»).
func TestReadStateNoFile(t *testing.T) {
	_, err := ReadState(filepath.Join(t.TempDir(), "нет.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ожидали ErrNotExist, got %v", err)
	}
}

// DefaultPath даёт непустой путь к lock.json (общий машинный каталог).
func TestDefaultPath(t *testing.T) {
	p := DefaultPath()
	if p == "" || !strings.HasSuffix(p, "lock.json") {
		t.Fatalf("DefaultPath = %q", p)
	}
}

// Служба замечает оффлайн-разблок (лок-экран очистил файл) → колбэк + синхронизация.
func TestDetectOfflineUnlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.json")
	fl := &fakeLocker{}
	m := New(path, fl, quietLog())
	if err := m.Lock("r1", bcryptHash(t, "pw"), "увольнение"); err != nil {
		t.Fatal(err)
	}

	if err := ClearState(path); err != nil { // имитируем разблок лок-экраном
		t.Fatal(err)
	}
	var got string
	m.detectOfflineUnlock(func(reqID, hash string) { got = reqID })

	if got != "r1" {
		t.Fatalf("onOfflineUnlock ожидали с r1, got %q", got)
	}
	if m.Locked() {
		t.Fatal("после оффлайн-разблока Manager должен быть разблокирован")
	}
	if fl.shown {
		t.Fatal("замок должен быть скрыт после оффлайн-разблока")
	}
}

// Пока файл всё ещё заблокирован — detectOfflineUnlock ничего не делает.
func TestDetectOfflineUnlock_StillLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.json")
	m := New(path, &fakeLocker{}, quietLog())
	if err := m.Lock("r1", bcryptHash(t, "pw"), "reason"); err != nil {
		t.Fatal(err)
	}
	called := false
	m.detectOfflineUnlock(func(string, string) { called = true })
	if called || !m.Locked() {
		t.Fatalf("пока заблокировано, колбэк не дёргаем: called=%v locked=%v", called, m.Locked())
	}
}
