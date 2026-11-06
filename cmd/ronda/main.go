// Command ronda watches websites from the outside and tells you on Telegram when one goes down.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/wisnuvb/ronda/sitespec"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/wisnuvb/ronda/check"
	"github.com/wisnuvb/ronda/history"
	"github.com/wisnuvb/ronda/i18n"
	"github.com/wisnuvb/ronda/internal/api"
	"github.com/wisnuvb/ronda/internal/auth"
	"github.com/wisnuvb/ronda/internal/bot"
	"github.com/wisnuvb/ronda/internal/config"
	"github.com/wisnuvb/ronda/internal/heartbeat"
	"github.com/wisnuvb/ronda/internal/registry"
	"github.com/wisnuvb/ronda/model"
	"github.com/wisnuvb/ronda/monitor"
	"github.com/wisnuvb/ronda/notify"
	"github.com/wisnuvb/ronda/report"
	"github.com/wisnuvb/ronda/web"
)

var version = "dev"

const usage = `ronda %s: watch websites from the outside and tell you on Telegram when one goes down.

Usage:
  ronda run [-config /etc/ronda/config.json]    run the monitor + dashboard + API
  ronda demo [-listen 127.0.0.1:9191] [-lang en|id] [-timezone UTC] [-public]
                                                   dashboard with synthetic data, no login (to try it out)
  ronda hash-password                             turn a password (read from stdin) into a hash for auth.password_hash
  ronda check [flags] URL                         check one URL now (nothing is saved)
  ronda config-check [-config ...]                validate the config (runs nothing)
  ronda healthcheck [-config ...] [-addr host:port]
                                                   exit 0 if the running instance answers /healthz (for Docker HEALTHCHECK)
  ronda telegram-test [-config ...]               send a test message to Telegram
  ronda gen-token                                 create a random API token
  ronda site list|add|rm|pause|resume|export|import ...  manage sites through the running API
  ronda version

The "site" command reads RONDA_API_TOKEN and RONDA_API (default http://127.0.0.1:9191).
Examples: ronda site add -name Shop -keyword "Cart" https://shop.example.com
          ronda site add -name Database tcp://db.example.com:5432
          ronda site add -dns-type MX -keyword mail.example.com dns://example.com
          ronda site export -o sites.json       then on another server: ronda site import -dry-run sites.json
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(args)
	case "check":
		err = cmdCheck(args)
	case "config-check":
		err = cmdConfigCheck(args)
	case "healthcheck":
		err = cmdHealthcheck(args)
	case "telegram-test":
		err = cmdTelegramTest(args)
	case "demo":
		err = cmdDemo(args)
	case "hash-password":
		err = cmdHashPassword(os.Stdin)
	case "gen-token":
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		fmt.Println(hex.EncodeToString(b))
	case "site":
		err = cmdSite(args)
	case "version", "-v", "--version":
		fmt.Println("ronda", version)
	case "help", "-h", "--help":
		fmt.Printf(usage, version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ronda:", err)
		os.Exit(1)
	}
}

// parse lets flags appear before or after positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	return pos, nil
}

func buildNotifier(c *config.Config) notify.Notifier {
	var ns notify.Multi
	if c.Notify.TelegramToken != "" {
		ns = append(ns, notify.NewTelegram(c.Notify.TelegramToken, c.ChatIDs()).WithRoutes(c.Routes()))
	}
	if c.Notify.WebhookURL != "" {
		ns = append(ns, notify.NewWebhook(c.Notify.WebhookURL))
	}
	if len(ns) == 0 {
		return nil
	}
	return ns
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	path := fs.String("config", "/etc/ronda/config.json", "path to config.json")
	static := fs.String("static", "", "read the dashboard files from this folder instead of the binary (for developing the UI)")
	fs.Parse(args)

	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	reg, err := registry.Open(cfg.DataDir)
	if err != nil {
		return err
	}

	var queue *notify.Queue
	var direct notify.Notifier // the unqueued notifier: reports and other free text bypass the queue
	var notifier monitor.Notifier
	if n := buildNotifier(cfg); n != nil {
		direct = n
		gap := time.Duration(*cfg.Notify.BatchSec) * time.Second
		queue = notify.NewQueueWith(n, notify.QueueOptions{
			Pace:      1100 * time.Millisecond, // Telegram allows about one message per second per chat
			BatchGap:  gap,
			BatchMax:  max(45*time.Second, 6*gap),
			DigestMin: cfg.Notify.DigestMin,
		})
		notifier = queue
	} else {
		log.Printf("warning: no notifications are configured (Telegram/webhook); outages are only recorded in the log and the API")
	}

	reg.SetRecipients(cfg.RecipientNames())
	hist := history.Open(cfg.DataDir)
	debug.FreeOSMemory() // loading a big history leaves a lot of garbage; hand it back instead of holding it
	mon := monitor.New(monitor.Options{
		CertWarnDays:       cfg.CertWarnDays,
		RemindAfterMin:     cfg.Notify.RemindAfterMin,
		EventRetentionDays: cfg.EventRetentionDays,
		Sites:              reg,
		Checker:            check.New(),
		Notifier:           notifier,
		Control:            &monitor.Control{Addrs: cfg.ControlAddrs},
		History:            hist,
		DataDir:            cfg.DataDir,
		MaxConcurrent:      cfg.MaxConcurrent,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	monDone := make(chan struct{})
	go func() { mon.Run(ctx); close(monDone) }()
	histDone := make(chan struct{})
	go func() { hist.AutoSave(ctx, 2*time.Minute); close(histDone) }()

	var side sync.WaitGroup // bot and heartbeat: background helpers that stop with ctx
	if cfg.Notify.TelegramCommands {
		tg := notify.NewTelegram(cfg.Notify.TelegramToken, cfg.ChatIDs())
		side.Add(1)
		go func() {
			defer side.Done()
			bot.New(tg, reg, mon).WithAdmins(cfg.Notify.TelegramAdminIDs).WithHistory(hist).Run(ctx)
		}()
		log.Printf("Telegram bot enabled for %d chat(s)", len(cfg.ChatIDs()))
	}
	if sched := (report.Schedule{Daily: cfg.Notify.ReportDaily, Weekly: cfg.Notify.ReportWeekly, WeeklyDay: cfg.ReportWeeklyDay()}); sched.Enabled() {
		if tn, ok := direct.(notify.TextNotifier); ok {
			rs := &report.Scheduler{Schedule: sched, Dir: cfg.DataDir, Send: func(k report.Kind) {
				sites, incs := report.Collect(mon, hist)
				r := report.Build(k, sites, incs, time.Now())
				tn.NotifyText(r.HTML(), r.Text())
				// Each client with sites of its own also gets a report of only those sites.
				if rn, ok := direct.(notify.RoutedTextNotifier); ok {
					for _, name := range cfg.RecipientNames() {
						if rr, any := report.For(name, k, sites, incs, time.Now()); any {
							rn.NotifyTextTo(name, rr.HTML())
						}
					}
				}
				log.Printf("%s report sent", k)
			}}
			side.Add(1)
			go func() { defer side.Done(); rs.Run(ctx) }()
			log.Printf("periodic reports enabled (daily %q, weekly %q)", sched.Daily, sched.Weekly)
		}
	}
	if cfg.HeartbeatURL != "" {
		pinger := heartbeat.New(cfg.HeartbeatURL, time.Duration(cfg.HeartbeatIntervalSec)*time.Second, mon.Healthy)
		side.Add(1)
		go func() { defer side.Done(); pinger.Run(ctx) }()
		log.Printf("heartbeat enabled every %d s", cfg.HeartbeatIntervalSec)
	} else {
		log.Printf("warning: heartbeat_url is empty; if ronda itself dies, nothing will tell you")
	}

	staticFS := web.Static()
	if *static != "" {
		staticFS = os.DirFS(*static)
	}
	if cfg.Auth.PasswordHash == "" {
		log.Printf("warning: auth.password_hash is empty, so the dashboard login is disabled (the API and CLI still work with api.token)")
	}

	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: api.New(api.Options{
			Registry: reg, Monitor: mon, History: hist,
			Token: cfg.API.Token, User: cfg.Auth.User, PasswordHash: cfg.Auth.PasswordHash,
			TrustProxy: cfg.TrustProxy, Static: staticFS, DataDir: cfg.DataDir, Recipients: cfg.RecipientNames(),
			StatusPage: api.StatusPageOptions{Enabled: cfg.StatusPage.Enabled, Title: cfg.StatusPage.Title, Description: cfg.StatusPage.Description},
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second, // a draft check may take up to 60s
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("ronda %s: %d sites, API at http://%s, data in %s, language %s, timezone %s", version, len(reg.List()), cfg.Listen, cfg.DataDir, i18n.Lang(), model.Loc())

	select {
	case err = <-errc:
		stop()
	case <-ctx.Done():
		log.Printf("stopping...")
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	<-monDone
	_ = mon.Close()
	side.Wait()
	<-histDone
	hist.Save() // the monitor may have recorded a few more results after the autosave's last pass
	if queue != nil {
		queue.Close(10 * time.Second)
	}
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	keyword := fs.String("keyword", "", "text that must appear on the page")
	timeout := fs.Int("timeout", 10, "timeout in seconds")
	method := fs.String("method", "GET", "GET or HEAD")
	private := fs.Bool("allow-private", false, "allow private/loopback addresses")
	noRedirect := fs.Bool("no-redirect", false, "do not follow redirects")
	dnsType := fs.String("dns-type", "", "record type for dns:// (A, AAAA, MX, TXT, NS)")
	dnsServer := fs.String("dns-server", "", "DNS server for dns:// (host or host:port)")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: ronda check [flags] URL   (URL may be https://..., tcp://host:port or dns://name)")
	}
	site := model.Site{URL: pos[0], Method: *method, Keyword: *keyword, TimeoutSec: *timeout,
		AllowPrivate: *private, NoRedirect: *noRedirect, IntervalSec: 60, DNSType: *dnsType, DNSServer: *dnsServer}
	if err := sitespec.Normalize(&site); err != nil {
		return err
	}
	r := check.New().Check(context.Background(), site)

	web := site.Type == sitespec.TypeHTTP
	switch {
	case r.OK && web:
		fmt.Printf("OK  HTTP %d  %d ms", r.Status, r.LatencyMs)
	case r.OK:
		fmt.Printf("OK  %s  %d ms", site.Type, r.LatencyMs)
	default:
		fmt.Printf("FAIL  %s", r.Kind)
		if r.Status != 0 {
			fmt.Printf("  HTTP %d  %d ms", r.Status, r.LatencyMs)
		}
	}
	if web {
		fmt.Printf("  (dns %d · connect %d · tls %d · ttfb %d ms)", r.DNSMs, r.ConnectMs, r.TLSMs, r.TTFBMs)
	}
	fmt.Println()
	if r.Answer != "" {
		fmt.Println("Answer:", r.Answer)
	}
	if r.CertNotAfter != 0 {
		left := time.Until(time.UnixMilli(r.CertNotAfter))
		fmt.Printf("TLS certificate valid until %s (%d days left)\n",
			model.In(time.UnixMilli(r.CertNotAfter)).Format("2006-01-02"), int(left.Hours()/24))
	}
	if r.Hint != "" {
		fmt.Println(r.Hint)
	}
	if !r.OK {
		os.Exit(1)
	}
	return nil
}

// cmdHealthcheck asks a running instance whether it is alive. It exists for container health checks, where the
// image has no curl: exit status 0 means /healthz answered 200 within a few seconds. The address is the config's
// listen (so RONDA_LISTEN applies), or -addr.
func cmdHealthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	path := fs.String("config", "/etc/ronda/config.json", "path to config.json")
	addr := fs.String("addr", "", "host:port to ask instead of the config's listen")
	fs.Parse(args)
	if *addr == "" {
		cfg, err := config.Load(*path)
		if err != nil {
			return err
		}
		*addr = cfg.Listen
	}
	host, port, err := net.SplitHostPort(*addr)
	if err != nil {
		return err
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1" // listening on every address: ask over loopback
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz answered %s", resp.Status)
	}
	return nil
}

// cmdConfigCheck validates the config and prints a one-line summary. It never prints a secret.
func cmdConfigCheck(args []string) error {
	fs := flag.NewFlagSet("config-check", flag.ExitOnError)
	path := fs.String("config", "/etc/ronda/config.json", "path to config.json")
	fs.Parse(args)
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	yn := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	fmt.Printf("ok listen=%s data_dir=%s timezone=%s language=%s dashboard=%s telegram=%s bot_commands=%s heartbeat=%s\n", cfg.Listen, cfg.DataDir,
		cfg.Timezone, cfg.Language, yn(cfg.Auth.PasswordHash != ""), yn(cfg.Notify.TelegramToken != ""), yn(cfg.Notify.TelegramCommands), yn(cfg.HeartbeatURL != ""))
	return nil
}

func cmdTelegramTest(args []string) error {
	fs := flag.NewFlagSet("telegram-test", flag.ExitOnError)
	path := fs.String("config", "/etc/ronda/config.json", "path to config.json")
	fs.Parse(args)
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if cfg.Notify.TelegramToken == "" {
		return errors.New("notify.telegram_token and notify.telegram_chat_id are not set")
	}
	tg := notify.NewTelegram(cfg.Notify.TelegramToken, cfg.ChatIDs())
	if err := tg.Send(model.Event{Time: time.Now().UnixMilli(), State: "test", SiteName: "ronda"}); err != nil {
		return err
	}
	fmt.Printf("sent to %d chat(s)\n", len(cfg.ChatIDs()))
	return nil
}

// ---- site: thin client for the running API ----

func apiCall(method, path string, body any) ([]byte, error) {
	base := strings.TrimRight(config.Getenv("RONDA_API"), "/")
	if base == "" {
		base = "http://127.0.0.1:9191"
	}
	tok := config.Getenv("RONDA_API_TOKEN")
	if tok == "" {
		return nil, errors.New("RONDA_API_TOKEN is not set")
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the API at %s: %w", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		return nil, fmt.Errorf("the API answered %s", resp.Status)
	}
	return raw, nil
}

// splitList turns "a, b" into ["a" "b"]; an empty string is no list at all.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cmdSite(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ronda site list|add|rm|pause|resume|export|import ...")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		raw, err := apiCall("GET", "/api/sites", nil)
		if err != nil {
			return err
		}
		var out struct {
			Sites []monitor.View `json:"sites"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTATUS\tRESPONSE\tNAME\tURL")
		for _, s := range out.Sites {
			lat := "-"
			if s.Last != nil {
				lat = fmt.Sprintf("%d ms", s.Last.LatencyMs)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.ID, strings.ToUpper(s.State), lat, s.Name, s.URL)
		}
		return tw.Flush()

	case "add":
		fs := flag.NewFlagSet("site add", flag.ExitOnError)
		name := fs.String("name", "", "display name (default: the host)")
		interval := fs.Int("interval", 0, "seconds between checks (default 60)")
		timeout := fs.Int("timeout", 0, "timeout in seconds (default 10)")
		threshold := fs.Int("threshold", 0, "consecutive failures before DOWN (default 3)")
		keyword := fs.String("keyword", "", "text that must appear on the page")
		method := fs.String("method", "", "GET (default) or HEAD")
		ua := fs.String("user-agent", "", "custom User-Agent")
		private := fs.Bool("allow-private", false, "allow private/loopback addresses")
		noRedirect := fs.Bool("no-redirect", false, "do not follow redirects")
		dnsType := fs.String("dns-type", "", "record type for dns:// (A, AAAA, MX, TXT, NS)")
		dnsServer := fs.String("dns-server", "", "DNS server for dns:// (host or host:port)")
		notifyTo := fs.String("notify-to", "", "send notifications only to these recipients (names from notify.recipients, comma-separated; \"default\" = the main chat)")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return errors.New("usage: ronda site add [flags] URL")
		}
		raw, err := apiCall("POST", "/api/sites", model.Site{
			URL: pos[0], Name: *name, IntervalSec: *interval, TimeoutSec: *timeout, FailThreshold: *threshold,
			Keyword: *keyword, Method: *method, UserAgent: *ua, AllowPrivate: *private, NoRedirect: *noRedirect,
			DNSType: *dnsType, DNSServer: *dnsServer, NotifyTo: splitList(*notifyTo),
		})
		if err != nil {
			return err
		}
		var s model.Site
		_ = json.Unmarshal(raw, &s)
		fmt.Printf("added: %s  %s  (%s)\n", s.ID, s.Name, s.URL)
		if s.PushToken != "" {
			fmt.Printf("ping address (secret): <ronda address>/ping/%s\n  from cron: ... && curl -fsS -m 10 --retry 3 <ronda address>/ping/%s\n", s.PushToken, s.PushToken)
		}
		return nil

	case "export":
		fs := flag.NewFlagSet("site export", flag.ExitOnError)
		out := fs.String("o", "", "write to this file (default: the screen)")
		tokens := fs.Bool("tokens", false, "include the ping tokens of push monitors (SECRET; keep the file like a password)")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) != 0 {
			return errors.New("usage: ronda site export [-tokens] [-o file]")
		}
		path := "/api/export"
		if *tokens {
			path += "?tokens=1"
		}
		raw, err := apiCall("GET", path, nil)
		if err != nil {
			return err
		}
		if *out == "" {
			_, err = os.Stdout.Write(raw)
			return err
		}
		if err := os.WriteFile(*out, raw, 0o600); err != nil { // 0600: with -tokens it holds secrets
			return err
		}
		fmt.Fprintf(os.Stderr, "written to %s\n", *out)
		if *tokens {
			fmt.Fprintln(os.Stderr, "warning: this file holds ping tokens (secret). Keep it like a password.")
		}
		return nil

	case "import":
		fs := flag.NewFlagSet("site import", flag.ExitOnError)
		dry := fs.Bool("dry-run", false, "only show what would happen, without saving")
		pos, err := parse(fs, rest)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return errors.New("usage: ronda site import [-dry-run] FILE   (or - to read from stdin)")
		}
		var data []byte
		if pos[0] == "-" {
			data, err = io.ReadAll(io.LimitReader(os.Stdin, 8<<20))
		} else {
			data, err = os.ReadFile(pos[0])
		}
		if err != nil {
			return err
		}
		if !json.Valid(data) {
			return errors.New("the file is not JSON; use a file made by `ronda site export`")
		}
		path := "/api/import"
		if *dry {
			path += "?dry_run=1"
		}
		raw, err := apiCall("POST", path, json.RawMessage(data))
		if err != nil {
			return err
		}
		var res registry.ImportResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		for _, it := range res.Items {
			if it.Status != registry.ImportAdded {
				who := it.Name
				if who == "" {
					who = it.URL
				}
				fmt.Printf("  #%d %-8s %s: %s\n", it.Index, it.Status, who, it.Reason)
			}
		}
		verb := "added"
		if res.DryRun {
			verb = "would be added (dry run, nothing saved)"
		}
		fmt.Printf("%d %s, %d already there, %d invalid\n", res.Added, verb, res.Skipped, res.Invalid)
		if res.Invalid > 0 {
			return fmt.Errorf("%d invalid site(s)", res.Invalid)
		}
		return nil

	case "rm", "pause", "resume":
		if len(rest) != 1 {
			return fmt.Errorf("usage: ronda site %s ID", sub)
		}
		method, path := "POST", "/api/sites/"+rest[0]+"/"+sub
		if sub == "rm" {
			method, path = "DELETE", "/api/sites/"+rest[0]
		}
		if _, err := apiCall(method, path, nil); err != nil {
			return err
		}
		fmt.Println("ok")
		return nil
	}
	return fmt.Errorf("unknown subcommand: %s", sub)
}

// cmdHashPassword reads one line from in and prints its hash. The password comes from
// stdin so that it never appears in the process list; to avoid shell history too, use:
//
//	read -rs PW; printf '%s\n' "$PW" | ronda hash-password
func cmdHashPassword(in io.Reader) error {
	line, err := bufio.NewReader(io.LimitReader(in, 4096)).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return errors.New("the password is read from stdin; for example: read -rs PW; printf '%s\\n' \"$PW\" | ronda hash-password")
	}
	h, err := auth.HashPassword(strings.TrimRight(line, "\r\n"))
	if err != nil {
		return err
	}
	fmt.Println(h)
	return nil
}
