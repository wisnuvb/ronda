package schedule

import (
	"strings"
	"testing"

	"github.com/wisnuvb/ronda/i18n"
	"github.com/wisnuvb/ronda/model"
)

func TestValidationMessagesFollowTheLanguage(t *testing.T) {
	t.Cleanup(func() { _ = i18n.SetLang("en") })
	bad := []model.Window{{Days: nil, Start: "08:00", Minutes: 30}}

	err := Validate(bad)
	if err == nil || !strings.Contains(err.Error(), "window 1: choose at least one day") {
		t.Fatalf("english: %v", err)
	}
	if err := i18n.SetLang("id"); err != nil {
		t.Fatal(err)
	}
	err = Validate(bad)
	if err == nil || !strings.Contains(err.Error(), "jendela 1: pilih minimal satu hari") {
		t.Fatalf("indonesian: %v", err)
	}
	// A wrapped error keeps its cause readable in the same language.
	_, err = ParseStart("8am")
	if err == nil || !strings.Contains(err.Error(), "HH:MM") || !strings.Contains(err.Error(), "jam mulai") {
		t.Fatalf("indonesian start time: %v", err)
	}
}
