package bot

import (
	"strings"
	"testing"

	"github.com/wisnuvb/ronda/internal/model"
)

func TestReportCommand(t *testing.T) {
	e := startAdmins(t, nil, []int64{7})
	if _, err := e.reg.Add(model.Site{URL: "https://toko.contoh.com", Name: "Toko <Utama>"}); err != nil {
		t.Fatal(err)
	}

	is := func(r, kind string) bool { return strings.Contains(strings.ToLower(r), kind+" report") }

	weekly := e.say(t, "/report")
	if !is(weekly, "weekly") || !strings.Contains(weekly, "1 site") {
		t.Fatalf("the default is the weekly report: %q", weekly)
	}
	if daily := e.say(t, "/report daily"); !is(daily, "daily") {
		t.Fatalf("daily: %q", daily)
	}
	// The Indonesian command and the Indonesian words for the period are accepted too.
	if r := e.say(t, "/laporan harian"); !is(r, "daily") {
		t.Fatalf("/laporan harian: %q", r)
	}
	if r := e.say(t, "/laporan mingguan"); !is(r, "weekly") {
		t.Fatalf("/laporan mingguan: %q", r)
	}
	if r := e.say(t, "/report weekly"); !is(r, "weekly") {
		t.Fatalf("explicit weekly: %q", r)
	}
	if bad := e.say(t, "/report monthly"); !strings.Contains(bad, "/report daily") || is(bad, "weekly") {
		t.Fatalf("an unknown period must explain itself, not guess: %q", bad)
	}
	if help := e.say(t, "/help"); !strings.Contains(help, "/report") {
		t.Fatalf("the help must list the command: %q", help)
	}
}

func TestReportIsReadOnlySoEveryGroupMemberMayAskForIt(t *testing.T) {
	e := startAdmins(t, nil, []int64{7})
	if r := e.sayIn(t, 8, "supergroup", "/report"); strings.Contains(r, "Ronda admins") || !strings.Contains(strings.ToLower(r), "weekly report") {
		t.Fatalf("a plain member may read the report: %q", r)
	}
	if mutating["laporan"] || mutating["report"] || mutating[primary("laporan")] {
		t.Fatal("the report changes nothing and must not be on the admin-only list")
	}
}
