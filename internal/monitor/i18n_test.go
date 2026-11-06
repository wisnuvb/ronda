package monitor

import (
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/i18n"
)

func TestFmtPeriodFollowsTheLanguage(t *testing.T) {
	t.Cleanup(func() { _ = i18n.SetLang("en") })
	cases := []struct {
		d       time.Duration
		en, ind string
	}{
		{90 * time.Second, "90 seconds", "90 detik"},
		{time.Second, "1 second", "1 detik"},
		{5 * time.Minute, "5 minutes", "5 menit"},
		{3 * time.Hour, "3 hours", "3 jam"},
		{72 * time.Hour, "3 days", "3 hari"},
	}
	for _, c := range cases {
		if got := fmtPeriod(c.d); got != c.en {
			t.Errorf("en %v: %q, want %q", c.d, got, c.en)
		}
	}
	if err := i18n.SetLang("id"); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := fmtPeriod(c.d); got != c.ind {
			t.Errorf("id %v: %q, want %q", c.d, got, c.ind)
		}
	}
}
