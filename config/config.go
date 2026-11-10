// Package config holds the immutable solver configuration of one packing call.
package config

import (
	"errors"
	"fmt"

	"github.com/packvium/packvium-go/units"
)

// SolverProfile chooses how hard the portfolio searches.
type SolverProfile string

const (
	Fast       SolverProfile = "fast"
	Balanced   SolverProfile = "balanced"
	Quality    SolverProfile = "quality"
	ExactSmall SolverProfile = "exact_small"
)

// ParseSolverProfile reads a profile's wire name.
func ParseSolverProfile(name string) (SolverProfile, error) {
	switch profile := SolverProfile(name); profile {
	case Fast, Balanced, Quality, ExactSmall:
		return profile, nil
	}
	return "", fmt.Errorf("'%s' is not a valid SolverProfile", name)
}

// EffortBudget is counted-work limits, checked alongside (never instead of) the wall clock,
// so a caller who needs bit-identical results across hosts can buy them. MaxRestarts bounds
// the portfolio; the other three are checked against one solver call's own counters. A nil
// field is no limit.
type EffortBudget struct {
	MaxCandidatesEvaluated *int64
	MaxPlacementAttempts   *int64
	MaxSearchNodes         *int64
	MaxRestarts            *int64
}

// Exceeded reports whether one solver call's counters have reached any limit.
func (b *EffortBudget) Exceeded(candidatesEvaluated, placementsAttempted, searchNodesExpanded int64) bool {
	if b == nil {
		return false
	}
	return reached(b.MaxCandidatesEvaluated, candidatesEvaluated) || reached(b.MaxPlacementAttempts, placementsAttempted) ||
		reached(b.MaxSearchNodes, searchNodesExpanded)
}

func reached(limit *int64, count int64) bool { return limit != nil && count >= *limit }

// Options is the caller's description of a configuration; Default fills in the reference
// defaults for a profile and New validates and freezes it.
type Options struct {
	Profile                     SolverProfile
	TimeLimitMS                 int64
	TopK                        int64
	Seed                        int64
	MaxContainers               *int64
	Clearance                   units.Length
	MinimumSupportRatio         float64
	ExactItemLimit              int64
	MultiStartOrders            int64
	ValidateResult              bool
	MaxCandidatesPerItem        int64
	MaxCandidatePoints          int64
	Solvers                     []string
	Objective                   string
	EffortBudget                *EffortBudget
	DimensionalWeightDivisor    *int64
	DimensionalWeightLengthUnit string
	DimensionalWeightWeightUnit string
	RequirePlacementCoordinates bool
	ContainerPlanBeamWidth      int64
	ContainerPlanNodeLimit      int64
}

// Default is the reference engine's default options for a profile.
func Default(profile SolverProfile) Options {
	options := Options{
		Profile: profile, TimeLimitMS: 1000, TopK: 3, Seed: 42, ExactItemLimit: 7, MultiStartOrders: 8,
		ValidateResult: true, MaxCandidatesPerItem: 1, MaxCandidatePoints: 4096, Objective: "default",
		DimensionalWeightLengthUnit: "in", DimensionalWeightWeightUnit: "lb", RequirePlacementCoordinates: true,
		ContainerPlanBeamWidth: 1, ContainerPlanNodeLimit: 1,
	}
	switch profile {
	case Fast:
		options.TimeLimitMS, options.TopK, options.MultiStartOrders = 200, 1, 1
	case Quality:
		options.TimeLimitMS, options.TopK, options.MultiStartOrders = 5000, 5, 24
		options.MaxCandidatesPerItem, options.ContainerPlanBeamWidth, options.ContainerPlanNodeLimit = 16, 16, 100_000
	case ExactSmall:
		options.TimeLimitMS = 10_000
	}
	return options
}

// PackingConfig is a validated, immutable configuration.
type PackingConfig struct{ options Options }

// New validates options in the reference order.
func New(options Options) (*PackingConfig, error) {
	switch {
	case options.TimeLimitMS <= 0 || options.TopK <= 0 || options.ExactItemLimit <= 0 || options.MultiStartOrders <= 0 ||
		options.MaxCandidatesPerItem <= 0 || options.ContainerPlanBeamWidth <= 0 || options.ContainerPlanNodeLimit <= 0:
		return nil, errors.New("positive configuration values required")
	case options.MaxCandidatePoints < 16:
		return nil, errors.New("max_candidate_points must be at least 16")
	case !(options.MinimumSupportRatio >= 0 && options.MinimumSupportRatio <= 1):
		return nil, errors.New("minimum_support_ratio must be between 0 and 1")
	case options.DimensionalWeightDivisor != nil && *options.DimensionalWeightDivisor <= 0:
		return nil, errors.New("dimensional_weight_divisor must be positive")
	}
	frozen := options
	frozen.Solvers = append([]string(nil), options.Solvers...)
	if options.EffortBudget != nil {
		budget := *options.EffortBudget
		frozen.EffortBudget = &budget
	}
	return &PackingConfig{frozen}, nil
}

// Options returns a copy of the frozen options.
func (c *PackingConfig) Options() Options {
	options := c.options
	options.Solvers = append([]string(nil), c.options.Solvers...)
	return options
}

func (c *PackingConfig) Profile() SolverProfile           { return c.options.Profile }
func (c *PackingConfig) TimeLimitMS() int64               { return c.options.TimeLimitMS }
func (c *PackingConfig) TopK() int64                      { return c.options.TopK }
func (c *PackingConfig) Seed() int64                      { return c.options.Seed }
func (c *PackingConfig) MaxContainers() *int64            { return c.options.MaxContainers }
func (c *PackingConfig) Clearance() units.Length          { return c.options.Clearance }
func (c *PackingConfig) MinimumSupportRatio() float64     { return c.options.MinimumSupportRatio }
func (c *PackingConfig) ExactItemLimit() int64            { return c.options.ExactItemLimit }
func (c *PackingConfig) MultiStartOrders() int64          { return c.options.MultiStartOrders }
func (c *PackingConfig) ValidateResult() bool             { return c.options.ValidateResult }
func (c *PackingConfig) MaxCandidatesPerItem() int64      { return c.options.MaxCandidatesPerItem }
func (c *PackingConfig) MaxCandidatePoints() int64        { return c.options.MaxCandidatePoints }
func (c *PackingConfig) Solvers() []string                { return append([]string(nil), c.options.Solvers...) }
func (c *PackingConfig) Objective() string                { return c.options.Objective }
func (c *PackingConfig) EffortBudget() *EffortBudget      { return c.options.EffortBudget }
func (c *PackingConfig) DimensionalWeightDivisor() *int64 { return c.options.DimensionalWeightDivisor }
func (c *PackingConfig) DimensionalWeightLengthUnit() string {
	return c.options.DimensionalWeightLengthUnit
}
func (c *PackingConfig) DimensionalWeightWeightUnit() string {
	return c.options.DimensionalWeightWeightUnit
}
func (c *PackingConfig) RequirePlacementCoordinates() bool {
	return c.options.RequirePlacementCoordinates
}
func (c *PackingConfig) ContainerPlanBeamWidth() int64 { return c.options.ContainerPlanBeamWidth }
func (c *PackingConfig) ContainerPlanNodeLimit() int64 { return c.options.ContainerPlanNodeLimit }
