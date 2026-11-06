package i18n

// Indonesian: config validation messages and defaults.
func init() {
	Register("id", map[string]string{
		"Service status": "Status Layanan",
		"api.token is required (at least %d characters, no spaces); create one with: ronda gen-token":                        "api.token wajib diisi (minimal %d karakter, tanpa spasi); buat dengan: ronda gen-token",
		"cert_warn_days must hold numbers 1..365":                                                                            "cert_warn_days harus berisi angka 1..365",
		"event_retention_days must be 7..3650":                                                                               "event_retention_days harus 7..3650",
		"heartbeat_interval_sec must be 10..3600":                                                                            "heartbeat_interval_sec harus 10..3600",
		"heartbeat_url must be an http:// or https:// address":                                                               "heartbeat_url harus berupa alamat http:// atau https://",
		"max_concurrent_checks must be 1..500":                                                                               "max_concurrent_checks harus 1..500",
		"notify.batch_sec must be 0..120":                                                                                    "notify.batch_sec harus 0..120",
		"notify.digest_min must be 2..50":                                                                                    "notify.digest_min harus 2..50",
		"notify.recipients allows at most 50 recipients":                                                                     "notify.recipients maksimal 50 penerima",
		"notify.recipients needs notify.telegram_token":                                                                      "notify.recipients butuh notify.telegram_token",
		"notify.recipients: %q already means telegram_chat_id; use another name":                                             "notify.recipients: %q sudah berarti telegram_chat_id; pakai nama lain",
		"notify.recipients: name %q is invalid (lowercase letters, digits, - or _; max 32)":                                  "notify.recipients: nama %q tidak valid (huruf kecil, angka, - atau _; maks 32)",
		"notify.recipients[%q]: give at least one chat id":                                                                   "notify.recipients[%q]: isi minimal satu chat id",
		"notify.remind_after_min must hold minutes 5..43200 (30 days); %d is outside that":                                   "notify.remind_after_min harus berisi menit 5..43200 (30 hari); %d di luar itu",
		"notify.report_weekly_day must be 0 (Sunday) to 6 (Saturday)":                                                        "notify.report_weekly_day harus 0 (Minggu) sampai 6 (Sabtu)",
		"notify.telegram_admin_ids: %d is not a Telegram user id (a positive number; a negative group id is misplaced here)": "notify.telegram_admin_ids: %d bukan id pengguna Telegram (angka positif; id grup yang negatif salah tempat)",
		"notify.telegram_commands needs notify.telegram_token and notify.telegram_chat_id":                                   "notify.telegram_commands butuh notify.telegram_token dan notify.telegram_chat_id",
		"notify.telegram_token and notify.telegram_chat_id must be set together":                                             "notify.telegram_token dan notify.telegram_chat_id harus diisi bersamaan",
		"periodic reports need notify.telegram_token or notify.webhook_url: there is nowhere to send them":                   "laporan berkala butuh notify.telegram_token atau notify.webhook_url: tidak ada tujuan pengirimannya",
		"status_page.title must be at most 100 and description at most 300 characters, with no control characters":           "status_page.title maksimal 100 dan description maksimal 300 karakter, tanpa karakter kontrol",
	})
}
