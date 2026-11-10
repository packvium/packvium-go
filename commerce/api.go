// Package commerce is Packvium's exported commercial and control-plane API: three
// deterministic functions -- Quote, EvaluatePolicy and CatalogVersionInfo -- over one
// canonical JSON commerce document. The contract (document format, result shapes,
// rejection codes) is docs/COMMERCE-API.md.
//
// A malformed document or request is an *InputError. A rejection the commercial model
// is entitled to make is not an error: it is a result document whose "status" is
// "rejected". Every number is an exact integer of arbitrary size.
//
// The package imports nothing from the packing engine; it is a leaf layer.
package commerce

import (
	"math/big"
	"sort"
)

// APIVersion is stamped on every result document.
const APIVersion = 1

// RejectionCodes is the closed set of rejection codes, in the order the contract
// tabulates them.
var RejectionCodes = []string{
	"tariff_not_found",
	"no_effective_tariff",
	"unavailable_zone",
	"unavailable_accessorial",
	"policy_rule_not_found",
	"policy_version_not_found",
	"catalog_not_found",
	"catalog_version_not_found",
	"no_effective_catalog_version",
	"ambiguous_catalog_reference",
}

// Result is one result document. Its members, and every nested payload's, keep the
// reference's insertion order, so IndentedJSON prints what the reference prints;
// CanonicalJSON gives the sorted wire spelling.
type Result = *Object

func okResult(key string, payload *Object) Result {
	return NewObject().with("api_version", APIVersion).with("status", "ok").with(key, payload)
}

func rejected(reason *rejection) Result {
	return NewObject().
		with("api_version", APIVersion).
		with("status", "rejected").
		with("error", NewObject().with("code", reason.code).with("fields", reason.fields))
}

// answer turns a rejection into its result document and passes every other error on.
func answer(key string, payload *Object, err error) (Result, error) {
	if reason, isRejection := err.(*rejection); isRejection {
		return rejected(reason), nil
	}
	if err != nil {
		return nil, err
	}
	return okResult(key, payload), nil
}

// prepare loads the document and checks the request is an object, in that order.
func prepare(document, request interface{}) (*Document, *Object, error) {
	loaded, err := LoadDocument(document)
	if err != nil {
		return nil, nil, err
	}
	normalized, err := normalize(request)
	if err != nil {
		return nil, nil, err
	}
	fields, err := mapping(normalized, "request")
	if err != nil {
		return nil, nil, err
	}
	return loaded, fields, nil
}

// exactlyOne names the single non-null key among names.
func exactlyOne(fields *Object, names ...string) (string, error) {
	var chosen []string
	for _, name := range names {
		if fields.lookup(name) != nil {
			chosen = append(chosen, name)
		}
	}
	if len(chosen) != 1 {
		return "", inputError("request", "expected exactly one of "+pythonList(names))
	}
	return chosen[0], nil
}

// -------------------------------------------------------------------------------- quote

// Quote prices one shipment against one pinned (tariff_version) or effective-dated
// (as_of) tariff version and returns the itemized rate breakdown.
func Quote(document, request interface{}) (Result, error) {
	loaded, fields, err := prepare(document, request)
	if err != nil {
		return nil, err
	}
	err = requireKeys(fields, "request",
		[]string{"carrier_id", "service_id", "zone", "actual_weight_g", "volume_mm3"},
		[]string{"tariff_version", "as_of", "requested_accessorials"})
	if err != nil {
		return nil, err
	}
	pin, err := exactlyOne(fields, "tariff_version", "as_of")
	if err != nil {
		return nil, err
	}
	carrierID, err := text(fields.lookup("carrier_id"), "request.carrier_id")
	if err != nil {
		return nil, err
	}
	serviceID, err := text(fields.lookup("service_id"), "request.service_id")
	if err != nil {
		return nil, err
	}
	accessorials, err := accessorialIDs(fields.lookup("requested_accessorials"))
	if err != nil {
		return nil, err
	}
	ratingRequest, err := buildRatingRequest(fields, accessorials)
	if err != nil {
		return nil, err
	}
	tariff, err := resolveTariff(loaded.Carriers, carrierID, serviceID, fields, pin)
	if err != nil {
		return answer("quote", nil, err)
	}
	breakdown, err := rate(tariff, ratingRequest)
	if err != nil {
		return answer("quote", nil, err)
	}
	return okResult("quote", quotePayload(breakdown)), nil
}

// accessorialIDs accepts a list of strings and nothing else: a bare string or an
// object is a malformed request, not a list of characters or keys.
func accessorialIDs(value interface{}) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	entries, err := sequence(value, "request.requested_accessorials")
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(entries))
	for index, entry := range entries {
		if ids[index], err = text(entry, indexed("request.requested_accessorials", index)); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func buildRatingRequest(fields *Object, accessorials []string) (RatingRequest, error) {
	zone, err := text(fields.lookup("zone"), "request.zone")
	if err != nil {
		return RatingRequest{}, err
	}
	weight, err := integer(fields.lookup("actual_weight_g"), "request.actual_weight_g")
	if err != nil {
		return RatingRequest{}, err
	}
	volume, err := integer(fields.lookup("volume_mm3"), "request.volume_mm3")
	if err != nil {
		return RatingRequest{}, err
	}
	request := RatingRequest{Zone: zone, ActualWeightG: weight, VolumeMM3: volume, RequestedAccessorials: accessorials}
	if err := request.validate(); err != nil {
		return RatingRequest{}, inputError("request", err.Error())
	}
	return request, nil
}

func resolveTariff(carriers *CarrierRegistry, carrierID, serviceID string, fields *Object, pin string) (*Tariff, error) {
	identity := func() *Object {
		return NewObject().with("carrier_id", carrierID).with("service_id", serviceID)
	}
	if pin == "tariff_version" {
		version, err := integer(fields.lookup("tariff_version"), "request.tariff_version")
		if err != nil {
			return nil, err
		}
		tariff, err := carriers.tariff(carrierID, serviceID, version)
		if err != nil {
			return nil, &rejection{code: "tariff_not_found", fields: identity().with("tariff_version", version)}
		}
		return tariff, nil
	}
	asOf, err := integer(fields.lookup("as_of"), "request.as_of")
	if err != nil {
		return nil, err
	}
	if _, err := carriers.history(carrierID, serviceID); err != nil {
		return nil, &rejection{code: "tariff_not_found", fields: identity()}
	}
	tariff, err := carriers.effectiveTariff(carrierID, serviceID, asOf)
	if err != nil {
		return nil, &rejection{code: "no_effective_tariff", fields: identity().with("as_of", asOf)}
	}
	return tariff, nil
}

func rate(tariff *Tariff, request RatingRequest) (RateBreakdown, error) {
	breakdown, err := rateTariff(tariff, request)
	unavailable, isUnavailable := err.(*unavailableService)
	if !isUnavailable {
		return breakdown, err
	}
	reason := NewObject().
		with("carrier_id", tariff.CarrierID).
		with("service_id", tariff.ServiceID).
		with("tariff_version", tariff.Version)
	if unavailable.zone != nil {
		return RateBreakdown{}, &rejection{code: "unavailable_zone", fields: reason.with("zone", *unavailable.zone)}
	}
	reason.Set("accessorial_ids", unavailable.accessorialIDs)
	return RateBreakdown{}, &rejection{code: "unavailable_accessorial", fields: reason}
}

func quotePayload(breakdown RateBreakdown) *Object {
	charges := make([]interface{}, len(breakdown.AccessorialChargesMinor))
	for index, charge := range breakdown.AccessorialChargesMinor {
		charges[index] = []interface{}{charge.AccessorialID, charge.AmountMinor}
	}
	return NewObject().
		with("carrier_id", breakdown.CarrierID).
		with("service_id", breakdown.ServiceID).
		with("tariff_version", breakdown.TariffVersion).
		with("zone", breakdown.Zone).
		with("actual_weight_g", breakdown.ActualWeightG).
		with("dimensional_weight_g", breakdown.DimensionalWeightG).
		with("billed_weight_g", breakdown.BilledWeightG).
		with("base_charge_minor", breakdown.BaseChargeMinor).
		with("minimum_charge_applied", breakdown.MinimumChargeApplied).
		with("fuel_surcharge_minor", breakdown.FuelSurchargeMinor).
		with("accessorial_charges_minor", charges).
		with("total_minor", breakdown.TotalMinor)
}

// ---------------------------------------------------------------------- evaluate_policy

// EvaluatePolicy decides one eligibility question against the rule set effective at
// as_of, or against an explicitly pinned rule_versions snapshot.
func EvaluatePolicy(document, request interface{}) (Result, error) {
	loaded, fields, err := prepare(document, request)
	if err != nil {
		return nil, err
	}
	if err := requireKeys(fields, "request", []string{"scope", "context"}, []string{"as_of", "rule_versions"}); err != nil {
		return nil, err
	}
	pin, err := exactlyOne(fields, "as_of", "rule_versions")
	if err != nil {
		return nil, err
	}
	scope, err := requestScope(fields.lookup("scope"))
	if err != nil {
		return nil, err
	}
	context, isObject := fields.lookup("context").(*Object)
	if !isObject {
		return nil, inputError("request.context", "expected an object")
	}
	decision, err := decideRequest(loaded.Policies, scope, context, fields, pin)
	if err != nil {
		return answer("decision", nil, err)
	}
	return okResult("decision", decisionPayload(decision)), nil
}

func requestScope(value interface{}) (PolicyScope, error) {
	word, err := text(value, "request.scope")
	if err != nil {
		return "", err
	}
	for _, scope := range policyScopes {
		if word == scope {
			return PolicyScope(word), nil
		}
	}
	return "", inputError("request.scope", "unsupported policy scope "+pythonRepr(word))
}

func decideRequest(policies *PolicyRegistry, scope PolicyScope, context *Object, fields *Object, pin string) (PolicyDecision, error) {
	if pin == "as_of" {
		asOf, err := integer(fields.lookup("as_of"), "request.as_of")
		if err != nil {
			return PolicyDecision{}, err
		}
		return policies.evaluate(scope, context, asOf)
	}
	rules, err := pinnedRules(policies, fields.lookup("rule_versions"))
	if err != nil {
		return PolicyDecision{}, err
	}
	return decide(rules, scope, context)
}

func pinnedRules(policies *PolicyRegistry, value interface{}) ([]*PolicyRule, error) {
	entries, err := sequence(value, "request.rule_versions")
	if err != nil {
		return nil, err
	}
	pins := make([]rulePin, 0, len(entries))
	for index, entry := range entries {
		path := indexed("request.rule_versions", index)
		pair, isList := entry.([]interface{})
		if !isList || len(pair) != 2 {
			return nil, inputError(path, "expected a [rule_id, version] pair")
		}
		ruleID, err := text(pair[0], path+"[0]")
		if err != nil {
			return nil, err
		}
		version, err := integer(pair[1], path+"[1]")
		if err != nil {
			return nil, err
		}
		pins = append(pins, rulePin{ruleID: ruleID, version: version})
	}
	rules, err := policies.resolveVersions(pins)
	switch typed := err.(type) {
	case nil:
		return rules, nil
	case *ruleVersionNotFound:
		return nil, &rejection{code: "policy_version_not_found",
			fields: NewObject().with("rule_id", typed.ruleID).with("version", typed.version)}
	case *ruleNotFound:
		return nil, &rejection{code: "policy_rule_not_found",
			fields: NewObject().with("rule_id", typed.ruleID)}
	}
	return nil, inputError("request.rule_versions", err.Error())
}

func decisionPayload(decision PolicyDecision) *Object {
	var citation interface{}
	if cited := decision.Citation; cited != nil {
		citation = NewObject().
			with("rule_id", cited.RuleID).
			with("version", cited.Version).
			with("action", string(cited.Action)).
			with("priority", cited.Priority).
			with("reason", cited.Reason)
	}
	return NewObject().
		with("scope", string(decision.Scope)).
		with("allowed", decision.Allowed).
		with("citation", citation)
}

// ----------------------------------------------------------------- catalog_version_info

// CatalogVersionInfo reports which catalog version a reference resolves to -- by
// explicit version, by as_of, or by the only version there is -- and what it contains.
func CatalogVersionInfo(document, request interface{}) (Result, error) {
	loaded, fields, err := prepare(document, request)
	if err != nil {
		return nil, err
	}
	if err := requireKeys(fields, "request", []string{"catalog_id", "resolved_at"}, []string{"version", "as_of"}); err != nil {
		return nil, err
	}
	catalogID, err := text(fields.lookup("catalog_id"), "request.catalog_id")
	if err != nil {
		return nil, err
	}
	resolvedAt, err := integer(fields.lookup("resolved_at"), "request.resolved_at")
	if err != nil {
		return nil, err
	}
	version, err := optionalInteger(fields.lookup("version"), "request.version")
	if err != nil {
		return nil, err
	}
	asOf, err := optionalInteger(fields.lookup("as_of"), "request.as_of")
	if err != nil {
		return nil, err
	}
	if version != nil && asOf != nil {
		return nil, inputError("request", "expected at most one of ['version', 'as_of']")
	}
	resolved, err := resolveCatalog(loaded, catalogID, version, asOf)
	if err != nil {
		return answer("catalog", nil, err)
	}
	if resolvedAt.Sign() < 0 {
		// The reference builds a CatalogReference here, whose own invariant raises a
		// ValueError outside its input-error type; it is a failure, not a refusal.
		return nil, errNegativeResolvedAt
	}
	return okResult("catalog", catalogPayload(catalogID, resolved, resolvedAt)), nil
}

var errNegativeResolvedAt = &referenceError{"ValueError: resolved_at cannot be negative"}

type referenceError struct{ message string }

func (err *referenceError) Error() string {
	return err.message
}

func resolveCatalog(loaded *Document, catalogID string, version, asOf *big.Int) (*CatalogVersion, error) {
	registry := loaded.Catalogs[catalogID]
	if registry == nil {
		return nil, &rejection{code: "catalog_not_found", fields: NewObject().with("catalog_id", catalogID)}
	}
	selector := NewObject().with("catalog_id", catalogID)
	if version != nil {
		selector.Set("version", version)
	}
	if asOf != nil {
		selector.Set("as_of", asOf)
	}
	resolved, err := registry.resolve(version, asOf)
	switch err {
	case nil:
		return resolved, nil
	case errCatalogVersionNotFound:
		return nil, &rejection{code: "catalog_version_not_found", fields: selector}
	case errNoEffectiveCatalog:
		return nil, &rejection{code: "no_effective_catalog_version", fields: selector}
	}
	return nil, &rejection{code: "ambiguous_catalog_reference", fields: selector}
}

func catalogPayload(catalogID string, version *CatalogVersion, resolvedAt *big.Int) *Object {
	snapshot := version.Snapshot
	var rolledBackFrom interface{}
	if version.RolledBackFrom != 0 {
		rolledBackFrom = version.RolledBackFrom
	}
	counts := NewObject().
		with("items", len(snapshot.Items)).
		with("cartons", len(snapshot.Cartons)).
		with("pallets", len(snapshot.Pallets)).
		with("exclusions", len(snapshot.Exclusions)).
		with("overrides", len(snapshot.Overrides))
	return NewObject().
		with("catalog_id", catalogID).
		with("version", version.Number).
		with("effective_at", version.EffectiveAt).
		with("published_at", version.PublishedAt).
		with("resolved_at", resolvedAt).
		with("rolled_back_from", rolledBackFrom).
		with("note", version.Note).
		with("entry_counts", counts).
		with("item_ids", sortedStrings(snapshot.itemIDs())).
		with("carton_ids", sortedStrings(snapshot.cartonIDs())).
		with("pallet_ids", sortedStrings(snapshot.palletIDs()))
}

// sortedStrings sorts byte-wise, which on UTF-8 is the reference's code-point order.
func sortedStrings(words []string) []string {
	sorted := append([]string{}, words...)
	sort.Strings(sorted)
	return sorted
}
