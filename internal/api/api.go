// Package api serves the dashboard and its JSON API. Two ways in: a static bearer
// token for scripts and the CLI, and a login session cookie for the dashboard.
package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wisnuvb/ronda/sitespec"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wisnuvb/ronda/history"
	"github.com/wisnuvb/ronda/i18n"
	"github.com/wisnuvb/ronda/internal/auth"
	"github.com/wisnuvb/ronda/internal/registry"
	"github.com/wisnuvb/ronda/model"
	"github.com/wisnuvb/ronda/monitor"
)

const (
	maxBody    = 64 << 10
	cookieName = "ronda_session"
	// Over HTTPS the cookie is named with the __Host- prefix. A browser only accepts such a cookie if it
	// was set Secure, with Path=/ and no Domain, so a page on a sibling sub-domain cannot plant a cookie of
	// the same name for the whole parent domain (cookie tossing), which would otherwise lock people out.
	hostCookieName = "__Host-" + cookieName

	// No inline script or style, nothing from other origins.
	csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; " +
		"font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
)

// StatusPageOptions configures the public status page, the one part readable without logging in.
type StatusPageOptions struct {
	Enabled     bool
	Title       string
	Description string
}

type Options struct {
	Registry *registry.Registry
	Monitor  *monitor.Monitor
	History  *history.Store

	Token        string // bearer token for scripts and the CLI
	User         string // dashboard login
	PasswordHash string // empty: no dashboard login
	// DataDir is where a password changed from the dashboard is kept (auth.json). Empty turns that feature off.
	DataDir    string
	TrustProxy bool // believe X-Forwarded-* from a reverse proxy
	// Recipients are the configured recipient names a site can route its notifications to (config notify.recipients).
	Recipients []string

	Static     fs.FS // dashboard files; nil serves the API only
	StatusPage StatusPageOptions

	// NoAuth disables all authentication. It exists for `ronda demo` on loopback only.
	NoAuth bool

	// ReadOnly serves a public demo: only GET and HEAD are answered, and /ping/ (a GET that changes a push
	// monitor) is refused too, so a visitor can look at everything and change nothing.
	ReadOnly bool
}

type Server struct {
	opt      Options
	token    [sha256.Size]byte
	sessions *auth.Sessions
	limiter  *auth.Limiter
	pingLim  *auth.Limiter // wrong ping tokens, per client address

	spMu   sync.Mutex // the public page is computed at most once per spTTL, however many visitors there are
	spAt   time.Time
	spBody []byte

	pwMu  sync.RWMutex
	hash  string // the password hash in force: the config's, or a newer one saved from the dashboard
	dummy string // verified against when the user name is wrong, so timing does not reveal it
}

func New(opt Options) *Server {
	s := &Server{
		opt:      opt,
		token:    sha256.Sum256([]byte(opt.Token)),
		sessions: auth.NewSessions(12*time.Hour, 7*24*time.Hour),
		limiter:  auth.NewLimiter(5, 10*time.Minute),
		pingLim:  auth.NewLimiter(20, 10*time.Minute),
	}
	if opt.PasswordHash != "" {
		s.hash = opt.PasswordHash
		if saved, ok := auth.LoadOverride(opt.DataDir, opt.PasswordHash); ok {
			s.hash = saved
		}
		s.dummy, _ = auth.Decoy(s.hash)
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") })

	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.auth(s.logout))
	mux.HandleFunc("GET /api/session", s.session)
	mux.HandleFunc("GET /api/public/status", s.publicStatus)
	for _, m := range []string{"GET", "POST"} { // GET also answers HEAD, which is what some cron wrappers send
		mux.HandleFunc(m+" /ping/{token}", s.ping(false))
		mux.HandleFunc(m+" /ping/{token}/fail", s.ping(true))
	}
	mux.HandleFunc("GET /status", s.statusPage)
	mux.HandleFunc("POST /api/password", s.changePassword)

	mux.HandleFunc("GET /api/sites", s.auth(s.list))
	mux.HandleFunc("POST /api/sites", s.auth(s.create))
	mux.HandleFunc("GET /api/sites/{id}", s.auth(s.get))
	mux.HandleFunc("PATCH /api/sites/{id}", s.auth(s.patch))
	mux.HandleFunc("DELETE /api/sites/{id}", s.auth(s.remove))
	mux.HandleFunc("POST /api/sites/{id}/pause", s.auth(s.pause(true)))
	mux.HandleFunc("POST /api/sites/{id}/resume", s.auth(s.pause(false)))
	mux.HandleFunc("POST /api/sites/{id}/maintenance", s.auth(s.startMaintenance))
	mux.HandleFunc("DELETE /api/sites/{id}/maintenance", s.auth(s.endMaintenance))
	mux.HandleFunc("POST /api/sites/{id}/check", s.auth(s.checkSite))
	mux.HandleFunc("POST /api/sites/{id}/token", s.auth(s.rotateToken))
	mux.HandleFunc("GET /api/sites/{id}/history", s.auth(s.history))
	mux.HandleFunc("GET /api/recipients", s.auth(s.recipients))
	mux.HandleFunc("GET /api/export", s.auth(s.export))
	mux.HandleFunc("POST /api/import", s.auth(s.importSites))
	mux.HandleFunc("POST /api/check", s.auth(s.check))
	mux.HandleFunc("GET /api/events", s.auth(s.events))
	mux.HandleFunc("GET /api/incidents", s.auth(s.incidents))

	if s.opt.Static != nil {
		mux.HandleFunc("GET /app-config.js", s.appConfig)
		mux.Handle("GET /", http.FileServerFS(s.opt.Static))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if s.opt.ReadOnly && (!safeMethod(r.Method) || strings.HasPrefix(r.URL.Path, "/ping/")) {
			writeErr(w, http.StatusForbidden, i18n.T("This is a read-only demo: changes are turned off."))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// ---- authentication ----

func safeMethod(m string) bool { return m == http.MethodGet || m == http.MethodHead }

func (s *Server) loginEnabled() bool { return s.opt.PasswordHash != "" }

func (s *Server) unauthorized(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": i18n.T("Not logged in"), "login_enabled": s.loginEnabled()})
}

// auth lets a request through if it carries the bearer token, or a valid session
// cookie plus (for anything that changes data) the session's CSRF token.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.opt.NoAuth {
			next(w, r)
			return
		}
		if h := r.Header.Get("Authorization"); h != "" {
			tok, ok := strings.CutPrefix(h, "Bearer ")
			got := sha256.Sum256([]byte(tok))
			if ok && s.opt.Token != "" && subtle.ConstantTimeCompare(got[:], s.token[:]) == 1 {
				next(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="ronda"`)
			s.unauthorized(w)
			return
		}
		if se, ok := s.sessionOf(r); ok {
			if !safeMethod(r.Method) && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(se.CSRF)) != 1 {
				writeErr(w, http.StatusForbidden, i18n.T("Invalid CSRF token; reload the page"))
				return
			}
			next(w, r)
			return
		}
		s.unauthorized(w)
	}
}

func (s *Server) sessionOf(r *http.Request) (auth.Session, bool) {
	c, err := r.Cookie(s.cookieName(r))
	if err != nil {
		return auth.Session{}, false
	}
	return s.sessions.Get(c.Value)
}

// cookieName picks the session cookie's name for this request: prefixed over HTTPS, plain over HTTP
// (a browser refuses __Host- cookies on plain HTTP, and the plain name is never read over HTTPS).
func (s *Server) cookieName(r *http.Request) string {
	if s.isHTTPS(r) {
		return hostCookieName
	}
	return cookieName
}

func (s *Server) isHTTPS(r *http.Request) bool {
	return r.TLS != nil || (s.opt.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
}

// clientIP is the address failed logins are counted against. Behind a trusted
// proxy that is the last X-Forwarded-For entry, the one the proxy itself added.
func (s *Server) clientIP(r *http.Request) string {
	if s.opt.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sameOrigin rejects a cross-site login attempt. A request with no Origin header
// (not a browser form post) passes.
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := r.Host
	if s.opt.TrustProxy {
		if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
			host = fh
		}
	}
	return strings.EqualFold(u.Host, host)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.loginEnabled() {
		writeErr(w, http.StatusServiceUnavailable, i18n.T("Dashboard login is not enabled: set auth.password_hash in the config (create one with: ronda hash-password)"))
		return
	}
	if !s.sameOrigin(r) {
		writeErr(w, http.StatusForbidden, i18n.T("Request origin does not match"))
		return
	}
	ip := s.clientIP(r)
	if blocked, wait := s.limiter.Blocked(ip); blocked {
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests,
			i18n.T("Too many failed attempts; try again in %d minutes", int(wait.Minutes())+1))
		return
	}

	var in struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	userOK := subtle.ConstantTimeCompare([]byte(in.User), []byte(s.opt.User)) == 1
	hash, dummy := s.hashes()
	if !userOK {
		hash = dummy // same work either way, so the response time does not reveal the user name
	}
	passOK := auth.VerifyPassword(hash, in.Password)
	if !userOK || !passOK {
		s.limiter.Fail(ip)
		writeErr(w, http.StatusUnauthorized, i18n.T("Wrong username or password"))
		return
	}
	s.limiter.Reset(ip)

	token, se := s.sessions.Create(s.opt.User)
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(r), Value: token, Path: "/",
		HttpOnly: true, Secure: s.isHTTPS(r), SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"user": se.User, "csrf": se.CSRF, "can_change_password": s.opt.DataDir != "", "status_page": s.opt.StatusPage.Enabled})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.cookieName(r)); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(r), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.isHTTPS(r), SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// session tells the dashboard who is logged in, and hands it the CSRF token.
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	if s.opt.NoAuth {
		writeJSON(w, http.StatusOK, map[string]any{"user": "demo", "csrf": "", "demo": true, "status_page": s.opt.StatusPage.Enabled})
		return
	}
	if se, ok := s.sessionOf(r); ok {
		writeJSON(w, http.StatusOK, map[string]any{"user": se.User, "csrf": se.CSRF, "can_change_password": s.opt.DataDir != "", "status_page": s.opt.StatusPage.Enabled})
		return
	}
	s.unauthorized(w)
}

const spTTL = 15 * time.Second

type publicDoc struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	monitor.PublicStatus
}

// publicStatus is readable by anyone, so it is cached: a crowd of visitors (or one abusive client)
// costs one computation per spTTL, not one per request.
func (s *Server) publicStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex")
	if !s.opt.StatusPage.Enabled {
		writeErr(w, http.StatusNotFound, i18n.T("Status page is not enabled"))
		return
	}
	s.spMu.Lock()
	if s.spBody == nil || time.Since(s.spAt) > spTTL {
		body, err := json.Marshal(publicDoc{Title: s.opt.StatusPage.Title, Description: s.opt.StatusPage.Description, PublicStatus: s.opt.Monitor.PublicStatus()})
		if err != nil {
			s.spMu.Unlock()
			writeErr(w, http.StatusInternalServerError, i18n.T("Internal error"))
			return
		}
		s.spBody, s.spAt = body, time.Now()
	}
	body := s.spBody
	s.spMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(spTTL.Seconds())))
	_, _ = w.Write(body)
}

// statusPage serves the public page's HTML at /status.
func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex")
	if !s.opt.StatusPage.Enabled || s.opt.Static == nil {
		http.NotFound(w, r)
		return
	}
	page, err := fs.ReadFile(s.opt.Static, "status.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}

func (s *Server) hashes() (hash, dummy string) {
	s.pwMu.RLock()
	defer s.pwMu.RUnlock()
	return s.hash, s.dummy
}

// changePassword lets a logged-in person replace the dashboard password. It takes only a login session
// (not the API token, which belongs to scripts), proof of the current password, and it signs every
// other session out.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if s.opt.NoAuth || !s.loginEnabled() {
		writeErr(w, http.StatusForbidden, i18n.T("There is no dashboard login whose password can be changed"))
		return
	}
	if r.Header.Get("Authorization") != "" {
		writeErr(w, http.StatusForbidden, i18n.T("The password can only be changed from a dashboard login session, not with the API token"))
		return
	}
	c, err := r.Cookie(s.cookieName(r))
	if err != nil {
		s.unauthorized(w)
		return
	}
	se, ok := s.sessions.Get(c.Value)
	if !ok {
		s.unauthorized(w)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(se.CSRF)) != 1 {
		writeErr(w, http.StatusForbidden, i18n.T("Invalid CSRF token; reload the page"))
		return
	}
	if s.opt.DataDir == "" {
		writeErr(w, http.StatusServiceUnavailable, i18n.T("Data directory is not set, so the new password cannot be saved"))
		return
	}
	ip := s.clientIP(r)
	if blocked, wait := s.limiter.Blocked(ip); blocked {
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, i18n.T("Too many failed attempts; try again in %d minutes", int(wait.Minutes())+1))
		return
	}

	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &in) {
		return
	}

	s.pwMu.Lock()
	defer s.pwMu.Unlock()
	// 403, not 401: a 401 would tell the dashboard the session expired and log the person out.
	if !auth.VerifyPassword(s.hash, in.Current) {
		s.limiter.Fail(ip)
		writeErr(w, http.StatusForbidden, i18n.T("Current password is wrong"))
		return
	}
	if len([]rune(in.New)) < auth.MinPasswordLen {
		writeErr(w, http.StatusBadRequest, i18n.T("New password must be at least %d characters", auth.MinPasswordLen))
		return
	}
	if in.New == in.Current {
		writeErr(w, http.StatusBadRequest, i18n.T("New password must differ from the current one"))
		return
	}
	newHash, err := auth.HashPassword(in.New)
	if err == nil {
		err = auth.SaveOverride(s.opt.DataDir, s.opt.PasswordHash, newHash)
	}
	if err != nil {
		log.Printf("auth: could not save the new password: %v", err)
		writeErr(w, http.StatusInternalServerError, i18n.T("The new password could not be saved"))
		return
	}
	s.hash = newHash
	s.dummy, _ = auth.Decoy(newHash)
	s.limiter.Reset(ip)
	ended := s.sessions.RevokeOthers(c.Value)
	log.Printf("auth: dashboard password changed by %s; %d other sessions signed out", se.User, ended)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sessions_ended": ended})
}

// ---- sites ----

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sites": s.opt.Monitor.Views()})
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	v, ok := s.opt.Monitor.View(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, i18n.T(model.ErrNotFound.Error()))
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var site model.Site
	if !decode(w, r, &site) {
		return
	}
	created, err := s.opt.Registry.Add(site)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// recipients lists who a site can notify besides the main chats, for the form. Names are not secrets (no chat ids).
func (s *Server) recipients(w http.ResponseWriter, r *http.Request) {
	names := s.opt.Recipients
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipients": names})
}

// maxImportBody holds a file at registry.MaxImport sites with room to spare.
const maxImportBody = 8 << 20

// export downloads the site list as a file. Ping tokens are secrets, so they are left out unless asked for
// (?tokens=1); the file is then as sensitive as a password and is never cached.
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	exp, err := s.opt.Registry.Export(r.URL.Query().Get("tokens") == "1")
	if err != nil {
		fail(w, err)
		return
	}
	name := "ronda-sites-" + model.In(time.Now()).Format("20060102") + ".json"
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(exp)
}

// importSites adds the sites of an export file. ?dry_run=1 reports what would happen and stores nothing.
func (s *Server) importSites(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBody))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, i18n.T("File is too large or unreadable"))
		return
	}
	res, err := s.opt.Registry.Import(raw, r.URL.Query().Get("dry_run") == "1")
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) patch(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeErr(w, http.StatusBadRequest, i18n.T("Body is too large or unreadable"))
		return
	}
	updated, err := s.opt.Registry.Patch(r.PathValue("id"), raw)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	if err := s.opt.Registry.Delete(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) pause(paused bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		site, err := s.opt.Registry.SetPaused(r.PathValue("id"), paused)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, site)
	}
}

// startMaintenance silences a site for the given number of minutes; it resumes by itself.
func (s *Server) startMaintenance(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Minutes int `json:"minutes"`
	}
	if !decode(w, r, &in) {
		return
	}
	maxMin := int(sitespec.MaxMaintenance / time.Minute)
	if in.Minutes < 1 || in.Minutes > maxMin {
		writeErr(w, http.StatusBadRequest, i18n.T("minutes must be 1..%d", maxMin))
		return
	}
	site, err := s.opt.Registry.SetMaintenance(r.PathValue("id"), time.Now().Add(time.Duration(in.Minutes)*time.Minute).UnixMilli())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, site)
}

func (s *Server) endMaintenance(w http.ResponseWriter, r *http.Request) {
	site, err := s.opt.Registry.EndMaintenance(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, site)
}

// ping is how a scheduled job reports in. There is no login: the token in the path is the credential,
// 128 random bits, so the route is limited per client address to keep guessing hopeless, and it never
// says anything about a token beyond "unknown". A known token always gets a plain 200, whether or not the
// ping was counted (paused, in maintenance, repeated), so a job is never failed by our own state.
func (s *Server) ping(failed bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := s.clientIP(r)
		if blocked, wait := s.pingLim.Blocked(ip); blocked {
			w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
			http.Error(w, i18n.T("Too many wrong tokens"), http.StatusTooManyRequests)
			return
		}
		if s.opt.Monitor.Ping(r.PathValue("token"), failed) == monitor.PingUnknown {
			s.pingLim.Fail(ip)
			http.Error(w, i18n.T("Unknown token"), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	}
}

// rotateToken replaces a push site's secret; the old ping URL stops working at once.
func (s *Server) rotateToken(w http.ResponseWriter, r *http.Request) {
	site, err := s.opt.Registry.RotateToken(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, site)
}

// check probes a draft site without saving it, so a form can say "works / fails" before Add.
func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	var site model.Site
	if !decode(w, r, &site) {
		return
	}
	if err := sitespec.Normalize(&site); err != nil {
		fail(w, err)
		return
	}
	if site.Type == sitespec.TypePush {
		writeErr(w, http.StatusBadRequest, i18n.T("Push sites are not checked from here: they wait for a ping from their job"))
		return
	}
	writeJSON(w, http.StatusOK, s.opt.Monitor.Check(r.Context(), site))
}

// checkSite probes a stored site right now. It records nothing and changes no state.
func (s *Server) checkSite(w http.ResponseWriter, r *http.Request) {
	res, err := s.opt.Monitor.CheckSite(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.opt.Registry.Get(id); err != nil {
		fail(w, err)
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "24h"
	}
	data, err := s.opt.History.Series(id, rng)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"events": s.opt.Monitor.Events()})
}

// incidents pages through the archive, newest first. `before` is the Started of the last incident
// already received; `more` says whether older ones remain.
func (s *Server) incidents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeErr(w, http.StatusBadRequest, i18n.T("limit must be 1..200"))
			return
		}
		limit = n
	}
	var before int64
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, i18n.T("before must be a unix time in milliseconds"))
			return
		}
		before = n
	}
	page, more := s.opt.Monitor.IncidentsPage(q.Get("site"), before, limit)
	writeJSON(w, http.StatusOK, map[string]any{"incidents": page, "more": more})
}

// ---- helpers ----

// decode reads one strict JSON object from the body.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, i18n.T("Invalid JSON: %s", err.Error()))
		return false
	}
	if dec.More() {
		writeErr(w, http.StatusBadRequest, i18n.T("Invalid JSON: trailing data after the object"))
		return false
	}
	return true
}

func fail(w http.ResponseWriter, err error) {
	var inv *model.InvalidError
	switch {
	case errors.Is(err, model.ErrNotFound):
		writeErr(w, http.StatusNotFound, i18n.T(err.Error()))
	case errors.As(err, &inv):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, i18n.T("Internal error"))
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// appConfig tells the dashboard which language and time zone the server runs in, so its text and its
// clock times match the Telegram messages. It is public and holds nothing secret; it is loaded as a
// script before the dashboard so that window.RONDA exists when the first module runs.
func (s *Server) appConfig(w http.ResponseWriter, r *http.Request) {
	cfg := map[string]any{"lang": i18n.Lang(), "timezone": model.Loc().String()}
	if s.opt.ReadOnly {
		cfg["demo"] = true // the dashboard shows a banner saying so
	}
	b, _ := json.Marshal(cfg)
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprintf(w, "window.RONDA = %s;\n", b)
}
