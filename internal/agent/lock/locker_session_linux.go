//go:build linux

package lock

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// SessionLocker — Locker для Linux-службы: полноэкранный X11-замок рисует отдельный
// процесс `agent lock-screen` (internal/agent/lockui), а поднимает его сама служба
// в активной графической сессии пользователя. Служба под root живёт в session 0
// без дисплея и GUI не рисует, но может запустить процесс от имени залогиненного
// пользователя с его DISPLAY/XAUTHORITY. Зеркало Windows/macOS-локеров.
//
// Пока заблокировано, фоновый цикл держит оверлей живым: сессии нет (никто не вошёл)
// → поднимем при входе; пользователь убил процесс → перезапустим. Идемпотентно.
//
// Best-effort и НЕ протестировано вживую в этой сборке (кросс-компиляция под linux
// проверяет только компиляцию): дистрибутивы по-разному отдают XAUTHORITY, а под
// чистым Wayland X11-оверлей не поднимется — тогда локер просто логирует и молчит,
// как прежняя лог-заглушка (состояние всё равно persist'ится, разблокировка с
// сервера работает). См. docs/ROADMAP.md (Wayland — следующий шаг).
type SessionLocker struct {
	log *slog.Logger
	exe string // путь к бинарю агента; запускаем "<exe> lock-screen"

	mu     sync.Mutex
	cancel context.CancelFunc
	cmd    *exec.Cmd
}

// NewSessionLocker создаёт локер. exe — путь к бинарю агента (os.Executable).
func NewSessionLocker(exe string, log *slog.Logger) *SessionLocker {
	return &SessionLocker{exe: exe, log: log}
}

// Show поднимает замок: запускает фоновый цикл, который держит оверлей в активной
// сессии. verify игнорируется — пароль проверяет сам оверлей (bcrypt из lock.json).
func (l *SessionLocker) Show(reason string, _ func(string) bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		return // уже поднят (идемпотентность)
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	go l.ensureLoop(ctx)
	l.log.Warn("lock: служба поднимает замок в активной сессии", slog.String("reason", reason))
}

// Hide снимает замок: останавливает цикл и завершает процесс оверлея.
func (l *SessionLocker) Hide() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel == nil {
		return
	}
	l.cancel()
	l.cancel = nil
	l.terminateOverlayLocked()
	l.log.Warn("lock: замок снят (служба)")
}

func (l *SessionLocker) ensureLoop(ctx context.Context) {
	l.ensureOverlay()
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.ensureOverlay()
		}
	}
}

// ensureOverlay запускает lock-screen в активной X11-сессии, если он ещё не запущен.
func (l *SessionLocker) ensureOverlay() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel == nil || l.overlayAliveLocked() {
		return
	}

	sess, err := activeGraphicalSession()
	if err != nil {
		return // нет активной X11-сессии — норма (никто не вошёл / Wayland / нет logind)
	}

	cmd := exec.Command(l.exe, "lock-screen")
	// Даём оверлею окружение X-сессии и роняем привилегии до пользователя: X-сервер
	// не пустит подключение от root без явной авторизации, а нам нужен именно
	// пользовательский дисплей.
	cmd.Env = append(os.Environ(),
		"DISPLAY="+sess.display,
		"XAUTHORITY="+sess.xauthority,
		"XDG_RUNTIME_DIR=/run/user/"+strconv.Itoa(sess.uid),
		"HOME="+sess.home,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uint32(sess.uid), Gid: uint32(sess.gid)},
		Setpgid:    true, // своя группа процессов — чтобы аккуратно завершать
	}
	if err := cmd.Start(); err != nil {
		l.log.Warn("lock: не удалось поднять оверлей в активной сессии", slog.Any("error", err))
		return
	}
	l.cmd = cmd
	l.log.Info("lock: оверлей запущен службой в активной X11-сессии",
		slog.String("display", sess.display), slog.Int("uid", sess.uid))

	go func(c *exec.Cmd) {
		_ = c.Wait()
		l.mu.Lock()
		if l.cmd == c {
			l.cmd = nil
		}
		l.mu.Unlock()
	}(cmd)
}

func (l *SessionLocker) overlayAliveLocked() bool { return l.cmd != nil }

func (l *SessionLocker) terminateOverlayLocked() {
	if l.cmd != nil && l.cmd.Process != nil {
		_ = l.cmd.Process.Kill()
		l.cmd = nil
	}
}

// graphicalSession — сведения об активной графической сессии, нужные для запуска
// оверлея от имени пользователя.
type graphicalSession struct {
	uid, gid   int
	display    string // напр. ":0"
	xauthority string
	home       string
}

// activeGraphicalSession ищет активную локальную X11-сессию через systemd-logind
// (loginctl) и собирает окружение для запуска оверлея. Ошибка — сессии нет либо она
// не X11 (Wayland/tty пока не поддержаны).
func activeGraphicalSession() (graphicalSession, error) {
	out, err := exec.Command("loginctl", "list-sessions", "--no-legend").Output()
	if err != nil {
		return graphicalSession{}, err // нет loginctl (не-systemd) или иной сбой
	}
	for _, sid := range loginctlSessionIDs(string(out)) {
		props, err := sessionProps(sid)
		if err != nil {
			continue
		}
		uid, display, name, ok := activeX11FromProps(props)
		if !ok {
			continue
		}
		u, err := user.Lookup(name)
		if err != nil {
			continue
		}
		gid, err := strconv.Atoi(u.Gid)
		if err != nil {
			continue
		}
		return graphicalSession{
			uid:        uid,
			gid:        gid,
			display:    display,
			home:       u.HomeDir,
			xauthority: findXauthority(uid, u.HomeDir),
		}, nil
	}
	return graphicalSession{}, errors.New("нет активной X11-сессии")
}

// sessionProps читает нужные свойства сессии одним вызовом loginctl.
func sessionProps(sid string) (map[string]string, error) {
	out, err := exec.Command("loginctl", "show-session", sid,
		"-p", "Active", "-p", "Type", "-p", "User", "-p", "Name", "-p", "Display").Output()
	if err != nil {
		return nil, err
	}
	return parseLoginctlProps(string(out)), nil
}

// findXauthority подбирает файл авторизации X. Единого места нет: классический
// ~/.Xauthority, но GDM/новые сессии кладут cookie в /run/user/<uid>/… — проверяем
// известные варианты и берём первый существующий. Последний фолбэк — ~/.Xauthority
// (пусть оверлей попробует; хуже, чем есть, не станет).
func findXauthority(uid int, home string) string {
	rt := "/run/user/" + strconv.Itoa(uid)
	candidates := []string{
		filepath.Join(home, ".Xauthority"),
		filepath.Join(rt, "gdm", "Xauthority"),
	}
	// В /run/user/<uid> имя cookie-файла бывает динамическим (.mutter-Xwaylandauth.*,
	// xauth_XXXXXX) — подберём по префиксу.
	if entries, err := os.ReadDir(rt); err == nil {
		for _, e := range entries {
			n := e.Name()
			if hasAnyPrefix(n, "xauth", ".mutter-Xwaylandauth", ".Xauthority") {
				candidates = append(candidates, filepath.Join(rt, n))
			}
		}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return filepath.Join(home, ".Xauthority")
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if len(s) >= len(p) && s[:len(p)] == p {
			return true
		}
	}
	return false
}
