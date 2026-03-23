// Package prompts provides static prompt text loaded from embedded files via go:embed.
// Large prompt blocks live in .txt files and are loaded at init for use by the jot agent.
// Parameterized prompts use text/template with strongly typed data structs.
package prompts

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed system_prompt.txt
var systemPromptTxt string

//go:embed source_code.txt
var sourceCodeTxt string

//go:embed data_safety.txt
var dataSafetyTxt string

//go:embed evaluator.txt
var evaluatorTxt string

//go:embed activity_history.txt
var activityHistoryTxt string

//go:embed app_capabilities.txt
var appCapabilitiesTxt string

//go:embed debug_report_prompt.txt
var debugReportPromptTxt string

//go:embed process_entry_report_prompt.txt
var processEntryReportPromptTxt string

//go:embed relationship_extractor.txt
var relationshipExtractorTxt string

//go:embed refinery.txt
var refineryTxt string

var (
	systemPromptTmpl       = template.Must(template.New("system").Parse(systemPromptTxt))
	activityHistoryTmpl    = template.Must(template.New("activityHistory").Parse(activityHistoryTxt))
	debugReportTmpl        = template.Must(template.New("debugReport").Parse(debugReportPromptTxt))
	processEntryReportTmpl = template.Must(template.New("processEntryReport").Funcs(template.FuncMap{
		"join": strings.Join,
	}).Parse(processEntryReportPromptTxt))
	relationshipExtractorTmpl = template.Must(template.New("relationshipExtractor").Parse(relationshipExtractorTxt))
	refineryTmpl              = template.Must(template.New("refinery").Parse(refineryTxt))
)

// SystemPromptData holds all inputs for the main FOH system prompt.
type SystemPromptData struct {
	DelimOpen          string
	DelimClose         string
	SourceCodeBlock    string
	Today              string
	CurrentTime        string
	CurrentWeek        string
	LastWeek           string
	CurrentMonth       string
	IdentityBlock      string
	RecentConversation string
	ProactiveSignals   string
	KnowledgeGapBlock  string
	OpenTodoBlock      string
	ActiveProjectBlock string
}

// BuildSystemPrompt executes the system prompt template with the given data.
func BuildSystemPrompt(data SystemPromptData) (string, error) {
	var buf bytes.Buffer
	if err := systemPromptTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute system prompt: %w", err)
	}
	return buf.String(), nil
}

// ActivityHistoryData holds topic, timeframe, and entries text for activity history summarization.
type ActivityHistoryData struct {
	Topic       string
	Timeframe   string
	EntriesText string
}

// BuildActivityHistory executes the activity-history template.
func BuildActivityHistory(data ActivityHistoryData) (string, error) {
	var buf bytes.Buffer
	if err := activityHistoryTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute activity history: %w", err)
	}
	return buf.String(), nil
}

// SourceCodeBlock returns the static source-code block appended to the system prompt.
func SourceCodeBlock() string { return sourceCodeTxt }

// DataSafety returns the prompt-injection safety suffix appended to many system prompts.
func DataSafety() string { return dataSafetyTxt }

// Evaluator returns the evaluator system prompt (without data safety suffix).
func Evaluator() string { return evaluatorTxt }

// AppCapabilities returns the static, LLM-readable description of Jot's parts (entry points, memory, journal, tools).
// Keep app_capabilities.txt up to date when the codebase changes.
func AppCapabilities() string {
	return appCapabilitiesTxt
}

// DebugReportData holds all inputs for the first-person debug report narrative prompt.
type DebugReportData struct {
	Question         string
	ToolCallsSummary string
	FilteredLogs     string
	Answer           string
}

// BuildDebugReport executes the debug-report prompt template with the given data.
func BuildDebugReport(data DebugReportData) (string, error) {
	var buf bytes.Buffer
	if err := debugReportTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute debug report: %w", err)
	}
	return buf.String(), nil
}

// ProcessEntryReportData holds all inputs for the process-entry narrative prompt.
type ProcessEntryReportData struct {
	Content        string
	Source         string
	Significance   float64
	Domain         string
	FactStored     string
	TaskCreated    string
	ContextsLinked int
	Mood           string
	Tags           []string
	EntityNames    []string
}

// BuildProcessEntryReport executes the process-entry report template with the given data.
func BuildProcessEntryReport(data ProcessEntryReportData) (string, error) {
	var buf bytes.Buffer
	if err := processEntryReportTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("process entry report template: %w", err)
	}
	return buf.String(), nil
}

// RelationshipExtractorData holds the entry content for SPO relationship extraction.
type RelationshipExtractorData struct {
	Content string
}

// BuildRelationshipExtractor executes the relationship-extractor template.
func BuildRelationshipExtractor(data RelationshipExtractorData) (string, error) {
	var buf bytes.Buffer
	if err := relationshipExtractorTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute relationship extractor: %w", err)
	}
	return buf.String(), nil
}

// RefineryData holds inputs for synchronous relationship extraction.
type RefineryData struct {
	Discovery string
	Entry     string
}

// BuildRefinery executes the refinery template.
func BuildRefinery(data RefineryData) (string, error) {
	var buf bytes.Buffer
	if err := refineryTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute refinery: %w", err)
	}
	return buf.String(), nil
}
