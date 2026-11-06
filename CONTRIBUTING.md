# Contributing to Ronda

Thanks for taking an interest. Ronda is a small project maintained by one person, so please read this first; it keeps
changes easy to review.

## Before you start

- **Open an issue before a large change** (a new check type, a new notification channel, a new dependency). A small fix
  does not need one.
- Ronda is deliberately **one static binary using only the Go standard library**. A pull request that adds a dependency
  will almost certainly be declined; if you think one is needed, make the case in an issue first.
- Maintenance is best-effort: replies can take a while.

## Building and testing

You need Go (see `go.mod`; the exact toolchain is pinned there and downloaded automatically).

```bash
make demo        # try the dashboard with synthetic data, no login: http://127.0.0.1:9191
make test        # go vet + go test -race ./...  (must pass)
make vuln        # govulncheck, needs network
```

`gofmt -l .` must print nothing. CI runs the same checks.

## Conventions

- **Messages are translated through `internal/i18n`.** The English text is the key: `i18n.T("Cause: %s", why)`. When you
  add or change a user-facing message, add the Indonesian text in the matching `internal/i18n/id_*.go` file (the
  completeness test fails otherwise). Dashboard text uses `t('English text {name}', {name})` with the catalog in
  `web/static/i18n-id.js`. Log lines and CLI output are plain English and are not translated.
- **Times and days follow the configured time zone** (`timezone`), including daylight saving. Never compute a day as
  24 h of arithmetic or format a clock time with a fixed offset; use `model.Loc()`, `model.Midnight`, `model.ClockOn` and
  `model.Stamp`. `dst_test.go` files show how to test it.
- **Add a test with a fix or a feature**, written to fail without your change. The existing tests are a good guide to the
  style: real servers on loopback, an injectable clock, no sleeping.
- Keep the dashboard accessible: status is never conveyed by colour alone, controls work with the keyboard, and new text
  is translated.
- Do not commit secrets. `deploy/config.example.json` is checked by a test to make sure it contains none.

## Pull requests

Keep them focused: one change, with a description of what and why. Mention how you tested it, and anything you could not
test (for example a browser or an OS you do not have).

## Adding a language

English is the source language; Indonesian is the first translation. To add another, register a catalog in
`internal/i18n/` (the Go completeness test then lists every server message that still needs a translation) and add a
matching catalog to `web/static/` (copy `i18n-id.js`, register it in `i18n.js`, and extend the checks in
`web/i18n_test.go`, which today only cover Indonesian). See *Development* in the README.

## Security problems

Do not open a public issue; see [SECURITY.md](SECURITY.md).
