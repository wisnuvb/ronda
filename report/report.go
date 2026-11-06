// Package report builds the periodic summary (the daily and weekly report): how the sites did over the last day
// or week, in a few lines that fit one Telegram message. Building is a pure function of its input so it can
// be tested with a fixed clock; Collect gathers that input from the running monitor.
package report

import (
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wisnuvb/ronda/history"
	"github.com/wisnuvb/ronda/i18n"
	"github.com/wisnuvb/ronda/model"
	"github.com/wisnuvb/ronda/monitor"
	"github.com/wisnuvb/ronda/notify"
)

// Kind is the length of the period a report covers. Its value is stored in reports.json, so it is
// language-neutral; the words people read come from i18n (see title).
type Kind string

const (
	Daily  Kind = "daily"
	Weekly Kind = "weekly"
)

func (k Kind) span() time.Duration {
	if k == Weekly {
		return 7 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// Site is what the report needs to know about one site.
type Site struct {
	ID           string
	To           []string // recipients of this site's notifications (Site.NotifyTo)
	Name         string
	Type         string
	State        string
	Since        int64
	Uptime24h    *float64
	Uptime7d     *float64
	AvgMs        int
	CertNotAfter int64
}

// Incident is one outage; Ended is 0 while it is still going on.
type Incident struct {
	SiteID   string
	SiteName string
	Started  int64
	Ended    int64
	Ongoing  bool
}

const (
	listMax    = 5
	certWarn   = 30 // days: certificates closer than this are listed
	nameMax    = 60
	slowestMax = 3
)

// Row is one line of a list in the report.
type Row struct {
	Name   string
	Detail string
}

// Report is the summary, ready to be rendered.
type Report struct {
	Kind     Kind
	Scope    string // the recipient this report is for; empty for the overview of every site
	From, To int64

	Total, Up, Down, Maintenance, Paused, Unknown int

	AvgUptime *float64 // mean over the sites that are not paused and have data

	IncidentCount int
	DowntimeSec   int64 // all downtime inside the period, summed over sites
	Longest       *Row  // the longest outage inside the period

	Lowest    []Row // the lowest uptime, only sites below 100%
	StillDown []Row
	Certs     []Row // certificates about to expire, soonest first
	Slowest   []Row
	More      map[string]int // how many rows each list left out
}

// Build summarises the period that ends at now.
func Build(kind Kind, sites []Site, incs []Incident, now time.Time) Report {
	r := Report{Kind: kind, To: now.UnixMilli(), From: now.Add(-kind.span()).UnixMilli(), More: map[string]int{}}
	uptime := func(s Site) *float64 {
		if kind == Weekly {
			return s.Uptime7d
		}
		return s.Uptime24h
	}

	type ranked struct {
		Row
		key float64
	}
	var low, certs, slow []ranked
	var down []ranked
	var sum float64
	var counted int
	for _, s := range sites {
		r.Total++
		switch s.State {
		case monitor.StUp:
			r.Up++
		case monitor.StDown:
			r.Down++
		case monitor.StMaint:
			r.Maintenance++
		case monitor.StPaused:
			r.Paused++
		default:
			r.Unknown++
		}
		if s.State == monitor.StPaused {
			continue // an administrator silenced it: its numbers say nothing about how it did
		}
		if u := uptime(s); u != nil {
			sum += *u
			counted++
			if *u < 100 {
				low = append(low, ranked{Row{short(s.Name), pct(*u)}, *u})
			}
		}
		if s.State == monitor.StDown {
			detail := i18n.T("just now")
			if s.Since > 0 {
				detail = i18n.T("since %s", when(s.Since))
			}
			down = append(down, ranked{Row{short(s.Name), detail}, float64(s.Since)})
		}
		if s.CertNotAfter > 0 {
			days := int((s.CertNotAfter - r.To) / 86400000)
			if days <= certWarn {
				certs = append(certs, ranked{Row{short(s.Name), daysLeft(days)}, float64(s.CertNotAfter)})
			}
		}
		if s.Type != "push" && s.State == monitor.StUp && s.AvgMs > 0 {
			slow = append(slow, ranked{Row{short(s.Name), fmt.Sprintf("%d ms", s.AvgMs)}, -float64(s.AvgMs)})
		}
	}
	if counted > 0 {
		avg := sum / float64(counted)
		if avg < 100 {
			avg = min(avg, 99.99) // the same promise as the dashboard: never 100% when something failed
		}
		r.AvgUptime = &avg
	}

	take := func(name string, in []ranked, limit int) []Row {
		slices.SortStableFunc(in, func(a, b ranked) int {
			switch {
			case a.key < b.key:
				return -1
			case a.key > b.key:
				return 1
			}
			return strings.Compare(a.Name, b.Name)
		})
		if len(in) > limit {
			r.More[name] = len(in) - limit
			in = in[:limit]
		}
		out := make([]Row, len(in))
		for i, x := range in {
			out[i] = x.Row
		}
		return out
	}
	r.Lowest = take("lowest", low, listMax)
	r.StillDown = take("down", down, listMax)
	r.Certs = take("certs", certs, listMax)
	r.Slowest = take("slow", slow, slowestMax)

	// Outages: only the part inside the period counts, so a long-running one reports this week's share.
	var longest int64
	for _, in := range incs {
		end := in.Ended
		if in.Ongoing || end == 0 {
			end = r.To
		}
		start := max(in.Started, r.From)
		end = min(end, r.To)
		if end <= start {
			continue
		}
		sec := (end - start) / 1000
		r.IncidentCount++
		r.DowntimeSec += sec
		if sec > longest {
			longest = sec
			r.Longest = &Row{short(in.SiteName), notify.Duration(sec)}
		}
	}
	return r
}

// For narrows sites and outages to those routed to the named recipient, and builds the report for them. A site
// listed for several recipients appears in each one's report; one that only goes to the main chats in none.
func For(recipient string, kind Kind, sites []Site, incs []Incident, now time.Time) (Report, bool) {
	var mine []Site
	ids := map[string]bool{}
	for _, s := range sites {
		if slices.Contains(s.To, recipient) {
			mine = append(mine, s)
			ids[s.ID] = true
		}
	}
	if len(mine) == 0 {
		return Report{}, false
	}
	var mineInc []Incident
	for _, in := range incs {
		if ids[in.SiteID] {
			mineInc = append(mineInc, in)
		}
	}
	r := Build(kind, mine, mineInc, now)
	r.Scope = recipient
	return r, true
}

// Healthy says nothing went wrong in the period.
func (r Report) Healthy() bool { return r.IncidentCount == 0 && r.Down == 0 && len(r.Lowest) == 0 }

func short(s string) string {
	if r := []rune(s); len(r) > nameMax {
		return string(r[:nameMax-1]) + "…"
	}
	return s
}

func pct(v float64) string {
	if v == 100 {
		return "100%"
	}
	return i18n.Float(v, 2) + "%"
}

func daysLeft(d int) string {
	switch {
	case d < 0:
		return i18n.T("already expired")
	case d < 1:
		return i18n.T("less than 1 day")
	case d == 1:
		return i18n.T("1 day")
	}
	return i18n.T("%d days", d)
}

// months are marked for translation here and translated when shown: "May" is "Mei" in Indonesian.
var months = [...]string{i18n.N("Jan"), i18n.N("Feb"), i18n.N("Mar"), i18n.N("Apr"), i18n.N("May"), i18n.N("Jun"),
	i18n.N("Jul"), i18n.N("Aug"), i18n.N("Sep"), i18n.N("Oct"), i18n.N("Nov"), i18n.N("Dec")}

func day(ms int64) string {
	t := model.In(time.UnixMilli(ms))
	return fmt.Sprintf("%d %s", t.Day(), i18n.T(months[t.Month()-1]))
}

func when(ms int64) string { return day(ms) + " " + model.Stamp(ms, "15:04") }

func (r Report) title() string {
	span := day(r.From) + " – " + day(r.To)
	if r.Kind == Daily {
		span = i18n.T("24 hours until %s", when(r.To))
	}
	y := model.In(time.UnixMilli(r.To)).Year()
	if r.Kind == Weekly {
		span += fmt.Sprintf(" %d", y)
	}
	if r.Kind == Weekly {
		if r.Scope != "" {
			return i18n.T("Weekly report · %s · %s", r.Scope, span)
		}
		return i18n.T("Weekly report · %s", span)
	}
	if r.Scope != "" {
		return i18n.T("Daily report · %s · %s", r.Scope, span)
	}
	return i18n.T("Daily report · %s", span)
}

// render lays the report out once for both formats; esc and bold differ between them.
func (r Report) render(esc func(string) string, bold func(string) string, bullet string) string {
	var b strings.Builder
	line := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	rows := func(heading string, rs []Row, more int) {
		if len(rs) == 0 {
			return
		}
		line("\n%s", bold(heading))
		for _, x := range rs {
			line("%s %s — %s", bullet, esc(x.Name), esc(x.Detail))
		}
		if more > 0 {
			line("%s", i18n.T("… and %d more (see the dashboard)", more))
		}
	}

	line("📊 %s", bold(esc(r.title())))
	total := i18n.T("%d sites", r.Total)
	if r.Total == 1 {
		total = i18n.T("1 site")
	}
	parts := []string{total}
	for _, p := range []struct {
		n    int
		icon string
		what string
	}{{r.Up, "🟢", i18n.N("healthy")}, {r.Down, "🔴", i18n.N("down")}, {r.Maintenance, "🔧", i18n.N("in maintenance")}, {r.Paused, "⏸", i18n.N("paused")}, {r.Unknown, "⚪", i18n.N("not checked yet")}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d %s", p.icon, p.n, i18n.T(p.what)))
		}
	}
	line("%s", strings.Join(parts, " · "))
	if r.Total == 0 {
		line("%s", i18n.T("No sites are being monitored yet."))
		return strings.TrimRight(b.String(), "\n")
	}
	if r.AvgUptime != nil {
		line("%s", i18n.T("Average uptime: %s", bold(pct(*r.AvgUptime))))
	}

	if r.Healthy() {
		line("\n%s", i18n.T("✅ No incidents in this period."))
	} else if r.IncidentCount > 0 {
		line("\n%s", i18n.T("Incidents: %s, total downtime %s", bold(strconv.Itoa(r.IncidentCount)), notify.Duration(r.DowntimeSec)))
		if r.Longest != nil && r.IncidentCount > 1 {
			line("%s", i18n.T("Longest: %s — %s", esc(r.Longest.Name), esc(r.Longest.Detail)))
		}
	}
	rows(i18n.T("Still down now"), r.StillDown, r.More["down"])
	rows(i18n.T("Lowest uptime"), r.Lowest, r.More["lowest"])
	rows(i18n.T("Certificates expiring soon"), r.Certs, r.More["certs"])
	rows(i18n.T("Slowest (24-hour average)"), r.Slowest, r.More["slow"])
	return strings.TrimRight(b.String(), "\n")
}

// HTML is the Telegram form (parse_mode=HTML); every piece of user text is escaped.
func (r Report) HTML() string {
	return r.render(html.EscapeString, func(s string) string { return "<b>" + s + "</b>" }, "•")
}

// Text is the plain form, for webhooks.
func (r Report) Text() string {
	return r.render(func(s string) string { return s }, func(s string) string { return s }, "-")
}

// Collect gathers the report's input from a running monitor. Uptime comes from the full history, because the
// list view only carries the last 24 hours.
func Collect(mon *monitor.Monitor, hist *history.Store) ([]Site, []Incident) {
	var sites []Site
	for _, v := range mon.Views() {
		s := Site{ID: v.ID, To: v.NotifyTo, Name: v.Name, Type: v.Type, State: v.State, Since: v.Since, Uptime24h: v.Uptime24h, AvgMs: v.AvgMs}
		if hist != nil {
			st := hist.Full(v.ID)
			s.Uptime24h, s.Uptime7d, s.AvgMs = st.Uptime24h, st.Uptime7d, st.AvgMs
		}
		if v.Last != nil && v.State != monitor.StPaused {
			s.CertNotAfter = v.Last.CertNotAfter
		}
		sites = append(sites, s)
	}
	var incs []Incident
	for _, in := range mon.Incidents("") {
		incs = append(incs, Incident{SiteID: in.SiteID, SiteName: in.SiteName, Started: in.Started, Ended: in.Ended, Ongoing: in.Ongoing})
	}
	return sites, incs
}
