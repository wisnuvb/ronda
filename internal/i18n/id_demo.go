package i18n

// Indonesian: the `ronda demo` sample data (site names, failure texts, status page).
func init() {
	Register("id", map[string]string{
		"502 Bad Gateway: the reverse proxy (e.g. nginx) is up but the backend is not answering or has crashed.": "502 Bad Gateway: reverse proxy (mis. nginx) hidup tetapi backend tidak menjawab atau crash.",
		"Campaign Landing Page": "Landing Kampanye",
		"Cloudflare 522: Cloudflare's connection to the origin timed out (server down, overloaded, or a firewall is blocking Cloudflare's IPs).": "Cloudflare 522: koneksi Cloudflare ke origin timeout (server mati, overload, atau firewall memblokir IP Cloudflare).",
		"Company Blog": "Blog Perusahaan",
		"Connected, but no response arrived within 10 s: the application or backend is stuck or overloaded.":                     "Tersambung, tetapi respons tidak datang dalam 10 dtk: aplikasi/backend macet atau kelebihan beban.",
		"Connection refused: the server is reachable but nothing is listening on that port (web server stopped, or wrong port).": "Koneksi ditolak: server terjangkau tetapi tidak ada layanan yang mendengarkan di port itu (web server mati atau port salah).",
		"Connection refused: the server is reachable but nothing is listening on that port.":                                     "Koneksi ditolak: server terjangkau tetapi tidak ada layanan yang mendengarkan di port itu.",
		"DNS example.com":                     "DNS contoh.id",
		"Daily Backup":                        "Backup Harian",
		"Documentation":                       "Dokumentasi",
		"Employee Portal":                     "Portal Karyawan",
		"Example service status":              "Status Layanan Contoh",
		"Internal Wiki":                       "Wiki Internal",
		"News Site":                           "Situs Berita",
		"Online Shop":                         "Toko Online",
		"Payments API":                        "API Pembayaran",
		"Production PostgreSQL":               "PostgreSQL Produksi",
		"The latest news about our services.": "Kabar terbaru tentang layanan kami.",
		"Weekly Report":                       "Laporan Mingguan",
	})
}
