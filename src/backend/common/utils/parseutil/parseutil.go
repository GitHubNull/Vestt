// Package parseutil ports Jackett.Common/Utils/ParseUtil.cs.
package parseutil

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	imdbIDRe      = regexp.MustCompile(`^(?:tt)?(\d{1,8})$`)
	multiSpacesRe = regexp.MustCompile(`\s+`)
)

// RemoveInvalidXmlChars mirrors upstream (RE2 has no look-around, so the
// surrogate-pair checks are applied rune-wise; valid UTF-8 cannot contain
// lone surrogates, which the upstream regex would strip).
func RemoveInvalidXmlChars(text string) string {
	if text == "" {
		return ""
	}
	var sb strings.Builder
	for _, r := range text {
		switch {
		case r <= 0x08,
			r == 0x0B, r == 0x0C,
			r >= 0x0E && r <= 0x1F,
			r >= 0x7F && r <= 0x9F,
			r == 0xFEFF, r == 0xFFFE, r == 0xFFFF:
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// NormalizeSpace mirrors upstream (null → empty string).
func NormalizeSpace(s string) string {
	if s == "" {
		return ""
	}
	return strings.TrimSpace(s)
}

// NormalizeMultiSpaces mirrors upstream.
func NormalizeMultiSpaces(s string) string {
	return multiSpacesRe.ReplaceAllString(NormalizeSpace(s), " ")
}

// normalizeNumber mirrors upstream NormalizeNumber.
func normalizeNumber(s string, isInt bool) string {
	var b strings.Builder
	for _, c := range s {
		if (c >= '0' && c <= '9') || c == '.' || c == ',' {
			b.WriteRune(c)
		}
	}
	valStr := strings.TrimSpace(b.String())
	valStr = strings.ReplaceAll(valStr, "-", "0")

	if isInt {
		if strings.Contains(valStr, ",") && strings.Contains(valStr, ".") {
			return valStr
		}
		if valStr == "" {
			return "0"
		}
		return strings.ReplaceAll(valStr, ".", ",")
	}

	if valStr == "" {
		return "0"
	}
	valStr = strings.ReplaceAll(valStr, ",", ".")
	if strings.Count(valStr, ".") > 1 {
		last := strings.LastIndex(valStr, ".")
		valStr = strings.ReplaceAll(valStr[:last], ".", "") + valStr[last:]
	}
	return valStr
}

// CoerceDouble mirrors upstream.
func CoerceDouble(str string) (float64, error) {
	n := normalizeNumber(str, false)
	f, err := strconv.ParseFloat(n, 64)
	if err != nil {
		return 0, err
	}
	return f, nil
}

// MustCoerceDouble mirrors upstream semantics where the caller assumes valid
// numeric input (returns 0 on failure, like double.Parse would throw upstream).
func MustCoerceDouble(str string) float64 {
	v, _ := CoerceDouble(str)
	return v
}

// CoerceFloat mirrors upstream.
func CoerceFloat(str string) (float64, error) {
	return CoerceDouble(str)
}

// CoerceInt mirrors upstream.
func CoerceInt(str string) (int64, error) {
	n := normalizeNumber(str, true)
	n = strings.ReplaceAll(n, ",", "")
	return strconv.ParseInt(strings.TrimSpace(n), 10, 64)
}

// CoerceLong mirrors upstream.
func CoerceLong(str string) (int64, error) { return CoerceInt(str) }

// MustCoerceLong mirrors upstream CoerceLong at call sites that tolerate junk
// (e.g. seeders/peer coercion clamps) — returns 0 on failure.
func MustCoerceLong(str string) int64 {
	v, err := CoerceInt(str)
	if err != nil {
		return 0
	}
	return v
}

// TryCoerceDouble mirrors upstream.
func TryCoerceDouble(str string) (float64, bool) {
	v, err := CoerceDouble(str)
	return v, err == nil
}

// GetArgumentFromQueryString mirrors upstream (split on ? then #; first value).
func GetArgumentFromQueryString(rawURL, argument string) string {
	if rawURL == "" || argument == "" {
		return ""
	}
	rest := rawURL
	if i := strings.Index(rest, "?"); i >= 0 {
		rest = rest[i+1:]
	} else {
		return ""
	}
	if i := strings.Index(rest, "#"); i >= 0 {
		rest = rest[:i]
	}
	vals, err := url.ParseQuery(rest)
	if err != nil {
		return ""
	}
	return vals.Get(argument)
}

// GetLongFromString mirrors upstream: first run of digits.
func GetLongFromString(str string) (int64, bool) {
	if strings.TrimSpace(str) == "" {
		return 0, false
	}
	var b strings.Builder
	for _, c := range str {
		if c < '0' || c > '9' {
			if b.Len() > 0 {
				break
			}
			continue
		}
		b.WriteRune(c)
	}
	if b.Len() == 0 {
		return 0, false
	}
	v, err := strconv.ParseInt(b.String(), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// GetImdbID mirrors upstream GetImdbId.
func GetImdbID(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	m := imdbIDRe.FindStringSubmatch(value)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// GetFullImdbID mirrors upstream (tt + 7-digit zero padded).
func GetFullImdbID(value string) string {
	id, ok := GetImdbID(value)
	if !ok || id == 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", id)
}

// GetBytes mirrors upstream: extract number and unit, scale by unit.
func GetBytes(str string) int64 {
	var num strings.Builder
	for _, c := range str {
		if (c >= '0' && c <= '9') || c == '.' || c == ',' {
			num.WriteRune(c)
		}
	}
	valStr := num.String()
	if valStr == "" {
		valStr = "0"
	}
	valStr = strings.ReplaceAll(valStr, ",", ".")
	if strings.Count(valStr, ".") > 1 {
		last := strings.LastIndex(valStr, ".")
		valStr = strings.ReplaceAll(valStr[:last], ".", "") + valStr[last:]
	}
	var unit strings.Builder
	for _, c := range str {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			unit.WriteRune(c)
		}
	}
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		val = 0
	}
	return GetBytesFromUnit(unit.String(), val)
}

// GetBytesFromUnit mirrors upstream GetBytes(unit, value).
func GetBytesFromUnit(unit string, value float64) int64 {
	unit = strings.ReplaceAll(strings.ToLower(unit), "i", "")
	switch {
	case strings.Contains(unit, "kb"):
		return int64(value * 1024)
	case strings.Contains(unit, "mb"):
		return int64(value * 1024 * 1024)
	case strings.Contains(unit, "gb"):
		return int64(value * 1024 * 1024 * 1024)
	case strings.Contains(unit, "tb"):
		return int64(value * 1024 * 1024 * 1024 * 1024)
	default:
		return int64(value)
	}
}
