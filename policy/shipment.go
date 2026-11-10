package policy

import "strings"

// shipmentFacts is the facts a rule may select on: properties of the shipment rather than of
// any item or container.
var shipmentFacts = []string{"facility", "customer", "carrier", "service"}

// ShipmentContext is declared facts about the shipment, each possibly absent. A fact nobody
// declared is not a wildcard: a rule naming it never participates, so a rule written for one
// customer cannot apply to a shipment that named no customer at all.
type ShipmentContext struct {
	facts [4]*string
}

// NewShipmentContext declares the given facts; nil leaves one undeclared.
func NewShipmentContext(facility, customer, carrier, service *string) ShipmentContext {
	return ShipmentContext{[4]*string{copyString(facility), copyString(customer), copyString(carrier), copyString(service)}}
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func (s ShipmentContext) Facility() *string { return copyString(s.facts[0]) }
func (s ShipmentContext) Customer() *string { return copyString(s.facts[1]) }
func (s ShipmentContext) Carrier() *string  { return copyString(s.facts[2]) }
func (s ShipmentContext) Service() *string  { return copyString(s.facts[3]) }

// ParseShipmentContext reads a shipment or applies_to object; nil declares nothing.
//
// Every fact is checked in the fixed order facility, customer, carrier, service: the decoded
// object does not keep its document order, so a block with two malformed facts names the
// first of these.
func ParseShipmentContext(value interface{}, where string) (ShipmentContext, error) {
	if value == nil {
		return ShipmentContext{}, nil
	}
	raw, ok := object(value)
	if !ok {
		return ShipmentContext{}, refuse("%s must be an object", where)
	}
	if unknown := sortedUnknown(raw, shipmentFacts); len(unknown) > 0 {
		return ShipmentContext{}, refuse("%s names unknown shipment facts: %s", where, strings.Join(unknown, ", "))
	}
	var context ShipmentContext
	for position, name := range shipmentFacts {
		fact, present := raw[name]
		if !present {
			continue
		}
		text, ok := nonEmptyString(fact)
		if !ok {
			return ShipmentContext{}, refuse("%s.%s must be a non-empty string", where, name)
		}
		context.facts[position] = &text
	}
	return context, nil
}

// SatisfiedBy reports whether every fact this selector names equals the shipment's own. O(1).
func (s ShipmentContext) SatisfiedBy(shipment ShipmentContext) bool {
	for position, selector := range s.facts {
		if selector == nil {
			continue
		}
		actual := shipment.facts[position]
		if actual == nil || *actual != *selector {
			return false
		}
	}
	return true
}
