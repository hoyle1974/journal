// Package tools: reflection-based schema generation from Go structs.
package tools

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"google.golang.org/genai"
)

// StructToGenaiSchema builds a genai.Schema (type object) from a pointer to a struct.
// Uses struct tags: json (name), description, required:"true", enum:"a,b,c".
// Min/max are not supported by genai.Schema; document them in description and enforce in Execute.
func StructToGenaiSchema(ptr any) *genai.Schema {
	t := reflect.TypeOf(ptr)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{}}
	}
	properties := make(map[string]*genai.Schema)
	var required []string
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		name := strings.Split(jsonTag, ",")[0]
		desc := field.Tag.Get("description")
		enumTag := field.Tag.Get("enum")
		if enumTag != "" {
			parts := strings.Split(enumTag, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			properties[name] = &genai.Schema{
				Type:        genai.TypeString,
				Description: desc,
				Enum:        parts,
			}
		} else {
			properties[name] = &genai.Schema{
				Type:        mapGoTypeToGenai(field.Type),
				Description: desc,
			}
		}
		if field.Tag.Get("required") == "true" {
			required = append(required, name)
		}
	}
	return &genai.Schema{
		Type:       genai.TypeObject,
		Properties: properties,
		Required:   required,
	}
}

func mapGoTypeToGenai(t reflect.Type) genai.Type {
	switch t.Kind() {
	case reflect.String:
		return genai.TypeString
	case reflect.Bool:
		return genai.TypeBoolean
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return genai.TypeInteger
	case reflect.Float32, reflect.Float64:
		return genai.TypeNumber
	case reflect.Slice, reflect.Array:
		return genai.TypeArray
	case reflect.Map, reflect.Struct:
		return genai.TypeObject
	default:
		panic(fmt.Sprintf("unsupported type %v for schema generation", t.Kind()))
	}
}

// MapToTypedArgs unmarshals the LLM arguments map into a new instance of the same type as tool.Args.
// tool.Args must be a pointer to a struct (e.g. &WeatherArgs{}). Returns the typed struct pointer.
func MapToTypedArgs(tool *Tool, arguments map[string]interface{}) (any, error) {
	if tool.Args == nil {
		return nil, nil
	}
	t := reflect.TypeOf(tool.Args)
	if t.Kind() != reflect.Ptr || t.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("tool.Args must be pointer to struct, got %v", t)
	}
	// Round-trip through JSON so float64 from LLM maps to int etc.
	data, err := json.Marshal(arguments)
	if err != nil {
		return nil, fmt.Errorf("marshal arguments: %w", err)
	}
	v := reflect.New(t.Elem())
	ptr := v.Interface()
	if err := json.Unmarshal(data, ptr); err != nil {
		return nil, fmt.Errorf("unmarshal into %s: %w", t.Elem().Name(), err)
	}
	return ptr, nil
}

// ParamInfo describes a single parameter for discovery/formatting.
type ParamInfo struct {
	Name        string
	Required    bool
	Description string
}

// ParamInfosFromArgs returns param name, required, and description from a pointer-to-struct.
func ParamInfosFromArgs(ptr any) []ParamInfo {
	t := reflect.TypeOf(ptr)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []ParamInfo
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		name := strings.Split(jsonTag, ",")[0]
		out = append(out, ParamInfo{
			Name:        name,
			Required:    field.Tag.Get("required") == "true",
			Description: field.Tag.Get("description"),
		})
	}
	return out
}

// ParamNamesFromArgs returns JSON param names in order (for ToolSummary.ParamNames).
// Optional params get "?" suffix for display.
func ParamNamesFromArgs(ptr any) []string {
	infos := ParamInfosFromArgs(ptr)
	names := make([]string, 0, len(infos))
	for _, p := range infos {
		s := p.Name
		if !p.Required {
			s += "?"
		}
		names = append(names, s)
	}
	return names
}

// ApplyDefaults sets default values on the struct from the "default" tag.
// Only supports string and int defaults; call after unmarshaling.
func ApplyDefaults(ptr any) {
	v := reflect.ValueOf(ptr)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		defTag := field.Tag.Get("default")
		if defTag == "" {
			continue
		}
		fv := v.Field(i)
		if !fv.CanSet() {
			continue
		}
		switch fv.Kind() {
		case reflect.String:
			if fv.String() == "" {
				fv.SetString(defTag)
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if fv.Int() == 0 {
				n, err := strconv.ParseInt(defTag, 10, 64)
				if err == nil {
					fv.SetInt(n)
				}
			}
		case reflect.Bool:
			if b, err := strconv.ParseBool(defTag); err == nil {
				fv.SetBool(b)
			}
		}
	}
}
