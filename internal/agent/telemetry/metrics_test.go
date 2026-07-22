package telemetry

import (
	"testing"
	"time"
)

func TestThroughput(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name                         string
		prevRx, prevTx, curRx, curTx uint64
		prevAt, curAt                time.Time
		wantRx, wantTx               int64
	}{
		{
			name:   "обычная дельта за 10с",
			prevRx: 1000, prevTx: 500, curRx: 11000, curTx: 5500,
			prevAt: base, curAt: base.Add(10 * time.Second),
			wantRx: 1000, wantTx: 500, // (11000-1000)/10, (5500-500)/10
		},
		{
			name:   "сброс счётчика (ребут) → 0, без переполнения",
			prevRx: 1_000_000, prevTx: 900_000, curRx: 100, curTx: 50,
			prevAt: base, curAt: base.Add(5 * time.Second),
			wantRx: 0, wantTx: 0,
		},
		{
			name:   "нулевой интервал → 0 (защита от деления на ноль)",
			prevRx: 100, prevTx: 100, curRx: 200, curTx: 200,
			prevAt: base, curAt: base,
			wantRx: 0, wantTx: 0,
		},
		{
			name:   "отрицательный интервал (часы назад) → 0",
			prevRx: 100, prevTx: 100, curRx: 500, curTx: 500,
			prevAt: base, curAt: base.Add(-time.Second),
			wantRx: 0, wantTx: 0,
		},
		{
			name:   "нет трафика → 0",
			prevRx: 4242, prevTx: 4242, curRx: 4242, curTx: 4242,
			prevAt: base, curAt: base.Add(time.Second),
			wantRx: 0, wantTx: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rx, tx := throughput(c.prevRx, c.prevTx, c.prevAt, c.curRx, c.curTx, c.curAt)
			if rx != c.wantRx || tx != c.wantTx {
				t.Fatalf("throughput = (%d, %d), want (%d, %d)", rx, tx, c.wantRx, c.wantTx)
			}
		})
	}
}

func TestRound2(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{12.3456, 12.35},
		{0, 0},
		{99.999, 100},
		{33.333333, 33.33},
	}
	for _, c := range cases {
		if got := round2(c.in); got != c.want {
			t.Errorf("round2(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
