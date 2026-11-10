package jsonvalue

import (
	"math"
	"testing"
)

func TestFloatReprMatchesReference(t *testing.T) {
	// Expected spellings are the reference's repr() of the same doubles.
	for value, want := range map[float64]string{
		0: "0.0", 1: "1.0", -2.5: "-2.5", 0.1: "0.1", 1e-4: "0.0001", 1e-5: "1e-05", 1e16: "1e+16",
		1e15: "1000000000000000.0", 123456789012345678: "1.2345678901234568e+17", 0.000123: "0.000123",
		1.5e300: "1.5e+300", 5e-324: "5e-324", 2.0 / 3: "0.6666666666666666", math.Inf(1): "Infinity",
	} {
		if got := FloatRepr(value); got != want {
			t.Errorf("FloatRepr(%v) = %q, want %q", value, got, want)
		}
	}
}

func TestIndentedMatchesReferenceLayout(t *testing.T) {
	value, err := DecodeBytes([]byte(`{"b":[1,2.5,{}],"a":"x <\u0001\"","c":[],"d":123456789012345678901234567890}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Indented(value)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": \"x <\\u0001\\\"\",\n  \"b\": [\n    1,\n    2.5,\n    {}\n  ],\n  \"c\": [],\n  \"d\": 123456789012345678901234567890\n}"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if _, err := Canonical(value); err == nil {
		t.Error("canonical form admitted a float")
	}
}
