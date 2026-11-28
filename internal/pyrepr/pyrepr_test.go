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

func TestPrintableReadsTheEmbeddedTable(t *testing.T) {
	for r, want := range map[rune]bool{'a': true, 0x7f: false, 0xa0: false, 0xad: false, 0xe9: true, 0x378: false,
		0x4e00: true, 0x2028: false, 0xe000: false, 0x1f600: true} {
		if Printable(r) != want {
			t.Errorf("%U: %v", r, !want)
		}
	}
}
