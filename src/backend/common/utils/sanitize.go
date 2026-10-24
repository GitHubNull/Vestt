package utils

import "regexp"

// Sensitive keys whose values are masked in log output (upstream
// JackettProtectedAttribute intent; backend-api.md/logging notes).
var sensitivePatterns = []*regexp.Regexp{
	// query/form style: apikey=xxx, password=xxx, cookieheader=xxx, passkey=xxx
	regexp.MustCompile(`(?i)\b(apikey|passkey|password|cookieheader)=([^&\s]+)`),
	// JSON style: "api_key":"xxx", "apiKey":"xxx", "password":"xxx"
	regexp.MustCompile(`(?i)"(api[_-]?key|passkey|password|cookieheader)"\s*:\s*"[^"]*"`),
}

// Sanitize masks sensitive values in a log message, replacing them with ***.
func Sanitize(msg string) string {
	out := sensitivePatterns[0].ReplaceAllString(msg, `$1=***`)
	out = sensitivePatterns[1].ReplaceAllString(out, `"$1":"***"`)
	return out
}
