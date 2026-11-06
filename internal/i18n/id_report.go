package i18n

// Indonesian wording of the daily and weekly report (internal/report).
func init() {
	Register("id", map[string]string{
		"… and %d more (see the dashboard)": "… dan %d lainnya (lihat dashboard)",

		// month abbreviations, shown in the report title and in "since" lines
		"Jan": "Jan", "Feb": "Feb", "Mar": "Mar", "Apr": "Apr", "May": "Mei", "Jun": "Jun",
		"Jul": "Jul", "Aug": "Agu", "Sep": "Sep", "Oct": "Okt", "Nov": "Nov", "Dec": "Des",

		"Daily report · %s":       "Laporan harian · %s",
		"Daily report · %s · %s":  "Laporan harian · %s · %s",
		"Weekly report · %s":      "Laporan mingguan · %s",
		"Weekly report · %s · %s": "Laporan mingguan · %s · %s",
		"24 hours until %s":       "24 jam sampai %s",

		"%d sites": "%d situs",
		"1 site":   "1 situs",
		// the status words after an icon: "🟢 3 healthy"
		"healthy":         "sehat",
		"down":            "down",
		"in maintenance":  "pemeliharaan",
		"paused":          "dijeda",
		"not checked yet": "belum diperiksa",

		"No sites are being monitored yet.": "Belum ada situs yang dipantau.",
		"Average uptime: %s":                "Rata-rata uptime: %s",
		"✅ No incidents in this period.":    "✅ Tidak ada gangguan pada periode ini.",
		"Incidents: %s, total downtime %s":  "Gangguan: %s kali, total down %s",
		"Longest: %s — %s":                  "Terlama: %s — %s",

		"Still down now":             "Masih down sekarang",
		"Lowest uptime":              "Uptime terendah",
		"Certificates expiring soon": "Sertifikat segera habis",
		"Slowest (24-hour average)":  "Paling lambat (rata-rata 24 jam)",

		"just now":        "baru saja",
		"since %s":        "sejak %s",
		"already expired": "sudah kedaluwarsa",
		// also used by internal/notify
		"less than 1 day": "kurang dari 1 hari",
		"1 day":           "1 hari",
		"%d days":         "%d hari",
	})
}
