package commerce

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"unicode"
)

// The shape primitives the document loader and the request readers share. Each one
// reports a wrong type as an InputError labelled with the JSON path, in the reference's
// wording.

func mapping(value interface{}, path string) (*Object, error) {
	object, ok := value.(*Object)
	if !ok {
		return nil, inputError(path, "expected an object")
	}
	return object, nil
}

func sequence(value interface{}, path string) ([]interface{}, error) {
	list, ok := value.([]interface{})
	if !ok {
		return nil, inputError(path, "expected a list")
	}
	return list, nil
}

// integer refuses a boolean: true where an exact integer belongs is a caller mistake,
// not a one. A float is refused too, even when its value is whole.
func integer(value interface{}, path string) (*big.Int, error) {
	number, ok := value.(*big.Int)
	if !ok {
		return nil, inputError(path, "expected an exact integer")
	}
	return number, nil
}

func optionalInteger(value interface{}, path string) (*big.Int, error) {
	if value == nil {
		return nil, nil
	}
	return integer(value, path)
}

func text(value interface{}, path string) (string, error) {
	word, ok := value.(string)
	if !ok {
		return "", inputError(path, "expected a string")
	}
	return word, nil
}

// orDefault collapses an omitted optional field and an explicit null, as every
// implementation of the contract does.
func orDefault(fields *Object, key string, fallback interface{}) interface{} {
	if value := fields.lookup(key); value != nil {
		return value
	}
	return fallback
}

// requireKeys refuses a missing required key first and an unrecognised key second, each
// listed in sorted order so the message is the same on every run.
func requireKeys(fields *Object, path string, required []string, optional []string) error {
	allowed := map[string]bool{}
	var missing []string
	for _, key := range required {
		allowed[key] = true
		if _, present := fields.Get(key); !present {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return inputError(path, "missing required key(s) "+pythonList(missing))
	}
	for _, key := range optional {
		allowed[key] = true
	}
	var unknown []string
	for _, key := range fields.keys {
		if !allowed[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return inputError(path, "unrecognised key(s) "+pythonList(unknown))
	}
	return nil
}

// pythonRepr spells a string the way Python's repr() does, so messages read the same
// as the reference's.
func pythonRepr(word string) string {
	quote := "'"
	if strings.Contains(word, "'") && !strings.Contains(word, `"`) {
		quote = `"`
	}
	var out strings.Builder
	out.WriteString(quote)
	for _, character := range word {
		switch {
		case character == '\\':
			out.WriteString(`\\`)
		case string(character) == quote:
			out.WriteString(`\` + quote)
		case character == '\n':
			out.WriteString(`\n`)
		case character == '\r':
			out.WriteString(`\r`)
		case character == '\t':
			out.WriteString(`\t`)
		case unicode.IsPrint(character) || character == ' ':
			out.WriteRune(character)
		case character < 0x100:
			fmt.Fprintf(&out, `\x%02x`, character)
		case character < 0x10000:
			fmt.Fprintf(&out, `\u%04x`, character)
		default:
			fmt.Fprintf(&out, `\U%08x`, character)
		}
	}
	out.WriteString(quote)
	return out.String()
}

func pythonList(words []string) string {
	spelled := make([]string, len(words))
	for index, word := range words {
		spelled[index] = pythonRepr(word)
	}
	return "[" + strings.Join(spelled, ", ") + "]"
}

func indexed(path string, index int) string {
	return fmt.Sprintf("%s[%d]", path, index)
}
