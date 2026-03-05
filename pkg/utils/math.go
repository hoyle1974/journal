package utils

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Knetic/govaluate"
)

// EvaluateMathExpression evaluates a mathematical expression string.
func EvaluateMathExpression(expr string) (string, error) {
	expr = strings.TrimSpace(expr)
	expr = strings.ToLower(expr)

	percentOfRegex := regexp.MustCompile(`([\d.]+)\s*%\s*of\s*([\d.]+)`)
	if matches := percentOfRegex.FindStringSubmatch(expr); len(matches) == 3 {
		percent, _ := strconv.ParseFloat(matches[1], 64)
		value, _ := strconv.ParseFloat(matches[2], 64)
		result := (percent / 100) * value
		return formatNumber(result), nil
	}

	if strings.HasSuffix(expr, "%") && !strings.Contains(expr, " ") {
		numStr := strings.TrimSuffix(expr, "%")
		num, err := strconv.ParseFloat(numStr, 64)
		if err == nil {
			return formatNumber(num / 100), nil
		}
	}

	expr = regexp.MustCompile(`\^`).ReplaceAllString(expr, "**")

	result, err := EvalSimpleArithmetic(expr)
	if err != nil {
		return "", err
	}
	return formatNumber(result), nil
}

// EvalSimpleArithmetic evaluates basic +, -, *, /, **, sqrt, etc. using govaluate.
func EvalSimpleArithmetic(expr string) (float64, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return 0, fmt.Errorf("empty expression")
	}
	functions := map[string]govaluate.ExpressionFunction{
		"sqrt": func(args ...interface{}) (interface{}, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("sqrt takes one argument")
			}
			switch v := args[0].(type) {
			case float64:
				return math.Sqrt(v), nil
			case int:
				return math.Sqrt(float64(v)), nil
			case int64:
				return math.Sqrt(float64(v)), nil
			default:
				return nil, fmt.Errorf("sqrt argument must be number")
			}
		},
		"pow": func(args ...interface{}) (interface{}, error) {
			if len(args) != 2 {
				return nil, fmt.Errorf("pow takes two arguments")
			}
			base, ok := toFloat64(args[0])
			if !ok {
				return nil, fmt.Errorf("pow base must be number")
			}
			exp, ok := toFloat64(args[1])
			if !ok {
				return nil, fmt.Errorf("pow exponent must be number")
			}
			return math.Pow(base, exp), nil
		},
	}
	eval, err := govaluate.NewEvaluableExpressionWithFunctions(expr, functions)
	if err != nil {
		return 0, err
	}
	result, err := eval.Evaluate(nil)
	if err != nil {
		return 0, err
	}
	switch v := result.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("expression did not evaluate to a number (got %T)", result)
	}
}

func toFloat64(a interface{}) (float64, bool) {
	switch v := a.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

func formatNumber(n float64) string {
	if n == float64(int64(n)) {
		return fmt.Sprintf("%d", int64(n))
	}
	return fmt.Sprintf("%.6g", n)
}

// PerformDateCalculation handles date arithmetic operations.
func PerformDateCalculation(operation, date1Str, date2Str string, days int) (string, error) {
	date1, err := parseFlexibleDate(date1Str)
	if err != nil {
		return "", fmt.Errorf("invalid date1: %v", err)
	}

	switch operation {
	case "days_between":
		if date2Str == "" {
			return "", fmt.Errorf("date2 required for days_between")
		}
		date2, err := parseFlexibleDate(date2Str)
		if err != nil {
			return "", fmt.Errorf("invalid date2: %v", err)
		}
		diff := date2.Sub(date1).Hours() / 24
		return fmt.Sprintf("%.0f days between %s and %s", math.Abs(diff), date1.Format("2006-01-02"), date2.Format("2006-01-02")), nil

	case "add_days":
		result := date1.AddDate(0, 0, days)
		return fmt.Sprintf("%s + %d days = %s (%s)", date1.Format("2006-01-02"), days, result.Format("2006-01-02"), result.Format("Monday")), nil

	case "subtract_days":
		result := date1.AddDate(0, 0, -days)
		return fmt.Sprintf("%s - %d days = %s (%s)", date1.Format("2006-01-02"), days, result.Format("2006-01-02"), result.Format("Monday")), nil

	case "day_of_week":
		return fmt.Sprintf("%s is a %s", date1.Format("2006-01-02"), date1.Format("Monday")), nil

	case "parse":
		return fmt.Sprintf("Parsed: %s (%s)", date1.Format("2006-01-02"), date1.Format("Monday, January 2, 2006")), nil

	default:
		return "", fmt.Errorf("unknown operation: %s", operation)
	}
}

func parseFlexibleDate(dateStr string) (time.Time, error) {
	dateStr = strings.ToLower(strings.TrimSpace(dateStr))
	now := time.Now()

	switch dateStr {
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()), nil
	case "tomorrow":
		return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location()), nil
	case "yesterday":
		return time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, now.Location()), nil
	}

	formats := []string{
		"2006-01-02",
		"01/02/2006",
		"1/2/2006",
		"Jan 2, 2006",
		"January 2, 2006",
		"2 Jan 2006",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("could not parse date: %s", dateStr)
}

// ParseRelativeDate interprets natural language date expressions and returns a (start, end) range in local time.
func ParseRelativeDate(input string) (time.Time, time.Time, error) {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("empty date expression")
	}
	now := time.Now()
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	yesterday := today.AddDate(0, 0, -1)

	switch input {
	case "today":
		return today, today.Add(24*time.Hour - time.Second), nil
	case "yesterday":
		return yesterday, yesterday.Add(24*time.Hour - time.Second), nil
	case "this morning":
		return today, now, nil
	case "since yesterday":
		return yesterday, now, nil
	case "last week":
		weekday := now.Weekday()
		if weekday == time.Sunday {
			weekday = 7
		}
		daysSinceMonday := int(weekday - time.Monday)
		thisMonday := today.AddDate(0, 0, -daysSinceMonday)
		lastMonday := thisMonday.AddDate(0, 0, -7)
		lastSunday := lastMonday.AddDate(0, 0, 6)
		return lastMonday, lastSunday.Add(24*time.Hour - time.Second), nil
	case "last month":
		firstOfThisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		lastMonth := firstOfThisMonth.AddDate(0, -1, 0)
		firstOfLastMonth := time.Date(lastMonth.Year(), lastMonth.Month(), 1, 0, 0, 0, 0, loc)
		firstOfThisMonthMinusOne := firstOfThisMonth.Add(-time.Second)
		return firstOfLastMonth, firstOfThisMonthMinusOne, nil
	}

	if strings.HasPrefix(input, "since ") {
		expr := strings.TrimSpace(input[6:])
		dayNames := map[string]time.Weekday{
			"monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
			"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday, "sunday": time.Sunday,
			"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
			"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
		}
		if wd, ok := dayNames[expr]; ok {
			daysBack := int(now.Weekday() - wd)
			if now.Weekday() == time.Sunday {
				daysBack = int(7 - wd)
			}
			if daysBack < 0 {
				daysBack += 7
			}
			thatDay := today.AddDate(0, 0, -daysBack)
			return thatDay, now, nil
		}
		t, err := parseFlexibleDate(expr)
		if err == nil {
			return t, now, nil
		}
	}

	t, err := parseFlexibleDate(input)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	end := start.Add(24*time.Hour - time.Second)
	return start, end, nil
}

// ResolveDateRange resolves start_date and end_date (natural language or YYYY-MM-DD) to YYYY-MM-DD strings for DB queries.
func ResolveDateRange(startExpr, endExpr string) (startStr, endStr string, err error) {
	var start, end time.Time
	if startTime, endTime, e := ParseRelativeDate(startExpr); e == nil {
		start = startTime
		end = endTime
	} else if len(startExpr) >= 10 && startExpr[4] == '-' && startExpr[7] == '-' {
		t, e := time.Parse("2006-01-02", startExpr[:10])
		if e != nil {
			return "", "", fmt.Errorf("invalid start_date: %w", e)
		}
		start = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
		end = start.Add(24*time.Hour - time.Second)
	} else {
		return "", "", fmt.Errorf("invalid start_date: %s", startExpr)
	}
	if endExpr != "" && endExpr != startExpr {
		_, endEnd, e := ParseRelativeDate(endExpr)
		if e == nil {
			end = endEnd
		} else if len(endExpr) >= 10 && endExpr[4] == '-' && endExpr[7] == '-' {
			t, e := time.Parse("2006-01-02", endExpr[:10])
			if e != nil {
				return "", "", fmt.Errorf("invalid end_date: %w", e)
			}
			end = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, t.Location())
		}
	}
	return start.Format("2006-01-02"), end.Format("2006-01-02"), nil
}
