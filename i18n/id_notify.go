package i18n

// Indonesian wording of the messages in internal/notify (Telegram and webhook events, digests, durations).
func init() {
	Register("id", map[string]string{
		"… and %d more (see the dashboard)": "… dan %d lainnya (lihat dashboard)",

		// digests
		"🔴 <b>%d sites DOWN at about the same time</b>":                                                                  "🔴 <b>%d situs DOWN hampir bersamaan</b>",
		"Many sites failed at once: the likely cause is shared (server, network, DNS, or CDN), not each site.\nSince %s": "Banyak situs gagal sekaligus: kemungkinan penyebabnya bersama (server, jaringan, DNS, atau CDN), bukan masing-masing situs.\nSejak %s",
		"⏰ <b>%d sites STILL DOWN</b>":                                                                                   "⏰ <b>%d situs MASIH DOWN</b>",
		"%s — %s, down for %s":                                                                                           "%s — %s, sudah %s",
		"None have recovered since %s.":                                                                                  "Belum ada yang pulih sejak %s.",
		"🟢 <b>%d sites RECOVERED</b>":                                                                                    "🟢 <b>%d situs PULIH</b>",
		"%s — was down for %s":                                                                                           "%s — down %s",
		"[DOWN] %d sites down at about the same time":                                                                    "[DOWN] %d situs down hampir bersamaan",
		"Likely a shared cause (server, network, DNS, or CDN). Since %s":                                                 "Kemungkinan penyebab bersama (server, jaringan, DNS, atau CDN). Sejak %s",
		"[STILL DOWN] %d sites have not recovered":                                                                       "[MASIH DOWN] %d situs belum pulih",
		"Since %s":                       "Sejak %s",
		"[RECOVERED] %d sites recovered": "[PULIH] %d situs pulih",

		// one event, Telegram (HTML)
		"🔴 <b>DOWN</b> — %s\n%s":                             "🔴 <b>DOWN</b> — %s\n%s",
		"<b>Cause:</b> %s":                                   "<b>Penyebab:</b> %s",
		"Failed %d times in a row · since %s":                "Gagal %dx berturut-turut · sejak %s",
		"⏰ <b>STILL DOWN</b> — %s\n%s":                       "⏰ <b>MASIH DOWN</b> — %s\n%s",
		"<b>Cause now:</b> %s":                               "<b>Penyebab sekarang:</b> %s",
		"Down for <b>%s</b> · since %s":                      "Sudah down selama <b>%s</b> · sejak %s",
		"🟢 <b>RECOVERED</b> — %s\n%s":                        "🟢 <b>PULIH</b> — %s\n%s",
		"Was down for %s · response %d ms":                   "Down selama %s · respons %d ms",
		"🟠 <b>CERTIFICATE</b> — %s\n%s":                      "🟠 <b>SERTIFIKAT</b> — %s\n%s",
		"The TLS certificate expires in <b>%s</b> (%s).\n%s": "Sertifikat TLS kedaluwarsa dalam <b>%s</b> (%s).\n%s",
		"✅ <b>%s</b>: test notification delivered · %s":      "✅ <b>%s</b>: tes notifikasi berhasil · %s",

		// one event, plain text (webhook)
		"[DOWN] %s — %s\nCause: %s\nFailed %d times in a row · since %s": "[DOWN] %s — %s\nPenyebab: %s\nGagal %dx berturut-turut · sejak %s",
		"[STILL DOWN] %s — %s\nCause now: %s\nDown for %s · since %s":    "[MASIH DOWN] %s — %s\nPenyebab sekarang: %s\nSudah down selama %s · sejak %s",
		"[RECOVERED] %s — %s\nWas down for %s · response %d ms":          "[PULIH] %s — %s\nDown selama %s · respons %d ms",
		"[CERTIFICATE] %s — %s\nExpires in %s (%s). %s":                  "[SERTIFIKAT] %s — %s\nKedaluwarsa dalam %s (%s). %s",
		"[TEST] %s · %s": "[TES] %s · %s",

		// time left and durations
		"less than 1 day": "kurang dari 1 hari",
		"1 day":           "1 hari",
		"%d days":         "%d hari",
		"%d s":            "%d dtk",
		"%d min %d s":     "%d mnt %d dtk",
		"%d h %d min":     "%d jam %d mnt",
		"%d d %d h":       "%d hari %d jam",
	})
}
