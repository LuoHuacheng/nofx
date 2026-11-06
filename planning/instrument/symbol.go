// Package instrument validates native identifiers without transliterating them.
package instrument

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidSymbol permits uppercase ASCII tickers and native Han-character asset
// codes with non-ASCII base assets. Venue metadata still determines whether it is listed.
// Whitespace, controls, URL/command separators and lookalike alphabets are not
// identifiers. Length is bounded in both runes and UTF-8 bytes.
func ValidSymbol(s string) bool {
	if !utf8.ValidString(s) || len(s) > 160 || utf8.RuneCountInString(s) < 1 || utf8.RuneCountInString(s) > 40 {
		return false
	}
	if parts := strings.Split(s, ":"); len(parts) == 2 {
		if len(parts[0]) < 1 || len(parts[0]) > 16 {
			return false
		}
		for _, r := range parts[0] {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				return false
			}
		}
		return ValidSymbol(parts[1]) && !strings.Contains(parts[1], ":")
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || unicode.Is(unicode.Han, r)) {
			return false
		}
	}
	return true
}
