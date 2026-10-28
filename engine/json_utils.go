package engine

import "unicode/utf8"

func truncateUTF8(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	value = value[:max]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
