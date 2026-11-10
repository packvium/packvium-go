// Package jsonvalue decodes and encodes JSON with the reference engine's rules: integers stay
// exact (int64, or *big.Int past that range), floats are distinct from integers, and output is
// byte-identical to the reference's json.dumps with sorted keys.
//
// Decoded values are nil, bool, string, int64, *big.Int, float64, []interface{} and
// map[string]interface{}. Encoding also accepts int, []string and []map[string]interface{}.
package jsonvalue

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// Decode reads exactly one JSON document. Duplicate keys keep the last value, as in the
// reference.
func Decode(reader io.Reader) (interface{}, error) {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	var raw interface{}
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("Extra data after the JSON document")
	}
	return normalise(raw)
}

// DecodeBytes is Decode over a byte slice.
func DecodeBytes(data []byte) (interface{}, error) { return Decode(bytes.NewReader(data)) }

func normalise(value interface{}) (interface{}, error) {
	switch typed := value.(type) {
	case json.Number:
		return number(string(typed))
	case []interface{}:
		for index, element := range typed {
			converted, err := normalise(element)
			if err != nil {
				return nil, err
			}
			typed[index] = converted
		}
	case map[string]interface{}:
		for key, element := range typed {
			converted, err := normalise(element)
			if err != nil {
				return nil, err
			}
			typed[key] = converted
		}
	}
	return value, nil
}

func number(text string) (interface{}, error) {
	if strings.ContainsAny(text, ".eE") {
		return strconv.ParseFloat(text, 64)
	}
	if value, err := strconv.ParseInt(text, 10, 64); err == nil {
		return value, nil
	}
	value, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return nil, fmt.Errorf("invalid number %s", text)
	}
	return value, nil
}

// Indented is the reference CLI's json.dumps(indent=2, sort_keys=True, ensure_ascii=False).
func Indented(value interface{}) (string, error) {
	var builder strings.Builder
	err := write(&builder, value, "\n", "  ", ": ")
	return builder.String(), err
}

// Canonical is the conformance contract's compact form: sorted keys, "," and ":" separators,
// no floats anywhere (a float would carry platform-dependent precision into a comparison).
func Canonical(value interface{}) (string, error) {
	if err := rejectFloats(value); err != nil {
		return "", err
	}
	var builder strings.Builder
	err := write(&builder, value, "", "", ":")
	return builder.String(), err
}

func rejectFloats(value interface{}) error {
	switch typed := value.(type) {
	case float64:
		return fmt.Errorf("canonical JSON forbids float values, found %s", FloatRepr(typed))
	case []interface{}:
		for _, element := range typed {
			if err := rejectFloats(element); err != nil {
				return err
			}
		}
	case map[string]interface{}:
		for _, element := range typed {
			if err := rejectFloats(element); err != nil {
				return err
			}
		}
	}
	return nil
}

// write renders value; an empty newline means compact output.
func write(builder *strings.Builder, value interface{}, newline, indent, colon string) error {
	switch typed := value.(type) {
	case nil:
		builder.WriteString("null")
	case bool:
		builder.WriteString(strconv.FormatBool(typed))
	case string:
		writeString(builder, typed)
	case int:
		builder.WriteString(strconv.Itoa(typed))
	case int64:
		builder.WriteString(strconv.FormatInt(typed, 10))
	case *big.Int:
		builder.WriteString(typed.String())
	case float64:
		builder.WriteString(FloatRepr(typed))
	case []string:
		list := make([]interface{}, len(typed))
		for index, element := range typed {
			list[index] = element
		}
		return writeList(builder, list, newline, indent, colon)
	case []map[string]interface{}:
		list := make([]interface{}, len(typed))
		for index, element := range typed {
			list[index] = element
		}
		return writeList(builder, list, newline, indent, colon)
	case []interface{}:
		return writeList(builder, typed, newline, indent, colon)
	case map[string]interface{}:
		return writeObject(builder, typed, newline, indent, colon)
	default:
		return fmt.Errorf("Object of type %T is not JSON serializable", value)
	}
	return nil
}

func writeList(builder *strings.Builder, list []interface{}, newline, indent, colon string) error {
	if len(list) == 0 {
		builder.WriteString("[]")
		return nil
	}
	inner := newline + indent
	builder.WriteString("[")
	for index, element := range list {
		if index > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(inner)
		if err := write(builder, element, inner, indent, colon); err != nil {
			return err
		}
	}
	builder.WriteString(newline)
	builder.WriteString("]")
	return nil
}

func writeObject(builder *strings.Builder, object map[string]interface{}, newline, indent, colon string) error {
	if len(object) == 0 {
		builder.WriteString("{}")
		return nil
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	// Byte order of UTF-8 equals code-point order, which is how the reference sorts keys.
	sort.Strings(keys)
	inner := newline + indent
	builder.WriteString("{")
	for index, key := range keys {
		if index > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(inner)
		writeString(builder, key)
		builder.WriteString(colon)
		if err := write(builder, object[key], inner, indent, colon); err != nil {
			return err
		}
	}
	builder.WriteString(newline)
	builder.WriteString("}")
	return nil
}

// writeString escapes exactly what the reference escapes with ensure_ascii off: quotes,
// backslashes and control characters. U+2028/U+2029 and HTML characters stay literal.
func writeString(builder *strings.Builder, text string) {
	builder.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		case '\b':
			builder.WriteString(`\b`)
		case '\f':
			builder.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(builder, `\u%04x`, r)
				continue
			}
			builder.WriteRune(r)
		}
	}
	builder.WriteByte('"')
}

// FloatRepr is the reference's shortest round-trip float spelling: fixed notation for
// decimal exponents in [-4, 16), scientific otherwise, and always a fractional part in fixed
// notation.
func FloatRepr(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	}
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	sign := ""
	if scientific[0] == '-' {
		sign, scientific = "-", scientific[1:]
	}
	mantissa, exponentText := scientific[:strings.IndexByte(scientific, 'e')], scientific[strings.IndexByte(scientific, 'e')+1:]
	exponent, _ := strconv.Atoi(exponentText)
	digits := strings.Replace(mantissa, ".", "", 1)
	if exponent < -4 || exponent >= 16 {
		formatted := digits[:1]
		if len(digits) > 1 {
			formatted += "." + digits[1:]
		}
		exponentSign := "+"
		if exponent < 0 {
			exponentSign, exponent = "-", -exponent
		}
		return fmt.Sprintf("%s%se%s%02d", sign, formatted, exponentSign, exponent)
	}
	point := exponent + 1
	switch {
	case point <= 0:
		return sign + "0." + strings.Repeat("0", -point) + digits
	case point >= len(digits):
		return sign + digits + strings.Repeat("0", point-len(digits)) + ".0"
	}
	return sign + digits[:point] + "." + digits[point:]
}
