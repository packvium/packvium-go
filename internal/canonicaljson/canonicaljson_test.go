package canonicaljson

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"os/exec"
	"testing"
)

var numbers = []struct {
	value   float64
	spelled string
}{
	{0.25, "0.25"}, {1.0, "1"}, {math.Copysign(0, -1), "0"}, {4.5, "4.5"}, {0.1, "0.1"}, {2e-3, "0.002"},
	{0.000001, "0.000001"}, {1e-7, "1e-7"}, {1.5e-7, "1.5e-7"}, {1e-27, "1e-27"}, {-1.5, "-1.5"},
	{123456789.5, "123456789.5"}, {333333333.33333329, "333333333.3333333"},
	{9007199254740991.0, "9007199254740991"}, {5e-324, "5e-324"}, {100.0, "100"},
	{1e15 + 0.5, "1000000000000000.5"}, {123e-20, "1.23e-18"}, {0.0000012, "0.0000012"},
}

func TestNumbersAreWrittenAsECMAScriptWritesThem(t *testing.T) {
	for _, number := range numbers {
		if got := mustMarshal(t, number.value); got != number.spelled {
			t.Errorf("%v: got %s, want %s", number.value, got, number.spelled)
		}
	}
}

// Where Node is installed its own JSON.stringify is the oracle, since RFC 8785 defines
// numbers as ECMAScript writes them.
func TestEveryNumberMatchesJavaScriptItself(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	values := make([]interface{}, len(numbers))
	for index, number := range numbers {
		values[index] = number.value
	}
	input, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	script := "process.stdout.write(JSON.stringify(JSON.parse(require('fs').readFileSync(0,'utf8'))))"
	command := exec.Command(node, "-e", script)
	command.Stdin = bytes.NewReader(input)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := mustMarshal(t, values); string(output) != want {
		t.Fatalf("node %s, Go %s", output, want)
	}
}

func TestIntegersAndLiteralsKeepTheirSpelling(t *testing.T) {
	value := []interface{}{0, int64(-7), int64(9007199254740991), big.NewInt(-9007199254740991), true, false, nil}
	if got := mustMarshal(t, value); got != "[0,-7,9007199254740991,-9007199254740991,true,false,null]" {
		t.Fatal(got)
	}
}

// U+FFFD sorts after U+1F600 by code point and before it by UTF-16 code unit, whose first unit
// is the high surrogate U+D83D. JavaScript sorts the second way; Go's byte order is the first.
func TestKeysAreSortedByUTF16CodeUnitsNotCodePoints(t *testing.T) {
	value := map[string]interface{}{"�": 1, "\U0001F600": 2, "a": 0, "": 3, "\U0001F600a": 4, "\U00010000": 5}
	want := "{\"a\":0,\"\U00010000\":5,\"\U0001F600\":2,\"\U0001F600a\":4,\"\":3,\"�\":1}"
	if got := mustMarshal(t, value); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestUTF16OrderIsATotalOrder(t *testing.T) {
	ordered := []string{"", "a", "ab", "é", "\U00010000", "\U0001F600", "", "￿"}
	for i, a := range ordered {
		for j, b := range ordered {
			if got := utf16Less(a, b); got != (i < j) {
				t.Errorf("utf16Less(%q, %q) = %v", a, b, got)
			}
		}
	}
}

func TestOnlyTheCharactersRFC8785NamesAreEscaped(t *testing.T) {
	text := "\"\\\b\t\n\f\r\x00\x1f/\x7f   é<&>"
	want := "\"\\\"\\\\\\b\\t\\n\\f\\r\\u0000\\u001f/\x7f   é<&>\""
	if got := mustMarshal(t, text); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestThereIsNoWhitespaceAndNestingIsPreserved(t *testing.T) {
	value := map[string]interface{}{
		"b": []interface{}{int64(1), map[string]interface{}{"d": []interface{}{}, "c": map[string]interface{}{}}},
		"a": "x",
	}
	if got := mustMarshal(t, value); got != `{"a":"x","b":[1,{"c":{},"d":[]}]}` {
		t.Fatal(got)
	}
}

func TestANumberNoEngineHoldsExactlyIsRefused(t *testing.T) {
	beyond := new(big.Int).Lsh(big.NewInt(1), 53)
	for _, value := range []interface{}{
		int64(1 << 53), int64(-(1 << 53)), beyond, new(big.Int).Neg(beyond), new(big.Int).Lsh(beyond, 20),
		9007199254740992.0, math.Inf(1), math.Inf(-1), math.NaN(),
	} {
		expectCode(t, map[string]interface{}{"n": value}, CodeNumberOutOfRange)
	}
}

func TestAStringThatIsNotUTF8IsRefusedInAValueAndInAKey(t *testing.T) {
	// "\xed\xa0\x80" is U+D800 written as UTF-8 would write it (WTF-8): a lone surrogate.
	for _, value := range []interface{}{"\xed\xa0\x80", map[string]interface{}{"\xed\xb0\x80": int64(1)}, "\xff"} {
		expectCode(t, value, CodeInvalidString)
	}
}

func TestABadKeyIsReportedBeforeABadValue(t *testing.T) {
	expectCode(t, map[string]interface{}{"a": math.NaN(), "\xff": int64(1)}, CodeInvalidString)
}

func TestAValueJSONCannotSpellIsRefused(t *testing.T) {
	for _, value := range []interface{}{map[int]string{1: "a"}, []byte("bytes"), struct{}{}, uint(3)} {
		expectCode(t, value, CodeInvalidValue)
	}
}

func TestHasLoneSurrogate(t *testing.T) {
	cases := map[string]bool{
		`{"bad":"\ud800"}`:        true,
		`{"bad":"\udc00"}`:        true,
		`["\ud800x"]`:             true,
		`["\ud800A"]`:             true,
		`["😀"]`:                   false,
		`["\\ud800"]`:             false,
		`{"\ud800":1}`:            true,
		`["😀", "é"]`:              false,
		`{"a":"plain"}`:           false,
		`["\"\ud800"]`:            true,
		"[\"\U0001F600\", \"�\"]": false,
	}
	for text, want := range cases {
		if got := HasLoneSurrogate([]byte(text)); got != want {
			t.Errorf("%s: got %v, want %v", text, got, want)
		}
	}
}

func mustMarshal(t *testing.T, value interface{}) string {
	t.Helper()
	text, err := Marshal(value)
	if err != nil {
		t.Fatalf("%v: %v", value, err)
	}
	return text
}

func expectCode(t *testing.T, value interface{}, code string) {
	t.Helper()
	_, err := Marshal(value)
	refusal, ok := err.(*Error)
	if !ok || refusal.Code != code {
		t.Errorf("%#v: got %v, want %s", value, err, code)
	}
}
