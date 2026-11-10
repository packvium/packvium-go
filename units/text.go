package units

import (
	"strings"
	"unicode"
)

// The reference engine reads quantities with Unicode-aware rules: its whitespace is
// every character `str.isspace` admits and its digits are every decimal digit (`\d`).
// Both sets are reproduced here so a quantity the reference accepts is accepted here,
// and one it refuses is refused.
const (
	spaceClass = `[\t\n\v\f\r \x1c-\x1f\x85\p{Z}]`
	digitClass = `\p{Nd}`
)

func isReferenceSpace(r rune) bool {
	if r >= 0x1c && r <= 0x1f {
		return true
	}
	return unicode.IsSpace(r) || unicode.Is(unicode.Z, r)
}

func stripReferenceSpace(text string) string {
	return strings.TrimFunc(text, isReferenceSpace)
}

// digitValue returns the value of a Unicode decimal digit. Every Nd range in Unicode is
// laid out as consecutive runs of ten starting at zero, so the value is the distance to
// the start of the run modulo ten.
func digitValue(r rune) int {
	start := r
	for unicode.IsDigit(start - 1) {
		start--
	}
	return int(r-start) % 10
}

// asciiDigits rewrites a run of Unicode decimal digits into ASCII digits, the form
// math/big parses.
func asciiDigits(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, r := range text {
		if r < 0x80 || !unicode.IsDigit(r) {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte(byte('0' + digitValue(r)))
	}
	return builder.String()
}
