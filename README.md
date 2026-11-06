# Ronda

**A self-hosted website and uptime monitor that watches from the outside and tells you on Telegram when something is down.**

[![CI](https://github.com/wisnuvb/ronda/actions/workflows/ci.yml/badge.svg)](https://github.com/wisnuvb/ronda/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

English | [Bahasa Indonesia](README.id.md)

*Ronda* is Indonesian for the night-watch round that a neighbourhood walks to check that all is well. This is the same
job for your websites, servers and scheduled jobs.

One static Go binary, standard library only, with the web dashboard embedded. Sites are added, changed, paused and
removed **while it runs**, from the dashboard, the CLI or the API.

## Why it exists

A monitor that runs on the server it watches dies with that server, and then nobody tells you. A check made from
inside the server (through a local nginx, say) also cannot see DNS, CDN, certificate or firewall problems. Ronda checks
like a visitor does: it opens a fresh connection (DNS, TCP, TLS, response) for every check.

**Run it on a different machine than the one it monitors.** A small VPS elsewhere is enough. Ronda can also ping an
outside service ([heartbeat](#heartbeat-who-watches-the-watcher)) so you hear about it if Ronda itself dies.

## Features

- **Checks:** HTTP(S), TCP port, DNS record, and **push monitors** for cron jobs and backups.
- **Diagnosis, not just "down":** the alert says why (DNS, refused, timeout, certificate, Cloudflare 52x, HTTP 4xx/5xx,
  missing keyword, ...). Several consecutive failures are needed, and a "my own network is down" test runs first.
- **Telegram alerts** (and an optional JSON webhook): bulk-outage digests, "still down" reminders, certificate warnings,
  daily/weekly reports, and a bot to check, pause, add or remove sites from chat.
- **Dashboard** with uptime (24 h to 90 days), charts, incidents and a hashed-password login; an optional
  **public status page**; maintenance windows; per-site recipients for several clients.
- Export/import, a permanent incident archive, and a systemd installer with rollback.
- English and Indonesian; any IANA time zone with daylight saving.

## Quick start

Requires Go 1.24 or newer (`go.mod` pins `toolchain go1.27.3`, which the `go` command downloads by itself).

**Try it first.** This starts the dashboard with synthetic data, no login, only on `127.0.0.1`:

```bash
make demo        # open http://127.0.0.1:9191
```

Options: `go run ./cmd/ronda demo -lang id -timezone Asia/Jakarta -listen 127.0.0.1:9191` (the demo refuses
non-loopback addresses because it has no login).

**Run it locally with a real config:**

```bash
make build                                                       # produces bin/ronda
./bin/ronda gen-token                                            # put it in api.token
read -rs PW; printf '%s\n' "$PW" | ./bin/ronda hash-password     # put the output in auth.password_hash (min. 12 characters)
cp deploy/config.example.json config.json                        # fill api.token, auth.password_hash, data_dir, notify.*
./bin/ronda telegram-test -config config.json
./bin/ronda run -config config.json                              # dashboard at http://127.0.0.1:9191
```

`read -rs` reads the password without echoing it and without writing it to your shell history. Without
`auth.password_hash` the dashboard login is disabled (the login page explains how to enable it), but the API and CLI
still work with `api.token`.

**Install on a Linux server (systemd).** On your computer, build the release tarball (it runs the tests first) and install
over SSH:

```bash
make release                                     # dist/ronda-<version>.tar.gz (linux amd64 + arm64)
make check-host HOST=ubuntu@1.2.3.4              # read-only check, changes nothing
make deploy HOST=ubuntu@1.2.3.4 ARGS="--set-password --timezone Europe/Berlin"
```

Or copy the tarball to the server, unpack it and run `sudo ./install.sh` there. See [Installing on a server](#installing-on-a-server).

## Configuration

One JSON file (`-config`, default `/etc/ronda/config.json`); unknown keys are rejected. Validate it with
`ronda config-check -config config.json`. Starting point: [`deploy/config.example.json`](deploy/config.example.json).

| Key | Default | Meaning |
|---|---|---|
| `listen` | `127.0.0.1:9191` | address for dashboard and API (no TLS; see [Security](#security)) |
| `data_dir` | `/var/lib/ronda` | where state, history and the incident archive are kept |
| `timezone` | `UTC` | IANA zone name; see [Language and time zone](#language-and-time-zone) |
| `language` | `en` | `en` or `id` |
| `trust_proxy` | `false` | believe `X-Forwarded-For`/`-Proto`; only behind a reverse proxy that all requests pass through |
| `api.token` | required | bearer token for scripts and CLI, at least 16 characters, no spaces. Ronda refuses to start without it |
| `auth.user` | `admin` | dashboard user name |
| `auth.password_hash` | empty | output of `ronda hash-password`; empty disables the dashboard login |
| `notify.telegram_token`, `notify.telegram_chat_id` | empty | set together; several chat ids separated by commas |
| `notify.webhook_url` | empty | receives one JSON document per batch of events |
| `notify.telegram_commands` | `false` | enable the [Telegram bot](#telegram-bot) |
| `notify.telegram_admin_ids` | `[]` | Telegram user ids allowed to change data from a group chat |
| `notify.batch_sec` | `8` | alert batching window, 0 to 120; `0` turns batching off |
| `notify.digest_min` | `3` | consecutive alerts merged into one summary, 2 to 50 |
| `notify.remind_after_min` | `[60, 360, 1440]` | "still down" reminders, minutes 5 to 43200; `[]` turns them off |
| `notify.report_daily`, `notify.report_weekly` | empty | `"HH:MM"` in `timezone`; empty = off |
| `notify.report_weekly_day` | `1` | 0 = Sunday ... 6 = Saturday |
| `notify.recipients` | none | named extra chats, see [Per-site recipients](docs/notifications.md#per-site-recipients) |
| `status_page.enabled` / `title` / `description` | `false` / `Service status` / empty | [public status page](#public-status-page); title up to 100, description up to 300 characters |
| `cert_warn_days` | `[14, 7, 3, 1]` | days before expiry at which to warn (1 to 365); `[]` turns it off |
| `event_retention_days` | `365` | how long incidents are kept, 7 to 3650 |
| `heartbeat_url` | empty | [heartbeat](#heartbeat-who-watches-the-watcher) address (`http://` or `https://`) |
| `heartbeat_interval_sec` | `60` | 10 to 3600 |
| `control_addrs` | `["1.1.1.1:443", "8.8.8.8:443"]` | reachability probes before declaring DOWN; `[]` disables them |
| `max_concurrent_checks` | `20` | 1 to 500 |

Other startup rules: periodic reports need `telegram_token` or `webhook_url`; `recipients` and `telegram_commands`
need `telegram_token`; `telegram_token` and `telegram_chat_id` must be set together.

**Environment overrides.** Secrets can come from the environment instead of the file; a non-empty variable wins.

| Variable | Overrides |
|---|---|
| `RONDA_LISTEN` | `listen` |
| `RONDA_TIMEZONE`, `RONDA_LANGUAGE` | `timezone`, `language` |
| `RONDA_API_TOKEN` | `api.token` (the CLI reads it too) |
| `RONDA_PASSWORD_HASH` | `auth.password_hash` |
| `RONDA_TELEGRAM_TOKEN`, `RONDA_TELEGRAM_CHAT_ID` | `notify.telegram_token`, `notify.telegram_chat_id` |
| `RONDA_WEBHOOK_URL` | `notify.webhook_url` |
| `RONDA_HEARTBEAT_URL` | `heartbeat_url` |
| `RONDA_API` | CLI only: API address (default `http://127.0.0.1:9191`) |

The old `WEBMON_*` names are still accepted as a deprecated fallback (a warning is logged) and will be removed in a
future release.

### Language and time zone

```json
{ "language": "en", "timezone": "Europe/Berlin" }
```

One instance has one language and one time zone, set for the whole process.

- **`language`**: `en` (default) or `id`. It affects Telegram and webhook messages, reports, the bot, API error text
  and the dashboard. A message without a translation falls back to English, never to an empty string. CLI output and
  help are English only.
- **`timezone`**: an IANA name such as `UTC`, `Europe/Berlin`, `America/New_York`, `Asia/Jakarta`. It decides the clock
  time in notifications and the dashboard, what `"08:00"` means in `report_daily`/`report_weekly` and maintenance
  windows, and where a day starts in the 90-day uptime history. Names such as `Local`, `WIB` or `GMT+7` are
  **rejected** because they depend on the server or are ambiguous.
- **Daylight saving is followed.** A maintenance window or report at `02:00` stays at 02:00 wall-clock after the clocks
  change, and days are 23 or 25 hours long on change days. Zone abbreviations (`CEST`, `WIB`) are printed.
- **Changing `timezone` on a running instance** changes what stored `"HH:MM"` times mean (maintenance windows,
  report times). History and incidents are stored in UTC and are not affected.

## Installing on a server

`install.sh` targets **Linux with systemd** (amd64 and arm64). It is safe to run again.

**From a release.** Each release attaches `ronda-<version>.tar.gz` (both Linux binaries, the installer, the unit and the
examples) and a `SHA256SUMS` file. On the server:

```bash
curl -fLO https://github.com/wisnuvb/ronda/releases/download/v0.1.0/ronda-v0.1.0.tar.gz
curl -fLO https://github.com/wisnuvb/ronda/releases/download/v0.1.0/SHA256SUMS
sha256sum -c SHA256SUMS
tar xzf ronda-v0.1.0.tar.gz && cd ronda-v0.1.0
./install.sh --check                   # read-only: can this host run Ronda?
sudo ./install.sh --set-password
```

You can build the same tarball yourself with `make release` (see [Quick start](#quick-start)).

To publish a read-only demo of your own, run `ronda demo -public` behind a reverse proxy. It serves synthetic data, listens
on loopback only and refuses every change (`GET` and `HEAD` only, `/ping/` included), so never point it at real data.

| Command | What it does |
|---|---|
| `./install.sh --check` | read-only: Linux, architecture, systemd, binary, port, curl |
| `sudo ./install.sh --dry-run` | print the steps, change nothing |
| `sudo ./install.sh [--set-password] [--listen ADDR] [--timezone ZONE] [--lang CODE] [--no-start]` | install or upgrade |
| `sudo ./install.sh --reset-password` | change the dashboard password (stdin or prompt) and restart |
| `sudo ./install.sh --uninstall [--purge]` | remove; `--purge` also deletes config, data and the `ronda` user |

`--listen` (default `127.0.0.1:9191`), `--timezone` (IANA name, default `UTC`) and `--lang` (`en` or `id`, default `en`)
only matter when the config file is created; `--set-password` likewise (the installer warns if the config already
exists). `--no-start` installs without starting the service.

**Layout**

| What | Where |
|---|---|
| binary | `/usr/local/bin/ronda` |
| config | `/etc/ronda/config.json` (mode `640 root:ronda`, random API token already filled in) |
| data | `/var/lib/ronda` |
| user and group | `ronda` (system user, no shell) |
| service | `ronda.service`, hardened: no capabilities, read-only filesystem except the data directory, `MemoryMax=256M`, `GOMEMLIMIT=192MiB`; `systemd-analyze security` score 1.7 |

**Upgrades** keep your config and data. The new binary is validated against the config (`config-check`) *before*
anything running is touched, and if the new version is not healthy within 20 seconds the previous binary is put
back. The dashboard listens on `127.0.0.1` only; open it through an SSH tunnel
(`ssh -N -L 9191:127.0.0.1:9191 user@server`) or a [reverse proxy](#security).

### Upgrading from webmon

Ronda is the new name of the project's working name, "webmon". If a `webmon` install is present, `sudo ./install.sh`
detects it and migrates automatically:

1. stops the old `webmon` service;
2. **copies** `/etc/webmon` and `/var/lib/webmon` to `/etc/ronda` and `/var/lib/ronda` (rewriting `data_dir`);
3. installs and starts `ronda`;
4. if Ronda is healthy, the old service is disabled and its unit and binary are removed. The old config and data
   directories are **left in place** for you to delete once you have checked everything;
5. if Ronda is not healthy, the old service is started again and nothing of the old install has been changed.

webmon always ran in WIB and in Indonesian. So that nothing shifts, if the migrated config has no `timezone` or
`language` key the installer adds `"timezone": "Asia/Jakarta"` and `"language": "id"` to `/etc/ronda/config.json` and
prints what it added. Without that, the instance would fall back to UTC and English, and the clock times of its
maintenance windows and reports would move by 7 hours. You can change both keys afterwards; changing `timezone` later
shifts what the stored `"HH:MM"` window and report times mean (see [Language and time zone](#language-and-time-zone)).

If the old install used a `data_dir` other than `/var/lib/webmon`, the data stays where it is, the `ronda` user takes
ownership of it, and the installer adds a systemd drop-in (`/etc/systemd/system/ronda.service.d/data-dir.conf`) that lets
the service write there. The unit otherwise only allows writes to `/var/lib/ronda`.

Dashboard sessions end once, because the session cookie is renamed (`ronda_session`). The `WEBMON_*` environment
variables still work for now (deprecated, see [Configuration](#configuration)); update them to `RONDA_*`.

### CLI

The `site` commands use the running API. The CLI itself only speaks English.

```bash
export RONDA_API_TOKEN=<the api.token>            # RONDA_API sets another address
ronda site add -name Shop -keyword "Cart" https://shop.example.com
ronda site list
ronda site pause <id>                             # also: resume, rm
ronda check https://example.com                   # check once, saves nothing
ronda site export -o sites.json                   # on another server: ronda site import -dry-run sites.json
```

Other commands: `run`, `demo`, `hash-password`, `config-check`, `telegram-test`, `gen-token`, `version`.

### Setting up Telegram (free)

1. Talk to `@BotFather`, send `/newbot` and keep the token it gives you.
2. Send any message to the bot (or add the bot to a group and write there).
3. Open `https://api.telegram.org/bot<TOKEN>/getUpdates` and read `chat.id` (groups are usually negative).
4. Fill in `notify.telegram_token` and `notify.telegram_chat_id` (several ids separated by commas), then run
   `ronda telegram-test`.

## Docker

Each release publishes a multi-architecture image (`linux/amd64`, `linux/arm64`) to GitHub's container registry as
`ghcr.io/wisnuvb/ronda` (tags `0.1.0`, `0.1` and `latest`). Until one is published you can build it yourself with
`docker build -t ronda .`. The image is built `FROM scratch`: the binary, the CA certificates (HTTPS checks need
them) and a state directory, about 3.5 MB. It runs as the unprivileged user `65532` and works with a read-only root
filesystem and all capabilities dropped.

```bash
# from a clone of the repository you can use deploy/config.example.json instead of downloading it
curl -fL -o config.json https://raw.githubusercontent.com/wisnuvb/ronda/v0.1.0/deploy/config.example.json
docker run --rm ghcr.io/wisnuvb/ronda:latest gen-token     # a random api.token; put it in config.json (leave data_dir as /var/lib/ronda)
docker run -d --name ronda --restart unless-stopped \
  -p 127.0.0.1:9191:9191 \
  -v "$PWD/config.json:/etc/ronda/config.json:ro" -v ronda-data:/var/lib/ronda \
  --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  ghcr.io/wisnuvb/ronda:latest
```

[deploy/docker-compose.yml](deploy/docker-compose.yml) is the same as a Compose file.

- **Port.** The image sets `RONDA_LISTEN=0.0.0.0:9191`, because inside a container the config's `127.0.0.1` would be
  unreachable. Publish the port to the host's loopback as above, and put a reverse proxy with TLS in front of it
  ([deploy/nginx.conf.example](deploy/nginx.conf.example)); do not publish it to the internet as it is.
- **Data.** Keep `/var/lib/ronda` on a volume. A named volume works as is; a bind-mounted directory must be owned by
  uid `65532`. If `data_dir` is not writable, Ronda refuses to start and says so.
- **Settings from the environment.** `RONDA_TIMEZONE`, `RONDA_LANGUAGE` and the secrets in
  [Environment overrides](#configuration) work in a container; the config file is still required.
- **Health check.** The image declares one (`ronda healthcheck`, which asks `/healthz` over loopback); there is no
  `curl` in it.
- **CLI.** `docker exec -e RONDA_API_TOKEN=<token> ronda ronda site list` runs the CLI against the container.

## Dashboard

| Page | Contents |
|---|---|
| **Sites** (`#/`) | Up/Down/Paused summary, search, filter, sort; table with 24 h uptime, last response time and a strip of the last 30 checks. Down rows show the cause. Refreshes every 10 seconds. |
| **Site detail** (`#/sites/ID`) | Uptime for 24 h / 7 / 30 / 90 days, average and p95 response time, certificate days left, response-time chart (1 hour to 90 days), details of the last check (DNS, connect, TLS, first byte), settings and this site's incidents. Buttons: Check now, Pause/Resume, Edit, Delete. |
| **Incidents** (`#/incidents`) | Every down period: start, end, duration, cause. Ongoing ones are marked. |
| **Add / Edit** | Dialog with a **Test first** button (checks without saving), inline validation, and server errors shown on the field concerned. |

Every page has its own address (bookmarkable). The old `#/situs/ID` and `#/insiden` still open. The theme follows the
system and can be switched in the top bar. Times are shown in the server's `timezone`, the same as in Telegram messages.

Accessibility by design (see [docs/testing.md](docs/testing.md) for what was verified): status is icon plus text, never
colour alone; contrast at least 4.5:1 for text and 3:1 for components in both themes; keyboard operation with a skip
link and focus moved to the page heading; charts can be explored with arrow keys/Home/End/Escape and have a text summary
and table alternative; chart failures are marked by shape and the word "FAIL"; `prefers-reduced-motion` is respected;
on narrow screens tables become cards.

## Check types

The type comes from the address prefix, so API, bot and CLI need no extra field.

| Address | What is checked | Fails when |
|---|---|---|
| `https://example.com` | a web page | status, text, TLS or time does not match |
| `tcp://db.example.com:5432` | the port accepts connections (database, SMTP, SSH, ...) | refused, no answer, or host not found |
| `dns://example.com` | the DNS record resolves | NXDOMAIN, no record of that type, no answer, or the answer lacks `keyword` |
| push monitor | a job pings Ronda | no ping within *schedule + grace*; see [Push monitors](#push-monitors-cron-and-backup-jobs) |

For DNS: `dns_type` (A, AAAA, MX, TXT, NS; default A), `dns_server` (`host` or `host:port`; empty = this server's
resolver) and `keyword` as the **answer that must be present**, e.g. the expected IP address. That catches records that
changed or point to the wrong place. The dashboard shows the last answer.

```bash
ronda check tcp://db.example.com:5432
ronda check -dns-type MX -keyword mail.example.com dns://example.com
ronda site add -name Database tcp://db.example.com:5432
```

TCP checks and DNS servers use the **same SSRF guard** as web checks: loopback, private and link-local addresses are
refused unless `allow_private` is enabled for that site (otherwise the dashboard could be used to port-scan an
internal network). A site's type cannot be changed after creation (a different target is a different history). A TCP
check only tests the connection, not the protocol on top of it.

## Push monitors: cron and backup jobs

A normal check asks "does the site answer?". A nightly backup has no address to ask, so the direction is reversed:
**the job calls Ronda after it succeeds, and silence means failure.** In the dashboard: Add site, choose **Scheduled
job**, set how often it reports and how much lateness to tolerate. The site page shows the ping address and a crontab
example:

```bash
0 2 * * * /usr/local/bin/backup.sh && curl -fsS -m 10 --retry 3 https://mon.example.com/ping/<token>
```

The `&&` matters: the ping is only sent if the backup succeeded. To report failure explicitly without waiting for the
schedule to lapse, call `.../ping/<token>/fail`. `GET` and `POST` are both accepted.

- **DOWN** when no ping arrives within *schedule + grace*; **recovers on the next ping**. `interval_sec` is 60 s to 31
  days (default 1 day), `grace_sec` 0 to 86400 (default 300), and `fail_threshold` is how many consecutive schedules
  may be missed (default 1).
- After Ronda restarts, every job gets one full period before it counts as late, so a restart does not cause false
  alarms. Paused sites and sites under maintenance still **accept** pings (answered `ok`) but do not evaluate or alert.
- **The ping address is a secret.** Anyone who knows it can send fake pings and hide a failure. Treat it like a password
  and keep it out of public repositories. If it leaks, click **Rotate token** (the old address dies at once). The
  token is random (128 bit) and compared in constant time; an unknown token is answered `404`; 20 wrong guesses per
  10 minutes per client address lock the ping path temporarily (`429`).
- The ping address never appears in Telegram messages, the public status page or logs. Only the dashboard (after login)
  and API responses for that site show it.
- Your reverse proxy must forward `/ping/` (see `deploy/nginx.conf.example`); this path deliberately has no login.
  Use `access_log off` there because the token is in the URL.
- The Telegram bot cannot create push monitors (`/add push://...` is refused): tokens must not travel through chat.
- Ronda does not know whether a job *should* run more often; it only judges whether a ping arrived.

## Public status page

A page anyone can open without logging in, for your clients or users. **Off by default:**

```json
"status_page": { "enabled": true, "title": "Example Service Status", "description": "Latest news about our services." }
```

Then tick **Show on public status page** on each site that should appear (default: none). The page is at `/status` (data
at `/api/public/status`): an overall banner, each site with status, 90-day uptime and a per-day bar, plus the
incidents of the last 14 days.

**What never leaves:** URLs, ids, technical hints ("connection refused to 10.0.0.5:8443"), and unticked sites.
Incidents show only a general kind (for example "Connection refused"). Paused sites are hidden. The site **name** is
shown as you typed it, so do not name a site something the public must not see. The result is cached for 15 seconds so
many visitors do not load the monitor, and the page is marked `noindex`.

To expose it without exposing the dashboard, forward **only** these paths in your reverse proxy:

```nginx
location = /status             { proxy_pass http://127.0.0.1:9191; }
location = /api/public/status  { proxy_pass http://127.0.0.1:9191; }
location ~ ^/(app\.css|theme\.js|status\.js|dom\.js|icons\.js|fmt\.js|favicon\.svg|i18n\.js|i18n-id\.js|app-config\.js)$ { proxy_pass http://127.0.0.1:9191; }
```

`app-config.js` is generated by the server: it tells the page which language and time zone Ronda runs in, and holds
nothing secret.

## Maintenance windows

Before a deploy or migration, press **Maintenance** on the site page (30 minutes to 7 days) or send
`/maintenance shop 2h` to the bot. During the window the site is **not checked and no notifications are sent**. When it
ends the site is checked again **by itself**, and if it is still down you are told. **End maintenance** ends it early.
This differs from **Pause**, which has no end time. Maintenance time does not count toward uptime because nothing was
checked. Through the API a window may last 1 minute to 30 days.

**Weekly schedule.** For recurring work (a nightly backup, a restart every Sunday), press **Set schedule** on the site
page: choose days, a start time (in `timezone`) and a length; up to 7 windows per site, and a window may cross
midnight. The scheduler wakes exactly when a window starts and ends. **End maintenance** during a scheduled window
skips only *that run*; next week's window still applies. Through the API, `maintenance_weekly` holds
`[{"days": [6, 0], "start": "02:00", "minutes": 120}]` (days: 0 = Sunday ... 6 = Saturday).

## Notifications

Alerts go to Telegram (`notify.telegram_token` plus `telegram_chat_id`) and, if set, a webhook. Details, rules and
examples for each item are in [docs/notifications.md](docs/notifications.md).

- **Batching.** Alerts are held until **8 seconds pass with nothing new** (at most 45 seconds), then **3 or more
  consecutive down (or recovered) alerts become one message** with each site's cause and a note that the cause is probably
  shared. Fewer than 3 go one by one; certificate warnings are never merged. Tune with `notify.batch_sec` (default 8;
  `0` turns it off) and `notify.digest_min` (default 3).
- **"Still down" reminders** at **1 h, 6 h and 24 h** after a site went down, then once a day, with the latest cause.
  One per threshold, restart-safe, never recorded as a new incident. `notify.remind_after_min` (minutes, 5 to 43200;
  `[]` turns them off).
- **Certificate warnings**, once as a certificate crosses 14, 7, 3 and 1 days to expiry (`cert_warn_days`, `[]` to turn
  off). A certificate that has already expired is DOWN (`tls_expired`).
- **Scheduled reports** (off by default): `"report_daily": "08:00"`, `"report_weekly": "08:00"`,
  `"report_weekly_day": 1` (0 = Sunday ... 6 = Saturday; times in `timezone`). Paused sites are not counted. A report is
  sent once, and late by at most 3 hours after a restart. `/report` in the bot makes one on demand.
- **Per-site recipients** for several clients: define `notify.recipients` (`{"client-a": "-1001234567890"}`), then set
  `notify_to` per site (form, `site add -notify-to`, or API). Digests and reports never mix recipients, chat ids are
  never shown in the dashboard or API, and the bot only serves the main chat. `default` means the main chat.

## Telegram bot

Enable with `"telegram_commands": true` (off by default) to manage Ronda from chat. Command names are English first;
the Indonesian names remain as aliases.

| Command | Does | Aliases |
|---|---|---|
| `/status`, `/list` | summary and the sites that are down; all sites with uptime and id | `/daftar` |
| `/report [daily\|weekly]` | uptime, incidents and certificates summary (default weekly); open to all group members | `/laporan [harian\|mingguan]` |
| `/check site` | check now, does not change state | `/cek` |
| `/pause site`, `/resume site` | pause, resume | `/jeda`, `/lanjut` |
| `/maintenance site 2h`, `/endmaint site` | maintenance (30m, 2h, 1d); end it | `/pemeliharaan`, `/akhiri` |
| `/add url [name]` | add a site; `shop.example.com` without a scheme is read as `https://`; also `tcp://host:port` and `dns://name` | `/tambah` |
| `/remove site yes` | remove; without `yes` (or `ya`) it only asks for confirmation | `/hapus`, `/delete` |

`site` can be an id, a name, or part of a name or address. If it is ambiguous the bot lists the candidates and changes
nothing. With `language` set to `id`, the bot's help shows the Indonesian command names.

**In groups, not every member may change data.** A private chat has one person, so every command works. In a group,
`/status`, `/list`, `/report` and `/check` are open to all members, but commands that **change** things (`/add`,
`/remove`, `/pause`, `/resume`, `/maintenance`, `/endmaint`, and their Indonesian aliases) are only accepted from users
whose Telegram id is listed:

```json
"notify": { "telegram_commands": true, "telegram_admin_ids": [123456789] }
```

These are **user** ids (positive numbers; message `@userinfobot` to find yours), not group ids. Without the list a
group can only read. The bot also refuses when Telegram does not state the chat type (fail closed), and refusals are
logged with the user id.

Bot security: only chats in `notify.telegram_chat_id` are served; other chats get **no reply** (their id is logged,
which helps when filling in the config). **In a group every member can use the read-only commands**, so use a
private chat or a group whose members are all trusted. Commands queued while Ronda was off are **skipped** at start.
There is no way to enable `allow_private` through the bot. The bot uses *long polling*, so it needs no public address.
Only one process may poll with the same token (otherwise the log explains the 409 error).

## Data, export/import and password

- **Incident archive.** Down, recovery and certificate events are appended to `events.jsonl` in the data directory and
  the Incidents list is built from it. Kept `event_retention_days` days (default 365, 7 to 3650).
  API: `GET /api/incidents?limit=&before=`.
- **Export/import** moves site settings (not history or incidents) between servers; import always previews first, is safe
  to repeat, and takes at most 5,000 sites per file (500 per server).
  Push-monitor ping tokens are secret and left out unless you export with `-tokens`:

  ```bash
  ronda site export -o sites.json            # GET  /api/export
  ronda site import -dry-run sites.json      # POST /api/import?dry_run=1  (only shows the result)
  ronda site import sites.json               # POST /api/import
  ```
- **Password change** from the dashboard (lock button, at least 12 characters) works from a login session only, signs out
  all other devices, and is stored in `auth.json` in the data directory. A changed `auth.password_hash` in the config
  (for example via `install.sh --reset-password`) always wins over that file.

More in [docs/data-and-accounts.md](docs/data-and-accounts.md).

## Heartbeat: who watches the watcher?

If Ronda or its server dies, nobody tells you, because the messenger died too. Set `heartbeat_url` to a ping address
from an outside service that alarms when pings stop (for example healthchecks.io, which can notify via Telegram), and
Ronda calls it every `heartbeat_interval_sec` (default 60). The ping **deliberately stops** if checking has stalled (no
check finished within twice the longest check interval plus one minute; push monitors do not count), so a process
that is alive but not working also triggers the alarm. The URL is a secret and is never written to the log.

## How monitoring works

- Each site is checked every `interval_sec` (default 60 s). Every check opens a new connection (DNS, TCP, TLS,
  response), like a new visitor. A newly added site is checked at once.
- **DOWN** only after `fail_threshold` (default 3) **consecutive** failures; while failing, checks repeat every 10 s to
  confirm. **RECOVERED** after 2 consecutive successes. A single blip sends nothing.
- **Monitor network down is not site down.** Before declaring DOWN, Ronda tries to connect to `control_addrs` (default
  1.1.1.1 and 8.8.8.8 on port 443). If all fail, the alert is held, logged, and the failure is **not counted** against
  the site's uptime. Set the list to `[]` to turn this off.
- State (up/down and since when) is stored, so a restart does not re-send alerts for sites that are already down.
- History: raw results for about the last 24 h (at most 2,880 points per site) and hourly summaries for 90 days, saved
  to `history.json` every 2 minutes and on shutdown. Uptime is never shown as 100% if any check failed (99.996% shows
  as 99.99%).
- Telegram messages state the **cause**, not just "down":

| Cause | Meaning |
|---|---|
| `dns` | domain not found / DNS did not answer |
| `refused` | server reachable but nothing listens on that port |
| `timeout_connect` / `timeout_tls` / `timeout_response` | server does not answer / TLS stalls / backend stalls |
| `tls_expired` / `tls_hostname` / `tls_untrusted` | certificate expired / name mismatch / not trusted |
| `cloudflare` | 520 to 526 and 530, each explained (521 = origin down, 522 = timeout, ...) |
| `http_5xx` / `http_4xx` | 502, 503, 504, 403 (possibly a WAF), 404, 429, ... |
| `keyword` | the page opens but the expected text is missing |
| `redirect_loop`, `blocked`, `reset`, `unreachable` | redirect loop, internal address blocked, connection cut, no route |
| `missed`, `reported_failure` | push monitor: no ping in time / the job reported failure itself |

## Scale and performance

Measured with a real daemon on a laptop (arm64, macOS): **500 sites**, each with full history (2,880 raw points + 2,160
hourly summaries, a 50 MB `history.json`, like an installation a year old), checked every 10 seconds (about 50 checks per
second) for 150 seconds while the API was queried continuously. All checks succeeded (500 `up`, no warnings in the log).

| | Result |
|---|---|
| Peak memory (RSS) | **164 MB** (the unit's `MemoryMax` is 256M) |
| Average CPU | **11.3%** |
| Site list (`GET /api/sites`), median | **13 ms** (p95 15 ms) |
| Detail / history / status page, median | 1.1 / 1.0 / 0.6 ms |
| Ready after start | 0.8 s |

The limit is 500 sites per server. The systemd unit sets `GOMEMLIMIT=192MiB` so the garbage collector works harder
before reaching `MemoryMax`. These are numbers **from one developer's machine**, against local targets with no real
network latency; on a small VPS they will differ, so repeat with a similar script if your scale approaches hundreds of
sites. See [docs/testing.md](docs/testing.md).

## Per-site options

| Field | Default | Notes |
|---|---|---|
| `url` | required | `http(s)://...`, `tcp://host:port` or `dns://name`; no `user:password` |
| `type` | from `url` | `http`, `tcp`, `dns` or `push` (cannot change after creation) |
| `name` | host | name in messages and lists (max 100 characters) |
| `method` | `GET` | `GET` or `HEAD` (web only) |
| `interval_sec` | 60 | 10 to 86400 (push: 60 to 2,678,400, default 86400) |
| `timeout_sec` | 10 | 1 to 60, must not exceed the interval |
| `fail_threshold` | 3 | 1 to 10 (push: default 1) |
| `grace_sec` | 300 | push only: extra time after `interval_sec` before a ping counts as missed, 0 to 86400 |
| `expect_status` | 200 to 399 | list of codes counted healthy, for example `[200, 401]` |
| `keyword` | none | web: text that must appear in the first 1 MB of the page; dns: answer that must be present (case-insensitive) |
| `dns_type`, `dns_server` | `A`, system | dns only: record type, DNS server |
| `no_redirect` | false | do not follow redirects |
| `user_agent` | `ronda/1 (uptime-check)` | for sites behind a WAF that blocks monitors |
| `allow_private` | false | allow loopback/LAN targets (see [Security](#security)) |
| `paused` | false | stop checking without deleting |
| `public` | false | show on the public status page |
| `notify_to` | none | recipient names, see [Per-site recipients](docs/notifications.md#per-site-recipients) |
| `maintenance_weekly` | none | weekly windows, see [Maintenance windows](#maintenance-windows) |

`maintenance_until` (Unix time in ms) marks a one-off maintenance window; set it through the endpoints below. Changing
`url` or `method` clears that site's state and history (a different target). Pausing clears state but keeps history.

## API

Two ways in: `Authorization: Bearer <api.token>` (scripts, CLI), or the session cookie from `POST /api/login` (the
dashboard; changes also need the `X-CSRF-Token` header from `GET /api/session`). Error text follows `language`.

| Method and path | Does |
|---|---|
| `POST /api/login`, `POST /api/logout`, `GET /api/session` | sign in, sign out, who is signed in |
| `POST /api/password` | change the dashboard password (login session only) |
| `GET /api/sites` | all sites: status, 24 h uptime, history strip |
| `POST /api/sites` | add a site (JSON as in the table above) |
| `GET /api/sites/{id}` | one site with full statistics and the last 60 results |
| `PATCH /api/sites/{id}`, `DELETE /api/sites/{id}` | change some fields, delete |
| `POST /api/sites/{id}/pause`, `/resume` | pause / resume |
| `POST /api/sites/{id}/check` | check now (does not change state or notify) |
| `POST /api/sites/{id}/maintenance` `{"minutes": N}`, `DELETE …/maintenance` | start maintenance (1 minute to 30 days), end it |
| `POST /api/sites/{id}/token` | rotate a push monitor's ping token |
| `GET /api/sites/{id}/history?range=1h\|6h\|24h\|7d\|30d\|90d` | chart data (raw up to 24 h, hourly above) |
| `POST /api/check` | test a draft site without saving it |
| `GET /api/export[?tokens=1]`, `POST /api/import[?dry_run=1]` | download all sites as JSON, import such a file (ping tokens only on request) |
| `GET /api/incidents[?site=ID&limit=&before=]`, `GET /api/events` | incidents; raw events (last 300) |
| `GET /api/recipients` | names of the configured recipients |
| `GET /ping/{token}`, `POST /ping/{token}`, and `…/fail` | push-monitor pings (no login; the token is the credential) |
| `GET /status`, `GET /api/public/status` | public status page and its data, if enabled (no login) |
| `GET /healthz` | `ok`, no authentication |

## Security

- **Dashboard login:** passwords are stored as PBKDF2-HMAC-SHA256 (310,000 iterations, random salt). Response time and
  message do not distinguish a wrong user name from a wrong password. **5 failed attempts per 10 minutes per address**
  lock the login (HTTP 429). Sessions live in memory (a restart signs everyone out) and expire after 12 hours idle or 7
  days after sign-in. The cookie is `HttpOnly`, `SameSite=Strict`, and `Secure` over HTTPS; it is named
  `ronda_session`, or `__Host-ronda_session` over HTTPS, so a neighbouring subdomain cannot overwrite it ("cookie tossing").
- **CSRF:** a cookie alone cannot change data; the session's `X-CSRF-Token` header is required. Login refuses
  cross-origin requests (`Origin` header).
- **Strict CSP:** `default-src 'none'; script-src 'self'; style-src 'self'` with no `unsafe-inline` or `unsafe-eval`,
  plus `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: no-referrer`, `Permissions-Policy` and
  `Cross-Origin-Opener-Policy`. The UI is built with `createElement` and text nodes, never by assembling HTML, so a
  site name or message cannot become markup. Automated tests guard this (no inline script or style, no `innerHTML`
  except static icons).
- **API token** is required (at least 16 characters, no spaces); the program refuses to start without one.
- **Listens on `127.0.0.1` without TLS by default.** For remote access put a reverse proxy with HTTPS in front and set
  `"trust_proxy": true`. nginx example (a complete one is in `deploy/nginx.conf.example`):

  ```nginx
  location / {
      proxy_pass http://127.0.0.1:9191;
      proxy_set_header Host $host;
      proxy_set_header X-Forwarded-For $remote_addr;
      proxy_set_header X-Forwarded-Proto $scheme;
  }
  ```

  Behind Cloudflare, install `ngx_http_realip_module` so `$remote_addr` is the visitor's IP. Otherwise all visitors look
  like one address and the login rate limit locks everyone out at once. Enable `trust_proxy` **only** if every request
  really passes through that proxy.
- **SSRF protection.** Since the URL is free-form, connections to loopback, private (10/8, 172.16/12, 192.168/16),
  link-local (including 169.254.169.254, cloud metadata), CGNAT and multicast addresses are **refused** by default. The
  check is made on the address actually connected to (after DNS), so redirects and DNS rebinding are covered, and
  translated IPv6 forms (NAT64 `64:ff9b::/96`, 6to4 `2002::/16`, site-local, Teredo) are judged by the IPv4 address
  they embed (so legitimate DNS64 networks still work). To monitor an internal service, enable `allow_private` per site.
- **Standard-library vulnerabilities travel with the binary.** `go.mod` pins `toolchain go1.27.3`, and `make vuln`
  (`govulncheck`, needs network) should be rerun periodically, raising that version when it reports something new. A
  binary built earlier must be rebuilt to pick up a fix.
- **No secrets in the example files.** An automated test makes sure `deploy/config.example.json` contains no token, chat
  id or hash, and that no file in `deploy/` looks like a Telegram token or a webhook address; `make release` runs it,
  so a token pasted by accident while testing does not end up in the tarball.
- The bot token and heartbeat URL never reach logs or error messages. URLs containing `user:password` are rejected so
  credentials cannot show up in Telegram messages.
- Checks do not use proxies from the environment and read at most 1 MB of the body.

To report a vulnerability, see [SECURITY.md](SECURITY.md).

## Known limitations

- Many users and roles (there is a single admin account).
- The public status page has no visitor subscriptions and cannot show manual announcements.
- TCP checks only test the connection (no banner or protocol), and DNS checks do not verify DNSSEC or compare several
  servers at once.
- Maintenance windows are weekly only (no "a given date each month").
- **One vantage point:** if the network on the monitor's side is partly broken, false positives can appear.
- No escalation (alerting someone else when nobody responds) and no on-call schedule.
- Recipients are Telegram only; a webhook is not split per site.
- Reports are daily and weekly only (no monthly or per-client SLA), and uptime follows 24 h and 7 day windows, not
  calendar months.
- Export carries no uptime history or incident archive; moving those means copying the data folder (`history.json`,
  `events.jsonl`) while Ronda is stopped.
- The login rate limit counts **per IP address**: if many people share one NAT, or the proxy does not forward the real
  address (`trust_proxy` wrong), one person mistyping repeatedly can lock everyone out for 10 minutes.
- The installer supports Linux with systemd only (the [Docker image](#docker) runs anywhere Docker does). `make build`
  and `make demo` work on macOS and Linux with Go installed; Windows and macOS servers are not claimed.
- Language catalogs: English and Indonesian only; the CLI and its help are English only.

What has and has not been tested is listed honestly in [docs/testing.md](docs/testing.md).

## Development

```bash
make test        # go vet + go test -race
make vuln        # govulncheck: known vulnerabilities in the code and the Go standard library (needs network)
make demo        # dashboard with synthetic data; UI files are read from disk (edit, then reload)
make build       # bin/ronda for this machine
make release     # test, then dist/ronda-<version>.tar.gz with linux amd64 + arm64 binaries and the installer
```

**Layout of the source**

Packages at the top level are importable by other Go programs (see [docs/library.md](docs/library.md)); the ones under
`internal/` belong to this program only.

| Package | Contents |
|---|---|
| `model`, `sitespec` | shared types and the configured time zone; the rules for what a site may look like (types, defaults, limits) |
| `check` | HTTP, TCP and DNS checkers with diagnosis and the SSRF guard (`NewStrict` for services that probe strangers' addresses) |
| `monitor` | scheduling, state machine, reminders, certificate warnings, push monitors; storage through small interfaces (`SiteSource`, `History`, `EventLog`, `StateStore`) |
| `history`, `eventlog` | raw results and hourly summaries; the permanent incident archive (`events.jsonl`) |
| `notify`, `report` | Telegram/webhook delivery, queue, digests, routing; daily/weekly reports |
| `schedule`, `i18n` | weekly maintenance windows and `HH:MM` handling with daylight saving; message catalogs |
| `internal/config`, `internal/registry` | config file, defaults, validation, env overrides; the stored site list (`sites.json`), import/export |
| `internal/api`, `internal/auth`, `internal/bot` | HTTP API, sessions, public status page; password hashing and login limiter; the Telegram bot |
| `internal/heartbeat`, `internal/integration` | outbound heartbeat; cross-package routing tests |

Elsewhere: `cmd/ronda` (the CLI), `web/static` (the embedded dashboard and public page, plain ES modules, no build
step), `deploy` (installer, systemd unit, example config, nginx example).

**Adding a language.** The English text of a message is its key, so English needs no catalog and a missing translation
falls back to English. Other languages are catalogs of English text to translation:

- Server: `i18n/id_*.go`, one file per area, each calling `i18n.Register("id", map[string]string{...})` from
  `init()`. Messages are marked in code with `i18n.T(...)` (or `i18n.N(...)` to mark one without translating it yet). A new
  language gets its own files registered under its code; `language` then accepts that code.
- Dashboard: `web/static/i18n-id.js`, strict JSON after `export default`, one entry per line. Register the new catalog
  in the `CATALOGS` map (and the locale line) of `web/static/i18n.js`. Placeholders such as `{name}` and `%s` must be
  kept exactly as in the English text.
- Tests enforce completeness (`go test ./i18n ./web`): every message used in the code needs an entry in every
  shipped catalog, translations must keep their placeholders and format verbs, and the dashboard catalog may hold nothing
  the code no longer uses. Never build a sentence from translated pieces; translate whole sentences.

**A note on the nginx example.** `go test ./web` checks that the reverse-proxy example for the public status page in this
README lists every file the public page loads. If you add an import to `status.js`, the test fails until the example
is updated.

## License

[MIT](LICENSE), by Wisnu Saputro.
