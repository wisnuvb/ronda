package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// rawDo is do without JSON parsing: an export is a file, and its exact bytes matter.
func rawDo(t *testing.T, h *harness, method, path, body string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestExportAndImportNeedLogin(t *testing.T) {
	h := setup(t)
	c := h.anon(t)
	for _, tc := range []struct{ method, path string }{{"GET", "/api/export"}, {"GET", "/api/export?tokens=1"}, {"POST", "/api/import"}, {"POST", "/api/import?dry_run=1"}} {
		if code, _, _ := c.do(tc.method, tc.path, `{"version":1,"sites":[{"url":"https://a.contoh.com"}]}`); code != 401 {
			t.Errorf("%s %s = %d without credentials, want 401", tc.method, tc.path, code)
		}
	}
	if len(h.reg.List()) != 0 {
		t.Fatal("an unauthenticated import must store nothing")
	}
}

func TestImportFromASessionNeedsTheCSRFToken(t *testing.T) {
	h := setup(t)
	c := h.anon(t)
	if code, _, _ := c.login(password); code != 200 {
		t.Fatal("login failed")
	}
	body := `{"version":1,"sites":[{"url":"https://a.contoh.com"}]}`
	csrf := c.csrf
	c.csrf = ""
	if code, _, _ := c.do("POST", "/api/import", body); code != 403 || len(h.reg.List()) != 0 {
		t.Fatalf("a cookie alone must not import (%d, %d stored)", code, len(h.reg.List()))
	}
	c.csrf = csrf
	if code, out, _ := c.do("POST", "/api/import", body); code != 200 || out["added"] != float64(1) {
		t.Fatalf("with the token it works: %d %v", code, out)
	}
}

func TestExportIsADownloadWithoutSecretsUnlessAsked(t *testing.T) {
	h := setup(t)
	c := h.bearer(t)
	_, push, _ := c.do("POST", "/api/sites", `{"type":"push","name":"Backup"}`)
	c.do("POST", "/api/sites", `{"url":"https://a.contoh.com","name":"A"}`)
	tok := push["push_token"].(string)

	code, body, hdr := rawDo(t, h, "GET", "/api/export", "")
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Disposition"), `attachment; filename="ronda-sites-`) || hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("export headers: %d %v", code, hdr)
	}
	if strings.Contains(body, tok) || strings.Contains(body, "push_token") {
		t.Fatalf("a default export must not carry ping tokens:\n%s", body)
	}
	_, body, _ = rawDo(t, h, "GET", "/api/export?tokens=1", "")
	if !strings.Contains(body, tok) {
		t.Fatal("?tokens=1 must include them")
	}
}

func TestExportOfOneServerImportsIntoAnother(t *testing.T) {
	a, b := setup(t), setup(t)
	ca := a.bearer(t)
	ca.do("POST", "/api/sites", `{"url":"https://toko.contoh.com","name":"Toko","interval_sec":30,"public":true}`)
	ca.do("POST", "/api/sites", `{"url":"tcp://db.contoh.com:5432","name":"DB"}`)
	_, file, _ := rawDo(t, a, "GET", "/api/export", "")

	code, body, _ := rawDo(t, b, "POST", "/api/import?dry_run=1", file)
	if code != 200 || !strings.Contains(body, `"dry_run":true`) || len(b.reg.List()) != 0 {
		t.Fatalf("a preview stores nothing: %d %s (%d stored)", code, body, len(b.reg.List()))
	}
	code, body, _ = rawDo(t, b, "POST", "/api/import", file)
	if code != 200 || !strings.Contains(body, `"added":2`) || len(b.reg.List()) != 2 {
		t.Fatalf("import: %d %s", code, body)
	}
	_, out, _ := b.bearer(t).do("GET", "/api/sites", "")
	sites := out["sites"].([]any)
	if len(sites) != 2 {
		t.Fatalf("the new server lists the imported sites: %v", out)
	}
	code, body, _ = rawDo(t, b, "POST", "/api/import", file)
	if code != 200 || !strings.Contains(body, `"added":0`) || !strings.Contains(body, `"skipped":2`) {
		t.Fatalf("importing the same file twice changes nothing: %d %s", code, body)
	}
}

func TestBadImportFilesAreClientErrors(t *testing.T) {
	h := setup(t)
	for name, body := range map[string]string{"not json": "halo", "empty": `{"version":1,"sites":[]}`, "future": `{"version":99,"sites":[{"url":"https://a.contoh.com"}]}`} {
		if code, out, _ := rawDo(t, h, "POST", "/api/import", body); code != 400 || !strings.Contains(out, `"error"`) {
			t.Errorf("%s = %d %s, want 400 with a message", name, code, out)
		}
	}
	big := `{"version":1,"sites":[{"url":"https://a.contoh.com","keyword":"` + strings.Repeat("x", 9<<20) + `"}]}`
	if code, _, _ := rawDo(t, h, "POST", "/api/import", big); code != 413 {
		t.Errorf("a 9 MB file = %d, want 413", code)
	}
	if len(h.reg.List()) != 0 {
		t.Fatal("refused files leave nothing behind")
	}
}
