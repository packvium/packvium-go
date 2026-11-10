package compression

import (
	"encoding/json"
	"io/ioutil"
	"math/big"
	"testing"

	"github.com/packvium/packvium-go/units"
)

// testdata/reference-cases.json was produced by the reference engine's compression module at
// v1.0.0: reduced pressures, occupied heights and crush refusals.
type referenceCases struct {
	Cases []struct {
		Load, Area int64
		Num, Den   string
		HeightIn   int64  `json:"height_in"`
		Ratio, KPA int64  `json:"-"`
		RatioPPM   int64  `json:"ratio"`
		Limit      int64  `json:"kpa"`
		Height     *int64 `json:"height"`
		Error      *string
	} `json:"cases"`
	Ratios []struct {
		Ratio float64
		PPM   int64
	} `json:"ratios"`
}

func TestMatchesReference(t *testing.T) {
	raw, err := ioutil.ReadFile("testdata/reference-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases referenceCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases.Cases {
		pressure, err := AppliedPressure(units.MustWeight(c.Load), big.NewInt(c.Area))
		if err != nil {
			t.Fatal(err)
		}
		if pressure.numerator.String() != c.Num || pressure.denominator.String() != c.Den {
			t.Errorf("pressure(%d, %d) = %s, want %s/%s", c.Load, c.Area, pressure, c.Num, c.Den)
		}
		height, err := EffectiveHeightTicks(c.HeightIn, c.RatioPPM, c.Limit, pressure)
		switch {
		case c.Error != nil:
			if err == nil || err.Error() != *c.Error {
				t.Errorf("height %+v: got %v, want refusal %q", c, err, *c.Error)
			}
		case err != nil || height != *c.Height:
			t.Errorf("height %+v: got %d (%v), want %d", c, height, err, *c.Height)
		}
	}
	for _, c := range cases.Ratios {
		if got, err := RatioToPPM(c.Ratio); err != nil || got != c.PPM {
			t.Errorf("RatioToPPM(%v) = %d (%v), want %d", c.Ratio, got, err, c.PPM)
		}
	}
	if _, err := RatioToPPM(1.5); err == nil {
		t.Error("a ratio above one was admitted")
	}
}
