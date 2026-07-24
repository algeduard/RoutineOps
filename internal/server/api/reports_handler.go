//go:build enterprise

package api

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Floodww/RoutineOps/internal/license"
	"github.com/go-chi/chi/v5"
)

// ReportsRoutes монтирует GET /reports/{type} — экспортируемые отчёты по УЖЕ существующим
// данным (устройства, инвентарь ПО, журнал аудита, алерты). Enterprise-фича FeatureReports:
// гейт по лицензии (mgr.Has → 402) первым делом. Ставится через WithAdminRoutes (it_admin):
// выгрузка парка/журнала наружу — чувствительная операция. В open-core роута нет (404).
//
// ?format=csv|pdf (по умолчанию csv). CSV — строгий (encoding/csv + нейтрализация
// CSV-инъекции, см. csvSafeCell). «PDF» реализован ПРАГМАТИЧНО, без тяжёлой Go-PDF-
// зависимости: сервер отдаёт самодостаточный печатный HTML (таблица + @media print CSS),
// пользователь печатает его в PDF из браузера (Ctrl/Cmd+P → Save as PDF). Инлайн-JS не
// используем — CSP деплоя (default-src 'self') его бы заблокировала; печать инициирует сам
// пользователь. Follow-up: нативный рендер PDF лёгкой чистой Go-либой, если понадобится
// серверная генерация файла без участия браузера.
//
// ?from=&to= фильтруют период (используется отчётом audit; для остальных игнорируются).
// Отчёты стримятся построчно (storage.Stream*Report → emit), поэтому большой парк/журнал
// не материализуется в память. Экспорт пишется в журнал (report_exported: type+format).
func ReportsRoutes(mgr *license.Manager) func(*Handler, chi.Router) {
	return func(h *Handler, r chi.Router) {
		r.Get("/reports/{type}", func(w http.ResponseWriter, req *http.Request) {
			if !mgr.Has(license.FeatureReports) {
				http.Error(w, "reporting requires a license", http.StatusPaymentRequired)
				return
			}

			reportType := chi.URLParam(req, "type")
			meta, ok := reportMetaByType[reportType]
			if !ok {
				http.Error(w, "unknown report type", http.StatusNotFound)
				return
			}

			format := strings.TrimSpace(req.URL.Query().Get("format"))
			if format == "" {
				format = "csv"
			}
			if format != "csv" && format != "pdf" {
				http.Error(w, "format must be 'csv' or 'pdf'", http.StatusBadRequest)
				return
			}

			from, to, perr := parseReportPeriod(req.URL.Query().Get("from"), req.URL.Query().Get("to"))
			if perr != nil {
				http.Error(w, perr.Error(), http.StatusBadRequest)
				return
			}

			// run прогоняет нужный отчёт, передавая emit-колбэк вниз в storage.
			run := func(emit func([]string) error) error {
				return h.streamReport(req.Context(), reportType, from, to, emit)
			}

			userID, email, _ := Actor(req.Context())
			h.audit(req.Context(), userID, email, "report_exported", "report", reportType,
				map[string]string{"type": reportType, "format": format})

			if format == "csv" {
				w.Header().Set("Content-Type", "text/csv; charset=utf-8")
				w.Header().Set("Content-Disposition", `attachment; filename="`+reportType+`-report.csv"`)
				w.WriteHeader(http.StatusOK)
				if err := writeReportCSV(w, meta.header, run); err != nil {
					slog.Error("reports csv export", "type", reportType, "err", err)
				}
				return
			}

			// pdf → печатный HTML (inline, чтобы открылся в браузере, а не скачался).
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			if err := writeReportHTML(w, meta, reportType, from, to, run); err != nil {
				slog.Error("reports html export", "type", reportType, "err", err)
			}
		})
	}
}

// reportMeta — статическая шапка отчёта: заголовок для HTML и колонки. Порядок и число
// колонок обязаны совпадать с []string, который стримит соответствующий storage.Stream*.
type reportMeta struct {
	title  string
	header []string
}

var reportMetaByType = map[string]reportMeta{
	"devices": {
		title:  "Device fleet report",
		header: []string{"hostname", "os", "os_version", "status", "disk_encryption", "owner_email", "last_seen", "agent_version", "serial_number"},
	},
	"inventory": {
		title:  "Software inventory report",
		header: []string{"software_name", "version", "device_count"},
	},
	"audit": {
		title:  "Audit log report",
		header: []string{"timestamp", "user_email", "action", "target_type", "target_id", "details"},
	},
	"alerts": {
		title:  "Alerts report",
		header: []string{"device_hostname", "alert_type", "severity", "details", "created_at", "acknowledged_at"},
	},
}

// streamReport диспетчеризует тип отчёта в соответствующий storage.Stream*Report. Диапазон
// from/to релевантен только audit; прочие отчёты его игнорируют.
func (h *Handler) streamReport(ctx context.Context, reportType string, from, to time.Time, emit func([]string) error) error {
	switch reportType {
	case "devices":
		return h.db.StreamDeviceReport(ctx, emit)
	case "inventory":
		return h.db.StreamSoftwareInventoryReport(ctx, emit)
	case "audit":
		return h.db.StreamAuditReport(ctx, from, to, emit)
	case "alerts":
		return h.db.StreamAlertsReport(ctx, emit)
	}
	return fmt.Errorf("unknown report type %q", reportType)
}

// writeReportCSV пишет заголовок и стримит строки в CSV. Каждая ячейка проходит через
// csvSafeCell (нейтрализация формул-инъекций) перед записью. encoding/csv сам квотирует
// запятые/кавычки/переводы строк — корректный RFC 4180.
func writeReportCSV(w io.Writer, header []string, run func(emit func([]string) error) error) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		return err
	}
	runErr := run(func(rec []string) error {
		safe := make([]string, len(rec))
		for i, c := range rec {
			safe[i] = csvSafeCell(c)
		}
		return cw.Write(safe)
	})
	cw.Flush()
	if runErr != nil {
		return runErr
	}
	return cw.Error()
}

// csvSafeCell нейтрализует CSV/формула-инъекцию: ячейку, начинающуюся с =,+,-,@ (а также с
// tab/CR — их табличные процессоры тоже трактуют как начало формулы), префиксуем апострофом,
// чтобы Excel/Sheets/LibreOffice не исполнили её как формулу при открытии выгрузки. Значения
// отчётов — текст (имена ПО/хостов/действий, метки времени, счётчики), поэтому апостроф-
// префикс не искажает данные, а положительные счётчики/ISO-даты под правило не попадают.
func csvSafeCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// parseReportPeriod разбирает ?from=&to= в полуинтервал [from, to). Пустая граница →
// сентинел (0001-01-01 / 9999-12-31), т.е. «без нижней/верхней границы». Принимает
// RFC3339 или YYYY-MM-DD; для date-only `to` берётся НАЧАЛО следующего дня (полуинтервал
// включает весь указанный день). Некорректный формат или to<from → ошибка (400 у ручки).
func parseReportPeriod(fromStr, toStr string) (from, to time.Time, err error) {
	from = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	to = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
	if s := strings.TrimSpace(fromStr); s != "" {
		t, e := parseReportTime(s, false)
		if e != nil {
			return from, to, fmt.Errorf("invalid 'from': %v", e)
		}
		from = t
	}
	if s := strings.TrimSpace(toStr); s != "" {
		t, e := parseReportTime(s, true)
		if e != nil {
			return from, to, fmt.Errorf("invalid 'to': %v", e)
		}
		to = t
	}
	if to.Before(from) {
		return from, to, errors.New("'to' must not be before 'from'")
	}
	return from, to, nil
}

func parseReportTime(s string, endOfDay bool) (time.Time, error) {
	if t, e := time.Parse(time.RFC3339, s); e == nil {
		return t.UTC(), nil
	}
	if t, e := time.Parse("2006-01-02", s); e == nil {
		if endOfDay {
			// Верхняя граница полуинтервала эксклюзивна → начало следующего дня включает
			// весь указанный день целиком.
			return t.AddDate(0, 0, 1), nil
		}
		return t, nil
	}
	return time.Time{}, errors.New("expected YYYY-MM-DD or RFC3339")
}

const reportHTMLStyle = `<style>
:root{color-scheme:light}
*{box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;margin:24px;color:#1e293b}
h1{font-size:20px;margin:0 0 4px}
.meta{color:#64748b;font-size:12px;margin:2px 0}
.banner{background:#eff6ff;border:1px solid #bfdbfe;color:#1e40af;padding:8px 12px;border-radius:6px;font-size:13px;margin:12px 0}
.tablewrap{overflow-x:auto}
table{border-collapse:collapse;width:100%;font-size:12px;margin-top:12px}
th,td{border:1px solid #e2e8f0;padding:5px 8px;text-align:left;vertical-align:top;word-break:break-word}
th{background:#f8fafc;font-weight:600;white-space:nowrap}
tbody tr:nth-child(even){background:#fafafa}
.empty{color:#64748b;font-size:13px;margin-top:12px}
.err{color:#b91c1c;font-size:12px;margin-top:8px}
@media print{.banner{display:none}body{margin:0}th{background:#f1f5f9!important;-webkit-print-color-adjust:exact;print-color-adjust:exact}}
</style>`

// writeReportHTML отдаёт самодостаточный печатный HTML-отчёт (таблица + print-CSS).
// Стримит строки в <tbody> по мере обхода, все значения экранируются html.EscapeString
// (в HTML CSV-инъекция неактуальна — важно лишь не сломать разметку/не дать XSS).
func writeReportHTML(w io.Writer, meta reportMeta, reportType string, from, to time.Time, run func(emit func([]string) error) error) error {
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	fmt.Fprint(w, `<meta name="viewport" content="width=device-width, initial-scale=1">`)
	fmt.Fprintf(w, `<title>%s</title>`, html.EscapeString(meta.title))
	fmt.Fprint(w, reportHTMLStyle)
	fmt.Fprint(w, `</head><body>`)
	fmt.Fprintf(w, `<h1>%s</h1>`, html.EscapeString(meta.title))
	fmt.Fprintf(w, `<p class="meta">Generated %s</p>`, html.EscapeString(time.Now().UTC().Format("2006-01-02 15:04 UTC")))
	if reportType == "audit" {
		fmt.Fprintf(w, `<p class="meta">Period: %s to %s</p>`,
			html.EscapeString(reportBoundLabel(from, "beginning")),
			html.EscapeString(reportBoundLabel(to, "now")))
	}
	fmt.Fprint(w, `<div class="banner">To save as PDF, use your browser's Print (Ctrl/Cmd+P) and choose "Save as PDF".</div>`)
	fmt.Fprint(w, `<div class="tablewrap"><table><thead><tr>`)
	for _, col := range meta.header {
		fmt.Fprintf(w, `<th>%s</th>`, html.EscapeString(col))
	}
	fmt.Fprint(w, `</tr></thead><tbody>`)

	rowCount := 0
	runErr := run(func(rec []string) error {
		rowCount++
		fmt.Fprint(w, `<tr>`)
		for _, c := range rec {
			fmt.Fprintf(w, `<td>%s</td>`, html.EscapeString(c))
		}
		fmt.Fprint(w, `</tr>`)
		return nil
	})

	fmt.Fprint(w, `</tbody></table></div>`)
	if rowCount == 0 {
		fmt.Fprint(w, `<p class="empty">No data.</p>`)
	}
	if runErr != nil {
		fmt.Fprint(w, `<p class="err">Report generation was interrupted; data may be incomplete.</p>`)
	}
	fmt.Fprint(w, `</body></html>`)
	return runErr
}

// reportBoundLabel печатает границу периода, подменяя сентинелы (0001/9999) на слово.
func reportBoundLabel(t time.Time, sentinelWord string) string {
	if t.Year() <= 1 || t.Year() >= 9999 {
		return sentinelWord
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}
