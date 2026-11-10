package units

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"testing"
)

// The cases in testdata/reference-cases.json were produced by the reference engine's units
// module at v1.0.0; every conversion and rendering here must agree with it exactly.
type referenceCases struct {
	Conversions []struct {
		Kind     string          `json:"kind"`
		Op       string          `json:"op"`
		Value    json.RawMessage `json:"value"`
		Unit     string          `json:"unit"`
		Rounding string          `json:"rounding"`
		Ticks    *json.Number    `json:"ticks"`
		Error    *string         `json:"error"`
	} `json:"conversions"`
	Renders []struct {
		Kind  string `json:"kind"`
		Ticks int64  `json:"ticks"`
		Unit  string `json:"unit"`
		Value string `json:"value"`
	} `json:"renders"`
}

func loadReferenceCases(t *testing.T) referenceCases {
	t.Helper()
	raw, err := ioutil.ReadFile("testdata/reference-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases referenceCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func quantityOf(t *testing.T, raw json.RawMessage) Quantity {
	t.Helper()
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return TextQuantity(text)
	}
	var integer int64
	if err := json.Unmarshal(raw, &integer); err != nil {
		t.Fatalf("unexpected case value %s", raw)
	}
	return IntQuantity(integer)
}

func convert(kind, op string, quantity Quantity, unit string, rounding Rounding) (int64, error) {
	switch kind + "." + op {
	case "length.of":
		value, err := LengthOf(quantity, unit, rounding)
		return value.Ticks(), err
	case "length.parse":
		value, err := ParseLength(quantity, unit, rounding)
		return value.Ticks(), err
	case "weight.of":
		value, err := WeightOf(quantity, unit, rounding)
		return value.Ticks(), err
	case "weight.parse":
		value, err := ParseWeight(quantity, unit, rounding)
		return value.Ticks(), err
	}
	panic("unknown conversion " + kind + "." + op)
}

func TestConversionsMatchReference(t *testing.T) {
	for _, c := range loadReferenceCases(t).Conversions {
		name := fmt.Sprintf("%s.%s(%s,%q,%s)", c.Kind, c.Op, c.Value, c.Unit, c.Rounding)
		ticks, err := convert(c.Kind, c.Op, quantityOf(t, c.Value), c.Unit, Rounding(c.Rounding))
		if c.Ticks != nil {
			if _, fits := c.Ticks.Int64(); fits != nil {
				// The reference's integers are unbounded; ticks are int64 here, as in PHP,
				// so a value past that range must be refused rather than wrapped.
				if err == nil {
					t.Errorf("%s: admitted %d ticks for an out-of-range reference value %s", name, ticks, *c.Ticks)
				}
				continue
			}
		}
		switch {
		case c.Error != nil && err == nil:
			t.Errorf("%s: got %d ticks, reference refused with %q", name, ticks, *c.Error)
		case c.Error == nil && err != nil:
			t.Errorf("%s: refused with %q, reference gave %s ticks", name, err, *c.Ticks)
		case c.Error == nil && fmt.Sprint(ticks) != c.Ticks.String():
			t.Errorf("%s: got %d ticks, reference gave %s", name, ticks, *c.Ticks)
		}
	}
}

func TestRenderingMatchesReference(t *testing.T) {
	for _, c := range loadReferenceCases(t).Renders {
		var got string
		var err error
		if c.Kind == "length" {
			got, err = Length{c.Ticks}.Decimal(c.Unit)
		} else {
			got, err = Weight{c.Ticks}.Decimal(c.Unit)
		}
		if err != nil || got != c.Value {
			t.Errorf("%s %d in %s: got %q (%v), reference rendered %q", c.Kind, c.Ticks, c.Unit, got, err, c.Value)
		}
	}
}

func TestNegativeTicksAreRefused(t *testing.T) {
	if _, err := NewLength(-1); err == nil {
		t.Error("a negative length was admitted")
	}
	if _, err := NewWeight(-1); err == nil {
		t.Error("a negative weight was admitted")
	}
}

func TestRoundingTiesToEven(t *testing.T) {
	for _, c := range []struct {
		text     string
		rounding Rounding
		want     int64
	}{
		{"1/2", Nearest, 0}, {"3/2", Nearest, 2}, {"5/2", Nearest, 2}, {"-1/2", Nearest, 0},
		{"-3/2", Nearest, -2}, {"1/3", Ceil, 1}, {"-1/3", Floor, -1}, {"-1/3", Ceil, 0},
	} {
		value, err := TextQuantity(c.text).Rational()
		if err != nil {
			t.Fatal(err)
		}
		if got := Round(value, c.rounding).Int64(); got != c.want {
			t.Errorf("Round(%s, %s) = %d, want %d", c.text, c.rounding, got, c.want)
		}
	}
}
