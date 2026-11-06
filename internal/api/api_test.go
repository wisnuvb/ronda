package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/wisnuvb/ronda/history"
	"github.com/wisnuvb/ronda/internal/auth"
	"github.com/wisnuvb/ronda/internal/registry"
	"github.com/wisnuvb/ronda/model"
	"github.com/wisnuvb/ronda/monitor"
)

const (
	token    = "token-uji-0123456789"
	password = "kata-sandi-uji-123"
)

var (
	hashOnce sync.Once
	pwHash   string
)

func hash(t *testing.T) string {
	hashOnce.Do(func() {
		var err error
		if pwHash, err = auth.HashPasswordCost(password, 1000); err != nil { // cheap on purpose: tests log in a lot
			t.Fatal(err)
		}
	})
	return pwHash
}

type okChecker struct{}

func (okChecker) Check(_ context.Context, s model.Site) model.Result {
	return model.Result{Time: time.Now().UnixMilli(), OK: true, Status: 200, LatencyMs: 7, Kind: model.KindOK}
}

type harness struct {
	srv *httptest.Server
	reg *registry.Registry
	mon *monitor.Monitor
	dir string // the data directory handed to the API
}

func setup(t *testing.T, mutate ...func(*Options)) *harness {
	t.Helper()
	reg, err := registry.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hist := history.Open("")
	mon := monitor.New(monitor.Options{Sites: reg, Checker: okChecker{}, History: hist})
	ctx, cancel := context.WithCancel(context.Background())
	go mon.Run(ctx)

	dataDir := t.TempDir()
	opt := Options{
		Registry: reg, Monitor: mon, History: hist, Token: token, User: "admin", PasswordHash: hash(t), DataDir: dataDir,
		Static: fstest.MapFS{
			"index.html":  {Data: []byte("<!doctype html><title>ronda</title>")},
			"status.html": {Data: []byte("<!doctype html><title>status</title>")},
		},
	}
	for _, m := range mutate {
		m(&opt)
	}
	srv := httptest.NewServer(New(opt).Handler())
	t.Cleanup(func() { srv.Close(); cancel() })
	return &harness{srv, reg, mon, dataDir}
}

type client struct {
	t      *testing.T
	base   string
	cookie *http.Cookie
	csrf   string
	bearer bool
}

func (h *harness) bearer(t *testing.T) *client { return &client{t: t, base: h.srv.URL, bearer: true} }
func (h *harness) anon(t *testing.T) *client   { return &client{t: t, base: h.srv.URL} }

func (c *client) do(method, path, body string, hdr ...string) (int, map[string]any, http.Header) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, strings.NewReader(body))
	if c.bearer {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, resp.Header
}

func (c *client) login(pw string) (int, map[string]any, http.Header) {
	code, out, hdr := c.do("POST", "/api/login", `{"user":"admin","password":"`+pw+`"}`)
	if code == 200 {
		for _, ck := range (&http.Response{Header: hdr}).Cookies() {
			if ck.Name == cookieName {
				c.cookie = ck
			}
		}
		c.csrf, _ = out["csrf"].(string)
	}
	return code, out, hdr
}

// ---- authentication ----

func TestEverythingButHealthzAndUIRequiresAuth(t *testing.T) {
	h := setup(t)
	c := h.anon(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/sites"}, {"POST", "/api/sites"}, {"GET", "/api/events"}, {"POST", "/api/check"},
		{"DELETE", "/api/sites/x"}, {"PATCH", "/api/sites/x"}, {"GET", "/api/incidents"},
		{"GET", "/api/sites/x/history"}, {"POST", "/api/sites/x/check"}, {"POST", "/api/logout"},
	} {
		code, out, _ := c.do(tc.method, tc.path, `{}`)
		if code != 401 || out["login_enabled"] != true {
			t.Errorf("%s %s = %d %v, want 401", tc.method, tc.path, code, out)
		}
	}
	if code, _, _ := c.do("GET", "/healthz", ""); code != 200 {
		t.Errorf("healthz = %d", code)
	}
	if code, _, _ := c.do("GET", "/api/sites", "", "Authorization", "Bearer salah"); code != 401 {
		t.Errorf("wrong bearer = %d", code)
	}
}

func TestLoginSessionLogoutFlow(t *testing.T) {
	h := setup(t)
	c := h.anon(t)

	if code, _, _ := c.do("GET", "/api/session", ""); code != 401 {
		t.Fatalf("session before login = %d", code)
	}
	code, out, hdr := c.login(password)
	if code != 200 || out["user"] != "admin" || c.csrf == "" || c.cookie == nil {
		t.Fatalf("login: %d %v", code, out)
	}
	if out["can_change_password"] != true {
		t.Fatalf("login must say whether the password can be changed, or the button only appears after a reload: %v", out)
	}
	if !c.cookie.HttpOnly || c.cookie.SameSite != http.SameSiteStrictMode || c.cookie.Secure {
		t.Fatalf("cookie flags: %+v (plain http, so not Secure)", c.cookie)
	}
	if hdr.Get("Content-Security-Policy") == "" {
		t.Fatal("no CSP on login response")
	}

	if code, out, _ := c.do("GET", "/api/session", ""); code != 200 || out["csrf"] != c.csrf {
		t.Fatalf("session: %d %v", code, out)
	}
	if code, _, _ := c.do("GET", "/api/sites", ""); code != 200 {
		t.Fatalf("list with cookie = %d", code)
	}

	if code, _, _ := c.do("POST", "/api/logout", ""); code != 204 {
		t.Fatalf("logout = %d", code)
	}
	if code, _, _ := c.do("GET", "/api/sites", ""); code != 401 {
		t.Fatalf("the cookie must be dead after logout, got %d", code)
	}
}

func TestWrongCredentialsAndLockout(t *testing.T) {
	h := setup(t)
	c := h.anon(t)

	code, out, _ := c.do("POST", "/api/login", `{"user":"orang-lain","password":"`+password+`"}`)
	if code != 401 || out["error"] != "Wrong username or password" {
		t.Fatalf("wrong user: %d %v", code, out)
	}
	code, out2, _ := c.login("salah-salah-salah")
	if code != 401 || out2["error"] != out["error"] {
		t.Fatalf("wrong password must look identical to wrong user: %d %v", code, out2)
	}
	for i := 0; i < 3; i++ {
		c.login("salah-salah-salah")
	}
	code, out, hdr := c.login(password) // correct password, but already locked
	if code != 429 || hdr.Get("Retry-After") == "" || !strings.Contains(out["error"].(string), "Too many failed attempts") {
		t.Fatalf("lockout: %d %v", code, out)
	}
}

func TestSuccessfulLoginClearsFailures(t *testing.T) {
	h := setup(t)
	c := h.anon(t)
	for i := 0; i < 4; i++ {
		c.login("salah-salah-salah")
	}
	if code, _, _ := c.login(password); code != 200 {
		t.Fatalf("login = %d", code)
	}
	for i := 0; i < 4; i++ {
		c.login("salah-salah-salah")
	}
	if code, _, _ := h.anon(t).login(password); code == 429 {
		t.Fatal("a success must reset the failure count")
	}
}

func TestCSRFRequiredForCookieSessionsOnly(t *testing.T) {
	h := setup(t)
	c := h.anon(t)
	c.login(password)
	good := c.csrf

	c.csrf = ""
	if code, _, _ := c.do("POST", "/api/sites", `{"url":"https://a.com"}`); code != 403 {
		t.Fatalf("cookie POST without CSRF = %d, want 403", code)
	}
	c.csrf = "salah"
	if code, _, _ := c.do("POST", "/api/sites", `{"url":"https://a.com"}`); code != 403 {
		t.Fatalf("cookie POST with wrong CSRF = %d, want 403", code)
	}
	if code, _, _ := c.do("DELETE", "/api/sites/x", ""); code != 403 {
		t.Fatalf("cookie DELETE with wrong CSRF = %d, want 403", code)
	}
	c.csrf = ""
	if code, _, _ := c.do("GET", "/api/sites", ""); code != 200 {
		t.Fatalf("reads need no CSRF token, got %d", code)
	}
	c.csrf = good
	if code, _, _ := c.do("POST", "/api/sites", `{"url":"https://a.com"}`); code != 201 {
		t.Fatalf("with the right token = %d", code)
	}
	if len(h.reg.List()) != 1 {
		t.Fatalf("only the request with the right CSRF token may change data, got %d sites", len(h.reg.List()))
	}

	// A bearer token is not ambient browser credentials, so it needs no CSRF token.
	if code, _, _ := h.bearer(t).do("POST", "/api/sites", `{"url":"https://b.com"}`); code != 201 {
		t.Fatalf("bearer POST = %d", code)
	}
}

func TestLoginRejectsCrossOrigin(t *testing.T) {
	h := setup(t)
	c := h.anon(t)
	code, _, _ := c.do("POST", "/api/login", `{"user":"admin","password":"`+password+`"}`, "Origin", "https://jahat.example")
	if code != 403 {
		t.Fatalf("cross-origin login = %d, want 403", code)
	}
	host := strings.TrimPrefix(h.srv.URL, "http://")
	code, _, _ = c.do("POST", "/api/login", `{"user":"admin","password":"`+password+`"}`, "Origin", "http://"+host)
	if code != 200 {
		t.Fatalf("same-origin login = %d", code)
	}
}

func TestDashboardDisabledWithoutPasswordHash(t *testing.T) {
	h := setup(t, func(o *Options) { o.PasswordHash = "" })
	c := h.anon(t)
	code, out, _ := c.login(password)
	if code != 503 || !strings.Contains(out["error"].(string), "password_hash") {
		t.Fatalf("%d %v", code, out)
	}
	if code, out, _ := c.do("GET", "/api/session", ""); code != 401 || out["login_enabled"] != false {
		t.Fatalf("session: %d %v", code, out)
	}
	if code, _, _ := h.bearer(t).do("GET", "/api/sites", ""); code != 200 {
		t.Fatalf("the API token must keep working, got %d", code)
	}
}

func TestSecureCookieBehindTrustedProxy(t *testing.T) {
	h := setup(t, func(o *Options) { o.TrustProxy = true })
	_, _, hdr := h.anon(t).do("POST", "/api/login", `{"user":"admin","password":"`+password+`"}`, "X-Forwarded-Proto", "https")
	var got *http.Cookie
	for _, ck := range (&http.Response{Header: hdr}).Cookies() {
		got = ck
	}
	if got == nil || got.Name != hostCookieName || !got.Secure || !got.HttpOnly || got.Path != "/" || got.Domain != "" {
		t.Fatalf("over https the cookie must carry the __Host- prefix and everything that prefix demands (Secure, Path=/, no Domain): %+v", got)
	}
}

func TestHostPrefixedCookieDefeatsCookieTossing(t *testing.T) {
	h := setup(t, func(o *Options) { o.TrustProxy = true })
	login := func() string {
		_, _, hdr := h.anon(t).do("POST", "/api/login", `{"user":"admin","password":"`+password+`"}`, "X-Forwarded-Proto", "https")
		for _, ck := range (&http.Response{Header: hdr}).Cookies() {
			return ck.Value
		}
		t.Fatal("no cookie")
		return ""
	}
	token := login()
	https := func(cookie string) int {
		code, _, _ := h.anon(t).do("GET", "/api/sites", "", "X-Forwarded-Proto", "https", "Cookie", cookie)
		return code
	}
	if https(hostCookieName+"="+token) != 200 {
		t.Fatal("the prefixed cookie must authenticate over https")
	}
	// A page on a sibling sub-domain can plant a plain-named cookie for the whole domain. Over https it must be ignored
	// (and the real, prefixed one must still win when both are sent).
	if code := https(cookieName + "=" + token); code != 401 {
		t.Fatalf("a plain-named cookie must not authenticate over https, got %d", code)
	}
	if code := https(cookieName + "=planted; " + hostCookieName + "=" + token); code != 200 {
		t.Fatalf("a planted cookie must not shadow the real one, got %d", code)
	}
	// Over plain http the prefixed name is not what the browser would have been given.
	if code, _, _ := h.anon(t).do("GET", "/api/sites", "", "Cookie", hostCookieName+"="+token); code != 401 {
		t.Fatalf("over http only the plain name counts, got %d", code)
	}
}

func TestHardeningHeaders(t *testing.T) {
	h := setup(t)
	_, _, hdr := h.anon(t).do("GET", "/healthz", "")
	for k, want := range map[string]string{"Cross-Origin-Opener-Policy": "same-origin", "X-Frame-Options": "DENY"} {
		if hdr.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, hdr.Get(k), want)
		}
	}
	if pp := hdr.Get("Permissions-Policy"); !strings.Contains(pp, "camera=()") || !strings.Contains(pp, "geolocation=()") {
		t.Errorf("Permissions-Policy = %q", pp)
	}
}

func TestLockoutIsPerClientAddressBehindProxy(t *testing.T) {
	h := setup(t, func(o *Options) { o.TrustProxy = true })
	c := h.anon(t)
	bad := `{"user":"admin","password":"salah-salah-salah"}`
	for i := 0; i < 5; i++ {
		c.do("POST", "/api/login", bad, "X-Forwarded-For", "6.6.6.6")
	}
	// A spoofed leftmost entry must not dodge the limit: only the proxy's own entry counts.
	if code, _, _ := c.do("POST", "/api/login", bad, "X-Forwarded-For", "1.2.3.4, 6.6.6.6"); code != 429 {
		t.Fatalf("spoofed X-Forwarded-For dodged the limiter: %d", code)
	}
	if code, _, _ := c.do("POST", "/api/login", `{"user":"admin","password":"`+password+`"}`, "X-Forwarded-For", "7.7.7.7"); code != 200 {
		t.Fatalf("another client must not be locked out: %d", code)
	}
}

func TestNoAuthMode(t *testing.T) {
	h := setup(t, func(o *Options) { o.NoAuth = true })
	c := h.anon(t)
	if code, out, _ := c.do("GET", "/api/session", ""); code != 200 || out["demo"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if code, _, _ := c.do("POST", "/api/sites", `{"url":"https://a.com"}`); code != 201 {
		t.Fatalf("no-auth POST = %d", code)
	}
}

// ---- static files and headers ----

func TestStaticUIAndSecurityHeaders(t *testing.T) {
	h := setup(t)
	resp, err := http.Get(h.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "<title>ronda</title>") {
		t.Fatalf("index: %d %s", resp.StatusCode, body)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP must not allow inline or eval: %s", csp)
	}
	for k, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer", "Cache-Control": "no-store"} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if r2, _ := http.Get(h.srv.URL + "/tidak-ada.js"); r2.StatusCode != 404 {
		t.Errorf("missing file = %d", r2.StatusCode)
	}
}

// ---- data endpoints (bearer, so these focus on behaviour) ----

func TestCRUDFlow(t *testing.T) {
	h := setup(t)
	c := h.bearer(t)

	code, created, _ := c.do("POST", "/api/sites", `{"url":"https://contoh.com","name":"Contoh","interval_sec":30}`)
	if code != 201 || created["id"] == "" || created["fail_threshold"].(float64) != 3 {
		t.Fatalf("create: %d %v", code, created)
	}
	id := created["id"].(string)

	var state string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		_, got, _ := c.do("GET", "/api/sites/"+id, "")
		if state, _ = got["state"].(string); state == "up" {
			if got["uptime_24h"] == nil || got["p95_ms"] == nil {
				t.Fatalf("detail must carry history stats: %v", got)
			}
			break
		}
	}
	if state != "up" {
		t.Fatalf("site never became up through the live monitor, state=%q", state)
	}

	code, list, _ := c.do("GET", "/api/sites", "")
	sites := list["sites"].([]any)
	if code != 200 || len(sites) != 1 || sites[0].(map[string]any)["strip"] == nil {
		t.Fatalf("list: %d %v", code, list)
	}

	if code, p, _ := c.do("PATCH", "/api/sites/"+id, `{"name":"Baru"}`); code != 200 || p["name"] != "Baru" || p["url"] != "https://contoh.com" {
		t.Fatalf("patch: %d %v", code, p)
	}
	if code, p, _ := c.do("POST", "/api/sites/"+id+"/pause", ""); code != 200 || p["paused"] != true {
		t.Fatalf("pause: %d %v", code, p)
	}
	if code, p, _ := c.do("POST", "/api/sites/"+id+"/resume", ""); code != 200 || p["paused"] != nil {
		t.Fatalf("resume: %d %v", code, p)
	}
	if code, _, _ := c.do("DELETE", "/api/sites/"+id, ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _, _ := c.do("GET", "/api/sites/"+id, ""); code != 404 {
		t.Fatalf("get after delete: %d", code)
	}
}

func TestHistoryIncidentsAndCheckNow(t *testing.T) {
	h := setup(t)
	c := h.bearer(t)
	_, created, _ := c.do("POST", "/api/sites", `{"url":"https://contoh.com"}`)
	id := created["id"].(string)

	var raw []any
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && len(raw) == 0; time.Sleep(10 * time.Millisecond) {
		_, out, _ := c.do("GET", "/api/sites/"+id+"/history?range=1h", "")
		raw, _ = out["raw"].([]any)
	}
	if len(raw) == 0 {
		t.Fatal("history never received the first check")
	}
	if code, out, _ := c.do("GET", "/api/sites/"+id+"/history?range=7d", ""); code != 200 || out["hourly"] == nil {
		t.Fatalf("7d: %d %v", code, out)
	}
	if code, _, _ := c.do("GET", "/api/sites/"+id+"/history?range=5y", ""); code != 400 {
		t.Fatalf("bad range = %d", code)
	}
	if code, _, _ := c.do("GET", "/api/sites/tidak-ada/history", ""); code != 404 {
		t.Fatalf("history of a missing site = %d", code)
	}

	if code, out, _ := c.do("GET", "/api/incidents", ""); code != 200 || out["incidents"] == nil {
		t.Fatalf("incidents: %d %v", code, out)
	}
	if code, out, _ := c.do("POST", "/api/sites/"+id+"/check", ""); code != 200 || out["ok"] != true {
		t.Fatalf("check now: %d %v", code, out)
	}
	if code, _, _ := c.do("POST", "/api/sites/tidak-ada/check", ""); code != 404 {
		t.Fatalf("check now on a missing site = %d", code)
	}
}

func TestValidationErrorsAre400(t *testing.T) {
	h := setup(t)
	c := h.bearer(t)
	for name, body := range map[string]string{
		"url salah":     `{"url":"ftp://x"}`,
		"field asing":   `{"url":"https://a.com","bukan":1}`,
		"bukan json":    `halo`,
		"data berlebih": `{"url":"https://a.com"}{"url":"https://b.com"}`,
		"tipe salah":    `{"url":"https://a.com","interval_sec":"cepat"}`,
	} {
		if code, out, _ := c.do("POST", "/api/sites", body); code != 400 || out["error"] == nil {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	if code, _, _ := c.do("PATCH", "/api/sites/tidak-ada", `{}`); code != 404 {
		t.Errorf("patch missing = %d", code)
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	h := setup(t)
	big := `{"url":"https://a.com","name":"` + strings.Repeat("a", 70<<10) + `"}`
	if code, _, _ := h.bearer(t).do("POST", "/api/sites", big); code != 400 {
		t.Fatalf("oversized body = %d", code)
	}
}

func TestCheckDraftDoesNotSave(t *testing.T) {
	h := setup(t)
	c := h.bearer(t)
	code, res, _ := c.do("POST", "/api/check", `{"url":"https://contoh.com"}`)
	if code != 200 || res["ok"] != true || res["kind"] != "ok" {
		t.Fatalf("check: %d %v", code, res)
	}
	if len(h.reg.List()) != 0 {
		t.Fatal("a draft check must not store the site")
	}
	if code, _, _ := c.do("POST", "/api/check", `{"url":"nope"}`); code != 400 {
		t.Fatalf("invalid draft = %d", code)
	}
}

func TestMaintenanceEndpoints(t *testing.T) {
	h := setup(t)
	c := h.bearer(t)
	_, created, _ := c.do("POST", "/api/sites", `{"url":"https://contoh.com"}`)
	id := created["id"].(string)

	code, site, _ := c.do("POST", "/api/sites/"+id+"/maintenance", `{"minutes":90}`)
	until, _ := site["maintenance_until"].(float64)
	want := float64(time.Now().Add(90 * time.Minute).UnixMilli())
	if code != 200 || until < want-5000 || until > want+5000 {
		t.Fatalf("start: %d %v (want about %v)", code, site, want)
	}

	deadline := time.Now().Add(5 * time.Second)
	state := ""
	for time.Now().Before(deadline) && state != "maintenance" {
		_, got, _ := c.do("GET", "/api/sites/"+id, "")
		state, _ = got["state"].(string)
		time.Sleep(10 * time.Millisecond)
	}
	if state != "maintenance" {
		t.Fatalf("state = %q, want maintenance", state)
	}

	for name, body := range map[string]string{"zero": `{"minutes":0}`, "negative": `{"minutes":-5}`, "too long": `{"minutes":999999}`, "unknown field": `{"hours":1}`, "not json": `x`} {
		if code, _, _ := c.do("POST", "/api/sites/"+id+"/maintenance", body); code != 400 {
			t.Errorf("%s: got %d, want 400", name, code)
		}
	}
	if code, _, _ := c.do("POST", "/api/sites/tidak-ada/maintenance", `{"minutes":5}`); code != 404 {
		t.Errorf("unknown site = %d", code)
	}

	code, site, _ = c.do("DELETE", "/api/sites/"+id+"/maintenance", "")
	if code != 200 || site["maintenance_until"] != nil {
		t.Fatalf("end: %d %v", code, site)
	}
	if code, _, _ := h.anon(t).do("POST", "/api/sites/"+id+"/maintenance", `{"minutes":5}`); code != 401 {
		t.Fatalf("maintenance needs auth, got %d", code)
	}
}

func TestIncidentsPaginationEndpoint(t *testing.T) {
	h := setup(t)
	c := h.bearer(t)
	_, created, _ := c.do("POST", "/api/sites", `{"url":"https://contoh.com"}`)
	id := created["id"].(string)
	base := time.Now().Add(-72 * time.Hour).UnixMilli()
	for i := 0; i < 5; i++ {
		at := base + int64(i)*1_000_000
		h.mon.AppendEventForTest(model.Event{State: "down", SiteID: id, SiteName: "A", Time: at, Since: at})
		h.mon.AppendEventForTest(model.Event{State: "up", SiteID: id, SiteName: "A", Time: at + 1000})
	}

	code, p1, _ := c.do("GET", "/api/incidents?site="+id+"&limit=2", "")
	list := p1["incidents"].([]any)
	if code != 200 || len(list) != 2 || p1["more"] != true {
		t.Fatalf("page 1: %d %v", code, p1)
	}
	last := int64(list[1].(map[string]any)["started"].(float64))
	_, p2, _ := c.do("GET", "/api/incidents?site="+id+"&limit=2&before="+strconv.FormatInt(last, 10), "")
	if l2 := p2["incidents"].([]any); len(l2) != 2 || int64(l2[0].(map[string]any)["started"].(float64)) >= last {
		t.Fatalf("page 2 must continue strictly before page 1: %v", p2)
	}
	_, p3, _ := c.do("GET", "/api/incidents?site="+id+"&limit=2&before=0", "")
	if len(p3["incidents"].([]any)) != 2 {
		t.Fatalf("before=0 starts from the newest: %v", p3)
	}
	for _, q := range []string{"limit=0", "limit=999", "limit=x", "before=-1", "before=x"} {
		if code, _, _ := c.do("GET", "/api/incidents?"+q, ""); code != 400 {
			t.Errorf("%s: got %d, want 400", q, code)
		}
	}
}

// ---- changing the password from the dashboard ----

const newPassword = "sandi-baru-yang-panjang-7"

func (c *client) changePassword(current, next string) (int, map[string]any) {
	code, out, _ := c.do("POST", "/api/password", fmt.Sprintf(`{"current":%q,"new":%q}`, current, next))
	return code, out
}

func TestChangePasswordFlow(t *testing.T) {
	h := setup(t)
	mine, laptop := h.anon(t), h.anon(t)
	mine.login(password)
	laptop.login(password) // another device, signed in with the old password

	code, out := mine.changePassword(password, newPassword)
	if code != 200 || out["ok"] != true || out["sessions_ended"].(float64) != 1 {
		t.Fatalf("change: %d %v", code, out)
	}
	if code, _, _ := mine.do("GET", "/api/sites", ""); code != 200 {
		t.Fatalf("the session that changed the password must stay signed in, got %d", code)
	}
	if code, _, _ := laptop.do("GET", "/api/sites", ""); code != 401 {
		t.Fatalf("every other session must be signed out, got %d", code)
	}
	if code, _, _ := h.anon(t).login(password); code != 401 {
		t.Fatalf("the old password must stop working, got %d", code)
	}
	if code, _, _ := h.anon(t).login(newPassword); code != 200 {
		t.Fatalf("the new password must work, got %d", code)
	}
	if fi, err := os.Stat(filepath.Join(h.dir, "auth.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("saved privately: %v %v", fi, err)
	}
}

func TestChangedPasswordSurvivesARestartUntilTheConfigHashChanges(t *testing.T) {
	h := setup(t)
	c := h.anon(t)
	c.login(password)
	if code, _ := c.changePassword(password, newPassword); code != 200 {
		t.Fatal(code)
	}

	restart := New(Options{Registry: h.reg, Monitor: h.mon, Token: token, User: "admin", PasswordHash: hash(t), DataDir: h.dir})
	got, _ := restart.hashes()
	if !auth.VerifyPassword(got, newPassword) || auth.VerifyPassword(got, password) {
		t.Fatal("after a restart the password chosen in the dashboard must still be the one in force")
	}

	// The administrator resets the password in the config: that must win over the saved one.
	edited, _ := auth.HashPasswordCost("diatur-admin-di-konsol-3", 1000)
	reset := New(Options{Registry: h.reg, Monitor: h.mon, Token: token, User: "admin", PasswordHash: edited, DataDir: h.dir})
	got, _ = reset.hashes()
	if !auth.VerifyPassword(got, "diatur-admin-di-konsol-3") || auth.VerifyPassword(got, newPassword) {
		t.Fatal("a new hash in the config must override what was saved from the dashboard")
	}
}

func TestChangePasswordRefusals(t *testing.T) {
	h := setup(t)
	c := h.anon(t)
	c.login(password)

	for name, tc := range map[string]struct {
		cur, next string
		want      int
	}{
		"wrong current":  {"bukan-sandi-saya-1", newPassword, 403},
		"too short":      {password, "pendek", 400},
		"same as before": {password, password, 400},
	} {
		if code, out := c.changePassword(tc.cur, tc.next); code != tc.want {
			t.Errorf("%s: %d %v, want %d", name, code, out, tc.want)
		}
	}
	if code, _, _ := c.do("GET", "/api/sites", ""); code != 200 {
		t.Fatalf("a wrong current password must not log the person out (that is what a 401 does), got %d", code)
	}
	if code, _, _ := h.anon(t).login(password); code != 200 {
		t.Fatalf("a refused change must leave the password alone, got %d", code)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "auth.json")); err == nil {
		t.Fatal("nothing may be saved after refusals")
	}
}

func TestChangePasswordNeedsALoginSessionWithCSRF(t *testing.T) {
	h := setup(t)
	body := fmt.Sprintf(`{"current":%q,"new":%q}`, password, newPassword)

	if code, _, _ := h.anon(t).do("POST", "/api/password", body); code != 401 {
		t.Errorf("no session: %d", code)
	}
	if code, out, _ := h.bearer(t).do("POST", "/api/password", body); code != 403 || !strings.Contains(out["error"].(string), "API token") {
		t.Errorf("the script token must not be able to change a human's password: %d %v", code, out)
	}
	c := h.anon(t)
	c.login(password)
	good := c.csrf
	c.csrf = "salah"
	if code, _ := c.changePassword(password, newPassword); code != 403 {
		t.Errorf("wrong CSRF token: %d", code)
	}
	c.csrf = good
	if _, err := os.Stat(filepath.Join(h.dir, "auth.json")); err == nil {
		t.Fatal("nothing may be saved by a refused request")
	}
}

func TestWrongCurrentPasswordsCountTowardTheLockout(t *testing.T) {
	h := setup(t)
	c := h.anon(t)
	c.login(password) // a success resets the counter; the failures below start from zero
	for i := 0; i < 5; i++ {
		c.changePassword("tebakan-salah-0"+fmt.Sprint(i)+"xx", newPassword)
	}
	code, _ := c.changePassword(password, newPassword)
	if code != 429 {
		t.Fatalf("someone with a stolen session must not be able to guess the current password forever, got %d", code)
	}
}

func TestChangePasswordWithoutADataDirectory(t *testing.T) {
	h := setup(t, func(o *Options) { o.DataDir = "" })
	c := h.anon(t)
	c.login(password)
	if code, _, _ := c.do("GET", "/api/session", ""); code != 200 {
		t.Fatal("session")
	}
	if code, out := c.changePassword(password, newPassword); code != 503 {
		t.Fatalf("%d %v", code, out)
	}
}

func TestSessionAdvertisesPasswordChange(t *testing.T) {
	h := setup(t)
	c := h.anon(t)
	c.login(password)
	if _, out, _ := c.do("GET", "/api/session", ""); out["can_change_password"] != true {
		t.Fatalf("%v", out)
	}
	h2 := setup(t, func(o *Options) { o.DataDir = "" })
	c2 := h2.anon(t)
	c2.login(password)
	if _, out, _ := c2.do("GET", "/api/session", ""); out["can_change_password"] != false {
		t.Fatalf("without somewhere to save it the feature must not be offered: %v", out)
	}
}

// ---- the public status page ----

func enableStatus(o *Options) {
	o.StatusPage = StatusPageOptions{Enabled: true, Title: "Status Contoh", Description: "Layanan kami"}
}

func TestStatusPageIsOffUnlessEnabled(t *testing.T) {
	h := setup(t)
	for _, path := range []string{"/status", "/api/public/status"} {
		resp, err := http.Get(h.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("%s while disabled = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestStatusPageIsPublicAndNothingElseIs(t *testing.T) {
	h := setup(t, enableStatus)
	c := h.bearer(t)
	_, pub, _ := c.do("POST", "/api/sites", `{"url":"https://rahasia.example/panel","name":"Toko","public":true}`)
	c.do("POST", "/api/sites", `{"url":"https://dalam.example","name":"Internal"}`)
	if pub["public"] != true {
		t.Fatalf("the public flag must be stored: %v", pub)
	}

	anon := h.anon(t)
	code, doc, hdr := anon.do("GET", "/api/public/status", "")
	if code != 200 || doc["title"] != "Status Contoh" || doc["description"] != "Layanan kami" {
		t.Fatalf("anonymous visitors can read the status: %d %v", code, doc)
	}
	if hdr.Get("X-Robots-Tag") != "noindex" || !strings.Contains(hdr.Get("Cache-Control"), "public") || hdr.Get("Set-Cookie") != "" {
		t.Fatalf("headers: %v", hdr)
	}
	sites := doc["sites"].([]any)
	if len(sites) != 1 || sites[0].(map[string]any)["name"] != "Toko" {
		t.Fatalf("only the site marked public: %v", sites)
	}
	raw, _ := json.Marshal(doc)
	for _, secret := range []string{"rahasia.example", "dalam.example", "Internal", "https://", "\"id\"", "url"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("the public document leaked %q: %s", secret, raw)
		}
	}

	resp, err := http.Get(h.srv.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "<title>status</title>") || resp.Header.Get("X-Robots-Tag") != "noindex" {
		t.Fatalf("/status: %d %s %v", resp.StatusCode, body, resp.Header)
	}

	// Turning the page on opens nothing else.
	for _, path := range []string{"/api/sites", "/api/events", "/api/incidents"} {
		if code, _, _ := anon.do("GET", path, ""); code != 401 {
			t.Errorf("%s must still need a login, got %d", path, code)
		}
	}
}

func TestStatusPageIsCached(t *testing.T) {
	h := setup(t, enableStatus)
	c := h.bearer(t)
	c.do("POST", "/api/sites", `{"url":"https://a.example","name":"A","public":true}`)

	_, first, _ := h.anon(t).do("GET", "/api/public/status", "")
	c.do("POST", "/api/sites", `{"url":"https://b.example","name":"B","public":true}`)
	_, second, _ := h.anon(t).do("GET", "/api/public/status", "")
	if first["updated"] != second["updated"] || len(second["sites"].([]any)) != 1 {
		t.Fatalf("within the cache window every visitor gets the same document, so a crowd costs one computation: %v vs %v", first["updated"], second["updated"])
	}
}

func TestSessionAndLoginAdvertiseTheStatusPage(t *testing.T) {
	h := setup(t, enableStatus)
	c := h.anon(t)
	_, out, _ := c.login(password)
	if out["status_page"] != true {
		t.Fatalf("login: %v", out)
	}
	if _, out, _ := c.do("GET", "/api/session", ""); out["status_page"] != true {
		t.Fatalf("session: %v", out)
	}
	h2 := setup(t)
	c2 := h2.anon(t)
	if _, out, _ := c2.login(password); out["status_page"] != false {
		t.Fatalf("a disabled page must not be advertised: %v", out)
	}
}

// ---- push sites: /ping/<token> ----

func plain(t *testing.T, method, url string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func newPush(t *testing.T, h *harness, extra string) (id, token string) {
	t.Helper()
	code, site, _ := h.bearer(t).do("POST", "/api/sites", `{"type":"push","name":"Backup"`+extra+`}`)
	if code != 201 || site["push_token"] == nil {
		t.Fatalf("create push site: %d %v", code, site)
	}
	return site["id"].(string), site["push_token"].(string)
}

func TestPingNeedsNoLoginAndMovesTheSite(t *testing.T) {
	h := setup(t)
	id, token := newPush(t, h, "")

	for _, m := range []string{"GET", "POST", "HEAD"} {
		if code, _ := plain(t, m, h.srv.URL+"/ping/"+token); code != 200 {
			t.Errorf("%s /ping/<token> = %d: a cron one-liner may use any of these", m, code)
		}
	}
	if code, body := plain(t, "GET", h.srv.URL+"/ping/"+token); code != 200 || body != "ok\n" {
		t.Fatalf("a plain, boring 200: %d %q", code, body)
	}
	c := h.bearer(t)
	var v map[string]any
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		_, v, _ = c.do("GET", "/api/sites/"+id, "")
		if v["state"] == "up" && v["last_ping"] != nil {
			break
		}
	}
	if v["state"] != "up" || v["last_ping"] == nil || v["ping_deadline"] == nil {
		t.Fatalf("a ping must make the site up and show when the next is due: %v", v)
	}
}

func TestReportedFailureGoesDownAtOnce(t *testing.T) {
	h := setup(t)
	id, token := newPush(t, h, "")
	for _, m := range []string{"GET", "POST"} {
		if code, _ := plain(t, m, h.srv.URL+"/ping/"+token+"/fail"); code != 200 {
			t.Fatalf("%s /fail = %d", m, code)
		}
	}
	c := h.bearer(t)
	var state any
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && state != "down"; time.Sleep(10 * time.Millisecond) {
		_, v, _ := c.do("GET", "/api/sites/"+id, "")
		state = v["state"]
	}
	if state != "down" {
		t.Fatalf("state = %v", state)
	}
	_, inc, _ := c.do("GET", "/api/incidents?site="+id, "")
	list := inc["incidents"].([]any)
	if len(list) == 0 || list[0].(map[string]any)["kind"] != "reported_failure" {
		t.Fatalf("incidents: %v", inc)
	}
}

func TestUnknownTokensAre404AndGuessingIsLimited(t *testing.T) {
	h := setup(t)
	_, token := newPush(t, h, "")
	guess := strings.Repeat("a", 32)
	for i := 0; i < 20; i++ {
		if code, _ := plain(t, "GET", h.srv.URL+"/ping/"+guess); code != 404 {
			t.Fatalf("guess %d = %d, want 404", i, code)
		}
	}
	if code, _ := plain(t, "GET", h.srv.URL+"/ping/"+guess); code != 429 {
		t.Fatalf("a client that keeps guessing must be stopped, got %d", code)
	}
	if code, _ := plain(t, "GET", h.srv.URL+"/ping/"+token); code != 429 {
		t.Fatalf("while locked out even the right token waits (the job retries later), got %d", code)
	}
}

func TestPingIsAcknowledgedEvenWhenPausedAndNeverRevealsAnything(t *testing.T) {
	h := setup(t)
	id, token := newPush(t, h, "")
	h.bearer(t).do("POST", "/api/sites/"+id+"/pause", "")
	time.Sleep(50 * time.Millisecond)
	if code, body := plain(t, "GET", h.srv.URL+"/ping/"+token); code != 200 || body != "ok\n" {
		t.Fatalf("a paused site must still answer 200 so the job does not fail: %d %q", code, body)
	}
}

func TestRotatingTheTokenKillsTheOldURL(t *testing.T) {
	h := setup(t)
	c := h.bearer(t)
	id, old := newPush(t, h, "")
	code, rotated, _ := c.do("POST", "/api/sites/"+id+"/token", "")
	fresh, _ := rotated["push_token"].(string)
	if code != 200 || len(fresh) != 32 || fresh == old {
		t.Fatalf("%d %v", code, rotated)
	}
	if code, _ := plain(t, "GET", h.srv.URL+"/ping/"+old); code != 404 {
		t.Fatalf("the old URL must stop working at once, got %d", code)
	}
	if code, _ := plain(t, "GET", h.srv.URL+"/ping/"+fresh); code != 200 {
		t.Fatalf("the new one works, got %d", code)
	}
	web := func() string {
		_, s, _ := c.do("POST", "/api/sites", `{"url":"https://w.example"}`)
		return s["id"].(string)
	}()
	if code, _, _ := c.do("POST", "/api/sites/"+web+"/token", ""); code != 400 {
		t.Fatalf("a web site has no token: %d", code)
	}
	if code, _, _ := h.anon(t).do("POST", "/api/sites/"+id+"/token", ""); code != 401 {
		t.Fatalf("rotating needs a login: %d", code)
	}
}

func TestTokenNeverLeavesThroughThePublicPage(t *testing.T) {
	h := setup(t, enableStatus)
	_, token := newPush(t, h, `,"public":true`)
	_, doc, _ := h.anon(t).do("GET", "/api/public/status", "")
	raw, _ := json.Marshal(doc)
	if !strings.Contains(string(raw), "Backup") {
		t.Fatalf("a public push site is listed: %s", raw)
	}
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), "push://") || strings.Contains(string(raw), "push_token") {
		t.Fatalf("the secret must never reach the public page: %s", raw)
	}
}

func TestPushSitesAreNotCheckedByHand(t *testing.T) {
	h := setup(t)
	c := h.bearer(t)
	id, _ := newPush(t, h, "")
	if code, _, _ := c.do("POST", "/api/sites/"+id+"/check", ""); code != 400 {
		t.Fatalf("%d", code)
	}
	if code, _, _ := c.do("POST", "/api/check", `{"type":"push"}`); code != 400 {
		t.Fatalf("the draft check must refuse it too: %d", code)
	}
}
