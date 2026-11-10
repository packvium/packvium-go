package commerce

import (
	"math/big"
	"sort"
)

// Loading the canonical commerce document into the three registries. Parsing is strict
// in both directions: a missing required key and an unrecognised extra key are both
// input errors. A version's number is its 1-based position in its versions list.
// One pass over the payload: O(size of the document) time and space.

// Document holds the three append-only histories a request is answered against.
type Document struct {
	Carriers *CarrierRegistry
	Policies *PolicyRegistry
	Catalogs map[string]*CatalogRegistry
}

// modelError re-labels a model's own invariant failure as an input error at path, so
// "valid" keeps a single definition inside the model.
func modelError(path string, err error) error {
	if err == nil {
		return nil
	}
	if _, isInput := err.(*InputError); isInput {
		return err
	}
	return inputError(path, err.Error())
}

// LoadDocument builds the registries one canonical commerce document describes. The
// document may be Decode output or plain Go maps, slices and integers.
func LoadDocument(document interface{}) (*Document, error) {
	normalized, err := normalize(document)
	if err != nil {
		return nil, err
	}
	return loadDocument(normalized)
}

func loadDocument(document interface{}) (*Document, error) {
	payload, err := mapping(document, "document")
	if err != nil {
		return nil, err
	}
	if err := requireKeys(payload, "document", nil, []string{"tariffs", "policy_rules", "catalogs"}); err != nil {
		return nil, err
	}
	carriers, err := loadTariffs(present(payload, "tariffs"))
	if err != nil {
		return nil, err
	}
	policies, err := loadPolicyRules(present(payload, "policy_rules"))
	if err != nil {
		return nil, err
	}
	catalogs, err := loadCatalogs(present(payload, "catalogs"))
	if err != nil {
		return nil, err
	}
	return &Document{Carriers: carriers, Policies: policies, Catalogs: catalogs}, nil
}

// present reads a top-level section: absent means empty, but an explicit null is kept
// so it is refused as "expected a list", exactly as the reference's payload.get(key, ())
// does.
func present(payload *Object, key string) interface{} {
	value, found := payload.Get(key)
	if !found {
		return []interface{}{}
	}
	return value
}

// ------------------------------------------------------------------------------ tariffs

func loadTariffs(value interface{}) (*CarrierRegistry, error) {
	registry := newCarrierRegistry()
	entries, err := sequence(value, "document.tariffs")
	if err != nil {
		return nil, err
	}
	seen := map[serviceKey]bool{}
	for index, entry := range entries {
		path := indexed("document.tariffs", index)
		fields, err := mapping(entry, path)
		if err != nil {
			return nil, err
		}
		if err := requireKeys(fields, path, []string{"carrier_id", "service_id", "versions"}, nil); err != nil {
			return nil, err
		}
		carrierID, err := text(fields.lookup("carrier_id"), path+".carrier_id")
		if err != nil {
			return nil, err
		}
		serviceID, err := text(fields.lookup("service_id"), path+".service_id")
		if err != nil {
			return nil, err
		}
		key := serviceKey{carrierID, serviceID}
		if seen[key] {
			return nil, inputError(path, "duplicate tariff history for "+carrierID+"/"+serviceID)
		}
		seen[key] = true
		if err := publishTariffVersions(registry, key, fields.lookup("versions"), path); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func publishTariffVersions(registry *CarrierRegistry, key serviceKey, value interface{}, parent string) error {
	versions, err := sequence(value, parent+".versions")
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return inputError(parent+".versions", "a tariff history needs at least one version")
	}
	for index, entry := range versions {
		path := indexed(parent+".versions", index)
		terms, err := loadTariffTerms(entry, path)
		if err != nil {
			return err
		}
		if _, err := registry.publish(key.carrierID, key.serviceID, terms); err != nil {
			return modelError(path, err)
		}
	}
	return nil
}

func loadTariffTerms(entry interface{}, path string) (tariffTerms, error) {
	fields, err := mapping(entry, path)
	if err != nil {
		return tariffTerms{}, err
	}
	err = requireKeys(fields, path,
		[]string{"effective_at", "dimensional_weight_divisor", "cost_per_dimensional_kg_minor"},
		[]string{"minimum_charge_minor", "fuel_surcharge_permille", "accessorials"})
	if err != nil {
		return tariffTerms{}, err
	}
	costs, err := loadZoneCosts(fields.lookup("cost_per_dimensional_kg_minor"), path+".cost_per_dimensional_kg_minor")
	if err != nil {
		return tariffTerms{}, err
	}
	accessorials, err := loadAccessorials(orDefault(fields, "accessorials", []interface{}{}), path+".accessorials")
	if err != nil {
		return tariffTerms{}, err
	}
	terms := tariffTerms{costPerDimensionalKgMinor: costs, accessorials: accessorials}
	integers := []struct {
		target   **big.Int
		key      string
		fallback interface{}
	}{
		{&terms.effectiveAt, "effective_at", nil},
		{&terms.dimensionalWeightDivisor, "dimensional_weight_divisor", nil},
		{&terms.minimumChargeMinor, "minimum_charge_minor", big.NewInt(0)},
		{&terms.fuelSurchargePermille, "fuel_surcharge_permille", big.NewInt(0)},
	}
	for _, field := range integers {
		raw := fields.lookup(field.key)
		if field.fallback != nil {
			raw = orDefault(fields, field.key, field.fallback)
		}
		if *field.target, err = integer(raw, path+"."+field.key); err != nil {
			return tariffTerms{}, err
		}
	}
	return terms, nil
}

func loadZoneCosts(value interface{}, path string) (map[string]*big.Int, error) {
	zones, err := mapping(value, path)
	if err != nil {
		return nil, err
	}
	costs := make(map[string]*big.Int, zones.Len())
	for _, zone := range zones.keys {
		cost, err := integer(zones.values[zone], path+"["+pythonRepr(zone)+"]")
		if err != nil {
			return nil, err
		}
		costs[zone] = cost
	}
	return costs, nil
}

func loadAccessorials(value interface{}, path string) (map[string]AccessorialCharge, error) {
	entries, err := sequence(value, path)
	if err != nil {
		return nil, err
	}
	charges := map[string]AccessorialCharge{}
	for index, entry := range entries {
		entryPath := indexed(path, index)
		fields, err := mapping(entry, entryPath)
		if err != nil {
			return nil, err
		}
		err = requireKeys(fields, entryPath, []string{"accessorial_id"}, []string{"flat_charge_minor", "permille_of_base"})
		if err != nil {
			return nil, err
		}
		id, err := text(fields.lookup("accessorial_id"), entryPath+".accessorial_id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := charges[id]; duplicate {
			return nil, inputError(entryPath, "duplicate accessorial_id "+pythonRepr(id))
		}
		flat, err := optionalInteger(fields.lookup("flat_charge_minor"), entryPath+".flat_charge_minor")
		if err != nil {
			return nil, err
		}
		permille, err := optionalInteger(fields.lookup("permille_of_base"), entryPath+".permille_of_base")
		if err != nil {
			return nil, err
		}
		charge, err := newAccessorialCharge(id, flat, permille)
		if err != nil {
			return nil, modelError(entryPath, err)
		}
		charges[id] = charge
	}
	return charges, nil
}

// ------------------------------------------------------------------------- policy rules

// enumValue reads a string from a closed vocabulary; kind names it in the message the
// way the reference's enum class name does.
func enumValue(value interface{}, path string, kind string, allowed []string) (string, error) {
	word, err := text(value, path)
	if err != nil {
		return "", err
	}
	for _, candidate := range allowed {
		if word == candidate {
			return word, nil
		}
	}
	return "", inputError(path, "unsupported "+kind+" "+pythonRepr(word))
}

func loadPolicyRules(value interface{}) (*PolicyRegistry, error) {
	registry := newPolicyRegistry()
	entries, err := sequence(value, "document.policy_rules")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for index, entry := range entries {
		path := indexed("document.policy_rules", index)
		fields, err := mapping(entry, path)
		if err != nil {
			return nil, err
		}
		if err := requireKeys(fields, path, []string{"rule_id", "versions"}, nil); err != nil {
			return nil, err
		}
		ruleID, err := text(fields.lookup("rule_id"), path+".rule_id")
		if err != nil {
			return nil, err
		}
		if seen[ruleID] {
			return nil, inputError(path, "duplicate rule history for "+pythonRepr(ruleID))
		}
		seen[ruleID] = true
		if err := publishRuleVersions(registry, ruleID, fields.lookup("versions"), path); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func publishRuleVersions(registry *PolicyRegistry, ruleID string, value interface{}, parent string) error {
	versions, err := sequence(value, parent+".versions")
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return inputError(parent+".versions", "a rule history needs at least one version")
	}
	for index, entry := range versions {
		path := indexed(parent+".versions", index)
		terms, err := loadRuleTerms(entry, path)
		if err != nil {
			return err
		}
		if _, err := registry.publish(ruleID, terms); err != nil {
			return modelError(path, err)
		}
	}
	return nil
}

func loadRuleTerms(entry interface{}, path string) (ruleTerms, error) {
	fields, err := mapping(entry, path)
	if err != nil {
		return ruleTerms{}, err
	}
	err = requireKeys(fields, path, []string{"scope", "action", "predicates", "priority", "effective_at"}, []string{"reason"})
	if err != nil {
		return ruleTerms{}, err
	}
	scope, err := enumValue(fields.lookup("scope"), path+".scope", "PolicyScope", policyScopes)
	if err != nil {
		return ruleTerms{}, err
	}
	action, err := enumValue(fields.lookup("action"), path+".action", "PolicyAction", policyActions)
	if err != nil {
		return ruleTerms{}, err
	}
	predicates, err := loadPredicates(fields.lookup("predicates"), path+".predicates")
	if err != nil {
		return ruleTerms{}, err
	}
	priority, err := integer(fields.lookup("priority"), path+".priority")
	if err != nil {
		return ruleTerms{}, err
	}
	effectiveAt, err := integer(fields.lookup("effective_at"), path+".effective_at")
	if err != nil {
		return ruleTerms{}, err
	}
	reason, err := text(orDefault(fields, "reason", ""), path+".reason")
	if err != nil {
		return ruleTerms{}, err
	}
	return ruleTerms{
		scope: PolicyScope(scope), action: PolicyAction(action), predicates: predicates,
		priority: priority, effectiveAt: effectiveAt, reason: reason,
	}, nil
}

func loadPredicates(value interface{}, path string) ([]PolicyPredicate, error) {
	entries, err := sequence(value, path)
	if err != nil {
		return nil, err
	}
	predicates := make([]PolicyPredicate, 0, len(entries))
	for index, entry := range entries {
		entryPath := indexed(path, index)
		predicate, err := loadPredicate(entry, entryPath)
		if err != nil {
			return nil, err
		}
		predicates = append(predicates, predicate)
	}
	return predicates, nil
}

func loadPredicate(entry interface{}, path string) (PolicyPredicate, error) {
	fields, err := mapping(entry, path)
	if err != nil {
		return PolicyPredicate{}, err
	}
	if err := requireKeys(fields, path, []string{"scope", "field", "operator"}, []string{"value"}); err != nil {
		return PolicyPredicate{}, err
	}
	scope, err := enumValue(fields.lookup("scope"), path+".scope", "PolicyScope", policyScopes)
	if err != nil {
		return PolicyPredicate{}, err
	}
	field, err := text(fields.lookup("field"), path+".field")
	if err != nil {
		return PolicyPredicate{}, err
	}
	operator, err := enumValue(fields.lookup("operator"), path+".operator", "PolicyOperator", policyOperators)
	if err != nil {
		return PolicyPredicate{}, err
	}
	predicate, err := newPolicyPredicate(PolicyScope(scope), field, PolicyOperator(operator), fields.lookup("value"))
	return predicate, modelError(path, err)
}

// ----------------------------------------------------------------------------- catalogs

func dimensions(value interface{}, path string, axes int) ([]*big.Int, error) {
	entries, err := sequence(value, path)
	if err != nil {
		return nil, err
	}
	if len(entries) != axes {
		return nil, inputError(path, "expected exactly "+itoa(axes)+" axes")
	}
	measured := make([]*big.Int, len(entries))
	for index, entry := range entries {
		if measured[index], err = integer(entry, indexed(path, index)); err != nil {
			return nil, err
		}
	}
	return measured, nil
}

func itoa(value int) string {
	return big.NewInt(int64(value)).String()
}

// entryLoader reads one master record; overrides dispatch on it by kind.
type entryLoader func(fields *Object, path string) (interface{}, error)

var entryLoaders = map[string]entryLoader{
	"item":   func(fields *Object, path string) (interface{}, error) { return loadItem(fields, path) },
	"carton": func(fields *Object, path string) (interface{}, error) { return loadCarton(fields, path) },
	"pallet": func(fields *Object, path string) (interface{}, error) { return loadPallet(fields, path) },
}

func loadItem(fields *Object, path string) (ItemMaster, error) {
	if err := requireKeys(fields, path, []string{"id", "dimensions_mm", "weight_g"}, []string{"description"}); err != nil {
		return ItemMaster{}, err
	}
	var item ItemMaster
	var err error
	if item.ID, err = text(fields.lookup("id"), path+".id"); err != nil {
		return ItemMaster{}, err
	}
	if item.DimensionsMM, err = dimensions(fields.lookup("dimensions_mm"), path+".dimensions_mm", 3); err != nil {
		return ItemMaster{}, err
	}
	if item.WeightG, err = integer(fields.lookup("weight_g"), path+".weight_g"); err != nil {
		return ItemMaster{}, err
	}
	if item.Description, err = text(orDefault(fields, "description", ""), path+".description"); err != nil {
		return ItemMaster{}, err
	}
	return item, modelError(path, item.validate())
}

func loadCarton(fields *Object, path string) (CartonMaster, error) {
	if err := requireKeys(fields, path, []string{"id", "inner_dimensions_mm", "max_payload_g"}, []string{"cost_minor"}); err != nil {
		return CartonMaster{}, err
	}
	var carton CartonMaster
	var err error
	if carton.ID, err = text(fields.lookup("id"), path+".id"); err != nil {
		return CartonMaster{}, err
	}
	if carton.InnerDimensionsMM, err = dimensions(fields.lookup("inner_dimensions_mm"), path+".inner_dimensions_mm", 3); err != nil {
		return CartonMaster{}, err
	}
	if carton.MaxPayloadG, err = integer(fields.lookup("max_payload_g"), path+".max_payload_g"); err != nil {
		return CartonMaster{}, err
	}
	if carton.CostMinor, err = integer(orDefault(fields, "cost_minor", big.NewInt(0)), path+".cost_minor"); err != nil {
		return CartonMaster{}, err
	}
	return carton, modelError(path, carton.validate())
}

func loadPallet(fields *Object, path string) (PalletMaster, error) {
	if err := requireKeys(fields, path, []string{"id", "deck_dimensions_mm", "max_payload_g"}, []string{"max_stack_height_mm"}); err != nil {
		return PalletMaster{}, err
	}
	var pallet PalletMaster
	var err error
	if pallet.ID, err = text(fields.lookup("id"), path+".id"); err != nil {
		return PalletMaster{}, err
	}
	if pallet.DeckDimensionsMM, err = dimensions(fields.lookup("deck_dimensions_mm"), path+".deck_dimensions_mm", 2); err != nil {
		return PalletMaster{}, err
	}
	if pallet.MaxPayloadG, err = integer(fields.lookup("max_payload_g"), path+".max_payload_g"); err != nil {
		return PalletMaster{}, err
	}
	if pallet.MaxStackHeightMM, err = optionalInteger(fields.lookup("max_stack_height_mm"), path+".max_stack_height_mm"); err != nil {
		return PalletMaster{}, err
	}
	return pallet, modelError(path, pallet.validate())
}

func loadExclusion(fields *Object, path string) (ExclusionRule, error) {
	if err := requireKeys(fields, path, []string{"id", "scope", "subject_id", "excluded_id"}, []string{"reason"}); err != nil {
		return ExclusionRule{}, err
	}
	var rule ExclusionRule
	var err error
	if rule.ID, err = text(fields.lookup("id"), path+".id"); err != nil {
		return ExclusionRule{}, err
	}
	scope, err := enumValue(fields.lookup("scope"), path+".scope", "ExclusionScope", exclusionScopes)
	if err != nil {
		return ExclusionRule{}, err
	}
	rule.Scope = ExclusionScope(scope)
	if rule.SubjectID, err = text(fields.lookup("subject_id"), path+".subject_id"); err != nil {
		return ExclusionRule{}, err
	}
	if rule.ExcludedID, err = text(fields.lookup("excluded_id"), path+".excluded_id"); err != nil {
		return ExclusionRule{}, err
	}
	if rule.Reason, err = text(orDefault(fields, "reason", ""), path+".reason"); err != nil {
		return ExclusionRule{}, err
	}
	return rule, modelError(path, rule.validate())
}

func loadOverride(fields *Object, path string) (FacilityOverride, error) {
	if err := requireKeys(fields, path, []string{"id", "facility_id", "entry_id", "kind", "override"}, nil); err != nil {
		return FacilityOverride{}, err
	}
	kind, err := text(fields.lookup("kind"), path+".kind")
	if err != nil {
		return FacilityOverride{}, err
	}
	load, known := entryLoaders[kind]
	if !known {
		return FacilityOverride{}, inputError(path+".kind", "expected one of "+pythonList(sortedLoaderKinds()))
	}
	overrideFields, err := mapping(fields.lookup("override"), path+".override")
	if err != nil {
		return FacilityOverride{}, err
	}
	var override FacilityOverride
	if override.Override, err = load(overrideFields, path+".override"); err != nil {
		return FacilityOverride{}, err
	}
	if override.ID, err = text(fields.lookup("id"), path+".id"); err != nil {
		return FacilityOverride{}, err
	}
	if override.FacilityID, err = text(fields.lookup("facility_id"), path+".facility_id"); err != nil {
		return FacilityOverride{}, err
	}
	if override.EntryID, err = text(fields.lookup("entry_id"), path+".entry_id"); err != nil {
		return FacilityOverride{}, err
	}
	return override, modelError(path, override.validate())
}

func sortedLoaderKinds() []string {
	kinds := make([]string, 0, len(entryLoaders))
	for kind := range entryLoaders {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// snapshotEntries walks one optional snapshot section, handing each object to load.
func snapshotEntries(fields *Object, key, path string, load func(*Object, string) error) error {
	sectionPath := path + "." + key
	entries, err := sequence(orDefault(fields, key, []interface{}{}), sectionPath)
	if err != nil {
		return err
	}
	for index, entry := range entries {
		entryPath := indexed(sectionPath, index)
		entryFields, err := mapping(entry, entryPath)
		if err != nil {
			return err
		}
		if err := load(entryFields, entryPath); err != nil {
			return err
		}
	}
	return nil
}

func loadSnapshot(value interface{}, path string) (*CatalogSnapshot, error) {
	fields, err := mapping(value, path)
	if err != nil {
		return nil, err
	}
	if err := requireKeys(fields, path, nil, []string{"items", "cartons", "pallets", "exclusions", "overrides"}); err != nil {
		return nil, err
	}
	snapshot := &CatalogSnapshot{}
	sections := []struct {
		key  string
		load func(*Object, string) error
	}{
		{"items", func(entry *Object, entryPath string) error {
			item, err := loadItem(entry, entryPath)
			snapshot.Items = append(snapshot.Items, item)
			return err
		}},
		{"cartons", func(entry *Object, entryPath string) error {
			carton, err := loadCarton(entry, entryPath)
			snapshot.Cartons = append(snapshot.Cartons, carton)
			return err
		}},
		{"pallets", func(entry *Object, entryPath string) error {
			pallet, err := loadPallet(entry, entryPath)
			snapshot.Pallets = append(snapshot.Pallets, pallet)
			return err
		}},
		{"exclusions", func(entry *Object, entryPath string) error {
			rule, err := loadExclusion(entry, entryPath)
			snapshot.Exclusions = append(snapshot.Exclusions, rule)
			return err
		}},
		{"overrides", func(entry *Object, entryPath string) error {
			override, err := loadOverride(entry, entryPath)
			snapshot.Overrides = append(snapshot.Overrides, override)
			return err
		}},
	}
	for _, section := range sections {
		if err := snapshotEntries(fields, section.key, path, section.load); err != nil {
			return nil, err
		}
	}
	return snapshot, modelError(path, snapshot.validate())
}

func loadCatalogs(value interface{}) (map[string]*CatalogRegistry, error) {
	entries, err := sequence(value, "document.catalogs")
	if err != nil {
		return nil, err
	}
	catalogs := map[string]*CatalogRegistry{}
	for index, entry := range entries {
		path := indexed("document.catalogs", index)
		fields, err := mapping(entry, path)
		if err != nil {
			return nil, err
		}
		if err := requireKeys(fields, path, []string{"catalog_id", "versions"}, nil); err != nil {
			return nil, err
		}
		catalogID, err := text(fields.lookup("catalog_id"), path+".catalog_id")
		if err != nil {
			return nil, err
		}
		if _, duplicate := catalogs[catalogID]; duplicate {
			return nil, inputError(path, "duplicate catalog history for "+pythonRepr(catalogID))
		}
		registry, err := newCatalogRegistry(catalogID)
		if err != nil {
			return nil, modelError(path, err)
		}
		if err := publishCatalogVersions(registry, fields.lookup("versions"), path); err != nil {
			return nil, err
		}
		catalogs[catalogID] = registry
	}
	return catalogs, nil
}

func publishCatalogVersions(registry *CatalogRegistry, value interface{}, parent string) error {
	versions, err := sequence(value, parent+".versions")
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return inputError(parent+".versions", "a catalog history needs at least one version")
	}
	for index, entry := range versions {
		path := indexed(parent+".versions", index)
		fields, err := mapping(entry, path)
		if err != nil {
			return err
		}
		// The key's presence alone selects the rollback shape, even with a null value.
		if _, isRollback := fields.Get("rollback_to"); isRollback {
			err = publishRollback(registry, fields, path)
		} else {
			err = publishCatalogVersion(registry, fields, path)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func publishCatalogVersion(registry *CatalogRegistry, fields *Object, path string) error {
	if err := requireKeys(fields, path, []string{"effective_at", "published_at", "snapshot"}, []string{"note"}); err != nil {
		return err
	}
	snapshot, err := loadSnapshot(fields.lookup("snapshot"), path+".snapshot")
	if err != nil {
		return err
	}
	effectiveAt, err := integer(fields.lookup("effective_at"), path+".effective_at")
	if err != nil {
		return err
	}
	publishedAt, err := integer(fields.lookup("published_at"), path+".published_at")
	if err != nil {
		return err
	}
	note, err := text(orDefault(fields, "note", ""), path+".note")
	if err != nil {
		return err
	}
	return modelError(path, registry.publish(snapshot, effectiveAt, publishedAt, note))
}

func publishRollback(registry *CatalogRegistry, fields *Object, path string) error {
	if err := requireKeys(fields, path, []string{"rollback_to", "published_at"}, []string{"effective_at", "note"}); err != nil {
		return err
	}
	effectiveAt, err := optionalInteger(fields.lookup("effective_at"), path+".effective_at")
	if err != nil {
		return err
	}
	target, err := integer(fields.lookup("rollback_to"), path+".rollback_to")
	if err != nil {
		return err
	}
	publishedAt, err := integer(fields.lookup("published_at"), path+".published_at")
	if err != nil {
		return err
	}
	note, err := text(orDefault(fields, "note", ""), path+".note")
	if err != nil {
		return err
	}
	if err := registry.rollback(target, publishedAt, effectiveAt, note); err != nil {
		if err == errCatalogVersionNotFound {
			return inputError(path, "catalog "+pythonRepr(registry.catalogID)+" has no version "+target.String())
		}
		return modelError(path, err)
	}
	return nil
}
