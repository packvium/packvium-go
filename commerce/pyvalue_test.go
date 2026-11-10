package commerce

import (
	"math"
	"math/big"
	"testing"
)

func mustDecode(t *testing.T, text string) interface{} {
	t.Helper()
	value, err := Decode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// Expected verdicts are what Python's == gives for json.loads of each side.
func TestPythonEqual(t *testing.T) {
	cases := []struct {
		left, right string
		equal       bool
	}{
		{`true`, `1`, true},
		{`false`, `0.0`, true},
		{`true`, `true`, true},
		{`1`, `1.0`, true},
		{`9007199254740993`, `9007199254740992.0`, false},
		{`9007199254740992`, `9007199254740992.0`, true},
		{`1e400`, `1e400`, true},
		{`1`, `"1"`, false},
		{`null`, `null`, true},
		{`null`, `0`, false},
		{`[]`, `{}`, false},
		{`[1, [true]]`, `[1.0, [1]]`, true},
		{`[1, 2]`, `[1]`, false},
		{`{"a": 1, "b": 2}`, `{"b": 2, "a": 1.0}`, true},
		{`{"a": 1}`, `{"a": 1, "b": 2}`, false},
		{`"é"`, `"é"`, true},
	}
	for _, c := range cases {
		left, right := mustDecode(t, c.left), mustDecode(t, c.right)
		if pythonEqual(left, right) != c.equal || pythonEqual(right, left) != c.equal {
			t.Errorf("%s == %s should be %v", c.left, c.right, c.equal)
		}
	}
}

func TestPythonContains(t *testing.T) {
	cases := []struct {
		container, needle string
		contained         bool
		failure           string
	}{
		{`"express-overnight"`, `"overnight"`, true, ""},
		{`"abc"`, `""`, true, ""},
		{`[["a", 1], ["b", 2]]`, `["b", 2.0]`, true, ""},
		{`[1, 2]`, `true`, true, ""},
		{`{"k": 1}`, `"k"`, true, ""},
		{`{"1": 1}`, `1`, false, ""},
		{`"abc"`, `1`, false, "TypeError: 'in <string>' requires string as left operand, not int"},
		{`5`, `5`, false, "TypeError: argument of type 'int' is not iterable"},
		{`true`, `1`, false, "TypeError: argument of type 'bool' is not iterable"},
		{`{"a": 1}`, `{}`, false, "TypeError: unhashable type: 'dict'"},
	}
	for _, c := range cases {
		contained, err := pythonContains(mustDecode(t, c.container), mustDecode(t, c.needle))
		if c.failure != "" {
			if err == nil || err.Error() != c.failure {
				t.Errorf("%s in %s: expected %q, got %v", c.needle, c.container, c.failure, err)
			}
			continue
		}
		if err != nil || contained != c.contained {
			t.Errorf("%s in %s: expected %v, got %v %v", c.needle, c.container, c.contained, contained, err)
		}
	}
}

func TestCeilDivRoundsUp(t *testing.T) {
	cases := []struct{ numerator, denominator, quotient int64 }{
		{0, 7, 0}, {1, 7, 1}, {7, 7, 1}, {8, 7, 2}, {-8, 7, -1}, {math.MaxInt64, 1000, 9223372036854776},
	}
	for _, c := range cases {
		got := ceilDiv(big.NewInt(c.numerator), big.NewInt(c.denominator))
		if got.Int64() != c.quotient {
			t.Errorf("ceil(%d/%d) = %v, want %d", c.numerator, c.denominator, got, c.quotient)
		}
	}
}

func TestPythonRepr(t *testing.T) {
	cases := map[string]string{
		"plain":  `'plain'`,
		"it's":   `"it's"`,
		`a'b"c`:  `'a\'b"c'`,
		"tab\t":  `'tab\t'`,
		"\x00é😀": `'\x00é😀'`,
	}
	for input, expected := range cases {
		if observed := pythonRepr(input); observed != expected {
			t.Errorf("repr(%q) = %s, want %s", input, observed, expected)
		}
	}
}
