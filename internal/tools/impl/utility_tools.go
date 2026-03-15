package impl

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/utils"
	"github.com/jackstrohm/jot/tools"
	"github.com/martinlindhe/unit"
)

func init() {
	registerUtilityTools()
}

func registerUtilityTools() {
	tools.Register(&tools.Tool{
		Name:        "calculate",
		Description: "Evaluate mathematical expressions. Supports basic arithmetic (+, -, *, /), percentages, powers (^), square roots, and common functions.",
		Category:    "utility",
		DocURL:      "https://github.com/Knetic/govaluate",
		Params: []tools.Param{
			tools.RequiredStringParam("expression", "The math expression to evaluate (e.g., '2+2', '15% of 200', 'sqrt(144)', '2^10')"),
		},
		Execute: func(ctx context.Context, env infra.ToolEnv, args *tools.Args) tools.Result {
			expression, ok := args.RequiredString("expression")
			if !ok {
				return tools.MissingParam("expression")
			}
			result, err := utils.EvaluateMathExpression(expression)
			if err != nil {
				return tools.Fail("Calculation error: %v", err)
			}
			return tools.OK("%s = %s", expression, result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "date_calc",
		Description: "Perform date calculations: days between dates, add/subtract days, get day of week, parse natural dates.",
		Category:    "utility",
		DocURL:      "https://pkg.go.dev/time",
		Params: []tools.Param{
			tools.EnumParam("operation", "Operation: 'days_between', 'add_days', 'subtract_days', 'day_of_week', 'parse'", true, []string{"days_between", "add_days", "subtract_days", "day_of_week", "parse"}),
			tools.RequiredStringParam("date1", "First date (YYYY-MM-DD format or 'today', 'tomorrow', 'yesterday')"),
			tools.OptionalStringParam("date2", "Second date for days_between (YYYY-MM-DD format)"),
			tools.IntParam("days", "Number of days for add_days/subtract_days operations", false, 0),
		},
		Execute: func(ctx context.Context, env infra.ToolEnv, args *tools.Args) tools.Result {
			operation, ok := args.RequiredString("operation")
			if !ok {
				return tools.MissingParam("operation")
			}
			date1Str, ok := args.RequiredString("date1")
			if !ok {
				return tools.MissingParam("date1")
			}
			date2Str := args.String("date2", "")
			days := args.Int("days", 0)
			result, err := utils.PerformDateCalculation(operation, date1Str, date2Str, days)
			if err != nil {
				return tools.Fail("Date calculation error: %v", err)
			}
			return tools.OK("%s", result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "text_stats",
		Description: "Get statistics about a piece of text: word count, character count, sentence count, reading time.",
		Category:    "utility",
		Params: []tools.Param{
			tools.RequiredStringParam("text", "The text to analyze"),
		},
		Execute: func(ctx context.Context, env infra.ToolEnv, args *tools.Args) tools.Result {
			text, ok := args.RequiredString("text")
			if !ok {
				return tools.MissingParam("text")
			}
			stats := utils.AnalyzeText(text)
			return tools.OK("%s", stats)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "convert_units",
		Description: "Convert between common units: temperature (C/F/K), length (m/ft/in/cm/km/mi), weight (kg/lb/oz/g), data (B/KB/MB/GB/TB).",
		Category:    "utility",
		DocURL:      "https://github.com/martinlindhe/unit",
		Params: []tools.Param{
			tools.NumberParam("value", "The numeric value to convert", true),
			tools.RequiredStringParam("from_unit", "Source unit (e.g., 'C', 'kg', 'm', 'GB')"),
			tools.RequiredStringParam("to_unit", "Target unit (e.g., 'F', 'lb', 'ft', 'MB')"),
		},
		Execute: func(ctx context.Context, env infra.ToolEnv, args *tools.Args) tools.Result {
			value := args.Float("value", 0)
			fromUnit, ok := args.RequiredString("from_unit")
			if !ok {
				return tools.MissingParam("from_unit")
			}
			toUnit, ok := args.RequiredString("to_unit")
			if !ok {
				return tools.MissingParam("to_unit")
			}
			result, err := convertUnits(value, fromUnit, toUnit)
			if err != nil {
				return tools.Fail("Conversion error: %v", err)
			}
			return tools.OK("%s", result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "random",
		Description: "Generate random values: numbers, UUIDs, or pick from a list.",
		Category:    "utility",
		Params: []tools.Param{
			tools.EnumParam("type", "Type of random: 'number', 'uuid', 'pick', 'coin', 'dice'", true, []string{"number", "uuid", "pick", "coin", "dice"}),
			tools.IntParam("min", "Minimum value for 'number' type (default 1)", false, 1),
			tools.IntParam("max", "Maximum value for 'number' type (default 100)", false, 100),
			tools.OptionalStringParam("choices", "Comma-separated list of choices for 'pick' type"),
		},
		Execute: func(ctx context.Context, env infra.ToolEnv, args *tools.Args) tools.Result {
			randType, ok := args.RequiredString("type")
			if !ok {
				return tools.MissingParam("type")
			}
			minVal := args.Int("min", 1)
			maxVal := args.Int("max", 100)
			choices := args.String("choices", "")
			result := utils.GenerateRandom(randType, minVal, maxVal, choices)
			return tools.OK("%s", result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "timezone_convert",
		Description: "Convert a time from one timezone to another. Supports common timezone names and abbreviations.",
		Category:    "utility",
		Params: []tools.Param{
			tools.RequiredStringParam("time", "The time to convert (e.g., '3:00 PM', '15:00', '3pm')"),
			tools.RequiredStringParam("from_tz", "Source timezone (e.g., 'PST', 'America/New_York', 'EST', 'UTC', 'JST', 'London', 'Tokyo')"),
			tools.RequiredStringParam("to_tz", "Target timezone (e.g., 'JST', 'Europe/London', 'PT', 'UTC')"),
		},
		Execute: func(ctx context.Context, env infra.ToolEnv, args *tools.Args) tools.Result {
			timeStr, ok := args.RequiredString("time")
			if !ok {
				return tools.MissingParam("time")
			}
			fromTZ, ok := args.RequiredString("from_tz")
			if !ok {
				return tools.MissingParam("from_tz")
			}
			toTZ, ok := args.RequiredString("to_tz")
			if !ok {
				return tools.MissingParam("to_tz")
			}
			result, err := utils.ConvertTimezone(timeStr, fromTZ, toTZ)
			if err != nil {
				return tools.Fail("Timezone conversion error: %v", err)
			}
			return tools.OK("%s", result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "encode_decode",
		Description: "Encode or decode text: base64, URL encoding, or format JSON.",
		Category:    "utility",
		DocURL:      "https://pkg.go.dev/encoding/base64",
		Params: []tools.Param{
			tools.EnumParam("operation", "Operation: 'base64_encode', 'base64_decode', 'url_encode', 'url_decode', 'json_format', 'json_minify'", true, []string{"base64_encode", "base64_decode", "url_encode", "url_decode", "json_format", "json_minify"}),
			tools.RequiredStringParam("text", "The text to process"),
		},
		Execute: func(ctx context.Context, env infra.ToolEnv, args *tools.Args) tools.Result {
			operation, ok := args.RequiredString("operation")
			if !ok {
				return tools.MissingParam("operation")
			}
			text, ok := args.RequiredString("text")
			if !ok {
				return tools.MissingParam("text")
			}
			result, err := utils.EncodeDecodeText(operation, text)
			if err != nil {
				return tools.Fail("Encode/decode error: %v", err)
			}
			return tools.OK("%s", result)
		},
	})
}

// convertUnits converts between common units: temp, length, mass, data (tool: convert_units).
func convertUnits(value float64, fromUnit, toUnit string) (string, error) {
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
