package registry

import (
	"errors"
	"strings"
	"testing"

	"github.com/wisnuvb/ronda/internal/i18n"
	"github.com/wisnuvb/ronda/internal/model"
)

func TestMessagesFollowTheConfiguredLanguage(t *testing.T) {
	r, _ := open(t)
	_, err := r.Add(model.Site{URL: "ftp://example.com"})
	var inv *InvalidError
	if !errors.As(err, &inv) || !strings.Contains(err.Error(), "url must start with http:// or https://") {
		t.Fatalf("English by default: %v", err)
	}

	i18n.SetLang("id")
	t.Cleanup(func() { _ = i18n.SetLang("en") })
	_, err = r.Add(model.Site{URL: "ftp://example.com"})
	if err == nil || !strings.Contains(err.Error(), "url harus diawali http:// atau https://") {
		t.Fatalf("Indonesian: %v", err)
	}
	if _, err := r.Get("nope"); !errors.Is(err, ErrNotFound) || err.Error() != "situs tidak ditemukan" {
		t.Fatalf("ErrNotFound must translate itself and still match errors.Is: %v", err)
	}
	// A message with verbs keeps its arguments.
	_, err = r.Add(model.Site{URL: "https://a.example.com", IntervalSec: 5})
	if err == nil || !strings.Contains(err.Error(), "interval_sec harus 10..86400") {
		t.Fatalf("verbs: %v", err)
	}
}
