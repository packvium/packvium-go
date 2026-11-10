package serialization

import (
	"strconv"

	"github.com/toxakara/packvium-go/constraint"
	"github.com/toxakara/packvium-go/domain"
	"github.com/toxakara/packvium-go/result"
	"github.com/toxakara/packvium-go/units"
)

// EncodeResult is the wire form of a packing result in the given output units. Key order is
// not part of the contract: every writer of this map sorts keys.
func EncodeResult(packed *result.PackingResult, lengthUnit, weightUnit string) map[string]interface{} {
	return encode(packed, lengthUnit, weightUnit, true)
}

func encode(packed *result.PackingResult, lengthUnit, weightUnit string, withAlternatives bool) map[string]interface{} {
	containers := make([]interface{}, len(packed.Containers))
	for index, container := range packed.Containers {
		containers[index] = encodeContainer(container, lengthUnit, weightUnit)
	}
	unpacked := make([]interface{}, len(packed.Unpacked))
	for index, item := range packed.Unpacked {
		unpacked[index] = map[string]interface{}{
			"item_id": item.Instance.ID(), "item_type": item.Instance.Item().ID(), "reason": item.Reason,
			"details": stringList(item.Details), "proof": item.Proof.ToMap(),
		}
	}
	alternatives := []interface{}{}
	if withAlternatives {
		for _, alternative := range packed.Alternatives {
			alternatives = append(alternatives, encode(alternative, lengthUnit, weightUnit, false))
		}
	}
	score := make([]interface{}, len(packed.Score))
	for index, component := range packed.Score {
		score[index] = component
	}
	report := packed.Algorithm
	return map[string]interface{}{
		"status": string(packed.Status), "feasibility": packed.Feasibility.ToMap(),
		"termination": packed.Termination.ToMap(), "optimality": packed.Optimality.ToMap(),
		"complete": packed.Complete(), "objective": packed.Objective,
		"algorithm": map[string]interface{}{
			"profile": report.Profile, "solver": report.Solver, "duration_ms": report.DurationMS, "seed": report.Seed,
			"time_limit_reached": report.TimeLimitReached, "effort_limit_reached": report.EffortLimitReached,
			"candidates_evaluated": report.CandidatesEvaluated, "placements_attempted": report.PlacementsAttempted,
			"metrics": report.Metrics.ToMap(),
		},
		"summary": map[string]interface{}{
			"container_count": int64(len(packed.Containers)), "packed_item_count": packed.PackedItemCount(),
			"unpacked_item_count": int64(len(packed.Unpacked)),
		},
		"score": score, "containers": containers, "unpacked_items": unpacked,
		"catalog_versions_used": []interface{}{}, "warnings": stringList(packed.Warnings), "alternatives": alternatives,
	}
}

func encodeContainer(packed *domain.PackedContainer, lengthUnit, weightUnit string) map[string]interface{} {
	container := packed.Container()
	placements := make([]interface{}, 0, len(packed.Placements()))
	for _, placement := range packed.Placements() {
		placements = append(placements, map[string]interface{}{
			"item_id": placement.Instance().ID(), "item_type": placement.Instance().Item().ID(),
			"position": placement.Position().ToMap(lengthUnit), "dimensions": placement.Dimensions().ToMap(lengthUnit),
			"orientation": string(placement.Rotation()), "support_ratio": fixed6(placement.SupportRatio()),
			"top_load": placement.TopLoad().ToMap(weightUnit),
		})
	}
	out := map[string]interface{}{
		"id": packed.ID(), "container_type": container.ID(),
		"inner_dimensions": container.InnerDimensions().ToMap(lengthUnit),
		"outer_dimensions": container.EffectiveOuterDimensions().ToMap(lengthUnit),
		"payload_weight":   packed.PayloadWeight().ToMap(weightUnit), "gross_weight": packed.GrossWeight().ToMap(weightUnit),
		"used_volume_ticks3": packed.UsedVolume().String(), "volume_utilization": fixed6(packed.Utilization()),
		"centre_of_mass_offset_ppm": packed.CentreOfMassOffsetPPM(),
		"void_fill_reserve_ticks3":  constraint.ReservedVolume(container).String(),
		"placements":                placements,
	}
	if summary := packed.LatticeSummary(); summary != nil {
		out["lattice_summary"] = map[string]interface{}{
			"item_type": summary.ItemType, "orientation": string(summary.Rotation),
			"physical_dimensions": summary.Physical.ToMap(lengthUnit), "envelope_dimensions": summary.Envelope.ToMap(lengthUnit),
			"nx": summary.NX, "ny": summary.NY, "layers_used": summary.LayersUsed(),
			"layer_step": units.MustLength(summary.LayerStep).ToMap(lengthUnit), "count": summary.Count,
		}
	}
	if axles := container.Axles(); axles != nil {
		reaction := constraint.Reactions(*axles, constraint.Units(packed.Placements()),
			container.TareWeight().Ticks(), container.InnerDimensions().Length().Ticks())
		// Decimal strings: the numerators are products of ticks and outgrow every engine's integers.
		out["axle_reactions"] = map[string]interface{}{
			"basis": "gross", "denominator": reaction.Denominator.String(),
			"front_numerator": reaction.FrontNumerator.String(), "rear_numerator": reaction.RearNumerator.String(),
		}
	}
	return out
}

// fixed6 is the reference's "%.6f": correctly rounded from the exact binary value.
func fixed6(value float64) string { return strconv.FormatFloat(value, 'f', 6, 64) }

func stringList(values []string) []interface{} {
	list := make([]interface{}, len(values))
	for index, value := range values {
		list[index] = value
	}
	return list
}
