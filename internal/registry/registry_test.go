package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/model"
)

func open(t *testing.T) (*Registry, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r, dir
}

func TestAddAppliesDefaults(t *testing.T) {
	r, _ := open(t)
	s, err := r.Add(model.Site{URL: " https://contoh.com/ ", ID: "diabaikan", Created: 1})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID == "" || s.ID == "diabaikan" || s.Created <= 1 {
		t.Fatalf("id/created not assigned: %+v", s)
	}
	if s.Name != "contoh.com" || s.Method != "GET" || s.IntervalSec != 60 || s.TimeoutSec != 10 || s.FailThreshold != 3 {
		t.Fatalf("defaults wrong: %+v", s)
	}
}

func TestValidation(t *testing.T) {
	r, _ := open(t)
	bad := map[string]model.Site{
		"kosong":         {},
		"skema ftp":      {URL: "ftp://contoh.com"},
		"tanpa host":     {URL: "https://"},
		"user:password":  {URL: "https://user:pw@contoh.com"},
		"method":         {URL: "https://a.com", Method: "POST"},
		"interval kecil": {URL: "https://a.com", IntervalSec: 5},
		"timeout besar":  {URL: "https://a.com", IntervalSec: 10, TimeoutSec: 20},
		"threshold":      {URL: "https://a.com", FailThreshold: 11},
		"status":         {URL: "https://a.com", ExpectStatus: []int{99}},
		"keyword+head":   {URL: "https://a.com", Method: "HEAD", Keyword: "x"},
		"ua newline":     {URL: "https://a.com", UserAgent: "a\r\nX-Evil: 1"},
		"nama kontrol":   {URL: "https://a.com", Name: "a\x00b"},
	}
	for name, s := range bad {
		_, err := r.Add(s)
		var inv *InvalidError
		if !errors.As(err, &inv) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}
	if len(r.List()) != 0 {
		t.Fatal("invalid sites must not be stored")
	}
}

func TestPersistenceAcrossOpen(t *testing.T) {
	r, dir := open(t)
	a, _ := r.Add(model.Site{URL: "https://a.com", ExpectStatus: []int{200, 301}})
	r.Add(model.Site{URL: "https://b.com"})
	if _, err := r.SetPaused(a.ID, true); err != nil {
		t.Fatal(err)
	}

	r2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := r2.List()
	if len(got) != 2 || got[0].ID != a.ID || !got[0].Paused || len(got[0].ExpectStatus) != 2 {
		t.Fatalf("reloaded: %+v", got)
	}
	if fi, err := os.Stat(filepath.Join(dir, "sites.json")); err != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("sites.json perm: %v %v", fi, err)
	}
}

func TestPatch(t *testing.T) {
	r, _ := open(t)
	a, _ := r.Add(model.Site{URL: "https://a.com", Name: "A", ExpectStatus: []int{200}})

	p, err := r.Patch(a.ID, []byte(`{"name":"Baru","interval_sec":30,"id":"x","created":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Baru" || p.IntervalSec != 30 || p.URL != "https://a.com" || p.ID != a.ID || p.Created != a.Created {
		t.Fatalf("patched: %+v", p)
	}
	if len(p.ExpectStatus) != 1 {
		t.Fatalf("untouched field lost: %+v", p)
	}

	if _, err := r.Patch(a.ID, []byte(`{"bukan_field":1}`)); err == nil {
		t.Fatal("unknown field must be rejected")
	}
	if _, err := r.Patch(a.ID, []byte(`{"interval_sec":1}`)); err == nil {
		t.Fatal("invalid value must be rejected")
	}
	if cur, _ := r.Get(a.ID); cur.IntervalSec != 30 {
		t.Fatalf("failed patch must not change the site: %+v", cur)
	}
	if _, err := r.Patch("nope", []byte(`{}`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestDelete(t *testing.T) {
	r, _ := open(t)
	a, _ := r.Add(model.Site{URL: "https://a.com"})
	if err := r.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if len(r.List()) != 0 {
		t.Fatal("not deleted")
	}
}

func TestCorruptFileIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sites.json")
	os.WriteFile(path, []byte("{rusak"), 0o640)
	if _, err := Open(dir); err == nil {
		t.Fatal("corrupt file must be an error")
	}
	if b, _ := os.ReadFile(path); string(b) != "{rusak" {
		t.Fatal("corrupt file was modified")
	}
}

func TestChangedSignal(t *testing.T) {
	r, _ := open(t)
	select {
	case <-r.Changed():
		t.Fatal("no signal expected yet")
	default:
	}
	r.Add(model.Site{URL: "https://a.com"})
	r.Add(model.Site{URL: "https://b.com"}) // coalesced
	select {
	case <-r.Changed():
	case <-time.After(time.Second):
		t.Fatal("no signal after Add")
	}
}

func TestListReturnsCopies(t *testing.T) {
	r, _ := open(t)
	r.Add(model.Site{URL: "https://a.com", ExpectStatus: []int{200}})
	l := r.List()
	l[0].ExpectStatus[0] = 999
	l[0].Name = "diubah"
	if cur := r.List()[0]; cur.ExpectStatus[0] != 200 || cur.Name == "diubah" {
		t.Fatalf("List leaked internal state: %+v", cur)
	}
}

func TestSiteLimit(t *testing.T) {
	r, _ := open(t)
	r.sites = make([]model.Site, MaxSites)
	_, err := r.Add(model.Site{URL: "https://a.com"})
	if err == nil || !strings.Contains(err.Error(), "limit of") {
		t.Fatalf("got %v", err)
	}
}

func TestSetMaintenance(t *testing.T) {
	r, _ := open(t)
	a, _ := r.Add(model.Site{URL: "https://a.com"})

	until := time.Now().Add(time.Hour).UnixMilli()
	s, err := r.SetMaintenance(a.ID, until)
	if err != nil || s.MaintenanceUntil != until {
		t.Fatalf("%+v %v", s, err)
	}
	if cur, _ := r.Get(a.ID); cur.MaintenanceUntil != until {
		t.Fatal("not stored")
	}
	for name, bad := range map[string]int64{
		"in the past": time.Now().Add(-time.Minute).UnixMilli(),
		"too far":     time.Now().Add(MaxMaintenance + 24*time.Hour).UnixMilli(),
		"negative":    -5,
	} {
		var inv *InvalidError
		if _, err := r.SetMaintenance(a.ID, bad); !errors.As(err, &inv) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}
	if s, err := r.SetMaintenance(a.ID, 0); err != nil || s.MaintenanceUntil != 0 {
		t.Fatalf("0 must end maintenance: %+v %v", s, err)
	}
	if _, err := r.SetMaintenance("tidak-ada", until); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestPatchCannotSmuggleBadMaintenance(t *testing.T) {
	r, _ := open(t)
	a, _ := r.Add(model.Site{URL: "https://a.com"})
	far := time.Now().Add(MaxMaintenance + 48*time.Hour).UnixMilli()
	if _, err := r.Patch(a.ID, []byte(fmt.Sprintf(`{"maintenance_until":%d}`, far))); err == nil {
		t.Fatal("PATCH must apply the same limit")
	}
}

// ---- tcp and dns sites ----

func TestTypeIsInferredFromTheURL(t *testing.T) {
	r, _ := open(t)
	for url, want := range map[string]string{
		"https://a.example": "http", "http://a.example": "http",
		"tcp://db.example:5432": "tcp", "TCP://db.example:5432": "tcp",
		"dns://a.example": "dns",
	} {
		s, err := r.Add(model.Site{URL: url})
		if err != nil || s.Type != want {
			t.Errorf("%s: type=%q err=%v, want %s", url, s.Type, err, want)
		}
	}
}

func TestTCPSiteDefaultsAndClearing(t *testing.T) {
	r, _ := open(t)
	s, err := r.Add(model.Site{URL: "tcp://db.example:5432", Method: "GET", ExpectStatus: []int{200}, NoRedirect: true, UserAgent: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "db.example:5432" || s.Method != "" || s.ExpectStatus != nil || s.NoRedirect || s.UserAgent != "" {
		t.Fatalf("web-only settings must be cleared and the name defaults to host:port: %+v", s)
	}
	if s.IntervalSec != 60 || s.TimeoutSec != 10 || s.FailThreshold != 3 {
		t.Fatalf("shared defaults: %+v", s)
	}
	if v6, err := r.Add(model.Site{URL: "tcp://[2001:db8::1]:25"}); err != nil || v6.URL != "tcp://[2001:db8::1]:25" {
		t.Fatalf("IPv6 literal: %+v %v", v6, err)
	}
}

func TestDNSSiteDefaultsAndServer(t *testing.T) {
	r, _ := open(t)
	s, err := r.Add(model.Site{URL: "dns://Contoh.COM."})
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "dns://Contoh.COM" || s.DNSType != "A" || s.Name != "Contoh.COM (DNS A)" || s.DNSServer != "" {
		t.Fatalf("%+v", s)
	}
	for in, want := range map[string]string{"1.1.1.1": "1.1.1.1:53", "dns.example": "dns.example:53", "10.0.0.2:5353": "10.0.0.2:5353", "2001:db8::1": "[2001:db8::1]:53", "[::1]:5353": "[::1]:5353"} {
		got, err := r.Add(model.Site{URL: "dns://a.example", DNSType: "mx", DNSServer: in})
		if err != nil || got.DNSServer != want || got.DNSType != "MX" {
			t.Errorf("server %q: %+v %v, want %s", in, got, err, want)
		}
	}
}

func TestTCPAndDNSValidation(t *testing.T) {
	r, _ := open(t)
	bad := map[string]model.Site{
		"tcp tanpa port":        {URL: "tcp://db.example"},
		"tcp port nol":          {URL: "tcp://db.example:0"},
		"tcp port besar":        {URL: "tcp://db.example:70000"},
		"tcp dengan path":       {URL: "tcp://db.example:25/x"},
		"tcp host buruk":        {URL: "tcp://db_ex ample:25"},
		"tcp dengan keyword":    {URL: "tcp://db.example:25", Keyword: "SSH"},
		"tcp user:password":     {URL: "tcp://u:p@db.example:25"},
		"dns dengan port":       {URL: "dns://a.example:53"},
		"dns alamat IP":         {URL: "dns://1.2.3.4"},
		"dns nama buruk":        {URL: "dns://-a.example"},
		"dns jenis tak dikenal": {URL: "dns://a.example", DNSType: "CNAME"},
		"dns server buruk":      {URL: "dns://a.example", DNSServer: "bukan server!"},
		"dns server port":       {URL: "dns://a.example", DNSServer: "1.1.1.1:99999"},
		"type tak dikenal":      {URL: "https://a.example", Type: "ftp"},
		"type tak cocok":        {URL: "https://a.example", Type: "tcp"},
		"dns_type di http":      {URL: "https://a.example", DNSType: "A"},
		"dns_server di tcp":     {URL: "tcp://a.example:25", DNSServer: "1.1.1.1"},
	}
	for name, s := range bad {
		var inv *InvalidError
		if _, err := r.Add(s); !errors.As(err, &inv) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}
	if len(r.List()) != 0 {
		t.Fatalf("nothing invalid may be stored: %+v", r.List())
	}
	// DNS may carry an expected answer.
	if s, err := r.Add(model.Site{URL: "dns://a.example", Keyword: "192.0.2.7"}); err != nil || s.Keyword != "192.0.2.7" {
		t.Fatalf("dns keyword: %+v %v", s, err)
	}
}

func TestExistingFilesWithoutTypeStillLoad(t *testing.T) {
	dir := t.TempDir()
	old := `{"sites":[{"id":"abc12345","name":"Lama","url":"https://lama.example","method":"GET","interval_sec":60,"timeout_sec":10,"fail_threshold":3,"created":1}]}`
	os.WriteFile(filepath.Join(dir, "sites.json"), []byte(old), 0o640)
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.List(); len(got) != 1 || got[0].Type != "http" || got[0].Method != "GET" {
		t.Fatalf("a file written before types existed must load as http: %+v", got)
	}
}

// ---- weekly maintenance ----

func TestWeeklyWindowValidationAndStorage(t *testing.T) {
	r, dir := open(t)
	a, _ := r.Add(model.Site{URL: "https://a.com"})

	p, err := r.Patch(a.ID, []byte(`{"maintenance_weekly":[{"days":[6,0],"start":"02:00","minutes":120}]}`))
	if err != nil || len(p.MaintenanceWeekly) != 1 || p.MaintenanceWeekly[0].Start != "02:00" {
		t.Fatalf("%+v %v", p, err)
	}
	r2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := r2.Get(a.ID); len(got.MaintenanceWeekly) != 1 || got.MaintenanceWeekly[0].Days[1] != 0 {
		t.Fatalf("not persisted: %+v", got)
	}

	for name, body := range map[string]string{
		"no days":   `{"maintenance_weekly":[{"days":[],"start":"02:00","minutes":60}]}`,
		"bad hour":  `{"maintenance_weekly":[{"days":[1],"start":"25:00","minutes":60}]}`,
		"too long":  `{"maintenance_weekly":[{"days":[1],"start":"02:00","minutes":2000}]}`,
		"duplicate": `{"maintenance_weekly":[{"days":[1,1],"start":"02:00","minutes":60}]}`,
	} {
		var inv *InvalidError
		if _, err := r.Patch(a.ID, []byte(body)); !errors.As(err, &inv) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}
	if cur, _ := r.Get(a.ID); len(cur.MaintenanceWeekly) != 1 {
		t.Fatal("a rejected patch must not change the stored schedule")
	}

	// Clearing the schedule also clears the skip marker that belonged to it.
	r.Patch(a.ID, []byte(`{"maintenance_skip_until":123}`))
	cleared, err := r.Patch(a.ID, []byte(`{"maintenance_weekly":[]}`))
	if err != nil || cleared.MaintenanceWeekly != nil || cleared.MaintenanceSkipUntil != 0 {
		t.Fatalf("%+v %v", cleared, err)
	}
}

func TestListDoesNotShareWindowSlices(t *testing.T) {
	r, _ := open(t)
	a, _ := r.Add(model.Site{URL: "https://a.com"})
	r.Patch(a.ID, []byte(`{"maintenance_weekly":[{"days":[1,2],"start":"02:00","minutes":60}]}`))
	l := r.List()
	l[0].MaintenanceWeekly[0].Days[0] = 6
	l[0].MaintenanceWeekly[0].Start = "09:99"
	if cur, _ := r.Get(a.ID); cur.MaintenanceWeekly[0].Days[0] != 1 || cur.MaintenanceWeekly[0].Start != "02:00" {
		t.Fatalf("a caller's edit leaked into the registry: %+v", cur.MaintenanceWeekly)
	}
}

// activeWindowNow builds a weekly window that began 30 minutes ago and lasts two hours.
func activeWindowNow() ([]model.Window, time.Time) {
	start := time.Now().Add(-30 * time.Minute).In(jakarta)
	return []model.Window{{Days: []int{int(start.Weekday())}, Start: start.Format("15:04"), Minutes: 120}}, start
}

func TestEndMaintenanceSkipsOnlyTheRunningWindow(t *testing.T) {
	r, _ := open(t)
	a, _ := r.Add(model.Site{URL: "https://a.com"})
	ws, _ := activeWindowNow()
	if _, err := r.Patch(a.ID, []byte(fmt.Sprintf(`{"maintenance_weekly":[{"days":[%d],"start":%q,"minutes":120}]}`, ws[0].Days[0], ws[0].Start))); err != nil {
		t.Fatal(err)
	}
	r.SetMaintenance(a.ID, time.Now().Add(time.Hour).UnixMilli())

	got, err := r.EndMaintenance(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaintenanceUntil != 0 {
		t.Fatal("the one-off window must end")
	}
	if got.MaintenanceSkipUntil <= time.Now().UnixMilli() {
		t.Fatalf("the running weekly window must be marked skipped until its end: %d", got.MaintenanceSkipUntil)
	}
	if len(got.MaintenanceWeekly) != 1 {
		t.Fatal("the schedule itself must survive: only this run is skipped")
	}
}

func TestEndMaintenanceWithNoScheduleLeavesNoSkipMarker(t *testing.T) {
	r, _ := open(t)
	a, _ := r.Add(model.Site{URL: "https://a.com"})
	r.SetMaintenance(a.ID, time.Now().Add(time.Hour).UnixMilli())
	got, err := r.EndMaintenance(a.ID)
	if err != nil || got.MaintenanceUntil != 0 || got.MaintenanceSkipUntil != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := r.EndMaintenance("tidak-ada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

// ---- push sites ----

func TestPushSiteGetsDefaultsAndASecretFromTheServer(t *testing.T) {
	r, dir := open(t)
	s, err := r.Add(model.Site{Type: "push", Name: "Backup malam", PushToken: "dari-klien-jangan-dipakai", URL: "push://apa-saja"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Type != "push" || s.URL != "push://"+s.ID {
		t.Fatalf("the address is only a label, push://<id>: %+v", s)
	}
	if len(s.PushToken) != 32 || s.PushToken == "dari-klien-jangan-dipakai" {
		t.Fatalf("only the server mints secrets, 128 bits of them: %q", s.PushToken)
	}
	if s.IntervalSec != 86400 || s.GraceSec != 300 || s.FailThreshold != 1 || s.TimeoutSec != 0 || s.Method != "" {
		t.Fatalf("a daily job, five minutes of grace, down after the first miss: %+v", s)
	}
	other, _ := r.Add(model.Site{Type: "push"})
	if other.PushToken == s.PushToken || other.Name != "Scheduled job" {
		t.Fatalf("every site has its own token: %+v", other)
	}
	// Type is inferred from the scheme too, and everything survives a restart.
	viaScheme, err := r.Add(model.Site{URL: "push://x"})
	if err != nil || viaScheme.Type != "push" {
		t.Fatalf("%+v %v", viaScheme, err)
	}
	r2, _ := Open(dir)
	if got, _ := r2.Get(s.ID); got.PushToken != s.PushToken || got.URL != s.URL {
		t.Fatalf("not persisted: %+v", got)
	}
}

func TestPushValidation(t *testing.T) {
	r, _ := open(t)
	bad := map[string]model.Site{
		"too frequent":  {Type: "push", IntervalSec: 30},
		"too rare":      {Type: "push", IntervalSec: MaxPushInterval + 1},
		"negative":      {Type: "push", GraceSec: -1},
		"huge grace":    {Type: "push", GraceSec: 90000},
		"keyword":       {Type: "push", Keyword: "ok"},
		"bad label":     {URL: "push://a b"},
		"with port":     {URL: "push://a:80"},
		"dns settings":  {Type: "push", DNSType: "A"},
		"type mismatch": {Type: "push", URL: "https://a.example"},
	}
	for name, s := range bad {
		var inv *InvalidError
		if _, err := r.Add(s); !errors.As(err, &inv) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}
	if len(r.List()) != 0 {
		t.Fatal("nothing invalid may be stored")
	}
	if s, err := r.Add(model.Site{Type: "push", IntervalSec: MaxPushInterval}); err != nil || s.IntervalSec != MaxPushInterval {
		t.Fatalf("a monthly job must fit: %+v %v", s, err)
	}
	// The push-only fields mean nothing on a web site and must not linger there.
	web, _ := r.Add(model.Site{URL: "https://a.example", PushToken: "x", GraceSec: 99})
	if web.PushToken != "" || web.GraceSec != 0 {
		t.Fatalf("%+v", web)
	}
}

func TestPushIdentityCannotBePatched(t *testing.T) {
	r, _ := open(t)
	a, _ := r.Add(model.Site{Type: "push", Name: "Backup"})

	p, err := r.Patch(a.ID, []byte(`{"url":"push://dibajak","push_token":"abcdefabcdefabcdefabcdefabcdefab","interval_sec":3600,"name":"Baru"}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.URL != a.URL || p.PushToken != a.PushToken {
		t.Fatalf("the label and the secret are not editable through PATCH: %+v", p)
	}
	if p.IntervalSec != 3600 || p.Name != "Baru" {
		t.Fatalf("ordinary settings still are: %+v", p)
	}
	for _, body := range []string{`{"type":"http","url":"https://a.example"}`, `{"type":"tcp","url":"tcp://a.example:25"}`} {
		var inv *InvalidError
		if _, err := r.Patch(a.ID, []byte(body)); !errors.As(err, &inv) {
			t.Errorf("%s: a push site cannot become something else: %v", body, err)
		}
	}
	web, _ := r.Add(model.Site{URL: "https://w.example"})
	if _, err := r.Patch(web.ID, []byte(`{"type":"push","url":"push://w"}`)); err == nil {
		t.Fatal("a web site cannot become a push site by patching (it would have no token)")
	}
}

func TestByTokenAndRotation(t *testing.T) {
	r, _ := open(t)
	a, _ := r.Add(model.Site{Type: "push", Name: "A"})
	b, _ := r.Add(model.Site{Type: "push", Name: "B"})
	web, _ := r.Add(model.Site{URL: "https://w.example"})

	if got, ok := r.ByToken(a.PushToken); !ok || got.ID != a.ID {
		t.Fatalf("lookup: %+v %v", got, ok)
	}
	if got, ok := r.ByToken(b.PushToken); !ok || got.ID != b.ID {
		t.Fatalf("lookup b: %+v %v", got, ok)
	}
	for _, token := range []string{"", "x", a.PushToken[:31], a.PushToken + "0", strings.Repeat("0", 32), web.ID} {
		if _, ok := r.ByToken(token); ok {
			t.Errorf("%q must not match anything", token)
		}
	}

	old := a.PushToken
	rotated, err := r.RotateToken(a.ID)
	if err != nil || rotated.PushToken == old || len(rotated.PushToken) != 32 {
		t.Fatalf("%+v %v", rotated, err)
	}
	if _, ok := r.ByToken(old); ok {
		t.Fatal("the old ping URL must stop working at once")
	}
	if got, ok := r.ByToken(rotated.PushToken); !ok || got.ID != a.ID {
		t.Fatal("the new one works")
	}
	if _, err := r.RotateToken(web.ID); err == nil {
		t.Fatal("a web site has no token to rotate")
	}
	if _, err := r.RotateToken("tidak-ada"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
}
