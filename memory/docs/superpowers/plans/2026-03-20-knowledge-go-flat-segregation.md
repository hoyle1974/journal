# knowledge.go Flat Segregation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split `knowledge.go` (921 lines, 28 functions) into five single-responsibility files within the same `memory` package, eliminating the junk-drawer without breaking the public API or introducing import cycles.

**Architecture:** Path 1 — Idiomatic Flat Segregation. Everything stays in `package memory`; the `Store` struct and all exported symbols remain unchanged. No subpackages, no circular-dependency risk. Each new file owns one cohesive concern; `knowledge.go` is deleted when empty.

> **File-naming note:** The spec proposed four files (`knowledge_crud.go`, `rag_pipeline.go`, `graph_spo.go`, `knowledge_collision.go`). This plan uses five files and slightly different names. Rationale: (1) `knowledge_collision.go` is merged into `knowledge_upsert.go` because collision detection and upsert are a single inseparable pipeline — splitting them would leave each file with too little context; (2) `rag_pipeline.go` is renamed `knowledge_search.go` to match the module's naming convention (`knowledge_*`); (3) `graph_spo.go` is renamed `knowledge_spo.go` for the same reason; (4) project/goal operations are extracted into their own `knowledge_project.go` to keep `knowledge_crud.go` under 185 lines.

**Tech Stack:** Go 1.21+, Firestore SDK, standard library. No new dependencies.

---

## File Map

| File | Responsibility | Est. lines |
|------|---------------|-----------|
| **Delete:** `knowledge.go` | Junk drawer — all 28 functions migrate out | 921 → 0 |
| **Create:** `knowledge_crud.go` | Package constants, node structs, point-reads, entity-link helpers, diagnostic list | ~185 |
| **Create:** `knowledge_upsert.go` | Collision detection (LLM), all Upsert* variants, dedup pipeline | ~235 |
| **Create:** `knowledge_search.go` | Vector KNN search, keyword scan, entity/project name finders, related-node discovery | ~220 |
| **Create:** `knowledge_spo.go` | SPO outgoing-edge queries, incoming entity-link queries | ~90 |
| **Create:** `knowledge_project.go` | Project/goal status updates, archive-summary append, completed-project lookup | ~125 |
| **Modify:** `blueprint.md` | Replace File Map entry for `knowledge.go` with the five new files | — |

### Precise function-to-file assignment

**`knowledge_crud.go`**
- Package header, imports, constants (`KnowledgeCollection`, `EntriesCollection`, `QueriesCollection`, `TasksCollection`)
- `KnowledgeNode`, `KnowledgeNodeWithLinks` structs
- `truncateForLog` helper
- `FindNearestWithThreshold`
- `AppendJournalEntryIDsToNode`, `AddEntityLink`
- `GetKnowledgeNodeByID`, `GetKnowledgeNodesByIDs`
- `GetUserIdentityNodes`, `GetActiveSignals`
- `ListKnowledgeNodes`

**`knowledge_upsert.go`**
- `factCollisionSystemPrompt` const, `evaluateFactCollision`
- `SPOExtra` struct
- `UpsertKnowledge`
- `UpsertSemanticMemory`, `UpsertSemanticMemoryPreembedded`, `UpsertSemanticMemoryPreembeddedWithSPO`
- `upsertSemanticMemoryWithVector` (private)

**`knowledge_search.go`**
- `QuerySimilarNodes`, `QuerySimilarSemanticNodes`
- `SearchKnowledgeNodes`
- `FindEntityNodeByName`, `FindProjectOrGoalByName`
- `DiscoverRelatedNodes`

**`knowledge_spo.go`**
- `QueryNodesLinkingTo`
- `QueryOutgoingEdges`

**`knowledge_project.go`**
- `metadataStatus` (private helper)
- `UpdateProjectStatus`
- `AppendToProjectArchiveSummary`
- `GetLinkedCompletedProjectID`, `isCompletedProjectByID` (private)

---

## Task 1: Baseline — confirm tests pass before touching anything

**Files:** none modified yet

- [ ] **Step 1: Run the full test suite**

```bash
cd /Users/jstrohm/code/memory
go test ./... -count=1 2>&1 | tail -30
```

Expected: all tests pass (or a known pre-existing failure list you can compare against later). Record any failures — they are *not* yours to fix in this plan.

- [ ] **Step 2: Record the build fingerprint**

```bash
go build ./...
echo "build ok"
```

Expected: no errors. If there are errors, stop and resolve them before proceeding.

---

## Task 2: Extract `knowledge_crud.go`

**Files:**
- Create: `knowledge_crud.go`
- Modify: `knowledge.go` (delete migrated lines)

The CRUD file gets: package header, imports, all four collection constants, both node structs, `truncateForLog`, and the seven read/helper functions.

- [ ] **Step 1: Create `knowledge_crud.go`**

Copy the following sections verbatim from `knowledge.go` into the new file. Do not paraphrase — copy exact code.

Sections to copy (by approximate line range):
- Lines 1–53: package declaration, imports block, four `const` collection declarations, `KnowledgeNode` struct, `KnowledgeNodeWithLinks` struct, `truncateForLog`
- Lines 283–303: `FindNearestWithThreshold`
- Lines 306–354: `AppendJournalEntryIDsToNode`, `AddEntityLink`
- Lines 527–579: `GetKnowledgeNodeByID`, `GetKnowledgeNodesByIDs`
- Lines 781–832: `GetUserIdentityNodes`, `GetActiveSignals`
- Lines 883–921: `ListKnowledgeNodes`

The imports needed in `knowledge_crud.go` are:
```go
import (
    "context"
    "fmt"
    "strings"
    "time"

    "cloud.google.com/go/firestore"
    "google.golang.org/api/iterator"
)
```

Remove any imports that aren't actually used by the functions in this file (e.g., `encoding/json` is not needed here).

- [ ] **Step 2: Delete the migrated sections from `knowledge.go`**

Remove lines 1–53, 283–354, 527–579, 781–832, 883–921 from `knowledge.go`. The remaining content of `knowledge.go` must keep its own `package memory` declaration at the top and update its `import` block to only include what the remaining functions actually use.

- [ ] **Step 3: Verify compilation**

```bash
go build ./...
```

Expected: no errors. If you see "undefined: X" errors, the symbol X was left in `knowledge.go`'s import block but its definition moved to `knowledge_crud.go` — this is a false alarm. The package compiles as a unit. If you see "declared and not used" errors, trim the import block in whichever file is complaining.

- [ ] **Step 4: Run tests**

```bash
go test ./... -count=1
```

Expected: same result as Task 1 baseline.

- [ ] **Step 5: Commit**

```bash
git add knowledge_crud.go knowledge.go
git commit -m "refactor: extract knowledge_crud.go from knowledge.go

Point-read helpers, node structs, collection constants, and
diagnostic list move to single-responsibility file.
knowledge.go shrinks by ~185 lines."
```

---

## Task 3: Extract `knowledge_upsert.go`

**Files:**
- Create: `knowledge_upsert.go`
- Modify: `knowledge.go`

The upsert file owns the full write pipeline: collision detection, all public Upsert* variants, and the private inner function.

- [ ] **Step 1: Create `knowledge_upsert.go`**

Copy these sections from `knowledge.go`:
- Lines containing `factCollisionSystemPrompt` const and `evaluateFactCollision` func (around original lines 62–80)
- `SPOExtra` struct (around original lines 184–188)
- `UpsertKnowledge` (around original lines 83–182)
- `UpsertSemanticMemory`, `UpsertSemanticMemoryPreembedded`, `UpsertSemanticMemoryPreembeddedWithSPO`, `upsertSemanticMemoryWithVector` (around original lines 191–280)

File header:
```go
// Package memory — upsert pipeline with LLM-driven fact-collision detection.
package memory

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"
    "time"

    "cloud.google.com/go/firestore"
)
```

Note: `encoding/json` is required — `UpsertKnowledge` calls `json.Unmarshal` when parsing the incoming `metadata` string before validation.

- [ ] **Step 2: Delete migrated sections from `knowledge.go`**

Remove the `factCollisionSystemPrompt`, `evaluateFactCollision`, `SPOExtra`, all four Upsert* functions from `knowledge.go`. Update `knowledge.go`'s import block.

- [ ] **Step 3: Verify compilation**

```bash
go build ./...
```

- [ ] **Step 4: Run tests**

```bash
go test ./... -count=1
```

- [ ] **Step 5: Commit**

```bash
git add knowledge_upsert.go knowledge.go
git commit -m "refactor: extract knowledge_upsert.go from knowledge.go

Collision detection and all Upsert* variants move to
single-responsibility file. knowledge.go shrinks by ~235 lines."
```

---

## Task 4: Extract `knowledge_search.go`

**Files:**
- Create: `knowledge_search.go`
- Modify: `knowledge.go`

The search file owns every function that retrieves a set of nodes by query — vector KNN, keyword scan, and semantic name-lookup helpers.

- [ ] **Step 1: Create `knowledge_search.go`**

Copy these sections from `knowledge.go`:
- `QuerySimilarNodes` (around original lines 357–419)
- `QuerySimilarSemanticNodes` (around original lines 423–482)
- `SearchKnowledgeNodes` (around original lines 485–524)
- `FindEntityNodeByName` (around original lines 582–608)
- `FindProjectOrGoalByName` (around original lines 611–626)
- `DiscoverRelatedNodes` (around original lines 670–692)

File header:
```go
// Package memory — semantic vector and keyword search over knowledge nodes.
package memory

import (
    "context"
    "fmt"
    "strings"

    "cloud.google.com/go/firestore"
    "google.golang.org/api/iterator"
)
```

Note: The score arithmetic in `QuerySimilarNodes`/`QuerySimilarSemanticNodes` (computing `1 - distance`, clamping to `[0, 1]`) uses only basic arithmetic operators — no `math` package is required.

- [ ] **Step 2: Delete migrated sections from `knowledge.go`**

- [ ] **Step 3: Verify compilation**

```bash
go build ./...
```

- [ ] **Step 4: Run tests**

```bash
go test ./... -count=1
```

- [ ] **Step 5: Commit**

```bash
git add knowledge_search.go knowledge.go
git commit -m "refactor: extract knowledge_search.go from knowledge.go

Vector KNN, keyword scan, and entity name-finder helpers move to
single-responsibility file. knowledge.go shrinks by ~220 lines."
```

---

## Task 5: Extract `knowledge_spo.go`

**Files:**
- Create: `knowledge_spo.go`
- Modify: `knowledge.go`

The SPO file owns the two Firestore queries for graph-edge traversal.

- [ ] **Step 1: Create `knowledge_spo.go`**

Copy these sections from `knowledge.go`:
- `QueryNodesLinkingTo` (around original lines 836–856)
- `QueryOutgoingEdges` (around original lines 860–880)

File header:
```go
// Package memory — SPO edge queries for graph traversal (incoming and outgoing).
package memory

import (
    "context"

    "cloud.google.com/go/firestore"
)
```

- [ ] **Step 2: Delete migrated sections from `knowledge.go`**

- [ ] **Step 3: Verify compilation**

```bash
go build ./...
```

- [ ] **Step 4: Run tests**

```bash
go test ./... -count=1
```

- [ ] **Step 5: Commit**

```bash
git add knowledge_spo.go knowledge.go
git commit -m "refactor: extract knowledge_spo.go from knowledge.go

QueryOutgoingEdges and QueryNodesLinkingTo move to
single-responsibility file. knowledge.go shrinks by ~90 lines."
```

---

## Task 6: Extract `knowledge_project.go` and delete `knowledge.go`

**Files:**
- Create: `knowledge_project.go`
- Delete: `knowledge.go`

After this task `knowledge.go` should have no remaining functions. Delete it rather than leaving an empty file.

- [ ] **Step 1: Create `knowledge_project.go`**

Copy these sections from `knowledge.go`:
- `metadataStatus` private helper (around original lines 694–704)
- `UpdateProjectStatus` (around original lines 630–667)
- `AppendToProjectArchiveSummary` (around original lines 707–743)
- `GetLinkedCompletedProjectID` (around original lines 746–770)
- `isCompletedProjectByID` (around original lines 772–778)

File header:
```go
// Package memory — project and goal node status management and archive helpers.
package memory

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"
    "time"

    "cloud.google.com/go/firestore"
)
```

Trim imports to what is actually used.

- [ ] **Step 2: Delete `knowledge.go`**

Verify `knowledge.go` contains no remaining function or type definitions before deleting. It should be empty (or contain only the package declaration). Delete it:

```bash
rm /Users/jstrohm/code/memory/knowledge.go
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./...
```

Expected: no errors. If you see "undefined: KnowledgeCollection" or similar, a file that was importing it indirectly now needs to be checked — but since all files are in `package memory`, this should not happen.

- [ ] **Step 4: Run tests**

```bash
go test ./... -count=1
```

Expected: identical results to Task 1 baseline. No new failures.

- [ ] **Step 5: Update `blueprint.md` File Map** (required by `.cursorrules` — must be in this same commit)

Find the row in `blueprint.md`:
```
| `knowledge.go` | CRUD for `KnowledgeNode`: ... |
```

Replace it with five rows:
```markdown
| `knowledge_crud.go` | Package constants, `KnowledgeNode`/`KnowledgeNodeWithLinks` structs, point-reads (`GetKnowledgeNodeByID`, `GetKnowledgeNodesByIDs`), entity-link helpers (`AppendJournalEntryIDsToNode`, `AddEntityLink`), `FindNearestWithThreshold`, `GetUserIdentityNodes`, `GetActiveSignals`, `ListKnowledgeNodes`. |
| `knowledge_upsert.go` | LLM-driven fact-collision detection (`evaluateFactCollision`), all `Upsert*` variants (`UpsertKnowledge`, `UpsertSemanticMemory`, `UpsertSemanticMemoryPreembedded`, `UpsertSemanticMemoryPreembeddedWithSPO`), private `upsertSemanticMemoryWithVector`. |
| `knowledge_search.go` | Vector KNN search (`QuerySimilarNodes`, `QuerySimilarSemanticNodes`), keyword scan (`SearchKnowledgeNodes`), entity/project name finders (`FindEntityNodeByName`, `FindProjectOrGoalByName`), `DiscoverRelatedNodes`. |
| `knowledge_spo.go` | SPO edge traversal: `QueryOutgoingEdges` (outgoing SPO edges), `QueryNodesLinkingTo` (incoming entity-link edges). |
| `knowledge_project.go` | Project/goal status mutations (`UpdateProjectStatus`), archive-summary append (`AppendToProjectArchiveSummary`), completed-project lookup (`GetLinkedCompletedProjectID`). |
```

- [ ] **Step 6: Commit (knowledge_project.go + knowledge.go deletion + blueprint.md together)**

```bash
git add knowledge_project.go blueprint.md
git rm knowledge.go
git commit -m "refactor: extract knowledge_project.go, delete knowledge.go, sync blueprint.md

Project/goal status, archive-summary append, and completed-project
lookup move to single-responsibility file. knowledge.go fully
drained and removed. blueprint.md File Map updated per .cursorrules."
```

---

## Task 7: Final verification

- [ ] **Step 1: Run full test suite one more time**

```bash
cd /Users/jstrohm/code/memory
go test ./... -count=1 -v 2>&1 | grep -E "^(ok|FAIL|---)" | sort
```

Expected: identical pass/fail pattern to Task 1 baseline.

- [ ] **Step 2: Confirm all new files are under 400 lines**

```bash
wc -l knowledge_crud.go knowledge_upsert.go knowledge_search.go knowledge_spo.go knowledge_project.go
```

Expected: every file under 400 lines.

- [ ] **Step 3: Confirm knowledge.go is gone**

```bash
ls /Users/jstrohm/code/memory/knowledge.go 2>&1
```

Expected: `ls: cannot access 'knowledge.go': No such file or directory`

- [ ] **Step 4: Confirm jot still builds**

```bash
cd /Users/jstrohm/code/jot && go build ./... && echo "jot build ok"
```

Expected: success. Because Path 1 keeps everything in `package memory` with the same exported symbols, `jot` needs zero changes.

---

## Notes

- **No API changes.** All exported symbols (`KnowledgeNode`, `KnowledgeNodeWithLinks`, `SPOExtra`, all `Store` methods) remain in `package memory`. Callers are unaffected.
- **`jot/` changes required: none.** Package path and all symbol names are identical.
- **`context.go` (709 lines) and `schema.go` (497 lines)** are also over the 400-line rule. They are out of scope for this plan but should be addressed in a follow-up refactor.
- **If a compile error says an import is missing,** check which of the five files uses the package and add it to that file's import block. Every file in the same Go package has its own imports.
