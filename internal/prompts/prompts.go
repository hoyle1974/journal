// Package prompts provides static prompt text loaded from embedded files via go:embed.
// Large prompt blocks live in .txt files and are loaded at init for use by the jot agent.
package prompts

import (
	_ "embed"
	"fmt"
	"sync"
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

var (
	specialistMap   map[string]string
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

// SystemPromptTemplate returns the main FOH system prompt template with 12 %s placeholders (delimOpen, delimClose, today, currentWeek, lastWeekStr, currentMonth, activeContextsStr, recentContext, recentConversation, proactiveSignals, knowledgeGapBlock, sourceCodeBlock).
func SystemPromptTemplate() string { return systemPromptTxt }

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

// ContextAnalyzeTemplate returns the context-analysis prompt template with one %s (entry content).
func ContextAnalyzeTemplate() string { return contextAnalyzeTxt }

// JournalAnalyzeTemplate returns the journal-analysis prompt template with three %s: entryID, date, entryText.
func JournalAnalyzeTemplate() string { return journalAnalyzeTxt }

// ReflectionCheckTemplate returns the reflection-check prompt template with two %s (answer, semanticMemory).
func ReflectionCheckTemplate() string { return reflectionCheckTxt }

// KnowledgeGapTemplate returns the knowledge-gap block template with one %s (gap list).
func KnowledgeGapTemplate() string { return knowledgeGapTxt }

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

// FormatContextAnalyze formats the context-analyze template with the given entry content.
func FormatContextAnalyze(entryContent string) string {
	return fmt.Sprintf(ContextAnalyzeTemplate(), entryContent)
}

// FormatJournalAnalyze formats the journal-analyze template with entry ID, date, and entry text (content).
func FormatJournalAnalyze(entryID, date, entryText string) string {
	return fmt.Sprintf(JournalAnalyzeTemplate(), entryID, date, entryText)
}

// FormatReflectionCheck formats the reflection-check template with answer and semantic memory.
func FormatReflectionCheck(answer, semanticMemory string) string {
	return fmt.Sprintf(ReflectionCheckTemplate(), answer, semanticMemory)
}

// FormatKnowledgeGap formats the knowledge-gap block with the given gap list content.
func FormatKnowledgeGap(gapListContent string) string {
	return fmt.Sprintf(KnowledgeGapTemplate(), gapListContent)
}

// DreamStoryTemplate returns the dream narrative (morning readout) system prompt.
func DreamStoryTemplate() string { return dreamStoryTxt }

// GapDetectorTemplate returns the gap-detector prompt template with two %s placeholders: recent journal, relevant knowledge.
func GapDetectorTemplate() string { return gapDetectorTxt }

// FormatGapDetector formats the gap-detector template with journal and knowledge text.
func FormatGapDetector(recentJournal, relevantKnowledge string) string {
	return fmt.Sprintf(GapDetectorTemplate(), recentJournal, relevantKnowledge)
}

// RollUpTemplate returns the roll-up prompt template with two %s: period label, journal analyses text.
func RollUpTemplate() string { return rollUpTxt }

// FormatRollUp formats the roll-up template with period and analyses text.
func FormatRollUp(periodLabel, analysesText string) string {
	return fmt.Sprintf(RollUpTemplate(), periodLabel, analysesText)
}

// ActivityHistoryTemplate returns the activity-history summarization prompt template with three %s: topic, timeframe, entriesText.
func ActivityHistoryTemplate() string { return activityHistoryTxt }

// FormatActivityHistory formats the activity-history template with topic, timeframe, and entries text.
func FormatActivityHistory(topic, timeframe, entriesText string) string {
	return fmt.Sprintf(ActivityHistoryTemplate(), topic, timeframe, entriesText)
}
