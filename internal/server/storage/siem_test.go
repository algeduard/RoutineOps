package storage_test

import (
	"context"
	"strconv"
	"testing"
)

func TestSIEMConfigAndCursor(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	// По умолчанию — пусто (строки конфига ещё нет).
	cfg, err := db.GetSIEMExportConfig(ctx)
	if err != nil || cfg.Enabled {
		t.Fatalf("дефолт должен быть пустым: %+v err=%v", cfg, err)
	}

	// Включение с секретом → курсор инициализируется текущим max(seq) (форвардим с «сейчас»).
	if err := db.SetSIEMExportConfig(ctx, true, "https://siem.example/ingest", "sekret"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = db.GetSIEMExportConfig(ctx)
	if !cfg.Enabled || cfg.WebhookURL != "https://siem.example/ingest" || !cfg.HasSecret {
		t.Fatalf("после включения: %+v", cfg)
	}
	baseline := cfg.LastSentSeq

	// Новые события аудита видны ListAuditSince по возрастанию seq.
	for i := 0; i < 3; i++ {
		if err := db.WriteAuditLog(ctx, "", "admin@test", "act"+strconv.Itoa(i), "t", "id", nil); err != nil {
			t.Fatal(err)
		}
	}
	// Свежие события (моложе лага) при minAgeSeconds>0 НЕ отдаются — защита от гонки
	// seq→commit (см. ListAuditSince). При лаге 0 — отдаются.
	if lagged, _ := db.ListAuditSince(ctx, baseline, 5, 10); len(lagged) != 0 {
		t.Fatalf("свежие события младше лага не должны отдаваться, got %d", len(lagged))
	}
	since, err := db.ListAuditSince(ctx, baseline, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 3 {
		t.Fatalf("ожидали 3 новых события, got %d", len(since))
	}
	for i := 1; i < len(since); i++ {
		if since[i].Seq <= since[i-1].Seq {
			t.Fatalf("seq не по возрастанию: %d затем %d", since[i-1].Seq, since[i].Seq)
		}
	}

	// Сдвиг курсора вперёд → повторная выборка с этого seq пуста.
	last := since[len(since)-1].Seq
	if err := db.AdvanceSIEMCursor(ctx, last); err != nil {
		t.Fatal(err)
	}
	cfg, _ = db.GetSIEMExportConfig(ctx)
	if cfg.LastSentSeq != last {
		t.Fatalf("курсор = %d, want %d", cfg.LastSentSeq, last)
	}
	again, _ := db.ListAuditSince(ctx, last, 0, 10)
	if len(again) != 0 {
		t.Fatalf("после сдвига за все события ожидали 0, got %d", len(again))
	}

	// Пустой секрет при апдейте = оставить прежний.
	if err := db.SetSIEMExportConfig(ctx, true, "https://new.example/x", ""); err != nil {
		t.Fatal(err)
	}
	cfg, _ = db.GetSIEMExportConfig(ctx)
	if !cfg.HasSecret || cfg.WebhookURL != "https://new.example/x" {
		t.Fatalf("секрет должен сохраниться: %+v", cfg)
	}

	// AdvanceSIEMCursor только вперёд: назад не откатывает.
	if err := db.AdvanceSIEMCursor(ctx, last-1); err != nil {
		t.Fatal(err)
	}
	cfg, _ = db.GetSIEMExportConfig(ctx)
	if cfg.LastSentSeq != last {
		t.Fatalf("курсор откатился назад: %d != %d", cfg.LastSentSeq, last)
	}
}
