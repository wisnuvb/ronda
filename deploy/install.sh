#!/usr/bin/env bash
# ronda installer for Linux with systemd. Safe to run again: an upgrade keeps your config and
# data, and puts the old binary back if the new one does not come up healthy.
#
#   sudo ./install.sh                     install or upgrade
#   ./install.sh --check                  read-only: can this host run ronda?
#   sudo ./install.sh --dry-run           show what would be done, change nothing
#   sudo ./install.sh --set-password      also set the dashboard password (read from stdin or prompted)
#   sudo ./install.sh --reset-password    change the dashboard password of an existing install
#   sudo ./install.sh --uninstall [--purge]
#
# An install still named "webmon" (Ronda's name before its first release) is migrated automatically; see
# "legacy webmon install" below.
#
# Options for a fresh install: --listen ADDR (default 127.0.0.1:9191), --timezone ZONE (an IANA name such as
# Europe/Berlin; default UTC), --lang CODE (en or id; default en), --no-start.
set -euo pipefail

BIN=/usr/local/bin/ronda
CONF_DIR=/etc/ronda
CONF=$CONF_DIR/config.json
DATA_DIR=/var/lib/ronda
SVC_USER=ronda
UNIT=/etc/systemd/system/ronda.service
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

MODE=install DRY=0 PURGE=0 START=1 SET_PASSWORD=0 LISTEN=127.0.0.1:9191 TIMEZONE=UTC LANG_CODE=en

say()  { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() { sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
  case $1 in
    --check) MODE=check ;;
    --dry-run) DRY=1 ;;
    --uninstall) MODE=uninstall ;;
    --purge) PURGE=1 ;;
    --set-password) SET_PASSWORD=1 ;;
    --reset-password) MODE=reset-password ;;
    --no-start) START=0 ;;
    --listen) shift; LISTEN=${1:?--listen needs an address, e.g. 127.0.0.1:9191} ;;
    --timezone) shift; TIMEZONE=${1:?--timezone needs an IANA zone name, e.g. Europe/Berlin} ;;
    --lang) shift; LANG_CODE=${1:?--lang needs a language code: en or id} ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
  shift
done

# These two end up inside the generated JSON, so they may only be plain words; config-check does the real validation.
case $TIMEZONE in *[!A-Za-z0-9_/+-]*|"") die "--timezone must be an IANA zone name such as Europe/Berlin or UTC" ;; esac
case $LANG_CODE in *[!a-z-]*|"") die "--lang must be a language code such as en or id" ;; esac

run() { if [ "$DRY" = 1 ]; then say "  + $*"; else "$@"; fi; }

# write_file PATH MODE OWNER:GROUP < content
write_file() {
  local path=$1 mode=$2 owner=$3 tmp
  if [ "$DRY" = 1 ]; then say "  + write $path ($mode $owner)"; cat >/dev/null; return; fi
  tmp=$(mktemp "$path.XXXXXX")
  cat >"$tmp"
  chmod "$mode" "$tmp"
  chown "$owner" "$tmp"
  mv -f "$tmp" "$path"
}

need_root() { [ "$DRY" = 1 ] || [ "$(id -u)" = 0 ] || die "run as root (sudo ./install.sh)"; }

arch() {
  case $(uname -m) in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) echo unsupported ;;
  esac
}

have_systemd() { command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; }

port_of() { local a=${1##*:}; echo "${a//[^0-9]/}"; }

# ---------------------------------------------------------------- legacy "webmon" install
# Ronda was called webmon before its first release. An install under the old name is migrated, not
# replaced: the old service is stopped and its config and data are COPIED. Only once Ronda is healthy are the
# old service, unit and binary removed; the old config and data directories are left for you to delete. If
# Ronda does not come up healthy, the copies are removed and the old service is started again, so a failed
# migration changes nothing.
LEGACY_BIN=/usr/local/bin/webmon
LEGACY_CONF_DIR=/etc/webmon
LEGACY_DATA_DIR=/var/lib/webmon
LEGACY_UNIT=/etc/systemd/system/webmon.service
LEGACY_USER=webmon
DROPIN_DIR=/etc/systemd/system/ronda.service.d
MIGRATING=0 LEGACY_WAS_ACTIVE=0 MIGRATED_DATA=""

legacy_found() { [ -f "$LEGACY_CONF_DIR/config.json" ] && { [ -f "$LEGACY_UNIT" ] || [ -x "$LEGACY_BIN" ]; }; }
legacy_active() { have_systemd && systemctl is-active --quiet webmon 2>/dev/null; }
dir_empty() { [ ! -d "$1" ] || [ -z "$(ls -A "$1" 2>/dev/null)" ]; }
legacy_data_dir() { sed -n 's/.*"data_dir"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$LEGACY_CONF_DIR/config.json" | head -1; }

# Decides whether this run migrates. Must run before the Ronda directories are created.
plan_migration() {
  legacy_found || return 0
  if [ -f "$CONF" ] || ! dir_empty "$DATA_DIR"; then
    warn "an install named webmon was found, but Ronda already has a config or data ($CONF, $DATA_DIR): not migrating."
    warn "stop and remove the old service yourself (systemctl disable --now webmon); it may hold the same port."
    return 0
  fi
  MIGRATING=1
}

migrate_legacy() {
  local src
  say "  found an install named webmon (Ronda's former name): migrating it; its files stay until Ronda is healthy"
  if legacy_active; then LEGACY_WAS_ACTIVE=1; run systemctl stop webmon; fi
  src=$(legacy_data_dir); src=${src:-$LEGACY_DATA_DIR}
  run cp -a "$LEGACY_CONF_DIR/." "$CONF_DIR/"
  run chown -R "root:$SVC_USER" "$CONF_DIR"
  run chmod 0640 "$CONF"
  if [ "$src" = "$LEGACY_DATA_DIR" ]; then
    [ -d "$src" ] && run cp -a "$src/." "$DATA_DIR/"
    run chown -R "$SVC_USER:$SVC_USER" "$DATA_DIR"
    run sed -i -E "s|(\"data_dir\"[[:space:]]*:[[:space:]]*\")$LEGACY_DATA_DIR/?(\")|\\1$DATA_DIR\\2|" "$CONF"
  else
    MIGRATED_DATA=$src
    run chown -R "$SVC_USER:$SVC_USER" "$src"
    # The unit only lets the service write to its own state directory (ProtectSystem=strict). Without this
    # drop-in the service would start, look healthy, and fail every write to a data_dir somewhere else.
    run install -d -m 0755 "$DROPIN_DIR"
    write_file "$DROPIN_DIR/data-dir.conf" 0644 root:root <<DROPIN
[Service]
ReadWritePaths=$src
DROPIN
    say "  data_dir is $src, not the default: it stays there, now belongs to the $SVC_USER user, and the service may write to it ($DROPIN_DIR/data-dir.conf)"
  fi
  # webmon always ran in WIB and in Indonesian. A migrated install keeps behaving that way: without these keys it
  # would fall back to UTC and English, and the clock times of its maintenance windows and reports would shift.
  local kept="" kv k v
  for kv in "timezone:Asia/Jakarta" "language:id"; do
    k=${kv%%:*}; v=${kv#*:}
    if ! grep -q "\"$k\"" "$CONF" 2>/dev/null; then
      run sed -i "0,/{/s|{|{\n  \"$k\": \"$v\",|" "$CONF"
      kept="$kept $k=$v"
    fi
  done
  [ -z "$kept" ] || say "  kept the old behaviour by adding to the config:$kept (change them in $CONF if you want)"
  if [ -d "$LEGACY_UNIT.d" ] || grep -qE '^(EnvironmentFile=|Environment=.*WEBMON_)' "$LEGACY_UNIT" 2>/dev/null; then
    warn "the old unit sets environment variables (a drop-in or EnvironmentFile). They are not carried over; put them in"
    warn "a drop-in for ronda.service by hand (the WEBMON_* names still work, deprecated; RONDA_* is preferred)."
  fi
}

# Called when Ronda failed to start: put everything back as it was.
abort_legacy() {
  [ "$MIGRATING" = 1 ] || return 0
  warn "undoing the migration: your webmon install is put back as it was."
  systemctl disable --now ronda >/dev/null 2>&1 || true
  rm -f "$UNIT" "$BIN" "$BIN.new" "$BIN.prev"
  rm -rf "$DROPIN_DIR"
  systemctl daemon-reload || true
  rm -rf "$CONF_DIR"
  if [ -n "$MIGRATED_DATA" ]; then chown -R "$LEGACY_USER:$LEGACY_USER" "$MIGRATED_DATA" || true; else rm -rf "$DATA_DIR"; fi
  if [ "$LEGACY_WAS_ACTIVE" = 1 ]; then systemctl start webmon || warn "could not start webmon again; try: systemctl start webmon"; fi
}

# Called when Ronda is healthy: retire the old service, keep the old files.
finish_legacy() {
  [ "$MIGRATING" = 1 ] || return 0
  systemctl disable webmon >/dev/null 2>&1 || true
  rm -f "$LEGACY_UNIT" "$LEGACY_BIN" "$LEGACY_BIN.prev"
  systemctl daemon-reload
  say "  migrated from webmon: the old service, unit and binary were removed"
  if [ -n "$MIGRATED_DATA" ]; then
    say "  left for you to delete once you have checked Ronda: $LEGACY_CONF_DIR"
  else
    say "  left for you to delete once you have checked Ronda: $LEGACY_CONF_DIR and $LEGACY_DATA_DIR"
  fi
  say "  Dashboard sessions ended once (the session cookie is now called ronda_session): log in again."
}

# ---------------------------------------------------------------- check
do_check() {
  local fails=0 a
  ok()   { say "  ok   $*"; }
  bad()  { say "  FAIL $*"; fails=$((fails + 1)); }
  note() { say "  info $*"; }

  say "Checking this host (nothing is changed):"
  [ "$(uname -s)" = Linux ] && ok "Linux" || bad "not Linux ($(uname -s))"
  a=$(arch)
  [ "$a" != unsupported ] && ok "architecture $(uname -m) ($a)" || bad "architecture $(uname -m) is not supported (only amd64 and arm64)"
  have_systemd && ok "systemd is running" || bad "systemd is not running; this installer needs it"
  [ -f "$HERE/ronda-linux-$a" ] && ok "binary ronda-linux-$a found in $HERE" || bad "binary ronda-linux-$a not found in $HERE"
  [ -f "$HERE/ronda.service" ] && ok "ronda.service found" || bad "ronda.service not found in $HERE"
  if legacy_found; then note "found an install named webmon (Ronda's former name): installing will migrate it and keep its files until Ronda is healthy"; fi
  if command -v curl >/dev/null 2>&1; then ok "curl is available (for the health check)"; else note "curl is missing; the health check will use the systemd status only"; fi
  if command -v ss >/dev/null 2>&1 && ss -ltn 2>/dev/null | awk '{print $4}' | grep -q ":$(port_of "$LISTEN")\$"; then
    if [ -x "$BIN" ] && systemctl is-active --quiet ronda 2>/dev/null; then note "port $(port_of "$LISTEN") is used by the ronda already installed (this is an upgrade)"
    elif legacy_found && legacy_active; then note "port $(port_of "$LISTEN") is used by the old webmon service; the migration stops it first"
    else bad "port $(port_of "$LISTEN") is already used by another process; pick another with --listen"; fi
  else ok "port $(port_of "$LISTEN") is free"; fi
  [ -x "$BIN" ] && note "already installed: $("$BIN" version 2>/dev/null || echo ?) (this will be an upgrade)" || note "not installed yet"
  [ "$(id -u)" = 0 ] && ok "running as root" || note "not root; installing later needs sudo"
  [ "$fails" = 0 ] && say "Ready." || say "$fails problem(s) must be fixed first."
  [ "$fails" = 0 ]
}

# ---------------------------------------------------------------- password
read_password() {
  local pw
  if [ -t 0 ]; then read -rsp "Dashboard password (at least 12 characters): " pw; echo >&2; else read -r pw; fi
  printf '%s' "$pw"
}

hash_password() { # $1 = binary
  local pw; pw=$(read_password)
  [ -n "$pw" ] || die "the password is empty"
  printf '%s\n' "$pw" | "$1" hash-password
}

do_reset_password() {
  need_root
  [ -f "$CONF" ] || die "$CONF does not exist; install first"
  [ -x "$BIN" ] || die "$BIN does not exist; install first"
  local h bak; h=$(hash_password "$BIN")
  bak=$(mktemp); cp -p "$CONF" "$bak"
  if ! grep -qE '"password_hash"[[:space:]]*:' "$CONF"; then rm -f "$bak"; die "there is no \"password_hash\" in $CONF; add it by hand in the \"auth\" section"; fi
  sed -E "s|(\"password_hash\"[[:space:]]*:[[:space:]]*\")[^\"]*(\")|\\1$h\\2|" "$CONF" >"$CONF.new"
  cat "$CONF.new" >"$CONF" && rm -f "$CONF.new"
  if ! "$BIN" config-check -config "$CONF" >/dev/null; then cat "$bak" >"$CONF"; rm -f "$bak"; die "the config is invalid after the change; restored as it was"; fi
  rm -f "$bak"
  systemctl restart ronda
  say "The dashboard password was changed and ronda restarted. All earlier login sessions have ended."
  say "This also replaces a password that was changed from the dashboard (a changed config always wins)."
}

# ---------------------------------------------------------------- uninstall
do_uninstall() {
  need_root
  say "Uninstalling ronda:"
  have_systemd && { run systemctl disable --now ronda 2>/dev/null || true; }
  run rm -f "$UNIT" "$BIN" "$BIN.prev"
  have_systemd && run systemctl daemon-reload
  if [ "$PURGE" = 1 ]; then
    run rm -rf "$CONF_DIR" "$DATA_DIR"
    id "$SVC_USER" >/dev/null 2>&1 && run userdel "$SVC_USER" 2>/dev/null || true
    say "Config, data and the $SVC_USER user were removed too."
  else
    say "Config ($CONF_DIR) and data ($DATA_DIR) were left in place. Add --purge to remove them too."
  fi
}

# ---------------------------------------------------------------- install
healthy() { # $1 = host:port from the config
  local addr=$1 host port
  host=${addr%:*}; port=${addr##*:}
  case $host in ""|"0.0.0.0"|"[::]"|"::") host=127.0.0.1 ;; esac
  command -v curl >/dev/null 2>&1 || { sleep 3; systemctl is-active --quiet ronda; return; }
  local i
  for i in $(seq 1 20); do
    curl -fsS --max-time 2 "http://$host:$port/healthz" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

rollback() {
  warn "the new ronda is not healthy."
  if [ "$MIGRATING" = 1 ]; then
    warn "see why with: journalctl -u ronda -n 40 --no-pager (kept in the journal after the undo)"
    abort_legacy
  elif [ -f "$BIN.prev" ]; then
    warn "putting the old binary back."
    mv -f "$BIN.prev" "$BIN"
    systemctl restart ronda || true
  else
    warn "this was a first install; see why with: journalctl -u ronda -n 40 --no-pager"
  fi
}

do_install() {
  local a new tok hash="" upgrade=0 addr
  need_root
  [ "$(uname -s)" = Linux ] || die "Linux only"
  have_systemd || die "systemd is not running"
  a=$(arch); [ "$a" != unsupported ] || die "architecture $(uname -m) is not supported"
  new=$HERE/ronda-linux-$a
  [ -f "$new" ] || die "$new not found; run this from the extracted release folder"
  [ -f "$HERE/ronda.service" ] || die "$HERE/ronda.service not found"
  [ -x "$BIN" ] && upgrade=1
  plan_migration

  say "$([ "$upgrade" = 1 ] && echo 'Upgrading' || echo 'Installing') ronda ($a):"

  getent group "$SVC_USER" >/dev/null || run groupadd --system "$SVC_USER"
  id "$SVC_USER" >/dev/null 2>&1 || run useradd --system --gid "$SVC_USER" --home-dir "$DATA_DIR" --no-create-home \
    --shell "$(command -v nologin || echo /usr/sbin/nologin)" "$SVC_USER"
  run install -d -m 0750 -o root -g "$SVC_USER" "$CONF_DIR"
  run install -d -m 0750 -o "$SVC_USER" -g "$SVC_USER" "$DATA_DIR"

  # The new binary is staged first so a bad download or a bad config never touches the running one.
  run install -m 0755 "$new" "$BIN.new"
  if [ "$MIGRATING" = 1 ]; then migrate_legacy; fi
  if [ ! -f "$CONF" ] && [ "$MIGRATING" = 0 ]; then
    if [ "$DRY" = 1 ]; then tok="<random token>"; else tok=$("$BIN.new" gen-token); fi
    if [ "$SET_PASSWORD" = 1 ] && [ "$DRY" = 0 ]; then hash=$(hash_password "$BIN.new"); fi
    write_file "$CONF" 0640 "root:$SVC_USER" <<CONFIG
{
  "listen": "$LISTEN",
  "data_dir": "$DATA_DIR",
  "timezone": "$TIMEZONE",
  "language": "$LANG_CODE",
  "trust_proxy": false,
  "api": { "token": "$tok" },
  "auth": { "user": "admin", "password_hash": "$hash" },
  "notify": { "telegram_token": "", "telegram_chat_id": "", "webhook_url": "", "telegram_commands": false },
  "heartbeat_url": ""
}
CONFIG
    say "  new config $([ "$DRY" = 1 ] && echo 'will be created' || echo 'created') at $CONF (a random API token is filled in)"
  elif [ "$MIGRATING" = 1 ]; then
    say "  config $([ "$DRY" = 1 ] && echo 'will be copied' || echo 'copied') from $LEGACY_CONF_DIR to $CONF"
  else
    say "  existing config ($CONF) kept"
    [ "$SET_PASSWORD" = 1 ] && warn "--set-password only applies when the config is created; for an existing one use --reset-password"
  fi

  if [ "$DRY" = 0 ]; then
    "$BIN.new" config-check -config "$CONF" >/dev/null || { rm -f "$BIN.new"; abort_legacy; die "the config is invalid; fix $CONF (run: $new config-check -config $CONF). The install was cancelled and nothing was changed."; }
    [ "$upgrade" = 1 ] && cp -p "$BIN" "$BIN.prev"
  fi
  run mv -f "$BIN.new" "$BIN"
  run install -m 0644 "$HERE/ronda.service" "$UNIT"
  run systemctl daemon-reload
  run systemctl enable ronda
  if [ "$START" = 0 ]; then say "  --no-start: the service was not started"; return; fi
  run systemctl restart ronda

  if [ "$DRY" = 1 ]; then say "(dry run finished, nothing was changed)"; return; fi
  addr=$("$BIN" config-check -config "$CONF" | sed -n 's/.*listen=\([^ ]*\).*/\1/p')
  if healthy "$addr"; then
    rm -f "$BIN.prev"
    finish_legacy
    say ""
    say "ronda is running: $("$BIN" version)"
    say "  address  : http://$addr (from this server only)"
    say "  config   : $CONF"
    say "  log      : journalctl -u ronda -f"
    summary "$addr"
  else
    rollback
    exit 1
  fi
}

summary() {
  local info n=0; info=$("$BIN" config-check -config "$CONF")
  say ""
  say "Next steps:"
  case $info in *"dashboard=no"*)
    n=$((n + 1)); say "  $n. Enable the dashboard login: sudo $0 --reset-password   (or fill in auth.password_hash in the config)" ;;
  esac
  case $info in *"telegram=no"*)
    n=$((n + 1)); say "  $n. Fill in notify.telegram_token and notify.telegram_chat_id in $CONF, then: sudo systemctl restart ronda" ;;
  esac
  case $info in *"heartbeat=no"*)
    n=$((n + 1)); say "  $n. Fill in heartbeat_url (e.g. from healthchecks.io) so you find out if ronda itself dies" ;;
  esac
  [ "$n" = 0 ] && say "  (everything is set up)"
  say "  To open the dashboard from your computer:  ssh -N -L 9191:$1 user@server   then browse to http://127.0.0.1:9191"
}

case $MODE in
  check) do_check ;;
  install) if [ "$DRY" = 1 ]; then say "Dry run: nothing is changed."; fi; do_install ;;
  uninstall) do_uninstall ;;
  reset-password) do_reset_password ;;
esac
