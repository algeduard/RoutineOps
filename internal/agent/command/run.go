package command

import (
	"bytes"
	"context"
	"os/exec"
	"strings"

	"github.com/Floodww/RoutineOps/internal/agent/scriptenc"
)

// interpreterCmd выбирает интерпретатор по полю platform задачи:
// windows → powershell -Command, иначе (macOS/Linux) → bash -c.
//
// Сравнение РЕГИСТРОНЕЗАВИСИМОЕ намеренно: значение platform едет из разных мест с
// разным регистром — UI шлёт строчное "windows" (web agentPlatform()), а справочник
// скриптов и серверная валидация оперируют "Windows". Строгое `case "Windows"`
// молча отправляло UI-задачи на Windows в bash-ветку → «exec: bash not found in
// %PATH%», все скрипты на Windows через интерфейс падали. Нормализуем в одном месте,
// где значение реально используется, а не чиним каждый источник по отдельности.
func interpreterCmd(ctx context.Context, platform, script string) *exec.Cmd {
	switch strings.ToLower(platform) {
	case "windows":
		return exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", scriptenc.PSUTF8Prefix+script)
	default: // macOS, Linux
		return exec.CommandContext(ctx, "bash", "-c", script)
	}
}

// runScript выполняет script_content интерпретатором по полю platform задачи.
// stdout → output задачи, stderr → error_log при ошибке.
//
// Оба буфера прогоняются через scriptenc.SanitizeUTF8: proto3 string обязан быть
// валидным UTF-8, иначе ReportTaskResult падает на маршалинге и результат задачи
// теряется навсегда (задача виснет в статусе acked на сервере). Backstop
// гарантирует, что отчёт уйдёт при любой кодировке вывода.
func runScript(ctx context.Context, platform, script string) (stdout, stderr string, err error) {
	cmd := interpreterCmd(ctx, platform, script)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err = cmd.Run()
	// Обрезаем до отправки: кадр >4 МБ сервер отвергает ResourceExhausted'ом, и отчёт
	// навсегда застревает в голове outbox-очереди (см. scriptenc.MaxOutputBytes).
	return scriptenc.TruncateOutput(scriptenc.SanitizeUTF8(out.String())),
		scriptenc.TruncateOutput(scriptenc.SanitizeUTF8(errBuf.String())), err
}
