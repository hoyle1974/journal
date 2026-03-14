Brief: Refactor Prompts to Strongly-Typed text/template
Date: 20260314
Status: done
Branch: feature/template-prompts (merged to main)
Worktree: removed

Goal
Eliminate all fragile %s placeholders and fmt.Sprintf calls in the internal/prompts package. Migrate all parameterized prompts to Go's text/template package using strongly-typed data structs. This provides compile-time safety for prompt dependencies, makes the .txt files self-documenting, and minimizes the exported API surface of the prompts package.

Scope
In:

internal/prompts/prompts.go: Replace FormatX string formatting functions with BuildX template execution functions.

internal/prompts/*.txt: Replace all %s placeholders with named template tags (e.g., {{.EntryText}}).

pkg/agent/*.go (specifically foh.go and query_agent.go): Update calls to pass the new strongly-typed structs.

Out:

Changes to the underlying LLM instructions or prompt engineering logic.

Prompts that do not currently take parameters (e.g., Evaluator, PlanSystem, Specialist). These will remain simple functions that return strings.

Approach & Key Decisions
1. Template Initialization (Internal)

All parameterized templates will be parsed at init() using template.Must. This ensures any syntax errors in the .txt files crash the app immediately on boot rather than failing silently at runtime.

Go
// internal/prompts/prompts.go
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
2. Exported Data Structs & Build Functions

Define a strict struct for every prompt that requires injection.

System Prompt:

Go
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

func BuildSystemPrompt(data SystemPromptData) (string, error) {
    var buf bytes.Buffer
    if err := systemPromptTmpl.Execute(&buf, data); err != nil {
        return "", fmt.Errorf("execute system prompt: %w", err)
    }
    return buf.String(), nil
}
(Cursor: Implement similar ...Data structs and Build... functions for ContextAnalyze, JournalAnalyze, ReflectionCheck, KnowledgeGap, GapDetector, RollUp, and ActivityHistory based on their required parameters).

3. Updating the .txt Files

Every corresponding .txt file must be updated to replace %s with the exact struct field name (e.g., {{.Topic}}).

4. Minimizing Exported Surface Area

Remove all exported functions that return the raw templates containing %s (e.g., SystemPromptTemplate(), ContextAnalyzeTemplate()).

5. Updating the Call Sites (pkg/agent/foh.go / query_agent.go)

Target the files where the FOH agent constructs its context. Replace the massive fmt.Sprintf(prompts.SystemPromptTemplate(), delimOpen, delimClose, ...) call with the strongly-typed struct.

Implementation Blueprint for Agent:

Go
// Inside pkg/agent/foh.go or pkg/agent/query_agent.go where the FOH prompt is assembled

promptData := prompts.SystemPromptData{
    DelimOpen:          "<user_data>",
    DelimClose:         "</user_data>",
    SourceCodeBlock:    prompts.SourceCodeBlock(),
    Today:              todayStr,
    CurrentTime:        timeStr,
    CurrentWeek:        currentWeekStr,
    LastWeek:           lastWeekStr,
    CurrentMonth:       currentMonthStr,
    IdentityBlock:      identityContext,
    ActiveContexts:     activeContexts,
    RecentConversation: recentConvo,
    ProactiveSignals:   signals,
    KnowledgeGapBlock:  knowledgeGaps,
    OpenTodoBlock:      openTodos,
}

systemPrompt, err := prompts.BuildSystemPrompt(promptData)
if err != nil {
    // Log the error and wrap it using %w per our .cursorrules
    return nil, fmt.Errorf("failed to build system prompt: %w", err)
}
(Cursor: Apply this exact pattern to all other agent files calling prompts.Format..., mapping their local string variables directly to the new ...Data struct fields and handling the returned error).

Edge Cases & Pre-Flight Checks
Missing Data Fields: If a struct field is left empty by the caller, text/template will render an empty string. The caller must ensure required data is populated before calling the Build... function.

Double Quotes and Escaping: text/template outputs plain text by default, which is perfect for LLM prompts. Ensure no HTML escaping functions (html/template) are accidentally imported.

Struct Visibility: Ensure all fields inside the ...Data structs are capitalized so the text/template engine can access them.

Affected Areas
[x] Prompts / app_capabilities.txt — All .txt files with %s are being rewritten to {{.Field}}.

[x] Agent / FOH loop — Every call to prompts.Format... in pkg/agent/ needs to be rewritten to build the struct, call prompts.Build..., and handle the returned error.

[x] Tools

[ ] Firestore schema or queries

[ ] API routes or cron jobs

Open Questions
[ ] None.

Checklist
Implementation

[x] Parse all 8 templates at package init via template.Must.

[x] Define the 8 ...Data structs with exported fields.

[x] Implement the 8 Build...(data ...Data) (string, error) functions.

[x] Rewrite all %s placeholders in the 8 target .txt files to {{.FieldName}}.

[x] Remove old Format... and ...Template functions from prompts.go.

[x] Update pkg/agent/foh.go and query_agent.go to construct SystemPromptData and call BuildSystemPrompt.

[x] Update all other callers in pkg/agent/ and internal/tools/ to pass structs and handle execution errors for the remaining prompts.

Verification (Proof of Work)

[x] Compilation: go build ./... passes cleanly (proves all callers were updated and strict types match).

[x] Tests: go test ./... passes.

[x] Lint/Format: Code is formatted and passes go vet.

Wrap-up

[x] Brief status set to done and moved to briefs/done/.

Key Files
briefs/active/20260314_template-prompts.md
internal/prompts/prompts.go
internal/prompts/system_prompt.txt
internal/prompts/context_analyze.txt
internal/prompts/journal_analyze.txt
internal/prompts/reflection_check.txt
internal/prompts/knowledge_gap.txt
internal/prompts/gap_detector.txt
internal/prompts/roll_up.txt
internal/prompts/activity_history.txt
pkg/agent/foh.go
pkg/agent/query_agent.go

Session Log
20260314: Brief created with strict struct definitions, text/template mappings, and exact implementation blueprints for the Agent call sites. Ready for Cursor implementation.
20260314: Implemented refactor in worktree (jot-template-prompts). All 8 templates parsed at init; 8 Data structs and BuildX functions added; .txt files updated to {{.Field}}; Format/Template getters removed; prompter.go uses BuildSystemPrompt with struct; foh.go, dreamer_synthesis.go, rollup.go, pkg/memory/context.go, pkg/journal/analysis.go, internal/tools/impl/journal_tools.go updated to BuildX + error handling. go build ./... and go test ./... and go vet pass.
20260314: Committed on feature/template-prompts, merged to main, worktree removed. Brief closed and moved to briefs/done/.
