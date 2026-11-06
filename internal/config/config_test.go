package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wisnuvb/ronda/internal/auth"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaults(t *testing.T) {
	c, err := Load(write(t, `{"api":{"token":"0123456789abcdef0123"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9191" || c.DataDir != "/var/lib/ronda" || c.MaxConcurrent != 20 || len(c.ControlAddrs) != 2 {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestEmptyControlAddrsDisablesIt(t *testing.T) {
	c, err := Load(write(t, `{"api":{"token":"0123456789abcdef0123"},"control_addrs":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.ControlAddrs == nil || len(c.ControlAddrs) != 0 {
		t.Fatalf("explicit empty list must stay empty: %#v", c.ControlAddrs)
	}
}

func TestTokenRequired(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"api":{"token":"pendek"}}`,
		`{"api":{"token":"ISI dengan keluaran: ronda gen-token"}}`, // a pasted placeholder must not pass as a token
	} {
		if _, err := Load(write(t, body)); err == nil || !strings.Contains(err.Error(), "api.token") {
			t.Errorf("%s: err = %v", body, err)
		}
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("RONDA_API_TOKEN", "dari-env-0123456789")
	t.Setenv("RONDA_TELEGRAM_TOKEN", "1:abc")
	t.Setenv("RONDA_TELEGRAM_CHAT_ID", "111, -100222 ,")
	c, err := Load(write(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.API.Token != "dari-env-0123456789" || c.Notify.TelegramToken != "1:abc" {
		t.Fatalf("env not applied: %+v", c)
	}
	if ids := c.ChatIDs(); len(ids) != 2 || ids[0] != "111" || ids[1] != "-100222" {
		t.Fatalf("chat ids: %v", ids)
	}
}

func TestTelegramNeedsBothFields(t *testing.T) {
	_, err := Load(write(t, `{"api":{"token":"0123456789abcdef0123"},"notify":{"telegram_token":"1:abc"}}`))
	if err == nil || !strings.Contains(err.Error(), "must be set together") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	if _, err := Load(write(t, `{"api":{"token":"0123456789abcdef0123"},"typo_field":1}`)); err == nil {
		t.Fatal("a misspelled key must not be silently ignored")
	}
}

func TestAuthSection(t *testing.T) {
	tok := `"api":{"token":"0123456789abcdef0123"}`

	c, err := Load(write(t, `{`+tok+`}`))
	if err != nil || c.Auth.User != "admin" || c.Auth.PasswordHash != "" {
		t.Fatalf("no dashboard login is allowed, user defaults to admin: %+v %v", c, err)
	}

	if _, err := Load(write(t, `{`+tok+`,"auth":{"password_hash":"bukan-hash"}}`)); err == nil || !strings.Contains(err.Error(), "auth.password_hash") {
		t.Fatalf("a malformed hash must be rejected at startup, got %v", err)
	}

	h, _ := auth.HashPassword("kata-sandi-panjang-1")
	t.Setenv("RONDA_PASSWORD_HASH", h)
	c, err = Load(write(t, `{`+tok+`,"trust_proxy":true}`))
	if err != nil || c.Auth.PasswordHash != h || !c.TrustProxy {
		t.Fatalf("env hash / trust_proxy: %+v %v", c, err)
	}
}

func TestStage3Options(t *testing.T) {
	tok := `"api":{"token":"0123456789abcdef0123"}`

	c, err := Load(write(t, `{`+tok+`}`))
	if err != nil || c.HeartbeatIntervalSec != 60 || c.HeartbeatURL != "" || c.CertWarnDays != nil || c.Notify.TelegramCommands {
		t.Fatalf("defaults: %+v %v", c, err)
	}

	c, err = Load(write(t, `{`+tok+`,"cert_warn_days":[]}`))
	if err != nil || c.CertWarnDays == nil || len(c.CertWarnDays) != 0 {
		t.Fatalf("an explicit empty list disables the warning and must stay empty: %#v %v", c.CertWarnDays, err)
	}

	t.Setenv("RONDA_HEARTBEAT_URL", "https://hc-ping.example/abc")
	c, err = Load(write(t, `{`+tok+`}`))
	if err != nil || c.HeartbeatURL != "https://hc-ping.example/abc" {
		t.Fatalf("env override: %+v %v", c, err)
	}
	t.Setenv("RONDA_HEARTBEAT_URL", "")

	for name, body := range map[string]string{
		"commands without telegram": `{` + tok + `,"notify":{"telegram_commands":true}}`,
		"warn day zero":             `{` + tok + `,"cert_warn_days":[7,0]}`,
		"warn day huge":             `{` + tok + `,"cert_warn_days":[999]}`,
		"heartbeat not a url":       `{` + tok + `,"heartbeat_url":"bukan url"}`,
		"heartbeat ftp":             `{` + tok + `,"heartbeat_url":"ftp://x.example"}`,
		"heartbeat too fast":        `{` + tok + `,"heartbeat_interval_sec":2}`,
		"heartbeat too slow":        `{` + tok + `,"heartbeat_interval_sec":99999}`,
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
	ok := `{` + tok + `,"notify":{"telegram_token":"1:abc","telegram_chat_id":"5","telegram_commands":true}}`
	if c, err := Load(write(t, ok)); err != nil || !c.Notify.TelegramCommands {
		t.Fatalf("commands with telegram configured: %+v %v", c, err)
	}
}

func TestBatchingOptions(t *testing.T) {
	tok := `"api":{"token":"0123456789abcdef0123"}`

	c, err := Load(write(t, `{`+tok+`}`))
	if err != nil || *c.Notify.BatchSec != 8 || c.Notify.DigestMin != 3 {
		t.Fatalf("defaults: batch=%v digest=%d %v", c.Notify.BatchSec, c.Notify.DigestMin, err)
	}
	c, err = Load(write(t, `{`+tok+`,"notify":{"batch_sec":0}}`))
	if err != nil || c.Notify.BatchSec == nil || *c.Notify.BatchSec != 0 {
		t.Fatalf("an explicit 0 must stay 0 (batching off), not become the default: %v %v", c.Notify.BatchSec, err)
	}
	c, err = Load(write(t, `{`+tok+`,"notify":{"batch_sec":20,"digest_min":5}}`))
	if err != nil || *c.Notify.BatchSec != 20 || c.Notify.DigestMin != 5 {
		t.Fatalf("explicit values: %v %v", c.Notify, err)
	}
	for name, body := range map[string]string{
		"negative gap": `{` + tok + `,"notify":{"batch_sec":-1}}`,
		"huge gap":     `{` + tok + `,"notify":{"batch_sec":900}}`,
		"digest of 1":  `{` + tok + `,"notify":{"digest_min":1}}`,
		"digest huge":  `{` + tok + `,"notify":{"digest_min":500}}`,
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestEventRetention(t *testing.T) {
	tok := `"api":{"token":"0123456789abcdef0123"}`
	c, err := Load(write(t, `{`+tok+`}`))
	if err != nil || c.EventRetentionDays != 365 {
		t.Fatalf("default: %d %v", c.EventRetentionDays, err)
	}
	if c, err = Load(write(t, `{`+tok+`,"event_retention_days":90}`)); err != nil || c.EventRetentionDays != 90 {
		t.Fatalf("explicit: %v %v", c, err)
	}
	for _, d := range []string{"3", "5000", "-1"} {
		if _, err := Load(write(t, `{`+tok+`,"event_retention_days":`+d+`}`)); err == nil {
			t.Errorf("%s days must be rejected", d)
		}
	}
}

func TestStatusPageOptions(t *testing.T) {
	tok := `"api":{"token":"0123456789abcdef0123"}`
	c, err := Load(write(t, `{`+tok+`}`))
	if err != nil || c.StatusPage.Enabled || c.StatusPage.Title != "Service status" {
		t.Fatalf("off by default, with a default title: %+v %v", c.StatusPage, err)
	}
	c, err = Load(write(t, `{`+tok+`,"status_page":{"enabled":true,"title":"Layanan Kami","description":"Kabar terbaru"}}`))
	if err != nil || !c.StatusPage.Enabled || c.StatusPage.Title != "Layanan Kami" {
		t.Fatalf("%+v %v", c.StatusPage, err)
	}
	long := strings.Repeat("a", 101)
	for name, body := range map[string]string{
		"long title":  `{` + tok + `,"status_page":{"title":"` + long + `"}}`,
		"control":     `{` + tok + `,"status_page":{"title":"a\u0000b"}}`,
		"unknown key": `{` + tok + `,"status_page":{"judul":"x"}}`,
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestRemindAfterMin(t *testing.T) {
	base := `{"api":{"token":"0123456789abcdef0123"},"notify":{%s}}`
	load := func(extra string) (*Config, error) { return Load(write(t, strings.Replace(base, "%s", extra, 1))) }

	c, err := load(``)
	if err != nil || c.Notify.RemindAfterMin != nil {
		t.Fatalf("missing must stay nil so the monitor applies its default: %v %v", c.Notify.RemindAfterMin, err)
	}
	if c, err = load(`"remind_after_min": []`); err != nil || c.Notify.RemindAfterMin == nil || len(c.Notify.RemindAfterMin) != 0 {
		t.Fatalf("an empty list must stay empty (reminders off), not become nil: %#v %v", c.Notify.RemindAfterMin, err)
	}
	if c, err = load(`"remind_after_min": [30, 120]`); err != nil || len(c.Notify.RemindAfterMin) != 2 {
		t.Fatalf("valid list: %v %v", c.Notify.RemindAfterMin, err)
	}
	for _, bad := range []string{`[0]`, `[4]`, `[-60]`, `[43201]`, `[60, 1]`} {
		if _, err := load(`"remind_after_min": ` + bad); err == nil || !strings.Contains(err.Error(), "remind_after_min") {
			t.Errorf("%s must be rejected with a message naming the field, got %v", bad, err)
		}
	}
}

func TestReportOptions(t *testing.T) {
	load := func(extra string) (*Config, error) {
		return Load(write(t, `{"api":{"token":"0123456789abcdef0123"},"notify":{"webhook_url":"http://127.0.0.1:1/x"`+extra+`}}`))
	}
	c, err := load(``)
	if err != nil || c.Notify.ReportDaily != "" || c.Notify.ReportWeekly != "" || c.ReportWeeklyDay() != 1 {
		t.Fatalf("off by default, Monday when on: %+v %v", c.Notify, err)
	}
	c, err = load(`,"report_daily":"08:00","report_weekly":"09:30","report_weekly_day":0`)
	if err != nil || c.Notify.ReportDaily != "08:00" || c.ReportWeeklyDay() != 0 {
		t.Fatalf("Sunday (0) is a real choice and must not be mistaken for 'missing': %+v %v", c.Notify, err)
	}
	for _, bad := range []string{`,"report_daily":"8:00"`, `,"report_weekly":"24:00"`, `,"report_weekly":"08:00","report_weekly_day":7`, `,"report_weekly":"08:00","report_weekly_day":-1`} {
		if _, err := load(bad); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
	// Reports with nowhere to go are a mistake worth refusing at start, not discovering a week later.
	if _, err := Load(write(t, `{"api":{"token":"0123456789abcdef0123"},"notify":{"report_daily":"08:00"}}`)); err == nil || !strings.Contains(err.Error(), "periodic reports need") {
		t.Fatalf("a report without Telegram or a webhook must be refused: %v", err)
	}
}

func TestRecipients(t *testing.T) {
	load := func(notify string) (*Config, error) {
		return Load(write(t, `{"api":{"token":"0123456789abcdef0123"},"notify":{`+notify+`}}`))
	}
	const base = `"telegram_token":"123456789:AAH-abcdefghijklmnopqrstuvwxyz0123456","telegram_chat_id":"111"`
	c, err := load(base + `,"recipients":{"klien-a":"-1001, -1002","tim":"222"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Routes()["klien-a"]; len(got) != 2 || got[0] != "-1001" || got[1] != "-1002" {
		t.Fatalf("several ids per recipient, spaces tolerated: %v", got)
	}
	if got := c.RecipientNames(); len(got) != 2 || got[0] != "klien-a" || got[1] != "tim" {
		t.Fatalf("names are sorted: %v", got)
	}
	for name, body := range map[string]string{
		"reserved name":   base + `,"recipients":{"default":"1"}`,
		"uppercase":       base + `,"recipients":{"Klien":"1"}`,
		"no chat":         base + `,"recipients":{"klien-a":" , "}`,
		"no bot token":    `"recipients":{"klien-a":"1"}`,
		"space in a name": base + `,"recipients":{"klien a":"1"}`,
	} {
		if _, err := load(body); err == nil || !strings.Contains(err.Error(), "recipients") {
			t.Errorf("%s must be refused with a message naming notify.recipients: %v", name, err)
		}
	}
	if c, err = load(base); err != nil || len(c.Routes()) != 0 {
		t.Fatalf("recipients are optional: %v", err)
	}
}
