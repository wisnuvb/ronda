package notify

import (
	"strings"
	"testing"

	"github.com/wisnuvb/ronda/internal/i18n"
	"github.com/wisnuvb/ronda/internal/model"
)

func indonesian(t *testing.T) {
	t.Helper()
	if err := i18n.SetLang("id"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i18n.SetLang("en") })
}

// The same event reads in the configured language: the catalog is wired to every message the package
// builds, including the digests and the durations inside them.
func TestMessagesFollowTheConfiguredLanguage(t *testing.T) {
	indonesian(t)

	down := FormatHTML(model.Event{State: "down", SiteName: "Toko", URL: "https://t", Hint: "port ditutup", Fails: 3})
	for _, want := range []string{"<b>DOWN</b>", "<b>Penyebab:</b> port ditutup", "Gagal 3x berturut-turut"} {
		if !strings.Contains(down, want) {
			t.Errorf("missing %q in:\n%s", want, down)
		}
	}
	if strings.Contains(down, "Cause") || strings.Contains(down, "Failed") {
		t.Errorf("English text leaked into an Indonesian message:\n%s", down)
	}

	up := FormatText(model.Event{State: "up", SiteName: "Toko", URL: "https://t", DowntimeSec: 252, LatencyMs: 231})
	if !strings.HasPrefix(up, "[PULIH] Toko") || !strings.Contains(up, "4 mnt 12 dtk") || !strings.Contains(up, "respons 231 ms") {
		t.Errorf("plain text:\n%s", up)
	}

	digest := FormatDigestHTML("up", []model.Event{{State: "up", SiteName: "A", DowntimeSec: 7500}, {State: "up", SiteName: "B", DowntimeSec: 30}})
	for _, want := range []string{"2 situs PULIH", "A — down 2 jam 5 mnt", "B — down 30 dtk"} {
		if !strings.Contains(digest, want) {
			t.Errorf("missing %q in:\n%s", want, digest)
		}
	}

	if got := FormatHTML(model.Event{State: "cert", DaysLeft: 0}); !strings.Contains(got, "kurang dari 1 hari") {
		t.Errorf("0 days left: %s", got)
	}
}

// The same events in English must not depend on the Indonesian catalog being loaded.
func TestEnglishIsTheDefaultLanguage(t *testing.T) {
	if got := i18n.Lang(); got != "en" {
		t.Fatalf("tests run in English, got %q", got)
	}
	if got := Duration(3725); got != "1 h 2 min" {
		t.Errorf("Duration = %q", got)
	}
}
