package algorithm

import (
	"math/big"

	"github.com/toxakara/packvium-go/config"
	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/internal/ints"
	"github.com/toxakara/packvium-go/units"
)

// LandedCostObjective is the objective that ranks by the money a shipment costs.
const LandedCostObjective = "lowest_landed_cost"

// UnpriceableMinor is the landed charge of a container its tariff cannot price: a search
// device that loses the candidate, never an answer, and the same sentinel as the other
// engines.
const UnpriceableMinor int64 = 1<<63 - 1

var scorePPM = big.NewInt(1_000_000)

// ObjectiveScore is a lexicographic objective vector; lower is better, compared key by key.
type ObjectiveScore []int64

// Compare orders two vectors key by key, a shorter prefix first.
func (s ObjectiveScore) Compare(other ObjectiveScore) int { return Ints(s).Compare(Ints(other)) }

// SolutionScorer ranks one start's answer.
type SolutionScorer interface {
	Score(solution RawSolution) ObjectiveScore
}

// DefaultSolutionScorer is the canonical objective vector: unpacked count, container count,
// total cost, unused volume (ppm, floored per container) and stack height (ppm, floored per
// container). Every key is an exact integer, so all engines agree bit for bit. O(c).
type DefaultSolutionScorer struct{}

// Score is the five-key default vector.
func (DefaultSolutionScorer) Score(solution RawSolution) ObjectiveScore {
	cost, unused, height := int64(0), int64(0), int64(0)
	for _, packed := range solution.Containers {
		inner := packed.Container().InnerDimensions()
		cost = ints.AddExact(cost, packed.Container().CostMinor())
		volume := inner.Volume()
		free := new(big.Int).Sub(volume, packed.UsedVolume())
		unused = ints.AddExact(unused, exactInt64(free.Mul(free, scorePPM).Quo(free, volume)))
		top := big.NewInt(packed.MaxZTicks())
		height = ints.AddExact(height, exactInt64(top.Mul(top, scorePPM).Quo(top, big.NewInt(inner.Height().Ticks()))))
	}
	return ObjectiveScore{int64(len(solution.Unpacked)), int64(len(solution.Containers)), cost, unused, height}
}

// exactInt64 refuses a value outside int64 as the reference's exact narrowing does.
func exactInt64(value *big.Int) int64 {
	if !value.IsInt64() {
		panic(ints.Overflow{Operation: "narrowing"})
	}
	return value.Int64()
}

// LowestCostSolutionScorer ranks total container cost ahead of container count and unused
// volume; completeness still dominates.
type LowestCostSolutionScorer struct{}

// Score reorders the default vector.
func (LowestCostSolutionScorer) Score(solution RawSolution) ObjectiveScore {
	base := DefaultSolutionScorer{}.Score(solution)
	return ObjectiveScore{base[0], base[2], base[1], base[3], base[4]}
}

// MaximumValueSolutionScorer ranks the declared value of unpacked items ahead of container
// count, cost and unused volume; unpacked count still leads. O(u).
type MaximumValueSolutionScorer struct{}

// Score puts the value forgone second.
func (MaximumValueSolutionScorer) Score(solution RawSolution) ObjectiveScore {
	base := DefaultSolutionScorer{}.Score(solution)
	forgone := int64(0)
	for _, item := range solution.Unpacked {
		if value := item.Instance.Item().Value(); value != nil {
			forgone = ints.AddExact(forgone, *value)
		}
	}
	return ObjectiveScore{base[0], forgone, base[1], base[2], base[3]}
}

// OpenDimensionSolutionScorer ranks the summed raw stack height ahead of container count,
// cost and unused volume: for a container whose height is a generous ceiling, the question is
// how tall the load ends up. O(c).
type OpenDimensionSolutionScorer struct{}

// Score puts the achieved height second.
func (OpenDimensionSolutionScorer) Score(solution RawSolution) ObjectiveScore {
	base := DefaultSolutionScorer{}.Score(solution)
	achieved := int64(0)
	for _, packed := range solution.Containers {
		achieved = ints.AddExact(achieved, packed.MaxZTicks())
	}
	return ObjectiveScore{base[0], achieved, base[1], base[2], base[3]}
}

// ShippingCostSolutionScorer ranks total carrier-billable weight ahead of container count,
// unused volume and stack height. Each container bills at max(gross weight, dimensional
// weight), the dimensional weight taken from the outer dimensions when declared.
type ShippingCostSolutionScorer struct {
	Divisor    int64
	LengthUnit string
	WeightUnit string
}

// ShippingCostFromConfig refuses a configuration without a divisor: there is no
// library-chosen default, a wrong guess would silently misprice every shipment.
func ShippingCostFromConfig(cfg *config.PackingConfig) ShippingCostSolutionScorer {
	return ShippingCostSolutionScorer{requireDivisor(cfg, "shipping_cost"), cfg.DimensionalWeightLengthUnit(),
		cfg.DimensionalWeightWeightUnit()}
}

func requireDivisor(cfg *config.PackingConfig, objective string) int64 {
	divisor := cfg.DimensionalWeightDivisor()
	if divisor == nil {
		refuse(&UnknownObjectiveError{"the " + objective + " objective requires configuration.dimensional_weight_divisor"})
	}
	return *divisor
}

func (s ShippingCostSolutionScorer) billed(packed *domain.PackedContainer) int64 {
	return BilledTicks(packed.Container(), packed.PayloadWeight().Ticks(), s.Divisor, s.LengthUnit, s.WeightUnit)
}

// BilledTicks is max(gross weight, dimensional weight of the shipped box): the one definition
// the search, the score and the final guard all use, so they cannot bill a container
// differently.
func BilledTicks(container *domain.Container, payloadTicks, divisor int64, lengthUnit, weightUnit string) int64 {
	dimensional, err := container.EffectiveOuterDimensions().DimensionalWeight(divisor, lengthUnit, weightUnit)
	if err != nil {
		refuse(err)
	}
	return ints.Max(ints.AddExact(container.TareWeight().Ticks(), payloadTicks), dimensional.Ticks())
}

// BilledTicksForConfig is BilledTicks with the configuration's divisor and units.
func BilledTicksForConfig(container *domain.Container, payloadTicks int64, cfg *config.PackingConfig) int64 {
	return BilledTicks(container, payloadTicks, requireDivisor(cfg, cfg.Objective()),
		cfg.DimensionalWeightLengthUnit(), cfg.DimensionalWeightWeightUnit())
}

// Score puts the billable weight second.
func (s ShippingCostSolutionScorer) Score(solution RawSolution) ObjectiveScore {
	base := DefaultSolutionScorer{}.Score(solution)
	billable := int64(0)
	for _, packed := range solution.Containers {
		billable = ints.AddExact(billable, s.billed(packed))
	}
	return ObjectiveScore{base[0], billable, base[1], base[3], base[4]}
}

// LandedCostSolutionScorer ranks by the money a shipment costs, not the grams it is billed
// at: a bracket step or minimum charge means the cheaper answer is not always the lighter
// one. The billed weight is the shipping scorer's, so the two never disagree about what is
// priced. A billed weight past the last bracket loses the candidate (UnpriceableMinor)
// instead of aborting a run that has a shippable alternative.
type LandedCostSolutionScorer struct{ ShippingCostSolutionScorer }

// LandedCostFromConfig refuses a configuration without a divisor.
func LandedCostFromConfig(cfg *config.PackingConfig) LandedCostSolutionScorer {
	return LandedCostSolutionScorer{ShippingCostSolutionScorer{requireDivisor(cfg, LandedCostObjective),
		cfg.DimensionalWeightLengthUnit(), cfg.DimensionalWeightWeightUnit()}}
}

// Score puts the landed charge second.
func (s LandedCostSolutionScorer) Score(solution RawSolution) ObjectiveScore {
	base := DefaultSolutionScorer{}.Score(solution)
	landed := int64(0)
	for _, packed := range solution.Containers {
		if packed.Container().RateTable() == nil {
			refuse(missingRateTable(packed.Container().ID()))
		}
		charge := ChargeOrUnpriceable(packed.Container(), s.billed(packed))
		if charge == UnpriceableMinor {
			landed = UnpriceableMinor
			break
		}
		landed = ints.AddExact(landed, charge)
	}
	return ObjectiveScore{base[0], landed, base[1], base[3], base[4]}
}

func missingRateTable(containerID string) error {
	return &UnknownObjectiveError{"the lowest_landed_cost objective requires a rate_table on every container; '" +
		containerID + "' has none"}
}

// ChargeOrUnpriceable is the landed charge at this billed weight, or UnpriceableMinor when
// the container has no tariff or its tariff does not reach that weight.
func ChargeOrUnpriceable(container *domain.Container, billedTicks int64) int64 {
	table := container.RateTable()
	if table == nil {
		return UnpriceableMinor
	}
	if charge, ok := table.ChargeMinorOrNone(Grams(billedTicks)); ok {
		return charge
	}
	return UnpriceableMinor
}

// Grams is a billed weight in whole grams, rounded up: a shipment fractionally over a bracket
// is in the next bracket, as on a carrier's scale.
func Grams(weightTicks int64) int64 {
	return ints.AddExact(weightTicks, units.TicksPerG-1) / units.TicksPerG
}

// Unpriceable is a container whose billed weight lies above its tariff's last bracket.
type Unpriceable struct {
	ContainerID  string
	BilledGrams  int64
	LastBracketG int64
}

// UnpriceableContainer is the first container of a finished landed-cost answer its tariff
// cannot price, if any.
func UnpriceableContainer(containers []*domain.PackedContainer, cfg *config.PackingConfig) *Unpriceable {
	if cfg.Objective() != LandedCostObjective || cfg.DimensionalWeightDivisor() == nil {
		return nil
	}
	scorer := LandedCostFromConfig(cfg)
	for _, packed := range containers {
		grams := Grams(scorer.billed(packed))
		table := packed.Container().RateTable()
		if table == nil {
			return &Unpriceable{packed.Container().ID(), grams, 0}
		}
		if _, ok := table.ChargeMinorOrNone(grams); !ok {
			brackets := table.WeightBracketsG()
			return &Unpriceable{packed.Container().ID(), grams, brackets[len(brackets)-1]}
		}
	}
	return nil
}

// objectiveNames are the registered objectives in registration order.
var objectiveNames = []string{"default", "lowest_cost", "shipping_cost", LandedCostObjective,
	"open_dimension_height", "maximum_value"}

// ResolveObjective is the scorer a configuration's objective names; each factory reads its
// own inputs from the configuration.
func ResolveObjective(name string, cfg *config.PackingConfig) SolutionScorer {
	switch name {
	case "default":
		return DefaultSolutionScorer{}
	case "lowest_cost":
		return LowestCostSolutionScorer{}
	case "shipping_cost":
		return ShippingCostFromConfig(cfg)
	case LandedCostObjective:
		return LandedCostFromConfig(cfg)
	case "open_dimension_height":
		return OpenDimensionSolutionScorer{}
	case "maximum_value":
		return MaximumValueSolutionScorer{}
	}
	refuse(&UnknownObjectiveError{"unknown objective " + pythonRepr(name) + "; expected one of " +
		pythonList(sortedStrings(objectiveNames))})
	return nil
}
