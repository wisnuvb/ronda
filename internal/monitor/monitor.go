package monitor

import (
	"context"
	"log"
	"math/rand/v2"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wisnuvb/ronda/internal/eventlog"
	"github.com/wisnuvb/ronda/internal/history"
	"github.com/wisnuvb/ronda/internal/i18n"
	"github.com/wisnuvb/ronda/internal/model"
	"github.com/wisnuvb/ronda/internal/registry"
	"github.com/wisnuvb/ronda/internal/schedule"
)

const recentKept = 60

type Checker interface {
	Check(ctx context.Context, s model.Site) model.Result
}

type Notifier interface {
	Notify(ev model.Event)
}

type Options struct {
	Registry *registry.Registry
	Checker  Checker
	Notifier Notifier       // optional
	Control  *Control       // optional
	History  *history.Store // optional; feeds uptime and charts
	DataDir  string         // optional; where state.json and events.jsonl live
	// EventRetentionDays is how long down/up events (and so incidents) are kept. 0 means 365.
	EventRetentionDays int
	MaxConcurrent      int // simultaneous checks, default 20
	// CertWarnDays: announce a certificate once as it crosses each of these many days to expiry.
	// nil means 14, 7, 3 and 1; an empty list turns the warning off.
	CertWarnDays []int
	// RemindAfterMin: while a site stays down, say so again this many minutes after it went down (once per
	// entry), then once a day. nil means 60, 360 and 1440 (1 h, 6 h, 24 h); an empty list turns reminders off.
	RemindAfterMin []int
	// StartSpread is how long the first checks of a start-up are spread over, so hundreds of sites do not all
	// connect in the same second. 0 means 10 s; a negative value means no spreading.
	StartSpread time.Duration
}

// DefaultCertWarnDays is what a nil Options.CertWarnDays means.
var DefaultCertWarnDays = []int{14, 7, 3, 1}

func spread(d time.Duration) time.Duration {
	switch {
	case d < 0:
		return 0
	case d == 0:
		return 10 * time.Second
	}
	return d
}

// DefaultRemindAfterMin is what a nil Options.RemindAfterMin means.
var DefaultRemindAfterMin = []int{60, 360, 1440}

// remindRepeat is how often a reminder repeats once every configured one has been sent.
const remindRepeat = 24 * time.Hour

// View is a site together with its live status, as served by the API.
type View struct {
	model.Site
	history.Stats
	LastPing        int64          `json:"last_ping,omitempty"`        // push: when the last ping arrived
	PingDeadline    int64          `json:"ping_deadline,omitempty"`    // push: the latest the next ping may arrive
	MaintenanceEnds int64          `json:"maintenance_ends,omitempty"` // when the current maintenance (one-off or weekly) ends
	NextMaintenance int64          `json:"next_maintenance,omitempty"` // when the next weekly window starts
	State           string         `json:"state"`
	Since           int64          `json:"since,omitempty"`
	Fails           int            `json:"fails,omitempty"`
	Last            *model.Result  `json:"last,omitempty"`
	Recent          []model.Result `json:"recent,omitempty"`
}

type runtime struct {
	lastPing int64 // push: when the last ping arrived (unix ms)
	deadline int64 // push: when silence becomes a miss (unix ms)
	m        Machine
	last     *model.Result
	recent   []model.Result
	certTier int // smallest expiry threshold already announced; 0 = none
	reminded int // "still down" reminders already sent for the current outage
}

type worker struct {
	site   model.Site
	cancel context.CancelFunc
	pings  chan pingEvent // push sites only
}

type pingEvent struct{ failed bool }

type Monitor struct {
	opt Options
	sem chan struct{}

	// Tunables, replaced in tests.
	jitterMax  time.Duration // spreads the first check of each site
	retryDelay time.Duration // spacing while a failure or recovery is being confirmed
	now        func() time.Time

	// pingGap is the closest two pings may come before the second is acknowledged but not counted.
	pingGap time.Duration

	// pushPeriod says how long silence is tolerated for a push site. Tests shorten it.
	pushPeriod func(model.Site) (interval, grace time.Duration)

	certWarn  []int           // descending
	remind    []time.Duration // ascending offsets from the moment a site went down
	lastCheck atomic.Int64    // unix ms of the latest completed check

	mu      sync.Mutex
	rt      map[string]*runtime
	workers map[string]*worker
	saved   map[string]model.SiteState // loaded at start, consumed as runtimes appear
	wg      sync.WaitGroup

	evlog *eventlog.Log
}

func New(opt Options) *Monitor {
	if opt.MaxConcurrent <= 0 {
		opt.MaxConcurrent = 20
	}
	warn := slices.Clone(opt.CertWarnDays)
	if opt.CertWarnDays == nil {
		warn = slices.Clone(DefaultCertWarnDays)
	}
	warn = slices.DeleteFunc(warn, func(d int) bool { return d < 1 })
	slices.SortFunc(warn, func(a, b int) int { return b - a })
	warn = slices.Compact(warn)

	offs := slices.Clone(opt.RemindAfterMin)
	if opt.RemindAfterMin == nil {
		offs = slices.Clone(DefaultRemindAfterMin)
	}
	offs = slices.DeleteFunc(offs, func(v int) bool { return v < 1 })
	slices.Sort(offs)
	offs = slices.Compact(offs)
	remind := make([]time.Duration, len(offs))
	for i, v := range offs {
		remind[i] = time.Duration(v) * time.Minute
	}

	m := &Monitor{
		certWarn:   warn,
		remind:     remind,
		opt:        opt,
		sem:        make(chan struct{}, opt.MaxConcurrent),
		jitterMax:  spread(opt.StartSpread),
		retryDelay: 10 * time.Second,
		now:        time.Now,
		pingGap:    time.Second,
		pushPeriod: func(s model.Site) (time.Duration, time.Duration) {
			return time.Duration(s.IntervalSec) * time.Second, time.Duration(s.GraceSec) * time.Second
		},
		rt:      map[string]*runtime{},
		workers: map[string]*worker{},
		saved:   loadState(opt.DataDir),
	}
	days := opt.EventRetentionDays
	if days <= 0 {
		days = 365
	}
	evlog, err := eventlog.Open(opt.DataDir, time.Duration(days)*24*time.Hour)
	if err != nil {
		log.Printf("monitor: event archive could not be opened (%v); events are kept in memory only until restart", err)
		evlog, _ = eventlog.Open("", 0)
	}
	m.evlog = evlog
	m.lastCheck.Store(time.Now().UnixMilli()) // a fresh start counts as alive
	return m
}

// Run schedules checks until ctx is cancelled, following registry changes live. It also
// wakes by itself when a maintenance window ends, so the site resumes without anyone touching it.
func (m *Monitor) Run(ctx context.Context) {
	next := m.reconcile(ctx, true)
	changed := m.opt.Registry.Changed()
	for {
		var wake <-chan time.Time
		var timer *time.Timer
		if !next.IsZero() {
			timer = time.NewTimer(time.Until(next))
			wake = timer.C
		}
		stop := func() {
			if timer != nil {
				timer.Stop()
			}
		}
		select {
		case <-ctx.Done():
			stop()
			m.wg.Wait()
			return
		case <-changed:
			stop()
			next = m.reconcile(ctx, false)
		case <-wake:
			next = m.reconcile(ctx, false)
		}
	}
}

// Healthy reports whether checks are actually running. A process that is alive but has stopped
// checking is exactly what the heartbeat must not vouch for.
func (m *Monitor) Healthy() bool {
	m.mu.Lock()
	var longest time.Duration
	for _, w := range m.workers {
		if w.site.Type == "push" {
			continue // these wait for pings instead of checking, and their period may be a month
		}
		longest = max(longest, time.Duration(w.site.IntervalSec)*time.Second)
	}
	m.mu.Unlock()
	if longest == 0 {
		return true // nothing to check
	}
	return time.Since(time.UnixMilli(m.lastCheck.Load())) < 2*longest+time.Minute
}

// Check runs one ad-hoc probe, sharing the concurrency limit. It does not touch any site's state.
func (m *Monitor) Check(ctx context.Context, s model.Site) model.Result {
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		return model.Result{Time: time.Now().UnixMilli(), Kind: model.KindError, Error: "dibatalkan"}
	}
	return m.opt.Checker.Check(ctx, s)
}

func (m *Monitor) runtimeLocked(id string) *runtime {
	r := m.rt[id]
	if r == nil {
		r = &runtime{}
		if st, ok := m.saved[id]; ok {
			if st.Status == StUp || st.Status == StDown {
				r.m = Machine{Status: st.Status, Since: st.Since}
			}
			r.certTier = st.CertTier
			if r.m.Status == StDown {
				r.reminded = st.Reminded
			}
		}
		delete(m.saved, id)
		m.rt[id] = r
	}
	return r
}

// reconcile starts, restarts and stops workers so they match the registry. At
// startup the first checks are spread out; a site added or edited later is checked at once.
// It returns when the next maintenance window ends (zero if none), so Run can wake then.
func (m *Monitor) reconcile(ctx context.Context, startup bool) time.Time {
	sites := m.opt.Registry.List()
	nowT := m.now()
	now := nowT.UnixMilli()
	var nextWake int64
	wake := func(ms int64) { // remember the earliest moment something must be re-decided
		if ms > now && (nextWake == 0 || ms < nextWake) {
			nextWake = ms
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	dirty := false
	live := make(map[string]bool, len(sites))

	for _, s := range sites {
		live[s.ID] = true
		w := m.workers[s.ID]

		// Paused, or inside a maintenance window (one-off or weekly): not checked, not alerted.
		until := s.MaintenanceUntil
		if end, ok := schedule.Active(s.MaintenanceWeekly, nowT, s.MaintenanceSkipUntil); ok && end.UnixMilli() > until {
			until = end.UnixMilli()
		}
		inMaint := !s.Paused && until > now
		if !s.Paused {
			if inMaint {
				wake(until) // resume when it ends
			}
			if next, ok := schedule.NextStart(s.MaintenanceWeekly, nowT, s.MaintenanceSkipUntil); ok {
				wake(next.UnixMilli()) // and go quiet when the next one begins
			}
		}
		if s.Paused || inMaint {
			if w != nil {
				w.cancel()
				delete(m.workers, s.ID)
			}
			want := StPaused
			if inMaint {
				want = StMaint
			}
			if r := m.runtimeLocked(s.ID); r.m.Status != want {
				r.m = Machine{Status: want, Since: now}
				dirty = true
			}
			continue
		}
		if w != nil && reflect.DeepEqual(w.site, s) {
			continue
		}

		r := m.runtimeLocked(s.ID)
		if w != nil {
			w.cancel()
			delete(m.workers, s.ID)
			if !sameTarget(w.site, s) {
				r.m, r.last, r.recent = Machine{}, nil, nil // a different target: old verdict no longer applies
				m.forgetHistory(s.ID)
				dirty = true
			}
		}
		if r.m.Status == StPaused || r.m.Status == StMaint {
			r.m = Machine{} // resumed: start from a clean verdict
			dirty = true
		}

		wctx, cancel := context.WithCancel(ctx)
		w = &worker{site: s, cancel: cancel}
		if s.Type == "push" {
			w.pings = make(chan pingEvent, 8)
			r.m.RecoverAfter = 1 // the first ping after trouble is proof enough
		}
		m.workers[s.ID] = w
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			if s.Type == "push" {
				m.loopPush(wctx, s, w.pings)
				return
			}
			m.loop(wctx, s, startup)
		}()
	}

	for id, w := range m.workers {
		if !live[id] {
			w.cancel()
			delete(m.workers, id)
		}
	}
	for id := range m.rt {
		if !live[id] {
			delete(m.rt, id)
			m.forgetHistory(id)
			dirty = true
		}
	}
	if dirty {
		m.saveStateLocked()
	}
	if nextWake == 0 {
		return time.Time{}
	}
	return time.UnixMilli(nextWake)
}

// sameTarget says whether a checked site still points at the same thing, so its old verdict and history apply.
func sameTarget(a, b model.Site) bool {
	return a.URL == b.URL && a.Type == b.Type && a.Method == b.Method && a.DNSType == b.DNSType && a.DNSServer == b.DNSServer
}

func (m *Monitor) loop(ctx context.Context, s model.Site, spread bool) {
	delay := time.Duration(0)
	if j := min(time.Duration(s.IntervalSec)*time.Second, m.jitterMax); spread && j > 0 {
		delay = rand.N(j)
	}
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		res, netOK := m.probe(ctx, s)
		if ctx.Err() != nil {
			return // site edited, paused, removed, or shutdown: the result is stale
		}
		delay = m.observe(s, res, netOK)
	}
}

// loopPush replaces checking for a push site: it waits for the job's ping, and silence past
// interval + grace counts as one missed check. After a miss the next period counts as another, so
// fail_threshold means "that many missed runs in a row".
func (m *Monitor) loopPush(ctx context.Context, s model.Site, pings <-chan pingEvent) {
	interval, grace := m.pushPeriod(s)
	next := time.Now().Add(interval + grace) // a new or restarted site gets a full period to report
	m.setPingTimes(s.ID, 0, next, false)

	for {
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return

		case p := <-pings:
			timer.Stop()
			res := model.Result{Time: m.now().UnixMilli(), OK: true, Kind: model.KindOK}
			if p.failed {
				res.OK, res.Kind, res.Error = false, model.KindReported, "job reported failure"
				res.Hint = i18n.T("The job called /fail: it knows its own run did not succeed. Check that job's log.")
			}
			m.observe(s, res, true)
			next = time.Now().Add(interval + grace) // it did run, even if it failed: the clock restarts
			m.setPingTimes(s.ID, res.Time, next, res.OK)

		case <-timer.C:
			res := model.Result{Time: m.now().UnixMilli(), Kind: model.KindMissed, Error: "ping not received",
				Hint: i18n.T("No ping within %s (interval + grace). The job may have failed, not run, or cannot reach ronda.",
					fmtPeriod(interval+grace))}
			// If ronda itself has lost the network, pings could not have arrived: hold the alert, as for any check.
			netOK := m.opt.Control.Up(ctx)
			if ctx.Err() != nil {
				return
			}
			m.observe(s, res, netOK)
			next = time.Now().Add(interval)
			m.setPingTimes(s.ID, 0, next, false)
		}
	}
}

func fmtPeriod(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return i18n.T("%d days", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return i18n.T("%d hours", int(d.Hours()))
	case d >= 2*time.Minute:
		return i18n.T("%d minutes", int(d.Minutes()))
	case d < 2*time.Second:
		return i18n.T("1 second")
	}
	return i18n.T("%d seconds", int(d.Seconds()))
}

// setPingTimes records when the last ping came (ignored unless ok) and the new deadline.
func (m *Monitor) setPingTimes(id string, lastPing int64, deadline time.Time, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.rt[id]; r != nil {
		if ok && lastPing != 0 {
			r.lastPing = lastPing
		}
		r.deadline = deadline.UnixMilli()
	}
}

// PingOutcome says what became of a ping.
type PingOutcome int

const (
	PingUnknown   PingOutcome = iota // no such token
	PingAccepted                     // counted
	PingInactive                     // the site is paused or in maintenance: acknowledged, not counted
	PingThrottled                    // came within a second of the previous one: acknowledged, not counted
)

// Ping delivers a job's ping (or, with failed, its report of failure) to the push site that owns token.
// Everything except an unknown token is acknowledged, so a job is never made to fail just because
// monitoring is paused or it pinged twice.
func (m *Monitor) Ping(token string, failed bool) PingOutcome {
	s, ok := m.opt.Registry.ByToken(token)
	if !ok {
		return PingUnknown
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.workers[s.ID]
	if w == nil || w.pings == nil {
		return PingInactive
	}
	if r := m.rt[s.ID]; r != nil && !failed {
		now := m.now().UnixMilli()
		if now-r.lastPing < m.pingGap.Milliseconds() {
			return PingThrottled
		}
		r.lastPing = now // recorded here, not when the worker gets to it, so a second ping right behind is caught
	}
	select {
	case w.pings <- pingEvent{failed: failed}:
	default: // a flood of pings while the worker is busy: dropping extras loses nothing
	}
	return PingAccepted
}

func (m *Monitor) probe(ctx context.Context, s model.Site) (model.Result, bool) {
	select {
	case m.sem <- struct{}{}:
	case <-ctx.Done():
		return model.Result{}, true
	}
	res := m.opt.Checker.Check(ctx, s)
	<-m.sem
	m.lastCheck.Store(time.Now().UnixMilli())

	netOK := true
	if !res.OK && ctx.Err() == nil {
		netOK = m.opt.Control.Up(ctx)
	}
	return res, netOK
}

// observe records a result, fires any resulting event, and returns the delay until the next check.
func (m *Monitor) observe(s model.Site, res model.Result, netOK bool) time.Duration {
	interval := time.Duration(s.IntervalSec) * time.Second

	m.mu.Lock()
	r := m.rt[s.ID]
	if r == nil {
		m.mu.Unlock()
		return interval
	}
	r.last = &res
	r.recent = append(r.recent, res)
	if len(r.recent) > recentKept {
		r.recent = r.recent[len(r.recent)-recentKept:]
	}

	// A failure while our own network is down says nothing about the site, so it
	// must not drag its uptime down.
	if m.opt.History != nil && (res.OK || netOK) {
		m.opt.History.Record(s.ID, res)
	}

	prevStatus, prevSince := r.m.Status, r.m.Since
	tr := r.m.Observe(res.OK, res.Time, s.FailThreshold, netOK)

	var evs []model.Event
	remindersChanged := false
	switch tr {
	case WentDown:
		r.reminded = 0
		evs = append(evs, model.Event{
			Time: res.Time, State: "down", SiteID: s.ID, SiteName: s.Name, URL: s.URL,
			Kind: res.Kind, Hint: res.Hint, Error: res.Error, Status: res.Status,
			Fails: s.FailThreshold, Since: r.m.Since,
		})
	case Recovered:
		r.reminded = 0
		evs = append(evs, model.Event{
			Time: res.Time, State: "up", SiteID: s.ID, SiteName: s.Name, URL: s.URL,
			Status: res.Status, LatencyMs: res.LatencyMs, DowntimeSec: max(0, (res.Time-prevSince)/1000),
		})
	case Held:
		log.Printf("monitor: %s failed %dx but the monitor's own network looks down; alert held back", s.Name, s.FailThreshold)
	}
	// Still down after the same outage has lasted a while: say so again, once per threshold, so a
	// silenced or forgotten alert does not leave a site dead for days.
	if tr == NoChange && r.m.Status == StDown && !res.OK && netOK {
		if due := m.remindersDue(r.m.Since, res.Time); due > r.reminded {
			r.reminded = due
			remindersChanged = true
			evs = append(evs, model.Event{
				Time: res.Time, State: "reminder", SiteID: s.ID, SiteName: s.Name, URL: s.URL,
				Kind: res.Kind, Hint: res.Hint, Error: res.Error, Status: res.Status,
				Since: r.m.Since, DowntimeSec: max(0, (res.Time-r.m.Since)/1000),
			})
		}
	}
	tierChanged := false
	if ev, changed := m.certEventLocked(s, r, res); changed {
		tierChanged = true
		if ev != nil {
			evs = append(evs, *ev)
		}
	}
	if r.m.Status != prevStatus || tierChanged || remindersChanged {
		m.saveStateLocked()
	}
	delay := interval
	if r.m.retrying() {
		delay = min(m.retryDelay, interval)
	}
	m.mu.Unlock()

	for _, ev := range evs {
		ev.To = s.NotifyTo // who hears about this site; the notifier resolves the names
		m.emit(ev)
	}
	return delay
}

// certTier maps days-to-expiry onto the smallest warning threshold already crossed (0 = none).
func (m *Monitor) certTier(days int) int {
	tier := 0
	for _, t := range m.certWarn { // descending, so the last one crossed is the smallest
		if days <= t {
			tier = t
		}
	}
	return tier
}

// certEventLocked announces a certificate once per threshold it crosses on the way to expiry, and
// quietly re-arms when it is renewed. changed says the stored tier moved (and must be saved).
func (m *Monitor) certEventLocked(s model.Site, r *runtime, res model.Result) (ev *model.Event, changed bool) {
	if res.CertNotAfter == 0 || len(m.certWarn) == 0 {
		return nil, false
	}
	days := int((res.CertNotAfter - res.Time) / 86400000)
	tier := m.certTier(days)
	switch {
	case tier == r.certTier:
		return nil, false
	case tier == 0 || (r.certTier != 0 && tier > r.certTier):
		r.certTier = tier // renewed: warn again when it gets close once more
		return nil, true
	}
	r.certTier = tier
	return &model.Event{
		Time: res.Time, State: "cert", SiteID: s.ID, SiteName: s.Name, URL: s.URL, Kind: "cert_expiring",
		CertNotAfter: res.CertNotAfter, DaysLeft: days,
		Hint: i18n.T("Renew the certificate (e.g. certbot renew) and reload the web server before it expires."),
	}, true
}

// remindersDue says how many reminders an outage that began at `since` has earned by `now`: one per
// configured offset, then one more every remindRepeat. A reminder missed while ronda was off (or the
// site checked rarely) is not replayed; the caller sends only one for the highest count reached.
func (m *Monitor) remindersDue(since, now int64) int {
	if len(m.remind) == 0 || since == 0 {
		return 0
	}
	elapsed := time.Duration(now-since) * time.Millisecond
	n := 0
	for _, off := range m.remind {
		if elapsed >= off {
			n++
		}
	}
	if n == len(m.remind) {
		n += int((elapsed - m.remind[n-1]) / remindRepeat)
	}
	return n
}

func (m *Monitor) emit(ev model.Event) {
	// A reminder repeats a fact already in the archive; keeping it out stops it from looking like a new incident.
	if ev.State != "reminder" {
		m.evlog.Append(ev)
	}

	log.Printf("monitor: %s %s (%s)", ev.State, ev.SiteName, ev.Kind)
	if m.opt.Notifier != nil {
		m.opt.Notifier.Notify(ev)
	}
}

func (m *Monitor) saveStateLocked() {
	st := make(map[string]model.SiteState, len(m.rt))
	for id, r := range m.rt {
		ss := model.SiteState{CertTier: r.certTier}
		if r.m.Status == StUp || r.m.Status == StDown {
			ss.Status, ss.Since = r.m.Status, r.m.Since
			if r.m.Status == StDown {
				ss.Reminded = r.reminded
			}
		}
		if ss != (model.SiteState{}) {
			st[id] = ss
		}
	}
	writeJSON(m.opt.DataDir, "state.json", st)
}

// Events returns the most recent state changes, newest first (at most 300).
func (m *Monitor) Events() []model.Event { return m.evlog.Recent(300) }

// AppendEventForTest writes straight to the event archive. It exists so tests in other packages can
// build a history without driving real checks.
func (m *Monitor) AppendEventForTest(ev model.Event) { m.evlog.Append(ev) }

// Close releases the event archive. Call it after Run has returned.
func (m *Monitor) Close() error { return m.evlog.Close() }

func (m *Monitor) forgetHistory(id string) {
	if m.opt.History != nil {
		m.opt.History.Delete(id)
	}
}

func (m *Monitor) viewLocked(s model.Site, withRecent bool) View {
	v := View{Site: s, State: StUnknown}
	nowT := m.now()
	if end, ok := schedule.Active(s.MaintenanceWeekly, nowT, s.MaintenanceSkipUntil); ok {
		v.MaintenanceEnds = end.UnixMilli()
	}
	if s.MaintenanceUntil > nowT.UnixMilli() && s.MaintenanceUntil > v.MaintenanceEnds {
		v.MaintenanceEnds = s.MaintenanceUntil
	}
	if next, ok := schedule.NextStart(s.MaintenanceWeekly, nowT, s.MaintenanceSkipUntil); ok {
		v.NextMaintenance = next.UnixMilli()
	}
	if m.opt.History != nil {
		if withRecent {
			v.Stats = m.opt.History.Full(s.ID)
		} else {
			v.Stats = m.opt.History.Brief(s.ID)
		}
	}
	if r := m.rt[s.ID]; r != nil {
		if r.m.Status != "" {
			v.State = r.m.Status
		}
		v.Since, v.Fails, v.Last = r.m.Since, r.m.Fails, r.last
		if s.Type == "push" && r.m.Status != StPaused && r.m.Status != StMaint {
			v.LastPing, v.PingDeadline = r.lastPing, r.deadline
		}
		if withRecent {
			v.Recent = append([]model.Result(nil), r.recent...)
		}
	}
	return v
}

// Views lists every site with its current status.
func (m *Monitor) Views() []View {
	sites := m.opt.Registry.List()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]View, len(sites))
	for i, s := range sites {
		out[i] = m.viewLocked(s, false)
	}
	return out
}

// View returns one site with its recent results.
func (m *Monitor) View(id string) (View, bool) {
	s, err := m.opt.Registry.Get(id)
	if err != nil {
		return View{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.viewLocked(s, true), true
}

// CheckSite runs one ad-hoc probe of a stored site. Like Check, it changes no state.
func (m *Monitor) CheckSite(ctx context.Context, id string) (model.Result, error) {
	s, err := m.opt.Registry.Get(id)
	if err != nil {
		return model.Result{}, err
	}
	if s.Type == "push" {
		return model.Result{}, &registry.InvalidError{Msg: i18n.T("A push site is not checked from here: it waits for pings from its job.")}
	}
	return m.Check(ctx, s), nil
}

// Incident is one period a site was down, built from the down/up events.
type Incident struct {
	SiteID      string `json:"site_id"`
	SiteName    string `json:"site_name"`
	URL         string `json:"url"`
	Kind        string `json:"kind"`
	Hint        string `json:"hint,omitempty"`
	Started     int64  `json:"started"`
	Ended       int64  `json:"ended,omitempty"` // 0: not ended, or ended without a recovery being confirmed
	DurationSec int64  `json:"duration_sec,omitempty"`
	Ongoing     bool   `json:"ongoing,omitempty"`
}

// Incidents pairs "down" with the next "up" of the same site, newest first. An
// incident is ongoing only while the site really is down: if it was paused, edited
// or removed in the meantime, it is closed without an end time.
// siteID filters to one site; empty means all.
func (m *Monitor) Incidents(siteID string) []Incident {
	events := m.evlog.All() // oldest first
	m.mu.Lock()
	down := map[string]bool{}
	for id, r := range m.rt {
		down[id] = r.m.Status == StDown
	}
	m.mu.Unlock()

	open := map[string]*Incident{}
	var out []*Incident
	for _, ev := range events {
		if siteID != "" && ev.SiteID != siteID {
			continue
		}
		switch ev.State {
		case "down":
			if prev := open[ev.SiteID]; prev != nil { // a down we never saw end
				prev.Ongoing = false
			}
			in := &Incident{SiteID: ev.SiteID, SiteName: ev.SiteName, URL: ev.URL, Kind: ev.Kind, Hint: ev.Hint,
				Started: ev.Since, Ongoing: true}
			if in.Started == 0 {
				in.Started = ev.Time
			}
			open[ev.SiteID] = in
			out = append(out, in)
		case "up":
			if in := open[ev.SiteID]; in != nil {
				in.Ended, in.Ongoing = ev.Time, false
				in.DurationSec = max(0, (in.Ended-in.Started)/1000)
				delete(open, ev.SiteID)
			}
		}
	}
	res := make([]Incident, 0, len(out))
	for i := len(out) - 1; i >= 0; i-- { // newest first
		in := out[i]
		if in.Ongoing && !down[in.SiteID] {
			in.Ongoing = false
		}
		res = append(res, *in)
	}
	return res
}

// IncidentsPage returns up to limit incidents that started before `before` (unix ms; 0 means from the
// newest), newest first, and whether older ones remain. Pass the last Started you got to read on.
func (m *Monitor) IncidentsPage(siteID string, before int64, limit int) (page []Incident, more bool) {
	all := m.Incidents(siteID)
	if before > 0 {
		i := 0
		for i < len(all) && all[i].Started >= before {
			i++
		}
		all = all[i:]
	}
	if limit <= 0 {
		limit = 50
	}
	if len(all) > limit {
		return all[:limit], true
	}
	return all, false
}
