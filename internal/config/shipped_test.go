package config

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The deploy/ files are packed into every release tarball and copied onto servers, so a real
// credential pasted into them while testing would be published with the next release.
var secretShapes = map[string]*regexp.Regexp{
	"Telegram bot token":  regexp.MustCompile(`\b\d{8,}:[A-Za-z0-9_-]{30,}\b`),
	"64-char hex secret":  regexp.MustCompile(`\b[0-9a-f]{48,}\b`),
	"PBKDF2 hash":         regexp.MustCompile(`pbkdf2[-_a-z0-9]*\$`),
	"ping or webhook URL": regexp.MustCompile(`/ping/[0-9a-f]{16,}|hooks\.slack\.com/services/|discord(app)?\.com/api/webhooks/`),
}

func TestShippedExampleConfigHasNoSecrets(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "config.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		API    struct{ Token string }        `json:"api"`
		Auth   struct{ PasswordHash string } `json:"auth"`
		Notify struct {
			TelegramToken  string `json:"telegram_token"`
			TelegramChatID string `json:"telegram_chat_id"`
			WebhookURL     string `json:"webhook_url"`
		} `json:"notify"`
		HeartbeatURL string `json:"heartbeat_url"`
	}
	c.Auth.PasswordHash = "x"
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{
		"api.token": c.API.Token, "notify.telegram_token": c.Notify.TelegramToken,
		"notify.telegram_chat_id": c.Notify.TelegramChatID, "notify.webhook_url": c.Notify.WebhookURL,
		"heartbeat_url": c.HeartbeatURL,
	} {
		if v != "" {
			t.Errorf("deploy/config.example.json: %s must stay empty in the shipped example", name)
		}
	}
	if !strings.Contains(string(raw), `"password_hash": ""`) {
		t.Error("deploy/config.example.json: auth.password_hash must stay empty in the shipped example")
	}
}

// shippedFiles lists every text file that is published with the project and could carry a secret by mistake: the
// deploy examples, the docs and the top-level documents. Images are skipped.
func shippedFiles(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	var files []string
	for _, dir := range []string{"deploy", "docs", ".github"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			switch filepath.Ext(path) {
			case ".png", ".webp", ".jpg", ".jpeg", ".gif", ".ico":
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"README.md", "README.id.md", "CHANGELOG.md", "CONTRIBUTING.md", "SECURITY.md", "Makefile", "Dockerfile"} {
		files = append(files, filepath.Join(root, f))
	}
	return files
}

func TestShippedFilesHaveNoSecretShapes(t *testing.T) {
	files := shippedFiles(t)
	if len(files) < 10 {
		t.Fatalf("found only %d files to scan: has the walk broken?", len(files))
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for what, re := range secretShapes {
			if m := re.Find(raw); m != nil {
				t.Errorf("%s looks like it contains a %s", filepath.Base(f), what)
			}
		}
	}
	// Positive control: the patterns must actually fire, or the test above proves nothing.
	for what, sample := range map[string]string{
		"Telegram bot token":  "123456789:AAH-abcdefghijklmnopqrstuvwxyz0123456",
		"64-char hex secret":  strings.Repeat("ab", 24),
		"PBKDF2 hash":         "pbkdf2-sha256$310000$abc",
		"ping or webhook URL": "https://x/ping/0123456789abcdef0123",
	} {
		if !secretShapes[what].MatchString(sample) {
			t.Errorf("pattern %q failed its positive control", what)
		}
	}
}
