//go:build enterprise

// Package siem форвардит новые события аудита на внешний webhook (SIEM). Фоновый цикл
// поллит audit_log по durable-курсору (last_sent_seq), батчами POST'ит их с HMAC-подписью
// тела и двигает курсор ТОЛЬКО при успешной доставке (2xx) — потеря/отказ webhook'а
// приводит к повтору, а не к пропуску события. Гейт по лицензии — снаружи (licensed()).
package siem

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

const (
	tickInterval = 15 * time.Second // как часто проверять новые события
	batchSize    = 200              // сколько событий за один POST (курсор двигаем на последний)
	httpTimeout  = 15 * time.Second
	// safetyLagSeconds — экспортируем только события старше этого лага. Закрывает гонку
	// «seq присвоен, но ряд ещё не закоммичен»: за несколько секунд короткие autocommit-
	// INSERT'ы аудита гарантированно коммитятся, и курсор не перепрыгнет невидимый меньший
	// seq (иначе — тихая потеря аудита; см. storage.ListAuditSince).
	safetyLagSeconds = 5
)

// Exporter — фоновый форвардер аудита. licensed() гейтит по лицензии (пустой тик, если
// FeatureSIEMExport не активна), чтобы пакет не зависел от internal/license.
type Exporter struct {
	db       *storage.DB
	licensed func() bool
	logger   *slog.Logger
	client   *http.Client
}

func NewExporter(db *storage.DB, licensed func() bool, logger *slog.Logger) *Exporter {
	return &Exporter{
		db:       db,
		licensed: licensed,
		logger:   logger,
		client:   &http.Client{Timeout: httpTimeout},
	}
}

// Run крутит цикл экспорта до завершения процесса (как прочие фоновые циклы cmd/server).
func (e *Exporter) Run() {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for range ticker.C {
		e.tick()
	}
}

func (e *Exporter) tick() {
	if !e.licensed() {
		return // лицензия не покрывает SIEM-экспорт — молча ничего не форвардим
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := e.db.GetSIEMExportConfig(ctx)
	if err != nil {
		e.logger.Error("siem: не удалось прочитать конфиг", "err", err)
		return
	}
	if !cfg.Enabled || cfg.WebhookURL == "" {
		return
	}
	entries, err := e.db.ListAuditSince(ctx, cfg.LastSentSeq, safetyLagSeconds, batchSize)
	if err != nil {
		e.logger.Error("siem: выборка аудита", "err", err)
		return
	}
	if len(entries) == 0 {
		return
	}
	if err := e.post(ctx, cfg.WebhookURL, cfg.HMACSecret, entries); err != nil {
		// Курсор НЕ двигаем — те же события уйдут на следующем тике (at-least-once).
		e.logger.Warn("siem: доставка на webhook не удалась, повтор позже", "err", err, "count", len(entries))
		return
	}
	last := entries[len(entries)-1].Seq
	if err := e.db.AdvanceSIEMCursor(ctx, last); err != nil {
		e.logger.Error("siem: сдвиг курсора", "err", err)
		return
	}
	e.logger.Info("siem: события аудита отправлены", "count", len(entries), "cursor", last)
}

// post отправляет батч событий JSON'ом на webhook с HMAC-SHA256 подписью тела в заголовке
// X-RoutineOps-Signature (sha256=<hex>), если задан секрет. Успех — только 2xx.
func (e *Exporter) post(ctx context.Context, url, secret string, entries []storage.AuditEntry) error {
	body, err := json.Marshal(map[string]any{"events": entries})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "RoutineOps-SIEM-Export")
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		req.Header.Set("X-RoutineOps-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook вернул %d", resp.StatusCode)
	}
	return nil
}
