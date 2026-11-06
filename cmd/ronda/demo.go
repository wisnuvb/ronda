package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wisnuvb/ronda/internal/api"
	"github.com/wisnuvb/ronda/internal/history"
	"github.com/wisnuvb/ronda/internal/i18n"
	"github.com/wisnuvb/ronda/internal/model"
	"github.com/wisnuvb/ronda/internal/monitor"
	"github.com/wisnuvb/ronda/internal/registry"
	"github.com/wisnuvb/ronda/web"
)

// profile describes how a pretend site behaves.
type profile struct {
	name     string
	url      string
	base     int     // typical latency, ms
	jitter   int     // spread, ms
	failRate float64 // chance a check fails
	down     bool    // fails every check right now
	slow     bool    // latency drifts up in waves
	certDays int
	paused   bool
	maint    bool // in a maintenance window
	weekly   []model.Window
	public   bool
	opts     func(*model.Site)
}

// The names below are English source text marked for translation: the demo shows them in the language
// chosen with -lang. The English name is also the key the demo uses internally to find a site again.
const (
	nShop      = "Online Shop"
	nBlog      = "Company Blog"
	nPayments  = "Payments API"
	nPortal    = "Employee Portal"
	nNews      = "News Site"
	nCampaign  = "Campaign Landing Page"
	nWiki      = "Internal Wiki"
	nDocs      = "Documentation"
	nDatabase  = "Production PostgreSQL"
	nBackup    = "Daily Backup"
	nWeekly    = "Weekly Report"
	nDNS       = "DNS example.com"
	recClientA = "client-a"
	recTeam    = "team"
)

var demoNames = []string{i18n.N("Online Shop"), i18n.N("Company Blog"), i18n.N("Payments API"), i18n.N("Employee Portal"),
	i18n.N("News Site"), i18n.N("Campaign Landing Page"), i18n.N("Internal Wiki"), i18n.N("Documentation"),
	i18n.N("Production PostgreSQL"), i18n.N("Daily Backup"), i18n.N("Weekly Report"), i18n.N("DNS example.com")}

// Diagnosis texts the demo's pretend failures carry (the real ones come from internal/check).
const (
	hintRefused  = "Connection refused: the server is reachable but nothing is listening on that port (web server stopped, or wrong port)."
	hintRefused2 = "Connection refused: the server is reachable but nothing is listening on that port."
	hintTimeout  = "Connected, but no response arrived within 10 s: the application or backend is stuck or overloaded."
	hintBadGW    = "502 Bad Gateway: the reverse proxy (e.g. nginx) is up but the backend is not answering or has crashed."
	hintCF522    = "Cloudflare 522: Cloudflare's connection to the origin timed out (server down, overloaded, or a firewall is blocking Cloudflare's IPs)."
)

var _ = []string{
	i18n.N("Connection refused: the server is reachable but nothing is listening on that port (web server stopped, or wrong port)."),
	i18n.N("Connection refused: the server is reachable but nothing is listening on that port."),
	i18n.N("Connected, but no response arrived within 10 s: the application or backend is stuck or overloaded."),
	i18n.N("502 Bad Gateway: the reverse proxy (e.g. nginx) is up but the backend is not answering or has crashed."),
	i18n.N("Cloudflare 522: Cloudflare's connection to the origin timed out (server down, overloaded, or a firewall is blocking Cloudflare's IPs)."),
}

var demoProfiles = []profile{
	{name: nShop, public: true, url: "https://shop.example.com", base: 180, jitter: 60, certDays: 62},
	{name: nBlog, public: true, url: "https://blog.example.com", base: 240, jitter: 90, certDays: 9},
	{name: nPayments, public: true, url: "https://api.pay.example.com/health", base: 95, jitter: 30, failRate: 0.015, certDays: 41,
		opts: func(s *model.Site) { s.IntervalSec = 30; s.Keyword = "ok" }},
	{name: nPortal, public: true, url: "https://portal.example.com", base: 420, jitter: 220, slow: true, certDays: 120},
	{name: nNews, public: true, url: "https://news.example.com", base: 310, jitter: 140, failRate: 0.04, certDays: 28,
		weekly: []model.Window{{Days: []int{6, 0}, Start: "02:00", Minutes: 120}}},
	{name: nCampaign, public: true, url: "https://promo.example.com", base: 150, jitter: 40, down: true, certDays: 75},
	{name: nWiki, url: "http://wiki.intranet.example.com", base: 60, jitter: 20, paused: true,
		opts: func(s *model.Site) { s.AllowPrivate = true }},
	{name: nDocs, public: true, url: "https://docs.example.com", base: 130, jitter: 35, certDays: 200, maint: true},
	{name: nDatabase, url: "tcp://db.example.com:5432", base: 12, jitter: 6},
	{name: nBackup, url: "push://backup", public: true, opts: func(s *model.Site) { s.IntervalSec = 3600 }},
	{name: nWeekly, url: "push://weekly-report", opts: func(s *model.Site) { s.IntervalSec = 604800 }},
	{name: nDNS, url: "dns://example.com", base: 28, jitter: 12, opts: func(s *model.Site) { s.Keyword = "203.0.113.10" }},
}

// demoChecker answers instead of the network, so the dashboard shows every state.
type demoChecker struct {
	byHost map[string]profile
	rng    *rand.Rand
}

func hostOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return p.Hostname()
}

func (d *demoChecker) Check(_ context.Context, s model.Site) model.Result {
	return d.shape(s, d.sample(d.byHost[hostOf(s.URL)], time.Now(), true))
}

// shape strips what only a web check has, and adds a DNS answer, for tcp and dns sites.
func (d *demoChecker) shape(s model.Site, r model.Result) model.Result {
	if s.Type == "http" || s.Type == "" {
		return r
	}
	r.Status, r.DNSMs, r.TLSMs, r.TTFBMs, r.CertNotAfter = 0, 0, 0, 0, 0
	if s.Type == "dns" && r.OK {
		r.Answer = "203.0.113.10"
	}
	return r
}

func (d *demoChecker) sample(p profile, at time.Time, live bool) model.Result {
	r := model.Result{Time: at.UnixMilli()}
	failing := p.down && live || d.rng.Float64() < p.failRate
	if failing {
		if p.down {
			r.Kind, r.Error, r.Hint = model.KindRefused, "connection refused", i18n.T(hintRefused)
			return r
		}
		r.Kind, r.Error, r.Status = model.KindHTTP5xx, "HTTP 502", 502
		r.Hint = i18n.T(hintBadGW)
		r.LatencyMs = 40 + d.rng.IntN(60)
		return r
	}
	ms := p.base + d.rng.IntN(p.jitter*2+1) - p.jitter
	if p.slow {
		wave := 1 + 0.9*float64((at.Unix()/1800)%6)/5 // drifts up and resets every few hours
		ms = int(float64(ms) * wave)
	}
	ms = max(ms, 8)
	r.OK, r.Kind, r.Status, r.LatencyMs = true, model.KindOK, 200, ms
	r.DNSMs, r.ConnectMs, r.TLSMs = 6+d.rng.IntN(20), 10+d.rng.IntN(25), 20+d.rng.IntN(40)
	r.TTFBMs = max(1, ms-r.DNSMs-r.ConnectMs-r.TLSMs)
	if p.certDays > 0 {
		r.CertNotAfter = at.Add(time.Duration(p.certDays) * 24 * time.Hour).UnixMilli()
	}
	return r
}

func cmdDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:9191", "listen address (must be loopback)")
	static := fs.String("static", "", "read the dashboard files from this folder instead of the binary (for developing the UI)")
	lang := fs.String("lang", i18n.Default, "language of the dashboard and messages: "+strings.Join(i18n.Supported(), " or "))
	tz := fs.String("timezone", "UTC", "IANA time zone for clock times and days, e.g. Europe/Berlin")
	fs.Parse(args)
	if err := i18n.SetLang(*lang); err != nil {
		return err
	}
	if err := model.SetTimezone(*tz); err != nil {
		return err
	}

	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("the demo has no login, so it may only listen on a loopback address (e.g. 127.0.0.1:9191)")
	}

	dir, err := os.MkdirTemp("", "ronda-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	reg, err := registry.Open(dir)
	if err != nil {
		return err
	}
	hist := history.Open(dir)
	chk := &demoChecker{byHost: map[string]profile{}, rng: rand.New(rand.NewPCG(7, 11))}
	now := time.Now()

	type seeded struct {
		id string
		p  profile
	}
	var sites []seeded
	reg.SetRecipients([]string{recClientA, recTeam})
	for _, p := range demoProfiles {
		s := model.Site{Name: i18n.T(p.name), URL: p.url, Public: p.public}
		switch p.name { // a taste of per-client routing in the demo
		case nShop, nBlog:
			s.NotifyTo = []string{recClientA}
		case nPayments:
			s.NotifyTo = []string{"default", recTeam}
		}
		if p.opts != nil {
			p.opts(&s)
		}
		added, err := reg.Add(s)
		if err != nil {
			return err
		}
		if p.paused {
			if _, err := reg.SetPaused(added.ID, true); err != nil {
				return err
			}
		}
		if len(p.weekly) > 0 {
			body, _ := json.Marshal(map[string]any{"maintenance_weekly": p.weekly})
			if _, err := reg.Patch(added.ID, body); err != nil {
				return err
			}
		}
		if p.maint {
			if _, err := reg.SetMaintenance(added.ID, now.Add(95*time.Minute).UnixMilli()); err != nil {
				return err
			}
		}
		chk.byHost[hostOf(p.url)] = p
		sites = append(sites, seeded{added.ID, p})
	}

	// 30 days of history: sparse before the last day, every 30s during it, with a couple of outages.
	seed := *chk
	seed.rng = rand.New(rand.NewPCG(3, 5))
	for _, st := range sites {
		if st.p.paused {
			continue
		}
		for at := now.Add(-30 * 24 * time.Hour); at.Before(now); {
			r := seed.sample(st.p, at, false)
			if st.p.name == nCampaign && at.After(now.Add(-26*time.Hour)) && at.Before(now.Add(-25*time.Hour)) ||
				st.p.name == nShop && at.After(now.Add(-9*24*time.Hour)) && at.Before(now.Add(-9*24*time.Hour+40*time.Minute)) {
				r = model.Result{Time: at.UnixMilli(), Kind: model.KindTimeoutResponse, Error: "response timeout"}
			}
			hist.Record(st.id, r)
			if at.Before(now.Add(-24 * time.Hour)) {
				at = at.Add(15 * time.Minute)
			} else {
				at = at.Add(30 * time.Second)
			}
		}
	}

	// Past incidents and one that is still open.
	ms := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	byName := map[string]string{}
	for _, st := range sites {
		byName[st.p.name] = st.id
	}
	mk := func(state, name string, at, since time.Duration, kind, hint string) model.Event {
		return model.Event{Time: ms(at), State: state, SiteID: byName[name], SiteName: i18n.T(name), URL: chk.byHost[hostOf(urlOf(name))].url,
			Kind: kind, Hint: hint, Fails: 3, Since: ms(since), DowntimeSec: int64((since - at).Seconds())}
	}
	events := []model.Event{
		mk("down", nCampaign, 18*time.Minute, 18*time.Minute+30*time.Second, model.KindRefused, i18n.T(hintRefused)),
		mk("up", nCampaign, 25*time.Hour, 0, "", ""),
		mk("down", nCampaign, 26*time.Hour, 26*time.Hour+40*time.Second, model.KindTimeoutResponse, i18n.T(hintTimeout)),
		mk("up", nShop, 9*24*time.Hour-40*time.Minute, 0, "", ""),
		mk("down", nShop, 9*24*time.Hour, 9*24*time.Hour+40*time.Second, model.KindCloudflare, i18n.T(hintCF522)),
	}
	// Two months of older, shorter incidents, enough to need several pages.
	names := []string{nShop, nBlog, nPayments, nPortal, nNews, nDocs}
	kinds := []struct{ kind, hint string }{
		{model.KindTimeoutResponse, i18n.T(hintTimeout)},
		{model.KindHTTP5xx, i18n.T(hintBadGW)},
		{model.KindRefused, i18n.T(hintRefused2)},
	}
	for i := 0; i < 60; i++ {
		n, k := names[i%len(names)], kinds[i%len(kinds)]
		start := 3*24*time.Hour + time.Duration(i)*17*time.Hour
		dur := time.Duration(2+i%9) * time.Minute
		up := mk("up", n, start-dur, 0, "", "")
		up.DowntimeSec = int64(dur.Seconds())
		events = append(events, up, mk("down", n, start, start+30*time.Second, k.kind, k.hint))
	}
	events[1].LatencyMs, events[3].LatencyMs = 190, 175
	events[1].DowntimeSec, events[3].DowntimeSec = 3600+30, 40*60
	writeDemoFile(dir, "events.json", events)
	state := map[string]model.SiteState{}
	for _, st := range sites {
		switch {
		case st.p.down:
			state[st.id] = model.SiteState{Status: monitor.StDown, Since: ms(18*time.Minute + 30*time.Second)}
		case !st.p.paused && !st.p.maint:
			state[st.id] = model.SiteState{Status: monitor.StUp, Since: ms(30 * 24 * time.Hour)}
		}
	}
	writeDemoFile(dir, "state.json", state)

	mon := monitor.New(monitor.Options{Registry: reg, Checker: chk, History: hist, DataDir: dir})
	staticFS := web.Static()
	if *static != "" {
		staticFS = os.DirFS(*static)
	}
	srv := &http.Server{
		Addr: *listen,
		Handler: api.New(api.Options{Registry: reg, Monitor: mon, History: hist, Static: staticFS, NoAuth: true, Recipients: []string{recClientA, recTeam},
			StatusPage: api.StatusPageOptions{Enabled: true, Title: i18n.T("Example service status"), Description: i18n.T("The latest news about our services.")}}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	go func() { mon.Run(ctx); close(done) }()
	go demoJobs(ctx, reg, mon)
	go func() { <-ctx.Done(); _ = srv.Close() }()

	log.Printf("ronda demo: synthetic data, no login, at http://%s (Ctrl+C to stop), language %s, timezone %s", *listen, i18n.Lang(), model.Loc())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-done
	return nil
}

func urlOf(name string) string {
	for _, p := range demoProfiles {
		if p.name == name {
			return p.url
		}
	}
	return ""
}

func writeDemoFile(dir, name string, v any) {
	b, _ := json.Marshal(v)
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o640); err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
	}
}

// demoJobs plays the scheduled jobs: one that reports in regularly, and one that says it failed.
func demoJobs(ctx context.Context, reg *registry.Registry, mon *monitor.Monitor) {
	tokens := map[string]string{}
	for _, s := range reg.List() {
		if s.Type == registry.TypePush {
			tokens[s.Name] = s.PushToken // keyed by the displayed name
		}
	}
	// The workers start a moment after the monitor does; a ping before that is "inactive", so try again.
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if mon.Ping(tokens[i18n.T(nWeekly)], true) == monitor.PingAccepted {
			break
		}
	}
	for {
		mon.Ping(tokens[i18n.T(nBackup)], false)
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Second):
		}
	}
}
