package core

import (
	"encoding/json"
	"strings"
)

// extractJSONObject returns the first valid JSON object embedded in text. It
// tolerates model prose around the object while still requiring valid JSON.
func extractJSONObject(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "{") && json.Valid([]byte(text)) {
		return text
	}
	for start := 0; start < len(text); start++ {
		if text[start] != '{' {
			continue
		}
		depth := 0
		inString := false
		escaped := false
		for end := start; end < len(text); end++ {
			ch := text[end]
			if inString {
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' {
					escaped = true
				} else if ch == '"' {
					inString = false
				}
				continue
			}
			switch ch {
			case '"':
				inString = true
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					candidate := text[start : end+1]
					if json.Valid([]byte(candidate)) {
						return candidate
					}
					end = len(text)
				}
			}
		}
	}
	return ""
}
