package configdata

import "regexp"

// regexpCompile wraps regexp.Compile so items.go stays readable.
func regexpCompile(pattern string) (*regexp.Regexp, error) { return regexp.Compile(pattern) }

// splitByPattern splits s using an upstream-derived delimiter pattern
// (e.g. "[^A-Za-z0-9\\-\\._~]+").
func splitByPattern(s, pattern string) []string {
	re, err := regexpCompile(pattern)
	if err != nil {
		return nil
	}
	return re.Split(s, -1)
}
