// Package prompts provides static prompt text loaded from embedded files via go:embed.
// Large prompt blocks live in .txt files and are loaded at init for use by the jot agent.
// Parameterized prompts use text/template with strongly-typed data structs.
package prompts

import (
	_ "embed"
	"bytes"
	"fmt"
	"sync"
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

//go:embed router.txt
var routerTxt string

//go:embed plan_system.txt
var planSystemTxt string

//go:embed context_analyze.txt
var contextAnalyzeTxt string

//go:embed journal_analyze.txt
var journalAnalyzeTxt string

//go:embed reflection_check.txt
var reflectionCheckTxt string

//go:embed knowledge_gap.txt
var knowledgeGapTxt string

//go:embed executive_summary.txt
var executiveSummaryTxt string

//go:embed identity_architect.txt
var identityArchitectTxt string

//go:embed specialist_relationship.txt
var specialistRelationshipTxt string

//go:embed specialist_work.txt
var specialistWorkTxt string

//go:embed specialist_task.txt
var specialistTaskTxt string

//go:embed specialist_thought.txt
var specialistThoughtTxt string

//go:embed specialist_selfmodel.txt
var specialistSelfmodelTxt string

//go:embed specialist_evolution.txt
var specialistEvolutionTxt string

//go:embed gap_detector.txt
var gapDetectorTxt string

//go:embed roll_up.txt
var rollUpTxt string

//go:embed activity_history.txt
var activityHistoryTxt string

//go:embed dream_story.txt
var dreamStoryTxt string

//go:embed synthesis_pass.txt
var synthesisPassTxt string

//go:embed app_capabilities.txt
var appCapabilitiesTxt string

var (
	systemPromptTmpl    = template.Must(template.New("system").Parse(systemPromptTxt))
	contextAnalyzeTmpl  = template.Must(template.New("context").Parse(contextAnalyzeTxt))
	journalAnalyzeTmpl  = template.Must(template.New("journal").Parse(journalAnalyzeTxt))
	reflectionCheckTmpl = template.Must(template.New("reflection").Parse(reflectionCheckTxt))
	knowledgeGapTmpl    = template.Must(template.New("knowledgeGap").Parse(knowledgeGapTxt))
	gapDetectorTmpl     = template.Must(template.New("gapDetector").Parse(gapDetectorTxt))
	rollUpTmpl          = template.Must(template.New("rollUp").Parse(rollUpTxt))
	activityHistoryTmpl = template.Must(template.New("activityHistory").Parse(activityHistoryTxt))
)

var (
	specialistMap     map[string]string
	specialistMapOnce sync.Once
)

func initSpecialistMap() {
	specialistMap = map[string]string{
		"relationship": specialistRelationshipTxt,
		"work":         specialistWorkTxt,
		"task":         specialistTaskTxt,
		"thought":      specialistThoughtTxt,
		"selfmodel":    specialistSelfmodelTxt,
		"evolution":    specialistEvolutionTxt,
	}
}

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
	ActiveContexts     string
	RecentConversation string
	ProactiveSignals   string
	KnowledgeGapBlock  string
	OpenTodoBlock      string
}

// BuildSystemPrompt executes the system prompt template with the given data.
func BuildSystemPrompt(data SystemPromptData) (string, error) {
	var buf bytes.Buffer
	if err := systemPromptTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute system prompt: %w", err)
	}
	return buf.String(), nil
}

// ContextAnalyzeData holds the entry content for context analysis.
type ContextAnalyzeData struct {
	EntryContent string
}

// BuildContextAnalyze executes the context-analyze template.
func BuildContextAnalyze(data ContextAnalyzeData) (string, error) {
	var buf bytes.Buffer
	if err := contextAnalyzeTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute context analyze: %w", err)
	}
	return buf.String(), nil
}

// JournalAnalyzeData holds entry ID, date, and text for journal analysis.
type JournalAnalyzeData struct {
	EntryID   string
	Date      string
	EntryText string
}

// BuildJournalAnalyze executes the journal-analyze template.
func BuildJournalAnalyze(data JournalAnalyzeData) (string, error) {
	var buf bytes.Buffer
	if err := journalAnalyzeTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute journal analyze: %w", err)
	}
	return buf.String(), nil
}

// ReflectionCheckData holds answer and semantic memory for reflection check.
type ReflectionCheckData struct {
	Answer         string
	SemanticMemory string
}

// BuildReflectionCheck executes the reflection-check template.
func BuildReflectionCheck(data ReflectionCheckData) (string, error) {
	var buf bytes.Buffer
	if err := reflectionCheckTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute reflection check: %w", err)
	}
	return buf.String(), nil
}

// KnowledgeGapData holds the gap list content for the knowledge-gap block.
type KnowledgeGapData struct {
	GapListContent string
}

// BuildKnowledgeGap executes the knowledge-gap template.
func BuildKnowledgeGap(data KnowledgeGapData) (string, error) {
	var buf bytes.Buffer
	if err := knowledgeGapTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute knowledge gap: %w", err)
	}
	return buf.String(), nil
}

// GapDetectorData holds recent journal, relevant knowledge, and tool manifest for gap detection.
type GapDetectorData struct {
	RecentJournal    string
	RelevantKnowledge string
	ToolManifest     string
}

// BuildGapDetector executes the gap-detector template.
func BuildGapDetector(data GapDetectorData) (string, error) {
	var buf bytes.Buffer
	if err := gapDetectorTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute gap detector: %w", err)
	}
	return buf.String(), nil
}

// RollUpData holds period label and analyses text for roll-up.
type RollUpData struct {
	PeriodLabel  string
	AnalysesText string
}

// BuildRollUp executes the roll-up template.
func BuildRollUp(data RollUpData) (string, error) {
	var buf bytes.Buffer
	if err := rollUpTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute roll up: %w", err)
	}
	return buf.String(), nil
}

// ActivityHistoryData holds topic, timeframe, and entries text for activity history summarization.
type ActivityHistoryData struct {
	Topic      string
	Timeframe  string
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

// Router returns the router/dispatcher system prompt (without data safety suffix).
func Router() string { return routerTxt }

// PlanSystem returns the plan-generation system instruction.
func PlanSystem() string { return planSystemTxt }

// ExecutiveSummary returns the living-context executive summary prompt.
func ExecutiveSummary() string { return executiveSummaryTxt }

// IdentityArchitect returns the identity-architect prompt for profile synthesis.
func IdentityArchitect() string { return identityArchitectTxt }

// Specialist returns the specialist system prompt for the given domain (relationship, work, task, thought, selfmodel, evolution). Empty string if unknown.
func Specialist(domain string) string {
	specialistMapOnce.Do(initSpecialistMap)
	if s, ok := specialistMap[domain]; ok {
		return s
	}
	return ""
}

// DreamStoryTemplate returns the dream narrative (morning readout) system prompt.
func DreamStoryTemplate() string { return dreamStoryTxt }

// SynthesisPass returns the synthesis-pass system prompt (retrieve-and-summarize refinement).
func SynthesisPass() string { return synthesisPassTxt }

// AppCapabilities returns the static, LLM-readable description of Jot's parts (entry points, agents, memory, journal, tools).
// Injected into gap-detection during dreaming so the model understands current capabilities. Keep app_capabilities.txt up to date when the codebase changes.
func AppCapabilities() string {
	return appCapabilitiesTxt
}
