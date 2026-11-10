package commerce

import (
	"errors"
	"fmt"
	"math/big"
)

// The versioned item, carton and pallet master data. A catalog's history is
// append-only: a rollback is a new, higher-numbered version whose snapshot equals a
// prior one's, and a version can be published now but take effect later.

// ItemMaster is one SKU master record.
type ItemMaster struct {
	ID           string
	DimensionsMM []*big.Int
	WeightG      *big.Int
	Description  string
}

// CartonMaster is one carton master record.
type CartonMaster struct {
	ID                string
	InnerDimensionsMM []*big.Int
	MaxPayloadG       *big.Int
	CostMinor         *big.Int
}

// PalletMaster is one pallet master record; MaxStackHeightMM is nil when unbounded.
type PalletMaster struct {
	ID               string
	DeckDimensionsMM []*big.Int
	MaxPayloadG      *big.Int
	MaxStackHeightMM *big.Int
}

// ExclusionScope says which kinds of entry an exclusion pairs.
type ExclusionScope string

// The closed exclusion vocabulary.
const (
	ExclusionItemCarton ExclusionScope = "item_carton"
	ExclusionItemPallet ExclusionScope = "item_pallet"
)

var exclusionScopes = []string{string(ExclusionItemCarton), string(ExclusionItemPallet)}

// ExclusionRule forbids one entry from being packed with another.
type ExclusionRule struct {
	ID         string
	Scope      ExclusionScope
	SubjectID  string
	ExcludedID string
	Reason     string
}

// FacilityOverride replaces one base entry at one facility. Override is an
// ItemMaster, CartonMaster or PalletMaster.
type FacilityOverride struct {
	ID         string
	FacilityID string
	EntryID    string
	Override   interface{}
}

// CatalogSnapshot is the complete content of one catalog version.
type CatalogSnapshot struct {
	Items      []ItemMaster
	Cartons    []CartonMaster
	Pallets    []PalletMaster
	Exclusions []ExclusionRule
	Overrides  []FacilityOverride
}

func requirePositiveDimensions(label string, dimensions []*big.Int) error {
	for _, dimension := range dimensions {
		if dimension.Sign() <= 0 {
			return fmt.Errorf("%s dimensions must be positive", label)
		}
	}
	return nil
}

func (item ItemMaster) validate() error {
	switch {
	case item.ID == "":
		return errors.New("item id is required")
	case len(item.DimensionsMM) != 3:
		return errors.New("item dimensions must have exactly three axes")
	}
	if err := requirePositiveDimensions("item", item.DimensionsMM); err != nil {
		return err
	}
	if item.WeightG.Sign() <= 0 {
		return errors.New("item weight must be positive")
	}
	return nil
}

func (carton CartonMaster) validate() error {
	switch {
	case carton.ID == "":
		return errors.New("carton id is required")
	case len(carton.InnerDimensionsMM) != 3:
		return errors.New("carton dimensions must have exactly three axes")
	}
	if err := requirePositiveDimensions("carton", carton.InnerDimensionsMM); err != nil {
		return err
	}
	switch {
	case carton.MaxPayloadG.Sign() <= 0:
		return errors.New("carton max_payload_g must be positive")
	case carton.CostMinor.Sign() < 0:
		return errors.New("cost_minor cannot be negative")
	}
	return nil
}

func (pallet PalletMaster) validate() error {
	switch {
	case pallet.ID == "":
		return errors.New("pallet id is required")
	case len(pallet.DeckDimensionsMM) != 2:
		return errors.New("pallet deck dimensions must have exactly two axes")
	}
	if err := requirePositiveDimensions("pallet", pallet.DeckDimensionsMM); err != nil {
		return err
	}
	switch {
	case pallet.MaxPayloadG.Sign() <= 0:
		return errors.New("pallet max_payload_g must be positive")
	case pallet.MaxStackHeightMM != nil && pallet.MaxStackHeightMM.Sign() <= 0:
		return errors.New("max_stack_height_mm must be positive")
	}
	return nil
}

func (rule ExclusionRule) validate() error {
	if rule.ID == "" {
		return errors.New("exclusion id is required")
	}
	if rule.SubjectID == "" || rule.ExcludedID == "" {
		return errors.New("an exclusion rule must reference both a subject and an excluded id")
	}
	return nil
}

func (override FacilityOverride) validate() error {
	switch {
	case override.ID == "":
		return errors.New("facility override id is required")
	case override.FacilityID == "":
		return errors.New("facility_id is required")
	case masterID(override.Override) != override.EntryID:
		return errors.New("a facility override's entry_id must match override.id")
	}
	return nil
}

func masterID(entry interface{}) string {
	switch typed := entry.(type) {
	case ItemMaster:
		return typed.ID
	case CartonMaster:
		return typed.ID
	case PalletMaster:
		return typed.ID
	}
	return ""
}

func (snapshot CatalogSnapshot) validate() error {
	checks := []struct {
		label string
		ids   []string
	}{
		{"item", snapshot.itemIDs()},
		{"carton", snapshot.cartonIDs()},
		{"pallet", snapshot.palletIDs()},
		{"exclusion", exclusionIDs(snapshot.Exclusions)},
		{"facility override", overrideIDs(snapshot.Overrides)},
	}
	for _, check := range checks {
		if hasDuplicate(check.ids) {
			return fmt.Errorf("duplicate %s ids in catalog snapshot", check.label)
		}
	}
	return nil
}

func (snapshot CatalogSnapshot) itemIDs() []string {
	ids := make([]string, len(snapshot.Items))
	for index, entry := range snapshot.Items {
		ids[index] = entry.ID
	}
	return ids
}

func (snapshot CatalogSnapshot) cartonIDs() []string {
	ids := make([]string, len(snapshot.Cartons))
	for index, entry := range snapshot.Cartons {
		ids[index] = entry.ID
	}
	return ids
}

func (snapshot CatalogSnapshot) palletIDs() []string {
	ids := make([]string, len(snapshot.Pallets))
	for index, entry := range snapshot.Pallets {
		ids[index] = entry.ID
	}
	return ids
}

func exclusionIDs(rules []ExclusionRule) []string {
	ids := make([]string, len(rules))
	for index, entry := range rules {
		ids[index] = entry.ID
	}
	return ids
}

func overrideIDs(overrides []FacilityOverride) []string {
	ids := make([]string, len(overrides))
	for index, entry := range overrides {
		ids[index] = entry.ID
	}
	return ids
}

func hasDuplicate(ids []string) bool {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return true
		}
		seen[id] = true
	}
	return false
}

// CatalogVersion is one numbered entry of a catalog's history. PublishedAt is audit
// only; it never affects resolution. RolledBackFrom is 0 unless this is a rollback.
type CatalogVersion struct {
	Number         int
	Snapshot       *CatalogSnapshot
	EffectiveAt    *big.Int
	PublishedAt    *big.Int
	RolledBackFrom int
	Note           string
}

func (version *CatalogVersion) validate() error {
	switch {
	case version.EffectiveAt.Sign() < 0:
		return errors.New("effective_at cannot be negative")
	case version.PublishedAt.Sign() < 0:
		return errors.New("published_at cannot be negative")
	}
	return nil
}

// CatalogRegistry is one catalog's append-only publication history.
type CatalogRegistry struct {
	catalogID string
	versions  []*CatalogVersion
}

func newCatalogRegistry(catalogID string) (*CatalogRegistry, error) {
	if catalogID == "" {
		return nil, errors.New("catalog_id is required")
	}
	return &CatalogRegistry{catalogID: catalogID}, nil
}

func (registry *CatalogRegistry) publish(snapshot *CatalogSnapshot, effectiveAt, publishedAt *big.Int, note string) error {
	version := &CatalogVersion{
		Number:      len(registry.versions) + 1,
		Snapshot:    snapshot,
		EffectiveAt: effectiveAt,
		PublishedAt: publishedAt,
		Note:        note,
	}
	if err := version.validate(); err != nil {
		return err
	}
	registry.versions = append(registry.versions, version)
	return nil
}

// rollback appends a version re-publishing an earlier one's snapshot. A nil
// effectiveAt takes effect at publishedAt.
func (registry *CatalogRegistry) rollback(target *big.Int, publishedAt, effectiveAt *big.Int, note string) error {
	source, err := registry.numbered(target)
	if err != nil {
		return err
	}
	if effectiveAt == nil {
		effectiveAt = publishedAt
	}
	if note == "" {
		note = fmt.Sprintf("rollback to version %d", source.Number)
	}
	version := &CatalogVersion{
		Number:         len(registry.versions) + 1,
		Snapshot:       source.Snapshot,
		EffectiveAt:    effectiveAt,
		PublishedAt:    publishedAt,
		RolledBackFrom: source.Number,
		Note:           note,
	}
	if err := version.validate(); err != nil {
		return err
	}
	registry.versions = append(registry.versions, version)
	return nil
}

// The three ways a catalog reference fails to resolve.
var (
	errCatalogVersionNotFound    = errors.New("catalog version not found")
	errNoEffectiveCatalog        = errors.New("no effective catalog version")
	errAmbiguousCatalogReference = errors.New("ambiguous catalog reference")
)

func (registry *CatalogRegistry) numbered(number *big.Int) (*CatalogVersion, error) {
	for _, version := range registry.versions {
		if number.Cmp(big.NewInt(int64(version.Number))) == 0 {
			return version, nil
		}
	}
	return nil, errCatalogVersionNotFound
}

// resolve pins a version by number, by the version effective at asOf (ties to the
// later version), or -- with neither -- by the only version there is. O(v).
func (registry *CatalogRegistry) resolve(version, asOf *big.Int) (*CatalogVersion, error) {
	if version != nil {
		return registry.numbered(version)
	}
	if asOf == nil {
		switch {
		case len(registry.versions) > 1:
			return nil, errAmbiguousCatalogReference
		case len(registry.versions) == 0:
			return nil, errCatalogVersionNotFound
		}
		return registry.versions[0], nil
	}
	var best *CatalogVersion
	for _, candidate := range registry.versions {
		if candidate.EffectiveAt.Cmp(asOf) > 0 {
			continue
		}
		if best == nil || candidate.EffectiveAt.Cmp(best.EffectiveAt) >= 0 {
			best = candidate
		}
	}
	if best == nil {
		return nil, errNoEffectiveCatalog
	}
	return best, nil
}
