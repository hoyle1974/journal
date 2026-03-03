package impl

import (
	"context"

	"github.com/jackstrohm/jot"
	"github.com/jackstrohm/jot/tools"
)

func init() {
	registerUtilityTools()
}

func registerUtilityTools() {
	tools.Register(&tools.Tool{
		Name:        "calculate",
		Description: "Evaluate mathematical expressions. Supports basic arithmetic (+, -, *, /), percentages, powers (^), square roots, and common functions.",
		Category:    "utility",
		Params: []tools.Param{
			tools.RequiredStringParam("expression", "The math expression to evaluate (e.g., '2+2', '15% of 200', 'sqrt(144)', '2^10')"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			expression, ok := args.RequiredString("expression")
			if !ok {
				return tools.MissingParam("expression")
			}
			result, err := jot.EvaluateMathExpression(expression)
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
		Params: []tools.Param{
			tools.EnumParam("operation", "Operation: 'days_between', 'add_days', 'subtract_days', 'day_of_week', 'parse'", true, []string{"days_between", "add_days", "subtract_days", "day_of_week", "parse"}),
			tools.RequiredStringParam("date1", "First date (YYYY-MM-DD format or 'today', 'tomorrow', 'yesterday')"),
			tools.OptionalStringParam("date2", "Second date for days_between (YYYY-MM-DD format)"),
			tools.IntParam("days", "Number of days for add_days/subtract_days operations", false, 0),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
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
			result, err := jot.PerformDateCalculation(operation, date1Str, date2Str, days)
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
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			text, ok := args.RequiredString("text")
			if !ok {
				return tools.MissingParam("text")
			}
			stats := jot.AnalyzeText(text)
			return tools.OK("%s", stats)
		},
	})

	tools.Register(&tools.Tool{
		Name:        "convert_units",
		Description: "Convert between common units: temperature (C/F/K), length (m/ft/in/cm/km/mi), weight (kg/lb/oz/g), data (B/KB/MB/GB/TB).",
		Category:    "utility",
		Params: []tools.Param{
			tools.NumberParam("value", "The numeric value to convert", true),
			tools.RequiredStringParam("from_unit", "Source unit (e.g., 'C', 'kg', 'm', 'GB')"),
			tools.RequiredStringParam("to_unit", "Target unit (e.g., 'F', 'lb', 'ft', 'MB')"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			value := args.Float("value", 0)
			fromUnit, ok := args.RequiredString("from_unit")
			if !ok {
				return tools.MissingParam("from_unit")
			}
			toUnit, ok := args.RequiredString("to_unit")
			if !ok {
				return tools.MissingParam("to_unit")
			}
			result, err := jot.ConvertUnits(value, fromUnit, toUnit)
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
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			randType, ok := args.RequiredString("type")
			if !ok {
				return tools.MissingParam("type")
			}
			minVal := args.Int("min", 1)
			maxVal := args.Int("max", 100)
			choices := args.String("choices", "")
			result := jot.GenerateRandom(randType, minVal, maxVal, choices)
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
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
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
			result, err := jot.ConvertTimezone(timeStr, fromTZ, toTZ)
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
		Params: []tools.Param{
			tools.EnumParam("operation", "Operation: 'base64_encode', 'base64_decode', 'url_encode', 'url_decode', 'json_format', 'json_minify'", true, []string{"base64_encode", "base64_decode", "url_encode", "url_decode", "json_format", "json_minify"}),
			tools.RequiredStringParam("text", "The text to process"),
		},
		Execute: func(ctx context.Context, args *tools.Args) tools.Result {
			operation, ok := args.RequiredString("operation")
			if !ok {
				return tools.MissingParam("operation")
			}
			text, ok := args.RequiredString("text")
			if !ok {
				return tools.MissingParam("text")
			}
			result, err := jot.EncodeDecodeText(operation, text)
			if err != nil {
				return tools.Fail("Encode/decode error: %v", err)
			}
			return tools.OK("%s", result)
		},
	})
}
