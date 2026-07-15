// Package scriptenc содержит общие хелперы кодировки для исполнения скриптов
// агентом: backstop валидации UTF-8 вывода и UTF-8-префикс для Windows
// PowerShell.
//
// Вынесено в отдельный пакет, чтобы ОБА пути исполнения — ad-hoc задачи
// (internal/agent/command) и скрипт-политики (internal/agent/scripts) —
// использовали один источник правды и не расходились: рассинхрон этих путей и
// был причиной молчаливой потери результатов политик на RU-Windows (proto3
// string обязан быть валидным UTF-8, иначе Marshal/ReportTaskResult падает, а
// результат теряется навсегда).
package scriptenc

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// utf8Replacement — символ-заглушка (U+FFFD) для невалидных UTF-8 байт.
const utf8Replacement = "�"

// PSUTF8Prefix заставляет Windows PowerShell 5.1 отдавать stdout нативных
// командлетов в UTF-8. Без этого на русской Windows вывод приезжает в OEM
// (cp866) / ANSI (cp1251) и ломает proto3-сериализацию результата.
// Легаси-EXE (whoami, ipconfig) всё равно могут писать в OEM — их добивает
// backstop SanitizeUTF8 уже на стороне Go.
const PSUTF8Prefix = "[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; " +
	"$OutputEncoding=[System.Text.Encoding]::UTF8; "

// SanitizeUTF8 заменяет невалидные UTF-8 последовательности на U+FFFD, чтобы
// строка всегда сериализовалась в proto3 string. stdlib-only, без транскода
// кодпейджа.
func SanitizeUTF8(s string) string {
	return strings.ToValidUTF8(s, utf8Replacement)
}

// MaxOutputBytes — потолок stdout/stderr одного отчёта. Сервер отвергает gRPC-кадр
// больше 4 МБ (grpc.MaxRecvMsgSize) кодом ResourceExhausted; отчёт с замороженным
// payload остаётся в голове FIFO-очереди outbox и намертво блокирует доставку ВСЕГО
// остального — security-событий, статусов лока, других результатов. Поэтому режем на
// источнике: 256 КиБ хватает на диагностику, а дампы логов агент возить не обязан.
const MaxOutputBytes = 256 * 1024

// TruncateOutput обрезает вывод до MaxOutputBytes по границе руны и дописывает
// пометку, чтобы обрезание было видно человеку, а не выглядело как конец скрипта.
func TruncateOutput(s string) string {
	if len(s) <= MaxOutputBytes {
		return s
	}
	cut := MaxOutputBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n\n[вывод обрезан: отброшено %d байт из %d]", len(s)-cut, len(s))
}
