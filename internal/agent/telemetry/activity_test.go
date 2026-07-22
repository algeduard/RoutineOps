package telemetry

import (
	"testing"

	pb "github.com/Floodww/RoutineOps/proto"
)

// appSeconds/daySeconds — хелперы поиска в дренированных дельтах.
func appSeconds(apps []*pb.AppUsageEntry, day, app string) int64 {
	for _, e := range apps {
		if e.Day == day && e.AppName == app {
			return e.ForegroundSeconds
		}
	}
	return -1
}

func titleSeconds(apps []*pb.AppUsageEntry, day, app, title string) int64 {
	for _, e := range apps {
		if e.Day == day && e.AppName == app && e.WindowTitle == title {
			return e.ForegroundSeconds
		}
	}
	return -1
}

func dayActivity(days []*pb.DailyActivity, day string) (active, idle int64) {
	for _, d := range days {
		if d.Day == day {
			return d.ActiveSeconds, d.IdleSeconds
		}
	}
	return -1, -1
}

func TestActivityAggregator_AccumulatesActiveAndIdle(t *testing.T) {
	a := newActivityAggregator()

	// Активный ввод: время идёт в активное И в счётчик foreground-приложения.
	a.record("2026-07-21", "chrome.exe", "", true, 30)
	a.record("2026-07-21", "chrome.exe", "", true, 30)
	a.record("2026-07-21", "code.exe", "", true, 15)
	// Простой: время идёт в idle, приложению НЕ приписывается.
	a.record("2026-07-21", "chrome.exe", "", false, 40)

	apps, days := a.drain()

	if got := appSeconds(apps, "2026-07-21", "chrome.exe"); got != 60 {
		t.Errorf("chrome foreground = %d, want 60", got)
	}
	if got := appSeconds(apps, "2026-07-21", "code.exe"); got != 15 {
		t.Errorf("code foreground = %d, want 15", got)
	}
	active, idle := dayActivity(days, "2026-07-21")
	if active != 75 { // 30+30+15
		t.Errorf("active = %d, want 75", active)
	}
	if idle != 40 {
		t.Errorf("idle = %d, want 40", idle)
	}
}

func TestActivityAggregator_IdleNotAttributedToApp(t *testing.T) {
	a := newActivityAggregator()
	// Простой при известном foreground-окне НЕ должен копить время приложению.
	a.record("2026-07-21", "chrome.exe", "", false, 100)
	apps, days := a.drain()
	if len(apps) != 0 {
		t.Errorf("простойное время приписано приложению: %+v", apps)
	}
	_, idle := dayActivity(days, "2026-07-21")
	if idle != 100 {
		t.Errorf("idle = %d, want 100", idle)
	}
}

func TestActivityAggregator_DrainResets(t *testing.T) {
	a := newActivityAggregator()
	a.record("2026-07-21", "chrome.exe", "", true, 30)
	a.drain()
	if !a.empty() {
		t.Fatal("после drain аккумулятор должен быть пуст")
	}
	apps, days := a.drain()
	if len(apps) != 0 || len(days) != 0 {
		t.Fatalf("повторный drain вернул данные: apps=%v days=%v", apps, days)
	}
}

func TestActivityAggregator_RestoreMergesWithNew(t *testing.T) {
	a := newActivityAggregator()
	a.record("2026-07-21", "chrome.exe", "", true, 30)
	apps, days := a.drain() // имитируем отправку

	// Между drain и restore пришёл новый сэмпл (отправка была в полёте).
	a.record("2026-07-21", "chrome.exe", "", true, 10)
	// Отправка не удалась — возвращаем дельты; они должны СЛОЖИТЬСЯ с новыми.
	a.restore(apps, days)

	apps2, days2 := a.drain()
	if got := appSeconds(apps2, "2026-07-21", "chrome.exe"); got != 40 {
		t.Errorf("после restore chrome = %d, want 40 (30 возвращённых + 10 новых)", got)
	}
	if active, _ := dayActivity(days2, "2026-07-21"); active != 40 {
		t.Errorf("после restore active = %d, want 40", active)
	}
}

func TestActivityAggregator_ResetDropsEverything(t *testing.T) {
	a := newActivityAggregator()
	a.record("2026-07-21", "chrome.exe", "", true, 30)
	a.reset() // сбор выключен — накопленное до отключения не должно утечь
	if !a.empty() {
		t.Fatal("после reset аккумулятор должен быть пуст")
	}
}

func TestActivityAggregator_IgnoresNonPositiveAndEmptyDay(t *testing.T) {
	a := newActivityAggregator()
	a.record("2026-07-21", "chrome.exe", "", true, 0)  // нулевая дельта
	a.record("2026-07-21", "chrome.exe", "", true, -5) // отрицательная
	a.record("", "chrome.exe", "", true, 30)           // пустой день
	if !a.empty() {
		t.Fatalf("аккумулятор должен игнорировать невалидные записи, но не пуст")
	}
}

// Заголовок окна — часть ключа: один процесс с разными заголовками (напр. разные
// вкладки браузера) копится раздельно, а одинаковые заголовки суммируются.
func TestActivityAggregator_WindowTitleIsPartOfKey(t *testing.T) {
	a := newActivityAggregator()
	a.record("2026-07-21", "chrome.exe", "Хабр — Google Chrome", true, 30)
	a.record("2026-07-21", "chrome.exe", "Хабр — Google Chrome", true, 20)
	a.record("2026-07-21", "chrome.exe", "YouTube — Google Chrome", true, 15)

	apps, _ := a.drain()
	if got := titleSeconds(apps, "2026-07-21", "chrome.exe", "Хабр — Google Chrome"); got != 50 {
		t.Errorf("Хабр = %d, want 50", got)
	}
	if got := titleSeconds(apps, "2026-07-21", "chrome.exe", "YouTube — Google Chrome"); got != 15 {
		t.Errorf("YouTube = %d, want 15", got)
	}
}
