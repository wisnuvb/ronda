package api

import (
	"strings"
	"testing"

	"github.com/wisnuvb/ronda/i18n"
)

func inIndonesian(t *testing.T) {
	t.Helper()
	if err := i18n.SetLang("id"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.SetLang("en") })
}

// One process speaks one language: the same wrong login is reported in the configured language, which is
// how the dashboard shows it, and the answer for a wrong user and a wrong password stays identical.
func TestErrorsFollowTheConfiguredLanguage(t *testing.T) {
	h := setup(t)
	code, en, _ := h.anon(t).do("POST", "/api/login", `{"user":"orang-lain","password":"x"}`)
	if code != 401 || en["error"] != "Wrong username or password" {
		t.Fatalf("english: %d %v", code, en)
	}

	inIndonesian(t)
	code, id, _ := h.anon(t).do("POST", "/api/login", `{"user":"orang-lain","password":"x"}`)
	if code != 401 || id["error"] != "nama pengguna atau kata sandi salah" {
		t.Fatalf("indonesian: %d %v", code, id)
	}
	if code, out, _ := h.anon(t).do("GET", "/api/sites", ""); code != 401 || out["error"] != "belum masuk" {
		t.Fatalf("not logged in, in indonesian: %d %v", code, out)
	}
}

func TestAppConfigTellsTheDashboardItsLanguageAndZone(t *testing.T) {
	h := setup(t)
	code, body, hdr := rawDo(t, h, "GET", "/app-config.js", "")
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/javascript") || hdr.Get("Cache-Control") != "no-cache" {
		t.Fatalf("headers: %d %v", code, hdr)
	}
	if !strings.HasPrefix(body, "window.RONDA = ") || !strings.Contains(body, `"lang":"en"`) || !strings.Contains(body, `"timezone":`) {
		t.Fatalf("body: %q", body)
	}

	inIndonesian(t)
	_, body, _ = rawDo(t, h, "GET", "/app-config.js", "")
	if !strings.Contains(body, `"lang":"id"`) {
		t.Fatalf("after switching language: %q", body)
	}
}
