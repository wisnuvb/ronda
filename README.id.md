# Ronda

**Pemantau situs web dan uptime yang dijalankan sendiri: memeriksa dari luar dan mengabari lewat Telegram saat ada yang down.**

[![CI](https://github.com/wisnuvb/ronda/actions/workflows/ci.yml/badge.svg)](https://github.com/wisnuvb/ronda/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[English](README.md) | Bahasa Indonesia

*Ronda* adalah keliling jaga malam yang dilakukan warga untuk memastikan lingkungan aman. Ronda yang ini mengerjakan hal
yang sama untuk situs web, server, dan pekerjaan terjadwal Anda.

Satu binary Go statis, hanya pustaka standar, dengan dashboard web tertanam di dalamnya. Situs ditambah, diubah, dijeda,
dan dihapus **saat program berjalan**, lewat dashboard, CLI, atau API.

> Dokumen rujukan yang lebih rinci (`docs/`) ditulis dalam bahasa Inggris.

## Mengapa ada

Pemantau yang berjalan di server yang dipantaunya ikut mati bersama server itu, dan tidak ada yang mengabari Anda.
Pemeriksaan dari dalam server (misalnya lewat nginx lokal) juga tidak melihat masalah DNS, CDN, sertifikat, atau
firewall. Ronda memeriksa seperti pengunjung: tiap pemeriksaan membuka koneksi baru (DNS, TCP, TLS, respons).

**Jalankan di mesin yang berbeda dari server yang dipantau.** VPS kecil di tempat lain sudah cukup. Ronda juga bisa
memanggil layanan luar ([heartbeat](#heartbeat-siapa-yang-memantau-pemantau)) supaya Anda tahu bila Ronda sendiri mati.

## Fitur

- **Cek:** HTTP(S), port TCP, rekaman DNS, dan **monitor push** untuk cron dan backup.
- **Diagnosis, bukan sekadar "down":** alert menyebut penyebabnya (DNS, ditolak, timeout, sertifikat, Cloudflare 52x,
  HTTP 4xx/5xx, kata kunci hilang, ...). Perlu beberapa kegagalan berturut-turut, dan "jaringan saya sendiri mati?"
  diuji lebih dulu.
- **Alert Telegram** (dan webhook JSON opsional): ringkasan massal, pengingat "masih down", peringatan sertifikat,
  laporan harian/mingguan, serta bot untuk memeriksa, menjeda, menambah, atau menghapus situs dari chat.
- **Dashboard** dengan uptime (24 jam sampai 90 hari), grafik, insiden, dan login dengan kata sandi ber-hash; **halaman status
  publik** opsional; jendela pemeliharaan; penerima per situs untuk beberapa klien.
- Ekspor/impor, arsip insiden permanen, dan installer systemd dengan rollback.
- Bahasa Inggris dan Indonesia; zona waktu IANA apa pun dengan waktu musim panas (DST).

## Mulai cepat

Butuh Go 1.24 atau lebih baru (`go.mod` mengunci `toolchain go1.27.3`, yang diunduh sendiri oleh perintah `go`).

**Coba dulu.** Perintah ini menjalankan dashboard dengan data sintetik, tanpa login, hanya di `127.0.0.1`:

```bash
make demo        # buka http://127.0.0.1:9191
```

Opsi: `go run ./cmd/ronda demo -lang id -timezone Asia/Jakarta -listen 127.0.0.1:9191` (demo menolak alamat selain
loopback karena tidak punya login).

**Jalankan lokal dengan config sungguhan:**

```bash
make build                                                       # menghasilkan bin/ronda
./bin/ronda gen-token                                            # isikan ke api.token
read -rs PW; printf '%s\n' "$PW" | ./bin/ronda hash-password     # isikan hasilnya ke auth.password_hash (min. 12 karakter)
cp deploy/config.example.json config.json                        # isi api.token, auth.password_hash, data_dir, notify.*
./bin/ronda telegram-test -config config.json
./bin/ronda run -config config.json                              # dashboard di http://127.0.0.1:9191
```

`read -rs` membaca kata sandi tanpa menampilkannya dan tanpa menyimpannya di riwayat shell. Tanpa
`auth.password_hash`, login dashboard nonaktif (halaman login menjelaskan caranya), tetapi API dan CLI tetap jalan
dengan `api.token`.

**Pasang di server Linux (systemd).** Di komputer Anda, bangun paket rilis (menjalankan tes dulu) lalu pasang lewat SSH:

```bash
make release                                     # dist/ronda-<versi>.tar.gz (linux amd64 + arm64)
make check-host HOST=ubuntu@1.2.3.4              # hanya memeriksa, tidak mengubah apa pun
make deploy HOST=ubuntu@1.2.3.4 ARGS="--set-password --timezone Asia/Jakarta --lang id"
```

Atau salin tarball ke server, ekstrak, dan jalankan `sudo ./install.sh` di sana. Lihat
[Memasang di server](#memasang-di-server).

## Konfigurasi

Satu berkas JSON (`-config`, bawaan `/etc/ronda/config.json`); kunci yang tidak dikenal ditolak. Validasi dengan
`ronda config-check -config config.json`. Titik awal: [`deploy/config.example.json`](deploy/config.example.json).

| Kunci | Bawaan | Arti |
|---|---|---|
| `listen` | `127.0.0.1:9191` | alamat dashboard dan API (tanpa TLS; lihat [Keamanan](#keamanan)) |
| `data_dir` | `/var/lib/ronda` | tempat status, riwayat, dan arsip insiden disimpan |
| `timezone` | `UTC` | nama zona IANA; lihat [Bahasa dan zona waktu](#bahasa-dan-zona-waktu) |
| `language` | `en` | `en` atau `id` |
| `trust_proxy` | `false` | percaya `X-Forwarded-For`/`-Proto`; hanya di belakang reverse proxy yang dilewati semua permintaan |
| `api.token` | wajib | token bearer untuk skrip dan CLI, minimal 16 karakter tanpa spasi. Ronda menolak start tanpanya |
| `auth.user` | `admin` | nama pengguna dashboard |
| `auth.password_hash` | kosong | keluaran `ronda hash-password`; kosong = login dashboard nonaktif |
| `notify.telegram_token`, `notify.telegram_chat_id` | kosong | diisi bersamaan; beberapa id dipisah koma |
| `notify.webhook_url` | kosong | menerima satu dokumen JSON per kelompok kejadian |
| `notify.telegram_commands` | `false` | aktifkan [bot Telegram](#bot-telegram) |
| `notify.telegram_admin_ids` | `[]` | id pengguna Telegram yang boleh mengubah data dari chat grup |
| `notify.batch_sec` | `8` | jendela penahanan alert, 0 sampai 120; `0` mematikan |
| `notify.digest_min` | `3` | jumlah alert berturut-turut yang dijadikan satu ringkasan, 2 sampai 50 |
| `notify.remind_after_min` | `[60, 360, 1440]` | pengingat "masih down", menit 5 sampai 43200; `[]` mematikan |
| `notify.report_daily`, `notify.report_weekly` | kosong | `"HH:MM"` dalam `timezone`; kosong = mati |
| `notify.report_weekly_day` | `1` | 0 = Minggu ... 6 = Sabtu |
| `notify.recipients` | tidak ada | chat tambahan bernama, lihat [Notifikasi](#notifikasi) |
| `status_page.enabled` / `title` / `description` | `false` / `Service status` / kosong | [halaman status publik](#halaman-status-publik); judul maks. 100, deskripsi maks. 300 karakter |
| `cert_warn_days` | `[14, 7, 3, 1]` | hari sebelum kedaluwarsa untuk memperingatkan (1 sampai 365); `[]` mematikan |
| `event_retention_days` | `365` | lama insiden disimpan, 7 sampai 3650 |
| `heartbeat_url` | kosong | alamat [heartbeat](#heartbeat-siapa-yang-memantau-pemantau) (`http://` atau `https://`) |
| `heartbeat_interval_sec` | `60` | 10 sampai 3600 |
| `control_addrs` | `["1.1.1.1:443", "8.8.8.8:443"]` | uji keterjangkauan sebelum menyatakan DOWN; `[]` menonaktifkan |
| `max_concurrent_checks` | `20` | 1 sampai 500 |

Aturan lain saat start: laporan berkala butuh `telegram_token` atau `webhook_url`; `recipients` dan `telegram_commands`
butuh `telegram_token`; `telegram_token` dan `telegram_chat_id` harus diisi bersamaan.

**Override lewat environment.** Rahasia bisa datang dari environment, bukan berkas; variabel yang tidak kosong menang.

| Variabel | Menimpa |
|---|---|
| `RONDA_API_TOKEN` | `api.token` (CLI juga membacanya) |
| `RONDA_PASSWORD_HASH` | `auth.password_hash` |
| `RONDA_TELEGRAM_TOKEN`, `RONDA_TELEGRAM_CHAT_ID` | `notify.telegram_token`, `notify.telegram_chat_id` |
| `RONDA_WEBHOOK_URL` | `notify.webhook_url` |
| `RONDA_HEARTBEAT_URL` | `heartbeat_url` |
| `RONDA_API` | khusus CLI: alamat API (bawaan `http://127.0.0.1:9191`) |

Nama lama `WEBMON_*` masih diterima sebagai cadangan yang usang (dicatat peringatan di log) dan akan dihapus di rilis
mendatang.

### Bahasa dan zona waktu

```json
{ "language": "id", "timezone": "Asia/Jakarta" }
```

Satu instance memakai satu bahasa dan satu zona waktu, berlaku untuk seluruh proses.

- **`language`**: `en` (bawaan) atau `id`. Memengaruhi pesan Telegram/webhook, laporan, bot, pesan galat API, dan
  dashboard. Pesan tanpa terjemahan jatuh ke bahasa Inggris, tidak pernah kosong. Keluaran dan bantuan CLI selalu
  berbahasa Inggris.
- **`timezone`**: nama zona IANA seperti `UTC`, `Europe/Berlin`, `America/New_York`, `Asia/Jakarta`. Menentukan jam yang
  tampil di notifikasi dan dashboard, arti `"08:00"` pada `report_daily`/`report_weekly` dan jendela pemeliharaan, serta
  di mana sebuah hari dimulai pada riwayat uptime 90 hari. Nama seperti `Local`, `WIB`, atau `GMT+7` **ditolak** karena
  bergantung pada server atau ambigu.
- **Waktu musim panas (DST) diikuti.** Jendela pemeliharaan atau laporan pukul `02:00` tetap pukul 02:00 di dinding
  setelah jam diubah, dan hari pada saat pergantian berumur 23 atau 25 jam. Singkatan zona (`CEST`, `WIB`) ikut dicetak.
- **Mengubah `timezone` pada instance yang berjalan** menggeser arti jam `"HH:MM"` yang tersimpan (jendela pemeliharaan,
  jam laporan). Riwayat dan insiden disimpan dalam UTC sehingga tidak terpengaruh.

## Memasang di server

`install.sh` untuk **Linux dengan systemd** (amd64 dan arm64). Aman dijalankan ulang.

| Perintah | Fungsi |
|---|---|
| `./install.sh --check` | hanya memeriksa: Linux, arsitektur, systemd, biner, port, curl |
| `sudo ./install.sh --dry-run` | menampilkan langkahnya tanpa mengubah apa pun |
| `sudo ./install.sh [--set-password] [--listen ADDR] [--timezone ZONA] [--lang KODE] [--no-start]` | pasang atau upgrade |
| `sudo ./install.sh --reset-password` | ganti kata sandi dashboard (dari stdin atau prompt) lalu restart |
| `sudo ./install.sh --uninstall [--purge]` | copot; `--purge` juga menghapus config, data, dan pengguna `ronda` |

`--listen` (bawaan `127.0.0.1:9191`), `--timezone` (nama IANA, bawaan `UTC`), dan `--lang` (`en` atau `id`, bawaan `en`)
hanya berpengaruh saat berkas config dibuat; `--set-password` juga begitu (installer memberi peringatan bila config sudah
ada). `--no-start` memasang tanpa menjalankan layanan.

**Tata letak**

| Apa | Di mana |
|---|---|
| biner | `/usr/local/bin/ronda` |
| config | `/etc/ronda/config.json` (mode `640 root:ronda`, token API acak sudah terisi) |
| data | `/var/lib/ronda` |
| pengguna dan grup | `ronda` (pengguna sistem, tanpa shell) |
| layanan | `ronda.service`, diperketat: tanpa capability, filesystem hanya-baca kecuali folder data, `MemoryMax=256M`, `GOMEMLIMIT=192MiB`; skor `systemd-analyze security` 1,7 |

**Upgrade** mempertahankan config dan data. Biner baru divalidasi terhadap config (`config-check`) *sebelum* yang berjalan
disentuh, dan bila versi baru tidak sehat dalam 20 detik, biner lama dikembalikan. Dashboard hanya mendengarkan di
`127.0.0.1`; buka lewat terowongan SSH (`ssh -N -L 9191:127.0.0.1:9191 user@server`) atau [reverse proxy](#keamanan).

### Upgrade dari webmon

Ronda adalah nama baru dari nama kerja proyek ini, "webmon". Bila ada instalasi `webmon`, `sudo ./install.sh` mendeteksinya
dan memigrasikannya secara otomatis:

1. menghentikan layanan `webmon` yang lama;
2. **menyalin** `/etc/webmon` dan `/var/lib/webmon` ke `/etc/ronda` dan `/var/lib/ronda` (menulis ulang `data_dir`);
3. memasang dan menjalankan `ronda`;
4. bila Ronda sehat, layanan lama dinonaktifkan dan unit serta binernya dihapus. Folder config dan data yang lama
   **dibiarkan** untuk Anda hapus sendiri setelah memeriksa semuanya;
5. bila Ronda tidak sehat, layanan lama dijalankan lagi dan tidak ada yang berubah pada instalasi lama.

webmon selalu berjalan dalam WIB dan berbahasa Indonesia. Agar tidak ada yang bergeser, bila config hasil migrasi tidak
punya kunci `timezone` atau `language`, installer menambahkan `"timezone": "Asia/Jakarta"` dan `"language": "id"` ke
`/etc/ronda/config.json` serta mencetak apa yang ditambahkannya. Tanpa itu, instance akan jatuh ke UTC dan bahasa
Inggris, dan jam jendela pemeliharaan serta laporannya bergeser 7 jam. Kedua kunci boleh diubah sesudahnya; mengubah
`timezone` belakangan menggeser arti jam `"HH:MM"` jendela dan laporan yang tersimpan (lihat
[Bahasa dan zona waktu](#bahasa-dan-zona-waktu)).

Bila instalasi lama memakai `data_dir` selain `/var/lib/webmon`, data tetap di tempatnya, pemiliknya dialihkan ke pengguna
`ronda`, dan installer menambahkan drop-in systemd (`/etc/systemd/system/ronda.service.d/data-dir.conf`) agar layanan boleh
menulis ke sana. Tanpa itu, unit hanya mengizinkan menulis ke `/var/lib/ronda`.

Sesi dashboard berakhir satu kali karena cookie sesinya berganti nama (`ronda_session`). Variabel environment `WEBMON_*`
masih berfungsi untuk sementara (usang, lihat [Konfigurasi](#konfigurasi)); gantilah menjadi `RONDA_*`.

### CLI

Perintah `site` memakai API yang sedang berjalan. CLI sendiri hanya berbahasa Inggris.

```bash
export RONDA_API_TOKEN=<api.token Anda>           # RONDA_API untuk alamat lain
ronda site add -name Toko -keyword "Keranjang" https://toko.contoh.com
ronda site list
ronda site pause <id>                             # juga: resume, rm
ronda check https://contoh.com                    # periksa sekali, tanpa menyimpan
ronda site export -o situs.json                   # di server lain: ronda site import -dry-run situs.json
```

Perintah lain: `run`, `demo`, `hash-password`, `config-check`, `telegram-test`, `gen-token`, `version`.

### Menyiapkan Telegram (gratis)

1. Chat ke `@BotFather`, kirim `/newbot`, simpan token yang diberikan.
2. Kirim satu pesan apa saja ke bot itu (atau tambahkan bot ke grup dan kirim pesan di sana).
3. Buka `https://api.telegram.org/bot<TOKEN>/getUpdates` dan ambil `chat.id` (grup biasanya negatif).
4. Isi `notify.telegram_token` dan `notify.telegram_chat_id` (beberapa id dipisah koma), lalu jalankan
   `ronda telegram-test`.

## Dashboard

| Halaman | Isi |
|---|---|
| **Sites** (`#/`) | Ringkasan Up/Down/Dijeda, pencarian, filter, pengurutan; tabel dengan uptime 24 jam, respons terakhir, dan strip 30 pemeriksaan terakhir. Baris down menampilkan penyebabnya. Menyegarkan diri tiap 10 detik. |
| **Detail situs** (`#/sites/ID`) | Uptime 24 jam / 7 / 30 / 90 hari, rata-rata dan p95 respons, sisa masa berlaku sertifikat, grafik waktu respons (1 jam sampai 90 hari), rincian pemeriksaan terakhir (DNS, koneksi, TLS, byte pertama), pengaturan, dan insiden situs itu. Tombol: Periksa sekarang, Jeda/Lanjutkan, Ubah, Hapus. |
| **Incidents** (`#/incidents`) | Setiap periode down: mulai, selesai, durasi, penyebab. Yang masih berlangsung ditandai. |
| **Tambah/Ubah** | Dialog dengan tombol **Uji dulu** (memeriksa tanpa menyimpan), validasi di tempat, dan galat dari server ditampilkan di kolom yang bersangkutan. |

Setiap halaman punya alamat sendiri (bisa di-bookmark). Alamat lama `#/situs/ID` dan `#/insiden` masih dibuka. Tema
mengikuti sistem dan bisa diganti dari bilah atas. Waktu ditampilkan dalam `timezone` server, sama dengan pesan Telegram.

Aksesibilitas yang dirancang (apa yang sudah diverifikasi ada di [docs/testing.md](docs/testing.md), bahasa Inggris):
status berupa ikon dan teks, tidak hanya warna; kontras minimal 4,5:1 untuk teks dan 3:1 untuk komponen di kedua tema;
bisa dioperasikan dengan keyboard, dengan tautan lompat ke konten dan fokus pindah ke judul halaman; grafik bisa
ditelusuri dengan panah/Home/End/Escape serta punya ringkasan teks dan tabel alternatif; kegagalan di grafik ditandai
bentuk dan teks "GAGAL"; `prefers-reduced-motion` dihormati; di layar sempit tabel menjadi kartu.

## Jenis cek

Jenisnya ditentukan dari awalan alamat, jadi API, bot, dan CLI tidak butuh field tambahan.

| Alamat | Yang diperiksa | Gagal bila |
|---|---|---|
| `https://contoh.com` | halaman web | status, teks, TLS, atau waktu tidak sesuai |
| `tcp://db.contoh.com:5432` | port menerima koneksi (database, SMTP, SSH, ...) | ditolak, tidak menjawab, atau host tidak ditemukan |
| `dns://contoh.com` | rekaman DNS bisa di-resolve | NXDOMAIN, tidak ada rekaman jenis itu, tidak ada jawaban, atau jawaban tidak memuat `keyword` |
| monitor push | pekerjaan memanggil Ronda | tidak ada ping dalam *jadwal + toleransi*; lihat [Monitor push](#monitor-push-cron-dan-backup) |

Untuk DNS: `dns_type` (A, AAAA, MX, TXT, NS; bawaan A), `dns_server` (`host` atau `host:port`; kosong = resolver server
ini), dan `keyword` sebagai **jawaban yang harus ada**, mis. alamat IP yang diharapkan. Berguna untuk menangkap rekaman
yang berubah atau salah diarahkan. Dashboard menampilkan jawaban terakhir.

```bash
ronda check tcp://db.contoh.com:5432
ronda check -dns-type MX -keyword mail.contoh.com dns://contoh.com
ronda site add -name Database tcp://db.contoh.com:5432
```

Cek TCP dan server DNS **memakai penjaga SSRF yang sama** dengan cek web: alamat loopback, privat, dan link-local ditolak
kecuali `allow_private` diaktifkan untuk situs itu (kalau tidak, dashboard bisa dipakai memindai port jaringan internal).
Jenis sebuah situs tidak bisa diubah setelah dibuat (target yang berbeda = riwayat yang berbeda). Cek TCP hanya menguji
sambungan, bukan protokol di atasnya.

## Monitor push: cron dan backup

Cek biasa bertanya "apakah situs menjawab?". Backup malam tidak punya alamat untuk ditanya, jadi arahnya dibalik:
**pekerjaan itu yang memanggil Ronda setelah berhasil, dan diam berarti gagal.** Di dashboard: Tambah situs, pilih
**Pekerjaan terjadwal**, tentukan seberapa sering ia melapor dan toleransi keterlambatan. Halaman situs menampilkan alamat
ping dan contoh crontab:

```bash
0 2 * * * /usr/local/bin/backup.sh && curl -fsS -m 10 --retry 3 https://mon.contoh.com/ping/<token>
```

Tanda `&&` itu penting: ping hanya terkirim bila backup berhasil. Untuk melapor gagal secara eksplisit tanpa menunggu
jadwal lewat, panggil `.../ping/<token>/fail`. `GET` dan `POST` sama-sama diterima.

- **DOWN** bila tidak ada ping dalam *jadwal + toleransi*; **pulih pada ping berikutnya**. `interval_sec` 60 detik sampai
  31 hari (bawaan 1 hari), `grace_sec` 0 sampai 86400 (bawaan 300), dan `fail_threshold` berarti berapa jadwal
  berturut-turut yang boleh terlewat (bawaan 1).
- Setelah Ronda dimulai ulang, setiap pekerjaan mendapat satu periode penuh sebelum dianggap terlambat, supaya restart
  tidak memicu alarm palsu. Situs yang dijeda atau sedang pemeliharaan tetap **menerima** ping (dijawab `ok`), hanya
  tidak menilai dan tidak mengabari.
- **Alamat ping adalah rahasia.** Siapa pun yang tahu bisa mengirim ping palsu dan menutupi kegagalan. Perlakukan seperti
  kata sandi dan jangan taruh di repositori publik. Bila bocor, klik **Putar ulang token** (alamat lama mati seketika).
  Token acak 128 bit, dibandingkan dengan waktu tetap; token tidak dikenal dijawab `404`; 20 tebakan salah per 10 menit
  per alamat klien mengunci jalur ping sementara (`429`).
- Alamat ping tidak pernah muncul di pesan Telegram, halaman status publik, atau log. Hanya dashboard (setelah login) dan
  respons API untuk situs itu yang menyebutnya.
- Reverse proxy harus meneruskan `/ping/` (lihat `deploy/nginx.conf.example`); jalur ini sengaja tanpa login. Pasang
  `access_log off` di sana karena tokennya ada di URL.
- Bot Telegram tidak bisa membuat monitor push (`/add push://...` ditolak): token tidak boleh lewat obrolan.
- Ronda tidak tahu apakah pekerjaan *seharusnya* berjalan lebih sering; ia hanya menilai ada tidaknya ping.

## Halaman status publik

Halaman yang bisa dibuka siapa saja tanpa login, untuk klien atau pengguna Anda. **Mati secara bawaan:**

```json
"status_page": { "enabled": true, "title": "Status Layanan Contoh", "description": "Kabar terbaru tentang layanan kami." }
```

Lalu centang **Tampilkan di halaman status publik** pada situs yang ingin ditampilkan (bawaan: tidak ada). Halaman ada di
`/status` (datanya di `/api/public/status`): banner keadaan umum, tiap situs dengan status, uptime 90 hari, dan batang per
hari, plus insiden 14 hari terakhir.

**Yang tidak pernah keluar:** URL, id, petunjuk teknis ("koneksi ditolak ke 10.0.0.5:8443"), dan situs yang tidak
dicentang. Insiden hanya menyebut jenis umum (mis. "Koneksi ditolak"). Situs yang dijeda disembunyikan. Tetapi **nama**
situs tampil apa adanya seperti yang Anda isi, jadi jangan menamai situs dengan sesuatu yang tidak boleh dilihat publik.
Hasilnya di-*cache* 15 detik agar banyak pengunjung tidak membebani monitor, dan halaman bertanda `noindex`.

Untuk membukanya ke publik tanpa membuka dashboard, teruskan **hanya** jalur ini di reverse proxy:

```nginx
location = /status             { proxy_pass http://127.0.0.1:9191; }
location = /api/public/status  { proxy_pass http://127.0.0.1:9191; }
location ~ ^/(app\.css|theme\.js|status\.js|dom\.js|icons\.js|fmt\.js|favicon\.svg|i18n\.js|i18n-id\.js|app-config\.js)$ { proxy_pass http://127.0.0.1:9191; }
```

`app-config.js` dibuat oleh server: ia memberi tahu halaman bahasa dan zona waktu yang dipakai Ronda, dan tidak berisi
rahasia.

## Jendela pemeliharaan

Sebelum deploy atau migrasi, tekan **Pemeliharaan** di halaman situs (30 menit sampai 7 hari) atau kirim
`/maintenance toko 2h` (atau `/pemeliharaan toko 2j`) ke bot. Selama itu situs **tidak diperiksa dan tidak ada
notifikasi**. Saat waktunya habis, situs diperiksa lagi **dengan sendirinya**, dan bila masih down Anda dikabari.
**Akhiri pemeliharaan** mengakhirinya lebih cepat. Beda dengan **Jeda**, yang tanpa batas waktu. Masa pemeliharaan tidak
ikut dihitung ke uptime karena memang tidak ada pemeriksaan. Lewat API, satu jendela bisa 1 menit sampai 30 hari.

**Jadwal mingguan.** Untuk yang berulang (backup tiap malam, restart tiap Minggu), tekan **Atur jadwal** di halaman
situs: pilih hari, jam mulai (dalam `timezone`), dan lama; sampai 7 jendela per situs, boleh melewati tengah malam.
Scheduler bangun tepat saat jendela mulai dan berakhir. **Akhiri pemeliharaan** saat jendela terjadwal berjalan hanya
melewati *kali itu*; jendela minggu depan tetap berlaku. Lewat API, `maintenance_weekly` berisi
`[{"days": [6, 0], "start": "02:00", "minutes": 120}]` (hari: 0 = Minggu ... 6 = Sabtu).

## Notifikasi

Alert dikirim ke Telegram (`notify.telegram_token` bersama `telegram_chat_id`) dan, bila diisi, ke webhook. Aturan dan
contoh lengkap tiap butir ada di [docs/notifications.md](docs/notifications.md) (bahasa Inggris).

- **Penggabungan.** Alert ditahan sampai **8 detik tanpa kejadian baru** (paling lama 45 detik), lalu **3 situs atau lebih
  yang down (atau pulih) berturut-turut dijadikan satu pesan** berisi penyebab tiap situs dan catatan bahwa penyebabnya
  kemungkinan bersama. Kurang dari 3 dikirim satu per satu; peringatan sertifikat tidak pernah digabung. Atur dengan
  `notify.batch_sec` (bawaan 8; `0` mematikan) dan `notify.digest_min` (bawaan 3).
- **Pengingat "masih down"** pada **1 jam, 6 jam, dan 24 jam** sejak situs jatuh, lalu sekali sehari, dengan penyebab
  terbaru. Satu pesan per ambang, aman terhadap restart, tidak pernah dicatat sebagai insiden baru.
  `notify.remind_after_min` (menit, 5 sampai 43200; `[]` mematikan).
- **Peringatan sertifikat**, sekali tiap sertifikat melewati 14, 7, 3, dan 1 hari sebelum kedaluwarsa (`cert_warn_days`,
  `[]` untuk mematikan). Sertifikat yang *sudah* kedaluwarsa dianggap DOWN (`tls_expired`).
- **Laporan berkala** (mati secara bawaan): `"report_daily": "08:00"`, `"report_weekly": "08:00"`,
  `"report_weekly_day": 1` (0 = Minggu ... 6 = Sabtu; jam dalam `timezone`). Situs yang dijeda tidak dihitung. Laporan
  dikirim sekali, dan terlambat paling lama 3 jam setelah restart. `/report` di bot membuat laporan sesuai permintaan.
- **Penerima per situs** untuk beberapa klien: definisikan `notify.recipients` (`{"klien-a": "-1001234567890"}`), lalu
  isi `notify_to` per situs (formulir, `site add -notify-to`, atau API). Ringkasan dan laporan tidak pernah mencampur
  penerima, id chat tidak pernah ditampilkan di dashboard atau API, dan bot hanya melayani chat utama. `default` berarti
  chat utama.

## Bot Telegram

Aktifkan dengan `"telegram_commands": true` (mati secara bawaan) untuk mengelola Ronda dari chat. Nama perintah
mengutamakan bahasa Inggris; nama Indonesia tetap ada sebagai alias.

| Perintah | Fungsi | Alias |
|---|---|---|
| `/status`, `/list` | ringkasan dan situs yang down; semua situs dengan uptime dan id | `/daftar` |
| `/report [daily\|weekly]` | ringkasan uptime, gangguan, dan sertifikat (bawaan weekly); terbuka untuk semua anggota grup | `/laporan [harian\|mingguan]` |
| `/check site` | periksa sekarang, tidak mengubah status | `/cek` |
| `/pause site`, `/resume site` | jeda, lanjutkan | `/jeda`, `/lanjut` |
| `/maintenance site 2h`, `/endmaint site` | pemeliharaan (30m, 2h, 1d); akhiri | `/pemeliharaan`, `/akhiri` |
| `/add url [name]` | tambah situs; `shop.example.com` tanpa skema dianggap `https://`; juga `tcp://host:port` dan `dns://nama` | `/tambah` |
| `/remove site yes` | hapus; tanpa `yes` (atau `ya`) hanya meminta konfirmasi | `/hapus`, `/delete` |

`site` boleh berupa id, nama, atau sebagian nama/alamat. Bila ambigu, bot menyebut kandidatnya dan tidak mengubah apa pun.
Bila `language` diisi `id`, bantuan bot menampilkan nama perintah Indonesianya.

**Di grup, tidak semua anggota boleh mengubah data.** Chat pribadi hanya berisi satu orang, jadi semua perintah jalan. Di
grup, `/status`, `/list`, `/report`, dan `/check` terbuka untuk semua anggota, tetapi perintah yang **mengubah** (`/add`,
`/remove`, `/pause`, `/resume`, `/maintenance`, `/endmaint`, dan alias Indonesianya) hanya diterima dari pengguna yang id
Telegram-nya terdaftar:

```json
"notify": { "telegram_commands": true, "telegram_admin_ids": [123456789] }
```

Itu id **pengguna** (angka positif; kirim pesan ke `@userinfobot` untuk mengetahuinya), bukan id grup. Tanpa daftar itu,
grup hanya bisa membaca. Bot juga menolak bila Telegram tidak menyebut jenis chat (gagal-tertutup), dan penolakan dicatat
di log beserta id penggunanya.

Keamanan bot: hanya chat di `notify.telegram_chat_id` yang dilayani; chat lain **tidak dibalas** (id-nya dicatat di log,
berguna untuk mengisi config). **Di grup, setiap anggota bisa memakai perintah baca**, jadi pakai chat pribadi atau grup
yang semua anggotanya tepercaya. Antrean perintah yang menumpuk saat Ronda mati **dilewati** saat start. Lewat bot tidak ada
cara mengaktifkan `allow_private`. Bot memakai *long polling*, jadi tidak butuh alamat publik. Hanya satu proses yang boleh
polling dengan token yang sama (bila ada yang lain, log menjelaskan galat 409).

## Data, ekspor/impor, dan kata sandi

- **Arsip insiden.** Kejadian down, pulih, dan sertifikat ditambahkan ke `events.jsonl` di folder data, dan daftar
  Insiden dibangun darinya. Disimpan `event_retention_days` hari (bawaan 365, 7 sampai 3650).
  API: `GET /api/incidents?limit=&before=`.
- **Ekspor/impor** memindahkan pengaturan situs (bukan riwayat atau insiden) antar server; impor selalu menampilkan
  pratinjau dulu, aman diulang, dan menerima paling banyak 5.000 situs per berkas (500 per server). Token ping monitor
  push adalah rahasia dan tidak ikut kecuali Anda mengekspor dengan `-tokens`:

  ```bash
  ronda site export -o situs.json            # GET  /api/export
  ronda site import -dry-run situs.json      # POST /api/import?dry_run=1  (hanya menampilkan hasilnya)
  ronda site import situs.json               # POST /api/import
  ```
- **Ganti kata sandi** dari dashboard (tombol kunci, minimal 12 karakter) hanya bisa dari sesi login, mengeluarkan semua
  perangkat lain, dan disimpan di `auth.json` di folder data. `auth.password_hash` di config yang diubah (mis. lewat
  `install.sh --reset-password`) selalu menang atas berkas itu.

Selengkapnya di [docs/data-and-accounts.md](docs/data-and-accounts.md) (bahasa Inggris).

## Heartbeat: siapa yang memantau pemantau?

Bila Ronda atau servernya mati, tidak ada yang mengabari Anda, karena pengabarnya ikut mati. Isi `heartbeat_url` dengan
alamat ping dari layanan luar yang punya fitur "alarm bila ping berhenti" (mis. healthchecks.io, yang bisa mengirim ke
Telegram), dan Ronda memanggilnya tiap `heartbeat_interval_sec` (bawaan 60). Ping **sengaja berhenti** bila pemeriksaan
macet (tidak ada pemeriksaan selesai selama dua kali interval terpanjang + 1 menit; monitor push tidak dihitung), supaya
proses yang hidup tetapi tidak bekerja pun memicu alarm. URL-nya rahasia dan tidak pernah ditulis ke log.

## Cara kerja pemantauan

- Tiap situs dicek sesuai `interval_sec` (bawaan 60 detik). Tiap pemeriksaan membuka koneksi baru (DNS, TCP, TLS,
  respons), seperti pengunjung baru. Situs yang baru ditambahkan dicek langsung.
- **DOWN** hanya setelah `fail_threshold` (bawaan 3) kegagalan **berturut-turut**; saat gagal, pengecekan diulang tiap
  10 detik untuk mengonfirmasi. **PULIH** setelah 2 sukses berturut-turut. Satu gangguan sesaat tidak mengirim apa pun.
- **Jaringan monitor mati bukan berarti situs mati.** Sebelum menyatakan DOWN, Ronda mencoba menyambung ke
  `control_addrs` (bawaan 1.1.1.1 dan 8.8.8.8 port 443). Bila semuanya gagal, alert ditahan, dicatat di log, dan kegagalan
  itu **tidak dihitung** ke uptime situs. Kosongkan daftar itu (`[]`) untuk mematikannya.
- Status (up/down dan sejak kapan) disimpan, jadi restart tidak mengirim ulang alert untuk situs yang sudah down.
- Riwayat: hasil mentah sekitar 24 jam terakhir (maksimal 2.880 titik per situs) dan rekap per jam selama 90 hari,
  disimpan ke `history.json` tiap 2 menit dan saat berhenti. Uptime tidak pernah tampil 100% bila ada satu pun
  pemeriksaan yang gagal (99,996% tampil 99,99%).
- Pesan Telegram berisi **penyebab**, bukan sekadar "down":

| Penyebab | Arti |
|---|---|
| `dns` | domain tidak ditemukan / DNS tidak menjawab |
| `refused` | server terjangkau tetapi tidak ada layanan di port itu |
| `timeout_connect` / `timeout_tls` / `timeout_response` | server tidak menjawab / TLS macet / backend macet |
| `tls_expired` / `tls_hostname` / `tls_untrusted` | sertifikat kedaluwarsa / nama tidak cocok / tidak tepercaya |
| `cloudflare` | 520 sampai 526 dan 530, dengan arti masing-masing (521 = origin mati, 522 = timeout, ...) |
| `http_5xx` / `http_4xx` | 502, 503, 504, 403 (kemungkinan WAF), 404, 429, ... |
| `keyword` | halaman terbuka tetapi teks yang dicari tidak ada |
| `redirect_loop`, `blocked`, `reset`, `unreachable` | loop redirect, alamat internal diblokir, koneksi diputus, rute putus |
| `missed`, `reported_failure` | monitor push: tidak ada ping tepat waktu / pekerjaan melapor gagal sendiri |

## Skala dan kinerja

Diukur pada daemon sungguhan di laptop (arm64, macOS): **500 situs**, masing-masing dengan riwayat penuh (2.880 titik
mentah + 2.160 rekap jam, `history.json` 50 MB, seperti instalasi berumur setahun), dicek tiap 10 detik (sekitar 50
pemeriksaan per detik) selama 150 detik sementara API ditanyai terus-menerus. Semua pemeriksaan sukses (500 `up`, tanpa
peringatan di log).

| | Hasil |
|---|---|
| Puncak memori (RSS) | **164 MB** (`MemoryMax` unit adalah 256M) |
| CPU rata-rata | **11,3%** |
| Daftar situs (`GET /api/sites`), median | **13 ms** (p95 15 ms) |
| Detail / riwayat / halaman status, median | 1,1 / 1,0 / 0,6 ms |
| Waktu siap setelah start | 0,8 detik |

Batasnya 500 situs per server. Unit systemd memasang `GOMEMLIMIT=192MiB` agar pemungut sampah bekerja lebih agresif
sebelum menyentuh `MemoryMax`. Ini angka **dari satu mesin pengembang**, pada target lokal tanpa latensi jaringan
sungguhan; di VPS kecil angkanya akan berbeda, jadi ulangi dengan skrip serupa bila skala Anda mendekati ratusan situs.
Lihat [docs/testing.md](docs/testing.md).

## Opsi per situs

| Field | Bawaan | Keterangan |
|---|---|---|
| `url` | wajib | `http(s)://...`, `tcp://host:port`, atau `dns://nama`; tanpa `user:password` |
| `type` | dari `url` | `http`, `tcp`, `dns`, atau `push` (tidak bisa diubah setelah dibuat) |
| `name` | host | nama di pesan dan daftar (maks. 100 karakter) |
| `method` | `GET` | `GET` atau `HEAD` (khusus web) |
| `interval_sec` | 60 | 10 sampai 86400 (push: 60 sampai 2.678.400, bawaan 86400) |
| `timeout_sec` | 10 | 1 sampai 60, tidak boleh melebihi interval |
| `fail_threshold` | 3 | 1 sampai 10 (push: bawaan 1) |
| `grace_sec` | 300 | khusus push: tambahan waktu setelah `interval_sec` sebelum ping dianggap terlewat, 0 sampai 86400 |
| `expect_status` | 200 sampai 399 | daftar kode yang dianggap sehat, mis. `[200, 401]` |
| `keyword` | tidak ada | web: teks yang harus ada di 1 MB pertama halaman; dns: jawaban yang harus ada (tanpa membedakan huruf besar/kecil) |
| `dns_type`, `dns_server` | `A`, sistem | khusus dns: jenis rekaman, server DNS |
| `no_redirect` | false | jangan ikuti pengalihan |
| `user_agent` | `ronda/1 (uptime-check)` | untuk situs di balik WAF yang memblokir monitor |
| `allow_private` | false | izinkan target loopback/LAN (lihat [Keamanan](#keamanan)) |
| `paused` | false | berhenti diperiksa tanpa dihapus |
| `public` | false | tampilkan di halaman status publik |
| `notify_to` | tidak ada | nama penerima, lihat [Notifikasi](#notifikasi) |
| `maintenance_weekly` | tidak ada | jendela mingguan, lihat [Jendela pemeliharaan](#jendela-pemeliharaan) |

`maintenance_until` (waktu Unix dalam ms) menandai jendela pemeliharaan satu kali; atur lewat endpoint di bawah. Mengubah
`url` atau `method` mengosongkan status dan riwayat situs itu (target yang berbeda). Menjeda mengosongkan status tetapi
mempertahankan riwayat.

## API

Dua cara masuk: `Authorization: Bearer <api.token>` (skrip, CLI), atau cookie sesi dari `POST /api/login` (dashboard;
perubahan juga wajib menyertakan header `X-CSRF-Token` dari `GET /api/session`). Teks galat mengikuti `language`.

| Metode dan path | Fungsi |
|---|---|
| `POST /api/login`, `POST /api/logout`, `GET /api/session` | masuk, keluar, siapa yang sedang masuk |
| `POST /api/password` | ganti kata sandi dashboard (hanya sesi login) |
| `GET /api/sites` | semua situs: status, uptime 24 jam, strip riwayat |
| `POST /api/sites` | tambah situs (JSON seperti tabel di atas) |
| `GET /api/sites/{id}` | satu situs + statistik lengkap + 60 hasil terakhir |
| `PATCH /api/sites/{id}`, `DELETE /api/sites/{id}` | ubah sebagian field, hapus |
| `POST /api/sites/{id}/pause`, `/resume` | jeda / lanjutkan |
| `POST /api/sites/{id}/check` | periksa sekarang (tidak mengubah status atau memicu notifikasi) |
| `POST /api/sites/{id}/maintenance` `{"minutes": N}`, `DELETE …/maintenance` | mulai pemeliharaan (1 menit sampai 30 hari), akhiri |
| `POST /api/sites/{id}/token` | putar ulang token ping monitor push |
| `GET /api/sites/{id}/history?range=1h\|6h\|24h\|7d\|30d\|90d` | data grafik (mentah sampai 24 jam, rekap per jam di atasnya) |
| `POST /api/check` | uji satu draf situs tanpa menyimpannya |
| `GET /api/export[?tokens=1]`, `POST /api/import[?dry_run=1]` | unduh semua situs sebagai JSON, impor berkas itu (token ping hanya bila diminta) |
| `GET /api/incidents[?site=ID&limit=&before=]`, `GET /api/events` | insiden; kejadian mentah (300 terakhir) |
| `GET /api/recipients` | nama penerima yang dikonfigurasi |
| `GET /ping/{token}`, `POST /ping/{token}`, dan `…/fail` | ping monitor push (tanpa login; token adalah kredensialnya) |
| `GET /status`, `GET /api/public/status` | halaman status publik dan datanya, bila diaktifkan (tanpa login) |
| `GET /healthz` | `ok`, tanpa autentikasi |

## Keamanan

- **Login dashboard:** kata sandi disimpan sebagai hash PBKDF2-HMAC-SHA256 (310.000 iterasi, garam acak). Waktu respons dan
  pesan tidak membedakan nama pengguna yang salah dari kata sandi yang salah. **5 percobaan gagal per 10 menit per
  alamat** mengunci login (HTTP 429). Sesi disimpan di memori (restart = keluar semua), kedaluwarsa setelah 12 jam tanpa
  aktivitas atau 7 hari sejak masuk. Cookie `HttpOnly`, `SameSite=Strict`, dan `Secure` lewat HTTPS; namanya
  `ronda_session`, atau `__Host-ronda_session` lewat HTTPS, sehingga sub-domain tetangga tidak bisa menimpanya
  ("cookie tossing").
- **CSRF:** cookie saja tidak cukup untuk mengubah data; wajib header `X-CSRF-Token` milik sesi. Login menolak permintaan
  lintas-asal (header `Origin`).
- **CSP ketat:** `default-src 'none'; script-src 'self'; style-src 'self'` tanpa `unsafe-inline`/`unsafe-eval`, ditambah
  `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: no-referrer`, `Permissions-Policy`, dan
  `Cross-Origin-Opener-Policy`. Antarmuka dibangun dengan `createElement` dan node teks, bukan dengan merangkai HTML,
  sehingga nama situs atau keterangan tidak bisa jadi markup. Tes otomatis menjaga ini (tidak ada skrip/gaya inline,
  tidak ada `innerHTML` selain ikon statis).
- **API token** wajib (minimal 16 karakter tanpa spasi); program menolak start tanpa itu.
- **Secara bawaan hanya mendengarkan di `127.0.0.1` dan tanpa TLS.** Untuk akses jarak jauh pasang reverse proxy ber-HTTPS
  dan set `"trust_proxy": true`. Contoh nginx (yang lengkap ada di `deploy/nginx.conf.example`):

  ```nginx
  location / {
      proxy_pass http://127.0.0.1:9191;
      proxy_set_header Host $host;
      proxy_set_header X-Forwarded-For $remote_addr;
      proxy_set_header X-Forwarded-Proto $scheme;
  }
  ```

  Di belakang Cloudflare, pasang `ngx_http_realip_module` agar `$remote_addr` adalah IP pengunjung. Bila tidak, semua
  pengunjung tampak berasal dari satu alamat dan pembatas login akan mengunci semuanya sekaligus. Aktifkan `trust_proxy`
  **hanya** bila setiap permintaan memang lewat proxy itu.
- **Perlindungan SSRF.** Karena URL bebas diisi, koneksi ke alamat loopback, privat (10/8, 172.16/12, 192.168/16),
  link-local (termasuk 169.254.169.254, metadata cloud), CGNAT, dan multicast **ditolak** secara bawaan. Pemeriksaan
  dilakukan pada alamat yang benar-benar disambungi (setelah DNS), jadi redirect dan DNS rebinding ikut tercakup, dan
  bentuk IPv6 terjemahan (NAT64 `64:ff9b::/96`, 6to4 `2002::/16`, site-local, Teredo) dinilai dari IPv4 yang tertanam
  (jadi jaringan DNS64 yang sah tetap berfungsi). Untuk memantau layanan internal, aktifkan `allow_private` per situs.
- **Kerentanan pustaka standar ikut terbawa ke biner.** `go.mod` mengunci `toolchain go1.27.3`, dan `make vuln`
  (`govulncheck`, butuh jaringan) perlu dijalankan ulang secara berkala, lalu naikkan versi itu bila ada temuan baru.
  Biner yang dibangun lebih dulu harus dibangun ulang agar mendapat perbaikannya.
- **Tidak ada rahasia di berkas contoh.** Tes otomatis memastikan `deploy/config.example.json` tidak berisi token, chat id,
  atau hash, dan bahwa tidak ada berkas di `deploy/` yang berbentuk token Telegram atau alamat webhook; `make release`
  menjalankannya, jadi token yang tak sengaja tertempel saat uji tidak ikut ke tarball.
- Token bot dan URL heartbeat tidak pernah masuk log atau pesan galat. URL dengan `user:password` ditolak agar kredensial
  tidak muncul di pesan Telegram.
- Pemeriksaan tidak memakai proxy dari environment dan membaca paling banyak 1 MB body.

Untuk melaporkan kerentanan, lihat [SECURITY.md](SECURITY.md).

## Keterbatasan yang diketahui

- Banyak pengguna dan peran (hanya ada satu akun admin).
- Halaman status publik belum punya langganan notifikasi untuk pengunjung dan belum bisa menampilkan pengumuman manual.
- Cek TCP hanya menguji sambungan (belum membaca banner atau protokol), dan DNS belum memeriksa DNSSEC atau membandingkan
  beberapa server sekaligus.
- Jendela pemeliharaan hanya mingguan (belum "tanggal tertentu tiap bulan").
- **Satu titik pandang:** bila jaringan di sisi monitor bermasalah sebagian, bisa muncul false positive.
- Belum ada eskalasi (mengabari orang lain bila tidak ada yang menanggapi) dan belum ada jadwal jaga/piket.
- Penerima hanya Telegram; webhook tidak dibedakan per situs.
- Laporan hanya harian dan mingguan (belum bulanan atau SLA per klien), dan uptimenya mengikuti jendela 24 jam dan 7 hari,
  bukan bulan kalender.
- Ekspor tidak membawa riwayat uptime atau arsip insiden; memindahkan itu berarti menyalin folder data (`history.json`,
  `events.jsonl`) saat Ronda berhenti.
- Pembatas login dihitung **per alamat IP**: bila banyak orang berbagi satu NAT, atau proxy tidak meneruskan alamat asli
  (`trust_proxy` salah), satu orang yang salah ketik berulang bisa mengunci semuanya selama 10 menit.
- Tidak ada image Docker, dan installer hanya untuk Linux dengan systemd. `make build` dan `make demo` berjalan di macOS
  dan Linux yang punya Go; server Windows dan macOS tidak diklaim.
- Katalog bahasa: hanya Inggris dan Indonesia; CLI dan bantuannya hanya berbahasa Inggris.

Apa yang sudah dan belum diuji dicatat apa adanya di [docs/testing.md](docs/testing.md) (bahasa Inggris).

## Pengembangan

```bash
make test        # go vet + go test -race
make vuln        # govulncheck: kerentanan di kode dan pustaka standar Go (butuh jaringan)
make demo        # dashboard dengan data sintetik; berkas UI dibaca dari disk (edit lalu muat ulang)
make build       # bin/ronda untuk mesin ini
make release     # tes, lalu dist/ronda-<versi>.tar.gz berisi biner linux amd64 + arm64 dan installer
```

**Tata letak `internal/`**

| Paket | Isi |
|---|---|
| `config`, `model` | berkas config, nilai bawaan, validasi, override environment; tipe bersama dan zona waktu yang dikonfigurasi |
| `registry` | daftar situs tersimpan: validasi, impor/ekspor, pemeliharaan |
| `check` | pemeriksa HTTP, TCP, dan DNS dengan diagnosis dan penjaga SSRF |
| `monitor` | penjadwalan, state machine, pengingat, peringatan sertifikat, monitor push, penyimpanan |
| `history`, `eventlog` | hasil mentah dan rekap per jam; arsip insiden permanen (`events.jsonl`) |
| `notify`, `report`, `bot` | pengiriman Telegram/webhook, antrean, ringkasan, perutean; laporan harian/mingguan; bot Telegram |
| `schedule` | jendela pemeliharaan mingguan dan penanganan `HH:MM` dengan DST |
| `api`, `auth` | API HTTP, sesi, halaman status publik; hash kata sandi dan pembatas login |
| `heartbeat`, `i18n`, `integration` | ping heartbeat keluar; katalog pesan; tes perutean lintas paket |

Di tempat lain: `cmd/ronda` (CLI), `web/static` (dashboard dan halaman publik yang tertanam, ES module biasa, tanpa
langkah build), `deploy` (installer, unit systemd, contoh config, contoh nginx).

**Menambah bahasa.** Teks Inggris sebuah pesan adalah kuncinya, jadi bahasa Inggris tidak butuh katalog dan terjemahan
yang belum ada jatuh ke bahasa Inggris. Bahasa lain berupa katalog teks Inggris ke terjemahan:

- Server: `internal/i18n/id_*.go`, satu berkas per area, masing-masing memanggil
  `i18n.Register("id", map[string]string{...})` dari `init()`. Pesan ditandai di kode dengan `i18n.T(...)` (atau
  `i18n.N(...)` untuk menandai tanpa menerjemahkannya dulu). Bahasa baru mendapat berkasnya sendiri yang didaftarkan
  dengan kodenya; `language` lalu menerima kode itu.
- Dashboard: `web/static/i18n-id.js`, JSON ketat setelah `export default`, satu entri per baris. Daftarkan katalog baru
  di peta `CATALOGS` (dan baris locale) pada `web/static/i18n.js`. Placeholder seperti `{name}` dan `%s` harus
  dipertahankan persis seperti di teks Inggris.
- Tes menjaga kelengkapan (`go test ./internal/i18n ./web`): setiap pesan yang dipakai kode butuh entri di setiap katalog
  yang dikirim, terjemahan harus mempertahankan placeholder dan format verb-nya, dan katalog dashboard tidak boleh
  memuat apa pun yang tidak lagi dipakai kode. Jangan merangkai kalimat dari potongan terjemahan; terjemahkan kalimat utuh.

**Catatan tentang contoh nginx.** `go test ./web` memeriksa bahwa contoh reverse proxy untuk halaman status publik di
`README.md` (versi Inggris) memuat setiap berkas yang dimuat halaman publik. Bila Anda menambah import di `status.js`,
tes gagal sampai contohnya diperbarui; perbarui kedua README.

## Lisensi

[MIT](LICENSE), oleh Wisnu Saputro.
