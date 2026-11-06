package api

import (
	"strings"
	"testing"
)

func TestRecipientsListNeedsLoginAndIsAnArrayEvenWhenEmpty(t *testing.T) {
	h := setup(t)
	if code, _, _ := h.anon(t).do("GET", "/api/recipients", ""); code != 401 {
		t.Fatalf("anonymous = %d", code)
	}
	code, out, _ := h.bearer(t).do("GET", "/api/recipients", "")
	if arr, ok := out["recipients"].([]any); code != 200 || !ok || len(arr) != 0 {
		t.Fatalf("none configured must be [], not null (the form iterates it): %d %v", code, out)
	}
	h2 := setup(t, func(o *Options) { o.Recipients = []string{"klien-a", "tim"} })
	_, out, _ = h2.bearer(t).do("GET", "/api/recipients", "")
	if arr := out["recipients"].([]any); len(arr) != 2 || arr[0] != "klien-a" {
		t.Fatalf("%v", out)
	}
}

func TestSitesCanBeRoutedOnlyToConfiguredRecipients(t *testing.T) {
	h := setup(t, func(o *Options) { o.Recipients = []string{"klien-a"} })
	h.reg.SetRecipients([]string{"klien-a"})
	c := h.bearer(t)

	code, site, _ := c.do("POST", "/api/sites", `{"url":"https://a.contoh.com","notify_to":["Klien-A"]}`)
	if code != 201 || site["notify_to"].([]any)[0] != "klien-a" {
		t.Fatalf("a known recipient (any case): %d %v", code, site)
	}
	code, out, _ := c.do("POST", "/api/sites", `{"url":"https://b.contoh.com","notify_to":["klien-z"]}`)
	if code != 400 || !strings.Contains(out["error"].(string), "klien-a") {
		t.Fatalf("an unknown recipient is a 400 that names the valid ones: %d %v", code, out)
	}
	id := site["id"].(string)
	code, out, _ = c.do("PATCH", "/api/sites/"+id, `{"notify_to":["default","klien-a"]}`)
	if code != 200 || len(out["notify_to"].([]any)) != 2 {
		t.Fatalf("patch: %d %v", code, out)
	}
	code, out, _ = c.do("PATCH", "/api/sites/"+id, `{"notify_to":[]}`)
	if code != 200 || out["notify_to"] != nil {
		t.Fatalf("an empty list clears the routing (omitted from the JSON): %d %v", code, out)
	}
	// The list view carries it too, so the dashboard can show it.
	c.do("PATCH", "/api/sites/"+id, `{"notify_to":["klien-a"]}`)
	_, list, _ := c.do("GET", "/api/sites", "")
	if got := list["sites"].([]any)[0].(map[string]any)["notify_to"]; got == nil {
		t.Fatalf("the list must include notify_to: %v", list)
	}
}
