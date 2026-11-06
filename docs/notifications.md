# Notifications in detail

Reference for how Ronda sends alerts, reminders, reports and warnings, and how it routes them to different people. The README has the short version.

[Back to the README](../README.md)

## Notification batching

When a server dies, dozens of sites go down at once and Telegram would be flooded. Alerts are held until **8 seconds
pass with nothing new** (extended while events keep arriving, at most 45 seconds or 6 gaps), then **3 or more
consecutive down (or recovered) alerts become one message**: a list of sites with their causes, and a note that the
cause is probably shared (server, network, DNS or CDN). Fewer than 3 are still sent one by one. Only consecutive runs
with the same status are merged, so a "down" and then "recovered" for one site are never reordered; certificate
warnings are never merged. When Ronda stops, held alerts are sent immediately.

Tune with `notify.batch_sec` (default 8; **0 turns holding and merging off**) and `notify.digest_min` (default 3). The
price: a single alert is delayed by up to `batch_sec` seconds. A webhook receives one JSON document containing all
events.

## "Still down" reminders

One DOWN message is easy to miss, and a site can stay down for days unnoticed. While a site **stays down**, Ronda sends
reminders **1 hour, 6 hours and 24 hours** after it went down, then **once a day**:

> ⏰ **STILL DOWN** — Shop · Current cause: … · Down for 6 h 2 min · since 2026-11-03 09:30:00 CET

The cause is the **latest check result**, so if the reason changes (say from "connection refused" to "timeout") you see
it. Set with `notify.remind_after_min` (minutes, 5 to 43200); `[]` turns it off:

```json
"notify": { "remind_after_min": [30, 240, 1440] }
```

- **One message per threshold**, never repeated. If Ronda was off or a site is checked so rarely that two thresholds
  passed at once, only **one** reminder is sent. The number already sent is stored, so a **restart does not repeat reminders**.
- No reminders while the monitor's own network is down, while a site is paused or in maintenance, or once the site
  answers again. After recovering and failing again, the count starts over.
- Several sites reminding at once (a server down for hours) are merged into **one message**, like the DOWN digest.
- Reminders are **not recorded as new incidents**: one outage stays one incident in the Incidents page and archive.
- For push monitors a reminder comes at the next scheduled evaluation after the threshold, so it can be late by up to
  that job's `interval_sec`.

## Scheduled reports

Alerts say when something breaks; a report answers "how was this week overall?" without opening the dashboard. **Off by
default.** Switch on with a send time (in `timezone`):

```json
"notify": { "report_daily": "08:00", "report_weekly": "08:00", "report_weekly_day": 1 }
```

`report_weekly_day`: 0 = Sunday ... 6 = Saturday (default 1, Monday). Leave a time empty to turn that report off. Ronda
refuses to start if reports are enabled with neither `telegram_token` nor `webhook_url`. Only the sections that have
content appear:

> 📊 **Weekly report · 29 Oct – 5 Nov 2026**
> 12 sites · 🟢 10 healthy · 🔴 1 down · ⏸ 1 paused
> Average uptime: **99.87%**
> Incidents: **3**, total downtime 1 h 20 min · Longest: Shop — 52 min 0 s
> **Still down now** · **Lowest uptime** · **Certificates expiring soon** (next 30 days) · **Slowest** (24 h average)

How the numbers are chosen:

- **Paused sites are not counted** (the figures would say nothing about their performance). Average uptime never shows
  100% if anything failed, same as the dashboard.
- **Only the part of an incident inside the period counts.** A site that has been down for 10 days is reported as a full
  week, not 10 days, in the weekly report.
- Each list is capped at 5 lines (3 for the slowest) with an "and N more" note, so one message never passes Telegram's
  4,096-character limit, even with 50 sites and 200-character names.
- **On time, once.** The last slot served is stored in `reports.json` in the data directory, so a restart does not repeat a
  report. If Ronda was down exactly at the scheduled time and comes back within **3 hours**, the report is sent late;
  after that it is skipped (a daily report that arrives at midnight is just noise). When a report is newly enabled it
  does not immediately send the report for a time that already passed today.
- Recipients are the same as for alerts (all Telegram chats, plus the webhook, which receives
  `{"report": true, "text": ...}`).
- Reports **bypass the alert queue**, so they are not held by bulk-alert batching.

At any time, `/report` in the bot produces the same report on the spot (`daily` or `weekly`).

## Per-site recipients

By default all notifications go to `telegram_chat_id`. If you monitor sites for several clients, each should hear only
about their own. Define named **recipients** in the config (name to one or more chat ids, comma-separated):

```json
"notify": {
  "telegram_token": "…", "telegram_chat_id": "111",
  "recipients": { "client-a": "-1001234567890", "team": "222, 333" }
}
```

then choose per site in the form ("Send notifications to"), with the CLI (`site add -notify-to client-a ...`), or the API
(`"notify_to": ["client-a"]`). The rules:

- **No `notify_to`:** goes to the main chat, as before. The special name **`default`** means the main chat;
  `["client-a","default"]` informs the client and the main chat.
- It applies to **all** notifications of that site: down, recovered, "still down" reminders and certificate warnings.
- **Bulk digests never mix recipients.** If client A's and client B's sites fail together, each client gets its own
  digest (counted separately, so a client with 2 sites still gets them one by one) and never sees another client's site names.
- **Periodic reports:** the main chat gets the report of **all** sites; each recipient that owns sites also gets a report of
  **only its sites**, titled with its name. A recipient with no sites gets no empty report.
- **A recipient removed from the config does not silence a site:** if all of a site's recipients are gone, the main chat is
  notified (and it is logged). Other changes to that site still work; only changing the route is validated. An
  unknown name is rejected with the valid names listed.
- Recipient names: lowercase letters, digits, `-` and `_` (max 32); at most 50 recipients; at most 10 per site. Chat ids are
  **never** shown in the dashboard or API, only names.
- **The bot does not serve recipient chats:** only chats in `telegram_chat_id` may give commands, so clients cannot change
  sites or see other clients' sites through `/list`. The bot must already be added to the client's chat or group and
  allowed to write.
- A webhook still receives **all** events (each carries `to`), because the webhook is your own channel.
- Export/import carries `notify_to`. Importing into a server that lacks that recipient rejects the site with a clear message.

## Certificate warnings

A TLS certificate close to expiry is announced **once** as it crosses 14, 7, 3 and 1 days (change with
`cert_warn_days`, `[]` to turn off). Announced thresholds are stored, so a restart does not repeat a message; when
the certificate is renewed the warning arms again for the next period. A certificate that has *already* expired is
handled as DOWN (`tls_expired`).
