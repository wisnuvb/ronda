package api

import (
	"strings"
	"testing"
)

// A public demo must let a visitor look at everything and change nothing, through every door: the API
// methods, the push-monitor URLs (a GET that changes state), and the import.
func TestReadOnlyDemoLooksButDoesNotTouch(t *testing.T) {
	h := setup(t, func(o *Options) { o.NoAuth, o.ReadOnly = true, true })
	c := h.anon(t)

	for _, p := range []string{"/api/sites", "/api/incidents", "/api/events", "/api/recipients", "/api/session", "/healthz"} {
		if code, _, _ := c.do("GET", p, ""); code != 200 {
			t.Errorf("GET %s must still work in the demo: %d", p, code)
		}
	}

	refused := []struct{ method, path, body string }{
		{"POST", "/api/sites", `{"name":"x","url":"https://example.com"}`},
		{"PATCH", "/api/sites/abc", `{"name":"y"}`},
		{"DELETE", "/api/sites/abc", ""},
		{"POST", "/api/sites/abc/pause", ""},
		{"POST", "/api/sites/abc/check", ""},
		{"POST", "/api/sites/abc/token", ""},
		{"POST", "/api/sites/abc/maintenance", `{"minutes":30}`},
		{"POST", "/api/check", `{"url":"https://example.com"}`},
		{"POST", "/api/import", `{}`},
		{"POST", "/api/password", `{}`},
		{"POST", "/api/login", `{}`},
		{"PUT", "/api/sites", ""},
		{"OPTIONS", "/api/sites", ""},
		{"GET", "/ping/abcdef", ""},
		{"POST", "/ping/abcdef/fail", ""},
	}
	for _, r := range refused {
		code, out, _ := c.do(r.method, r.path, r.body)
		if code != 403 {
			t.Errorf("%s %s must be refused in the demo, got %d", r.method, r.path, code)
			continue
		}
		if msg, _ := out["error"].(string); !strings.Contains(msg, "read-only demo") {
			t.Errorf("%s %s: the refusal should say why: %v", r.method, r.path, out)
		}
	}

	// Nothing was created, even by the request that looked valid.
	code, body, _ := c.do("GET", "/api/sites", "")
	if sites, _ := body["sites"].([]any); code != 200 || len(sites) != 0 {
		t.Errorf("the demo store must be untouched: %d %v", code, body)
	}
}

func TestReadOnlyDemoTellsTheDashboardAndOnlyThen(t *testing.T) {
	h := setup(t)
	if _, body, _ := rawDo(t, h, "GET", "/app-config.js", ""); strings.Contains(body, "demo") {
		t.Fatalf("a normal server must not claim to be a demo: %s", body)
	}
	h2 := setup(t, func(o *Options) { o.NoAuth, o.ReadOnly = true, true })
	if _, body, _ := rawDo(t, h2, "GET", "/app-config.js", ""); !strings.Contains(body, `"demo":true`) {
		t.Fatalf("the demo must tell the dashboard so it can show its banner: %s", body)
	}
}

func TestReadOnlyRefusalIsTranslated(t *testing.T) {
	h := setup(t, func(o *Options) { o.NoAuth, o.ReadOnly = true, true })
	inIndonesian(t)
	_, out, _ := h.anon(t).do("POST", "/api/sites", `{}`)
	if out["error"] != "Ini demo baca-saja: perubahan dimatikan." {
		t.Fatalf("indonesian: %v", out)
	}
}
