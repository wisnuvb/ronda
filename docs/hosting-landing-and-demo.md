# Hosting the landing page and the live demo

How to publish the project's website: a static landing page (`site/`) and a read-only live demo (`ronda demo -public`)
on one server behind Cloudflare. The examples use these names; change them if you use others.

| Host | What it serves | Behind it |
|---|---|---|
| `ronda.tipamedia.com` | the landing page | nginx serving `/var/www/ronda` |
| `ronda-demo.tipamedia.com` | the live demo dashboard | nginx proxying to `ronda demo -public` on `127.0.0.1:9192` |

The demo has its own host because the dashboard uses absolute paths (`/app.css`, `/api/...`), so it cannot live under
`/demo/`. The host name is one label deep on purpose: Cloudflare's free universal certificate covers
`*.tipamedia.com` but not `demo.ronda.tipamedia.com`.

Where the names appear, if you pick different ones: the five demo links in `site/index.html` (the top of the file has a
`sed` one-liner), `server_name` in `deploy/site/nginx-demo.conf.example` and `nginx-landing.conf.example`, and the
canonical, Open Graph and sitemap URLs in `site/` (`grep -rn tipamedia.com site deploy/site`).

## What is in the repository

| File | Purpose |
|---|---|
| `site/` | the landing page: `index.html`, `assets/`, `robots.txt`, `sitemap.xml` |
| `deploy/site/ronda-demo.service` | systemd unit for the demo (dynamic user, hardened, loopback only) |
| `deploy/site/nginx-demo.conf.example` | nginx for the demo host: read-only methods, rate limits, TLS |
| `deploy/site/nginx-landing.conf.example` | nginx for the landing host: static files, strict CSP, TLS |
| `deploy/site/update-cloudflare-ips.sh` | writes the nginx include that makes nginx see the visitor's real address |
| `make deploy-site HOST=user@server` | copies `site/` to `/var/www/ronda` with rsync |

## 1. DNS and TLS in Cloudflare

1. DNS: add two **A** records, `ronda` and `ronda-demo`, both to the server's IPv4 address, **proxied** (orange cloud).
   Add AAAA records only if the server has IPv6.
2. SSL/TLS, Overview: set the mode to **Full (strict)**. (Flexible would send visitors' traffic from Cloudflare to the
   server in clear text; Full without strict accepts any certificate.)
3. SSL/TLS, Origin Server: **Create Certificate** for `ronda.tipamedia.com` and `ronda-demo.tipamedia.com` (or
   `*.tipamedia.com`). Save the two parts on the server as `/etc/ssl/ronda/origin.pem` and
   `/etc/ssl/ronda/origin.key` (`chmod 600` the key, owned by root). The nginx files point at these paths.
   Let's Encrypt works as well; change the two `ssl_certificate` lines.
4. SSL/TLS, Edge Certificates: turn on **Always Use HTTPS** and set **Minimum TLS Version** to 1.2.

## 2. The demo service

The demo is the same binary as the monitor, in a mode that serves synthetic data and refuses every change.

```bash
# the binary: from a release tarball (ronda-linux-amd64 or -arm64, whichever `uname -m` says), or from ./install.sh
sudo install -m 0755 ronda-linux-amd64 /usr/local/bin/ronda

sudo cp deploy/site/ronda-demo.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ronda-demo
curl -s http://127.0.0.1:9192/app-config.js        # window.RONDA = {"demo":true,"lang":"en",...}
```

If this server also runs a real Ronda installed with `install.sh`, they share `/usr/local/bin/ronda`; after upgrading
Ronda, `sudo systemctl restart ronda-demo` as well.

`ronda demo` creates its data in a private temporary directory on every start, so there is nothing to back up.

## 3. nginx

```bash
sudo install -d /etc/ssl/ronda /var/www/ronda
sudo ./deploy/site/update-cloudflare-ips.sh        # writes /etc/nginx/cloudflare-realip.conf

sudo cp deploy/site/nginx-landing.conf.example /etc/nginx/sites-available/ronda-landing
sudo cp deploy/site/nginx-demo.conf.example    /etc/nginx/sites-available/ronda-demo
sudo ln -s /etc/nginx/sites-available/ronda-landing /etc/nginx/sites-enabled/
sudo ln -s /etc/nginx/sites-available/ronda-demo    /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
```

`http2 on;` needs nginx 1.25.1 or newer; on an older nginx replace it by adding `http2` to the `listen ... ssl` line.

The Cloudflare address list changes rarely. Run the script again now and then (a monthly cron job is plenty). Without
the `real_ip` lines every visitor looks like a Cloudflare address and the rate limit would apply to everyone at once.

## 4. The landing page

```bash
sudo install -d -o "$USER" /var/www/ronda               # once, on the server, so rsync can write as you
make deploy-site HOST=you@your-server                   # from the repository, on your computer
```

`make deploy-site` uses `rsync --delete`, so `/var/www/ronda` mirrors `site/` exactly. Re-run it after every change.

## 5. Check it

```bash
curl -sI https://ronda.tipamedia.com/ | head -12                  # 200, Content-Security-Policy, HSTS
curl -s  https://ronda-demo.tipamedia.com/app-config.js           # {"demo":true,...}
curl -s -o /dev/null -w '%{http_code}\n' https://ronda-demo.tipamedia.com/api/sites          # 200
curl -s -X POST -d '{}' https://ronda-demo.tipamedia.com/api/sites                             # 403: read-only
curl -s -o /dev/null -w '%{http_code}\n' https://ronda-demo.tipamedia.com/ping/anything        # 404
```

Then open both in a browser, in a light and a dark system theme, and on a phone.

## Why the demo is safe to expose

It has no login by design, so it is locked in layers, and each one alone would be enough for the actions it covers:

1. **The program** (`-public`): every request that is not `GET` or `HEAD` is answered with 403, and `/ping/` (a `GET`
   that changes a push monitor in a normal install) is refused as well. It listens on loopback only and refuses another
   address.
2. **nginx**: `limit_except GET HEAD`, `/ping/` is a 404, request bodies are limited to 1 KB, and requests are rate
   limited per visitor address.
3. **systemd**: a dynamic unprivileged user, no capabilities, a read-only filesystem, `IPAddressDeny=any` apart from
   loopback (the demo is synthetic and makes no outgoing connections), and `MemoryMax=128M`.

A visitor can read the synthetic data and use some CPU; the limits above bound that. Never point the demo at real data,
never remove `-public`, and never publish port 9192 directly.

## What was tested, and what was not

Tested: the nginx files with a real nginx in a container (redirect, methods, `/ping/`, body limit, headers, the rate
limit, and a normal page load staying under it); the landing page served through the landing config in real Chrome with
the CSP applied (no violations, every request 200); the demo unit in a systemd container (it starts under the
hardening, answers reads, refuses writes); `update-cloudflare-ips.sh` against Cloudflare's published lists.

Not tested: anything with Cloudflare in front, the Origin Certificate flow, the real server (its distribution, nginx
version and architecture), `make deploy-site` against a real host, and the landing page in Safari and Firefox.
