// Package canonicaljson writes RFC 8785 canonical JSON: the one spelling every Packvium engine
// agrees on byte for byte.
//
// Every language's default JSON writer disagrees with the others somewhere: one prints `1.0`
// where JavaScript prints `1`, JavaScript sorts keys by UTF-16 code unit where the others sort
// by code point, one escapes U+2028 unless told not to. RFC 8785 settles each of those the way
// JavaScript already behaves, which matters because JavaScript cannot tell `1.0` from `1` once
// the text is parsed.
//
//   - Object keys are sorted by their UTF-16 code units.
//   - Strings escape `"`, `\` and U+0000..U+001F (`\b \t \n \f \r` by name, the rest as
//     lowercase `\u00xx`); everything else is written as UTF-8, U+2028 included.
//   - Numbers are written as ECMAScript's Number::toString writes them.
//   - No whitespace.
//
// Two refusals keep that promise honest. A number whose magnitude exceeds 2^53 - 1, or that is
// not finite, is refused, because JavaScript already holds a different number by the time it
// can see it. A string that is not valid UTF-8 is refused: a Go string holding a lone UTF-16
// surrogate (WTF-8) or any other invalid byte sequence has no UTF-8 spelling.
//
// Values are the decoded JSON form of internal/jsonvalue: nil, bool, string, int, int64,
// *big.Int, float64, []interface{} and map[string]interface{}.
package canonicaljson

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxExactMagnitude is the largest magnitude every engine holds exactly.
const MaxExactMagnitude = 1<<53 - 1

// The refusal codes, shared with the other engines.
const (
	CodeNumberOutOfRange = "number_out_of_range"
	CodeInvalidString    = "invalid_string"
	CodeInvalidValue     = "invalid_value"
)

// Error is a value with no canonical spelling. Code names which rule it broke.
type Error struct {
	Code    string
	Message string
}

func (err *Error) Error() string { return err.Message }

func refuse(code, format string, arguments ...interface{}) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, arguments...)}
}

var maxExact = big.NewInt(MaxExactMagnitude)

// Marshal is value's canonical form. O(n + k log k) for a document of n values whose largest
// object has k keys.
func Marshal(value interface{}) (string, error) {
	var builder strings.Builder
	if err := write(&builder, value); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func write(builder *strings.Builder, value interface{}) error {
	switch typed := value.(type) {
	case nil:
		builder.WriteString("null")
	case bool:
		builder.WriteString(strconv.FormatBool(typed))
	case string:
		return writeString(builder, typed)
	case int:
		return writeInteger(builder, int64(typed))
	case int64:
		return writeInteger(builder, typed)
	case *big.Int:
		if typed.CmpAbs(maxExact) > 0 {
			return refuse(CodeNumberOutOfRange, "%s is beyond what every engine holds exactly", typed)
		}
		builder.WriteString(typed.String())
	case float64:
		text, err := Number(typed)
		if err != nil {
			return err
		}
		builder.WriteString(text)
	case []interface{}:
		return writeList(builder, typed)
	case map[string]interface{}:
		return writeObject(builder, typed)
	default:
		return refuse(CodeInvalidValue, "a %T has no JSON spelling", value)
	}
	return nil
}

func writeInteger(builder *strings.Builder, value int64) error {
	if value > MaxExactMagnitude || value < -MaxExactMagnitude {
		return refuse(CodeNumberOutOfRange, "%d is beyond what every engine holds exactly", value)
	}
	builder.WriteString(strconv.FormatInt(value, 10))
	return nil
}

func writeList(builder *strings.Builder, list []interface{}) error {
	builder.WriteByte('[')
	for index, element := range list {
		if index > 0 {
			builder.WriteByte(',')
		}
		if err := write(builder, element); err != nil {
			return err
		}
	}
	builder.WriteByte(']')
	return nil
}

func writeObject(builder *strings.Builder, object map[string]interface{}) error {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(a, b int) bool { return utf16Less(keys[a], keys[b]) })
	// Every key is checked before any value is written, as the reference does, so a document
	// with a bad key and a bad value is refused for its key in every engine.
	for _, key := range keys {
		if !utf8.ValidString(key) {
			return invalidString()
		}
	}
	builder.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			builder.WriteByte(',')
		}
		if err := writeString(builder, key); err != nil {
			return err
		}
		builder.WriteByte(':')
		if err := write(builder, object[key]); err != nil {
			return err
		}
	}
	builder.WriteByte('}')
	return nil
}

// utf16Less orders two strings by their UTF-16 code units. UTF-8 byte order is code-point
// order, and the two differ only where a code point in U+E000..U+FFFF meets one beyond the
// Basic Multilingual Plane, whose leading surrogate (U+D800..U+DBFF) sorts first. Strings
// equal rune for rune (possible only when both are invalid UTF-8) fall back to byte order,
// so the order is total. O(length), allocation-free.
func utf16Less(a, b string) bool {
	left, right := a, b
	for left != "" && right != "" {
		x, xWidth := utf8.DecodeRuneInString(left)
		y, yWidth := utf8.DecodeRuneInString(right)
		if x != y {
			xUnit, yUnit := leadingUnit(x), leadingUnit(y)
			if xUnit != yUnit {
				return xUnit < yUnit
			}
			// Both beyond the BMP with the same leading surrogate: the trailing surrogates
			// order as the code points do.
			return x < y
		}
		left, right = left[xWidth:], right[yWidth:]
	}
	if left == "" && right == "" {
		return a < b
	}
	return left == ""
}

// leadingUnit is the first UTF-16 code unit of r.
func leadingUnit(r rune) rune {
	if r < 0x10000 {
		return r
	}
	return 0xD800 + ((r - 0x10000) >> 10)
}

func invalidString() error {
	return refuse(CodeInvalidString, "a string carries a lone UTF-16 surrogate or is not UTF-8")
}

func writeString(builder *strings.Builder, text string) error {
	if !utf8.ValidString(text) {
		return invalidString()
	}
	builder.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\b':
			builder.WriteString(`\b`)
		case '\t':
			builder.WriteString(`\t`)
		case '\n':
			builder.WriteString(`\n`)
		case '\f':
			builder.WriteString(`\f`)
		case '\r':
			builder.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(builder, `\u%04x`, r)
				continue
			}
			builder.WriteRune(r)
		}
	}
	builder.WriteByte('"')
	return nil
}

// Number is a float as ECMAScript's Number::toString writes it: the shortest digit string
// that round-trips, fixed notation for decimal exponents in [-7, 21), scientific otherwise,
// no trailing ".0", and 0 for -0.
func Number(value float64) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > MaxExactMagnitude {
		return "", refuse(CodeNumberOutOfRange, "%s is beyond what every engine holds exactly", floatRepr(value))
	}
	if value == 0 {
		return "0", nil
	}
	// Go's shortest formatting picks the same digits as ECMAScript: the fewest that
	// round-trip, closest to the exact value when several do.
	scientific := strconv.FormatFloat(math.Abs(value), 'e', -1, 64)
	mark := strings.IndexByte(scientific, 'e')
	exponent, _ := strconv.Atoi(scientific[mark+1:])
	digits := strings.Replace(scientific[:mark], ".", "", 1)
	count, point := len(digits), exponent+1
	var rendered string
	switch {
	case count <= point && point <= 21:
		rendered = digits + strings.Repeat("0", point-count)
	case 0 < point && point <= 21:
		rendered = digits[:point] + "." + digits[point:]
	case -6 < point && point <= 0:
		rendered = "0." + strings.Repeat("0", -point) + digits
	default:
		rendered = digits[:1]
		if count > 1 {
			rendered += "." + digits[1:]
		}
		sign := "+"
		if point-1 < 0 {
			sign = "-"
		}
		rendered += "e" + sign + strconv.Itoa(abs(point-1))
	}
	if value < 0 {
		return "-" + rendered, nil
	}
	return rendered, nil
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// floatRepr is the reference's repr of a float, for messages.
func floatRepr(value float64) string {
	switch {
	case math.IsNaN(value):
		return "nan"
	case math.IsInf(value, 1):
		return "inf"
	case math.IsInf(value, -1):
		return "-inf"
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// HasLoneSurrogate reports whether JSON text escapes a UTF-16 surrogate that is not half of a
// pair, such as "\ud800". encoding/json silently decodes one as U+FFFD, a different string
// that would then be written as if it had been sent, so a reader checks the text first.
// O(n), one pass, and it reads only inside strings.
func HasLoneSurrogate(text []byte) bool {
	inString := false
	for index := 0; index < len(text); index++ {
		character := text[index]
		if !inString {
			inString = character == '"'
			continue
		}
		switch character {
		case '"':
			inString = false
		case '\\':
			if index+1 < len(text) && text[index+1] == 'u' {
				lone, consumed := surrogateAt(text, index)
				if lone {
					return true
				}
				index += consumed - 1
				continue
			}
			index++
		}
	}
	return false
}

// surrogateAt reads the \uXXXX escape at text[start], and the low half after it when the
// escape is a high surrogate. It returns whether the escape is a lone surrogate and how many
// bytes were read.
func surrogateAt(text []byte, start int) (bool, int) {
	unit, ok := escapedUnit(text, start)
	if !ok {
		return false, 2
	}
	switch {
	case unit >= 0xDC00 && unit <= 0xDFFF:
		return true, 6
	case unit >= 0xD800 && unit <= 0xDBFF:
		low, ok := escapedUnit(text, start+6)
		if !ok || low < 0xDC00 || low > 0xDFFF {
			return true, 6
		}
		return false, 12
	}
	return false, 6
}

// escapedUnit is the code unit of the \uXXXX escape at text[start].
func escapedUnit(text []byte, start int) (uint64, bool) {
	if start+6 > len(text) || text[start] != '\\' || text[start+1] != 'u' {
		return 0, false
	}
	unit, err := strconv.ParseUint(string(text[start+2:start+6]), 16, 16)
	return unit, err == nil
}

// Integer is the integer a decoded JSON value is, judged by value as every engine can judge
// it. 1.0 is 1, because JavaScript cannot tell them apart once the text is parsed; true, "1"
// and 1.5 are not integers, and neither is anything past 2^53 - 1, which JavaScript no longer
// holds exactly.
func Integer(value interface{}) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return exactInteger(int64(typed))
	case int64:
		return exactInteger(typed)
	case *big.Int:
		if !typed.IsInt64() {
			return 0, false
		}
		return exactInteger(typed.Int64())
	case float64:
		if typed != math.Trunc(typed) || math.Abs(typed) > MaxExactMagnitude {
			return 0, false
		}
		return int64(typed), true
	}
	return 0, false
}

func exactInteger(value int64) (int64, bool) {
	if value > MaxExactMagnitude || value < -MaxExactMagnitude {
		return 0, false
	}
	return value, true
}

// Spelling is how a refusal quotes a value: its canonical JSON, the one spelling every engine
// shares, or a fixed phrase for a value that has none.
func Spelling(value interface{}) string {
	text, err := Marshal(value)
	if err == nil {
		return text
	}
	if refusal, ok := err.(*Error); ok && refusal.Code == CodeNumberOutOfRange {
		return "an out-of-range number"
	}
	return "an unspellable value"
}
