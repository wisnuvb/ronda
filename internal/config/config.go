// Package config loads ronda's JSON config file, with secrets overridable from the environment.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/wisnuvb/ronda/i18n"
	"github.com/wisnuvb/ronda/internal/auth"
	"github.com/wisnuvb/ronda/model"
	"github.com/wisnuvb/ronda/schedule"
)

type Config struct {
	Listen  string `json:"listen"`
	DataDir string `json:"data_dir"`

	// Timezone is an IANA zone name ("Europe/Berlin", "America/New_York", "Asia/Jakarta"). It decides what
	// clock time people read in notifications and the dashboard, what "08:00" means in report times and
	// maintenance windows, and where a day begins in the uptime history. Daylight saving is followed.
	// Missing: UTC.
	Timezone string `json:"timezone"`

	// Language is the language of notifications, the bot, API messages and the dashboard: "en" or "id".
	// Missing: en.
	Language string `json:"language"`

	// API is for scripts and the CLI: a static bearer token.
	API struct {
		Token string `json:"token"`
	} `json:"api"`

	// Auth is for the dashboard: a login with a hashed password. Leave password_hash
	// empty to run without a dashboard login (the API and CLI keep working).
	Auth struct {
		User         string `json:"user"`
		PasswordHash string `json:"password_hash"`
	} `json:"auth"`

	// TrustProxy makes ronda believe X-Forwarded-For/-Proto from a reverse proxy.
	// Only enable it when every request really comes through one.
	TrustProxy bool `json:"trust_proxy"`

	Notify struct {
		TelegramToken  string `json:"telegram_token"`
		TelegramChatID string `json:"telegram_chat_id"` // one id, or several separated by commas
		WebhookURL     string `json:"webhook_url"`
		// TelegramCommands lets the chats in telegram_chat_id manage ronda (/status, /tambah, /hapus ...).
		// Off by default: in a group, every member would be able to use them.
		TelegramCommands bool `json:"telegram_commands"`
		// TelegramAdminIDs are the Telegram user ids (not chat ids) allowed to change things (/tambah,
		// /hapus, /jeda, ...) from a GROUP chat. Anyone in an allowed group can still read /status and
		// /list. A private chat needs no entry: only one person is in it.
		TelegramAdminIDs []int64 `json:"telegram_admin_ids"`
		// BatchSec: hold alerts until this many seconds pass with nothing new, so a burst (a server
		// dying takes dozens of sites down) is reported as one message. Missing: 8. 0 turns it off.
		BatchSec *int `json:"batch_sec"`
		// DigestMin: how many consecutive down (or up) alerts become one summary. Missing: 3.
		DigestMin int `json:"digest_min"`
		// Recipients names extra Telegram chats a site can send its notifications to (site field notify_to),
		// e.g. {"klien-a": "-1001234567890", "tim": "111,222"}: name -> one chat id or several separated by
		// commas. A site with no notify_to goes to telegram_chat_id as before.
		Recipients map[string]string `json:"recipients"`
		// ReportDaily and ReportWeekly ("HH:MM", in the configured timezone) switch on a summary of how the sites did. Empty = off.
		// ReportWeeklyDay is the weekday of the weekly one (0 = Sunday ... 6 = Saturday); missing: 1 (Monday).
		ReportDaily     string `json:"report_daily"`
		ReportWeekly    string `json:"report_weekly"`
		ReportWeeklyDay *int   `json:"report_weekly_day"`
		// RemindAfterMin: while a site stays down, repeat the alert this many minutes after it went down,
		// then once a day. Missing: [60, 360, 1440]. An empty list turns reminders off.
		RemindAfterMin []int `json:"remind_after_min"`
	} `json:"notify"`

	// StatusPage is a public page (/status) listing the sites marked "public": name, status, 90-day
	// uptime and recent incidents, never a URL or a technical detail. Off by default.
	StatusPage struct {
		Enabled     bool   `json:"enabled"`
		Title       string `json:"title"`
		Description string `json:"description"`
	} `json:"status_page"`

	// EventRetentionDays is how long down/up events, and therefore the incident list, are kept. Missing: 365.
	EventRetentionDays int `json:"event_retention_days"`

	// CertWarnDays: warn once as a certificate crosses each of these days to expiry.
	// Missing: 14, 7, 3, 1. An empty list turns the warning off.
	CertWarnDays []int `json:"cert_warn_days"`

	// HeartbeatURL is pinged every HeartbeatIntervalSec (default 60) while checks are running, so an
	// outside service (e.g. healthchecks.io) can tell you when ronda itself has died. It is a secret.
	HeartbeatURL         string `json:"heartbeat_url"`
	HeartbeatIntervalSec int    `json:"heartbeat_interval_sec"`

	// ControlAddrs are dialled to tell "the site is down" from "our own network is down".
	// Missing: defaults below. Empty list: disabled.
	ControlAddrs  []string `json:"control_addrs"`
	MaxConcurrent int      `json:"max_concurrent_checks"`
}

const MinTokenLen = 16

// Load reads path, applies defaults and environment overrides, and validates.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// The language and zone come first (and may be overridden from the environment, which is how a container sets
	// them): every error after this point, validation included, is in that language.
	for dst, key := range map[*string]string{&c.Timezone: "RONDA_TIMEZONE", &c.Language: "RONDA_LANGUAGE"} {
		if v := Getenv(key); v != "" {
			*dst = v
		}
	}
	if err := c.ApplyLocale(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.ApplyDefaults()
	c.applyEnv()
	return c, c.Validate()
}

// ApplyLocale makes the configured language and time zone the program's. Load calls it; it is exported for
// callers that build a Config by hand.
func (c *Config) ApplyLocale() error {
	if c.Language == "" {
		c.Language = i18n.Default
	}
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	if err := i18n.SetLang(c.Language); err != nil {
		return fmt.Errorf("language: %w", err)
	}
	return model.SetTimezone(c.Timezone)
}

func (c *Config) ApplyDefaults() {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:9191"
	}
	if c.DataDir == "" {
		c.DataDir = "/var/lib/ronda"
	}
	if c.ControlAddrs == nil {
		c.ControlAddrs = []string{"1.1.1.1:443", "8.8.8.8:443"}
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 20
	}
	if c.Auth.User == "" {
		c.Auth.User = "admin"
	}
	if c.HeartbeatIntervalSec == 0 {
		c.HeartbeatIntervalSec = 60
	}
	if c.EventRetentionDays == 0 {
		c.EventRetentionDays = 365
	}
	if c.StatusPage.Title == "" {
		c.StatusPage.Title = i18n.T("Service status")
	}
	if c.Notify.BatchSec == nil {
		eight := 8
		c.Notify.BatchSec = &eight
	}
	if c.Notify.DigestMin == 0 {
		c.Notify.DigestMin = 3
	}
}

// Getenv returns the trimmed value of an environment variable named RONDA_*. Before its first release the
// project was called "webmon", so the same name with a WEBMON_ prefix is still read when the RONDA_ one is
// unset; it logs a warning, and the fallback will be removed in a future release.
func Getenv(key string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	if rest, ok := strings.CutPrefix(key, "RONDA_"); ok {
		old := "WEBMON_" + rest
		if v := strings.TrimSpace(os.Getenv(old)); v != "" {
			log.Printf("config: %s is deprecated and will stop working; rename it to %s", old, key)
			return v
		}
	}
	return ""
}

func (c *Config) applyEnv() {
	set := func(dst *string, key string) {
		if v := Getenv(key); v != "" {
			*dst = v
		}
	}
	set(&c.Listen, "RONDA_LISTEN")
	set(&c.API.Token, "RONDA_API_TOKEN")
	set(&c.HeartbeatURL, "RONDA_HEARTBEAT_URL")
	set(&c.Auth.PasswordHash, "RONDA_PASSWORD_HASH")
	set(&c.Notify.TelegramToken, "RONDA_TELEGRAM_TOKEN")
	set(&c.Notify.TelegramChatID, "RONDA_TELEGRAM_CHAT_ID")
	set(&c.Notify.WebhookURL, "RONDA_WEBHOOK_URL")
}

func (c *Config) Validate() error {
	if len(c.API.Token) < MinTokenLen || strings.ContainsFunc(c.API.Token, unicode.IsSpace) {
		return errors.New(i18n.T("api.token is required (at least %d characters, no spaces); create one with: ronda gen-token", MinTokenLen))
	}
	if c.Auth.PasswordHash != "" {
		if err := auth.ValidHash(c.Auth.PasswordHash); err != nil {
			return fmt.Errorf("auth.password_hash: %w", err)
		}
	}
	for name, at := range map[string]string{"report_daily": c.Notify.ReportDaily, "report_weekly": c.Notify.ReportWeekly} {
		if at == "" {
			continue
		}
		if _, err := schedule.ParseStart(at); err != nil {
			return fmt.Errorf("notify.%s: %w", name, err)
		}
	}
	if d := c.ReportWeeklyDay(); d < 0 || d > 6 {
		return errors.New(i18n.T("notify.report_weekly_day must be 0 (Sunday) to 6 (Saturday)"))
	}
	if (c.Notify.ReportDaily != "" || c.Notify.ReportWeekly != "") && c.Notify.TelegramToken == "" && c.Notify.WebhookURL == "" {
		return errors.New(i18n.T("periodic reports need notify.telegram_token or notify.webhook_url: there is nowhere to send them"))
	}
	if len(c.Notify.Recipients) > 0 && c.Notify.TelegramToken == "" {
		return errors.New(i18n.T("notify.recipients needs notify.telegram_token"))
	}
	if len(c.Notify.Recipients) > 50 {
		return errors.New(i18n.T("notify.recipients allows at most 50 recipients"))
	}
	for name, ids := range c.Notify.Recipients {
		if name == model.DefaultRecipient {
			return errors.New(i18n.T("notify.recipients: %q already means telegram_chat_id; use another name", name))
		}
		if !model.ValidRecipient(name) {
			return errors.New(i18n.T("notify.recipients: name %q is invalid (lowercase letters, digits, - or _; max 32)", name))
		}
		if len(splitIDs(ids)) == 0 {
			return errors.New(i18n.T("notify.recipients[%q]: give at least one chat id", name))
		}
	}
	for _, m := range c.Notify.RemindAfterMin {
		if m < 5 || m > 43200 {
			return errors.New(i18n.T("notify.remind_after_min must hold minutes 5..43200 (30 days); %d is outside that", m))
		}
	}
	for _, id := range c.Notify.TelegramAdminIDs {
		if id <= 0 {
			return errors.New(i18n.T("notify.telegram_admin_ids: %d is not a Telegram user id (a positive number; a negative group id is misplaced here)", id))
		}
	}
	if c.Notify.TelegramCommands && c.Notify.TelegramToken == "" {
		return errors.New(i18n.T("notify.telegram_commands needs notify.telegram_token and notify.telegram_chat_id"))
	}
	for _, d := range c.CertWarnDays {
		if d < 1 || d > 365 {
			return errors.New(i18n.T("cert_warn_days must hold numbers 1..365"))
		}
	}
	if t := c.StatusPage; len([]rune(t.Title)) > 100 || len([]rune(t.Description)) > 300 ||
		strings.ContainsFunc(t.Title+t.Description, unicode.IsControl) {
		return errors.New(i18n.T("status_page.title must be at most 100 and description at most 300 characters, with no control characters"))
	}
	if c.EventRetentionDays < 7 || c.EventRetentionDays > 3650 {
		return errors.New(i18n.T("event_retention_days must be 7..3650"))
	}
	if b := *c.Notify.BatchSec; b < 0 || b > 120 {
		return errors.New(i18n.T("notify.batch_sec must be 0..120"))
	}
	if c.Notify.DigestMin < 2 || c.Notify.DigestMin > 50 {
		return errors.New(i18n.T("notify.digest_min must be 2..50"))
	}
	if c.HeartbeatURL != "" {
		u, err := url.Parse(c.HeartbeatURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New(i18n.T("heartbeat_url must be an http:// or https:// address"))
		}
	}
	if c.HeartbeatIntervalSec < 10 || c.HeartbeatIntervalSec > 3600 {
		return errors.New(i18n.T("heartbeat_interval_sec must be 10..3600"))
	}
	if c.MaxConcurrent < 1 || c.MaxConcurrent > 500 {
		return errors.New(i18n.T("max_concurrent_checks must be 1..500"))
	}
	if (c.Notify.TelegramToken == "") != (len(c.ChatIDs()) == 0) {
		return errors.New(i18n.T("notify.telegram_token and notify.telegram_chat_id must be set together"))
	}
	return nil
}

// ReportWeeklyDay is the weekday of the weekly report; Monday unless configured.
func (c *Config) ReportWeeklyDay() int {
	if c.Notify.ReportWeeklyDay != nil {
		return *c.Notify.ReportWeeklyDay
	}
	return 1
}

// ChatIDs splits notify.telegram_chat_id into individual ids.
func (c *Config) ChatIDs() []string { return splitIDs(c.Notify.TelegramChatID) }

// Routes maps each recipient name to its chat ids.
func (c *Config) Routes() map[string][]string {
	out := make(map[string][]string, len(c.Notify.Recipients))
	for name, ids := range c.Notify.Recipients {
		out[name] = splitIDs(ids)
	}
	return out
}

// RecipientNames lists the configured recipient names, sorted.
func (c *Config) RecipientNames() []string {
	names := make([]string, 0, len(c.Notify.Recipients))
	for n := range c.Notify.Recipients {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func splitIDs(list string) []string {
	var out []string
	for _, id := range strings.Split(list, ",") {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}
