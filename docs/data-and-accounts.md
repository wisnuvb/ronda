# Data, export/import and the dashboard password

Reference for what Ronda stores, how to move site settings between servers, and how the dashboard password is kept. The README has the short version.

[Back to the README](../README.md)

## Incident archive

Every down, recovery and certificate event is appended to `events.jsonl` in the data directory (one line per event,
never rewritten), and the **Incidents** list is built from it, so history is not cut off at the last 300 events (one
server outage with dozens of sites already produces hundreds). It keeps `event_retention_days` days (default 365, 7 to
3650); older entries are dropped at startup. An `events.json` from an older version is imported once automatically. The
Incidents page loads 50 at a time with a **Load older incidents** button; API: `GET /api/incidents?limit=&before=`.

## Export and import

Move settings to a new server, back them up, or restore after an accidental delete. Dashboard: **Export** and
**Import** buttons on the Sites page. CLI and API:

```bash
ronda site export -o sites.json            # GET  /api/export
ronda site import -dry-run sites.json      # POST /api/import?dry_run=1  (only shows the result)
ronda site import sites.json               # POST /api/import
```

- The file is JSON with **settings only**: name, address, interval, keyword, weekly maintenance windows, and so on. No
  uptime history, incidents or ids; the target server makes its own ids.
- **Import always shows the result first** (dashboard: a preview before the Import button is enabled). Each site is
  validated like one added by hand; invalid ones are reported by position and **do not stop** the others.
- **Safe to repeat:** sites whose address already exists (for push monitors: whose name exists) are skipped, so importing the
  same file twice changes nothing. A file holds at most 5,000 sites, and a server is still limited to 500.
- A running one-off maintenance window is not included, and a file cannot force ids or creation times.
- **Push-monitor ping tokens are secret**, so a normal export omits them: on the new server every push monitor gets a
  new ping address to install in its job. To move **without touching cron**, export with `-tokens` (dashboard: tick
  "Include ping tokens"); that file must then be guarded like a password (the CLI writes it with mode `600`). Tokens that
  are malformed or already used by another site are replaced with new ones.

## Changing the password from the dashboard

Use the lock button in the top bar: enter the current password and the new one (at least 12 characters). Only a
**login session** may do this (not the API token); a wrong current password counts toward the login rate limit, and
all other devices are **signed out**; the session that made the change stays in.

The new hash is stored in `auth.json` in the data directory (mode `600`), not in the config, because the service must
not write its own config. That file is **bound to the config hash it replaced**: as soon as `auth.password_hash` in the
config changes (for example with `install.sh --reset-password`), the file stops applying, so an admin with server
access always has the last word. Deleting `auth.json` restores the config's password.
