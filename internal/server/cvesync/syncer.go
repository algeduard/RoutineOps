//go:build enterprise

// Package cvesync периодически подтягивает CVE-фид из внешнего источника (enterprise-фича
// FeatureCVEScan) вместо ручной заливки POST /cve/feed. Фоновый цикл по расписанию (интервал
// из конфига cve_feed_source) скачивает фид с настроенного администратором URL, ЗАМЕНЯЕТ им
// текущий фид (storage.LoadCVEFeed) и, если включён auto_scan, пересобирает находки
// (storage.ScanCVE). Ошибки источника НЕ роняют сервер — логируются и пишутся в last_status.
// Гейт по лицензии — снаружи (licensed()), чтобы пакет не зависел от internal/license.
//
// БЕЗОПАСНОСТЬ: URL задаёт it_admin (не SSRF-вектор извне), но запрос всё равно ограничен по
// таймауту (fetchTimeout) и размеру тела (maxFeedBytes) — анти-DoS против гигантского/зависшего
// ответа. Ожидаемый формат — тот же JSON-массив записей, что принимает POST /cve/feed
// (storage.CVEFeedEntry); реальные выгрузки NVD/OSV деплойер приводит к нему через прокси/скрипт.
package cvesync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Floodww/RoutineOps/internal/server/storage"
)

const (
	tickInterval = 15 * time.Minute // как часто проверять, не пора ли синкать (по расписанию)
	fetchTimeout = 30 * time.Second // таймаут HTTP-запроса к источнику фида
	maxFeedBytes = 32 << 20         // анти-DoS: максимум тела ответа (32 МиБ; выгрузки NVD/OSV крупнее 1 МБ лимита POST /cve/feed)
)

// defaultClient — общий клиент для форс-синка из API-хендлера (Sync с client == nil).
var defaultClient = &http.Client{Timeout: fetchTimeout}

// Syncer — фоновый синхронизатор CVE-фида. licensed() гейтит по лицензии (пустой тик, если
// FeatureCVEScan не активна), чтобы пакет не зависел от internal/license.
type Syncer struct {
	db       *storage.DB
	licensed func() bool
	logger   *slog.Logger
	client   *http.Client
}

func NewSyncer(db *storage.DB, licensed func() bool, logger *slog.Logger) *Syncer {
	return &Syncer{
		db:       db,
		licensed: licensed,
		logger:   logger,
		client:   &http.Client{Timeout: fetchTimeout},
	}
}

// Run крутит цикл синка до завершения процесса (как прочие фоновые циклы cmd/server).
func (s *Syncer) Run() {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for range ticker.C {
		s.tick()
	}
}

func (s *Syncer) tick() {
	if !s.licensed() {
		return // лицензия не покрывает CVE-скан — молча ничего не синкаем
	}
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout+30*time.Second)
	defer cancel()

	cfg, err := s.db.GetCVEFeedSource(ctx)
	if err != nil {
		s.logger.Error("cve sync: чтение конфига источника", "err", err)
		return
	}
	if !cfg.Enabled || strings.TrimSpace(cfg.URL) == "" {
		return // источник выключен или не настроен
	}
	if !due(cfg, time.Now()) {
		return // ещё не подошёл срок по расписанию
	}

	loaded, status, err := Sync(ctx, s.db, s.client)
	if err != nil {
		// Реальный сбой БД (не «источник недоступен» — тот уходит в last_status без ошибки).
		s.logger.Error("cve sync: синхронизация", "err", err)
		return
	}
	// Аудит фонового синка — системный актор (у фонового цикла нет пользователя-инициатора).
	if aerr := s.db.WriteAuditLog(context.WithoutCancel(ctx), "", "system", "cve_feed_synced", "cve", "",
		map[string]any{"count": loaded, "status": status}); aerr != nil {
		s.logger.Error("cve sync: запись аудита", "err", aerr)
	}
	s.logger.Info("cve sync: фид синхронизирован", "loaded", loaded, "status", status)
}

// due сообщает, подошёл ли срок очередного синка: никогда не синкали → да; иначе прошло не
// меньше sync_interval_hours с последней ПОПЫТКИ (last_synced_at двигается и на ошибке — см.
// storage.CVEFeedSource — поэтому битый источник не молотится каждый тик).
func due(cfg storage.CVEFeedSource, now time.Time) bool {
	if cfg.LastSyncedAt == nil {
		return true
	}
	interval := time.Duration(cfg.SyncIntervalHours) * time.Hour
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return now.Sub(*cfg.LastSyncedAt) >= interval
}

// Sync выполняет ОДИН проход синхронизации: читает конфиг источника, скачивает фид, ЗАМЕНЯЕТ им
// текущий фид и (если auto_scan) пересобирает находки, затем фиксирует last_synced_at/last_status.
// Гейтинг enabled/расписания — забота вызывающего (фоновый tick), сам Sync лишь требует URL —
// так форс-синк из API работает даже при выключенном авто-синке. client == nil → общий дефолтный.
//
// Возвращает число загруженных записей и человекочитаемый статус. Недоступный/битый источник —
// НЕ ошибка Sync (пишем 'error: ...' в last_status и возвращаем err == nil): сервер не должен
// падать из-за внешнего фида. err != nil — только реальный сбой БД (чтение конфига/заливка/скан).
func Sync(ctx context.Context, db *storage.DB, client *http.Client) (loaded int, status string, err error) {
	if client == nil {
		client = defaultClient
	}
	cfg, err := db.GetCVEFeedSource(ctx)
	if err != nil {
		return 0, "", err
	}
	url := strings.TrimSpace(cfg.URL)
	if url == "" {
		status = "error: источник не настроен (url пуст)"
		if merr := db.MarkCVEFeedSourceSynced(ctx, status); merr != nil {
			return 0, "", merr
		}
		return 0, status, nil
	}

	entries, ferr := fetchFeed(ctx, client, url)
	if ferr != nil {
		status = "error: " + ferr.Error()
		if merr := db.MarkCVEFeedSourceSynced(ctx, status); merr != nil {
			return 0, "", merr
		}
		return 0, status, nil // источник недоступен ≠ отказ сервера
	}

	n, lerr := db.LoadCVEFeed(ctx, entries)
	if lerr != nil {
		return 0, "", lerr
	}
	status = fmt.Sprintf("ok: загружено записей %d", n)
	if cfg.AutoScan {
		if _, serr := db.ScanCVE(ctx); serr != nil {
			return 0, "", serr
		}
		status += ", скан выполнен"
	}
	if merr := db.MarkCVEFeedSourceSynced(ctx, status); merr != nil {
		return 0, "", merr
	}
	return n, status, nil
}

// fetchFeed скачивает фид GET-запросом и разбирает его как JSON-массив storage.CVEFeedEntry.
// Тело ограничено maxFeedBytes (анти-DoS), запрос — таймаутом клиента. Успех — только 2xx.
func fetchFeed(ctx context.Context, client *http.Client, url string) ([]storage.CVEFeedEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "RoutineOps-CVE-Sync")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("источник вернул %d", resp.StatusCode)
	}
	var entries []storage.CVEFeedEntry
	// LimitReader режет тело жёстко: превышение лимита проявится как ошибка разбора усечённого
	// JSON — что нам и нужно (не хотим грузить в память гигантский ответ целиком).
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxFeedBytes)).Decode(&entries); err != nil {
		return nil, fmt.Errorf("разбор фида: %w", err)
	}
	return entries, nil
}
