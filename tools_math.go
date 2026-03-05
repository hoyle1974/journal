package jot

import (
	"time"

	"github.com/jackstrohm/jot/pkg/utils"
)

// EvaluateMathExpression evaluates a mathematical expression string. Re-exported from utils.
func EvaluateMathExpression(expr string) (string, error) {
	return utils.EvaluateMathExpression(expr)
}

// EvalSimpleArithmetic evaluates basic arithmetic using govaluate. Re-exported from utils.
func EvalSimpleArithmetic(expr string) (float64, error) {
	return utils.EvalSimpleArithmetic(expr)
}

// PerformDateCalculation handles date arithmetic. Re-exported from utils.
func PerformDateCalculation(operation, date1Str, date2Str string, days int) (string, error) {
	return utils.PerformDateCalculation(operation, date1Str, date2Str, days)
}

// ParseRelativeDate interprets natural language date expressions. Re-exported from utils.
func ParseRelativeDate(input string) (time.Time, time.Time, error) {
	return utils.ParseRelativeDate(input)
}

// ResolveDateRange resolves start_date and end_date to YYYY-MM-DD strings. Re-exported from utils.
func ResolveDateRange(startExpr, endExpr string) (startStr, endStr string, err error) {
	return utils.ResolveDateRange(startExpr, endExpr)
}
