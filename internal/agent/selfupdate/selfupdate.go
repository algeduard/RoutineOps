// Package selfupdate реализует безопасное самообновление агента (Этап 7-8).
//
// Агент периодически спрашивает у сервера актуальную версию (manifest), и если
// она новее — скачивает бинарь, ПРОВЕРЯЕТ его целостность (sha256) и ПОДПИСЬ
// (ed25519 публичным ключом релиза, вшитым в агент), атомарно заменяет себя и
// инициирует перезапуск через супервизор службы.
//
// Безопасность — главное: агент работает с правами root/админа на каждом
// устройстве, поэтому скомпрометированный сервер НЕ должен иметь возможности
// подсунуть произвольный бинарь. Применяется только бинарь, подписанный приватным
// ключом релиза (его на сервере нет). Без валидной подписи обновление отклоняется.
package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"
)

// Manifest — ответ сервера о доступной версии (см. docs/self-update.md).
type Manifest struct {
	Version   string `json:"version"`   // semver, напр. "v1.4.2"
	URL       string `json:"url"`       // откуда качать бинарь под этот os/arch
	SHA256    string `json:"sha256"`    // hex sha256 бинаря
	Signature string `json:"signature"` // ed25519 над sha256(бинарь) — legacy, verify() её больше не использует

	// ManifestSignature — base64 ed25519-подпись КАНОНА version\nos\narch\nsha256
	// (см. signedMessage), НЕ только sha256 бинаря. Публикуется publish-release
	// (cmd/publish-release/main.go), новое поле — старое signature осталось
	// нетронутым для агентов до этой версии. Пусто → verify() отклоняет (fail-closed,
	// см. migrations/019_agent_release_manifest_sig.sql).
	ManifestSignature string `json:"manifest_signature"`
}

// Updater оркестрирует проверку и применение обновлений.
type Updater struct {
	current   string // текущая версия агента (ldflags); "dev" → автообновление выключено
	interval  time.Duration
	pubKey    ed25519.PublicKey
	floorFile string // high-water mark версии (""=только память, см. loadFloor)
	log       *slog.Logger

	// Сеймы (подменяются в тестах; в проде — HTTP + замена файла + рестарт).
	check    func(ctx context.Context) (*Manifest, error)
	download func(ctx context.Context, url string) ([]byte, error)
	replace  func(newBinary []byte) error // атомарно заменить текущий исполняемый файл
	restart  func()                       // инициировать перезапуск (graceful-shutdown → супервизор)

	// OnReplaceFail (опционально) зовётся, когда замена бинаря ПРОВАЛИЛАСЬ. Нужен
	// Windows: replaceExecutable к этому моменту уже убил трей юзер-сессии taskkill'ом
	// (тот держит блокировку .old), а рестарта службы при ошибке не будет — без
	// реакции иконка пропадает до перелогина на всё время, пока замена падает
	// (AV держит файл, диск полон). Best-effort, выставляется из cmd/agent.
	OnReplaceFail func()
}

// New собирает Updater. pubKey — публичный ключ релиза (ed25519); если пуст,
// автообновление выключено (защита от применения неподписанных бинарей).
// floorFile — файл high-water mark применённой версии (""=без анти-rollback
// защиты, только сравнение с current). restart вызывается после успешной замены
// — обычно отмена корневого контекста агента (graceful shutdown), после чего
// служба перезапускается супервизором (launchd KeepAlive / Windows recovery action).
func New(current string, interval time.Duration, pubKey ed25519.PublicKey, checkURL, caFile, floorFile string, restart func(), log *slog.Logger) *Updater {
	u := &Updater{
		current:   current,
		interval:  interval,
		pubKey:    pubKey,
		floorFile: floorFile,
		log:       log,
		restart:   restart,
	}
	// manifest/бинарь отдаёт тот же сервер (приватная CA) — клиент должен ей
	// доверять, иначе TLS не пройдёт (подлинность бинаря гарантирует подпись).
	client, ok := newHTTPClient(caFile)
	if !ok {
		log.Warn("selfupdate: CA для проверки эндпоинта обновлений не загружен — используются системные корни", slog.String("ca", caFile))
	}
	u.check = func(ctx context.Context) (*Manifest, error) { return httpCheck(ctx, client, checkURL, current) }
	u.download = func(ctx context.Context, url string) ([]byte, error) { return httpDownload(ctx, client, url) }
	u.replace = replaceExecutable
	return u
}

// Run периодически проверяет и применяет обновления, пока ctx жив.
func (u *Updater) Run(ctx context.Context) {
	if len(u.pubKey) != ed25519.PublicKeySize {
		u.log.Warn("selfupdate: нет публичного ключа релиза — автообновление отключено")
		return
	}
	if u.current == "dev" || u.current == "" {
		u.log.Info("selfupdate: dev-сборка — автообновление отключено")
		return
	}
	ticker := time.NewTicker(u.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := u.checkAndApply(ctx); err != nil {
				u.log.Error("selfupdate: цикл обновления", slog.Any("error", err))
			}
		}
	}
}

// checkAndApply — одна итерация: проверить версию, при наличии новее — скачать,
// проверить и применить. Выделено для тестируемости.
func (u *Updater) checkAndApply(ctx context.Context) error {
	m, err := u.check(ctx)
	if err != nil {
		return fmt.Errorf("проверка версии: %w", err)
	}

	// Базовая версия для сравнения — не только текущая, но и high-water mark
	// (максимум из них): без этого сервер (или злоумышленник, укравший релизный
	// канал) мог бы подсунуть валидно подписанный, но состарившийся с тех пор
	// манифест устаревшей версии, если она всё ещё формально новее u.current —
	// например, после отката бинаря вручную (SEC-3, аудит 2026-07-01).
	baseline := u.current
	if floor := u.loadFloor(); floor != "" {
		if floorNewer, ferr := IsNewer(baseline, floor); ferr == nil && floorNewer {
			baseline = floor
		}
	}
	newer, err := IsNewer(baseline, m.Version)
	if err != nil {
		return fmt.Errorf("сравнение версий (%q vs %q): %w", baseline, m.Version, err)
	}
	if !newer {
		return nil // уже актуальны (или манифест не новее high-water mark)
	}
	u.log.Info("selfupdate: доступна новая версия",
		slog.String("current", u.current), slog.String("available", m.Version))

	data, err := u.download(ctx, m.URL)
	if err != nil {
		return fmt.Errorf("скачивание: %w", err)
	}
	if err := verify(data, m, u.pubKey, runtime.GOOS, runtime.GOARCH); err != nil {
		return fmt.Errorf("проверка бинаря отклонена: %w", err)
	}
	if err := u.replace(data); err != nil {
		if u.OnReplaceFail != nil {
			u.OnReplaceFail()
		}
		return fmt.Errorf("замена бинаря: %w", err)
	}
	u.saveFloor(m.Version) // best-effort — сбой персиста не должен отменять уже применённое обновление
	u.log.Info("selfupdate: новая версия применена — перезапуск", slog.String("version", m.Version))
	if u.restart != nil {
		u.restart()
	}
	return nil
}

// loadFloor читает high-water mark версии с диска. Пустая строка — нет флора
// (файла нет, не задан или битый — деградация к сравнению только с current, не отказ).
func (u *Updater) loadFloor() string {
	if u.floorFile == "" {
		return ""
	}
	data, err := os.ReadFile(u.floorFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// saveFloor персистит версию только что применённого обновления как новый пол.
func (u *Updater) saveFloor(version string) {
	if u.floorFile == "" {
		return
	}
	if err := os.WriteFile(u.floorFile, []byte(version), 0o644); err != nil {
		u.log.Warn("selfupdate: не удалось сохранить high-water mark версии", slog.Any("error", err))
	}
}

// signedMessage — канон, который подписывает release-ключ (publish-release,
// canon = version\nos\narch\nsha256, см. cmd/publish-release/main.go): не только
// sha256 бинаря, как было раньше. Без этого сервер/злоумышленник, укравший канал
// раздачи манифеста, мог бы взять СТАРЫЙ валидно подписанный бинарь+его настоящий
// sha256 (подпись которого покрывала только sha256) и подсунуть его под
// ПРОИЗВОЛЬНОЙ version — агент решил бы, что это новее (SEC-3, аудит 2026-07-01).
// os/arch — свои (runtime.GOOS/GOARCH), НЕ из ответа сервера: агент и так знает,
// под какую платформу просил манифест (см. httpCheck), сервер их не эхо́ит обратно.
func signedMessage(m *Manifest, goos, goarch string) []byte {
	return []byte(m.Version + "\n" + goos + "\n" + goarch + "\n" + m.SHA256)
}

// verify проверяет целостность (sha256) и подпись (ed25519) скачанного бинаря.
func verify(data []byte, m *Manifest, pubKey ed25519.PublicKey, goos, goarch string) error {
	sum := sha256.Sum256(data)
	wantSum, err := hex.DecodeString(m.SHA256)
	if err != nil {
		return fmt.Errorf("битый sha256 в манифесте: %w", err)
	}
	if len(wantSum) != len(sum) || !equalBytes(sum[:], wantSum) {
		return errors.New("sha256 не совпал (бинарь повреждён или подменён)")
	}
	if m.ManifestSignature == "" {
		return errors.New("манифест без manifest_signature — сервер ещё не публикует новую схему подписи (fail-closed, SEC-3)")
	}
	sig, err := base64.StdEncoding.DecodeString(m.ManifestSignature)
	if err != nil {
		return fmt.Errorf("битая manifest_signature: %w", err)
	}
	// Подписывается весь канон (version+os+arch+sha256), не только дайджест бинаря.
	if !ed25519.Verify(pubKey, signedMessage(m, goos, goarch), sig) {
		return errors.New("подпись манифеста невалидна — не от релиза, отклонена")
	}
	return nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
