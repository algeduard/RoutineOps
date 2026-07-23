package storage_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
)

// Единый тест: в общей тест-БД подписывает записи ТОЛЬКО этот тест (ключ задаётся здесь),
// поэтому цепочка под нашим контролем. Проверяем: чистый журнал ок; модификация ловится;
// откат чинит; усечение хвоста ловится через голову цепочки.
func TestAuditIntegrityChain(t *testing.T) {
	t.Setenv("ROUTINEOPS_AUDIT_HMAC_KEY", "test-audit-key")
	db := newDB(t)
	ctx := context.Background()

	// Конкурентная запись: FOR UPDATE головы обязан сериализовать цепочку. Если бы две
	// записи прочитали одну голову, цепочка разветвилась бы и verify нашла бы нарушение.
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = db.WriteAuditLog(ctx, "", "conc-"+uniq(t), "c"+strconv.Itoa(i), "t", "id", map[string]any{"i": i})
		}(i)
	}
	wg.Wait()
	if r, _ := db.VerifyAuditIntegrity(ctx, 0); r.Tampered {
		t.Fatalf("конкурентная запись нарушила цепочку: %+v", r)
	}

	marker := "integ-" + uniq(t)
	var seqs []int64
	for i := 0; i < 3; i++ {
		if err := db.WriteAuditLog(ctx, "", marker, "action"+strconv.Itoa(i), "device", "id"+strconv.Itoa(i),
			map[string]any{"k": i, "s": "значение", "nested": map[string]any{"a": 1}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Pool().QueryRow(ctx,
		`SELECT array_agg(seq ORDER BY seq) FROM audit_log WHERE user_email=$1`, marker).Scan(&seqs); err != nil {
		t.Fatal(err)
	}
	if len(seqs) != 3 {
		t.Fatalf("ожидали 3 строки, got %d", len(seqs))
	}

	// Чистая цепочка (это же проверяет, что canonical записи == проверки — иначе всё было бы tampered).
	r, err := db.VerifyAuditIntegrity(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Configured || r.Tampered || r.TailTruncated || r.Checked < 3 {
		t.Fatalf("нетронутая цепочка: %+v", r)
	}

	// Модификация 2-й строки (не якорь) — детект ровно на ней.
	mid := seqs[1]
	var orig string
	if err := db.Pool().QueryRow(ctx, `SELECT action FROM audit_log WHERE seq=$1`, mid).Scan(&orig); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `UPDATE audit_log SET action='HACKED' WHERE seq=$1`, mid); err != nil {
		t.Fatal(err)
	}
	r, _ = db.VerifyAuditIntegrity(ctx, 0)
	if !r.Tampered || r.FirstTamperedSeq != mid {
		t.Fatalf("модификация: tampered=%v first=%d (want %d)", r.Tampered, r.FirstTamperedSeq, mid)
	}

	// Откат восстанавливает целостность.
	if _, err := db.Pool().Exec(ctx, `UPDATE audit_log SET action=$1 WHERE seq=$2`, orig, mid); err != nil {
		t.Fatal(err)
	}
	if r, _ = db.VerifyAuditIntegrity(ctx, 0); r.Tampered {
		t.Fatalf("после отката должно быть чисто: %+v", r)
	}

	// Усечение хвоста: удаляем последнюю строку — голова цепочки перестаёт сходиться.
	if _, err := db.Pool().Exec(ctx, `DELETE FROM audit_log WHERE seq=$1`, seqs[2]); err != nil {
		t.Fatal(err)
	}
	r, _ = db.VerifyAuditIntegrity(ctx, 0)
	if !r.Tampered || !r.TailTruncated {
		t.Fatalf("усечение хвоста не поймано: %+v", r)
	}
}

// Без ключа verify сообщает «не сконфигурировано», не падает и не врёт про целостность.
func TestAuditIntegrityUnconfigured(t *testing.T) {
	t.Setenv("ROUTINEOPS_AUDIT_HMAC_KEY", "")
	db := newDB(t)
	r, err := db.VerifyAuditIntegrity(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Configured || r.Tampered {
		t.Fatalf("без ключа configured/tampered должны быть false: %+v", r)
	}
}
