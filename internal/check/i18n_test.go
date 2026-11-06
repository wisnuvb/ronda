package check

import (
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/i18n"
)

// The hints are for people, so they follow the configured language; the short message (third result) is
// for logs and stays English whatever the language.
func TestHintsFollowTheLanguageButLogMessagesDoNot(t *testing.T) {
	_, en, short := classifyError(syscall.ECONNREFUSED, phases{}, time.Second)
	if !strings.Contains(en, "Connection refused") || short != "connection refused" {
		t.Fatalf("english: %q / %q", en, short)
	}

	if err := i18n.SetLang("id"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.SetLang("en") })
	_, id, short := classifyError(syscall.ECONNREFUSED, phases{}, time.Second)
	if !strings.Contains(id, "Koneksi ditolak") || strings.Contains(id, "Connection refused") {
		t.Fatalf("indonesian hint: %q", id)
	}
	if short != "connection refused" {
		t.Fatalf("the log message must not be translated: %q", short)
	}
	if _, hint := classifyStatus(404); !strings.Contains(hint, "tidak ditemukan") {
		t.Fatalf("status hint: %q", hint)
	}
	if _, hint := classifyStatus(599); !strings.Contains(hint, "599") || strings.Contains(hint, "The server") {
		t.Fatalf("a formatted hint keeps its argument in the translation: %q", hint)
	}
}
