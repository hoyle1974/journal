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

type calculateArgs struct {
	Expression string `json:"expression" description:"The math expression to evaluate (e.g., '2+2', '15% of 200', 'sqrt(144)', '2^10')" required:"true"`
}

type dateCalcArgs struct {
	Operation string `json:"operation" description:"Operation: 'days_between', 'add_days', 'subtract_days', 'day_of_week', 'parse'" required:"true" enum:"days_between,add_days,subtract_days,day_of_week,parse"`
	Date1     string `json:"date1" description:"First date (YYYY-MM-DD format or 'today', 'tomorrow', 'yesterday')" required:"true"`
	Date2     string `json:"date2" description:"Second date for days_between (YYYY-MM-DD format)"`
	Days      int    `json:"days" description:"Number of days for add_days/subtract_days operations" default:"0"`
}

type textStatsArgs struct {
	Text string `json:"text" description:"The text to analyze" required:"true"`
}

type convertUnitsArgs struct {
	Value    float64 `json:"value" description:"The numeric value to convert" required:"true"`
	FromUnit string  `json:"from_unit" description:"Source unit (e.g., 'C', 'kg', 'm', 'GB')" required:"true"`
	ToUnit   string  `json:"to_unit" description:"Target unit (e.g., 'F', 'lb', 'ft', 'MB')" required:"true"`
}

type randomArgs struct {
	Type    string `json:"type" description:"Type of random: 'number', 'uuid', 'pick', 'coin', 'dice'" required:"true" enum:"number,uuid,pick,coin,dice"`
	Min     int    `json:"min" description:"Minimum value for 'number' type (default 1)" default:"1"`
	Max     int    `json:"max" description:"Maximum value for 'number' type (default 100)" default:"100"`
	Choices string `json:"choices" description:"Comma-separated list of choices for 'pick' type"`
}

type timezoneConvertArgs struct {
	Time   string `json:"time" description:"The time to convert (e.g., '3:00 PM', '15:00', '3pm')" required:"true"`
	FromTZ string `json:"from_tz" description:"Source timezone (e.g., 'PST', 'America/New_York', 'EST', 'UTC', 'JST', 'London', 'Tokyo')" required:"true"`
	ToTZ   string `json:"to_tz" description:"Target timezone (e.g., 'JST', 'Europe/London', 'PT', 'UTC')" required:"true"`
}

type encodeDecodeArgs struct {
	Operation string `json:"operation" description:"Operation: 'base64_encode', 'base64_decode', 'url_encode', 'url_decode', 'json_format', 'json_minify'" required:"true" enum:"base64_encode,base64_decode,url_encode,url_decode,json_format,json_minify"`
	Text      string `json:"text" description:"The text to process" required:"true"`
}

func init() {
	registerUtilityTools()
}

func registerUtilityTools() {
	tools.Register(&tools.Tool{
		Name:        "calculate",
		Description: "Evaluate mathematical expressions. Supports basic arithmetic (+, -, *, /), percentages, powers (^), square roots, and common functions.",
		Category:    "utility",
		DocURL:      "https://github.com/Knetic/govaluate",
		Args:        &calculateArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*calculateArgs)
			if a.Expression == "" {
				return tools.MissingParam("expression")
			}
			result, err := utils.EvaluateMathExpression(a.Expression)
			if err != nil {
				return tools.Fail("Calculation error: %v", err)
			}
			return tools.OK("%s = %s", a.Expression, result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "date_calc",
		Description: "Perform date calculations: days between dates, add/subtract days, get day of week, parse natural dates.",
		Category:    "utility",
		DocURL:      "https://pkg.go.dev/time",
		Args:        &dateCalcArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*dateCalcArgs)
			if a.Operation == "" {
				return tools.MissingParam("operation")
			}
			if a.Date1 == "" {
				return tools.MissingParam("date1")
			}
			result, err := utils.PerformDateCalculation(a.Operation, a.Date1, a.Date2, a.Days)
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
		Args:        &textStatsArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*textStatsArgs)
			if a.Text == "" {
				return tools.MissingParam("text")
			}
			stats := utils.AnalyzeText(a.Text)
			return tools.OK("%s", stats)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "convert_units",
		Description: "Convert between common units: temperature (C/F/K), length (m/ft/in/cm/km/mi), weight (kg/lb/oz/g), data (B/KB/MB/GB/TB).",
		Category:    "utility",
		DocURL:      "https://github.com/martinlindhe/unit",
		Args:        &convertUnitsArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*convertUnitsArgs)
			if a.FromUnit == "" {
				return tools.MissingParam("from_unit")
			}
			if a.ToUnit == "" {
				return tools.MissingParam("to_unit")
			}
			result, err := convertUnits(a.Value, a.FromUnit, a.ToUnit)
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
		Args:        &randomArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*randomArgs)
			if a.Type == "" {
				return tools.MissingParam("type")
			}
			minVal, maxVal := a.Min, a.Max
			if minVal == 0 {
				minVal = 1
			}
			if maxVal == 0 {
				maxVal = 100
			}
			result := utils.GenerateRandom(a.Type, minVal, maxVal, a.Choices)
			return tools.OK("%s", result)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "timezone_convert",
		Description: "Convert a time from one timezone to another. Supports common timezone names and abbreviations.",
		Category:    "utility",
		Args:        &timezoneConvertArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*timezoneConvertArgs)
			if a.Time == "" {
				return tools.MissingParam("time")
			}
			if a.FromTZ == "" {
				return tools.MissingParam("from_tz")
			}
			if a.ToTZ == "" {
				return tools.MissingParam("to_tz")
			}
			result, err := utils.ConvertTimezone(a.Time, a.FromTZ, a.ToTZ)
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
		Args:        &encodeDecodeArgs{},
		Execute: func(ctx context.Context, env infra.ToolEnv, args any) tools.Result {
			a := args.(*encodeDecodeArgs)
			if a.Operation == "" {
				return tools.MissingParam("operation")
			}
			if a.Text == "" {
				return tools.MissingParam("text")
			}
			result, err := utils.EncodeDecodeText(a.Operation, a.Text)
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
