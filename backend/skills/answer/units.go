package answer

import (
	"strconv"
	"strings"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

// unit is a unit the model may name in a conversion.
type unit struct {
	dimension string
	factor    float64 // to the dimension's base unit (temperatures: see convert)
	singular  string
	plural    string
}

var units = map[string]unit{
	"mm": {"length", 0.001, "millimeter", "millimeters"}, "cm": {"length", 0.01, "centimeter", "centimeters"},
	"m": {"length", 1, "meter", "meters"}, "km": {"length", 1000, "kilometer", "kilometers"},
	"in": {"length", 0.0254, "inch", "inches"}, "ft": {"length", 0.3048, "foot", "feet"},
	"yd": {"length", 0.9144, "yard", "yards"}, "mi": {"length", 1609.344, "mile", "miles"},
	"g": {"mass", 1, "gram", "grams"}, "kg": {"mass", 1000, "kilogram", "kilograms"},
	"oz": {"mass", 28.349523125, "ounce", "ounces"}, "lb": {"mass", 453.59237, "pound", "pounds"},
	"ml": {"volume", 0.001, "milliliter", "milliliters"}, "l": {"volume", 1, "liter", "liters"},
	"cup": {"volume", 0.2365882365, "cup", "cups"}, "tsp": {"volume", 0.00492892159375, "teaspoon", "teaspoons"},
	"tbsp": {"volume", 0.01478676478125, "tablespoon", "tablespoons"},
	"floz": {"volume", 0.0295735295625, "fluid ounce", "fluid ounces"}, "gal": {"volume", 3.785411784, "gallon", "gallons"},
	"c": {"temperature", 0, "degree Celsius", "degrees Celsius"}, "f": {"temperature", 0, "degree Fahrenheit", "degrees Fahrenheit"},
	"k":   {"temperature", 0, "kelvin", "kelvin"},
	"kmh": {"speed", 1 / 3.6, "kilometer per hour", "kilometers per hour"}, "mph": {"speed", 0.44704, "mile per hour", "miles per hour"},
	"s": {"time", 1, "second", "seconds"}, "min": {"time", 60, "minute", "minutes"}, "h": {"time", 3600, "hour", "hours"},
	"day": {"time", 86400, "day", "days"}, "week": {"time", 604800, "week", "weeks"},
}

// unitSymbols lists the symbols for the model, in a stable order.
var unitSymbols = []string{"mm", "cm", "m", "km", "in", "ft", "yd", "mi", "g", "kg", "oz", "lb", "ml", "l", "cup", "tsp",
	"tbsp", "floz", "gal", "c", "f", "k", "kmh", "mph", "s", "min", "h", "day", "week"}

// convert converts value between units of the same dimension.
func convert(value float64, from, to string) (float64, error) {
	f, ok1 := units[from]
	t, ok2 := units[to]
	if !ok1 || !ok2 || f.dimension != t.dimension {
		return 0, errors.WithMessagef(fault.ErrInvalidValue, "can't convert %s to %s", from, to)
	}
	if f.dimension != "temperature" {
		return value * f.factor / t.factor, nil
	}
	kelvin := value
	switch from {
	case "c":
		kelvin = value + 273.15
	case "f":
		kelvin = (value-32)*5/9 + 273.15
	}
	switch to {
	case "c":
		return kelvin - 273.15, nil
	case "f":
		return (kelvin-273.15)*9/5 + 32, nil
	}
	return kelvin, nil
}

// unitName says an amount of a unit: "1 pound", "453.59 grams".
func unitName(v float64, symbol string) string {
	u := units[symbol]
	if v == 1 {
		return "1 " + u.singular
	}
	return formatNumber(v) + " " + u.plural
}

// unitWords maps spoken unit names to symbols, for checking which unit the
// amount in the question is in.
var unitWords = map[string]string{
	"millimeter": "mm", "millimeters": "mm", "millimetre": "mm", "millimetres": "mm",
	"centimeter": "cm", "centimeters": "cm", "centimetre": "cm", "centimetres": "cm",
	"meter": "m", "meters": "m", "metre": "m", "metres": "m",
	"kilometer": "km", "kilometers": "km", "kilometre": "km", "kilometres": "km",
	"inch": "in", "inches": "in", "foot": "ft", "feet": "ft", "yard": "yd", "yards": "yd", "mile": "mi", "miles": "mi",
	"gram": "g", "grams": "g", "kilogram": "kg", "kilograms": "kg", "kilo": "kg", "kilos": "kg",
	"ounce": "oz", "ounces": "oz", "pound": "lb", "pounds": "lb",
	"milliliter": "ml", "milliliters": "ml", "liter": "l", "liters": "l", "litre": "l", "litres": "l",
	"cup": "cup", "cups": "cup", "teaspoon": "tsp", "teaspoons": "tsp", "tablespoon": "tbsp", "tablespoons": "tbsp",
	"gallon": "gal", "gallons": "gal",
	"celsius": "c", "centigrade": "c", "fahrenheit": "f", "kelvin": "k",
	"second": "s", "seconds": "s", "minute": "min", "minutes": "min", "hour": "h", "hours": "h",
	"day": "day", "days": "day", "week": "week", "weeks": "week",
}

// unitAfterValue finds the unit the question gives the amount in: the
// first unit word after the number (or after "a"/"an"/"one" for 1), e.g.
// "celsius" in "30 degrees celsius in fahrenheit". Speeds ("kilometers per
// hour") come out as their first word's unit; callers only use the result
// to check the direction. "" if not found.
func unitAfterValue(question string, value float64) string {
	tokens := strings.Fields(question)
	for i, t := range tokens {
		isValue := strings.ReplaceAll(t, ",", "") == strconv.FormatFloat(value, 'f', -1, 64)
		if value == 1 && (t == "a" || t == "an" || t == "one") {
			isValue = true
		}
		if !isValue {
			continue
		}
		for _, next := range tokens[i+1 : min(len(tokens), i+4)] {
			if symbol, ok := unitWords[next]; ok {
				return symbol
			}
		}
	}
	return ""
}
