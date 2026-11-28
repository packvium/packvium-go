// Package pyrepr renders a string the way the reference engine's repr() does. Several
// refusal messages quote a caller-supplied value with repr(), and the rejection corpus
// matches those messages byte for byte.
package pyrepr

import (
	"fmt"
	"sort"
	"strings"
)

// String is repr(text) for a str: single quotes unless the text holds a single quote and no
// double quote, backslash escapes for the quote, the backslash and \t \n \r, \xhh for other
// control characters, and \xhh, \uhhhh or \Uhhhhhhhh for a non-printable non-ASCII one.
func String(text string) string {
	quote := '\''
	if strings.ContainsRune(text, '\'') && !strings.ContainsRune(text, '"') {
		quote = '"'
	}
	var builder strings.Builder
	builder.Grow(len(text) + 2)
	builder.WriteRune(quote)
	for _, r := range text {
		writeRune(&builder, r, quote)
	}
	builder.WriteRune(quote)
	return builder.String()
}

func writeRune(builder *strings.Builder, r, quote rune) {
	switch {
	case r == quote || r == '\\':
		builder.WriteByte('\\')
		builder.WriteRune(r)
	case r == '\t':
		builder.WriteString(`\t`)
	case r == '\n':
		builder.WriteString(`\n`)
	case r == '\r':
		builder.WriteString(`\r`)
	case r < ' ' || r == 0x7f:
		fmt.Fprintf(builder, `\x%02x`, r)
	case r < 0x7f:
		builder.WriteRune(r)
	case Printable(r):
		builder.WriteRune(r)
	case r <= 0xff:
		fmt.Fprintf(builder, `\x%02x`, r)
	case r <= 0xffff:
		fmt.Fprintf(builder, `\u%04x`, r)
	default:
		fmt.Fprintf(builder, `\U%08x`, r)
	}
}

// StringList is repr(list) of strings: each item's repr, comma-separated, in brackets.
func StringList(texts []string) string {
	quoted := make([]string, len(texts))
	for index, text := range texts {
		quoted[index] = String(text)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// Printable is the reference's str.isprintable for one code point, from the embedded Unicode
// 14.0.0 table rather than the runtime's, so every Go release prints the same. O(log r).
func Printable(r rune) bool {
	if r >= 0x20 && r < 0x7f {
		return true
	}
	if r < 0x20 {
		return false
	}
	at := sort.Search(len(nonPrintable), func(index int) bool { return nonPrintable[index][1] >= r })
	return at == len(nonPrintable) || r < nonPrintable[at][0]
}
