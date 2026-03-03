package tools

// Args wraps tool arguments with type-safe extraction methods.
type Args struct {
	raw map[string]interface{}
}

// NewArgs creates an Args wrapper from a raw argument map.
func NewArgs(raw map[string]interface{}) *Args {
	if raw == nil {
		raw = make(map[string]interface{})
	}
	return &Args{raw: raw}
}

// String returns a string parameter with a default value.
func (a *Args) String(name, def string) string {
	if v, ok := a.raw[name].(string); ok {
		return v
	}
	return def
}

// RequiredString returns a string parameter or an error if missing/empty.
func (a *Args) RequiredString(name string) (string, bool) {
	if v, ok := a.raw[name].(string); ok && v != "" {
		return v, true
	}
	return "", false
}

// OptionalString returns a string pointer (nil if not provided).
func (a *Args) OptionalString(name string) *string {
	if v, ok := a.raw[name].(string); ok {
		return &v
	}
	return nil
}

// OptionalStringNonEmpty returns a string pointer (nil if not provided or empty).
func (a *Args) OptionalStringNonEmpty(name string) *string {
	if v, ok := a.raw[name].(string); ok && v != "" {
		return &v
	}
	return nil
}

// Int returns an integer parameter with a default value.
// JSON numbers from Gemini come as float64, so we convert.
func (a *Args) Int(name string, def int) int {
	if v, ok := a.raw[name].(float64); ok {
		return int(v)
	}
	return def
}

// IntBounded returns an integer parameter clamped to min/max bounds.
func (a *Args) IntBounded(name string, def, min, max int) int {
	v := a.Int(name, def)
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// Float returns a float64 parameter with a default value.
func (a *Args) Float(name string, def float64) float64 {
	if v, ok := a.raw[name].(float64); ok {
		return v
	}
	return def
}

// Bool returns a boolean parameter with a default value.
func (a *Args) Bool(name string, def bool) bool {
	if v, ok := a.raw[name].(bool); ok {
		return v
	}
	return def
}

// Raw returns the underlying map (for debugging or special cases).
func (a *Args) Raw() map[string]interface{} {
	return a.raw
}
