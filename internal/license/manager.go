//go:build enterprise

package license

import (
	"crypto/ed25519"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Status — снимок энтайтлмента для GET/POST /license. Поля и семантика совпадают с
// LicenseStatus во фронте (web/src/lib/api.ts): два флага configured/valid различают
// «истекла» и «не задана»; пустой Features = вся редакция; expires_at шлём всегда
// (zero-время = бессрочно); persist_warning только когда live-применение не легло на диск.
type Status struct {
	Configured     bool      `json:"configured"`
	Valid          bool      `json:"valid"`
	Licensee       string    `json:"licensee,omitempty"`
	Edition        string    `json:"edition,omitempty"`
	Features       []string  `json:"features,omitempty"`
	Seats          int       `json:"seats,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	PersistWarning string    `json:"persist_warning,omitempty"`
}

// Manager держит активную лицензию деплоя и обслуживает её жизненный цикл: загрузка на
// старте, live-применение/деактивация через веб, персист на rw-том, feature-gate.
// Потокобезопасен (RWMutex): применение из HTTP-хендлера конкурирует с чтениями Has/Status.
type Manager struct {
	pub   ed25519.PublicKey
	grace time.Duration
	path  string // файл персиста (ROUTINEOPS_LICENSE_FILE); "" = персист выключен

	// life сериализует изменения жизненного цикла (Apply/Deactivate/LoadInitial) так,
	// чтобы пара «мутация памяти + запись/удаление файла» была атомарной относительно
	// других таких пар. Без него две конкурентные it_admin-операции могли переплестись
	// и оставить диск рассинхронизированным с памятью, а LoadInitial на буте доверяет
	// диску — деактивированная лицензия воскресала бы после рестарта. Отдельный от mu:
	// диск-I/O не должен блокировать читателей Status/Has (они под mu.RLock).
	life sync.Mutex

	mu  sync.RWMutex
	cur *License // nil = редакция Free
	now func() time.Time
}

// NewManager создаёт менеджер. pub == nil означает «корень доверия не задан»: применить
// лицензию нельзя (любая отвергается как неподписанная валидным ключом), статус — «не задана».
func NewManager(pub ed25519.PublicKey, grace time.Duration, path string) *Manager {
	return &Manager{pub: pub, grace: grace, path: path, now: time.Now}
}

// LoadInitial загружает лицензию на старте. Приоритет — уже активированный файл на диске:
// подпись проверяется, но пароль повторно НЕ спрашивается (граница доверия — см.
// дизайн-док §«Пароль активации»: запись в license-том требует доступа к серверу, а на
// этом уровне оператор уже владеет деплоем; пароль защищает путь веб/env, не хост). Если
// файла нет, но задан envBlob (ROUTINEOPS_LICENSE), активируем его headless с envPassword.
// Ошибки не фатальны: сервер стартует в Free и логирует причину.
func (m *Manager) LoadInitial(envBlob, envPassword string, logger *slog.Logger) {
	m.life.Lock()
	defer m.life.Unlock()
	if m.pub == nil {
		if envBlob != "" {
			logger.Warn("ROUTINEOPS_LICENSE задана, но нет публичного ключа лицензий (ROUTINEOPS_LICENSE_PUBKEY / вшитого) — проверить нечем, игнорируется")
		}
		return
	}
	// 1) Персистнутый активированный blob.
	if m.path != "" {
		if b, err := os.ReadFile(m.path); err == nil {
			lic, err := Parse(string(b), m.pub)
			if err != nil {
				logger.Warn("сохранённая лицензия не проходит проверку — игнорируется", "err", err)
			} else {
				m.set(lic)
				logger.Info("лицензия загружена с диска", "licensee", lic.Claims.Licensee, "valid", m.Status().Valid)
				return
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			logger.Warn("не удалось прочитать файл лицензии", "path", m.path, "err", err)
		}
	}
	// 2) Headless из env (первая активация без веба).
	if envBlob != "" {
		lic, err := Parse(envBlob, m.pub)
		if err != nil {
			logger.Warn("ROUTINEOPS_LICENSE отклонена", "err", err)
			return
		}
		if err := lic.CheckPassword(envPassword); err != nil {
			logger.Warn("ROUTINEOPS_LICENSE: неверный пароль активации (ROUTINEOPS_LICENSE_PASSWORD)", "err", err)
			return
		}
		m.set(lic)
		if w := m.persist(lic.Blob()); w != "" {
			logger.Warn("лицензия применена из env, но не сохранена", "warn", w)
		}
		logger.Info("лицензия активирована из env", "licensee", lic.Claims.Licensee, "valid", m.Status().Valid)
	}
}

// Apply проверяет подпись и пароль активации, применяет лицензию live (без рестарта) и
// сохраняет на диск. Отклонённый ключ НЕ сбрасывает текущую лицензию. Принимается любая
// корректно подписанная лицензия (в т.ч. вне срока) — тогда Configured=true, Valid=false;
// persist_warning в статусе, если live-применение не легло на диск.
func (m *Manager) Apply(blob, password string) (Status, error) {
	if m.pub == nil {
		return Status{}, ErrSignature
	}
	lic, err := Parse(blob, m.pub)
	if err != nil {
		return Status{}, err
	}
	if err := lic.CheckPassword(password); err != nil {
		return Status{}, err
	}
	// Мутация памяти и запись файла — под life: конкурентная Deactivate не переплетётся.
	m.life.Lock()
	defer m.life.Unlock()
	m.set(lic)
	warn := m.persist(lic.Blob())
	st := m.Status()
	st.PersistWarning = warn
	return st, nil
}

// Deactivate сбрасывает сервер в Free и удаляет ключ с диска. persist_warning, если файл
// удалить не удалось (после рестарта лицензия могла бы вернуться).
func (m *Manager) Deactivate() Status {
	m.life.Lock()
	defer m.life.Unlock()
	m.set(nil)
	warn := m.removePersist()
	st := m.Status()
	st.PersistWarning = warn
	return st
}

// Status — текущий снимок для UI. Valid учитывает срок и grace.
func (m *Manager) Status() Status {
	m.mu.RLock()
	lic := m.cur
	m.mu.RUnlock()
	if lic == nil {
		return Status{}
	}
	valid := lic.ValidAt(m.now(), m.grace) == nil
	return Status{
		Configured: true,
		Valid:      valid,
		Licensee:   lic.Claims.Licensee,
		Edition:    lic.Claims.Edition,
		Features:   lic.Claims.Features,
		Seats:      lic.Claims.Seats,
		ExpiresAt:  lic.Claims.ExpiresAt,
	}
}

// Has — включена ли enterprise-фича: есть активная лицензия, она в сроке И покрывает фичу
// (пустой список фич = вся редакция). Feature-gate для будущих enterprise-функций.
func (m *Manager) Has(feature string) bool {
	m.mu.RLock()
	lic := m.cur
	m.mu.RUnlock()
	if lic == nil || lic.ValidAt(m.now(), m.grace) != nil {
		return false
	}
	return lic.Claims.Has(feature)
}

func (m *Manager) set(lic *License) {
	m.mu.Lock()
	m.cur = lic
	m.mu.Unlock()
}

// persist сохраняет blob на rw-том (0600). Возвращает человекочитаемое предупреждение,
// если сохранить не удалось (пустая строка = ок).
func (m *Manager) persist(blob string) string {
	if m.path == "" {
		return "ROUTINEOPS_LICENSE_FILE не задан — лицензия применена, но не переживёт рестарт"
	}
	if err := os.WriteFile(m.path, []byte(blob), 0o600); err != nil {
		return "не удалось записать файл лицензии: " + err.Error()
	}
	return ""
}

// removePersist удаляет файл лицензии. Отсутствие файла — не ошибка.
func (m *Manager) removePersist() string {
	if m.path == "" {
		return ""
	}
	if err := os.Remove(m.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "не удалось удалить файл лицензии: " + err.Error()
	}
	return ""
}
