package registry

import (
	"encoding/json"
	"github.com/wisnuvb/ronda/sitespec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/model"
)

func openTemp(t *testing.T) *Registry {
	t.Helper()
	r, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.SetRecipients([]string{"klien-a", "tim"})
	return r
}

// fixtures set every configuration field of model.Site at least once between them.
func fixtures() []model.Site {
	return []model.Site{
		{Name: "Toko", URL: "https://toko.contoh.com/sehat", IntervalSec: 30, TimeoutSec: 5, FailThreshold: 2,
			ExpectStatus: []int{200, 204}, Keyword: "Keranjang", NoRedirect: true, UserAgent: "ronda-uji", AllowPrivate: true,
			Paused: true, Public: true, NotifyTo: []string{"default", "klien-a"},
			MaintenanceWeekly: []model.Window{{Days: []int{0, 6}, Start: "02:00", Minutes: 90}}},
		{Name: "Rekaman MX", URL: "dns://contoh.com", DNSType: "MX", DNSServer: "8.8.8.8:53", Keyword: "mail.contoh.com", AllowPrivate: true},
		{Name: "Basis Data", URL: "tcp://db.contoh.com:5432"},
		{Name: "Cek Ringan", URL: "https://ringan.contoh.com", Method: "HEAD"},
		{Name: "Backup Harian", Type: "push", IntervalSec: 3600, GraceSec: 120, FailThreshold: 2, Public: true},
	}
}

// stable strips what is allowed to differ between two installations holding the same configuration.
func stable(s model.Site) model.Site {
	s.ID, s.Created, s.MaintenanceUntil, s.MaintenanceSkipUntil, s.PushToken = "", 0, 0, 0, ""
	if s.Type == sitespec.TypePush {
		s.URL = ""
	}
	return s
}

func TestFixturesCoverEveryConfigurationField(t *testing.T) {
	skip := map[string]bool{"ID": true, "Created": true, "MaintenanceUntil": true, "MaintenanceSkipUntil": true, "PushToken": true, "URL": false}
	covered := map[string]bool{}
	for _, s := range fixtures() {
		v := reflect.ValueOf(s)
		for i := 0; i < v.NumField(); i++ {
			if !v.Field(i).IsZero() {
				covered[v.Type().Field(i).Name] = true
			}
		}
	}
	typ := reflect.TypeOf(model.Site{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if !skip[name] && !covered[name] {
			t.Errorf("model.Site.%s is not set by any fixture: add it, so that export/import is proven to carry it", name)
		}
	}
}

func TestExportThenImportReproducesEveryConfigurationField(t *testing.T) {
	a := openTemp(t)
	for _, s := range fixtures() {
		if _, err := a.Add(s); err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
	}
	exp, err := a.Export(false)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(exp)

	b := openTemp(t)
	res, err := b.Import(data, false)
	if err != nil || res.Added != 5 || res.Skipped != 0 || res.Invalid != 0 {
		t.Fatalf("import: %+v %v", res, err)
	}
	want, got := a.List(), b.List()
	for i := range want {
		if !reflect.DeepEqual(stable(want[i]), stable(got[i])) {
			t.Errorf("site %q changed in the round trip:\n want %+v\n  got %+v", want[i].Name, stable(want[i]), stable(got[i]))
		}
		if got[i].ID == want[i].ID && want[i].ID != "" {
			t.Errorf("%s: an import must mint its own ids", want[i].Name)
		}
	}
}

func TestExportHidesIdsStateAndSecrets(t *testing.T) {
	r := openTemp(t)
	push, _ := r.Add(model.Site{Type: "push", Name: "Backup"})
	web, _ := r.Add(model.Site{URL: "https://a.contoh.com"})
	if _, err := r.SetMaintenance(web.ID, time.Now().Add(2*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}

	exp, _ := r.Export(false)
	raw, _ := json.Marshal(exp)
	for _, leak := range []string{push.PushToken, push.ID, web.ID, "maintenance_until", `"created"`, `"id"`, "push_token", "push://"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("a default export must not contain %q:\n%s", leak, raw)
		}
	}
	if exp.Version != ExportVersion || exp.ExportedAt == 0 {
		t.Fatalf("header: %+v", exp)
	}

	withTokens, _ := r.Export(true)
	raw, _ = json.Marshal(withTokens)
	if !strings.Contains(string(raw), push.PushToken) {
		t.Fatal("includeTokens must carry the ping token, or a migrated job would need editing")
	}
	if strings.Contains(string(raw), push.ID) {
		t.Fatalf("ids still stay out of an export with tokens:\n%s", raw)
	}
}

func TestImportKeepsAWellFormedUnusedTokenAndMintsOtherwise(t *testing.T) {
	const kept = "0123456789abcdef0123456789abcdef"
	file := func(tok string) []byte {
		b, _ := json.Marshal(map[string]any{"version": 1, "sites": []map[string]any{{"type": "push", "name": "Backup", "push_token": tok}}})
		return b
	}
	r := openTemp(t)
	if _, err := r.Import(file(kept), false); err != nil {
		t.Fatal(err)
	}
	if got := r.List()[0]; got.PushToken != kept || got.URL != "push://"+got.ID {
		t.Fatalf("a good token is kept and the label rebuilt: %+v", got)
	}
	if _, ok := r.ByToken(kept); !ok {
		t.Fatal("the kept token must work for pings at once")
	}

	// Another site claiming the same token must not share it: that would let one job's ping count for another.
	other := []byte(`{"version":1,"sites":[{"type":"push","name":"Laporan","push_token":"` + kept + `"}]}`)
	if _, err := r.Import(other, false); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range r.List() {
		if seen[s.PushToken] {
			t.Fatalf("two sites share a token: %+v", r.List())
		}
		seen[s.PushToken] = true
	}

	for name, tok := range map[string]string{"empty": "", "short": "abc", "uppercase": strings.ToUpper(kept), "not hex": strings.Repeat("z", 32)} {
		r2 := openTemp(t)
		if _, err := r2.Import(file(tok), false); err != nil {
			t.Fatal(name, err)
		}
		if got := r2.List()[0].PushToken; got == tok || !tokenShape.MatchString(got) {
			t.Errorf("%s token must be replaced by a freshly minted one, got %q", name, got)
		}
	}
}

func TestImportIsIdempotentAndSkipsDuplicatesInsideTheFile(t *testing.T) {
	r := openTemp(t)
	data := []byte(`{"version":1,"sites":[
		{"url":"https://a.contoh.com","name":"A"},
		{"url":"HTTPS://A.CONTOH.COM","name":"A lagi"},
		{"url":"tcp://db.contoh.com:5432"},
		{"type":"push","name":"Backup"},
		{"type":"push","name":"backup"}]}`)
	res, err := r.Import(data, false)
	if err != nil || res.Added != 3 || res.Skipped != 2 {
		t.Fatalf("first import: %+v %v", res, err)
	}
	before := r.List()
	res, err = r.Import(data, false)
	if err != nil || res.Added != 0 || res.Skipped != 5 {
		t.Fatalf("the same file again must change nothing: %+v %v", res, err)
	}
	if !reflect.DeepEqual(before, r.List()) {
		t.Fatal("the registry changed on a repeated import")
	}
}

func TestImportReportsBadSitesByPositionAndStillAddsTheGoodOnes(t *testing.T) {
	r := openTemp(t)
	data := []byte(`{"version":1,"sites":[
		{"url":"https://baik.contoh.com"},
		{"url":"ftp://salah.contoh.com"},
		{"url":"https://x.contoh.com","interval_sec":1},
		{"url":"https://y.contoh.com","bukan_kolom":true},
		{"url":"https://user:pw@z.contoh.com"},
		{"url":"tcp://db.contoh.com:5432"}]}`)
	res, err := r.Import(data, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 || res.Invalid != 4 || len(res.Items) != 6 {
		t.Fatalf("summary: %+v", res)
	}
	for i, wantStatus := range []string{ImportAdded, ImportInvalid, ImportInvalid, ImportInvalid, ImportInvalid, ImportAdded} {
		it := res.Items[i]
		if it.Index != i+1 || it.Status != wantStatus || (wantStatus == ImportInvalid && it.Reason == "") {
			t.Errorf("item %d: %+v, want %s with a reason for failures", i+1, it, wantStatus)
		}
	}
	if len(r.List()) != 2 {
		t.Fatalf("only the valid ones are stored: %d", len(r.List()))
	}
}

func TestDryRunStoresNothingButPredictsTheRealRun(t *testing.T) {
	r := openTemp(t)
	_, _ = r.Add(model.Site{URL: "https://sudah.contoh.com"})
	data := []byte(`{"version":1,"sites":[{"url":"https://sudah.contoh.com"},{"url":"https://baru.contoh.com"},{"url":"nonsens"}]}`)
	dry, err := r.Import(data, true)
	if err != nil || !dry.DryRun || len(r.List()) != 1 {
		t.Fatalf("dry run: %+v %v, %d stored", dry, err, len(r.List()))
	}
	real, _ := r.Import(data, false)
	if dry.Added != real.Added || dry.Skipped != real.Skipped || dry.Invalid != real.Invalid || real.DryRun {
		t.Fatalf("the preview must match the real run: %+v vs %+v", dry, real)
	}
}

func TestImportIgnoresIdsAndTransientState(t *testing.T) {
	r := openTemp(t)
	data := []byte(`{"version":1,"sites":[{"id":"deadbeef","created":1,"url":"https://a.contoh.com","maintenance_until":4102444800000,"maintenance_skip_until":4102444800000}]}`)
	if res, err := r.Import(data, false); err != nil || res.Added != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	s := r.List()[0]
	if s.ID == "deadbeef" || s.Created <= 1 || s.MaintenanceUntil != 0 || s.MaintenanceSkipUntil != 0 {
		t.Fatalf("a file must not dictate ids, creation time, or silence a site: %+v", s)
	}
}

func TestImportRefusesBadFiles(t *testing.T) {
	r := openTemp(t)
	for name, body := range map[string]string{
		"not json":    `halo`,
		"empty list":  `{"version":1,"sites":[]}`,
		"no sites":    `{"version":1}`,
		"newer file":  `{"version":99,"sites":[{"url":"https://a.contoh.com"}]}`,
		"wrong shape": `[1,2,3]`,
	} {
		if _, err := r.Import([]byte(body), false); err == nil {
			t.Errorf("%s must be refused", name)
		} else if _, ok := err.(*model.InvalidError); !ok {
			t.Errorf("%s: %T is not an InvalidError, so the API would answer 500", name, err)
		}
	}
	if len(r.List()) != 0 {
		t.Fatal("a refused file must leave nothing behind")
	}
}

func TestImportStopsAtTheSiteLimitButKeepsWhatFits(t *testing.T) {
	r := openTemp(t)
	var sb strings.Builder
	sb.WriteString(`{"version":1,"sites":[`)
	for i := 0; i < MaxSites+3; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"url":"https://s` + itoa(i) + `.contoh.com"}`)
	}
	sb.WriteString(`]}`)
	res, err := r.Import([]byte(sb.String()), false)
	if err != nil || res.Added != MaxSites || res.Invalid != 3 || len(r.List()) != MaxSites {
		t.Fatalf("%+v %v (%d stored)", res, err, len(r.List()))
	}
	if last := res.Items[len(res.Items)-1]; !strings.Contains(last.Reason, "limit of") {
		t.Fatalf("the reason must say why: %+v", last)
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }
