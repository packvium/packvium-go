package commerce

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// The versioned carrier rating model. Every tariff is a numbered, effective-dated
// version of one (carrier_id, service_id) pair, and every charge is exact integer
// arithmetic on minor currency units and permille, with every inexact division rounded
// up -- never down and never through a float.

var thousand = big.NewInt(1000)

// ceilDiv is ceil(numerator / denominator) for a positive denominator. big.Int.Div is
// Euclidean, which equals floor division when the divisor is positive.
func ceilDiv(numerator, denominator *big.Int) *big.Int {
	negated := new(big.Int).Neg(numerator)
	return negated.Div(negated, denominator).Neg(negated)
}

// AccessorialCharge is one named accessorial: a flat charge or a permille of the base
// charge, never both.
type AccessorialCharge struct {
	AccessorialID   string
	FlatChargeMinor *big.Int
	PermilleOfBase  *big.Int
}

func newAccessorialCharge(id string, flat, permille *big.Int) (AccessorialCharge, error) {
	if id == "" {
		return AccessorialCharge{}, errors.New("accessorial_id is required")
	}
	if (flat == nil) == (permille == nil) {
		return AccessorialCharge{}, errors.New(
			"an accessorial must set exactly one of flat_charge_minor or permille_of_base")
	}
	if flat != nil && flat.Sign() < 0 {
		return AccessorialCharge{}, errors.New("flat_charge_minor cannot be negative")
	}
	if permille != nil && permille.Sign() < 0 {
		return AccessorialCharge{}, errors.New("permille_of_base cannot be negative")
	}
	return AccessorialCharge{AccessorialID: id, FlatChargeMinor: flat, PermilleOfBase: permille}, nil
}

func (charge AccessorialCharge) chargeMinor(baseChargeMinor *big.Int) *big.Int {
	if charge.FlatChargeMinor != nil {
		return charge.FlatChargeMinor
	}
	return ceilDiv(new(big.Int).Mul(baseChargeMinor, charge.PermilleOfBase), thousand)
}

// Tariff is one immutable, numbered version of one carrier service's rate card.
type Tariff struct {
	CarrierID                 string
	ServiceID                 string
	Version                   int
	EffectiveAt               *big.Int
	DimensionalWeightDivisor  *big.Int
	CostPerDimensionalKgMinor map[string]*big.Int
	MinimumChargeMinor        *big.Int
	FuelSurchargePermille     *big.Int
	Accessorials              map[string]AccessorialCharge
}

func (tariff *Tariff) validate() error {
	switch {
	case tariff.CarrierID == "":
		return errors.New("carrier_id is required")
	case tariff.ServiceID == "":
		return errors.New("service_id is required")
	case tariff.EffectiveAt.Sign() < 0:
		return errors.New("effective_at cannot be negative")
	case tariff.DimensionalWeightDivisor.Sign() <= 0:
		return errors.New("dimensional_weight_divisor must be positive")
	case tariff.MinimumChargeMinor.Sign() < 0:
		return errors.New("minimum_charge_minor cannot be negative")
	case tariff.FuelSurchargePermille.Sign() < 0:
		return errors.New("fuel_surcharge_permille cannot be negative")
	}
	for _, cost := range tariff.CostPerDimensionalKgMinor {
		if cost.Sign() < 0 {
			return errors.New("cost_per_dimensional_kg_minor entries cannot be negative")
		}
	}
	return nil
}

// RatingRequest is what is being rated: the shipment's real weight and volume, its
// zone, and the accessorials this shipment needs, in the caller's order.
type RatingRequest struct {
	Zone                  string
	ActualWeightG         *big.Int
	VolumeMM3             *big.Int
	RequestedAccessorials []string
}

func (request RatingRequest) validate() error {
	switch {
	case request.Zone == "":
		return errors.New("zone is required")
	case request.ActualWeightG.Sign() < 0:
		return errors.New("actual_weight_g cannot be negative")
	case request.VolumeMM3.Sign() < 0:
		return errors.New("volume_mm3 cannot be negative")
	}
	seen := map[string]bool{}
	for _, id := range request.RequestedAccessorials {
		if id == "" {
			return errors.New("requested accessorial ids must be non-empty")
		}
	}
	for _, id := range request.RequestedAccessorials {
		if seen[id] {
			return errors.New("requested accessorial ids must be unique")
		}
		seen[id] = true
	}
	return nil
}

// AccessorialAmount is one accessorial's charge in a breakdown.
type AccessorialAmount struct {
	AccessorialID string
	AmountMinor   *big.Int
}

// RateBreakdown itemizes every component of TotalMinor and the tariff version that
// produced it.
type RateBreakdown struct {
	CarrierID               string
	ServiceID               string
	TariffVersion           int
	Zone                    string
	ActualWeightG           *big.Int
	DimensionalWeightG      *big.Int
	BilledWeightG           *big.Int
	BaseChargeMinor         *big.Int
	MinimumChargeApplied    bool
	FuelSurchargeMinor      *big.Int
	AccessorialChargesMinor []AccessorialAmount
	TotalMinor              *big.Int
}

// unavailableService names exactly which zone or accessorial ids a tariff lacks;
// exactly one of zone and accessorialIDs is set.
type unavailableService struct {
	message        string
	zone           *string
	accessorialIDs []string
}

func (err *unavailableService) Error() string {
	return err.message
}

// rateTariff rates a request against one resolved tariff version. O(a log a) in the
// requested accessorials, for the sort of the unknown ones.
func rateTariff(tariff *Tariff, request RatingRequest) (RateBreakdown, error) {
	rate, priced := tariff.CostPerDimensionalKgMinor[request.Zone]
	if !priced {
		zone := request.Zone
		return RateBreakdown{}, &unavailableService{
			message: fmt.Sprintf("tariff %s/%s v%d has no rate for zone %s",
				tariff.CarrierID, tariff.ServiceID, tariff.Version, pythonRepr(zone)),
			zone: &zone,
		}
	}
	if unknown := unknownAccessorials(tariff, request.RequestedAccessorials); len(unknown) > 0 {
		return RateBreakdown{}, &unavailableService{
			message: fmt.Sprintf("tariff %s/%s v%d does not offer accessorial(s) %s",
				tariff.CarrierID, tariff.ServiceID, tariff.Version, pythonList(unknown)),
			accessorialIDs: unknown,
		}
	}

	dimensionalWeight := ceilDiv(request.VolumeMM3, tariff.DimensionalWeightDivisor)
	billedWeight := request.ActualWeightG
	if dimensionalWeight.Cmp(billedWeight) > 0 {
		billedWeight = dimensionalWeight
	}
	rawBaseCharge := ceilDiv(new(big.Int).Mul(billedWeight, rate), thousand)
	minimumApplied := rawBaseCharge.Cmp(tariff.MinimumChargeMinor) < 0
	baseCharge := rawBaseCharge
	if minimumApplied {
		baseCharge = tariff.MinimumChargeMinor
	}
	fuelSurcharge := ceilDiv(new(big.Int).Mul(baseCharge, tariff.FuelSurchargePermille), thousand)

	total := new(big.Int).Add(baseCharge, fuelSurcharge)
	charges := make([]AccessorialAmount, 0, len(request.RequestedAccessorials))
	for _, id := range request.RequestedAccessorials {
		amount := tariff.Accessorials[id].chargeMinor(baseCharge)
		charges = append(charges, AccessorialAmount{AccessorialID: id, AmountMinor: amount})
		total.Add(total, amount)
	}

	return RateBreakdown{
		CarrierID:               tariff.CarrierID,
		ServiceID:               tariff.ServiceID,
		TariffVersion:           tariff.Version,
		Zone:                    request.Zone,
		ActualWeightG:           request.ActualWeightG,
		DimensionalWeightG:      dimensionalWeight,
		BilledWeightG:           billedWeight,
		BaseChargeMinor:         baseCharge,
		MinimumChargeApplied:    minimumApplied,
		FuelSurchargeMinor:      fuelSurcharge,
		AccessorialChargesMinor: charges,
		TotalMinor:              total,
	}, nil
}

func unknownAccessorials(tariff *Tariff, requested []string) []string {
	var unknown []string
	for _, id := range requested {
		if _, offered := tariff.Accessorials[id]; !offered {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	return unknown
}

type serviceKey struct {
	carrierID string
	serviceID string
}

// CarrierRegistry is the per-(carrier_id, service_id) append-only tariff history.
type CarrierRegistry struct {
	versions map[serviceKey][]*Tariff
}

func newCarrierRegistry() *CarrierRegistry {
	return &CarrierRegistry{versions: map[serviceKey][]*Tariff{}}
}

type tariffTerms struct {
	effectiveAt               *big.Int
	dimensionalWeightDivisor  *big.Int
	costPerDimensionalKgMinor map[string]*big.Int
	minimumChargeMinor        *big.Int
	fuelSurchargePermille     *big.Int
	accessorials              map[string]AccessorialCharge
}

// publish appends the next numbered version; its number is its 1-based position.
func (registry *CarrierRegistry) publish(carrierID, serviceID string, terms tariffTerms) (*Tariff, error) {
	key := serviceKey{carrierID, serviceID}
	history := registry.versions[key]
	tariff := &Tariff{
		CarrierID:                 carrierID,
		ServiceID:                 serviceID,
		Version:                   len(history) + 1,
		EffectiveAt:               terms.effectiveAt,
		DimensionalWeightDivisor:  terms.dimensionalWeightDivisor,
		CostPerDimensionalKgMinor: terms.costPerDimensionalKgMinor,
		MinimumChargeMinor:        terms.minimumChargeMinor,
		FuelSurchargePermille:     terms.fuelSurchargePermille,
		Accessorials:              terms.accessorials,
	}
	if err := tariff.validate(); err != nil {
		return nil, err
	}
	registry.versions[key] = append(history, tariff)
	return tariff, nil
}

// errTariffNotFound marks a missing history, version or effective version.
var errTariffNotFound = errors.New("tariff not found")

func (registry *CarrierRegistry) history(carrierID, serviceID string) ([]*Tariff, error) {
	history, known := registry.versions[serviceKey{carrierID, serviceID}]
	if !known {
		return nil, errTariffNotFound
	}
	return history, nil
}

// tariff resolves one pinned version. O(h) in the history length.
func (registry *CarrierRegistry) tariff(carrierID, serviceID string, version *big.Int) (*Tariff, error) {
	history, err := registry.history(carrierID, serviceID)
	if err != nil {
		return nil, err
	}
	for _, candidate := range history {
		if version.Cmp(big.NewInt(int64(candidate.Version))) == 0 {
			return candidate, nil
		}
	}
	return nil, errTariffNotFound
}

// effectiveTariff resolves the highest effective_at not after asOf, ties to the later
// version. O(h).
func (registry *CarrierRegistry) effectiveTariff(carrierID, serviceID string, asOf *big.Int) (*Tariff, error) {
	history, err := registry.history(carrierID, serviceID)
	if err != nil {
		return nil, err
	}
	var best *Tariff
	for _, candidate := range history {
		if candidate.EffectiveAt.Cmp(asOf) > 0 {
			continue
		}
		// Versions ascend, so a later candidate with an equal effective_at wins the tie.
		if best == nil || candidate.EffectiveAt.Cmp(best.EffectiveAt) >= 0 {
			best = candidate
		}
	}
	if best == nil {
		return nil, errTariffNotFound
	}
	return best, nil
}
