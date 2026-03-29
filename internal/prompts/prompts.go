// Package prompts provides static prompt text loaded from embedded files via go:embed.
// Large prompt blocks live in .txt files and are loaded at init for use by the jot agent.
// Parameterized prompts use text/template with strongly typed data structs.
package prompts

import (
	"bytes"
	_ "embed"
	"fmt"
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

//go:embed debug_report_prompt.txt
var debugReportPromptTxt string

//go:embed refinery.txt
var refineryTxt string

//go:embed dreamer.txt
var dreamerTxt string

//go:embed dreamer_selfcheck.txt
var dreamerSelfCheckTxt string

//go:embed morning_briefing.txt
var morningBriefingTxt string

var (
	systemPromptTmpl     = template.Must(template.New("system").Parse(systemPromptTxt))
	activityHistoryTmpl  = template.Must(template.New("activityHistory").Parse(activityHistoryTxt))
	debugReportTmpl      = template.Must(template.New("debugReport").Parse(debugReportPromptTxt))
	refineryTmpl         = template.Must(template.New("refinery").Parse(refineryTxt))
	dreamerTmpl          = template.Must(template.New("dreamer").Parse(dreamerTxt))
	dreamerSelfCheckTmpl = template.Must(template.New("dreamerSelfCheck").Parse(dreamerSelfCheckTxt))
	morningBriefingTmpl  = template.Must(template.New("morningBriefing").Parse(morningBriefingTxt))
)

// SystemPromptData holds all inputs for the main FOH system prompt.
type SystemPromptData struct {
	DelimOpen               string
	DelimClose              string
	SourceCodeBlock         string
	Today                   string
	CurrentTime             string
	CurrentWeek             string
	LastWeek                string
	CurrentMonth            string
	IdentityBlock           string
	RecentConversation      string
	ProactiveSignals        string
	KnowledgeGapBlock       string
	ActiveProjectBlock      string
	LoomContextBlock        string // 2-hop RAG context from refinery; empty string = omit section
	RecentReflectionsBlock  string // Dreamer summary nodes; empty string = omit section
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

// RefineryData holds inputs for synchronous relationship extraction.
// Discovery context has been removed per Project Loom spec — context retrieval
// is now the responsibility of Stage 4 (Response Worker / BuildLoomRAGContext).
type RefineryData struct {
	Entry             string
	AllowedPredicates string
	// OwnerName is the primary name of the user (from the identity_anchor node).
	// Empty when identity has not yet been established.
	OwnerName string
}

// BuildRefinery executes the refinery template.
func BuildRefinery(data RefineryData) (string, error) {
	var buf bytes.Buffer
	if err := refineryTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute refinery: %w", err)
	}
	return buf.String(), nil
}

// DreamerData holds inputs for the Dreamer background synthesis prompt.
type DreamerData struct {
	Today                string
	CurrentTime          string
	EntriesText          string
	OpenTasksText        string
	LoomContextBlock     string // injected RAG context
	RecentQuestionsText  string // recently asked (open) and answered questions
}

// BuildDreamer executes the dreamer prompt template.
func BuildDreamer(data DreamerData) (string, error) {
	var buf bytes.Buffer
	if err := dreamerTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute dreamer: %w", err)
	}
	return buf.String(), nil
}

// DreamerSelfCheckData holds inputs for the self-check prompt.
type DreamerSelfCheckData struct {
	Question     string
	GraphContext string
}

// BuildDreamerSelfCheck executes the self-check prompt template.
func BuildDreamerSelfCheck(data DreamerSelfCheckData) (string, error) {
	var buf bytes.Buffer
	if err := dreamerSelfCheckTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute dreamer self-check: %w", err)
	}
	return buf.String(), nil
}

// MorningBriefingData holds inputs for the Morning Briefing agent prompt.
type MorningBriefingData struct {
	Today        string
	CurrentTime  string
	GravelEntries string // formatted log entries since last briefing
	GoldNodes    string // formatted active goals and projects
}

// BuildMorningBriefing executes the morning briefing prompt template.
func BuildMorningBriefing(data MorningBriefingData) (string, error) {
	var buf bytes.Buffer
	if err := morningBriefingTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute morning briefing: %w", err)
	}
	return buf.String(), nil
}
