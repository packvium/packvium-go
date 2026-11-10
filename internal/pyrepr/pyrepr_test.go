package pyrepr

import "testing"

// Expected renderings were produced by the reference interpreter's repr().
func TestMatchesTheReferenceRepr(t *testing.T) {
	cases := []struct{ text, expected string }{
		{"up", `'up'`},
		{"it's", `"it's"`},
		{`a"b`, `'a"b'`},
		{`both'"`, `'both\'"'`},
		{"tab\there", `'tab\there'`},
		{"nl\n", `'nl\n'`},
		{`bs\`, `'bs\\'`},
		{"\x01\x7f", `'\x01\x7f'`},
		{"café", `'café'`},
		{"\u00a0", `'\xa0'`},
		{"\u2028", "'\\u2028'"},
		{"\U0001F600", "'\U0001F600'"},
		{"\ue000", "'\\ue000'"},
		{"\U000e0001", `'\U000e0001'`},
	}
	for _, test := range cases {
		if actual := String(test.text); actual != test.expected {
			t.Errorf("repr(%q): expected %s, got %s", test.text, test.expected, actual)
		}
	}
}
