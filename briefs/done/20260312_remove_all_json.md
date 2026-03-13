Brief: Total Migration from JSON to Simple K/V Line Formats
Date: 20260312
Status: done
Branch: feature/remove-json (merged to main)
Worktree: removed

Goal
Standardize all LLM communication to use the "Simplest Output Available." This project will eliminate the use of JSON as an output format from all LLM prompts and agentic loops, replacing it with flat, newline-delimited Key/Value (K/V) or Pipe-Separated Value (PSV) formats. This migration minimizes token overhead, eliminates JSON-specific parsing failures (like missing braces), and aligns the implementation with the project's existing prompt engineering style.

Scope
In:

Core Parser: Creation of a centralized, resilient K/V line parser in pkg/utils/kvparse.go.

Tool Calling (FOH): Overhauling the Front-of-House ReAct loop (pkg/agent/foh.go) to parse tool calls in K/V format.

The Planner: Re-engineering goal decomposition (pkg/agent/planner.go) to use structured "PHASE" blocks instead of JSON arrays.

Prompt Auditing: Reviewing all 20+ .txt files in internal/prompts/ to explicitly forbid JSON and markdown fences.

Code Pruning: Deleting the llmjson/ package and removing json.Unmarshal from all LLM-to-Struct code paths.

Documentation: Updating .cursorrules and blueprint.md to establish K/V as the permanent architectural standard.

Out:

Internal API Serialization: Machine-to-machine JSON (CLI to Server, Server to Firestore) remains unchanged.

User Display: No changes to how data is formatted for the terminal user interface.

Approach & Key Decisions
1. Unified Utility: pkg/utils/kvparse.go

Instead of bespoke string splitting, we will use a generic ParseKV(text string, target any) utility.

Type Conversion: Use reflection to map key: value strings to Go struct fields (strings, ints, floats, and bools).

Boolean Resilience: Map "true", "yes", "on", "1" to true.

PSV Lists: Support headers followed by pipe-delimited lines (e.g., issues: \n gap | question | context) to populate slices of structs.

2. New Tool Invocation Format

We move away from {"tool": "name", "args": {...}} to a simpler multi-line block:

Plaintext
TOOL: name
ARGS:
param_name | value_string
The FOH loop will use regex or the new kvparse to extract these in parallel loops.

3. Planner "Record Delimiters"

Since the Planner requires a list of complex objects, we will use a specific record header:

Plaintext
PHASE: Phase Title
DESCRIPTION: Detailed description here.
DEPENDENCIES: Title 1, Title 2
---
PHASE: Next Phase
...
4. Mandatory Prompt Headers

Every prompt will now start with a "Format Guard":

OUTPUT FORMAT: Output ONLY structured key/value lines. NO JSON. NO MARKDOWN CODE FENCES. Use the provided keys exactly.

Edge Cases & Pre-Flight Checks
Pipes in Content: PSV parsing must be cautious of | characters inside user-generated content. We will use strings.SplitN(line, "|", count) based on expected field counts.

Metadata Field: Some tools (like upsert_knowledge) take a metadata string. We will treat this as a raw string; if the model provides K/V for metadata, it will be stored as-is without internal JSON nesting.

Empty Blocks: Ensure the parser doesn't crash if a model outputs a header like entities: but provides no lines under it.

Affected Areas
[x] Agent / FOH loop — Switch pkg/agent/foh.go from llmjson to kvparse.

[x] Planner — Complete overhaul of pkg/agent/planner.go and plan_system.txt.

[x] Tools — Update bootstrap_tools.go instructions and discovery_search output.

[x] Prompts — Audit and update all 20+ files in internal/prompts/.

[ ] Firestore schema — No change.

[x] Documentation — Update .cursorrules and blueprint.md.

[x] Package cleanup — Delete internal/llmjson/.

Open Questions
[ ] Should we allow the model to use --- as a record separator, or rely solely on repeating keys (like PHASE:)? (Decision: Use repeating keys as the primary indicator).

Checklist
Implementation

[ ] New pkg/utils/kvparse.go passes explicit *infra.App for logging where necessary.

[ ] All logging uses LoggerFrom(ctx).

[ ] Debug logs pass full strings (ensure kvparse logs the raw text it is attempting to parse).

[ ] No JSON parsing in agent/ or service/ for LLM responses.

[ ] No file exceeds 400 lines (watch kvparse.go).

Verification (Proof of Work)

[ ] Compilation: go build ./... passes.

[ ] Tests: go test ./pkg/utils/... (New tests for KV/PSV parsing).

[ ] Tests: go test ./pkg/agent/... (Verify existing logic works with K/V).

[ ] Manual Smoke Test: jot plan "Build a shed" results in correctly parsed and stored phases.

[ ] Manual Smoke Test: jot query "What time is it?" triggers a tool call via K/V.

Wrap-up

[ ] app_capabilities.txt updated if formats are exposed as "how to talk to me".

[ ] blueprint.md updated to reflect the removal of llmjson.

[x] Brief status set to done and moved to briefs/done/.

Key Files
pkg/utils/kvparse.go

pkg/agent/foh.go

pkg/agent/planner.go

internal/prompts/*.txt

.cursorrules

Session Log
Initial Brief Creation: Documented the total shift from JSON to K/V.

Format Specs: Defined "TOOL/ARGS" and "PHASE" block structures.

Pruning Plan: Scheduled llmjson for deletion.

2026-03-12: Implemented K/V tool-call parsing and removed llmjson. tool_call.go now uses utils.ParseKeyValueMap for TOOL/ARGS format (no JSON). Updated bootstrap_tools, prompter, dreamer prompts to require key/value lines; added debug logging at FOH, dreamer, planner for raw text before parse. kvparse: section lines are now collected raw so values may contain ":". Deleted llmjson package; updated .cursorrules, blueprint.md, app_capabilities.txt, briefs/TEMPLATE.md. Added tool_call_test.go (K/V cases) and kvparse test for section-with-colon. go build ./... and tests pass.

2026-03-13: Closeout. Committed feature/remove-json work in worktree; merged feature/remove-json into main. Resolved stash conflict in prompter.go (kept "MUST call discovery_search" wording). Main now has: K/V tool calls, llmjson removed, current time in context, MISSING INFO prompt, compact LLM_CONTEXT_SENT log. Brief moved to done; worktree removed.
