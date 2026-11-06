# What has been tested, and what has not

This page records what was verified and, just as important, what was not. Statements about a feature in the README
describe how the code is designed; this page says how far that has been checked in practice. It is kept in third person
and is updated as testing changes.

[Back to the README](../README.md)

## Automated tests

`make test` runs `go vet` and `go test -race` over all packages. Together they cover:

- the full logic (state machine, scheduling, registry validation, import/export, reports, notification batching and
  routing, authentication, the API);
- the Telegram bot against a *fake* Telegram server, including the group-admin restriction and `/report`;
- the heartbeat;
- DNS checks against a stub DNS server (all record types, NXDOMAIN, SERVFAIL, timeout);
- the maintenance scheduler with a controllable clock;
- daylight-saving behaviour of maintenance windows, reports and day boundaries;
- completeness of the language catalogs (every message in the code has a translation with matching placeholders), and
  the guarantees of the embedded web files (no inline script or style, every referenced file embedded, the public
  status page's files all listed in the README's reverse-proxy example);
- that `deploy/config.example.json` holds no token, chat id or hash, and that no file in `deploy/` looks like a Telegram
  token or webhook address.

## Tested on a real daemon with real timers

- a weekly maintenance window that starts and ends by itself;
- one summary message for six sites failing and recovering together;
- changing the password (it survived a restart and lost against a hash in the config);
- import of an old `events.json`;
- the 500-site load test (see below);
- the attack script against the daemon itself (see below);
- push monitors with real pings via `curl`;
- the "still down" reminder: DOWN, exactly one reminder at minute 5 with `remind_after_min: [5]`, then RECOVERED, received
  through a webhook;
- export and then import between two daemons through the CLI: preview, import, a repeat import in which every site was
  skipped, a corrupt file exiting with code 1, and an old ping token still accepted on the destination server;
- a daily report with a real clock: scheduled two minutes ahead, it arrived 9 seconds after its time through a webhook,
  with real data from one healthy and one dead site; a restart afterwards did not repeat it;
- the per-site recipient path with the monitor, the queue and a real Telegram client against a *fake* Telegram server:
  three sites of client A going down together became one summary in chat A only, while client B and the main chat heard
  only about their own sites;
- heartbeat and clean shutdown.

## Installer

- The installer was run on Ubuntu 24.04 arm64 with real systemd: install, idempotent upgrade, a broken config, an
  unhealthy binary followed by rollback, password reset and uninstall.
- It was re-run in an Ubuntu 24.04 arm64 container with the latest unit: `GOMEMLIMIT` installed and visible in the
  process, `systemd-analyze security` score 1.7, a push monitor working from the installed daemon, data surviving an
  upgrade, and `--uninstall --purge` leaving nothing behind.
- The migration from an install still named `webmon` was tested in real systemd containers (Ubuntu 24.04 arm64), starting
  from a genuine old install made with the old installer and the old binary, with a site created through its API:
  - `--check` and `--dry-run` change nothing (the old service stays active, no `ronda` user or directories appear).
  - Successful migration: Ronda is active, the old service is stopped, disabled and its unit and binary removed, the
    site and the old API token still work, `data_dir` is rewritten, `timezone` `Asia/Jakarta` and `language` `id` are
    added to the migrated config, `/app-config.js` reports them, and the old config and data directories are left in
    place.
  - Rollback when the new binary starts but does not become healthy, and when its config check fails: the old service
    is active again with its data and token, and the unit, binary, config and data of `ronda` are gone.
  - A `data_dir` outside `/var/lib/webmon`: the data stays where it is and now belongs to the `ronda` user. This test
    found a bug: the unit's `ProtectSystem=strict` made that directory read-only, so Ronda looked healthy but every write
    returned HTTP 500. The installer now adds a systemd drop-in with `ReadWritePaths`; with it a site added after the
    migration survives a restart, and the rollback restores the old owner and removes the drop-in.
- The amd64 binary was run in an *emulated* x86_64 container.

## Docker image

- The image was built on an arm64 host (Docker Desktop) and run with the settings of `deploy/docker-compose.yml`: read-only
  root filesystem, all capabilities dropped, `no-new-privileges`, the unprivileged user `65532`, a named volume for
  `/var/lib/ronda`, and a config file mounted read-only.
  - Its health check reported `healthy`; `/healthz` answered from the host.
  - A site added through the API was still there after `docker restart`.
  - `RONDA_TIMEZONE` and `RONDA_LANGUAGE` took effect (`/app-config.js` and the startup log showed them).
  - An HTTPS check run from inside the image succeeded, which shows the CA certificates are present.
  - The image is about 3.4 MB.
- A `data_dir` outside `/var/lib/ronda` was tested in a real systemd container (Ubuntu 24.04 arm64): without a drop-in
  the service now refuses to start and the journal says why; `install.sh` warns when the directory exists but is not owned
  by `ronda` (it never changes the ownership of a directory you chose), and creates the directory and the
  `ReadWritePaths` drop-in when it does not exist, after which a site written to it survives a restart. The migration
  scenarios were repeated afterwards with the same results.

## Dashboard

- The dashboard was checked in Chromium at desktop width and at 375 px, in light and dark themes, including creating a
  push monitor through the form.
- The Export/Import dialog was checked in Chromium (desktop and 375 px). The content of the downloaded file was inspected
  from the Blob in the page; the browser's own act of saving the download to disk was **not** exercised.
- The per-site recipient form and detail page were checked in Chromium: ticking recipients, the "at least one"
  validation, saving, and showing the choice again when editing.
- The internationalisation and time-zone work in the dashboard (English/Indonesian text, IANA zones) was verified in
  headless Chrome at desktop width only. Safari, Firefox, screen readers, the 375 px layout and the login screen have not
  been tested with it.

## Telegram

- `telegram-test` sent a message to a private chat on real Telegram, and the daemon sent a DOWN notification for a failing
  site to real Telegram.
- Everything else about the bot was tested only against a fake Telegram server.

## Security review

The daemon was attacked with scripts: 11 API paths times 7 methods without credentials (none returned 200), wrong and
near-miss tokens, 12 path-traversal attempts, `X-Forwarded-For` rotation to evade the login limiter, a 2 MB body and
200,000 nested brackets, overflowing numbers, NUL and CRLF in names and user agents, and **24 disguises of internal
addresses** (`127.1`, `2130706433`, `0x7f000001`, `::ffff:127.0.0.1`, `localtest.me`, `127.0.0.1.nip.io`, ...) for HTTP, TCP
and `dns_server`. A fake internal service received **zero** requests; as a positive control, the same requests with
`allow_private` did reach it, so "zero" really meant "blocked".

Findings that were fixed:

1. Translated IPv6 addresses (NAT64 `64:ff9b::/96`, 6to4 `2002::/16`, site-local, Teredo) slipped past the block. They are
   now judged by the embedded IPv4 address, so legitimate DNS64 networks still work.
2. The session cookie could be overwritten from a neighbouring subdomain ("cookie tossing"); over HTTPS it now uses the
   `__Host-` prefix.
3. The `Permissions-Policy` and `Cross-Origin-Opener-Policy` headers were added.

Vulnerability scan: `govulncheck` found **11 standard-library vulnerabilities** (net/http, HTTP/2, crypto/tls,
crypto/x509, net/textproto) in the Go 1.27.1 toolchain then used for building, all affecting Ronda's code. They were fixed
in Go 1.27.2 and 1.27.3, so `go.mod` pins `toolchain go1.27.3` (downloaded automatically by `go`) and the result is "No
vulnerabilities found". This applies to freshly built binaries; older binaries must be rebuilt.

## Performance

The 500-site test used a real daemon on a laptop (arm64, macOS): 500 sites with full history (2,880 raw points and 2,160
hourly summaries each, a 50 MB `history.json`, like an installation a year old), checked every 10 seconds (about 50
checks per second) for 150 seconds while the API was queried continuously.

| | Before the fixes | After |
|---|---|---|
| Peak memory (RSS) | 312 MB (over `MemoryMax=256M`, so systemd would kill it) | 164 MB |
| Average CPU | 29.5% | 11.3% |
| Site list (`GET /api/sites`), median | 86 ms | 13 ms (p95 15 ms) |
| Detail / history / status page, median | n/a | 1.1 / 1.0 / 0.6 ms |
| Ready after start | n/a | 0.8 s |

Causes and fixes: history saving used to assemble the whole file in memory (it is now written site by site), the list
summary sorted 2,880 values for an unused p95 (now skipped), and uptime walked 2,160 buckets (now stops when it has
enough). The systemd unit sets `GOMEMLIMIT=192MiB` so the garbage collector works harder before reaching `MemoryMax`. All
checks succeeded (500 `up`, no warnings in the log).

These figures are from the developer's own machine, against local targets with no real network latency. On a small VPS
the numbers will differ.

## Not tested

- Bot replies to commands (`/status` and the others) on **real Telegram**; they were tested only against a fake server,
  including `/report`.
- Periodic reports delivered to **real Telegram** (only through a webhook).
- The group-admin restriction in a **real group**.
- RECOVERED notifications on real Telegram.
- Delivery to a real client chat for per-site recipients.
- The installer with systemd on amd64 hardware (amd64 was only run emulated in a container), and on Ubuntu releases other
  than 24.04.
- The dashboard in Safari and Firefox, and with screen readers.
- The i18n and time-zone dashboard work at 375 px width and on the login screen (see above).
- The browser's file-saving action for the Export download.
- The migration from `webmon` on amd64 hardware or on Ubuntu releases other than 24.04, and the warning printed when the old
  unit sets environment variables through a drop-in or an `EnvironmentFile`.
- The Docker image on amd64 (only the arm64 image was run), and the image built from the published registry.
- The `docker` and `release` jobs of the GitHub workflows: their YAML was validated and the Docker commands were run by
  hand, but the workflows themselves had not run when this was written. The first CI run found two problems (a `gofmt`
  version mismatch and `make vuln` using an older Go); both are fixed but not yet seen passing on GitHub.
- `deploy/docker-compose.yml` was validated with `docker compose config`; `docker compose up` was not run.
