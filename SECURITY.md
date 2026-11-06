# Security policy

Ronda is a monitor that makes outgoing requests to addresses you give it and serves a dashboard with a login, so
security reports are welcome and taken seriously.

## Reporting a vulnerability

Please **do not open a public issue** for a security problem. Report it privately through GitHub:
[Report a vulnerability](https://github.com/wisnuvb/ronda/security/advisories/new) (the *Security* tab of the repository,
then *Report a vulnerability*).

Include what you found, how to reproduce it (a config and the requests involved are ideal), the version
(`ronda version`), and what an attacker gains.

Ronda is maintained by one person, in their own time. Reports are read and answered on a best-effort basis; there is no
guaranteed response time. A fix for a confirmed vulnerability is released as soon as it is ready, and the report is
credited unless you prefer otherwise.

## What counts

Things that are in scope and that we would like to hear about, for example:

- bypassing the dashboard login, the API token check, or the CSRF protection;
- reading or changing data without being logged in, or leaking a secret (API token, push tokens, Telegram token,
  password hash) through the API, the export file, the logs or the public status page;
- making Ronda connect to addresses it blocks by default (the private-network, loopback, link-local and cloud-metadata
  guard in `check`), including through redirects or DNS tricks;
- anything on the public status page (`/status`, `/api/public/status`) that exposes more than names, status, uptime
  and incident times;
- a way to make the Telegram bot act for someone who is not allowed to use it.

The [Security section of the README](README.md#security) describes the protections that exist and the limits that are
known. A problem listed there as a known limitation is not a vulnerability, but a better way to handle it is welcome as an
ordinary issue.

## Supported versions

Only the latest release receives fixes. Ronda is pre-1.0: upgrade to the newest release before reporting, if you can.
