package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wisnuvb/ronda/internal/i18n"
	"github.com/wisnuvb/ronda/internal/model"
)

const tok = `"api":{"token":"0123456789abcdef0123"}`

// Load changes process-wide settings; put them back so other tests see the defaults.
func resetLocale(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		_ = i18n.SetLang("en")
		_ = model.SetTimezone("UTC")
	})
}

func TestLocaleDefaultsAreEnglishAndUTC(t *testing.T) {
	resetLocale(t)
	c, err := Load(write(t, `{`+tok+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Language != "en" || c.Timezone != "UTC" || i18n.Lang() != "en" || model.Loc().String() != "UTC" {
		t.Fatalf("defaults: %q %q / %s %s", c.Language, c.Timezone, i18n.Lang(), model.Loc())
	}
}

func TestLoadSwitchesTheProgramsLanguageAndZone(t *testing.T) {
	resetLocale(t)
	c, err := Load(write(t, `{`+tok+`,"timezone":"Europe/Berlin","language":"id"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Timezone != "Europe/Berlin" || i18n.Lang() != "id" || model.Loc().String() != "Europe/Berlin" {
		t.Fatalf("not applied: %s %s", i18n.Lang(), model.Loc())
	}
	// The default status page title follows the configured language...
	if c.StatusPage.Title != "Status Layanan" {
		t.Fatalf("title: %q", c.StatusPage.Title)
	}
	// ...and so does a validation error, because the language is set before validating.
	_, err = Load(write(t, `{`+tok+`,"language":"id","event_retention_days":1}`))
	if err == nil || !strings.Contains(err.Error(), "event_retention_days harus 7..3650") {
		t.Fatalf("Indonesian validation error expected: %v", err)
	}
}

func TestTimezoneMustBeAnIANAName(t *testing.T) {
	resetLocale(t)
	for _, ok := range []string{"UTC", "Europe/Berlin", "America/New_York", "Asia/Jakarta"} {
		if _, err := Load(write(t, `{`+tok+`,"timezone":"`+ok+`"}`)); err != nil {
			t.Errorf("%s should be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"Mars/Olympus", "Local", "WIB", "GMT+7"} {
		_, err := Load(write(t, `{`+tok+`,"timezone":"`+bad+`"}`))
		if err == nil || !strings.Contains(err.Error(), "timezone") {
			t.Errorf("%q must be refused with a message about the timezone: %v", bad, err)
			continue
		}
		if !strings.Contains(err.Error(), "Europe/Berlin") && !strings.Contains(err.Error(), "IANA") {
			t.Errorf("%q: the message should say what to write instead: %v", bad, err)
		}
	}
}

func TestLanguageMustBeSupported(t *testing.T) {
	resetLocale(t)
	for _, ok := range []string{"en", "id"} {
		if _, err := Load(write(t, `{`+tok+`,"language":"`+ok+`"}`)); err != nil {
			t.Errorf("%s should be accepted: %v", ok, err)
		}
	}
	_, err := Load(write(t, `{`+tok+`,"language":"xx"}`))
	if err == nil || !strings.Contains(err.Error(), "language") || !strings.Contains(err.Error(), "en") {
		t.Fatalf("an unknown language must be refused and name the choices: %v", err)
	}
}

func TestEnvFallsBackToTheOldWebmonNames(t *testing.T) {
	t.Setenv("RONDA_API_TOKEN", "")
	t.Setenv("WEBMON_API_TOKEN", " old-name-token-0123456789 ")
	if got := Getenv("RONDA_API_TOKEN"); got != "old-name-token-0123456789" {
		t.Fatalf("the old WEBMON_ name must still work, trimmed: %q", got)
	}
	t.Setenv("RONDA_API_TOKEN", "new-name-token-0123456789")
	if got := Getenv("RONDA_API_TOKEN"); got != "new-name-token-0123456789" {
		t.Fatalf("RONDA_ wins over WEBMON_: %q", got)
	}
	t.Setenv("RONDA_API_TOKEN", "")
	t.Setenv("WEBMON_API_TOKEN", "")
	if got := Getenv("RONDA_API_TOKEN"); got != "" {
		t.Fatalf("nothing set must be empty: %q", got)
	}
	if got := Getenv("OTHER_THING"); got != "" {
		t.Fatalf("only RONDA_ names have a fallback: %q", got)
	}
}

// A container sets these from its environment instead of editing the config file.
func TestListenTimezoneAndLanguageCanComeFromTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"api":{"token":"0123456789abcdef0123"},"data_dir":"` + dir + `","listen":"127.0.0.1:9191","timezone":"UTC","language":"en"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.SetTimezone("UTC"); _ = i18n.SetLang("en") })
	t.Setenv("RONDA_LISTEN", "0.0.0.0:9191")
	t.Setenv("RONDA_TIMEZONE", "Europe/Berlin")
	t.Setenv("RONDA_LANGUAGE", "id")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "0.0.0.0:9191" || c.Timezone != "Europe/Berlin" || c.Language != "id" {
		t.Fatalf("env must win over the file: %+v", c)
	}
	if model.Loc().String() != "Europe/Berlin" || i18n.Lang() != "id" {
		t.Fatalf("Load must switch the program's zone and language: %s %s", model.Loc(), i18n.Lang())
	}
	t.Setenv("RONDA_TIMEZONE", "Mars/Olympus")
	if _, err := Load(path); err == nil {
		t.Fatal("an invalid RONDA_TIMEZONE must be refused like an invalid timezone in the file")
	}
}
