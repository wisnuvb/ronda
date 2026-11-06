package i18n

// Indonesian wording of the Telegram bot (internal/bot). In Indonesian the primary command names are the
// Indonesian ones; the bot accepts both spellings in either language.
func init() {
	Register("id", map[string]string{
		"⚠️ In a group, commands that change data are for ronda admins only. You can still use /status, /list and /check.": "⚠️ Di grup, perintah yang mengubah data hanya untuk admin ronda. Anda masih bisa memakai /status, /list, dan /cek.",

		`<b>ronda</b> — commands:
/status — summary, and the sites that are down
/list — all sites
/report [<i>daily</i>|<i>weekly</i>] — summary of uptime, incidents and certificates (default: weekly)
/check <i>site</i> — check now
/pause <i>site</i> · /resume <i>site</i>
/maintenance <i>site</i> <i>duration</i> — e.g. 30m, 2h, 1d (stops checking, then resumes by itself)
/endmaint <i>site</i> — end maintenance
/add <i>url</i> [name] — add a site; <code>tcp://host:port</code> checks a port, <code>dns://name</code> checks a DNS record
/remove <i>site</i> yes — remove a site

<i>site</i> can be an id, a name, or part of a name or address.`: `<b>ronda</b> — perintah:
/status — ringkasan, dan situs yang down
/list — semua situs
/laporan [<i>harian</i>|<i>mingguan</i>] — ringkasan uptime, gangguan, dan sertifikat (default: mingguan)
/cek <i>situs</i> — periksa sekarang
/jeda <i>situs</i> · /lanjut <i>situs</i>
/pemeliharaan <i>situs</i> <i>durasi</i> — mis. 30m, 2j, 1d (berhenti diperiksa, lalu lanjut sendiri)
/akhiri <i>situs</i> — akhiri pemeliharaan
/tambah <i>url</i> [nama] — tambah situs; <code>tcp://host:port</code> memeriksa port, <code>dns://nama</code> memeriksa rekaman DNS
/hapus <i>situs</i> ya — hapus situs

<i>situs</i> boleh id, nama, atau sebagian nama/alamat.`,

		"⏸ <b>%s</b> paused. No checks or notifications until /resume.":      "⏸ <b>%s</b> dijeda. Tidak ada pemeriksaan atau notifikasi sampai /lanjut.",
		"▶️ <b>%s</b> resumed and checked right away.":                       "▶️ <b>%s</b> dilanjutkan dan segera diperiksa.",
		"▶️ Maintenance on <b>%s</b> ended; the site is checked right away.": "▶️ Pemeliharaan <b>%s</b> diakhiri; situs segera diperiksa.",
		"Unknown command. Send /help for the list of commands.":              "Perintah tidak dikenal. Kirim /help untuk daftar perintah.",
		"⚠️ Use <code>/report daily</code> or <code>/report weekly</code>.":  "⚠️ Pakai <code>/laporan harian</code> atau <code>/laporan mingguan</code>.",
		"⚠️ Site not found.":                          "⚠️ Situs tidak ditemukan.",
		"⚠️ An internal error occurred.":              "⚠️ Terjadi kesalahan internal.",
		"No sites yet. Add one with /add <i>url</i>.": "Belum ada situs. Tambahkan dengan /tambah <i>url</i>.",
		"<b>%d site</b>: 🟢 %d up · 🔴 %d down · ⏸ %d paused · 🔧 %d maintenance":  "<b>%d situs</b>: 🟢 %d up · 🔴 %d down · ⏸ %d dijeda · 🔧 %d pemeliharaan",
		"<b>%d sites</b>: 🟢 %d up · 🔴 %d down · ⏸ %d paused · 🔧 %d maintenance": "<b>%d situs</b>: 🟢 %d up · 🔴 %d down · ⏸ %d dijeda · 🔧 %d pemeliharaan",
		" · ⚪ %d pending":                                                                " · ⚪ %d menunggu",
		"All active sites are healthy.":                                                  "Semua situs yang aktif sehat.",
		"… and %d more site (see the dashboard)":                                         "… dan %d situs lainnya (lihat dashboard)",
		"… and %d more sites (see the dashboard)":                                        "… dan %d situs lainnya (lihat dashboard)",
		"Name the site: its id, name, or part of a name. Send /list to see them.":        "Sebutkan situsnya: id, nama, atau sebagian nama. Kirim /list untuk daftar.",
		"No site matches “%s”. Send /list to see them.":                                  "Tidak ada situs yang cocok dengan “%s”. Kirim /list untuk daftar.",
		"%d sites match “%s”: %s.\nUse the id to be certain.":                            "Ada %d situs yang cocok dengan “%s”: %s.\nPakai id-nya agar pasti.",
		"✅ <b>%s</b>: HTTP %d · %d ms\n<i>Manual check: does not change the status.</i>": "✅ <b>%s</b>: HTTP %d · %d ms\n<i>Pemeriksaan manual: tidak mengubah status.</i>",
		"❌ <b>%s</b>: %s\n%s\n<i>Manual check: does not change the status.</i>":          "❌ <b>%s</b>: %s\n%s\n<i>Pemeriksaan manual: tidak mengubah status.</i>",

		"Usage: /maintenance <i>site</i> <i>duration</i>, e.g. <code>/maintenance shop 2h</code> (30m, 2h, 1d).":                     "Pemakaian: /pemeliharaan <i>situs</i> <i>durasi</i>, mis. <code>/pemeliharaan toko 2j</code> (30m, 2j, 1d).",
		"⚠️ Maintenance can last at most %d day.":                                                                                    "⚠️ Pemeliharaan paling lama %d hari.",
		"⚠️ Maintenance can last at most %d days.":                                                                                   "⚠️ Pemeliharaan paling lama %d hari.",
		"🔧 <b>%s</b> is under maintenance until %s. No checks and no notifications; it resumes by itself afterwards (or /endmaint).": "🔧 <b>%s</b> dalam pemeliharaan sampai %s. Tidak diperiksa dan tidak ada notifikasi; lanjut sendiri setelahnya (atau /akhiri).",

		"Usage: /add <i>url</i> [name], e.g. <code>/add shop.example.com Online Shop</code>.":                                                                           "Pemakaian: /tambah <i>url</i> [nama], mis. <code>/tambah toko.contoh.com Toko Online</code>.",
		"⚠️ Scheduled jobs are created from the dashboard: their ping address holds a secret that does not belong in a chat.":                                           "⚠️ Pekerjaan terjadwal dibuat dari dashboard: alamat pingnya berisi rahasia yang tidak pantas dikirim lewat chat.",
		"✅ <b>%s</b> added and checked right away.\n%s\nid: <code>%s</code>\n<i>Private/internal addresses are blocked; other options can be set in the dashboard.</i>": "✅ <b>%s</b> ditambahkan dan segera diperiksa.\n%s\nid: <code>%s</code>\n<i>Alamat privat/internal diblokir; opsi lain bisa diatur di dashboard.</i>",

		"Usage: /remove <i>site</i> yes": "Pemakaian: /hapus <i>situs</i> ya",
		"Removing <b>%s</b> also deletes its entire uptime history and cannot be undone.\nSend <code>/remove %s yes</code> to continue.": "Menghapus <b>%s</b> juga menghapus seluruh riwayat uptimenya dan tidak bisa dibatalkan.\nKirim <code>/hapus %s ya</code> untuk melanjutkan.",
		"🗑 <b>%s</b> removed.": "🗑 <b>%s</b> dihapus.",
	})
}
