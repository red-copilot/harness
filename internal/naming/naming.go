// Package naming holds the identifier rules shared by adapters that hand
// names to Docker and to the Pi RPC process.
package naming

// Identifier maps an arbitrary value onto the character set Docker names and
// RPC session IDs accept: ASCII letters, digits, '-' and '_'. Every other rune
// becomes '_'. An input that yields nothing returns fallback.
func Identifier(value, fallback string) string {
	var b []rune
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b = append(b, r)
		default:
			b = append(b, '_')
		}
	}
	if len(b) == 0 {
		return fallback
	}
	return string(b)
}
