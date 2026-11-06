package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/i18n"
	"github.com/wisnuvb/ronda/monitor"
)

// The report is written in the configured language, down to the month names and the decimal separator.
func TestReportFollowsTheConfiguredLanguage(t *testing.T) {
	if err := i18n.SetLang("id"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.SetLang("en") })

	r := Build(Weekly, []Site{
		{Name: "A", State: monitor.StUp, Uptime7d: f(99.5), AvgMs: 90},
		{Name: "B", State: monitor.StDown, Since: ago(90 * time.Minute), Uptime7d: f(80)},
	}, []Incident{{SiteID: "b", SiteName: "B", Started: ago(90 * time.Minute), Ongoing: true}}, now)
	txt := r.Text()
	for _, want := range []string{
		"Laporan mingguan · 29 Okt – 5 Nov 2026", // October is Okt, not Oct
		"2 situs · 🟢 1 sehat · 🔴 1 down",
		"Rata-rata uptime: 89,75%", // a decimal comma
		"Gangguan: 1 kali, total down 1 jam 30 mnt",
		"Masih down sekarang",
		"B — sejak 5 Nov 06:30 WIB",
		"Uptime terendah",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q in:\n%s", want, txt)
		}
	}
	for _, english := range []string{"Weekly report", "Average uptime", "Incidents", "Still down"} {
		if strings.Contains(txt, english) {
			t.Errorf("English %q leaked into an Indonesian report:\n%s", english, txt)
		}
	}

	if d := Build(Daily, nil, nil, now); !strings.Contains(d.Text(), "Laporan harian · 24 jam sampai 5 Nov 08:00 WIB") {
		t.Errorf("daily title:\n%s", d.Text())
	}
}

// reports.json used to be keyed "harian"/"mingguan". After an upgrade the last-served slot must survive,
// or a restart would treat both kinds as new, or resend a report that already went out.
func TestReportsJSONFromAnOlderVersionIsMigrated(t *testing.T) {
	dir := t.TempDir()
	served := wib(2026, 11, 5, 8, 0).UnixMilli()
	old := map[string]int64{"harian": served, "mingguan": wib(2026, 11, 2, 8, 0).UnixMilli()}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(dir, "reports.json"), b, 0o640); err != nil {
		t.Fatal(err)
	}

	s := &Scheduler{Schedule: Schedule{Daily: "08:00", Weekly: "08:00", WeeklyDay: 1}, Dir: dir}
	// Thursday 5 Nov 08:30: both slots were already served by the old version, so nothing is due...
	if got := s.Due(wib(2026, 11, 5, 8, 30)); len(got) != 0 {
		t.Fatalf("a report the old version already sent must not go out again: %v", got)
	}
	// ...and the next daily slot is still found, because the old marker was read.
	if got := s.Due(wib(2026, 11, 6, 8, 1)); len(got) != 1 || got[0] != Daily {
		t.Fatalf("Friday morning: only the daily report: %v", got)
	}

	// The file on disk now uses the new keys only.
	raw, err := os.ReadFile(filepath.Join(dir, "reports.json"))
	if err != nil {
		t.Fatal(err)
	}
	var now map[string]int64
	if err := json.Unmarshal(raw, &now); err != nil {
		t.Fatal(err)
	}
	if _, ok := now["harian"]; ok {
		t.Errorf("legacy key still written: %s", raw)
	}
	if _, ok := now["mingguan"]; ok {
		t.Errorf("legacy key still written: %s", raw)
	}
	if now["daily"] == 0 || now["weekly"] != wib(2026, 11, 2, 8, 0).UnixMilli() {
		t.Errorf("slots lost in the migration: %s", raw)
	}
}

func TestMigrationKeepsTheLaterSlotWhenBothKeysExist(t *testing.T) {
	dir := t.TempDir()
	older, newer := wib(2026, 11, 4, 8, 0).UnixMilli(), wib(2026, 11, 5, 8, 0).UnixMilli()
	b, _ := json.Marshal(map[string]int64{"harian": newer, "daily": older})
	if err := os.WriteFile(filepath.Join(dir, "reports.json"), b, 0o640); err != nil {
		t.Fatal(err)
	}
	s := &Scheduler{Schedule: Schedule{Daily: "08:00"}, Dir: dir}
	s.load()
	if s.last[Daily] != newer || len(s.last) != 1 {
		t.Fatalf("want only daily=%d, got %v", newer, s.last)
	}
}
