package tools

import "google.golang.org/genai"

// CountParam returns a standard "count" parameter (default 10, max 50).
func CountParam() Param {
	min, max := 1, 50
	return Param{
		Name:        "count",
		Description: "Number of items to retrieve (default 10, max 50)",
		Type:        genai.TypeInteger,
		Required:    false,
		Default:     10,
		Min:         &min,
		Max:         &max,
	}
}

// LimitParam returns a "limit" parameter with custom default and max.
func LimitParam(def, max int) Param {
	min := 1
	return Param{
		Name:        "limit",
		Description: "Maximum number of results to return",
		Type:        genai.TypeInteger,
		Required:    false,
		Default:     def,
		Min:         &min,
		Max:         &max,
	}
}

// RequiredStringParam returns a required string parameter.
func RequiredStringParam(name, description string) Param {
	return Param{
		Name:        name,
		Description: description,
		Type:        genai.TypeString,
		Required:    true,
	}
}

// OptionalStringParam returns an optional string parameter.
func OptionalStringParam(name, description string) Param {
	return Param{
		Name:        name,
		Description: description,
		Type:        genai.TypeString,
		Required:    false,
	}
}

// DateRangeParams returns start_date and end_date parameters.
func DateRangeParams(required bool) []Param {
	return []Param{
		{
			Name:        "start_date",
			Description: "Start date in YYYY-MM-DD format",
			Type:        genai.TypeString,
			Required:    required,
		},
		{
			Name:        "end_date",
			Description: "End date in YYYY-MM-DD format",
			Type:        genai.TypeString,
			Required:    required,
		},
	}
}

// EnumParam returns a string parameter with enumerated values.
func EnumParam(name, description string, required bool, values []string) Param {
	return Param{
		Name:        name,
		Description: description,
		Type:        genai.TypeString,
		Required:    required,
		Enum:        values,
	}
}

// IntParam returns an integer parameter.
func IntParam(name, description string, required bool, def int) Param {
	return Param{
		Name:        name,
		Description: description,
		Type:        genai.TypeInteger,
		Required:    required,
		Default:     def,
	}
}

// BoolParam returns a boolean parameter.
func BoolParam(name, description string, def bool) Param {
	return Param{
		Name:        name,
		Description: description,
		Type:        genai.TypeBoolean,
		Required:    false,
		Default:     def,
	}
}

// NumberParam returns a number (float64) parameter.
func NumberParam(name, description string, required bool) Param {
	return Param{
		Name:        name,
		Description: description,
		Type:        genai.TypeNumber,
		Required:    required,
	}
}
