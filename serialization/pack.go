package serialization

import (
	"fmt"

	"github.com/packvium/packvium-go/algorithm"
	"github.com/packvium/packvium-go/packer"
	"github.com/packvium/packvium-go/policy"
	"github.com/packvium/packvium-go/result"
	"github.com/packvium/packvium-go/units"
)

// Pack is what every engine's command line does: a decoded wire request in, a wire result out.
func Pack(raw interface{}) (map[string]interface{}, error) {
	request, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	packed, err := PackDecoded(request)
	if err != nil {
		return nil, err
	}
	if err := checkOutputUnits(request); err != nil {
		return nil, err
	}
	encoded := EncodeResult(packed, request.OutputLengthUnit, request.OutputWeightUnit)
	encoded["catalog_versions_used"] = request.CatalogVersionsUsed
	return encoded, nil
}

// PackDecoded solves a decoded request. Policy rules compile into the constraint pipeline
// rather than post-filtering a chosen answer, so the winner was never illegal.
func PackDecoded(request *DecodedRequest) (packed *result.PackingResult, err error) {
	// An exact-arithmetic overflow deep in the engine is a refusal of the request, never a
	// crash of the caller.
	defer func() {
		if recovered := recover(); recovered != nil {
			if failure, ok := recovered.(error); ok {
				packed, err = nil, failure
				return
			}
			packed, err = nil, fmt.Errorf("%v", recovered)
		}
	}()
	rules, err := policy.ParseRuleSet(request.Policy)
	if err != nil {
		return nil, err
	}
	extensions := algorithm.ExtensionsWithConstraints(rules.Constraints())
	return packer.New(request.Config, extensions).Pack(request.Items, request.Containers)
}

// checkOutputUnits refuses an output unit the renderer cannot express, before rendering.
func checkOutputUnits(request *DecodedRequest) error {
	if _, err := units.MustLength(0).Decimal(request.OutputLengthUnit); err != nil {
		return err
	}
	_, err := units.MustWeight(0).Decimal(request.OutputWeightUnit)
	return err
}
