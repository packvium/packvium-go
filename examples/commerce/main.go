// Commerce: quote a shipment, apply a policy rule, and inspect a catalog version.
//
// Run it from packvium-go/:
//
//	go run ./examples/commerce
//
// Everything the three functions need arrives in one commerce document: the tariffs you
// publish, the eligibility rules you publish, and the catalog versions you publish. Each
// history is a list, and a version's number is simply its position in that list starting
// at 1 -- so `tariff_version: 2` always means "the second entry under this carrier and
// service", with no separate numbering to keep in sync.
//
// Nothing here reads the clock. Every "which version applies" question is answered from a
// pin you supply, so the same call replays to the same answer next year.
package main

import (
	"fmt"
	"log"

	"github.com/packvium/packvium-go/commerce"
)

type object = map[string]interface{}
type list = []interface{}

// document holds one document, three histories. You would normally load this from your
// own storage.
var document = object{
	"tariffs": list{
		object{
			"carrier_id": "acme",
			"service_id": "ground",
			// Two published versions. The second takes effect at instant 1000.
			"versions": list{
				object{
					"effective_at": 0,
					// Volume in mm^3 divided by this gives dimensional weight in grams.
					"dimensional_weight_divisor": 5000,
					// Minor currency units (cents) per billed kilogram, per zone.
					"cost_per_dimensional_kg_minor": object{"zone-a": 450, "zone-b": 610},
					"minimum_charge_minor":          900,
					// Permille: 120 means 12.0%.
					"fuel_surcharge_permille": 120,
					"accessorials": list{
						object{"accessorial_id": "liftgate", "flat_charge_minor": 250},
						object{"accessorial_id": "residential", "permille_of_base": 75},
					},
				},
				object{
					"effective_at":                  1000,
					"dimensional_weight_divisor":    4000,
					"cost_per_dimensional_kg_minor": object{"zone-a": 480},
					"minimum_charge_minor":          950,
					"fuel_surcharge_permille":       140,
					"accessorials":                  list{object{"accessorial_id": "liftgate", "flat_charge_minor": 275}},
				},
			},
		},
	},
	"policy_rules": list{
		object{
			"rule_id": "no-hazmat-air",
			"versions": list{
				object{
					"scope":        "hazmat",
					"action":       "reject",
					"priority":     10,
					"effective_at": 0,
					"reason":       "class 1.4 is not accepted on air services",
					"predicates": list{
						object{"scope": "hazmat", "field": "un_class", "operator": "equals", "value": "1.4"},
					},
				},
			},
		},
	},
	"catalogs": list{
		object{
			"catalog_id": "dc-12",
			"versions": list{
				object{
					"effective_at": 0, "published_at": 0, "note": "initial",
					"snapshot": object{
						"items": list{object{"id": "sku-1", "dimensions_mm": list{100, 200, 300}, "weight_g": 1200}},
						"cartons": list{object{"id": "box-m", "inner_dimensions_mm": list{320, 240, 180},
							"max_payload_g": 15000, "cost_minor": 85}},
					},
				},
				// A rollback is a new, higher-numbered version, never an edit of history.
				object{"rollback_to": 1, "published_at": 900, "effective_at": 900,
					"note": "revert the weight correction"},
			},
		},
	},
}

func main() {
	// ------------------------------------------------------------------------------------
	// 1. Quote: what does this shipment cost?
	// ------------------------------------------------------------------------------------
	pinned := answer(commerce.Quote(document, object{
		"carrier_id":             "acme",
		"service_id":             "ground",
		"tariff_version":         1, // replay against exactly this version...
		"zone":                   "zone-a",
		"actual_weight_g":        1200,
		"volume_mm3":             6000000,
		"requested_accessorials": list{"liftgate"},
	}))
	show("a quote pinned to tariff version 1", pinned)
	fmt.Printf("   -> the caller pays %v minor units\n", member(pinned, "quote", "total_minor"))

	effective := answer(commerce.Quote(document, object{
		"carrier_id":             "acme",
		"service_id":             "ground",
		"as_of":                  1500, // ...or against whatever was in force at this instant
		"zone":                   "zone-a",
		"actual_weight_g":        1200,
		"volume_mm3":             6000000,
		"requested_accessorials": list{"liftgate"},
	}))
	fmt.Printf("\n   as of instant 1500 the tariff is version %v, and the price is %v\n",
		member(effective, "quote", "tariff_version"), member(effective, "quote", "total_minor"))

	// A request the model cannot answer is not an error. It is a result with a status, a
	// code from a closed set, and the structured fields that say what was missing.
	unpriceable := answer(commerce.Quote(document, object{
		"carrier_id": "acme", "service_id": "ground", "tariff_version": 1,
		"zone": "zone-nowhere", "actual_weight_g": 1200, "volume_mm3": 6000000,
	}))
	show("a zone this tariff does not price", unpriceable)

	// ------------------------------------------------------------------------------------
	// 2. Policy: may this shipment go at all?
	// ------------------------------------------------------------------------------------
	decision := answer(commerce.EvaluatePolicy(document, object{
		"scope":   "hazmat",
		"context": object{"un_class": "1.4"},
		"as_of":   0,
	}))
	show("a policy decision, with the rule that made it", decision)

	allowed := answer(commerce.EvaluatePolicy(document, object{
		"scope": "hazmat", "context": object{"un_class": "9"}, "as_of": 0,
	}))
	fmt.Printf("\n   nothing matched, so the shipment is allowed with no citation: %s\n",
		spell(member(allowed, "decision", "citation")))

	// ------------------------------------------------------------------------------------
	// 3. Catalog: which master data was this decision made against?
	// ------------------------------------------------------------------------------------
	catalog := answer(commerce.CatalogVersionInfo(document, object{
		"catalog_id":  "dc-12",
		"version":     2,
		"resolved_at": 1700,
	}))
	show("catalog version metadata", catalog)
	fmt.Printf("\n   version %v is a rollback of version %v\n",
		member(catalog, "catalog", "version"), member(catalog, "catalog", "rolled_back_from"))

	// ------------------------------------------------------------------------------------
	// Storing or comparing a result: use the canonical form, never fmt's %v.
	// ------------------------------------------------------------------------------------
	fmt.Println("\n== the canonical form is what you store, log and compare")
	canonical, err := commerce.CanonicalJSON(pinned)
	check(err)
	fmt.Println(canonical)
}

func show(title string, result commerce.Result) {
	fmt.Printf("\n== %s\n", title)
	indented, err := commerce.IndentedJSON(result)
	check(err)
	fmt.Println(indented)
}

// answer stops the example on an input error; a rejection is a result, not an error.
func answer(result commerce.Result, err error) commerce.Result {
	check(err)
	return result
}

// member walks nested result objects by key.
func member(result commerce.Result, path ...string) interface{} {
	var value interface{} = result
	for _, key := range path {
		value, _ = value.(*commerce.Object).Get(key)
	}
	return value
}

// spell prints an absent citation the way Go names it.
func spell(value interface{}) string {
	if value == nil {
		return "nil"
	}
	return fmt.Sprint(value)
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
