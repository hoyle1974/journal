Brief: Codebase Cleanup and Documentation Alignment
Date: 20260312
Status: in-progress
Branch: feature/cleanup-and-alignment
Worktree: ../jot-cleanup-and-alignment

Goal
This brief addresses technical debt, unused utilities, and "source of truth" drift identified during the project analysis. The goal is to prune dead code, synchronize documentation with implementation reality, and enforce consistency across the agentic toolset.

Scope
In:

Removal of dead code in internal/service and internal/api.

Standardization of date-range resolution logic across tools.

Synchronization of blueprint.md and app_capabilities.txt with actual code.

Enforcement of <user_data> wrapping in internal/tools/impl/.

Out:

Refactoring the bridge types in backend.go (kept for decoupling).

Implementing implement "missing" features like delete for bookmarks/countdowns.

Approach & Key Decisions
Pruning: Delete GetAnswer, looksLikeQuestion, and the no-op sanitizeResponseForDoc.

Logic Consolidation: Create a shared helper in internal/tools/impl/helpers.go for resolving date ranges to ensure tools like search_entries and get_queries_by_date behave identically.

Doc Update: Rewrite sections of blueprint.md to remove the "Discovery Room" hallucination and correctly describe the discovery_search (JIT schema) flow.

Prompt Safety: Audit all web category tools to ensure WrapAsUserData is applied before passing user strings to LLM-driven synthesis.

Edge Cases & Pre-Flight Checks
GDoc Sync: Ensure that removing GetAnswer doesn't break the Google Doc sync (it should use the AgentService directly).

Regex Safety: Ensure that standardizing date strings doesn't break the Firestore query format (YYYY-MM-DD vs RFC3339).

Affected Areas
[x] Agent / FOH loop — Update blueprint.md descriptions.

[x] Tools — Standardize date handling and add <user_data> wrapping.

[x] Prompts / app_capabilities.txt — Align tool descriptions with reality.

[ ] Firestore schema or queries — No changes needed.

[ ] New dependencies — No changes needed.

[x] API routes or cron jobs — Prune unused helpers.

Checklist
Implementation

[x] Remove GetAnswer from internal/service/query_agent.go.

[x] Remove looksLikeQuestion from internal/service/query_agent.go.

[x] Remove sanitizeResponseForDoc from internal/api/handler_gdoc.go.

[x] Create resolveToolDateRange in internal/tools/impl/helpers.go.

[x] Update journal_tools.go, query_tools.go, and context_tools.go to use the new helper.

[ ] Wrap user queries in web_tools.go and memory_tools.go with WrapAsUserData(). (Audit: web_tools has no LLM synthesis with user strings; memory_tools get_entity_network and planner already wrap.)

Verification (Proof of Work)

[x] Compilation: go build ./... passes.

[x] Tests: go test ./... passes.

[ ] Manual Smoke Test: Run jot sync to verify GDoc processing still works after GetAnswer removal.

Wrap-up

[x] app_capabilities.txt updated to reflect summarize_daily_activities limitations.

[x] blueprint.md updated to match actual specialist and discovery patterns (no Discovery Room refs; discovery_search already described).

Session Log
Brief created following project analysis.

Identified specific dead code locations: GetAnswer, looksLikeQuestion, sanitizeResponseForDoc.

Outlined standardization plan for date-range parsing.

Session: Removed GetAnswer and looksLikeQuestion from query_agent.go; removed sanitizeResponseForDoc from handler_gdoc.go (GDoc uses s.Agent.RunQuery().Answer directly). Added resolveToolDateRange in helpers.go (wraps utils.ResolveDateRange) and switched journal_tools, query_tools, context_tools to use it. Build and tests pass. Updated app_capabilities.txt with date-range tool limitation note. Blueprint already correct (no Discovery Room; discovery_search described). WrapAsUserData: web_tools has no LLM synthesis; memory_tools/planner already wrap user data.
