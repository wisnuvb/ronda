package config

import (
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
