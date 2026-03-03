package tools

import "fmt"

// OK creates a successful result with formatted message.
func OK(format string, args ...interface{}) Result {
	return Result{
		Success: true,
		Result:  fmt.Sprintf(format, args...),
	}
}

// Fail creates a failed result with formatted error message.
func Fail(format string, args ...interface{}) Result {
	return Result{
		Success: false,
		Result:  fmt.Sprintf(format, args...),
	}
}

// MissingParam returns a failure result for missing required parameter.
func MissingParam(name string) Result {
	return Fail("Missing required parameter: %s", name)
}

// MissingParams returns a failure result for missing required parameters.
func MissingParams(names ...string) Result {
	if len(names) == 1 {
		return MissingParam(names[0])
	}
	return Fail("Missing required parameters: %s", names)
}
