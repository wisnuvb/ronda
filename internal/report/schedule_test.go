package report

import (
	"context"
	"sync"
	"testing"
	"time"
)

func wib(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, jakarta)
}

func TestSlotIsTheLatestScheduledMomentNotAfterNow(t *testing.T) {
	for _, c := range []struct {
		name    string
		now     time.Time
		kind    Kind
		at      string
		weekday int
		want    time.Time
	}{
		{"daily, already passed today", wib(2026, 11, 5, 9, 0), Daily, "08:00", 0, wib(2026, 11, 5, 8, 0)},
		{"daily, exactly on the minute", wib(2026, 11, 5, 8, 0), Daily, "08:00", 0, wib(2026, 11, 5, 8, 0)},
		{"daily, not yet today: yesterday's", wib(2026, 11, 5, 7, 59), Daily, "08:00", 0, wib(2026, 11, 4, 8, 0)},
		{"daily, across a month boundary", wib(2026, 12, 1, 6, 0), Daily, "08:00", 0, wib(2026, 11, 30, 8, 0)},
		{"weekly Monday, on a Thursday", wib(2026, 11, 5, 9, 0), Weekly, "08:00", 1, wib(2026, 11, 2, 8, 0)},
		{"weekly Monday, on Monday before the time", wib(2026, 11, 2, 7, 0), Weekly, "08:00", 1, wib(2026, 10, 26, 8, 0)},
		{"weekly Monday, on Monday after the time", wib(2026, 11, 2, 8, 30), Weekly, "08:00", 1, wib(2026, 11, 2, 8, 0)},
		{"weekly Sunday", wib(2026, 11, 5, 9, 0), Weekly, "20:30", 0, wib(2026, 11, 1, 20, 30)},
	} {
		if got := slot(c.now, c.kind, c.at, c.weekday); !got.Equal(c.want) {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// The clock of the machine may be in any zone; the schedule is WIB.
	utc := wib(2026, 11, 5, 9, 0).UTC()
	if !slot(utc, Daily, "08:00", 0).Equal(wib(2026, 11, 5, 8, 0)) {
		t.Error("slot must not depend on the zone of the clock it is given")
	}
}

func TestDueNeverSendsOnTheFirstSightingThenSendsEachSlotOnce(t *testing.T) {
	dir := t.TempDir()
	s := &Scheduler{Schedule: Schedule{Daily: "08:00", Weekly: "08:00", WeeklyDay: 1}, Dir: dir}

	if got := s.Due(wib(2026, 11, 5, 8, 30)); len(got) != 0 {
		t.Fatalf("switching reports on at 08:30 must not fire the 08:00 report: %v", got)
	}
	if got := s.Due(wib(2026, 11, 5, 20, 0)); len(got) != 0 {
		t.Fatalf("nothing new is due in the evening: %v", got)
	}
	got := s.Due(wib(2026, 11, 6, 8, 0)) // Friday 08:00: the daily slot only
	if len(got) != 1 || got[0] != Daily {
		t.Fatalf("Friday morning: daily, not weekly: %v", got)
	}
	if got := s.Due(wib(2026, 11, 6, 8, 0)); len(got) != 0 {
		t.Fatalf("the same slot twice: %v", got)
	}
	if got := s.Due(wib(2026, 11, 6, 9, 59)); len(got) != 0 {
		t.Fatalf("still the same slot: %v", got)
	}
	got = s.Due(wib(2026, 11, 9, 8, 1)) // Monday: both
	if len(got) != 2 || got[0] != Daily || got[1] != Weekly {
		t.Fatalf("Monday morning: both: %v", got)
	}
}

func TestACatchUpWindowForRestartsButNoLateReports(t *testing.T) {
	dir := t.TempDir()
	first := &Scheduler{Schedule: Schedule{Daily: "08:00"}, Dir: dir}
	first.Due(wib(2026, 11, 5, 9, 0)) // marks Thursday's slot

	// ronda was off at 08:00 on Friday and is back at 09:30: the report is late, not lost.
	second := &Scheduler{Schedule: Schedule{Daily: "08:00"}, Dir: dir}
	if got := second.Due(wib(2026, 11, 6, 9, 30)); len(got) != 1 {
		t.Fatalf("1.5 h late is still worth sending: %v", got)
	}
	// ...and a restart right after must not send it again.
	third := &Scheduler{Schedule: Schedule{Daily: "08:00"}, Dir: dir}
	if got := third.Due(wib(2026, 11, 6, 9, 31)); len(got) != 0 {
		t.Fatalf("a restart must not repeat a report: %v", got)
	}

	// Off for two days: the missed ones are dropped, not sent as a pile at 23:00.
	late := &Scheduler{Schedule: Schedule{Daily: "08:00"}, Dir: dir}
	if got := late.Due(wib(2026, 11, 8, 23, 0)); len(got) != 0 {
		t.Fatalf("a report 15 h late is noise: %v", got)
	}
	again := &Scheduler{Schedule: Schedule{Daily: "08:00"}, Dir: dir}
	if got := again.Due(wib(2026, 11, 8, 23, 5)); len(got) != 0 {
		t.Fatalf("dropped means dropped: %v", got)
	}
}

func TestDisabledKindsAreNeverDue(t *testing.T) {
	s := &Scheduler{Schedule: Schedule{Weekly: "08:00", WeeklyDay: 1}, Dir: t.TempDir()}
	s.Due(wib(2026, 11, 5, 9, 0))
	for _, k := range s.Due(wib(2026, 11, 20, 9, 0)) {
		if k == Daily {
			t.Fatal("daily is off")
		}
	}
	if (Schedule{}).Enabled() || !(Schedule{Daily: "08:00"}).Enabled() {
		t.Fatal("Enabled")
	}
}

func TestScheduleValidate(t *testing.T) {
	if err := (Schedule{Daily: "08:00", Weekly: "23:59"}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Schedule{{Daily: "8:00"}, {Daily: "24:00"}, {Weekly: "08:60"}, {Weekly: "pagi"}} {
		if bad.Validate() == nil {
			t.Errorf("%+v must be rejected", bad)
		}
	}
}

func TestRunSendsDueReportsAndStopsWithItsContext(t *testing.T) {
	var mu sync.Mutex
	var sent []Kind
	clock := wib(2026, 11, 5, 7, 59)
	s := &Scheduler{
		Schedule: Schedule{Daily: "08:00"}, Dir: t.TempDir(), Tick: 5 * time.Millisecond,
		Now:  func() time.Time { mu.Lock(); defer mu.Unlock(); return clock },
		Send: func(k Kind) { mu.Lock(); sent = append(sent, k); mu.Unlock() },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond) // first sighting at 07:59 (yesterday's slot): marked, nothing sent
	mu.Lock()
	clock = wib(2026, 11, 5, 8, 0)
	mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(sent)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(40 * time.Millisecond) // many more ticks at the same slot
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || sent[0] != Daily {
		t.Fatalf("exactly one daily report: %v", sent)
	}
}
