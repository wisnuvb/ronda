package registry

import (
	"slices"
	"strings"
	"testing"

	"github.com/wisnuvb/ronda/model"
)

func TestNotifyToIsNormalised(t *testing.T) {
	r := openTemp(t)
	s, err := r.Add(model.Site{URL: "https://a.contoh.com", NotifyTo: []string{" Klien-A ", "tim", "klien-a", "DEFAULT"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"klien-a", "tim", "default"}; !slices.Equal(s.NotifyTo, want) {
		t.Fatalf("trimmed, lower-cased and de-duplicated in order: %v, want %v", s.NotifyTo, want)
	}
	for _, bad := range []string{"Klien A", "-awal", "a/b", strings.Repeat("x", 33), "ünïcode", ""} {
		if _, err := r.Add(model.Site{URL: "https://b.contoh.com", NotifyTo: []string{bad}}); err == nil {
			t.Errorf("recipient name %q must be rejected", bad)
		}
	}
	many := []string{}
	for i := 0; i < 11; i++ {
		many = append(many, "n"+string(rune('a'+i)))
	}
	if _, err := r.Add(model.Site{URL: "https://c.contoh.com", NotifyTo: many}); err == nil {
		t.Error("more than 10 recipients per site must be rejected")
	}
}

func TestOnlyConfiguredRecipientsAreAccepted(t *testing.T) {
	r := openTemp(t)
	if _, err := r.Add(model.Site{URL: "https://a.contoh.com", NotifyTo: []string{"klien-b"}}); err == nil || !strings.Contains(err.Error(), "klien-a, tim") {
		t.Fatalf("an unknown recipient is refused and the known ones are listed: %v", err)
	}
	if _, err := r.Add(model.Site{URL: "https://a.contoh.com", NotifyTo: []string{"default"}}); err != nil {
		t.Fatalf("'default' always exists: %v", err)
	}
	bare := func() *Registry { x, _ := Open(t.TempDir()); return x }()
	if _, err := bare.Add(model.Site{URL: "https://a.contoh.com", NotifyTo: []string{"klien-a"}}); err == nil || !strings.Contains(err.Error(), "no recipients are defined") {
		t.Fatalf("with no recipients configured the message says so: %v", err)
	}
	if _, err := bare.Add(model.Site{URL: "https://a.contoh.com"}); err != nil {
		t.Fatalf("a site without routing never needs any: %v", err)
	}
}

func TestPatchChecksOnlyAChangedRoute(t *testing.T) {
	r := openTemp(t)
	s, _ := r.Add(model.Site{URL: "https://a.contoh.com", NotifyTo: []string{"klien-a"}})
	if _, err := r.Patch(s.ID, []byte(`{"notify_to":["tidak-ada"]}`)); err == nil {
		t.Fatal("routing to a recipient that does not exist must be refused")
	}
	// The recipient disappears from the config later (a typo fixed, a client gone).
	r.SetRecipients([]string{"tim"})
	got, err := r.Patch(s.ID, []byte(`{"interval_sec":120}`))
	if err != nil || got.IntervalSec != 120 || !slices.Equal(got.NotifyTo, []string{"klien-a"}) {
		t.Fatalf("an unrelated edit must not fail because of a stale route: %+v %v", got, err)
	}
	if got, err = r.Patch(s.ID, []byte(`{"notify_to":["tim"]}`)); err != nil || !slices.Equal(got.NotifyTo, []string{"tim"}) {
		t.Fatalf("moving it to an existing recipient works: %+v %v", got, err)
	}
	if got, err = r.Patch(s.ID, []byte(`{"notify_to":[]}`)); err != nil || len(got.NotifyTo) != 0 {
		t.Fatalf("an empty list goes back to the main chats: %+v %v", got, err)
	}
}

func TestAStaleRouteOnDiskStillLoads(t *testing.T) {
	dir := t.TempDir()
	a, _ := Open(dir)
	a.SetRecipients([]string{"klien-a"})
	if _, err := a.Add(model.Site{URL: "https://a.contoh.com", NotifyTo: []string{"klien-a"}}); err != nil {
		t.Fatal(err)
	}
	b, err := Open(dir) // recipients not set (or removed from the config): the file must still open
	if err != nil || len(b.List()) != 1 {
		t.Fatalf("a removed recipient must not stop ronda from starting: %v", err)
	}
}

func TestImportRefusesUnknownRecipientsPerSite(t *testing.T) {
	r := openTemp(t)
	res, err := r.Import([]byte(`{"version":1,"sites":[
		{"url":"https://a.contoh.com","notify_to":["klien-a"]},
		{"url":"https://b.contoh.com","notify_to":["klien-z"]}]}`), false)
	if err != nil || res.Added != 1 || res.Invalid != 1 || !strings.Contains(res.Items[1].Reason, "klien-z") {
		t.Fatalf("%+v %v", res, err)
	}
}
