package telemetry

import (
	"sync"

	pb "github.com/Floodww/RoutineOps/proto"
)

// activityAggregator накапливает ДЕЛЬТЫ активности приложений и времени за ПК с
// прошлой отправки. Модель дельт (а не абсолютных суток) переживает рестарт агента:
// in-memory счётчик сбрасывается, но сервер аккумулирует existing + delta, поэтому
// абсолют не регрессирует; теряется лишь неотправленное окно.
//
// Гранулярность — имя foreground-процесса (напр. "chrome.exe"), а НЕ заголовки
// окон/URL: приватность (см. docs/device-telemetry-design.md §4). foreground-время
// приложения считается ТОЛЬКО пока пользователь активен (idle не приписывается
// приложению). Потокобезопасен: сэмплер пишет, репортер сливает.
type activityAggregator struct {
	mu   sync.Mutex
	apps map[appKey]int64   // (day, app) → секунды на переднем плане
	days map[string]*dayAcc // day → активные/простойные секунды
}

type appKey struct {
	day   string
	app   string
	title string // заголовок окна; "" когда capture_window_titles выключен
}

type dayAcc struct {
	active int64
	idle   int64
}

func newActivityAggregator() *activityAggregator {
	return &activityAggregator{
		apps: map[appKey]int64{},
		days: map[string]*dayAcc{},
	}
}

// record учитывает seconds истёкшего времени: при active — в активное время дня и
// (если известно foreground-приложение) в его счётчик; иначе — в простой.
func (a *activityAggregator) record(day, app, title string, active bool, seconds int64) {
	if seconds <= 0 || day == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	d := a.days[day]
	if d == nil {
		d = &dayAcc{}
		a.days[day] = d
	}
	if active {
		d.active += seconds
		if app != "" {
			a.apps[appKey{day, app, title}] += seconds
		}
	} else {
		d.idle += seconds
	}
}

// drain атомарно возвращает накопленные дельты и обнуляет их. Вызывается перед
// отправкой; при неудаче отправки дельты возвращаются через restore.
func (a *activityAggregator) drain() (apps []*pb.AppUsageEntry, days []*pb.DailyActivity) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, s := range a.apps {
		apps = append(apps, &pb.AppUsageEntry{Day: k.day, AppName: k.app, WindowTitle: k.title, ForegroundSeconds: s})
	}
	for day, d := range a.days {
		days = append(days, &pb.DailyActivity{Day: day, ActiveSeconds: d.active, IdleSeconds: d.idle})
	}
	a.apps = map[appKey]int64{}
	a.days = map[string]*dayAcc{}
	return apps, days
}

// restore возвращает дельты в аккумулятор после неудачной отправки, СУММИРУЯ их с
// теми, что накопились за время попытки (не затирая — иначе потерялись бы новые
// сэмплы). На следующем тике отправки уедут вместе.
func (a *activityAggregator) restore(apps []*pb.AppUsageEntry, days []*pb.DailyActivity) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range apps {
		a.apps[appKey{e.Day, e.AppName, e.WindowTitle}] += e.ForegroundSeconds
	}
	for _, d := range days {
		acc := a.days[d.Day]
		if acc == nil {
			acc = &dayAcc{}
			a.days[d.Day] = acc
		}
		acc.active += d.ActiveSeconds
		acc.idle += d.IdleSeconds
	}
}

// reset выбрасывает всё накопленное. Вызывается, когда сбор выключен (privacy):
// данные, набранные до отключения флага и ещё не отправленные, не должны утечь.
func (a *activityAggregator) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.apps = map[appKey]int64{}
	a.days = map[string]*dayAcc{}
}

// empty сообщает, что накопить нечего (нет смысла слать пустой отчёт).
func (a *activityAggregator) empty() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.apps) == 0 && len(a.days) == 0
}
