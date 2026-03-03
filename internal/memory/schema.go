// Package memory provides typed, validated metadata schemas for knowledge nodes.
// Context nodes are out of scope (they use jot.ContextMetadata in the main package).
package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Node type constants for use by callers and the registry.
const (
	NodeTypePerson     = "person"
	NodeTypeProject    = "project"
	NodeTypeGoal       = "goal"
	NodeTypePreference = "preference"
	NodeTypeEvent      = "event"
	NodeTypeMilestone  = "milestone"
	NodeTypePlace      = "place"
	NodeTypeAsset      = "asset"
	NodeTypeTool       = "tool"
	NodeTypeGeneric    = "generic"
)

// Project/Goal status values (metadataStatus and existing code expect lowercase).
const (
	StatusActive   = "active"
	StatusBlocked  = "blocked"
	StatusDone     = "done"
	StatusPlanning = "planning"
	StatusPending  = "pending"
	StatusCompleted = "completed"
)

// Preference category and sentiment.
const (
	CategoryFood     = "food"
	CategoryWorkflow = "workflow"
	CategoryTech     = "tech"
	SentimentLike    = "like"
	SentimentDislike = "dislike"
	SentimentRigid   = "rigid"
)

// Event/Milestone type.
const (
	EventTypeCelebration = "celebration"
	EventTypeWork        = "work"
	EventTypeHealth      = "health"
)

// Place category.
const (
	PlaceCategoryHome   = "home"
	PlaceCategoryOffice = "office"
	PlaceCategoryTravel = "travel"
)

// Asset/Tool type.
const (
	AssetTypeSoftware = "software"
	AssetTypeHardware = "hardware"
	AssetTypeAccount  = "account"
)

// PersonMeta is the metadata schema for person nodes.
type PersonMeta struct {
	RelationshipStrength string   `json:"relationship_strength"`
	Occupation           string   `json:"occupation"`
	Birthdate            string   `json:"birthdate"`
	Interests            []string `json:"interests"`
	LastInteraction      string   `json:"last_interaction"`
}

// ProjectGoalMeta is the metadata schema for project/goal nodes.
// Uses "status" and "parent_goal" for compatibility with metadataStatus and GetLinkedCompletedProjectID.
type ProjectGoalMeta struct {
	Status        string `json:"status"`          // active, blocked, done, planning, pending, completed
	Deadline      string `json:"deadline"`
	ParentGoalID  string `json:"parent_goal"`     // canonical key for compatibility
	ArchiveSummary string `json:"archive_summary"`
}

// PreferenceMeta is the metadata schema for preference nodes.
type PreferenceMeta struct {
	Subject   string `json:"subject"`
	Category  string `json:"category"`  // food, workflow, tech
	Sentiment string `json:"sentiment"`  // like, dislike, rigid
}

// EventMilestoneMeta is the metadata schema for event/milestone nodes.
type EventMilestoneMeta struct {
	Date       string   `json:"date"`
	Type       string   `json:"type"`       // celebration, work, health
	Attendees  []string `json:"attendees"`  // UUIDs
}

// PlaceMeta is the metadata schema for place nodes.
type PlaceMeta struct {
	Address  string `json:"address"`
	Category string `json:"category"` // home, office, travel
	Notes    string `json:"notes"`
}

// AssetToolMeta is the metadata schema for asset/tool nodes.
type AssetToolMeta struct {
	Type          string            `json:"type"` // software, hardware, account
	Configuration map[string]any    `json:"configuration"`
	Preferences   map[string]any    `json:"preferences"`
}

// GenericNodeMeta is the fallback schema for uncategorized facts.
type GenericNodeMeta struct {
	SourceExcerpt   string   `json:"source_excerpt"`
	ExtractedFacts  []string `json:"extracted_facts"`
	ConfidenceScore float64  `json:"confidence_score"`
	Tags            []string `json:"tags"`
}

type validatorFunc func(map[string]any) error
type normalizerFunc func(map[string]any) (map[string]any, error)

var registry = map[string]struct {
	validate  validatorFunc
	normalize normalizerFunc
}{
	NodeTypePerson:     {validate: validatePerson, normalize: normalizePerson},
	NodeTypeProject:    {validate: validateProjectGoal, normalize: normalizeProjectGoal},
	NodeTypeGoal:       {validate: validateProjectGoal, normalize: normalizeProjectGoal},
	NodeTypePreference: {validate: validatePreference, normalize: normalizePreference},
	NodeTypeEvent:      {validate: validateEventMilestone, normalize: normalizeEventMilestone},
	NodeTypeMilestone:  {validate: validateEventMilestone, normalize: normalizeEventMilestone},
	NodeTypePlace:      {validate: validatePlace, normalize: normalizePlace},
	NodeTypeAsset:      {validate: validateAssetTool, normalize: normalizeAssetTool},
	NodeTypeTool:       {validate: validateAssetTool, normalize: normalizeAssetTool},
	NodeTypeGeneric:    {validate: validateGeneric, normalize: normalizeGeneric},
}

// IsRegistered returns true if nodeType has a schema in the registry.
func IsRegistered(nodeType string) bool {
	_, ok := registry[nodeType]
	return ok
}

// ValidateMetadata validates m against the schema for nodeType. Returns nil if nodeType is not registered.
func ValidateMetadata(nodeType string, m map[string]any) error {
	if m == nil {
		return errors.New("metadata map is nil")
	}
	entry, ok := registry[nodeType]
	if !ok {
		return nil
	}
	return entry.validate(m)
}

// NormalizeMetadata normalizes m for the given nodeType. Returns the normalized map and nil error.
// If nodeType is not registered, returns m unchanged and nil.
func NormalizeMetadata(nodeType string, m map[string]any) (map[string]any, error) {
	if m == nil {
		return map[string]any{}, nil
	}
	entry, ok := registry[nodeType]
	if !ok {
		return m, nil
	}
	return entry.normalize(m)
}

// MetadataToJSON marshals the normalized map to a JSON string for storage.
func MetadataToJSON(m map[string]any) (string, error) {
	if m == nil {
		return "{}", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal metadata: %w", err)
	}
	return string(b), nil
}

func validatePerson(m map[string]any) error {
	// All fields optional for person
	return nil
}

func normalizePerson(m map[string]any) (map[string]any, error) {
	out := make(map[string]any)
	setString(out, "relationship_strength", getString(m, "relationship_strength"))
	setString(out, "occupation", getString(m, "occupation"))
	setString(out, "birthdate", getString(m, "birthdate"))
	setString(out, "last_interaction", getString(m, "last_interaction"))
	setStringSlice(out, "interests", getStringSlice(m, "interests"))
	return out, nil
}

var projectGoalStatuses = map[string]bool{
	StatusActive: true, StatusBlocked: true, StatusDone: true,
	StatusPlanning: true, StatusPending: true, StatusCompleted: true,
}

func validateProjectGoal(m map[string]any) error {
	s := getString(m, "status")
	if s != "" && !projectGoalStatuses[strings.ToLower(s)] {
		return fmt.Errorf("invalid status %q for project/goal", s)
	}
	return nil
}

func normalizeProjectGoal(m map[string]any) (map[string]any, error) {
	out := make(map[string]any)
	// Accept parent_goal, parent_goal_id, project_id; output both for GetLinkedCompletedProjectID compatibility
	pid := getString(m, "parent_goal")
	if pid == "" {
		pid = getString(m, "parent_goal_id")
	}
	if pid == "" {
		pid = getString(m, "project_id")
	}
	if pid != "" {
		out["parent_goal"] = pid
		out["project_id"] = pid
	}
	// status: normalize to lowercase
	s := getString(m, "status")
	if s != "" {
		out["status"] = strings.ToLower(s)
	}
	setString(out, "deadline", getString(m, "deadline"))
	setString(out, "archive_summary", getString(m, "archive_summary"))
	// Preserve task-specific keys not in schema (step_number, dependencies) for backward compat
	if v, ok := m["step_number"]; ok {
		out["step_number"] = v
	}
	if v, ok := m["dependencies"]; ok {
		out["dependencies"] = v
	}
	return out, nil
}

var preferenceCategories = map[string]bool{CategoryFood: true, CategoryWorkflow: true, CategoryTech: true}
var preferenceSentiments = map[string]bool{SentimentLike: true, SentimentDislike: true, SentimentRigid: true}

func validatePreference(m map[string]any) error {
	c := getString(m, "category")
	if c != "" && !preferenceCategories[strings.ToLower(c)] {
		return fmt.Errorf("invalid category %q for preference", c)
	}
	s := getString(m, "sentiment")
	if s != "" && !preferenceSentiments[strings.ToLower(s)] {
		return fmt.Errorf("invalid sentiment %q for preference", s)
	}
	return nil
}

func normalizePreference(m map[string]any) (map[string]any, error) {
	out := make(map[string]any)
	setString(out, "subject", getString(m, "subject"))
	c := getString(m, "category")
	if c != "" {
		out["category"] = strings.ToLower(c)
	}
	s := getString(m, "sentiment")
	if s != "" {
		out["sentiment"] = strings.ToLower(s)
	}
	return out, nil
}

var eventTypes = map[string]bool{EventTypeCelebration: true, EventTypeWork: true, EventTypeHealth: true}

func validateEventMilestone(m map[string]any) error {
	t := getString(m, "type")
	if t != "" && !eventTypes[strings.ToLower(t)] {
		return fmt.Errorf("invalid type %q for event/milestone", t)
	}
	return nil
}

func normalizeEventMilestone(m map[string]any) (map[string]any, error) {
	out := make(map[string]any)
	setString(out, "date", getString(m, "date"))
	t := getString(m, "type")
	if t != "" {
		out["type"] = strings.ToLower(t)
	}
	setStringSlice(out, "attendees", getStringSlice(m, "attendees"))
	return out, nil
}

var placeCategories = map[string]bool{PlaceCategoryHome: true, PlaceCategoryOffice: true, PlaceCategoryTravel: true}

func validatePlace(m map[string]any) error {
	c := getString(m, "category")
	if c != "" && !placeCategories[strings.ToLower(c)] {
		return fmt.Errorf("invalid category %q for place", c)
	}
	return nil
}

func normalizePlace(m map[string]any) (map[string]any, error) {
	out := make(map[string]any)
	setString(out, "address", getString(m, "address"))
	c := getString(m, "category")
	if c != "" {
		out["category"] = strings.ToLower(c)
	}
	setString(out, "notes", getString(m, "notes"))
	return out, nil
}

var assetTypes = map[string]bool{AssetTypeSoftware: true, AssetTypeHardware: true, AssetTypeAccount: true}

func validateAssetTool(m map[string]any) error {
	t := getString(m, "type")
	if t != "" && !assetTypes[strings.ToLower(t)] {
		return fmt.Errorf("invalid type %q for asset/tool", t)
	}
	return nil
}

func normalizeAssetTool(m map[string]any) (map[string]any, error) {
	out := make(map[string]any)
	t := getString(m, "type")
	if t != "" {
		out["type"] = strings.ToLower(t)
	}
	if v, ok := m["configuration"]; ok && v != nil {
		out["configuration"] = v
	}
	if v, ok := m["preferences"]; ok && v != nil {
		out["preferences"] = v
	}
	return out, nil
}

func validateGeneric(m map[string]any) error {
	return nil
}

func normalizeGeneric(m map[string]any) (map[string]any, error) {
	out := make(map[string]any)
	setString(out, "source_excerpt", getString(m, "source_excerpt"))
	setStringSlice(out, "extracted_facts", getStringSlice(m, "extracted_facts"))
	if v, ok := m["confidence_score"]; ok {
		switch x := v.(type) {
		case float64:
			out["confidence_score"] = x
		case int:
			out["confidence_score"] = float64(x)
		default:
			out["confidence_score"] = 0.0
		}
	} else {
		out["confidence_score"] = 0.0
	}
	setStringSlice(out, "tags", getStringSlice(m, "tags"))
	return out, nil
}

func getString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func setString(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

func getStringSlice(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func setStringSlice(m map[string]any, key string, s []string) {
	if len(s) > 0 {
		m[key] = s
	}
}
