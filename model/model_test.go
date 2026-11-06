package model

import (
	"strings"
	"testing"
	"time"
)

func TestSetTimezone(t *testing.T) {
	t.Cleanup(func() { _ = SetTimezone("UTC") })
	for _, ok := range []string{"", "UTC", "Europe/Berlin", "America/New_York", "Asia/Jakarta", "Asia/Kolkata"} {
		if err := SetTimezone(ok); err != nil {
			t.Errorf("%q should be accepted: %v", ok, err)
		}
	}
	if err := SetTimezone("Asia/Kolkata"); err != nil || Stamp(0, "15:04") != "05:30 IST" {
		t.Errorf("a half-hour zone: %v %q", err, Stamp(0, "15:04"))
	}
	for _, bad := range []string{"Local", "Mars/Olympus", "WIB", "+07:00", "europe/berlin "} {
		if err := SetTimezone(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		} else if !strings.Contains(err.Error(), "IANA") && !strings.Contains(err.Error(), "Local") {
			t.Errorf("%q: the error should say what to write instead: %v", bad, err)
		}
	}
	if Loc().String() != "Asia/Kolkata" {
		t.Errorf("a refused zone must leave the previous one in place, got %s", Loc())
	}
}

func TestMidnightAndClockOnAroundDaylightSaving(t *testing.T) {
	t.Cleanup(func() { _ = SetTimezone("UTC") })
	if err := SetTimezone("America/New_York"); err != nil {
		t.Fatal(err)
	}
	ny := Loc()
	spring := time.Date(2026, 3, 8, 15, 0, 0, 0, ny)
	if m := Midnight(spring); m.Hour() != 0 || m.Day() != 8 {
		t.Errorf("midnight = %s", m)
	}
	if got := ClockOn(spring, 8*60); got.Hour() != 8 || got.Day() != 8 {
		t.Errorf("08:00 on the spring-forward day = %s", got)
	}
	if got := Midnight(spring.AddDate(0, 0, 1)).Sub(Midnight(spring)); got != 23*time.Hour {
		t.Errorf("the spring-forward day is %s long, want 23h", got)
	}
}
