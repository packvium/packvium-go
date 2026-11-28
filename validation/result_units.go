package validation

import (
	"math/big"
	"sort"
	"strings"
	"unicode"
)

// Measures to exact ticks, re-derived from the unit tables rather than taken from the engine:
// a validator built on the engine's unit parser could not catch a bug in it.

type unitTable struct {
	// names are the units in the reference's order, which breaks a tie between two suffixes
	// of one length.
	names []string
	ticks map[string]int64
}

func newUnitTable(names []string, ticks []int64) unitTable {
	table := unitTable{names: names, ticks: map[string]int64{}}
	for index, name := range names {
		table.ticks[name] = ticks[index]
	}
	return table
}

var (
	// lengthTicks: one tick is 1/16000 mm, so an inch is exactly 406,400 ticks.
	lengthTicks = newUnitTable([]string{"mm", "cm", "m", "in", "inch", "inches", "ft", "tick", "ticks"},
		[]int64{16_000, 160_000, 16_000_000, 406_400, 406_400, 406_400, 4_876_800, 1, 1})
	// weightTicks: one tick is 1/8 microgram.
	weightTicks = newUnitTable([]string{"mg", "g", "kg", "oz", "lb", "lbs", "tick", "ticks"},
		[]int64{8_000, 8_000_000, 8_000_000_000, 226_796_185, 3_628_738_960, 3_628_738_960, 1, 1})
)

const (
	ticksPerGram = 8_000_000
	ticksPerKG   = 8_000_000_000
)

// perUnit is table[unit], an exact key.
func (t unitTable) perUnit(unit string) *big.Int {
	ticks, present := t.ticks[unit]
	if !present {
		panic(shapeRefusal("unknown unit " + pyRepr(unit)))
	}
	return bi(ticks)
}

// bySuffixLength is the unit names longest first, ties in the table's order, as the
// reference's stable sort over the table gives them.
func (t unitTable) bySuffixLength() []string {
	names := append([]string(nil), t.names...)
	sort.SliceStable(names, func(left, right int) bool { return len(names[left]) > len(names[right]) })
	return names
}

// toTicks converts a measure (a number, a string with an optional unit suffix, or
// {value, unit}) to exact ticks, rounding halves to even.
func toTicks(measure interface{}, defaultUnit string, table unitTable) *big.Int {
	unit := defaultUnit
	raw := measure
	switch typed := measure.(type) {
	case *jsonObject:
		raw = member(typed, "value")
		unit = text(get(typed, "unit", defaultUnit), "a measure's unit")
	case string:
		stripped := stripSpace(typed)
		lowered := strings.ToLower(stripped)
		for _, suffix := range table.bySuffixLength() {
			if strings.HasSuffix(lowered, suffix) && len(stripped) >= len(suffix) {
				number := stripSpace(stripped[:len(stripped)-len(suffix)])
				if number != "" {
					raw, unit = number, suffix
					break
				}
			}
		}
	}
	scaled := new(big.Rat).Mul(parseRational(raw), ratOf(table.perUnit(strings.ToLower(unit))))
	return roundHalfEven(scaled)
}

func roundHalfEven(value *big.Rat) *big.Int {
	floor := ratFloor(value)
	remainder := new(big.Rat).Sub(value, ratOf(floor))
	switch remainder.Cmp(big.NewRat(1, 2)) {
	case 1:
		return add(floor, bi(1))
	case -1:
		return floor
	}
	if mod(floor, bi(2)).Sign() == 0 {
		return floor
	}
	return add(floor, bi(1))
}

// parseRational is an integer exactly, anything else through its str() as a fraction, and a
// mixed fraction ("12 3/8") when the text holds a space.
func parseRational(value interface{}) *big.Rat {
	if number := intInstance(value); number != nil {
		return ratOf(number)
	}
	stripped := stripSpace(pyStr(value))
	if !strings.Contains(stripped, " ") {
		return parseFraction(stripped)
	}
	whole, rest := splitOnce(stripped)
	part := parseFraction(rest)
	if strings.HasPrefix(whole, "-") {
		part = new(big.Rat).Neg(part)
	}
	return new(big.Rat).Add(parseFraction(whole), part)
}

// splitOnce is text.split(None, 1) of text already stripped: the first run of whitespace
// separates the two parts.
func splitOnce(text string) (string, string) {
	at := strings.IndexFunc(text, isSpace)
	rest := strings.TrimLeftFunc(text[at:], isSpace)
	return text[:at], rest
}

// ratioOf is Fraction(str(value)), the reference's reading of a ratio field.
func ratioOf(value interface{}) *big.Rat { return parseFraction(pyStr(value)) }

// parseFraction is Fraction(text) by the reference's grammar: optional whitespace and sign,
// then n/d, or a decimal with an optional exponent; digit groups may be split by single
// underscores. Only ASCII digits are read.
func parseFraction(literal string) *big.Rat {
	refuse := func() { panic(shapeRefusal("Invalid literal for Fraction: " + pyRepr(literal))) }
	rest := stripSpace(literal)
	negative := false
	if strings.HasPrefix(rest, "+") || strings.HasPrefix(rest, "-") {
		negative, rest = rest[0] == '-', rest[1:]
	}
	if !(startsWithDigit(rest) || (strings.HasPrefix(rest, ".") && startsWithDigit(rest[1:]))) {
		refuse()
	}
	digits, rest := digitGroup(rest)
	numerator, denominator := groupValue(digits), bi(1)
	if strings.HasPrefix(rest, "/") {
		digits, rest = digitGroup(rest[1:])
		if digits == "" || rest != "" {
			refuse()
		}
		denominator = groupValue(digits)
	} else {
		if strings.HasPrefix(rest, ".") {
			digits, rest = digitGroup(rest[1:])
			if digits != "" {
				clean := strings.Replace(digits, "_", "", -1)
				scale := new(big.Int).Exp(bi(10), bi(int64(len(clean))), nil)
				numerator, denominator = add(mul(numerator, scale), groupValue(clean)), scale
			}
		}
		if strings.HasPrefix(rest, "e") || strings.HasPrefix(rest, "E") {
			numerator, denominator, rest = applyExponent(rest[1:], numerator, denominator, refuse)
		}
		if rest != "" {
			refuse()
		}
	}
	value := fraction(numerator, denominator)
	if negative {
		value.Neg(value)
	}
	return value
}

// applyExponent scales by the exponent at the head of rest and returns what follows it.
func applyExponent(rest string, numerator, denominator *big.Int, refuse func()) (*big.Int, *big.Int, string) {
	negative := false
	if strings.HasPrefix(rest, "+") || strings.HasPrefix(rest, "-") {
		negative, rest = rest[0] == '-', rest[1:]
	}
	digits, rest := digitGroup(rest)
	if digits == "" {
		refuse()
	}
	power := groupValue(digits)
	if power.Cmp(bi(400)) > 0 {
		panic(shapeRefusal("arithmetic overflow: an exponent beyond what this validator computes"))
	}
	factor := new(big.Int).Exp(bi(10), power, nil)
	if negative {
		return numerator, mul(denominator, factor), rest
	}
	return mul(numerator, factor), denominator, rest
}

func startsWithDigit(text string) bool { return text != "" && text[0] >= '0' && text[0] <= '9' }

// digitGroup splits off a leading run of digits with single underscores between them.
func digitGroup(text string) (string, string) {
	end := 0
	for end < len(text) {
		isDigit := text[end] >= '0' && text[end] <= '9'
		joins := text[end] == '_' && end > 0 && text[end-1] != '_' && end+1 < len(text) &&
			text[end+1] >= '0' && text[end+1] <= '9'
		if !isDigit && !joins {
			break
		}
		end++
	}
	return text[:end], text[end:]
}

// groupValue is a digit group's value, zero for an empty one.
func groupValue(digits string) *big.Int {
	if digits == "" {
		return bi(0)
	}
	value, _ := new(big.Int).SetString(strings.Replace(digits, "_", "", -1), 10)
	return value
}

// isSpace is the reference's str.isspace for one character.
func isSpace(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }

// stripSpace is the reference's str.strip(): whitespace from both ends.
func stripSpace(text string) string { return strings.TrimFunc(text, isSpace) }

func dimensionsOf(value interface{}, unit string) [3]*big.Int {
	return [3]*big.Int{
		toTicks(member(value, "length"), unit, lengthTicks),
		toTicks(member(value, "width"), unit, lengthTicks),
		toTicks(member(value, "height"), unit, lengthTicks),
	}
}
