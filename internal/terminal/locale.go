package terminal

import "regexp"

var utf8Locale = regexp.MustCompile(`(?i)^(UTF-?8|[a-z][a-z0-9_+-]*\.UTF-?8(@[a-z0-9_+-]+)?)$`)

func UTF8(getenv func(string) string) bool {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if value := getenv(key); value != "" {
			return utf8Locale.MatchString(value)
		}
	}
	return false
}
