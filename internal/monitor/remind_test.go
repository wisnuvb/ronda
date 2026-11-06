package monitor

import (
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/model"
)

const minute = int64(60_000)

func TestRemindersDueFollowsTheOffsetsThenRepeatsDaily(t *testing.T) {
	m := New(Options{}) // default offsets: 60, 360, 1440 minutes
	const since = int64(1_000_000)
	for _, c := range []struct {
		elapsedMin int64
		want       int
	}{
		{0, 0}, {59, 0}, {60, 1}, {359, 1}, {360, 2}, {1439, 2}, {1440, 3},
		{1440 + 1439, 3}, {1440 + 1440, 4}, {1440 + 3*1440, 6},
	} {
		if got := m.remindersDue(since, since+c.elapsedMin*minute); got != c.want {
			t.Errorf("after %d min: %d reminders due, want %d", c.elapsedMin, got, c.want)
		}
	}
	if New(Options{RemindAfterMin: []int{}}).remindersDue(since, since+999*minute) != 0 {
		t.Error("an empty list must turn reminders off")
	}
	if m.remindersDue(0, 999*minute) != 0 {
		t.Error("an unknown start (since = 0) must never remind")
	}
	if n := New(Options{RemindAfterMin: []int{30, 30, 5, 0, -3}}).remindersDue(since, since+31*minute); n != 2 {
		t.Errorf("offsets must be sorted, deduplicated and cleaned of nonsense: %d due after 31 min", n)
	}
}

func remindFixture(t *testing.T) (*Monitor, model.Site, *recorder, string, int64) {
	t.Helper()
	m, reg, rec, dir := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	site, err := reg.Add(model.Site{URL: "https://a.example", Name: "A", FailThreshold: 1})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.runtimeLocked(site.ID)
	m.mu.Unlock()
	return m, site, rec, dir, time.Now().Add(-100 * time.Hour).UnixMilli()
}

func bad(at int64, hint string) model.Result {
	return model.Result{Time: at, Kind: model.KindRefused, Error: "koneksi ditolak", Hint: hint}
}

func states(rec *recorder) []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var out []string
	for _, ev := range rec.ev {
		out = append(out, ev.State)
	}
	return out
}

func TestStillDownIsRepeatedOncePerThresholdAndNeverReplayed(t *testing.T) {
	m, site, rec, _, base := remindFixture(t)
	m.observe(site, bad(base, "server mati"), true)
	m.observe(site, bad(base+30*minute, "server mati"), true)
	if got := states(rec); len(got) != 1 || got[0] != "down" {
		t.Fatalf("30 minutes in there is nothing to repeat yet: %v", got)
	}

	m.observe(site, bad(base+61*minute, "port 443 ditolak"), true)
	m.observe(site, bad(base+90*minute, "port 443 ditolak"), true)
	got := states(rec)
	if len(got) != 2 || got[1] != "reminder" {
		t.Fatalf("one reminder at the 1 hour mark, and only one: %v", got)
	}
	ev := rec.ev[1]
	if ev.SiteName != "A" || ev.Since != base || ev.DowntimeSec != 61*60 || ev.Hint != "port 443 ditolak" || ev.Kind != model.KindRefused {
		t.Fatalf("a reminder says how long, since when, and why it is down NOW: %+v", ev)
	}

	// ronda was off, or the site is checked rarely: 25 h later the 6 h and 24 h marks have both passed,
	// yet exactly one message goes out, not a replay of each.
	m.observe(site, bad(base+25*60*minute, "masih mati"), true)
	if got := states(rec); len(got) != 3 || got[2] != "reminder" {
		t.Fatalf("one reminder for the highest mark reached: %v", got)
	}
	m.observe(site, bad(base+26*60*minute, "masih mati"), true)
	if len(states(rec)) != 3 {
		t.Fatal("the same mark must not be announced twice")
	}
	// ...then daily.
	m.observe(site, bad(base+49*60*minute+minute, "masih mati"), true)
	if got := states(rec); len(got) != 4 || got[3] != "reminder" {
		t.Fatalf("a reminder every 24 h after the last mark: %v", got)
	}
}

func TestReminderIsNotArchivedAsAnIncident(t *testing.T) {
	m, site, _, _, base := remindFixture(t)
	m.observe(site, bad(base, "x"), true)
	m.observe(site, bad(base+61*minute, "x"), true)
	m.observe(site, bad(base+7*60*minute, "x"), true)
	for _, ev := range m.Events() {
		if ev.State == "reminder" {
			t.Fatalf("a reminder repeats a fact already archived; it must not be stored again: %+v", ev)
		}
	}
	if n := len(m.Incidents(site.ID)); n != 1 {
		t.Fatalf("one outage is one incident, however many reminders: %d", n)
	}
}

func TestNoReminderWhenTheMonitorsOwnNetworkIsDownOrTheSiteRespondsAgain(t *testing.T) {
	m, site, rec, _, base := remindFixture(t)
	m.observe(site, bad(base, "x"), true)
	m.observe(site, bad(base+2*60*minute, "x"), false) // our network is down: says nothing about the site
	if len(states(rec)) != 1 {
		t.Fatalf("no reminder while the monitor cannot see out: %v", states(rec))
	}
	m.observe(site, model.Result{Time: base + 3*60*minute, OK: true, Kind: model.KindOK}, true) // 1 of 2 successes
	if len(states(rec)) != 1 {
		t.Fatalf("a site that just answered is not 'still down': %v", states(rec))
	}
}

func TestRecoveryRearmsReminders(t *testing.T) {
	m, site, rec, _, base := remindFixture(t)
	m.observe(site, bad(base, "x"), true)
	m.observe(site, bad(base+61*minute, "x"), true) // reminder 1
	up := func(at int64) model.Result { return model.Result{Time: at, OK: true, Kind: model.KindOK} }
	m.observe(site, up(base+70*minute), true)
	m.observe(site, up(base+71*minute), true) // recovered
	m.observe(site, bad(base+200*minute, "x"), true)
	m.observe(site, bad(base+200*minute+61*minute, "x"), true)
	want := []string{"down", "reminder", "up", "down", "reminder"}
	got := states(rec)
	if len(got) != len(want) {
		t.Fatalf("a second outage gets its own reminders: %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v, want %v", got, want)
		}
	}
}

func TestRestartDoesNotRepeatAReminderAlreadySent(t *testing.T) {
	m, site, rec, dir, base := remindFixture(t)
	m.observe(site, bad(base, "x"), true)
	m.observe(site, bad(base+61*minute, "x"), true)
	if got := states(rec); len(got) != 2 {
		t.Fatalf("setup: %v", got)
	}

	rec2 := newRecorder()
	m2 := New(Options{Registry: m.opt.Registry, Checker: m.opt.Checker, Notifier: rec2, DataDir: dir})
	m2.mu.Lock()
	r := m2.runtimeLocked(site.ID)
	m2.mu.Unlock()
	if r.m.Status != StDown || r.reminded != 1 {
		t.Fatalf("the count of reminders sent must survive a restart: status=%s reminded=%d", r.m.Status, r.reminded)
	}
	m2.observe(site, bad(base+65*minute, "x"), true)
	if len(states(rec2)) != 0 {
		t.Fatalf("restart must not send the 1 hour reminder again: %v", states(rec2))
	}
	m2.observe(site, bad(base+361*minute, "x"), true)
	if got := states(rec2); len(got) != 1 || got[0] != "reminder" {
		t.Fatalf("but the next mark still fires: %v", got)
	}
}

func TestEveryEventKindCarriesTheSitesRecipients(t *testing.T) {
	m, reg, rec, _ := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	site, err := reg.Add(model.Site{URL: "https://a.example", Name: "A", FailThreshold: 1, NotifyTo: nil})
	if err != nil {
		t.Fatal(err)
	}
	site.NotifyTo = []string{"klien-a", "default"} // as the registry would hand it to a worker
	m.mu.Lock()
	m.runtimeLocked(site.ID)
	m.mu.Unlock()
	base := time.Now().Add(-100 * time.Hour).UnixMilli()
	m.observe(site, bad(base, "x"), true)
	m.observe(site, bad(base+61*minute, "x"), true)
	up := func(at int64) model.Result { return model.Result{Time: at, OK: true, Kind: model.KindOK} }
	m.observe(site, up(base+70*minute), true)
	res := up(base + 71*minute)
	res.CertNotAfter = res.Time + 3*86400000
	m.observe(site, res, true)
	if got, want := states(rec), []string{"down", "reminder", "up", "cert"}; len(got) != len(want) {
		t.Fatalf("states %v, want %v", got, want)
	}
	for _, ev := range rec.ev {
		if len(ev.To) != 2 || ev.To[0] != "klien-a" || ev.To[1] != "default" {
			t.Errorf("a %q event lost its routing: %v", ev.State, ev.To)
		}
	}
}
