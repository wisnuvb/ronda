package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wisnuvb/ronda/sitespec"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/history"
	"github.com/wisnuvb/ronda/internal/registry"
	"github.com/wisnuvb/ronda/model"
)

// ---- Machine ----

func TestMachineNeedsConsecutiveFailuresToGoDown(t *testing.T) {
	var m Machine
	if tr := m.Observe(true, 1000, 3, true); tr != NoChange || m.Status != StUp {
		t.Fatalf("first ok: %v %s", tr, m.Status)
	}
	if tr := m.Observe(false, 2000, 3, true); tr != NoChange {
		t.Fatalf("1st fail: %v", tr)
	}
	if tr := m.Observe(true, 3000, 3, true); tr != NoChange || m.Fails != 0 {
		t.Fatalf("a success must reset the streak: %v fails=%d", tr, m.Fails)
	}
	m.Observe(false, 4000, 3, true)
	m.Observe(false, 5000, 3, true)
	if tr := m.Observe(false, 6000, 3, true); tr != WentDown || m.Status != StDown {
		t.Fatalf("3rd consecutive fail: %v %s", tr, m.Status)
	}
	if m.Since != 4000 {
		t.Fatalf("Since = %d, want the first failure of the streak (4000)", m.Since)
	}
	if tr := m.Observe(false, 7000, 3, true); tr != NoChange {
		t.Fatalf("already down must not re-alert: %v", tr)
	}
}

func TestMachineRecoveryNeedsTwoSuccesses(t *testing.T) {
	m := Machine{Status: StDown, Since: 1000}
	if tr := m.Observe(true, 2000, 3, true); tr != NoChange || m.Status != StDown {
		t.Fatalf("1st ok: %v %s", tr, m.Status)
	}
	if tr := m.Observe(false, 3000, 3, true); tr != NoChange || m.Oks != 0 {
		t.Fatalf("failure while recovering resets the count: %v oks=%d", tr, m.Oks)
	}
	m.Observe(true, 4000, 3, true)
	if tr := m.Observe(true, 5000, 3, true); tr != Recovered || m.Status != StUp {
		t.Fatalf("2nd ok: %v %s", tr, m.Status)
	}
}

func TestMachineFirstFailuresFromUnknown(t *testing.T) {
	var m Machine
	m.Observe(false, 1, 2, true)
	if tr := m.Observe(false, 2, 2, true); tr != WentDown {
		t.Fatalf("a site that never worked must still alert: %v", tr)
	}
}

func TestMachineThresholdOne(t *testing.T) {
	var m Machine
	if tr := m.Observe(false, 5, 1, true); tr != WentDown || m.Since != 5 {
		t.Fatalf("%v since=%d", tr, m.Since)
	}
}

func TestMachineHoldsWhenMonitorNetworkIsDown(t *testing.T) {
	var m Machine
	m.Observe(true, 1, 3, true)
	m.Observe(false, 2, 3, false)
	m.Observe(false, 3, 3, false)
	if tr := m.Observe(false, 4, 3, false); tr != Held || m.Status != StUp {
		t.Fatalf("must hold, not alert: %v %s", tr, m.Status)
	}
	// Network back, site still failing: the very next failure confirms it.
	if tr := m.Observe(false, 5, 3, true); tr != WentDown {
		t.Fatalf("after the network returns: %v", tr)
	}
	if m.Since != 4 {
		t.Fatalf("Since = %d: downtime must not start before the held period ended", m.Since)
	}
}

// ---- Control ----

func TestControl(t *testing.T) {
	var calls atomic.Int32
	c := &Control{Addrs: []string{"a", "b"}, TTL: time.Hour, Dial: func(ctx context.Context, addr string) error {
		calls.Add(1)
		if addr == "b" {
			return nil
		}
		return errors.New("down")
	}}
	if !c.Up(context.Background()) {
		t.Fatal("one reachable address is enough")
	}
	// Up returns as soon as one address answers; the other dial may still be in flight. Let it land,
	// or its late increment would look like a second probe.
	for deadline := time.Now().Add(2 * time.Second); calls.Load() < 2 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	n := calls.Load()
	c.Up(context.Background())
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != n {
		t.Fatal("verdict must be cached")
	}

	dead := &Control{Addrs: []string{"a"}, Dial: func(context.Context, string) error { return errors.New("x") }}
	if dead.Up(context.Background()) {
		t.Fatal("no reachable address means down")
	}
	var nilc *Control
	if !nilc.Up(context.Background()) || !(&Control{}).Up(context.Background()) {
		t.Fatal("no control addresses means always up")
	}
}

// ---- Monitor ----

type recorder struct {
	mu sync.Mutex
	ev []model.Event
	ch chan model.Event
}

func newRecorder() *recorder { return &recorder{ch: make(chan model.Event, 16)} }
func (r *recorder) Notify(ev model.Event) {
	r.mu.Lock()
	r.ev = append(r.ev, ev)
	r.mu.Unlock()
	r.ch <- ev
}

func newMonitor(t *testing.T, chk Checker, ctl *Control) (*Monitor, *registry.Registry, *recorder, string) {
	t.Helper()
	dir := t.TempDir()
	reg, err := registry.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := newRecorder()
	m := New(Options{Sites: reg, Checker: chk, Notifier: rec, Control: ctl, DataDir: dir})
	m.jitterMax, m.retryDelay = 0, 5*time.Millisecond
	return m, reg, rec, dir
}

type fakeChecker struct {
	mu    sync.Mutex
	fn    func(s model.Site, n int) bool
	calls map[string]int
}

func (f *fakeChecker) Check(_ context.Context, s model.Site) model.Result {
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[s.ID]++
	n := f.calls[s.ID]
	f.mu.Unlock()
	ok := f.fn(s, n)
	r := model.Result{Time: time.Now().UnixMilli(), OK: ok, LatencyMs: 12, Kind: model.KindOK, Status: 200}
	if !ok {
		r.Kind, r.Status, r.Error, r.Hint = model.KindHTTP5xx, 502, "HTTP 502", "502 Bad Gateway"
	}
	return r
}

func (f *fakeChecker) count(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[id]
}

func wait(t *testing.T, rec *recorder) model.Event {
	t.Helper()
	select {
	case ev := <-rec.ch:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an event")
		return model.Event{}
	}
}

func TestObserveEmitsDownThenUpAndPersists(t *testing.T) {
	m, reg, rec, dir := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	site, _ := reg.Add(model.Site{URL: "https://a.example", Name: "A", FailThreshold: 2})
	m.mu.Lock()
	m.runtimeLocked(site.ID)
	m.mu.Unlock()

	base := time.Now().Add(-time.Hour).UnixMilli() // recent enough to survive the archive's retention
	ok := func(at int64) model.Result {
		return model.Result{Time: at, OK: true, LatencyMs: 80, Kind: model.KindOK}
	}
	bad := func(at int64) model.Result {
		return model.Result{Time: at, Kind: model.KindRefused, Error: "koneksi ditolak", Hint: "server mati"}
	}

	if d := m.observe(site, ok(base+1_000), true); d != 60*time.Second {
		t.Fatalf("healthy delay = %v", d)
	}
	if d := m.observe(site, bad(base+61_000), true); d != 5*time.Millisecond {
		t.Fatalf("first failure must retry fast, delay = %v", d)
	}
	if len(rec.ev) != 0 {
		t.Fatal("one failure must not alert")
	}
	m.observe(site, bad(base+66_000), true)
	if len(rec.ev) != 1 || rec.ev[0].State != "down" || rec.ev[0].Kind != model.KindRefused || rec.ev[0].Since != base+61_000 || rec.ev[0].Fails != 2 {
		t.Fatalf("down event: %+v", rec.ev)
	}
	if d := m.observe(site, bad(base+126_000), true); d != 60*time.Second || len(rec.ev) != 1 {
		t.Fatalf("down state polls at the normal interval without re-alerting: %v %d", d, len(rec.ev))
	}

	m.observe(site, ok(base+186_000), true)
	m.observe(site, ok(base+191_000), true)
	if len(rec.ev) != 2 || rec.ev[1].State != "up" || rec.ev[1].DowntimeSec != 130 || rec.ev[1].LatencyMs != 80 {
		t.Fatalf("up event: %+v", rec.ev)
	}

	if v, _ := m.View(site.ID); v.State != StUp || len(v.Recent) != 6 || v.Last == nil {
		t.Fatalf("view: %+v", v)
	}
	if _, err := os.Stat(filepath.Join(dir, "events.jsonl")); err != nil {
		t.Fatal("the event archive was not written:", err)
	}

	// A restart keeps the verdict, so a site that is down does not re-alert as "new".
	m.observe(site, bad(base+300_000), true)
	m.observe(site, bad(base+305_000), true)
	m2 := New(Options{Sites: reg, Checker: &fakeChecker{fn: func(model.Site, int) bool { return false }}, DataDir: dir})
	m2.mu.Lock()
	r := m2.runtimeLocked(site.ID)
	m2.mu.Unlock()
	if r.m.Status != StDown || r.m.Since != base+300_000 {
		t.Fatalf("restored machine: %+v", r.m)
	}
	if len(m2.Events()) != 3 {
		t.Fatalf("restored events: %d", len(m2.Events()))
	}
}

func TestObserveHoldsAlertWhileMonitorNetworkIsDown(t *testing.T) {
	m, reg, rec, _ := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return false }}, nil)
	site, _ := reg.Add(model.Site{URL: "https://a.example", FailThreshold: 2})
	m.mu.Lock()
	m.runtimeLocked(site.ID)
	m.mu.Unlock()

	for i := int64(1); i <= 5; i++ {
		m.observe(site, model.Result{Time: i * 1000, Kind: model.KindTimeoutConnect}, false)
	}
	if len(rec.ev) != 0 {
		t.Fatalf("no alert may fire while our own network is down: %+v", rec.ev)
	}
	m.observe(site, model.Result{Time: 6000, Kind: model.KindTimeoutConnect}, true)
	if len(rec.ev) != 1 || rec.ev[0].State != "down" {
		t.Fatalf("alert should fire once the network is back: %+v", rec.ev)
	}
}

func TestLifecycleGoesDownExactlyOnce(t *testing.T) {
	// Always failing: the scheduler must confirm after 3 failures, alert once,
	// then fall back to the normal interval instead of hammering a dead site.
	chk := &fakeChecker{fn: func(model.Site, int) bool { return false }}
	m, reg, rec, _ := newMonitor(t, chk, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()

	site, err := reg.Add(model.Site{URL: "https://a.example", Name: "A", IntervalSec: 10})
	if err != nil {
		t.Fatal(err)
	}
	down := wait(t, rec)
	if down.State != "down" || down.SiteID != site.ID || down.Fails != 3 || down.Kind != model.KindHTTP5xx {
		t.Fatalf("down: %+v", down)
	}

	time.Sleep(100 * time.Millisecond)
	if n := chk.count(site.ID); n != 3 {
		t.Fatalf("checks after going down = %d, want exactly 3 (next one is a full interval away)", n)
	}
	select {
	case ev := <-rec.ch:
		t.Fatalf("unexpected second event: %+v", ev)
	default:
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestPauseResumeEditDelete(t *testing.T) {
	chk := &fakeChecker{fn: func(model.Site, int) bool { return true }}
	m, reg, _, _ := newMonitor(t, chk, nil)
	start(t, m)

	site, _ := reg.Add(model.Site{URL: "https://a.example", IntervalSec: 10})
	waitFor(t, func() bool { return chk.count(site.ID) >= 1 })
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StUp })

	// Pause: the worker stops and the state says so.
	reg.SetPaused(site.ID, true)
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StPaused })
	n := chk.count(site.ID)
	time.Sleep(50 * time.Millisecond)
	if chk.count(site.ID) != n {
		t.Fatal("paused site is still being checked")
	}

	// Resume: a fresh verdict is reached.
	reg.SetPaused(site.ID, false)
	waitFor(t, func() bool { return chk.count(site.ID) > n })
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StUp })

	// Changing the URL discards the old verdict.
	reg.Patch(site.ID, []byte(`{"url":"https://b.example"}`))
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.URL == "https://b.example" && v.State == StUp })

	// Delete: the runtime goes away.
	reg.Delete(site.ID)
	waitFor(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.rt) == 0 && len(m.workers) == 0 })
}

func TestConcurrencyLimit(t *testing.T) {
	var cur, peak atomic.Int32
	chk := checkerFunc(func(ctx context.Context, s model.Site) model.Result {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		cur.Add(-1)
		return model.Result{Time: time.Now().UnixMilli(), OK: true, Kind: model.KindOK}
	})
	dir := t.TempDir()
	reg, _ := registry.Open(dir)
	m := New(Options{Sites: reg, Checker: chk, MaxConcurrent: 2})
	m.jitterMax = 0
	for i := 0; i < 8; i++ {
		reg.Add(model.Site{URL: "https://a.example"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	time.Sleep(300 * time.Millisecond)
	cancel()
	if peak.Load() > 2 || peak.Load() == 0 {
		t.Fatalf("peak concurrency = %d, want 1..2", peak.Load())
	}
}

// start runs the monitor for the rest of the test and, at cleanup, stops it and waits until it has
// finished. Without the wait a late write to the data directory races the temp-dir removal.
func start(t *testing.T, m *Monitor) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("monitor did not stop")
		}
	})
}

type checkerFunc func(ctx context.Context, s model.Site) model.Result

func (f checkerFunc) Check(ctx context.Context, s model.Site) model.Result { return f(ctx, s) }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// ---- history and incidents ----

func TestObserveFeedsHistoryExceptWhenOurNetworkIsDown(t *testing.T) {
	m, reg, _, _ := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	hist := history.Open("")
	m.opt.History = hist
	site, _ := reg.Add(model.Site{URL: "https://a.example", FailThreshold: 3})
	m.mu.Lock()
	m.runtimeLocked(site.ID)
	m.mu.Unlock()

	now := time.Now().UnixMilli()
	m.observe(site, model.Result{Time: now, OK: true, LatencyMs: 40, Kind: model.KindOK}, true)
	m.observe(site, model.Result{Time: now + 1, Kind: model.KindTimeoutConnect}, true)  // a real failure
	m.observe(site, model.Result{Time: now + 2, Kind: model.KindTimeoutConnect}, false) // our network: ignored

	st := hist.Full(site.ID)
	if st.Checks24h != 2 || *st.Uptime24h != 50 {
		t.Fatalf("checks=%d uptime=%v, want 2 checks at 50%%: a failure caused by our own network must not count", st.Checks24h, *st.Uptime24h)
	}
	if v, _ := m.View(site.ID); v.Uptime24h == nil || *v.Uptime24h != 50 || v.P95Ms != 40 {
		t.Fatalf("view stats: %+v", v.Stats)
	}
	if vs := m.Views(); len(vs[0].Strip) != 2 || vs[0].P95Ms != 0 {
		t.Fatalf("list view must carry the strip and skip the expensive stats: %+v", vs[0].Stats)
	}
}

func TestHistoryDroppedWithSite(t *testing.T) {
	chk := &fakeChecker{fn: func(model.Site, int) bool { return true }}
	m, reg, _, _ := newMonitor(t, chk, nil)
	m.opt.History = history.Open("")
	start(t, m)

	site, _ := reg.Add(model.Site{URL: "https://a.example", IntervalSec: 10})
	waitFor(t, func() bool { return m.opt.History.Full(site.ID).Checks24h >= 1 })

	reg.Patch(site.ID, []byte(`{"url":"https://b.example"}`))
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.URL == "https://b.example" })
	// A new target starts with a clean record (it may already hold one fresh check).
	waitFor(t, func() bool { return m.opt.History.Full(site.ID).Checks24h <= 1 })

	reg.Delete(site.ID)
	waitFor(t, func() bool { return m.opt.History.Full(site.ID).Uptime24h == nil })
}

func TestIncidents(t *testing.T) {
	m, reg, _, _ := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	a, _ := reg.Add(model.Site{URL: "https://a.example", Name: "A"})
	b, _ := reg.Add(model.Site{URL: "https://b.example", Name: "B"})
	c, _ := reg.Add(model.Site{URL: "https://c.example", Name: "C"})

	ev := func(state string, id, name string, at, since int64) model.Event {
		return model.Event{State: state, SiteID: id, SiteName: name, Time: at, Since: since, Kind: model.KindRefused, Hint: "mati"}
	}
	// Oldest first, as they happen. A: down then recovered. B: still down. C: down, never confirmed up.
	for _, e := range []model.Event{
		ev("down", a.ID, "A", 3_000, 1_000),
		ev("up", a.ID, "A", 5_000, 0),
		ev("down", b.ID, "B", 7_000, 6_000),
		ev("down", c.ID, "C", 9_000, 8_000),
	} {
		m.evlog.Append(e)
	}
	m.mu.Lock()
	m.runtimeLocked(b.ID).m = Machine{Status: StDown, Since: 6_000}
	m.runtimeLocked(c.ID).m = Machine{Status: StUp} // paused/edited while down, then back up: no "up" event
	m.mu.Unlock()

	got := m.Incidents("")
	if len(got) != 3 {
		t.Fatalf("incidents = %+v", got)
	}
	if got[0].SiteName != "C" || got[0].Ongoing || got[0].Ended != 0 {
		t.Fatalf("C must be closed without an end time, not ongoing forever: %+v", got[0])
	}
	if got[1].SiteName != "B" || !got[1].Ongoing || got[1].Started != 6_000 {
		t.Fatalf("B: %+v", got[1])
	}
	if got[2].SiteName != "A" || got[2].Ongoing || got[2].Started != 1_000 || got[2].Ended != 5_000 || got[2].DurationSec != 4 {
		t.Fatalf("A: %+v", got[2])
	}

	if only := m.Incidents(a.ID); len(only) != 1 || only[0].SiteName != "A" {
		t.Fatalf("filter by site: %+v", only)
	}
}

func TestCheckSite(t *testing.T) {
	chk := &fakeChecker{fn: func(model.Site, int) bool { return true }}
	m, reg, _, _ := newMonitor(t, chk, nil)
	site, _ := reg.Add(model.Site{URL: "https://a.example"})
	if r, err := m.CheckSite(context.Background(), site.ID); err != nil || !r.OK {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := m.CheckSite(context.Background(), "tidak-ada"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if v, _ := m.View(site.ID); v.State != StUnknown {
		t.Fatalf("an ad-hoc check must not change state: %s", v.State)
	}
}

// ---- certificate warnings ----

const day = int64(24 * time.Hour / time.Millisecond)

func certResult(at, daysLeft int64) model.Result {
	return model.Result{Time: at, OK: true, Kind: model.KindOK, LatencyMs: 50, CertNotAfter: at + daysLeft*day + day/2}
}

func TestCertWarningFiresOncePerThreshold(t *testing.T) {
	m, reg, rec, _ := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	site, _ := reg.Add(model.Site{URL: "https://a.example", Name: "A"})
	m.mu.Lock()
	m.runtimeLocked(site.ID)
	m.mu.Unlock()

	now := time.Now().UnixMilli()
	step := func(daysLeft int64) { m.observe(site, certResult(now, daysLeft), true); now += 1000 }

	step(60) // far from expiry: silent
	step(20)
	if len(rec.ev) != 0 {
		t.Fatalf("no warning above the largest threshold: %+v", rec.ev)
	}
	step(13)
	if len(rec.ev) != 1 || rec.ev[0].State != "cert" || rec.ev[0].DaysLeft != 13 || rec.ev[0].Kind != "cert_expiring" {
		t.Fatalf("crossing 14 days: %+v", rec.ev)
	}
	for i := 0; i < 5; i++ { // every later check must stay quiet
		step(13)
		step(12)
	}
	if len(rec.ev) != 1 {
		t.Fatalf("a threshold must be announced once, got %d events", len(rec.ev))
	}
	step(6)
	step(2)
	step(0) // expires today
	if len(rec.ev) != 4 {
		t.Fatalf("7, 3 and 1 day thresholds: got %d events: %+v", len(rec.ev), rec.ev)
	}
	if got := []int{rec.ev[1].DaysLeft, rec.ev[2].DaysLeft, rec.ev[3].DaysLeft}; got[0] != 6 || got[1] != 2 || got[2] != 0 {
		t.Fatalf("days left in the events: %v", got)
	}

	// A renewed certificate re-arms the warnings.
	step(89)
	step(10)
	if len(rec.ev) != 5 || rec.ev[4].DaysLeft != 10 {
		t.Fatalf("after renewal the next crossing must warn again: %d events", len(rec.ev))
	}
}

func TestCertWarningStateSurvivesRestartAndCanBeDisabled(t *testing.T) {
	chk := &fakeChecker{fn: func(model.Site, int) bool { return true }}
	m, reg, rec, dir := newMonitor(t, chk, nil)
	site, _ := reg.Add(model.Site{URL: "https://a.example"})
	m.mu.Lock()
	m.runtimeLocked(site.ID)
	m.mu.Unlock()
	now := time.Now().UnixMilli()
	m.observe(site, certResult(now, 5), true)
	if len(rec.ev) != 1 {
		t.Fatalf("first warning: %+v", rec.ev)
	}

	rec2 := newRecorder()
	m2 := New(Options{Sites: reg, Checker: chk, Notifier: rec2, DataDir: dir})
	m2.mu.Lock()
	m2.runtimeLocked(site.ID)
	m2.mu.Unlock()
	m2.observe(site, certResult(now+1000, 5), true)
	if len(rec2.ev) != 0 {
		t.Fatalf("a restart must not repeat a warning already sent: %+v", rec2.ev)
	}

	off := New(Options{Sites: reg, Checker: chk, Notifier: newRecorder(), CertWarnDays: []int{}})
	off.mu.Lock()
	off.runtimeLocked(site.ID)
	off.mu.Unlock()
	off.observe(site, certResult(now, 1), true)
	if len(off.Events()) != 0 {
		t.Fatal("an empty list must turn the warnings off")
	}
}

func TestCertTierMapping(t *testing.T) {
	m := New(Options{Sites: nil, CertWarnDays: []int{3, 14, 7, 7, 1, 0, -2}})
	for days, want := range map[int]int{100: 0, 15: 0, 14: 14, 10: 14, 7: 7, 5: 7, 3: 3, 2: 3, 1: 1, 0: 1, -1: 1} {
		if got := m.certTier(days); got != want {
			t.Errorf("certTier(%d) = %d, want %d", days, got, want)
		}
	}
}

// ---- maintenance windows ----

func TestMaintenanceSilencesThenResumesByItself(t *testing.T) {
	chk := &fakeChecker{fn: func(model.Site, int) bool { return true }}
	m, reg, _, _ := newMonitor(t, chk, nil)
	start(t, m)

	site, _ := reg.Add(model.Site{URL: "https://a.example", IntervalSec: 10})
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StUp })

	if _, err := reg.SetMaintenance(site.ID, time.Now().Add(600*time.Millisecond).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StMaint })
	n := chk.count(site.ID)
	time.Sleep(100 * time.Millisecond)
	if chk.count(site.ID) != n {
		t.Fatal("a site in maintenance must not be checked")
	}

	// Nobody touches it: the window ends and checking resumes on its own.
	waitFor(t, func() bool { return chk.count(site.ID) > n })
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StUp })
}

func TestEndingMaintenanceEarlyResumesAtOnce(t *testing.T) {
	chk := &fakeChecker{fn: func(model.Site, int) bool { return true }}
	m, reg, _, _ := newMonitor(t, chk, nil)
	start(t, m)

	site, _ := reg.Add(model.Site{URL: "https://a.example", IntervalSec: 10})
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StUp })
	reg.SetMaintenance(site.ID, time.Now().Add(time.Hour).UnixMilli())
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StMaint })
	n := chk.count(site.ID)

	reg.SetMaintenance(site.ID, 0)
	waitFor(t, func() bool { return chk.count(site.ID) > n })
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StUp })
}

func TestMaintenanceDoesNotAlertAndPausedWins(t *testing.T) {
	chk := &fakeChecker{fn: func(model.Site, int) bool { return false }}
	m, reg, rec, _ := newMonitor(t, chk, nil)
	start(t, m)

	site, _ := reg.Add(model.Site{URL: "https://a.example", IntervalSec: 10})
	reg.SetMaintenance(site.ID, time.Now().Add(time.Hour).UnixMilli())
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StMaint })
	time.Sleep(150 * time.Millisecond)
	if len(rec.ev) != 0 {
		t.Fatalf("no alert may fire during maintenance: %+v", rec.ev)
	}

	reg.SetPaused(site.ID, true)
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StPaused })
}

func TestHealthy(t *testing.T) {
	m, reg, _, _ := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	if !m.Healthy() {
		t.Fatal("nothing to check is healthy")
	}
	site, _ := reg.Add(model.Site{URL: "https://a.example", IntervalSec: 60})
	m.mu.Lock()
	m.workers[site.ID] = &worker{site: site, cancel: func() {}}
	m.mu.Unlock()

	m.lastCheck.Store(time.Now().UnixMilli())
	if !m.Healthy() {
		t.Fatal("a check just ran")
	}
	m.lastCheck.Store(time.Now().Add(-10 * time.Minute).UnixMilli())
	if m.Healthy() {
		t.Fatal("no check for 10 minutes with a 60s interval is a stall; the heartbeat must not vouch for it")
	}
}

// ---- weekly maintenance, driven by a clock we control ----

func wibTime(day int, hhmm string) time.Time {
	h := int(hhmm[0]-'0')*10 + int(hhmm[1]-'0')
	m := int(hhmm[3]-'0')*10 + int(hhmm[4]-'0')
	return time.Date(2026, 11, day, h, m, 0, 0, jakarta) // 2026-11-07 is a Saturday
}

func TestWeeklyWindowSilencesAndResumesOnSchedule(t *testing.T) {
	chk := &fakeChecker{fn: func(model.Site, int) bool { return true }}
	m, reg, rec, _ := newMonitor(t, chk, nil)
	site, _ := reg.Add(model.Site{URL: "https://a.example", IntervalSec: 10})
	// Saturdays 02:00, two hours.
	if _, err := reg.Patch(site.ID, []byte(`{"maintenance_weekly":[{"days":[6],"start":"02:00","minutes":120}]}`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	clock := wibTime(6, "23:00") // Friday night, before the window
	m.now = func() time.Time { return clock }

	next := m.reconcile(ctx, false)
	if !next.Equal(wibTime(7, "02:00")) {
		t.Fatalf("before the window the scheduler must ask to be woken at its start (Sat 02:00), got %v", next)
	}
	waitFor(t, func() bool { return chk.count(site.ID) >= 1 }) // checking normally
	if v, _ := m.View(site.ID); v.State == StMaint || v.NextMaintenance != wibTime(7, "02:00").UnixMilli() {
		t.Fatalf("before the window: state=%s next=%d", v.State, v.NextMaintenance)
	}

	clock = wibTime(7, "02:00") // the window begins
	next = m.reconcile(ctx, false)
	if !next.Equal(wibTime(7, "04:00")) {
		t.Fatalf("inside the window the wake-up is its end, got %v", next)
	}
	v, _ := m.View(site.ID)
	if v.State != StMaint || v.MaintenanceEnds != wibTime(7, "04:00").UnixMilli() {
		t.Fatalf("inside the window: state=%s ends=%d", v.State, v.MaintenanceEnds)
	}
	n := chk.count(site.ID)
	time.Sleep(60 * time.Millisecond)
	if chk.count(site.ID) != n {
		t.Fatal("no checks during the window")
	}

	clock = wibTime(7, "04:00") // it ends
	next = m.reconcile(ctx, false)
	if !next.Equal(wibTime(14, "02:00")) {
		t.Fatalf("after the window the next wake-up is next Saturday, got %v", next)
	}
	waitFor(t, func() bool { return chk.count(site.ID) > n })
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StUp })
	if len(rec.ev) != 0 {
		t.Fatalf("a scheduled window must never raise an alert: %+v", rec.ev)
	}
}

func TestSkippedRunIsNotMaintenanceButTheNextOneIs(t *testing.T) {
	chk := &fakeChecker{fn: func(model.Site, int) bool { return true }}
	m, reg, _, _ := newMonitor(t, chk, nil)
	site, _ := reg.Add(model.Site{URL: "https://a.example", IntervalSec: 10})
	skip := wibTime(7, "04:00").UnixMilli() // this Saturday's run was ended by hand
	body := fmt.Sprintf(`{"maintenance_weekly":[{"days":[6],"start":"02:00","minutes":120}],"maintenance_skip_until":%d}`, skip)
	if _, err := reg.Patch(site.ID, []byte(body)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	m.now = func() time.Time { return wibTime(7, "03:00") } // inside the skipped run
	next := m.reconcile(ctx, false)
	waitFor(t, func() bool { return chk.count(site.ID) >= 1 })
	if v, _ := m.View(site.ID); v.State == StMaint {
		t.Fatal("a run that was ended by hand must not silence the site again")
	}
	if !next.Equal(wibTime(14, "02:00")) {
		t.Fatalf("the next wake-up is next week's run, got %v", next)
	}

	m.now = func() time.Time { return wibTime(14, "03:00") }
	m.reconcile(ctx, false)
	if v, _ := m.View(site.ID); v.State != StMaint {
		t.Fatalf("next week's run must still happen, state=%s", v.State)
	}
}

func TestPausedSiteIgnoresTheSchedule(t *testing.T) {
	m, reg, _, _ := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	site, _ := reg.Add(model.Site{URL: "https://a.example", IntervalSec: 10})
	reg.Patch(site.ID, []byte(`{"maintenance_weekly":[{"days":[6],"start":"02:00","minutes":120}]}`))
	reg.SetPaused(site.ID, true)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	m.now = func() time.Time { return wibTime(7, "03:00") }
	if next := m.reconcile(ctx, false); !next.IsZero() {
		t.Fatalf("a paused site needs no wake-up, got %v", next)
	}
	if v, _ := m.View(site.ID); v.State != StPaused {
		t.Fatalf("paused wins over maintenance, got %s", v.State)
	}
}

// ---- the permanent archive ----

func TestHistoryOutlivesTheOldThreeHundredEventCap(t *testing.T) {
	m, reg, _, dir := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	site, _ := reg.Add(model.Site{URL: "https://a.example", Name: "A"})

	// A server dying takes dozens of sites down and back: far more than 300 events in a few outages.
	for i := 0; i < 500; i++ {
		at := time.Now().Add(-time.Duration(1000-2*i) * time.Minute).UnixMilli()
		m.evlog.Append(model.Event{State: "down", SiteID: site.ID, SiteName: "A", Time: at, Since: at, Kind: model.KindRefused})
		m.evlog.Append(model.Event{State: "up", SiteID: site.ID, SiteName: "A", Time: at + 60_000})
	}
	if got := len(m.Incidents(site.ID)); got != 500 {
		t.Fatalf("all 500 incidents must still be there, got %d", got)
	}
	if got := len(m.Events()); got != 300 {
		t.Fatalf("the raw event view stays capped at 300 for the API, got %d", got)
	}

	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m2 := New(Options{Sites: reg, Checker: &fakeChecker{fn: func(model.Site, int) bool { return true }}, DataDir: dir})
	defer m2.Close()
	if got := len(m2.Incidents(site.ID)); got != 500 {
		t.Fatalf("after a restart: %d incidents", got)
	}
}

func TestIncidentsPagination(t *testing.T) {
	m, reg, _, _ := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	site, _ := reg.Add(model.Site{URL: "https://a.example", Name: "A"})
	base := time.Now().Add(-48 * time.Hour).UnixMilli()
	for i := 0; i < 7; i++ { // incident i starts at base+i*1000 s
		at := base + int64(i)*1_000_000
		m.evlog.Append(model.Event{State: "down", SiteID: site.ID, SiteName: "A", Time: at, Since: at})
		m.evlog.Append(model.Event{State: "up", SiteID: site.ID, SiteName: "A", Time: at + 1000})
	}

	p1, more := m.IncidentsPage(site.ID, 0, 3)
	if len(p1) != 3 || !more || p1[0].Started != base+6_000_000 || p1[2].Started != base+4_000_000 {
		t.Fatalf("first page is the newest three: %+v more=%v", p1, more)
	}
	p2, more := m.IncidentsPage(site.ID, p1[2].Started, 3)
	if len(p2) != 3 || !more || p2[0].Started != base+3_000_000 {
		t.Fatalf("second page continues strictly before the last one seen: %+v", p2)
	}
	p3, more := m.IncidentsPage(site.ID, p2[2].Started, 3)
	if len(p3) != 1 || more || p3[0].Started != base {
		t.Fatalf("last page: %+v more=%v", p3, more)
	}
	if page, more := m.IncidentsPage(site.ID, 0, 100); len(page) != 7 || more {
		t.Fatalf("a big limit returns everything: %d %v", len(page), more)
	}
	if page, _ := m.IncidentsPage(site.ID, 0, 0); len(page) != 7 {
		t.Fatalf("limit 0 means the default of 50: %d", len(page))
	}
}

// ---- the public status page ----

func publicFixture(t *testing.T) (*Monitor, *registry.Registry, map[string]model.Site) {
	t.Helper()
	m, reg, _, _ := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	m.opt.History = history.Open("")
	sites := map[string]model.Site{}
	add := func(name, url string, public bool) {
		s, err := reg.Add(model.Site{URL: url, Name: name, Public: public})
		if err != nil {
			t.Fatal(err)
		}
		sites[name] = s
		m.mu.Lock()
		m.runtimeLocked(s.ID)
		m.mu.Unlock()
	}
	add("Toko", "https://rahasia-internal.example/admin", true)
	add("Blog", "https://blog.example", true)
	add("Panel Admin", "https://panel.internal.example", false) // never shown
	add("Lama", "https://lama.example", true)                   // paused: never shown
	reg.SetPaused(sites["Lama"].ID, true)
	return m, reg, sites
}

func setState(m *Monitor, s model.Site, state string) {
	m.mu.Lock()
	m.runtimeLocked(s.ID).m = Machine{Status: state, Since: time.Now().UnixMilli()}
	m.mu.Unlock()
}

func TestPublicStatusShowsOnlyOptedInActiveSites(t *testing.T) {
	m, _, sites := publicFixture(t)
	setState(m, sites["Toko"], StUp)
	setState(m, sites["Blog"], StUp)

	ps := m.PublicStatus()
	var names []string
	for _, s := range ps.Sites {
		names = append(names, s.Name)
	}
	if fmt.Sprint(names) != "[Blog Toko]" {
		t.Fatalf("only the two public, active sites, sorted by name: %v", names)
	}
	if ps.Overall != "operational" {
		t.Fatalf("overall = %s", ps.Overall)
	}
	for _, s := range ps.Sites {
		if len(s.Days) != 90 {
			t.Fatalf("90 days of bars, got %d", len(s.Days))
		}
	}
}

func TestPublicStatusLeaksNothingInternal(t *testing.T) {
	m, _, sites := publicFixture(t)
	setState(m, sites["Toko"], StDown)
	setState(m, sites["Blog"], StUp)
	now := time.Now().UnixMilli()
	m.evlog.Append(model.Event{State: "down", SiteID: sites["Toko"].ID, SiteName: "Toko", URL: "https://rahasia-internal.example/admin",
		Kind: model.KindRefused, Hint: "Connection refused to 10.0.0.5:8443 (admin panel)", Error: "dial tcp 10.0.0.5:8443", Time: now, Since: now})
	// Another site's incident, which is not public.
	m.evlog.Append(model.Event{State: "down", SiteID: sites["Panel Admin"].ID, SiteName: "Panel Admin", Kind: model.KindRefused, Time: now, Since: now})

	raw, err := json.Marshal(m.PublicStatus())
	if err != nil {
		t.Fatal(err)
	}
	out := string(raw)
	for _, secret := range []string{"rahasia-internal", "10.0.0.5", "8443", "admin panel", "https://", "http://", "dial tcp",
		sites["Toko"].ID, sites["Blog"].ID, "Panel Admin", "hint", "url", "site_id"} {
		if strings.Contains(out, secret) {
			t.Fatalf("the public JSON must not contain %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "Connection refused") {
		t.Fatalf("the general kind of trouble is fine to show:\n%s", out)
	}
}

func TestPublicOverallStates(t *testing.T) {
	m, _, sites := publicFixture(t)
	cases := []struct {
		toko, blog, want string
	}{
		{StUp, StUp, "operational"},
		{StDown, StUp, "partial"},
		{StDown, StDown, "outage"},
		{StMaint, StMaint, "maintenance"},
		{StMaint, StUp, "operational"},
		{StUnknown, StUp, "operational"},
	}
	for _, c := range cases {
		setState(m, sites["Toko"], c.toko)
		setState(m, sites["Blog"], c.blog)
		if got := m.PublicStatus().Overall; got != c.want {
			t.Errorf("toko=%s blog=%s: overall = %s, want %s", c.toko, c.blog, got, c.want)
		}
	}
	empty, _, _, _ := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	if got := empty.PublicStatus().Overall; got != "none" {
		t.Errorf("no public sites: %s", got)
	}
}

func TestPublicIncidentsAreRecentAndCapped(t *testing.T) {
	m, _, sites := publicFixture(t)
	setState(m, sites["Toko"], StUp)
	now := time.Now()
	add := func(ago time.Duration, dur time.Duration) {
		at := now.Add(-ago).UnixMilli()
		m.evlog.Append(model.Event{State: "down", SiteID: sites["Toko"].ID, SiteName: "Toko", Kind: model.KindHTTP5xx, Time: at, Since: at})
		m.evlog.Append(model.Event{State: "up", SiteID: sites["Toko"].ID, SiteName: "Toko", Time: at + dur.Milliseconds()})
	}
	add(30*24*time.Hour, time.Minute) // a month ago: outside the 14 days
	add(5*24*time.Hour, 3*time.Minute)
	got := m.PublicStatus().Incidents
	if len(got) != 1 || got[0].Kind != "Server error (5xx)" || got[0].DurationSec != 180 {
		t.Fatalf("only the recent incident, with a general kind: %+v", got)
	}

	for i := 0; i < 60; i++ {
		add(time.Duration(i+1)*time.Hour, time.Minute)
	}
	if got := m.PublicStatus().Incidents; len(got) != publicIncidentLimit {
		t.Fatalf("capped at %d, got %d", publicIncidentLimit, len(got))
	}
}

// ---- push sites: a job pings us, and silence is the failure ----

func newPushMonitor(t *testing.T, interval, grace time.Duration) (*Monitor, *registry.Registry, *recorder) {
	t.Helper()
	m, reg, rec, _ := newMonitor(t, &fakeChecker{fn: func(model.Site, int) bool { return true }}, nil)
	m.pushPeriod = func(model.Site) (time.Duration, time.Duration) { return interval, grace }
	m.pingGap = 30 * time.Millisecond // the real gap is a second; these tests run on a millisecond scale
	return m, reg, rec
}

func noEventWithin(t *testing.T, rec *recorder, d time.Duration, why string) {
	t.Helper()
	select {
	case ev := <-rec.ch:
		t.Fatalf("%s, but got %s %s", why, ev.State, ev.Kind)
	case <-time.After(d):
	}
}

func TestPushSiteGoesDownOnSilenceAndRecoversOnTheFirstPing(t *testing.T) {
	m, reg, rec := newPushMonitor(t, 150*time.Millisecond, 0)
	site, _ := reg.Add(model.Site{Type: "push", Name: "Backup"})
	start(t, m)

	down := wait(t, rec)
	if down.State != "down" || down.Kind != model.KindMissed || down.SiteID != site.ID || !strings.Contains(down.Hint, "ping") {
		t.Fatalf("silence is a failure, and the first miss is enough by default: %+v", down)
	}
	if m.Ping(site.PushToken, false) != PingAccepted {
		t.Fatal("ping not accepted")
	}
	up := wait(t, rec)
	if up.State != "up" || up.SiteID != site.ID {
		t.Fatalf("ONE ping must be enough to recover (a daily job cannot be asked to prove itself twice): %+v", up)
	}
	if v, _ := m.View(site.ID); v.State != StUp || v.LastPing == 0 || v.PingDeadline <= v.LastPing {
		t.Fatalf("view: state=%s last=%d deadline=%d", v.State, v.LastPing, v.PingDeadline)
	}
}

func TestRegularPingsKeepASiteUp(t *testing.T) {
	m, reg, rec := newPushMonitor(t, 300*time.Millisecond, 0)
	site, _ := reg.Add(model.Site{Type: "push"})
	start(t, m)

	for i := 0; i < 8; i++ {
		time.Sleep(100 * time.Millisecond)
		m.Ping(site.PushToken, false)
	}
	noEventWithin(t, rec, 50*time.Millisecond, "a job that keeps pinging must raise nothing")
	if v, _ := m.View(site.ID); v.State != StUp {
		t.Fatalf("state = %s", v.State)
	}
}

func TestJobReportingFailureIsDownAtOnce(t *testing.T) {
	m, reg, rec := newPushMonitor(t, time.Hour, 0)
	site, _ := reg.Add(model.Site{Type: "push"})
	start(t, m)
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.PingDeadline != 0 }) // the worker is running

	m.Ping(site.PushToken, true)
	ev := wait(t, rec)
	if ev.State != "down" || ev.Kind != model.KindReported {
		t.Fatalf("a job that says it failed must not have to wait out the whole period: %+v", ev)
	}
	m.Ping(site.PushToken, false)
	if up := wait(t, rec); up.State != "up" {
		t.Fatalf("%+v", up)
	}
}

func TestPushThresholdCountsMissedRunsInARow(t *testing.T) {
	m, reg, rec := newPushMonitor(t, 120*time.Millisecond, 0)
	reg.Add(model.Site{Type: "push", FailThreshold: 2})
	begin := time.Now()
	start(t, m)

	noEventWithin(t, rec, 160*time.Millisecond, "one miss is not enough with a threshold of 2") // the first miss is at ~120ms
	ev := wait(t, rec)
	if ev.State != "down" || time.Since(begin) < 220*time.Millisecond {
		t.Fatalf("down only after the second period went by too (%v): %+v", time.Since(begin), ev)
	}
}

func TestPingOutcomes(t *testing.T) {
	m, reg, rec := newPushMonitor(t, time.Hour, 0)
	a, _ := reg.Add(model.Site{Type: "push", Name: "A"})
	paused, _ := reg.Add(model.Site{Type: "push", Name: "P"})
	maint, _ := reg.Add(model.Site{Type: "push", Name: "M"})
	reg.SetPaused(paused.ID, true)
	reg.SetMaintenance(maint.ID, time.Now().Add(time.Hour).UnixMilli())
	start(t, m)
	waitFor(t, func() bool { v, _ := m.View(a.ID); return v.PingDeadline != 0 })
	waitFor(t, func() bool { v, _ := m.View(maint.ID); return v.State == StMaint })

	if m.Ping("0123456789abcdef0123456789abcdef", false) != PingUnknown || m.Ping("", false) != PingUnknown {
		t.Fatal("an unknown token is the only thing that is refused")
	}
	if m.Ping(paused.PushToken, false) != PingInactive || m.Ping(maint.PushToken, false) != PingInactive {
		t.Fatal("a paused site or one in maintenance acknowledges the ping without counting it: the job must not fail because of our pause")
	}
	if m.Ping(a.PushToken, false) != PingAccepted {
		t.Fatal("first ping")
	}
	if m.Ping(a.PushToken, false) != PingThrottled {
		t.Fatal("a second ping within a second is acknowledged but not counted")
	}
	waitFor(t, func() bool { v, _ := m.View(a.ID); return v.State == StUp })
	noEventWithin(t, rec, 50*time.Millisecond, "a throttled ping is not an event")
	if v, _ := m.View(paused.ID); v.State != StPaused || v.PingDeadline != 0 {
		t.Fatalf("a paused site shows no deadline: %+v", v)
	}
}

func TestPushSitesDoNotStretchTheHeartbeatsStallThreshold(t *testing.T) {
	m, reg, _ := newPushMonitor(t, time.Hour, 0)
	reg.Add(model.Site{Type: "push", IntervalSec: sitespec.MaxPushInterval}) // a monthly job
	reg.Add(model.Site{URL: "https://a.example", IntervalSec: 60})
	start(t, m)
	waitFor(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.workers) == 2 })

	m.lastCheck.Store(time.Now().Add(-10 * time.Minute).UnixMilli())
	if m.Healthy() {
		t.Fatal("a stalled checker must be noticed within minutes; a monthly push job must not stretch that to two months")
	}
}

func TestPushSiteCannotBeCheckedByHand(t *testing.T) {
	m, reg, _ := newPushMonitor(t, time.Hour, 0)
	site, _ := reg.Add(model.Site{Type: "push"})
	var inv *model.InvalidError
	if _, err := m.CheckSite(context.Background(), site.ID); !errors.As(err, &inv) {
		t.Fatalf("there is nothing to fetch: %v", err)
	}
}

func TestResumedPushSiteGetsAFullPeriodBeforeItCanMiss(t *testing.T) {
	m, reg, rec := newPushMonitor(t, 250*time.Millisecond, 0)
	site, _ := reg.Add(model.Site{Type: "push"})
	start(t, m)
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.PingDeadline != 0 })

	reg.SetMaintenance(site.ID, time.Now().Add(400*time.Millisecond).UnixMilli())
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State == StMaint })
	noEventWithin(t, rec, 300*time.Millisecond, "a maintenance window must never produce a missed ping") // spans the old deadline
	waitFor(t, func() bool { v, _ := m.View(site.ID); return v.State != StMaint })
	noEventWithin(t, rec, 120*time.Millisecond, "right after maintenance the job gets a whole new period, not an instant miss")
	if ev := wait(t, rec); ev.State != "down" || ev.Kind != model.KindMissed {
		t.Fatalf("and a job that stays silent after that is down: %+v", ev)
	}
}
