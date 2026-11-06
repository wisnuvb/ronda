package sitespec

import (
	"errors"
	"testing"

	"github.com/wisnuvb/ronda/model"
)

func TestNormalizeAppliesDefaultsPerType(t *testing.T) {
	web := model.Site{URL: " https://example.com/health "}
	if err := Normalize(&web); err != nil {
		t.Fatal(err)
	}
	if web.Type != TypeHTTP || web.Method != "GET" || web.IntervalSec != 60 || web.TimeoutSec != 10 || web.FailThreshold != 3 || web.Name != "example.com" {
		t.Fatalf("web defaults: %+v", web)
	}

	push := model.Site{Type: "push"}
	if err := Normalize(&push); err != nil {
		t.Fatal(err)
	}
	if push.IntervalSec != 86400 || push.GraceSec != 300 || push.FailThreshold != 1 || push.TimeoutSec != 0 {
		t.Fatalf("push defaults: %+v", push)
	}
}

func TestNormalizeRejectsWithAnInvalidError(t *testing.T) {
	for name, s := range map[string]model.Site{
		"credentials in the url": {URL: "https://user:pw@example.com"},
		"scheme":                 {URL: "ftp://example.com"},
		"type mismatch":          {Type: "tcp", URL: "https://example.com"},
		"interval too short":     {URL: "https://example.com", IntervalSec: 5},
		"push interval too long": {Type: "push", IntervalSec: MaxPushInterval + 1},
		"dns type on a web site": {URL: "https://example.com", DNSType: "MX"},
	} {
		err := Normalize(&s)
		var inv *model.InvalidError
		if !errors.As(err, &inv) {
			t.Errorf("%s: want *model.InvalidError, got %v", name, err)
		}
	}
}
