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
	return PackWithExtensions(raw, nil)
}

// PackWithExtensions is Pack with a caller's extensions. They are added to the constraints the
// request's policy compiles to, never substituted for them: replacing would let an extension,
// an operator lock among them, silently drop a policy rule the request asked for. A nil
// registry is the built-ins only.
func PackWithExtensions(raw interface{}, extensions *algorithm.ExtensionRegistry) (map[string]interface{}, error) {
	request, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	packed, err := PackDecodedWithExtensions(request, extensions)
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
func PackDecoded(request *DecodedRequest) (*result.PackingResult, error) {
	return PackDecodedWithExtensions(request, nil)
}

// PackDecodedWithExtensions is PackDecoded with a caller's extensions added after the policy
// constraints, as PackWithExtensions describes.
func PackDecodedWithExtensions(request *DecodedRequest, extensions *algorithm.ExtensionRegistry) (packed *result.PackingResult, err error) {
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
	rules, err := compiledPolicy(request)
	if err != nil {
		return nil, asRequestError(err)
	}
	return packer.New(request.Config, withPolicy(rules, extensions)).PackWithFixedPlacements(request.Items,
		request.Containers, request.FixedPlacements)
}

// compiledPolicy is the rule set Decode compiled, or the request's policy compiled now when
// the request was built by hand.
func compiledPolicy(request *DecodedRequest) (policy.PolicyRuleSet, error) {
	if request.rules != nil {
		return *request.rules, nil
	}
	return policy.ParseRuleSet(request.Policy)
}

// withPolicy is the caller's registry with the policy constraints first and every other
// extension carried over unchanged.
func withPolicy(rules policy.PolicyRuleSet, supplied *algorithm.ExtensionRegistry) *algorithm.ExtensionRegistry {
	if supplied == nil {
		supplied = algorithm.NoExtensions()
	}
	constraints := append(rules.Constraints(), supplied.PlacementConstraints()...)
	return algorithm.NewExtensionRegistry(constraints, supplied.ItemOrderStrategies(), supplied.Solvers(),
		supplied.CandidateScorer(), supplied.ContainerSelector())
}

// checkOutputUnits refuses an output unit the renderer cannot express, before rendering.
func checkOutputUnits(request *DecodedRequest) error {
	if _, err := units.MustLength(0).Decimal(request.OutputLengthUnit); err != nil {
		return err
	}
	_, err := units.MustWeight(0).Decimal(request.OutputWeightUnit)
	return err
}
