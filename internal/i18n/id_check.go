package i18n

// Indonesian wording of what internal/check tells people: the diagnosis hints and the DNS/TCP texts.
func init() {
	Register("id", map[string]string{
		"too many redirects": "terlalu banyak pengalihan",

		// diagnose.go: transport errors
		"The destination address (%s) is on a private/internal network, so it was blocked. If that is intentional, enable allow_private for this site.": "Alamat tujuan (%s) ada di jaringan privat/internal sehingga diblokir. Bila memang disengaja, aktifkan allow_private untuk situs ini.",
		"More than %d consecutive redirects: probably a redirect loop (e.g. http↔https or www↔non-www pointing at each other).":                         "Lebih dari %d pengalihan beruntun: kemungkinan loop redirect (mis. http↔https atau www↔non-www yang saling mengarahkan).",
		"The TLS certificate is not valid yet (starts %s). Check the server clock.":                                                                     "Sertifikat TLS belum berlaku (mulai %s). Periksa jam server.",
		"The TLS certificate expired on %s. Renew it (e.g. certbot renew) and reload the web server.":                                                   "Sertifikat TLS kedaluwarsa pada %s. Perpanjang sertifikat (mis. certbot renew) lalu reload web server.",
		"The TLS certificate was rejected: %s": "Sertifikat TLS ditolak: %s",
		"The TLS certificate does not match the domain name. Check that the vhost uses the right certificate. (%s)":                            "Sertifikat TLS tidak cocok dengan nama domain. Periksa apakah vhost memakai sertifikat yang benar. (%s)",
		"The TLS certificate is not trusted (self-signed, unknown issuer, or the intermediate certificate chain is incomplete on the server).": "Sertifikat TLS tidak tepercaya (self-signed, penerbit tidak dikenal, atau rantai sertifikat intermediate tidak lengkap di server).",
		"The domain name was not found (NXDOMAIN). Check the A/AAAA records in DNS, or whether the domain has expired.":                        "Nama domain tidak ditemukan (NXDOMAIN). Periksa catatan A/AAAA di DNS, atau apakah domain sudah kedaluwarsa.",
		"The DNS server did not answer in time. Check the domain's nameservers or the monitor's DNS connectivity.":                             "Server DNS tidak menjawab tepat waktu. Periksa nameserver domain atau konektivitas DNS monitor.",
		"DNS resolution failed (%s). Check the domain's nameservers.":                                                                          "Resolusi DNS gagal (%s). Periksa nameserver domain.",
		"Connection refused: the server is reachable but nothing is listening on that port (web server down or wrong port).":                   "Koneksi ditolak: server terjangkau tetapi tidak ada layanan yang mendengarkan di port itu (web server mati atau port salah).",
		"The connection was forcibly closed by the server (reset). The web server or a firewall closed it.":                                    "Koneksi diputus paksa oleh server (reset). Web server atau firewall menutup koneksi.",
		"The server address is unreachable from the monitor (routing or firewall problem).":                                                    "Alamat server tidak terjangkau dari monitor (masalah rute atau firewall).",
		"Could not connect within %d s: the server is not answering (down, a firewall dropping packets, or the network is cut).":               "Tidak bisa tersambung dalam %d dtk: server tidak menjawab (mati, firewall menjatuhkan paket, atau jaringan terputus).",
		"Connected, but the TLS handshake did not finish within %d s. The web server is stuck or overloaded.":                                  "Tersambung, tetapi negosiasi TLS tidak selesai dalam %d dtk. Web server macet atau kelebihan beban.",
		"Connected, but no response came within %d s: the application/backend is stuck or overloaded.":                                         "Tersambung, tetapi respons tidak datang dalam %d dtk: aplikasi/backend macet atau kelebihan beban.",
		"The server closed the connection without sending a complete response (process crash or misconfiguration).":                            "Server menutup koneksi tanpa mengirim respons lengkap (proses crash atau salah konfigurasi).",
		"TLS handshake failed: %s": "Negosiasi TLS gagal: %s",
		"Request failed: %s":       "Permintaan gagal: %s",

		// diagnose.go: HTTP statuses
		"Cloudflare 520: the origin sent an invalid response or dropped the connection. Check the web server/application logs.":                "Cloudflare 520: origin mengirim respons yang tidak valid atau memutus koneksi. Periksa log web server/aplikasi.",
		"Cloudflare 521: the origin refused the connection from Cloudflare (web server down, or port 80/443 closed or blocked by a firewall).": "Cloudflare 521: origin menolak koneksi dari Cloudflare (web server mati atau port 80/443 tertutup/diblokir firewall).",
		"Cloudflare 522: Cloudflare's connection to the origin timed out (server down, overloaded, or a firewall blocking Cloudflare's IPs).":  "Cloudflare 522: koneksi Cloudflare ke origin timeout (server mati, overload, atau firewall memblokir IP Cloudflare).",
		"Cloudflare 523: the origin is unreachable (wrong origin IP in DNS or a broken route).":                                                "Cloudflare 523: origin tidak terjangkau (IP origin di DNS salah atau rute putus).",
		"Cloudflare 524: the origin accepted the connection but did not reply in time (application stuck or too slow).":                        "Cloudflare 524: origin menerima koneksi tetapi tidak membalas tepat waktu (aplikasi macet atau terlalu lambat).",
		"Cloudflare 525: the SSL handshake between Cloudflare and the origin failed. Check the origin's certificate and TLS configuration.":    "Cloudflare 525: handshake SSL antara Cloudflare dan origin gagal. Periksa sertifikat dan konfigurasi TLS origin.",
		"Cloudflare 526: the origin's SSL certificate is invalid (Full strict mode). Renew or fix the origin certificate.":                     "Cloudflare 526: sertifikat SSL origin tidak valid (mode Full strict). Perpanjang atau perbaiki sertifikat origin.",
		"Cloudflare 530 (error 1xxx): usually a DNS/zone problem or an unreachable origin. See the 1xxx error code on the Cloudflare page.":    "Cloudflare 530 (error 1xxx): biasanya masalah DNS/zone atau origin tidak terjangkau. Lihat kode error 1xxx di halaman Cloudflare.",
		"502 Bad Gateway: the reverse proxy (e.g. nginx) is up but the backend is not answering or has crashed.":                               "502 Bad Gateway: reverse proxy (mis. nginx) hidup tetapi backend tidak menjawab atau crash.",
		"503 Service Unavailable: the service is overloaded, under maintenance, or the backend is down.":                                       "503 Service Unavailable: layanan kelebihan beban, sedang maintenance, atau backend mati.",
		"504 Gateway Timeout: the backend took too long to answer the reverse proxy.":                                                          "504 Gateway Timeout: backend terlalu lambat membalas reverse proxy.",
		"401: the page requires a login. Add expect_status 401 if that is what you expect, or monitor a public path.":                          "401: halaman butuh login. Tambahkan expect_status 401 bila memang itu yang diharapkan, atau pantau path publik.",
		"403: access denied. A WAF/Cloudflare may be blocking the monitor; allow the monitor's IP or set a custom user_agent.":                 "403: akses ditolak. Bisa jadi WAF/Cloudflare memblokir monitor; izinkan IP monitor atau atur user_agent khusus.",
		"404: page not found. Check the path in the URL.":                                                                                      "404: halaman tidak ditemukan. Periksa path pada URL.",
		"429: the monitor is being rate limited. Increase the check interval or allow the monitor's IP.":                                       "429: monitor terkena rate limit. Naikkan interval pemeriksaan atau izinkan IP monitor.",
		"The server returned HTTP %d: an error on the application side. Check the application logs.":                                           "Server mengembalikan HTTP %d: error di sisi aplikasi. Periksa log aplikasi.",
		"The server returned HTTP %d. Check the URL and access settings.":                                                                      "Server mengembalikan HTTP %d. Periksa URL dan pengaturan akses.",
		"HTTP %d is not what was expected. Adjust expect_status if this is normal.":                                                            "HTTP %d tidak sesuai harapan. Sesuaikan expect_status bila ini memang normal.",

		// check.go
		"A push site waits for pings from its job and is not checked from here.":                                                  "Situs push menunggu ping dari pekerjaannya dan tidak diperiksa dari sini.",
		"The URL cannot be used to make a request.":                                                                               "URL tidak bisa dipakai untuk membuat request.",
		"The page opened (HTTP %d) but the text %q is not in the first 1 MB: the application may be failing or the page changed.": "Halaman terbuka (HTTP %d) tetapi teks %q tidak ada di 1 MB pertama: aplikasi mungkin error atau halaman berubah.",

		// netcheck.go
		"A tcp address must look like tcp://host:port.":                                                                                        "Alamat tcp harus berbentuk tcp://host:port.",
		"Connection refused: the server is reachable but nothing is listening on that port (service down or wrong port).":                      "Koneksi ditolak: server terjangkau tetapi tidak ada layanan yang mendengarkan di port itu (layanan mati atau port salah).",
		"The %s record of %s answered %q, but %q is not in it: the record changed, points to the wrong place, or has not propagated yet.":      "Rekaman %s %s menjawab %q, tetapi %q tidak ada di dalamnya: rekaman berubah, salah diarahkan, atau belum menyebar.",
		"The DNS server (%s) is on a private/internal network, so it was blocked. If that is intentional, enable allow_private for this site.": "Server DNS (%s) ada di jaringan privat/internal sehingga diblokir. Bila memang disengaja, aktifkan allow_private untuk situs ini.",
		"There is no %s record for %s: the name does not exist (NXDOMAIN) or has no record of that type. Check the domain's DNS settings.":     "Tidak ada rekaman %s untuk %s: nama tidak ada (NXDOMAIN) atau tidak punya rekaman jenis itu. Periksa pengaturan DNS domain.",
		"The DNS server %s did not answer within %d s: the server is down, blocked by a firewall, or the network is cut.":                      "Server DNS %s tidak menjawab dalam %d dtk: server mati, diblokir firewall, atau jaringan terputus.",
		"The DNS server did not answer within %d s: the server is down, blocked by a firewall, or the network is cut.":                         "Server DNS tidak menjawab dalam %d dtk: server mati, diblokir firewall, atau jaringan terputus.",
		"The DNS server failed to answer (%s). Check the domain's nameservers.":                                                                "Server DNS gagal menjawab (%s). Periksa nameserver domain.",
	})
}
