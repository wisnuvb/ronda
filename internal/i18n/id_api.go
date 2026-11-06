package i18n

// Indonesian for the HTTP API (internal/api) and password handling (internal/auth).

func init() {
	Register("id", map[string]string{
		"This is a read-only demo: changes are turned off.":                                                           "Ini demo baca-saja: perubahan dimatikan.",
		"Invalid CSRF token; reload the page":                                                                         "token CSRF tidak valid; muat ulang halaman",
		"Dashboard login is not enabled: set auth.password_hash in the config (create one with: ronda hash-password)": "login dashboard belum diaktifkan: isi auth.password_hash di config (buat dengan: ronda hash-password)",
		"Request origin does not match":                                                                               "asal permintaan tidak cocok",
		"Wrong username or password":                                                                                  "nama pengguna atau kata sandi salah",
		"Status page is not enabled":                                                                                  "halaman status tidak aktif",
		"Internal error":                                                                                              "kesalahan internal",
		"There is no dashboard login whose password can be changed":                                                   "tidak ada login dashboard yang bisa diubah kata sandinya",
		"The password can only be changed from a dashboard login session, not with the API token":                     "kata sandi hanya bisa diganti dari sesi login dashboard, bukan dengan token API",
		"Data directory is not set, so the new password cannot be saved":                                              "folder data tidak diatur, jadi kata sandi baru tidak bisa disimpan",
		"Current password is wrong":                                                                                   "kata sandi saat ini salah",
		"New password must differ from the current one":                                                               "kata sandi baru harus berbeda dari yang sekarang",
		"The new password could not be saved":                                                                         "kata sandi baru tidak bisa disimpan",
		"File is too large or unreadable":                                                                             "berkas terlalu besar atau tidak terbaca",
		"Body is too large or unreadable":                                                                             "body terlalu besar atau tidak terbaca",
		"Too many wrong tokens":                                                                                       "terlalu banyak token salah",
		"Unknown token":                                                                                               "token tidak dikenal",
		"Push sites are not checked from here: they wait for a ping from their job":                                   "situs push tidak diperiksa dari sini: ia menunggu ping dari pekerjaannya",
		"limit must be 1..200":                                                                                        "limit harus 1..200",
		"before must be a unix time in milliseconds":                                                                  "before harus berupa waktu unix dalam milidetik",
		"Not logged in": "belum masuk",
		"Invalid JSON: trailing data after the object":              "JSON tidak valid: data berlebih setelah objek",
		"Too many failed attempts; try again in %d minutes":         "terlalu banyak percobaan gagal; coba lagi dalam %d menit",
		"New password must be at least %d characters":               "kata sandi baru minimal %d karakter",
		"minutes must be 1..%d":                                     "minutes harus 1..%d",
		"Invalid JSON: %s":                                          "JSON tidak valid: %s",
		"Password must be at least %d characters":                   "kata sandi minimal %d karakter",
		"Invalid iteration count":                                   "jumlah iterasi tidak valid",
		"Unknown hash format; create one with: ronda hash-password": "format hash tidak dikenal; buat dengan: ronda hash-password",
		"Invalid hash iteration count":                              "jumlah iterasi hash tidak valid",
		"Corrupt hash":                                              "hash rusak",
		"Data directory is not set":                                 "folder data tidak diatur",
	})
}
