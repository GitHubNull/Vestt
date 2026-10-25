// Package dateutil ports Jackett.Common/Utils/DateTimeUtil.cs. All functions
// return local time, mirroring upstream.
package dateutil

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vestt/common/utils/parseutil"
)

// RFC1123ZPattern is the upstream Rfc1123ZPattern wire format.
const RFC1123ZPattern = "ddd, dd MMM yyyy HH':'mm':'ss z"

var (
	timeAgoRe      = regexp.MustCompile(`(?i)\bago`)
	todayRe        = regexp.MustCompile(`(?i)\btoday(?:[\s,]+(?:at){0,1}\s*|[\s,]*|$)`)
	tomorrowRe     = regexp.MustCompile(`(?i)\btomorrow(?:[\s,]+(?:at){0,1}\s*|[\s,]*|$)`)
	yesterdayRe    = regexp.MustCompile(`(?i)\byesterday(?:[\s,]+(?:at){0,1}\s*|[\s,]*|$)`)
	daysOfWeekRe   = regexp.MustCompile(`(?i)\b(monday|tuesday|wednesday|thursday|friday|saturday|sunday)\s+at\s+`)
	missingYearRe  = regexp.MustCompile(`^(\d{1,2}-\d{1,2})(\s|$)`)
	missingYearRe2 = regexp.MustCompile(`^(\d{1,2}\s+\w{3})\s+(\d{1,2}\:\d{1,2}.*)$`)
	timeAgoPartsRe = regexp.MustCompile(`\s*?([\d\.]+)\s*?([^\d\s\.]+)\s*?`)
	allDigitsRe    = regexp.MustCompile(`^\d+$`)
)

// UnixTimestampToDateTime mirrors upstream (seconds → local time).
func UnixTimestampToDateTime(unixTime int64) time.Time {
	return time.Unix(unixTime, 0).Local()
}

// DateTimeToUnixTimestamp mirrors upstream.
func DateTimeToUnixTimestamp(dt time.Time) int64 {
	return dt.Unix()
}

// FromTimeAgo mirrors upstream ("2 hours 1 day" style strings).
func FromTimeAgo(str string, relativeFrom ...time.Time) (time.Time, error) {
	now := time.Now()
	if len(relativeFrom) > 0 {
		now = relativeFrom[0]
	}
	lower := strings.ToLower(str)

	if strings.Contains(lower, "now") {
		return now, nil
	}

	lower = strings.ReplaceAll(lower, ",", "")
	lower = strings.ReplaceAll(lower, "ago", "")
	lower = strings.ReplaceAll(lower, "and", "")

	var ago time.Duration
	matches := timeAgoPartsRe.FindAllStringSubmatch(lower, -1)
	for _, m := range matches {
		val, err := parseutil.CoerceDouble(m[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("TimeAgo parsing failed: %v", err)
		}
		unit := m[2]
		switch {
		case strings.Contains(unit, "sec") || unit == "s":
			ago += time.Duration(val * float64(time.Second))
		case strings.Contains(unit, "min") || unit == "m":
			ago += time.Duration(val * float64(time.Minute))
		case strings.Contains(unit, "hour") || strings.Contains(unit, "hr") || unit == "h":
			ago += time.Duration(val * float64(time.Hour))
		case strings.Contains(unit, "day") || unit == "d":
			ago += time.Duration(val * float64(24*time.Hour))
		case strings.Contains(unit, "week") || strings.Contains(unit, "wk") || unit == "w":
			ago += time.Duration(val * 7 * float64(24*time.Hour))
		case strings.Contains(unit, "month") || unit == "mo":
			ago += time.Duration(val * 30 * float64(24*time.Hour))
		case strings.Contains(unit, "year") || unit == "y":
			ago += time.Duration(val * 365 * float64(24*time.Hour))
		default:
			return time.Time{}, fmt.Errorf("TimeAgo parsing failed, unknown unit: %s", unit)
		}
	}
	return now.Add(-ago), nil
}

// fuzzyLayouts is the FromFuzzyTime port: upstream delegates to the
// DateTimeRoutines library (a broad DateTime.TryParse equivalent). Go has no
// single equivalent, so a curated layout list covers the formats tracker
// pages emit; USA (month-first) variants are tried before UK ones when the
// format is not "UK".
var fuzzyLayoutsUsa = []string{
	"January 2, 2006", "Jan 2, 2006", "January 2 2006", "Jan 2 2006",
	"1/2/2006", "1/2/06", "01/02/2006", "01/02/06",
	"1/2/2006 15:04:05", "1/2/2006 15:04", "01/02/2006 15:04:05", "01/02/2006 15:04",
	"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02",
	"02-01-2006 15:04:05", "02-01-2006 15:04", "02-01-2006",
	"02.01.2006 15:04:05", "02.01.2006 15:04", "02.01.2006",
	"2 Jan 2006 15:04:05", "2 Jan 2006 15:04", "2 Jan 2006",
	"Jan 2 2006 15:04:05", "Jan 2 2006 15:04", "Jan 2 2006",
	"January 2, 2006 15:04:05", "January 2, 2006 15:04",
	"2006/01/02 15:04:05", "2006/01/02",
	time.RFC1123Z, time.RFC1123, time.RFC3339, time.RFC822, time.RFC850, time.ANSIC,
	"15:04:05 02/01/2006", "15:04 02/01/2006",
}

var fuzzyLayoutsUk = []string{
	"2 January 2006", "2 Jan 2006", "2/1/2006", "2/1/06", "02/01/2006", "02/01/06",
	"02-01-2006 15:04:05", "02-01-2006 15:04", "02-01-2006",
	"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02",
}

// FromFuzzyTime mirrors upstream FromFuzzyTime (format "" → USA, "UK" → UK).
func FromFuzzyTime(str, format string) (time.Time, error) {
	layouts := fuzzyLayoutsUsa
	if format == "UK" {
		layouts = fuzzyLayoutsUk
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, str, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("FromFuzzyTime parsing failed")
}

func fromFuzzyPastTime(str, format string, now time.Time) (time.Time, error) {
	result, err := FromFuzzyTime(str, format)
	if err != nil {
		return time.Time{}, err
	}
	if result.After(now) {
		result = result.AddDate(-1, 0, 0)
	}
	return result, nil
}

// dayOfWeekFromName mirrors the upstream StartsWith chain (tuesday before
// thursday; saturday before sunday).
func dayOfWeekFromName(name string) time.Weekday {
	n := strings.ToLower(name)
	switch {
	case strings.HasPrefix(n, "monday"):
		return time.Monday
	case strings.HasPrefix(n, "tuesday"):
		return time.Tuesday
	case strings.HasPrefix(n, "wednesday"):
		return time.Wednesday
	case strings.HasPrefix(n, "thursday"):
		return time.Thursday
	case strings.HasPrefix(n, "friday"):
		return time.Friday
	case strings.HasPrefix(n, "saturday"):
		return time.Saturday
	default:
		return time.Sunday
	}
}

// FromUnknown mirrors upstream FromUnknown.
func FromUnknown(str string, format string, relativeFrom ...time.Time) (time.Time, error) {
	str = parseutil.NormalizeMultiSpaces(str)
	if str == "" {
		return time.Time{}, fmt.Errorf("DateTime parsing failed for empty string")
	}
	now := time.Now()
	if len(relativeFrom) > 0 {
		now = relativeFrom[0]
	}
	orig := str

	fail := func(err error) (time.Time, error) {
		return time.Time{}, fmt.Errorf("DateTime parsing failed for %q: %v", orig, err)
	}

	// RFC1123Z ("Mon, 02 Jan 2006 15:04:05 -0700")
	if t, err := time.ParseInLocation(time.RFC1123Z, str, time.Local); err == nil {
		return t, nil
	}

	// unix timestamp
	if allDigitsRe.MatchString(str) {
		if unixTimeStamp, err := strconv.ParseInt(str, 10, 64); err == nil {
			return UnixTimestampToDateTime(unixTimeStamp), nil
		}
	}

	if strings.Contains(strings.ToLower(str), "now") {
		return now, nil
	}

	// ... ago
	if timeAgoRe.MatchString(str) {
		t, err := FromTimeAgo(str)
		if err != nil {
			return fail(err)
		}
		return t, nil
	}

	// Today ...
	if m := todayRe.FindStringSubmatch(str); m != nil {
		dt := dayStart(now)
		ts, err := parseTimeOfDay(strings.Replace(str, m[0], "", 1))
		if err != nil {
			return fail(err)
		}
		return dt.Add(ts), nil
	}

	// Yesterday ...
	if m := yesterdayRe.FindStringSubmatch(str); m != nil {
		dt := dayStart(now)
		ts, err := parseTimeOfDay(strings.Replace(str, m[0], "", 1))
		if err != nil {
			return fail(err)
		}
		return dt.Add(ts).AddDate(0, 0, -1), nil
	}

	// Tomorrow ...
	if m := tomorrowRe.FindStringSubmatch(str); m != nil {
		dt := dayStart(now)
		ts, err := parseTimeOfDay(strings.Replace(str, m[0], "", 1))
		if err != nil {
			return fail(err)
		}
		return dt.Add(ts).AddDate(0, 0, 1), nil
	}

	// [day of week] at ... (eg: Saturday at 14:22)
	if m := daysOfWeekRe.FindStringSubmatch(str); m != nil {
		dt := dayStart(now)
		ts, err := parseTimeOfDay(strings.Replace(str, m[0], "", 1))
		if err != nil {
			return fail(err)
		}
		dt = dt.Add(ts)
		dow := dayOfWeekFromName(m[1])
		for dt.Weekday() != dow {
			dt = dt.AddDate(0, 0, -1)
		}
		return dt, nil
	}

	// add missing year: "06-15 ..." style
	if m := missingYearRe.FindStringSubmatch(str); m != nil {
		date := m[1]
		newStr := strings.Replace(str, date, fmt.Sprintf("%d-%s", now.Year(), date), 1)
		t, err := fromFuzzyPastTime(newStr, format, now)
		if err != nil {
			return fail(err)
		}
		return t, nil
	}

	// add missing year 2: "1 Jan 10:30"
	if m := missingYearRe2.FindStringSubmatch(str); m != nil {
		newStr := fmt.Sprintf("%s %d %s", m[1], now.Year(), m[2])
		t, err := fromFuzzyPastTime(newStr, format, now)
		if err != nil {
			return fail(err)
		}
		return t, nil
	}

	t, err := FromFuzzyTime(str, format)
	if err != nil {
		return fail(err)
	}
	return t, nil
}

func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// parseTimeOfDay mirrors upstream ParseTimeSpan (DateTime.Parse(time).TimeOfDay).
func parseTimeOfDay(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	for _, layout := range []string{"15:04:05", "15:04", "3:04:05 PM", "3:04 PM"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute + time.Duration(t.Second())*time.Second, nil
		}
	}
	return 0, fmt.Errorf("unable to parse time of day %q", s)
}

// ParseDateTimeGoLang parses with the given layout. Upstream
// (DateTimeUtil.ParseDateTimeGoLang) converts Go layouts to C# before
// ParseExact, so definitions may use either token style — some ship
// C#-style layouts (e.g. "yyyy-MM-dd HH:mm:ss zzz"), which pass through the
// upstream conversion unchanged. Go layouts parse directly; on failure the
// layout is translated from .NET tokens and retried. Yearless layouts
// resolve to the past (mirrors upstream AddYears(-1)).
func ParseDateTimeGoLang(date, layout string, relativeFrom ...time.Time) (time.Time, error) {
	now := time.Now()
	if len(relativeFrom) > 0 {
		now = relativeFrom[0]
	}
	date = parseutil.NormalizeMultiSpaces(date)
	t, err := parseDateTimeLayout(date, layout)
	if err != nil {
		if translated := dotnetToGoLayout(layout); translated != layout {
			t, err = parseDateTimeLayout(date, translated)
			if err != nil && strings.Contains(translated, "Z07:00") {
				// Go has no single zone token accepting both "+02:00" and
				// "+0200"; retry the colon-less form.
				t, err = parseDateTimeLayout(date, strings.ReplaceAll(translated, "Z07:00", "-0700"))
			}
		}
	}
	if err != nil {
		return time.Time{}, err
	}
	hasYear := strings.Contains(layout, "2006") || strings.Contains(layout, "06") ||
		strings.Contains(layout, "yyyy") || strings.Contains(layout, "yy")
	if !hasYear {
		// layouts without a year parse as year 0; upstream DateTime.ParseExact
		// substitutes the current year, then resolves future dates to the past
		t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
		if t.After(now) {
			t = t.AddDate(-1, 0, 0)
		}
	}
	return t, nil
}

func parseDateTimeLayout(date, layout string) (time.Time, error) {
	t, err := time.ParseInLocation(layout, date, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("Error while parsing DateTime %q, using layout %q: %v", date, layout, err) //nolint:staticcheck // 上游文案 DateTimeUtil.cs:315
	}
	return t, nil
}

// dotnetToGoLayout translates .NET date format tokens to a Go layout so
// definitions written in C# style (e.g. "yyyy-MM-dd HH:mm:ss zzz") parse.
// Longer tokens are replaced first; unknown text passes through untouched.
func dotnetToGoLayout(layout string) string {
	// Go's NewReplacer resolves same-position ties by argument order (not
	// longest match), so pairs must be listed longest-token-first.
	replacer := strings.NewReplacer(
		"yyyy", "2006",
		"dddd", "Monday",
		"MMMM", "January",
		"yyy", "2006",
		"ddd", "Mon",
		"MMM", "Jan",
		"zzz", "Z07:00",
		"yy", "06",
		"MM", "01",
		"dd", "02",
		"HH", "15",
		"hh", "03",
		"mm", "04",
		"ss", "05",
		"tt", "pm",
		"zz", "Z07:00",
		"K", "Z07:00",
		"d", "2",
		"h", "3",
	)
	return replacer.Replace(layout)
}

// FormatRFC1123Z renders like upstream datetime.ToString(Rfc1123ZPattern):
// "Mon, 02 Jan 2006 15:04:05 -0700".
func FormatRFC1123Z(t time.Time) string {
	return t.Format("Mon, 02 Jan 2006 15:04:05 -0700")
}
