package report

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/monitor"
)

// Thursday 5 November 2026, 08:00 WIB.
var now = time.Date(2026, 11, 5, 8, 0, 0, 0, jakarta)

func f(v float64) *float64      { return &v }
func ago(d time.Duration) int64 { return now.Add(-d).UnixMilli() }

func TestHealthyWeekSaysSo(t *testing.T) {
	r := Build(Weekly, []Site{
		{Name: "A", State: monitor.StUp, Uptime7d: f(100), AvgMs: 90},
		{Name: "B", State: monitor.StUp, Uptime7d: f(100), AvgMs: 120},
	}, nil, now)
	txt := r.Text()
	for _, want := range []string{"Weekly report · 29 Oct – 5 Nov 2026", "2 sites · 🟢 2 healthy", "Average uptime: 100%", "No incidents"} {
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q in:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "Lowest uptime") || strings.Contains(txt, "Still down") {
		t.Fatalf("sections with nothing in them must not appear:\n%s", txt)
	}
	if !r.Healthy() {
		t.Fatal("Healthy() must agree")
	}
}

func TestAveragePausedSitesAndTheNever100Rule(t *testing.T) {
	r := Build(Weekly, []Site{
		{Name: "A", State: monitor.StUp, Uptime7d: f(99.996)},
		{Name: "B", State: monitor.StUp, Uptime7d: f(100)},
		{Name: "Dijeda", State: monitor.StPaused, Uptime7d: f(10)},
		{Name: "Baru", State: monitor.StUnknown}, // no data yet: not part of the mean
	}, nil, now)
	if r.AvgUptime == nil || *r.AvgUptime >= 100 || *r.AvgUptime < 99.99 {
		t.Fatalf("a mean below 100 must never read 100%%: %v", r.AvgUptime)
	}
	if got := pct(*r.AvgUptime); got != "99.99%" {
		t.Fatalf("pct = %s", got)
	}
	if r.Total != 4 || r.Up != 2 || r.Paused != 1 || r.Unknown != 1 {
		t.Fatalf("counts: %+v", r)
	}
	for _, row := range r.Lowest {
		if row.Name == "Dijeda" {
			t.Fatal("a paused site's numbers say nothing about how it did")
		}
	}
}

func TestDailyUsesTheDayFigureAndWeeklyTheWeekFigure(t *testing.T) {
	s := []Site{{Name: "A", State: monitor.StUp, Uptime24h: f(90), Uptime7d: f(99)}}
	if d := Build(Daily, s, nil, now); *d.AvgUptime != 90 || !strings.Contains(d.Text(), "Daily report · 24 hours until 5 Nov 08:00 WIB") {
		t.Fatalf("daily: %v\n%s", *d.AvgUptime, d.Text())
	}
	if w := Build(Weekly, s, nil, now); *w.AvgUptime != 99 {
		t.Fatalf("weekly: %v", *w.AvgUptime)
	}
}

func TestOutagesCountOnlyTheirShareInsideThePeriod(t *testing.T) {
	r := Build(Weekly, []Site{{Name: "Toko", State: monitor.StDown, Since: ago(10 * 24 * time.Hour), Uptime7d: f(0)}}, []Incident{
		{SiteName: "Toko", Started: ago(10 * 24 * time.Hour), Ongoing: true},                   // began before the week: only 7 days count
		{SiteName: "Blog", Started: ago(2 * time.Hour), Ended: ago(time.Hour)},                 // 1 h inside
		{SiteName: "Lama", Started: ago(30 * 24 * time.Hour), Ended: ago(20 * 24 * time.Hour)}, // entirely before: ignored
		{SiteName: "Baru", Started: ago(5 * time.Minute), Ended: ago(10 * time.Minute)},        // malformed (ends before it starts): ignored
	}, now)
	if r.IncidentCount != 2 {
		t.Fatalf("two outages touch the week: %d", r.IncidentCount)
	}
	if want := int64(7*24*3600 + 3600); r.DowntimeSec != want {
		t.Fatalf("downtime = %d s, want %d (7 days of the ongoing one + 1 hour)", r.DowntimeSec, want)
	}
	if r.Longest == nil || r.Longest.Name != "Toko" || r.Longest.Detail != "7 d 0 h" {
		t.Fatalf("longest: %+v", r.Longest)
	}
	txt := r.Text()
	if !strings.Contains(txt, "Incidents: 2, total downtime 7 d 1 h") || !strings.Contains(txt, "Still down now") || !strings.Contains(txt, "Toko — since 26 Oct") {
		t.Fatalf("text:\n%s", txt)
	}
	if r.Healthy() {
		t.Fatal("not healthy")
	}
}

func TestListsAreCappedAndSayHowManyWereLeftOut(t *testing.T) {
	var sites []Site
	for i := 0; i < 9; i++ {
		sites = append(sites, Site{Name: fmt.Sprintf("S%d", i), State: monitor.StUp, Uptime7d: f(90 + float64(i)), AvgMs: 100 + i})
	}
	r := Build(Weekly, sites, nil, now)
	if len(r.Lowest) != listMax || r.More["lowest"] != 4 || r.Lowest[0].Name != "S0" || r.Lowest[0].Detail != "90.00%" {
		t.Fatalf("lowest first and capped: %+v more=%v", r.Lowest, r.More)
	}
	if len(r.Slowest) != slowestMax || r.Slowest[0].Name != "S8" || r.Slowest[0].Detail != "108 ms" {
		t.Fatalf("slowest first: %+v", r.Slowest)
	}
	if txt := r.Text(); !strings.Contains(txt, "… and 4 more (see the dashboard)") {
		t.Fatalf("the cut must be announced:\n%s", txt)
	}
}

func TestCertificatesSoonestFirstAndOnlyWithinAMonth(t *testing.T) {
	day := int64(86400000)
	r := Build(Weekly, []Site{
		{Name: "Jauh", State: monitor.StUp, CertNotAfter: now.UnixMilli() + 90*day},
		{Name: "Dekat", State: monitor.StUp, CertNotAfter: now.UnixMilli() + 9*day + 1000},
		{Name: "Besok", State: monitor.StUp, CertNotAfter: now.UnixMilli() + day/2},
		{Name: "Mati", State: monitor.StUp, CertNotAfter: now.UnixMilli() - day},
		{Name: "Dijeda", State: monitor.StPaused, CertNotAfter: now.UnixMilli() + day},
		{Name: "TanpaTLS", State: monitor.StUp},
	}, nil, now)
	var got []string
	for _, c := range r.Certs {
		got = append(got, c.Name+"="+c.Detail)
	}
	if want := "Mati=already expired Besok=less than 1 day Dekat=9 days"; strings.Join(got, " ") != want {
		t.Fatalf("certs: %v, want %s", got, want)
	}
}

func TestPushSitesAreNotRankedBySpeed(t *testing.T) {
	r := Build(Weekly, []Site{{Name: "Backup", Type: "push", State: monitor.StUp, AvgMs: 5000}, {Name: "Web", State: monitor.StUp, AvgMs: 200}}, nil, now)
	if len(r.Slowest) != 1 || r.Slowest[0].Name != "Web" {
		t.Fatalf("a job's 'response time' means nothing: %+v", r.Slowest)
	}
}

func TestHTMLEscapesNamesAndKeepsTheLengthSane(t *testing.T) {
	evil := "<b>Toko</b> & <script>x</script>" + strings.Repeat("z", 200)
	r := Build(Weekly, []Site{{Name: evil, State: monitor.StDown, Since: ago(time.Hour), Uptime7d: f(50)}}, []Incident{{SiteName: evil, Started: ago(time.Hour), Ongoing: true}}, now)
	out := r.HTML()
	if strings.Contains(out, "<script>") || strings.Contains(out, "<b>Toko") {
		t.Fatalf("user text must be escaped:\n%s", out)
	}
	if !strings.Contains(out, "&lt;b&gt;Toko&lt;/b&gt; &amp;") || !strings.Contains(out, "<b>Weekly report") {
		t.Fatalf("escaped text and real markup both expected:\n%s", out)
	}
	if len([]rune(out)) > 1500 {
		t.Fatalf("a long name must be cut, the message stays far below Telegram's 4096 limit: %d runes", len([]rune(out)))
	}
	// A worst case: every list full, all names at the limit.
	var many []Site
	for i := 0; i < 50; i++ {
		many = append(many, Site{Name: strings.Repeat("n", 200) + fmt.Sprint(i), State: monitor.StDown, Since: ago(time.Hour), Uptime7d: f(float64(i)), AvgMs: i + 1, CertNotAfter: now.UnixMilli() + 1000})
	}
	if n := len([]rune(Build(Weekly, many, nil, now).HTML())); n > 4096 {
		t.Fatalf("the worst-case report is %d runes: over Telegram's limit", n)
	}
}

func TestEmptyInstallation(t *testing.T) {
	txt := Build(Daily, nil, nil, now).Text()
	if !strings.Contains(txt, "No sites are being monitored yet.") || strings.Contains(txt, "No incidents") {
		t.Fatalf("%s", txt)
	}
}

func TestReportForARecipientCoversOnlyTheirSites(t *testing.T) {
	sites := []Site{
		{ID: "1", Name: "Toko A", State: monitor.StDown, Since: ago(time.Hour), Uptime7d: f(90), To: []string{"klien-a"}},
		{ID: "2", Name: "Blog A", State: monitor.StUp, Uptime7d: f(100), To: []string{"klien-a", "default"}},
		{ID: "3", Name: "Toko B", State: monitor.StDown, Since: ago(time.Hour), Uptime7d: f(10), To: []string{"klien-b"}},
		{ID: "4", Name: "Internal", State: monitor.StUp, Uptime7d: f(100)},
	}
	incs := []Incident{
		{SiteID: "1", SiteName: "Toko A", Started: ago(time.Hour), Ongoing: true},
		{SiteID: "3", SiteName: "Toko B", Started: ago(2 * time.Hour), Ended: ago(time.Hour)},
	}
	r, ok := For("klien-a", Weekly, sites, incs, now)
	if !ok || r.Total != 2 || r.IncidentCount != 1 || r.Scope != "klien-a" {
		t.Fatalf("client A has two sites and one outage: %+v", r)
	}
	txt := r.Text()
	if !strings.Contains(txt, "Weekly report · klien-a · 29 Oct – 5 Nov 2026") || !strings.Contains(txt, "Toko A") {
		t.Fatalf("scoped title and own sites:\n%s", txt)
	}
	for _, other := range []string{"Toko B", "Internal"} {
		if strings.Contains(txt, other) {
			t.Fatalf("client A's report must never mention %q:\n%s", other, txt)
		}
	}
	if strings.Contains(txt, "90.00") == false || *r.AvgUptime != 95 {
		t.Fatalf("the average is over their sites only: %v", *r.AvgUptime)
	}
	if _, ok := For("klien-c", Weekly, sites, incs, now); ok {
		t.Fatal("a recipient with no sites gets no empty report")
	}
	if all := Build(Weekly, sites, incs, now); all.Total != 4 || all.IncidentCount != 2 || all.Scope != "" {
		t.Fatalf("the overview still covers everything: %+v", all)
	}
}
