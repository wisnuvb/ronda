# Using Ronda's packages in your own program

Ronda is one program (single tenant, files on disk), but its checking engine is built from packages that another Go
program can import: for example a service that runs checks for many accounts on top of a database.

[Back to the README](../README.md)

## Stability

The module is `github.com/wisnuvb/ronda`, still **v0.x**: the exported API may change between minor versions, so pin a
version and read the [CHANGELOG](../CHANGELOG.md) before upgrading. Only the packages in the table are meant to be
imported; everything under `internal/` can change freely (and Go refuses to import it from another module anyway).

| Package | Use it for |
|---|---|
| `model` | `Site`, `Result`, `Event`, `SiteState`; the process-wide time zone; `InvalidError` and `ErrNotFound` |
| `sitespec` | `Normalize`: defaults and validation of a site, the same rules the single-tenant registry applies |
| `check` | `New()` / `NewStrict()` checkers, `Guard`, `GuardedClient` |
| `monitor` | the scheduler and state machine (`monitor.New(Options)`) |
| `history`, `eventlog` | in-memory rollups with file persistence; the append-only incident archive |
| `notify`, `report` | message formatting, delivery queue and digests; daily and weekly reports |
| `schedule`, `i18n` | weekly maintenance windows; message catalogs |

## Plugging in your own storage

`monitor.Monitor` reaches storage only through four small interfaces, defined in `monitor/ports.go`:

| Interface | Option | If left nil |
|---|---|---|
| `SiteSource` (`List`, `Get`, `ByToken`, `Changed`) | `Sites` (required) | |
| `History` | `History` | no history |
| `EventLog` | `Events` | `events.jsonl` in `DataDir`, closed by `Monitor.Close` |
| `StateStore` (`Load`, `Save`) | `State` | `state.json` in `DataDir` |

An `EventLog` you pass in is yours to close. A `History` must be a nil interface, not a typed nil pointer, to turn history
off. To run one monitor per shard of a large site list, give each its own `SiteSource` that returns only that shard.

## Probing addresses that strangers choose

`check.New()` blocks loopback, private and link-local addresses unless a site sets `allow_private`: right for a monitor
you run yourself, where watching a LAN service is normal. A service whose users are strangers should use
**`check.NewStrict()`**, which ignores `allow_private` on every kind of check (and never builds the permissive client).
For any other outgoing request whose destination a user can choose, such as a webhook, use `check.GuardedClient` (or
`check.Guard` as a dialer's `Control`), and set it as `notify.Webhook{Client: ...}`. The guard judges the address after
DNS, so redirects and DNS rebinding are covered. Also run the probes in a network that cannot reach your own services:
the guard is one wall, not the only one.

## Known limits for multi-account use

- **Language and time zone are process-wide** (`i18n.SetLang`, `model.SetTimezone`): messages and clock times are
  produced in the one configured language and zone. A service with accounts in several languages or zones cannot yet
  format per account.
- `notify.Telegram` sends with one bot token and sleeps through rate limits (429), which suits one installation; a service
  that sends for many accounts should bring its own delivery and reuse the formatting (`notify.FormatHTML`).
- `notify.Queue` groups digests by recipient name only; do not share one queue between accounts.
