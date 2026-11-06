# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- **Docker image** (`Dockerfile`, `deploy/docker-compose.yml`): `FROM scratch`, about 3.5 MB, unprivileged user, works
  with a read-only root filesystem. The release workflow publishes it for `linux/amd64` and `linux/arm64` to
  `ghcr.io/wisnuvb/ronda`.
- `ronda healthcheck`: exit status 0 if a running instance answers `/healthz` (used by the image's health check).
- Environment overrides `RONDA_LISTEN`, `RONDA_TIMEZONE` and `RONDA_LANGUAGE`.

### Fixed

- Ronda refuses to start, with a message that says why, when `data_dir` is not writable. Before, it started, looked
  healthy and lost every change.
- `install.sh` adds a systemd drop-in (`ReadWritePaths`) when the config's `data_dir` is not `/var/lib/ronda`, for any
  install and not only a migrated one.
- `make vuln` and CI use the Go toolchain pinned in `go.mod`; with an older Go, govulncheck failed to read the standard
  library.

## [0.1.0] - First public release

The first public release. It renames the project from its working name **webmon** to **Ronda**, and
internationalises it: English is the default language, Indonesian is included, and time zones are IANA names with
daylight saving.

### Added

- **Checks:** HTTP(S) with diagnosis of the cause, status-code and keyword matching and TLS handling; TCP port checks;
  DNS record checks (A, AAAA, MX, TXT, NS, optional DNS server and expected answer); **push monitors** for cron jobs and
  backups with a secret ping address and a grace period.
- **State machine** with consecutive-failure thresholds, recovery confirmation, and a "monitor network is down" test
  (`control_addrs`) that suppresses false alerts.
- **Notifications:** Telegram and webhook; bulk-outage digests (`batch_sec`, `digest_min`); "still down" reminders
  (`remind_after_min`); certificate-expiry warnings (`cert_warn_days`); daily and weekly reports; per-site recipients
  so several clients each hear only about their own sites.
- **Telegram bot** (`/status /list /report /check /pause /resume /maintenance /endmaint /add /remove`), with admin
  restriction in groups. Indonesian command names remain as aliases.
- **Dashboard** embedded in the binary: site list, site detail with uptime (24 h to 90 days) and response-time charts,
  incident list, add/edit dialog with a "test first" button, light and dark themes, accessibility work, password
  login with a rate limiter, and password change from the dashboard.
- **Public status page** (off by default) that never exposes URLs or technical details.
- **Maintenance windows:** one-off and weekly schedules.
- **Permanent incident archive** (`events.jsonl`) with paging.
- **Export/import** of site settings (CLI, API, dashboard), safe to repeat.
- **Heartbeat** to an outside service so you learn if Ronda itself dies.
- **HTTP API** and **CLI** (`ronda site ...`, `ronda check`, `ronda config-check`, ...).
- **Installer** (`deploy/install.sh`) for Linux with systemd: check, dry run, upgrade with automatic rollback, password
  reset, uninstall, and a hardened `ronda.service`. Install layout: `/usr/local/bin/ronda`, `/etc/ronda/config.json`,
  `/var/lib/ronda`, system user and group `ronda`. Options `--listen`, `--timezone`, `--lang`, `--no-start`.
- `make demo`: the dashboard with synthetic data and no login.
- **Internationalisation:** config keys `language` (`en` default, or `id`) and `timezone` (IANA name, default `UTC`;
  `Local`, `WIB` and `GMT+7` are rejected). One language and one zone per instance; they affect Telegram and webhook
  messages, reports, the bot, API error text and the dashboard. Daylight saving is followed: a maintenance window or
  report at `02:00` stays at 02:00 wall-clock, and days are 23 or 25 hours long on change days. CLI output and help are
  English only.
- Dashboard routes `#/`, `#/sites/ID` and `#/incidents`.
- Documentation: English and Indonesian READMEs, and `docs/` with notification details, data handling and a testing
  record.

### Changed

- **Renamed from webmon to Ronda.** Binary, config directory, data directory, service and system user now
  use `ronda`. Environment variables are `RONDA_API_TOKEN`, `RONDA_HEARTBEAT_URL`, `RONDA_PASSWORD_HASH`,
  `RONDA_TELEGRAM_TOKEN`, `RONDA_TELEGRAM_CHAT_ID` and `RONDA_WEBHOOK_URL`; the CLI reads `RONDA_API_TOKEN` and
  `RONDA_API`. The session cookie is now `ronda_session` (`__Host-ronda_session` over HTTPS), so dashboard sessions end
  once on upgrade.
- Bot command names are English first; the Indonesian names (`/laporan /cek /jeda /lanjut /pemeliharaan /akhiri
  /tambah /hapus /daftar`) remain as aliases. Report kinds are `daily` and `weekly`.
- The default User-Agent of checks is `ronda/1 (uptime-check)`.
- Dashboard routes were renamed from `#/situs/ID` and `#/insiden`; the old addresses still open.

### Deprecated

- The old `WEBMON_*` environment variable names are still accepted as a fallback (a warning is logged) and will be
  removed in a future release.

### Upgrading from webmon

`sudo ./install.sh` detects an existing `webmon` install and migrates it automatically:

1. it stops the old `webmon` service;
2. it **copies** `/etc/webmon` and `/var/lib/webmon` to `/etc/ronda` and `/var/lib/ronda` (rewriting `data_dir`);
3. it installs and starts `ronda`;
4. if Ronda is healthy, the old service is disabled and its unit and binary are removed; the old config and data
   directories are left in place for you to delete after checking;
5. if Ronda is not healthy, the old service is started again and nothing of the old install was changed.

webmon always ran in WIB and in Indonesian. If the migrated config has no `timezone` or `language` key, the installer
adds `"timezone": "Asia/Jakarta"` and `"language": "id"` to `/etc/ronda/config.json` and prints what it added; without
that the instance would fall back to UTC and English and the clock times of its maintenance windows and reports would
shift by 7 hours. Both keys can be changed afterwards; changing `timezone` later shifts what stored `"HH:MM"` window and
report times mean (history and incidents are stored in UTC and are unaffected).

## History before 0.1.0

Ronda was developed in ten internal stages under its working name, webmon. They were never released separately.

- **Stages 1 to 2:** HTTP(S) checker with diagnosis, state machine, Telegram alerts, API and CLI, web dashboard with
   login, 90-day uptime history, charts, incident list.
- **Stage 3:** certificate warnings, maintenance windows, Telegram bot, heartbeat, installer and systemd unit.
- **Stage 4:** TCP and DNS checks, bulk-notification digests, weekly maintenance schedules.
- **Stage 5:** permanent incident archive, password change from the dashboard, public status page.
- **Stage 6:** push monitors for cron and backup jobs, a 500-site load test, and a security review.
- **Stage 7:** bot command restriction in groups, vulnerability scanning (`make vuln`), and a guard against secrets
   in example files.
- **Stage 8:** "still down" reminders and site export/import.
- **Stage 9:** daily and weekly reports and the bot's report command.
- **Stage 10:** per-site recipients (each client hears only about their own sites, reports included).

[Unreleased]: https://github.com/wisnuvb/ronda/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/wisnuvb/ronda/releases/tag/v0.1.0
