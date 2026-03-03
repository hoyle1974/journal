package jot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Knetic/govaluate"
	"github.com/google/uuid"
)

// NOTE: ToolDefinitions and ExecuteTool have been migrated to the tools/ package.
// Tool definitions are now in tool_impls.go (registered via tools.Register).
// Tool execution uses tools.Execute().
// The helper functions below are kept for use by tool implementations.

// FormatEntriesForContext formats entries into a readable string for the LLM context.
func FormatEntriesForContext(entries []Entry, maxChars int) string {
	if len(entries) == 0 {
		return "No entries found."
	}

	var lines []string
	totalRunes := 0

	for i, e := range entries {
		ts := e.Timestamp
		if ts == "" {
			ts = "(no date)"
		} else {
			ts = SafeTruncate(ts, 19)
		}
		content := SanitizePrompt(e.Content)
		line := fmt.Sprintf("[%s] (%s) %s", ts, e.Source, content)
		lineRunes := utf8.RuneCountInString(line)
		if totalRunes+lineRunes+1 > maxChars {
			lines = append(lines, fmt.Sprintf("... and %d more entries (truncated)", len(entries)-i))
			break
		}
		lines = append(lines, line)
		totalRunes += lineRunes + 1
	}

	return strings.Join(lines, "\n")
}

// FormatQueriesForContext formats queries into a readable string for the LLM context.
func FormatQueriesForContext(queries []QueryLog, maxChars int) string {
	if len(queries) == 0 {
		return "No queries found."
	}

	var lines []string
	totalRunes := 0

	for i, q := range queries {
		answer := SanitizePrompt(q.Answer)
		if utf8.RuneCountInString(answer) > 300 {
			answer = SafeTruncate(answer, 300) + "..."
		}
		ts := q.Timestamp
		if ts == "" {
			ts = "(no date)"
		} else {
			ts = SafeTruncate(ts, 19)
		}
		question := SanitizePrompt(q.Question)
		line := fmt.Sprintf("[%s] (%s)\n  Q: %s\n  A: %s", ts, q.Source, question, answer)
		lineRunes := utf8.RuneCountInString(line)
		if totalRunes+lineRunes+2 > maxChars {
			lines = append(lines, fmt.Sprintf("... and %d more queries (truncated)", len(queries)-i))
			break
		}
		lines = append(lines, line)
		totalRunes += lineRunes + 2
	}

	return strings.Join(lines, "\n\n")
}

// ToolResult represents the result of executing a tool.
type ToolResult struct {
	Success bool
	Result  string
}


// === UTILITY TOOL HELPER FUNCTIONS ===

// EvaluateMathExpression evaluates a mathematical expression string
func EvaluateMathExpression(expr string) (string, error) {
	expr = strings.TrimSpace(expr)
	expr = strings.ToLower(expr)

	// Handle percentage expressions like "15% of 200"
	percentOfRegex := regexp.MustCompile(`([\d.]+)\s*%\s*of\s*([\d.]+)`)
	if matches := percentOfRegex.FindStringSubmatch(expr); len(matches) == 3 {
		percent, _ := strconv.ParseFloat(matches[1], 64)
		value, _ := strconv.ParseFloat(matches[2], 64)
		result := (percent / 100) * value
		return formatNumber(result), nil
	}

	// Handle simple percentage like "15%" (convert to decimal)
	if strings.HasSuffix(expr, "%") && !strings.Contains(expr, " ") {
		numStr := strings.TrimSuffix(expr, "%")
		num, err := strconv.ParseFloat(numStr, 64)
		if err == nil {
			return formatNumber(num / 100), nil
		}
	}

	// Handle sqrt
	sqrtRegex := regexp.MustCompile(`sqrt\(([\d.]+)\)`)
	if matches := sqrtRegex.FindStringSubmatch(expr); len(matches) == 2 {
		num, _ := strconv.ParseFloat(matches[1], 64)
		return formatNumber(math.Sqrt(num)), nil
	}

	// Handle power with ^
	if strings.Contains(expr, "^") {
		parts := strings.Split(expr, "^")
		if len(parts) == 2 {
			base, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			exp, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 == nil && err2 == nil {
				return formatNumber(math.Pow(base, exp)), nil
			}
		}
	}

	// Simple arithmetic evaluation
	result, err := EvalSimpleArithmetic(expr)
	if err != nil {
		return "", err
	}
	return formatNumber(result), nil
}

// EvalSimpleArithmetic evaluates basic +, -, *, / expressions and parenthesized
// subexpressions using govaluate for correct precedence and robust parsing.
func EvalSimpleArithmetic(expr string) (float64, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return 0, fmt.Errorf("empty expression")
	}
	eval, err := govaluate.NewEvaluableExpression(expr)
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

func formatNumber(n float64) string {
	if n == float64(int64(n)) {
		return fmt.Sprintf("%d", int64(n))
	}
	return fmt.Sprintf("%.6g", n)
}

// PerformDateCalculation handles date arithmetic operations
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

	// Try standard formats
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
// Handles: "this morning", "yesterday", "last week", "last month", "since Tuesday", "since yesterday", "today", etc.
// Single-day expressions (e.g. "yesterday") return that day 00:00:00 to 23:59:59.
// "Since X" expressions return X 00:00:00 to now.
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
		// ISO week: Monday = 1. Go's Weekday(): Monday=1, Sunday=0 (so Sunday is 0).
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

	// "since <day name>" or "since <date>"
	if strings.HasPrefix(input, "since ") {
		expr := strings.TrimSpace(input[6:])
		// Day names
		dayNames := map[string]time.Weekday{
			"monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
			"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday, "sunday": time.Sunday,
			"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
			"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
		}
		if wd, ok := dayNames[expr]; ok {
			// Most recent occurrence of that weekday (today or in the past)
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
		// Try YYYY-MM-DD or parseFlexibleDate
		t, err := parseFlexibleDate(expr)
		if err == nil {
			return t, now, nil
		}
	}

	// Single flexible date (treat as full day)
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
	// Resolve start: use start of range for the expression
	var start, end time.Time
	if startTime, endTime, e := ParseRelativeDate(startExpr); e == nil {
		start = startTime
		// If end_expr not given we use same range end for start expression (e.g. "yesterday" -> both bounds)
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
	// Resolve end from endExpr (overrides end of range)
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

// FetchURLContent fetches and extracts text from a URL
func FetchURLContent(url string, maxLength int) (string, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // 1MB max
	if err != nil {
		return "", err
	}

	// Strip HTML tags (simple approach)
	text := stripHTMLTags(string(body))

	// Clean up whitespace
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	if len(text) > maxLength {
		text = truncateToMaxBytes(text, maxLength) + "...[truncated]"
	}

	return fmt.Sprintf("Content from %s:\n\n%s", url, text), nil
}

func stripHTMLTags(html string) string {
	// Remove script and style content
	scriptRegex := regexp.MustCompile(`(?is)<script.*?</script>`)
	styleRegex := regexp.MustCompile(`(?is)<style.*?</style>`)
	html = scriptRegex.ReplaceAllString(html, "")
	html = styleRegex.ReplaceAllString(html, "")

	// Remove HTML tags
	tagRegex := regexp.MustCompile(`<[^>]*>`)
	text := tagRegex.ReplaceAllString(html, " ")

	// Decode common HTML entities
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")

	return text
}

// AnalyzeText returns statistics about a text
func AnalyzeText(text string) string {
	charCount := len(text)
	charCountNoSpaces := len(strings.ReplaceAll(text, " ", ""))

	words := strings.Fields(text)
	wordCount := len(words)

	// Count sentences (simple heuristic)
	sentenceRegex := regexp.MustCompile(`[.!?]+`)
	sentences := sentenceRegex.FindAllString(text, -1)
	sentenceCount := len(sentences)
	if sentenceCount == 0 && wordCount > 0 {
		sentenceCount = 1
	}

	// Reading time (average 200 words per minute)
	readingMinutes := float64(wordCount) / 200.0

	// Count paragraphs
	paragraphs := strings.Split(text, "\n\n")
	paraCount := 0
	for _, p := range paragraphs {
		if strings.TrimSpace(p) != "" {
			paraCount++
		}
	}

	return fmt.Sprintf("Text Statistics:\n- Characters: %d (without spaces: %d)\n- Words: %d\n- Sentences: %d\n- Paragraphs: %d\n- Reading time: %.1f minutes",
		charCount, charCountNoSpaces, wordCount, sentenceCount, paraCount, readingMinutes)
}

// ConvertUnits converts between common units
func ConvertUnits(value float64, fromUnit, toUnit string) (string, error) {
	fromUnit = strings.ToLower(strings.TrimSpace(fromUnit))
	toUnit = strings.ToLower(strings.TrimSpace(toUnit))

	// Temperature conversions
	tempUnits := map[string]bool{"c": true, "f": true, "k": true, "celsius": true, "fahrenheit": true, "kelvin": true}
	if tempUnits[fromUnit] && tempUnits[toUnit] {
		result, err := ConvertTemperature(value, fromUnit, toUnit)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%.2f %s = %.2f %s", value, fromUnit, result, toUnit), nil
	}

	// Length conversions (to meters as base)
	lengthToMeters := map[string]float64{
		"m": 1, "meter": 1, "meters": 1,
		"cm": 0.01, "centimeter": 0.01,
		"mm": 0.001, "millimeter": 0.001,
		"km": 1000, "kilometer": 1000,
		"in": 0.0254, "inch": 0.0254, "inches": 0.0254,
		"ft": 0.3048, "foot": 0.3048, "feet": 0.3048,
		"yd": 0.9144, "yard": 0.9144,
		"mi": 1609.344, "mile": 1609.344, "miles": 1609.344,
	}
	if fromFactor, ok1 := lengthToMeters[fromUnit]; ok1 {
		if toFactor, ok2 := lengthToMeters[toUnit]; ok2 {
			result := (value * fromFactor) / toFactor
			return fmt.Sprintf("%.4g %s = %.4g %s", value, fromUnit, result, toUnit), nil
		}
	}

	// Weight conversions (to grams as base)
	weightToGrams := map[string]float64{
		"g": 1, "gram": 1, "grams": 1,
		"kg": 1000, "kilogram": 1000,
		"mg": 0.001, "milligram": 0.001,
		"lb": 453.592, "pound": 453.592, "pounds": 453.592,
		"oz": 28.3495, "ounce": 28.3495, "ounces": 28.3495,
	}
	if fromFactor, ok1 := weightToGrams[fromUnit]; ok1 {
		if toFactor, ok2 := weightToGrams[toUnit]; ok2 {
			result := (value * fromFactor) / toFactor
			return fmt.Sprintf("%.4g %s = %.4g %s", value, fromUnit, result, toUnit), nil
		}
	}

	// Data size conversions (to bytes as base)
	dataToBytes := map[string]float64{
		"b": 1, "byte": 1, "bytes": 1,
		"kb": 1024, "kilobyte": 1024,
		"mb": 1024 * 1024, "megabyte": 1024 * 1024,
		"gb": 1024 * 1024 * 1024, "gigabyte": 1024 * 1024 * 1024,
		"tb": 1024 * 1024 * 1024 * 1024, "terabyte": 1024 * 1024 * 1024 * 1024,
	}
	if fromFactor, ok1 := dataToBytes[fromUnit]; ok1 {
		if toFactor, ok2 := dataToBytes[toUnit]; ok2 {
			result := (value * fromFactor) / toFactor
			return fmt.Sprintf("%.4g %s = %.4g %s", value, fromUnit, result, toUnit), nil
		}
	}

	return "", fmt.Errorf("unknown unit conversion: %s to %s", fromUnit, toUnit)
}

// ConvertTemperature converts temperature between C, F, K.
func ConvertTemperature(value float64, from, to string) (float64, error) {
	// Normalize unit names
	from = strings.ToLower(from)
	to = strings.ToLower(to)
	if from == "celsius" {
		from = "c"
	}
	if from == "fahrenheit" {
		from = "f"
	}
	if from == "kelvin" {
		from = "k"
	}
	if to == "celsius" {
		to = "c"
	}
	if to == "fahrenheit" {
		to = "f"
	}
	if to == "kelvin" {
		to = "k"
	}

	// Convert to Celsius first
	var celsius float64
	switch from {
	case "c":
		celsius = value
	case "f":
		celsius = (value - 32) * 5 / 9
	case "k":
		celsius = value - 273.15
	default:
		return 0, fmt.Errorf("unknown temperature unit: %s", from)
	}

	// Convert from Celsius to target
	switch to {
	case "c":
		return celsius, nil
	case "f":
		return celsius*9/5 + 32, nil
	case "k":
		return celsius + 273.15, nil
	default:
		return 0, fmt.Errorf("unknown temperature unit: %s", to)
	}
}

// GenerateRandom generates random values
func GenerateRandom(randType string, minVal, maxVal int, choices string) string {
	switch strings.ToLower(randType) {
	case "number":
		if maxVal <= minVal {
			maxVal = minVal + 100
		}
		n := rand.Intn(maxVal-minVal+1) + minVal
		return fmt.Sprintf("Random number (%d-%d): %d", minVal, maxVal, n)

	case "uuid":
		return fmt.Sprintf("Random UUID: %s", uuid.New().String())

	case "pick":
		if choices == "" {
			return "Error: 'choices' parameter required for pick"
		}
		items := strings.Split(choices, ",")
		for i := range items {
			items[i] = strings.TrimSpace(items[i])
		}
		pick := items[rand.Intn(len(items))]
		return fmt.Sprintf("Picked: %s (from %d choices)", pick, len(items))

	case "coin":
		if rand.Intn(2) == 0 {
			return "Coin flip: Heads"
		}
		return "Coin flip: Tails"

	case "dice", "die":
		n := rand.Intn(6) + 1
		return fmt.Sprintf("Dice roll: %d", n)

	default:
		return fmt.Sprintf("Unknown random type: %s (use: number, uuid, pick, coin, dice)", randType)
	}
}

// === NEW UTILITY TOOL HELPER FUNCTIONS ===

// Timezone name mappings to IANA timezone names
var timezoneAliases = map[string]string{
	// US timezones
	"pst": "America/Los_Angeles", "pt": "America/Los_Angeles", "pacific": "America/Los_Angeles",
	"pdt": "America/Los_Angeles", "los angeles": "America/Los_Angeles", "la": "America/Los_Angeles",
	"mst": "America/Denver", "mt": "America/Denver", "mountain": "America/Denver",
	"mdt": "America/Denver", "denver": "America/Denver",
	"cst": "America/Chicago", "ct": "America/Chicago", "central": "America/Chicago",
	"cdt": "America/Chicago", "chicago": "America/Chicago",
	"est": "America/New_York", "et": "America/New_York", "eastern": "America/New_York",
	"edt": "America/New_York", "new york": "America/New_York", "nyc": "America/New_York",
	// International
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

// ConvertTimezone converts a time from one timezone to another
func ConvertTimezone(timeStr, fromTZ, toTZ string) (string, error) {
	// Resolve timezone aliases
	fromTZ = resolveTimezone(fromTZ)
	toTZ = resolveTimezone(toTZ)

	// Load locations
	fromLoc, err := time.LoadLocation(fromTZ)
	if err != nil {
		return "", fmt.Errorf("unknown source timezone: %s", fromTZ)
	}
	toLoc, err := time.LoadLocation(toTZ)
	if err != nil {
		return "", fmt.Errorf("unknown target timezone: %s", toTZ)
	}

	// Parse time (try various formats)
	parsedTime, err := parseTimeString(timeStr)
	if err != nil {
		return "", err
	}

	// Create time in source timezone (using today's date)
	now := time.Now()
	sourceTime := time.Date(now.Year(), now.Month(), now.Day(),
		parsedTime.Hour(), parsedTime.Minute(), 0, 0, fromLoc)

	// Convert to target timezone
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
	// Try as-is (might be a valid IANA name)
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

// HandleCountdown manages countdown events
func HandleCountdown(ctx context.Context, action, name, dateStr string) (string, error) {
	switch strings.ToLower(action) {
	case "create":
		if name == "" || dateStr == "" {
			return "", fmt.Errorf("name and date required for create")
		}
		targetDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return "", fmt.Errorf("invalid date format (use YYYY-MM-DD): %v", err)
		}
		metadata := fmt.Sprintf(`{"target_date": "%s"}`, dateStr)
		id, err := UpsertKnowledge(ctx, fmt.Sprintf("Countdown: %s", name), "countdown", metadata)
		if err != nil {
			return "", err
		}
		daysUntil := int(time.Until(targetDate).Hours() / 24)
		return fmt.Sprintf("Countdown '%s' created for %s (%d days away). ID: %s", name, dateStr, daysUntil, id), nil

	case "check":
		if name == "" {
			return "", fmt.Errorf("name required for check")
		}
		// Search for countdown by name
		queryVec, err := GenerateEmbedding(ctx, fmt.Sprintf("Countdown: %s", name))
		if err != nil {
			return "", err
		}
		nodes, err := QuerySimilarNodes(ctx, queryVec, 5)
		if err != nil {
			return "", err
		}
		for _, node := range nodes {
			if node.NodeType == "countdown" && strings.Contains(strings.ToLower(node.Content), strings.ToLower(name)) {
				// Parse target date from metadata
				var meta map[string]string
				if err := json.Unmarshal([]byte(node.Metadata), &meta); err == nil {
					if targetStr, ok := meta["target_date"]; ok {
						targetDate, _ := time.Parse("2006-01-02", targetStr)
						daysUntil := int(time.Until(targetDate).Hours() / 24)
						if daysUntil < 0 {
							return fmt.Sprintf("'%s' was %d days ago (%s)", name, -daysUntil, targetStr), nil
						}
						return fmt.Sprintf("%d days until '%s' (%s)", daysUntil, name, targetStr), nil
					}
				}
			}
		}
		return fmt.Sprintf("Countdown '%s' not found", name), nil

	case "list":
		// Search for all countdowns
		queryVec, err := GenerateEmbedding(ctx, "Countdown event")
		if err != nil {
			return "", err
		}
		nodes, err := QuerySimilarNodes(ctx, queryVec, 20)
		if err != nil {
			return "", err
		}
		var countdowns []string
		for _, node := range nodes {
			if node.NodeType == "countdown" {
				var meta map[string]string
				if err := json.Unmarshal([]byte(node.Metadata), &meta); err == nil {
					if targetStr, ok := meta["target_date"]; ok {
						targetDate, _ := time.Parse("2006-01-02", targetStr)
						daysUntil := int(time.Until(targetDate).Hours() / 24)
						name := strings.TrimPrefix(node.Content, "Countdown: ")
						if daysUntil < 0 {
							countdowns = append(countdowns, fmt.Sprintf("- %s: %d days ago (%s)", name, -daysUntil, targetStr))
						} else {
							countdowns = append(countdowns, fmt.Sprintf("- %s: %d days (%s)", name, daysUntil, targetStr))
						}
					}
				}
			}
		}
		if len(countdowns) == 0 {
			return "No countdowns found.", nil
		}
		return fmt.Sprintf("Countdowns:\n%s", strings.Join(countdowns, "\n")), nil

	case "delete":
		return "", fmt.Errorf("delete not yet implemented - countdowns are stored in knowledge graph")

	default:
		return "", fmt.Errorf("unknown action: %s (use: create, check, list)", action)
	}
}

// LookupWord fetches word definition from Free Dictionary API
func LookupWord(word string) (string, error) {
	word = strings.ToLower(strings.TrimSpace(word))
	url := fmt.Sprintf("https://api.dictionaryapi.dev/api/v2/entries/en/%s", word)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return fmt.Sprintf("No definition found for '%s'", word), nil
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("dictionary API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var entries []struct {
		Word     string `json:"word"`
		Phonetic string `json:"phonetic"`
		Meanings []struct {
			PartOfSpeech string `json:"partOfSpeech"`
			Definitions  []struct {
				Definition string `json:"definition"`
				Example    string `json:"example"`
			} `json:"definitions"`
		} `json:"meanings"`
	}

	if err := json.Unmarshal(body, &entries); err != nil {
		return "", fmt.Errorf("failed to parse dictionary response: %v", err)
	}

	if len(entries) == 0 {
		return fmt.Sprintf("No definition found for '%s'", word), nil
	}

	var result []string
	entry := entries[0]
	result = append(result, fmt.Sprintf("**%s**", entry.Word))
	if entry.Phonetic != "" {
		result = append(result, fmt.Sprintf("Pronunciation: %s", entry.Phonetic))
	}
	result = append(result, "")

	for _, meaning := range entry.Meanings {
		result = append(result, fmt.Sprintf("_%s_", meaning.PartOfSpeech))
		for i, def := range meaning.Definitions {
			if i >= 3 { // Limit to 3 definitions per part of speech
				break
			}
			result = append(result, fmt.Sprintf("%d. %s", i+1, def.Definition))
			if def.Example != "" {
				result = append(result, fmt.Sprintf("   Example: \"%s\"", def.Example))
			}
		}
		result = append(result, "")
	}

	return strings.Join(result, "\n"), nil
}

// EncodeDecodeText performs encoding/decoding operations
func EncodeDecodeText(operation, text string) (string, error) {
	switch strings.ToLower(operation) {
	case "base64_encode":
		encoded := base64.StdEncoding.EncodeToString([]byte(text))
		return fmt.Sprintf("Base64 encoded:\n%s", encoded), nil

	case "base64_decode":
		decoded, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return "", fmt.Errorf("invalid base64: %v", err)
		}
		return fmt.Sprintf("Base64 decoded:\n%s", string(decoded)), nil

	case "url_encode":
		encoded := url.QueryEscape(text)
		return fmt.Sprintf("URL encoded:\n%s", encoded), nil

	case "url_decode":
		decoded, err := url.QueryUnescape(text)
		if err != nil {
			return "", fmt.Errorf("invalid URL encoding: %v", err)
		}
		return fmt.Sprintf("URL decoded:\n%s", decoded), nil

	case "json_format", "json_prettify":
		var data interface{}
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			return "", fmt.Errorf("invalid JSON: %v", err)
		}
		formatted, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Formatted JSON:\n%s", string(formatted)), nil

	case "json_minify":
		var data interface{}
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			return "", fmt.Errorf("invalid JSON: %v", err)
		}
		minified, err := json.Marshal(data)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Minified JSON:\n%s", string(minified)), nil

	default:
		return "", fmt.Errorf("unknown operation: %s (use: base64_encode, base64_decode, url_encode, url_decode, json_format, json_minify)", operation)
	}
}

// HandleBookmark manages bookmarks
func HandleBookmark(ctx context.Context, action, bookmarkURL, title, tags, query string) (string, error) {
	switch strings.ToLower(action) {
	case "save":
		if bookmarkURL == "" {
			return "", fmt.Errorf("url required for save")
		}
		if title == "" {
			title = bookmarkURL
		}
		metadata := map[string]interface{}{
			"url":  bookmarkURL,
			"tags": strings.Split(tags, ","),
		}
		metaJSON, _ := json.Marshal(metadata)
		id, err := UpsertKnowledge(ctx, fmt.Sprintf("Bookmark: %s", title), "bookmark", string(metaJSON))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Bookmark saved: %s\nURL: %s\nID: %s", title, bookmarkURL, id), nil

	case "search":
		searchQuery := query
		if searchQuery == "" && tags != "" {
			searchQuery = tags
		}
		if searchQuery == "" {
			return "", fmt.Errorf("query or tags required for search")
		}
		queryVec, err := GenerateEmbedding(ctx, fmt.Sprintf("Bookmark %s", searchQuery))
		if err != nil {
			return "", err
		}
		nodes, err := QuerySimilarNodes(ctx, queryVec, 10)
		if err != nil {
			return "", err
		}
		var bookmarks []string
		for _, node := range nodes {
			if node.NodeType == "bookmark" {
				var meta map[string]interface{}
				if err := json.Unmarshal([]byte(node.Metadata), &meta); err == nil {
					urlStr, _ := meta["url"].(string)
					title := strings.TrimPrefix(node.Content, "Bookmark: ")
					bookmarks = append(bookmarks, fmt.Sprintf("- %s\n  %s", title, urlStr))
				}
			}
		}
		if len(bookmarks) == 0 {
			return fmt.Sprintf("No bookmarks found matching '%s'", searchQuery), nil
		}
		return fmt.Sprintf("Bookmarks matching '%s':\n%s", searchQuery, strings.Join(bookmarks, "\n")), nil

	case "list":
		queryVec, err := GenerateEmbedding(ctx, "Bookmark")
		if err != nil {
			return "", err
		}
		nodes, err := QuerySimilarNodes(ctx, queryVec, 20)
		if err != nil {
			return "", err
		}
		var bookmarks []string
		for _, node := range nodes {
			if node.NodeType == "bookmark" {
				var meta map[string]interface{}
				if err := json.Unmarshal([]byte(node.Metadata), &meta); err == nil {
					urlStr, _ := meta["url"].(string)
					title := strings.TrimPrefix(node.Content, "Bookmark: ")
					bookmarks = append(bookmarks, fmt.Sprintf("- %s\n  %s", title, urlStr))
				}
			}
		}
		if len(bookmarks) == 0 {
			return "No bookmarks saved yet.", nil
		}
		return fmt.Sprintf("All bookmarks:\n%s", strings.Join(bookmarks, "\n")), nil

	case "delete":
		return "", fmt.Errorf("delete not yet implemented - bookmarks are stored in knowledge graph")

	default:
		return "", fmt.Errorf("unknown action: %s (use: save, search, list)", action)
	}
}

// SearchWikipedia searches Wikipedia and returns article summary
func SearchWikipedia(query string) (string, error) {
	// First, search for the article
	searchURL := fmt.Sprintf("https://en.wikipedia.org/api/rest_v1/page/summary/%s", url.PathEscape(query))

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	// Wikipedia requires a User-Agent header
	req.Header.Set("User-Agent", "JotPersonalAssistant/1.0 (https://github.com/jackstrohm/jot)")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// If not found directly, try search API
	if resp.StatusCode == 404 {
		return SearchWikipediaFallback(query)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Wikipedia API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var article struct {
		Title       string `json:"title"`
		Extract     string `json:"extract"`
		Description string `json:"description"`
		ContentURLs struct {
			Desktop struct {
				Page string `json:"page"`
			} `json:"desktop"`
		} `json:"content_urls"`
	}

	if err := json.Unmarshal(body, &article); err != nil {
		return "", fmt.Errorf("failed to parse Wikipedia response: %v", err)
	}

	if article.Extract == "" {
		return fmt.Sprintf("No Wikipedia article found for '%s'", query), nil
	}

	var result []string
	result = append(result, fmt.Sprintf("**%s**", article.Title))
	if article.Description != "" {
		result = append(result, fmt.Sprintf("_%s_", article.Description))
	}
	result = append(result, "")
	result = append(result, article.Extract)
	if article.ContentURLs.Desktop.Page != "" {
		result = append(result, "")
		result = append(result, fmt.Sprintf("Read more: %s", article.ContentURLs.Desktop.Page))
	}

	return strings.Join(result, "\n"), nil
}

// SearchWikipediaFallback uses the search API when direct lookup fails
func SearchWikipediaFallback(query string) (string, error) {
	searchURL := fmt.Sprintf("https://en.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json&srlimit=1", url.QueryEscape(query))

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "JotPersonalAssistant/1.0 (https://github.com/jackstrohm/jot)")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var searchResult struct {
		Query struct {
			Search []struct {
				Title   string `json:"title"`
				Snippet string `json:"snippet"`
			} `json:"search"`
		} `json:"query"`
	}

	if err := json.Unmarshal(body, &searchResult); err != nil {
		return "", fmt.Errorf("failed to parse search response: %v", err)
	}

	if len(searchResult.Query.Search) == 0 {
		return fmt.Sprintf("No Wikipedia articles found for '%s'", query), nil
	}

	// Get the summary for the first result
	title := searchResult.Query.Search[0].Title
	return SearchWikipedia(title)
}

// WebSearch performs a web search using Google News RSS feed
func WebSearch(query string, numResults int) (string, error) {
	// Use Google News RSS feed - publicly accessible, no API key required
	searchURL := fmt.Sprintf("https://news.google.com/rss/search?q=%s&hl=en-US&gl=US&ceid=US:en", url.QueryEscape(query))

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "JotPersonalAssistant/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("search returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Parse RSS XML
	type RSSItem struct {
		Title   string `xml:"title"`
		Link    string `xml:"link"`
		PubDate string `xml:"pubDate"`
		Source  string `xml:"source"`
	}
	type RSSChannel struct {
		Items []RSSItem `xml:"item"`
	}
	type RSS struct {
		Channel RSSChannel `xml:"channel"`
	}

	var rss RSS
	if err := xml.Unmarshal(body, &rss); err != nil {
		return "", fmt.Errorf("failed to parse RSS: %v", err)
	}

	if len(rss.Channel.Items) == 0 {
		return fmt.Sprintf("No news results found for '%s'", query), nil
	}

	var resultLines []string
	resultLines = append(resultLines, fmt.Sprintf("Recent news for '%s':\n", query))

	count := numResults
	if count > len(rss.Channel.Items) {
		count = len(rss.Channel.Items)
	}

	for i := 0; i < count; i++ {
		item := rss.Channel.Items[i]
		// Clean up the title (often has " - Source" at the end)
		title := item.Title
		// Parse and format the date
		pubDate := item.PubDate
		if t, err := time.Parse(time.RFC1123, pubDate); err == nil {
			pubDate = t.Format("Jan 2, 2006")
		} else if t, err := time.Parse(time.RFC1123Z, pubDate); err == nil {
			pubDate = t.Format("Jan 2, 2006")
		}

		resultLines = append(resultLines, fmt.Sprintf("%d. %s\n   Date: %s\n   %s\n", i+1, title, pubDate, item.Link))
	}

	return strings.Join(resultLines, "\n"), nil
}

// SanitizePrompt ensures a string is valid UTF-8 and removes any partial multi-byte characters.
// Use before sending text to Gemini; Protobuf rejects invalid UTF-8.
// This is for encoding only and does NOT protect against prompt injection; mitigation is
// handled by WrapAsUserData and system-prompt instructions at prompt construction sites.
func SanitizePrompt(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "")
}

// UserDataDelimOpen and UserDataDelimClose wrap user- or external-origin content in prompts
// so the model treats it as data only, not instructions. Used with system-prompt instructions.
const (
	UserDataDelimOpen  = "<user_data>"
	UserDataDelimClose = "</user_data>"
)

// WrapAsUserData wraps s in the standard user-data delimiters for prompt-injection mitigation.
// Use when embedding user- or external-origin content (entries, queries, logs, tool results text)
// into prompt text sent to the LLM. Do not use for storage or non-LLM output.
func WrapAsUserData(s string) string {
	if s == "" {
		return UserDataDelimOpen + UserDataDelimClose
	}
	return UserDataDelimOpen + "\n" + s + "\n" + UserDataDelimClose
}

// SafeTruncate slices a string based on rune count, not byte count, to prevent splitting emojis.
func SafeTruncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}
