package utils

import (
	"fmt"
	"strings"
	"time"

	"github.com/martinlindhe/unit"
)

// ConvertUnits converts between common units using github.com/martinlindhe/unit for temp, length, mass, and data (binary).
func ConvertUnits(value float64, fromUnit, toUnit string) (string, error) {
	fromUnit = strings.ToLower(strings.TrimSpace(fromUnit))
	toUnit = strings.ToLower(strings.TrimSpace(toUnit))

	if result, err := convertTemperature(value, fromUnit, toUnit); err == nil {
		return fmt.Sprintf("%.2f %s = %.2f %s", value, fromUnit, result, toUnit), nil
	}
	if result, err := convertLength(value, fromUnit, toUnit); err == nil {
		return fmt.Sprintf("%.4g %s = %.4g %s", value, fromUnit, result, toUnit), nil
	}
	if result, err := convertMass(value, fromUnit, toUnit); err == nil {
		return fmt.Sprintf("%.4g %s = %.4g %s", value, fromUnit, result, toUnit), nil
	}
	if result, err := convertDatasize(value, fromUnit, toUnit); err == nil {
		return fmt.Sprintf("%.4g %s = %.4g %s", value, fromUnit, result, toUnit), nil
	}
	return "", fmt.Errorf("unknown unit conversion: %s to %s", fromUnit, toUnit)
}

func convertTemperature(value float64, from, to string) (float64, error) {
	from = normTemp(from)
	to = normTemp(to)
	var t unit.Temperature
	switch from {
	case "c":
		t = unit.FromCelsius(value)
	case "f":
		t = unit.FromFahrenheit(value)
	case "k":
		t = unit.FromKelvin(value)
	default:
		return 0, fmt.Errorf("unknown temp unit: %s", from)
	}
	switch to {
	case "c":
		return t.Celsius(), nil
	case "f":
		return t.Fahrenheit(), nil
	case "k":
		return t.Kelvin(), nil
	default:
		return 0, fmt.Errorf("unknown temp unit: %s", to)
	}
}

func normTemp(s string) string {
	switch s {
	case "celsius":
		return "c"
	case "fahrenheit":
		return "f"
	case "kelvin":
		return "k"
	}
	return s
}

func convertLength(value float64, from, to string) (float64, error) {
	var L unit.Length
	switch from {
	case "m", "meter", "meters":
		L = unit.Length(value) * unit.Meter
	case "cm", "centimeter", "centimeters":
		L = unit.Length(value) * unit.Centimeter
	case "mm", "millimeter", "millimeters":
		L = unit.Length(value) * unit.Millimeter
	case "km", "kilometer", "kilometers":
		L = unit.Length(value) * unit.Kilometer
	case "in", "inch", "inches":
		L = unit.Length(value) * unit.Inch
	case "ft", "foot", "feet":
		L = unit.Length(value) * unit.Foot
	case "yd", "yard", "yards":
		L = unit.Length(value) * unit.Yard
	case "mi", "mile", "miles":
		L = unit.Length(value) * unit.Mile
	default:
		return 0, fmt.Errorf("unknown length unit: %s", from)
	}
	switch to {
	case "m", "meter", "meters":
		return L.Meters(), nil
	case "cm", "centimeter", "centimeters":
		return L.Centimeters(), nil
	case "mm", "millimeter", "millimeters":
		return L.Millimeters(), nil
	case "km", "kilometer", "kilometers":
		return L.Kilometers(), nil
	case "in", "inch", "inches":
		return L.Inches(), nil
	case "ft", "foot", "feet":
		return L.Feet(), nil
	case "yd", "yard", "yards":
		return L.Yards(), nil
	case "mi", "mile", "miles":
		return L.Miles(), nil
	default:
		return 0, fmt.Errorf("unknown length unit: %s", to)
	}
}

func convertMass(value float64, from, to string) (float64, error) {
	var M unit.Mass
	switch from {
	case "g", "gram", "grams":
		M = unit.Mass(value) * unit.Gram
	case "kg", "kilogram", "kilograms":
		M = unit.Mass(value) * unit.Kilogram
	case "mg", "milligram", "milligrams":
		M = unit.Mass(value) * unit.Milligram
	case "lb", "pound", "pounds":
		M = unit.Mass(value) * unit.AvoirdupoisPound
	case "oz", "ounce", "ounces":
		M = unit.Mass(value) * unit.AvoirdupoisOunce
	default:
		return 0, fmt.Errorf("unknown mass unit: %s", from)
	}
	switch to {
	case "g", "gram", "grams":
		return M.Grams(), nil
	case "kg", "kilogram", "kilograms":
		return M.Kilograms(), nil
	case "mg", "milligram", "milligrams":
		return M.Milligrams(), nil
	case "lb", "pound", "pounds":
		return M.AvoirdupoisPounds(), nil
	case "oz", "ounce", "ounces":
		return M.AvoirdupoisOunces(), nil
	default:
		return 0, fmt.Errorf("unknown mass unit: %s", to)
	}
}

func convertDatasize(value float64, from, to string) (float64, error) {
	var D unit.Datasize
	switch from {
	case "b", "byte", "bytes":
		D = unit.Datasize(value) * unit.Byte
	case "kb", "kilobyte", "kilobytes":
		D = unit.Datasize(value) * unit.Kibibyte
	case "mb", "megabyte", "megabytes":
		D = unit.Datasize(value) * unit.Mebibyte
	case "gb", "gigabyte", "gigabytes":
		D = unit.Datasize(value) * unit.Gibibyte
	case "tb", "terabyte", "terabytes":
		D = unit.Datasize(value) * unit.Tebibyte
	default:
		return 0, fmt.Errorf("unknown data unit: %s", from)
	}
	switch to {
	case "b", "byte", "bytes":
		return D.Bytes(), nil
	case "kb", "kilobyte", "kilobytes":
		return D.Kibibytes(), nil
	case "mb", "megabyte", "megabytes":
		return D.Mebibytes(), nil
	case "gb", "gigabyte", "gigabytes":
		return D.Gibibytes(), nil
	case "tb", "terabyte", "terabytes":
		return D.Tebibytes(), nil
	default:
		return 0, fmt.Errorf("unknown data unit: %s", to)
	}
}

// ConvertTemperature converts temperature between C, F, K.
func ConvertTemperature(value float64, from, to string) (float64, error) {
	return convertTemperature(value, from, to)
}

var timezoneAliases = map[string]string{
	"pst": "America/Los_Angeles", "pt": "America/Los_Angeles", "pacific": "America/Los_Angeles",
	"pdt": "America/Los_Angeles", "los angeles": "America/Los_Angeles", "la": "America/Los_Angeles",
	"mst": "America/Denver", "mt": "America/Denver", "mountain": "America/Denver",
	"mdt": "America/Denver", "denver": "America/Denver",
	"cst": "America/Chicago", "ct": "America/Chicago", "central": "America/Chicago",
	"cdt": "America/Chicago", "chicago": "America/Chicago",
	"est": "America/New_York", "et": "America/New_York", "eastern": "America/New_York",
	"edt": "America/New_York", "new york": "America/New_York", "nyc": "America/New_York",
	"utc": "UTC", "gmt": "UTC", "z": "UTC",
	"jst": "Asia/Tokyo", "tokyo": "Asia/Tokyo", "japan": "Asia/Tokyo",
	"kst": "Asia/Seoul", "seoul": "Asia/Seoul", "korea": "Asia/Seoul",
	"cet": "Europe/Paris", "paris": "Europe/Paris",
	"bst": "Europe/London", "london": "Europe/London", "uk": "Europe/London",
	"ist": "Asia/Kolkata", "india": "Asia/Kolkata",
	"aest": "Australia/Sydney", "sydney": "Australia/Sydney",
	"aedt": "Australia/Sydney",
	"cst_china": "Asia/Shanghai", "shanghai": "Asia/Shanghai", "beijing": "Asia/Shanghai", "china": "Asia/Shanghai",
	"hkt": "Asia/Hong_Kong", "hong kong": "Asia/Hong_Kong",
	"sgt": "Asia/Singapore", "singapore": "Asia/Singapore",
}

// ConvertTimezone converts a time from one timezone to another.
func ConvertTimezone(timeStr, fromTZ, toTZ string) (string, error) {
	fromTZ = resolveTimezone(fromTZ)
	toTZ = resolveTimezone(toTZ)

	fromLoc, err := time.LoadLocation(fromTZ)
	if err != nil {
		return "", fmt.Errorf("unknown source timezone: %s", fromTZ)
	}
	toLoc, err := time.LoadLocation(toTZ)
	if err != nil {
		return "", fmt.Errorf("unknown target timezone: %s", toTZ)
	}

	parsedTime, err := parseTimeString(timeStr)
	if err != nil {
		return "", err
	}

	now := time.Now()
	sourceTime := time.Date(now.Year(), now.Month(), now.Day(),
		parsedTime.Hour(), parsedTime.Minute(), 0, 0, fromLoc)

	targetTime := sourceTime.In(toLoc)

	return fmt.Sprintf("%s %s = %s %s",
		sourceTime.Format("3:04 PM"), fromTZ,
		targetTime.Format("3:04 PM"), toTZ), nil
}

func resolveTimezone(tz string) string {
	tz = strings.ToLower(strings.TrimSpace(tz))
	if alias, ok := timezoneAliases[tz]; ok {
		return alias
	}
	return tz
}

func parseTimeString(timeStr string) (time.Time, error) {
	timeStr = strings.TrimSpace(timeStr)
	formats := []string{
		"3:04 PM", "3:04PM", "3:04pm", "3:04 pm",
		"15:04", "3PM", "3pm", "3 PM", "3 pm",
		"15:04:05",
	}
	for _, format := range formats {
		if t, err := time.Parse(format, timeStr); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("could not parse time: %s", timeStr)
}
