package dateutil

import (
	"testing"
	"time"
)

func TestFromTimeAgo(t *testing.T) {
	now := time.Date(2020, 6, 15, 12, 0, 0, 0, time.Local)
	tests := []struct {
		in   string
		want time.Duration // offset from now
	}{
		{"1 min ago", -time.Minute},
		{"2 hours ago", -2 * time.Hour},
		{"1 day 2 hours ago", -26 * time.Hour},
		{"3 weeks ago", -21 * 24 * time.Hour},
		{"1 month ago", -30 * 24 * time.Hour},
		{"1 year ago", -365 * 24 * time.Hour},
		{"just now", 0},
	}
	for _, tt := range tests {
		got, err := FromTimeAgo(tt.in, now)
		if err != nil {
			t.Errorf("FromTimeAgo(%q): %v", tt.in, err)
			continue
		}
		if got.Sub(now) != tt.want {
			t.Errorf("FromTimeAgo(%q) offset = %v, want %v", tt.in, got.Sub(now), tt.want)
		}
	}
	if _, err := FromTimeAgo("5 quuxes ago", now); err == nil {
		t.Errorf("unknown unit must error")
	}
}

func TestParseDateTimeGoLang(t *testing.T) {
	now := time.Date(2020, 6, 15, 12, 0, 0, 0, time.Local)
	got, err := ParseDateTimeGoLang("15-06-2020 14:30", "02-01-2006 15:04", now)
	if err != nil {
		t.Fatalf("ParseDateTimeGoLang: %v", err)
	}
	if got.Year() != 2020 || got.Month() != 6 || got.Day() != 15 || got.Hour() != 14 || got.Minute() != 30 {
		t.Errorf("got %v", got)
	}
	// yearless layout in the future resolves to the past year
	got, err = ParseDateTimeGoLang("12-31 23:59", "01-02 15:04", now)
	if err != nil {
		t.Fatalf("ParseDateTimeGoLang yearless: %v", err)
	}
	if got.Year() != 2019 {
		t.Errorf("yearless future date = %v, want 2019", got.Year())
	}
}

func TestFromUnknown(t *testing.T) {
	now := time.Date(2020, 6, 17, 12, 0, 0, 0, time.Local) // a Wednesday
	tests := []struct {
		in   string
		want time.Time
	}{
		{"1600000000", time.Unix(1600000000, 0).Local()},
		{"yesterday at 10:00", time.Date(2020, 6, 16, 10, 0, 0, 0, time.Local)},
		{"today at 08:30", time.Date(2020, 6, 17, 8, 30, 0, 0, time.Local)},
		{"tomorrow at 08:30", time.Date(2020, 6, 18, 8, 30, 0, 0, time.Local)},
		{"Monday at 09:15", time.Date(2020, 6, 15, 9, 15, 0, 0, time.Local)},
		{"2020-06-10 14:22:33", time.Date(2020, 6, 10, 14, 22, 33, 0, time.Local)},
	}
	for _, tt := range tests {
		got, err := FromUnknown(tt.in, "", now)
		if err != nil {
			t.Errorf("FromUnknown(%q): %v", tt.in, err)
			continue
		}
		if !got.Equal(tt.want) {
			t.Errorf("FromUnknown(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestUnixTimestampRoundTrip(t *testing.T) {
	if got := DateTimeToUnixTimestamp(UnixTimestampToDateTime(1600000000)); got != 1600000000 {
		t.Errorf("round trip = %d", got)
	}
}
