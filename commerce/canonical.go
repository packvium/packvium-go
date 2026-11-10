package commerce

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// CanonicalJSON is the one byte-comparable spelling of a result document: Python's
// json.dumps(sort_keys=True, separators=(",", ":"), ensure_ascii=False). Cross-language
// equality is asserted on this string, so key order and whitespace cannot make two
// identical answers differ. O(n log n) in the document size, for the key sorts.
func CanonicalJSON(value interface{}) (string, error) {
	return encode(value, jsonLayout{sortKeys: true})
}

// IndentedJSON is Python's json.dumps(value, indent=2, ensure_ascii=False): members in
// insertion order, one per line, "," between them and ": " after a key. It is for
// people to read; compare and store CanonicalJSON instead. A plain Go map has no
// insertion order, so its keys are written sorted. O(n) for an *Object document.
func IndentedJSON(value interface{}) (string, error) {
	return encode(value, jsonLayout{indent: "  "})
}

// jsonLayout is the difference between the two spellings; the escaping is shared.
type jsonLayout struct {
	sortKeys bool
	indent   string
}

func encode(value interface{}, layout jsonLayout) (string, error) {
	writer := jsonWriter{layout: layout}
	if err := writer.value(value, 0); err != nil {
		return "", err
	}
	return writer.out.String(), nil
}

type jsonWriter struct {
	out    strings.Builder
	layout jsonLayout
}

func (writer *jsonWriter) value(value interface{}, depth int) error {
	out := &writer.out
	switch typed := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(typed))
	case int:
		out.WriteString(strconv.Itoa(typed))
	case int64:
		out.WriteString(strconv.FormatInt(typed, 10))
	case *big.Int:
		out.WriteString(typed.String())
	case string:
		writeString(out, typed)
	case []interface{}:
		return writer.list(typed, depth)
	case []string:
		list := make([]interface{}, len(typed))
		for index, word := range typed {
			list[index] = word
		}
		return writer.list(list, depth)
	case map[string]interface{}:
		return writer.members(sortedKeys(typed), typed, depth)
	case *Object:
		keys := typed.keys
		if writer.layout.sortKeys {
			keys = sortedKeys(typed.values)
		}
		return writer.members(keys, typed.values, depth)
	default:
		// A result document never carries a float, and Python's float spelling is not
		// worth reproducing for a value that cannot occur.
		return fmt.Errorf("cannot serialize a value of type %T", value)
	}
	return nil
}

func (writer *jsonWriter) list(values []interface{}, depth int) error {
	if len(values) == 0 {
		writer.out.WriteString("[]")
		return nil
	}
	writer.out.WriteByte('[')
	for index, entry := range values {
		writer.separate(index, depth+1)
		if err := writer.value(entry, depth+1); err != nil {
			return err
		}
	}
	writer.close(depth, ']')
	return nil
}

func (writer *jsonWriter) members(keys []string, values map[string]interface{}, depth int) error {
	if len(keys) == 0 {
		writer.out.WriteString("{}")
		return nil
	}
	writer.out.WriteByte('{')
	for index, key := range keys {
		writer.separate(index, depth+1)
		writeString(&writer.out, key)
		writer.out.WriteByte(':')
		if writer.layout.indent != "" {
			writer.out.WriteByte(' ')
		}
		if err := writer.value(values[key], depth+1); err != nil {
			return err
		}
	}
	writer.close(depth, '}')
	return nil
}

// separate writes what precedes the index-th element: a comma after the first, and in
// the indented layout a line break and the element's indentation.
func (writer *jsonWriter) separate(index, depth int) {
	if index > 0 {
		writer.out.WriteByte(',')
	}
	writer.newline(depth)
}

func (writer *jsonWriter) close(depth int, bracket byte) {
	writer.newline(depth)
	writer.out.WriteByte(bracket)
}

func (writer *jsonWriter) newline(depth int) {
	if writer.layout.indent == "" {
		return
	}
	writer.out.WriteByte('\n')
	writer.out.WriteString(strings.Repeat(writer.layout.indent, depth))
}

// sortedKeys sorts byte-wise, which on valid UTF-8 is exactly Python's code-point order
// -- not the UTF-16 order a Java or JavaScript sort would give.
func sortedKeys(members map[string]interface{}) []string {
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// writeString escapes exactly what json.dumps escapes under ensure_ascii=False: the
// quote, the backslash and the C0 controls. U+2028 and U+2029 stay raw.
func writeString(out *strings.Builder, text string) {
	out.WriteByte('"')
	for index := 0; index < len(text); index++ {
		character := text[index]
		switch character {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			if character < 0x20 {
				fmt.Fprintf(out, `\u%04x`, character)
			} else {
				out.WriteByte(character)
			}
		}
	}
	out.WriteByte('"')
}
