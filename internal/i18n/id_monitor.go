package i18n

// Indonesian wording of what internal/monitor, internal/schedule and internal/model tell people.
func init() {
	Register("id", map[string]string{
		// model: short names of diagnosis kinds
		"Healthy": "Sehat", "DNS failed": "DNS gagal", "Connection refused": "Koneksi ditolak", "Unreachable": "Tidak terjangkau",
		"Connection reset": "Koneksi diputus", "Connect timeout": "Timeout menyambung", "TLS timeout": "Timeout TLS",
		"Response timeout": "Timeout respons", "Certificate expired": "Sertifikat kedaluwarsa",
		"Certificate name mismatch": "Nama sertifikat tidak cocok", "Certificate not trusted": "Sertifikat tidak tepercaya", "TLS error": "Kesalahan TLS",
		"Redirect loop": "Loop redirect", "Address blocked": "Alamat diblokir", "Cloudflare error": "Error Cloudflare",
		"Server error (5xx)": "Error server (5xx)", "Access error (4xx)": "Error akses (4xx)", "Unexpected status": "Status tidak sesuai",
		"Text not found": "Teks tidak ditemukan", "Error": "Kesalahan",
		"Ping not received": "Ping tidak diterima", "Job reported failure": "Pekerjaan melaporkan gagal",

		// monitor
		"The job called /fail: it knows its own run did not succeed. Check that job's log.":              "Pekerjaan memanggil /fail: ia sendiri tahu bahwa prosesnya tidak berhasil. Lihat log pekerjaan itu.",
		"No ping within %s (interval + grace). The job may have failed, not run, or cannot reach ronda.": "Tidak ada ping dalam %s (interval + toleransi). Pekerjaan mungkin gagal, tidak berjalan, atau tidak bisa menjangkau ronda.",
		"%d days":    "%d hari",
		"%d hours":   "%d jam",
		"%d minutes": "%d menit",
		"%d seconds": "%d detik",
		"1 second":   "1 detik",
		"Renew the certificate (e.g. certbot renew) and reload the web server before it expires.": "Perpanjang sertifikat (mis. certbot renew) lalu reload web server sebelum kedaluwarsa.",
		"A push site is not checked from here: it waits for pings from its job.":                  "Situs push tidak diperiksa dari sini: ia menunggu ping dari pekerjaannya.",

		// schedule
		"the start time must look like HH:MM":                                    "jam mulai harus berbentuk HH:MM",
		"the start time must look like HH:MM, between 00:00 and 23:59":           "jam mulai harus berbentuk HH:MM antara 00:00 dan 23:59",
		"at most %d maintenance windows":                                         "maksimal %d jendela pemeliharaan",
		"window %d: choose at least one day":                                     "jendela %d: pilih minimal satu hari",
		"window %d: days must be 0 (Sunday) to 6 (Saturday), without duplicates": "jendela %d: hari harus 0 (Minggu) sampai 6 (Sabtu), tanpa duplikat",
		"window %d": "jendela %d",
		"window %d: the duration must be 1..%d minutes": "jendela %d: durasi harus 1..%d menit",
	})
}
