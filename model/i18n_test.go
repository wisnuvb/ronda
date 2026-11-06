package model

import (
	"testing"

	"github.com/wisnuvb/ronda/i18n"
)

func TestKindLabelFollowsTheLanguage(t *testing.T) {
	t.Cleanup(func() { _ = i18n.SetLang("en") })
	if got := KindLabel(KindRefused); got != "Connection refused" {
		t.Fatalf("english: %q", got)
	}
	if err := i18n.SetLang("id"); err != nil {
		t.Fatal(err)
	}
	if got := KindLabel(KindRefused); got != "Koneksi ditolak" {
		t.Fatalf("indonesian: %q", got)
	}
	if got := KindLabel("something_new"); got != "something_new" {
		t.Fatalf("an unknown kind is shown as is: %q", got)
	}
	if got := KindLabel(""); got != "–" {
		t.Fatalf("empty kind: %q", got)
	}
}
