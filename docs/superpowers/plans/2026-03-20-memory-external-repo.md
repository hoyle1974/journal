# pkg/memory External Repository Extraction Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the self-contained `pkg/memory` package (already decoupled from `internal/infra`) into a new standalone Go module at `github.com/jackstrohm/memory`, then update jot to depend on it as an external module.

**Architecture:** Create a new GitHub repo `jackstrohm/memory`, copy the contents of `pkg/memory/` into it as the module root, update internal self-references in the `gemini/` sub-package, publish `v0.1.0`, then update jot's imports and remove `pkg/memory/`.

**Tech Stack:** Go 1.26, `gh` CLI, `go mod`, standard filesystem operations.

**Starting state:** All work is in the `jot-memory-store` worktree at `../jot-memory-store` on branch `feature/memory-store`. `pkg/memory` already compiles with no `internal/infra` or `pkg/utils` imports.

---

### Task 1: Create the new GitHub repo

- [ ] **Step 1: Create `jackstrohm/memory` on GitHub**

```bash
gh repo create jackstrohm/memory --public --description "Jot memory store: knowledge graph and RAG layer backed by Firestore and Gemini"
```

Expected: repo created, URL printed.

- [ ] **Step 2: Clone it locally**

```bash
git clone git@github.com:jackstrohm/memory.git ../jot-memory-external
```

- [ ] **Step 3: Verify the clone exists**

```bash
ls ../jot-memory-external
```

Expected: empty repo (just `.git/`).

---

### Task 2: Initialize the new module and copy files

- [ ] **Step 1: Initialize `go.mod`**

```bash
cd ../jot-memory-external
go mod init github.com/jackstrohm/memory
```

Expected: `go.mod` created with `module github.com/jackstrohm/memory` and the correct Go version.

- [ ] **Step 2: Edit `go.mod` to set Go version to match jot**

Open `go.mod` and set the Go version to `1.26` (matching jot's `go.mod`):

```
module github.com/jackstrohm/memory

go 1.26
```

- [ ] **Step 3: Copy all files from `pkg/memory/` into the new repo root**

```bash
cp -r /Users/jstrohm/code/jot-memory-store/pkg/memory/. ../jot-memory-external/
```

This copies `*.go`, `gemini/`, `prompts/`, and test files. The new root package will be `package memory`.

- [ ] **Step 4: Verify the structure**

```bash
ls ../jot-memory-external/
ls ../jot-memory-external/gemini/
ls ../jot-memory-external/prompts/
```

Expected: all `.go` files and sub-packages present.

---

### Task 3: Update internal self-references

The `gemini/` sub-package currently imports `github.com/jackstrohm/jot/pkg/memory`. Update it to the new path.

- [ ] **Step 1: Find all old import references**

```bash
grep -r 'jackstrohm/jot/pkg/memory' ../jot-memory-external/ --include="*.go"
```

Expected: matches in `gemini/embedder.go` and `gemini/dispatcher.go`.

- [ ] **Step 2: Replace the import path in both files**

```bash
sed -i '' 's|github.com/jackstrohm/jot/pkg/memory|github.com/jackstrohm/memory|g' \
    ../jot-memory-external/gemini/embedder.go \
    ../jot-memory-external/gemini/dispatcher.go
```

- [ ] **Step 3: Verify no old references remain**

```bash
grep -r 'jackstrohm/jot' ../jot-memory-external/ --include="*.go"
```

Expected: zero output.

---

### Task 4: Resolve dependencies and build

- [ ] **Step 1: Run `go mod tidy` in the new module**

```bash
cd ../jot-memory-external && go mod tidy
```

Expected: `go.sum` created; all transitive dependencies resolved. If any dependency is missing, `go mod tidy` adds it.

- [ ] **Step 2: Build the module**

```bash
cd ../jot-memory-external && go build ./...
```

Expected: 0 errors.

- [ ] **Step 3: Run the tests**

```bash
cd ../jot-memory-external && go test ./... 2>&1
```

Expected: tests that don't require a live Firestore/Gemini pass; integration tests are skipped or noted.

---

### Task 5: Commit and tag `v0.1.0`

- [ ] **Step 1: Stage all files**

```bash
cd ../jot-memory-external
git add .
```

- [ ] **Step 2: Commit**

```bash
git commit -m "$(cat <<'EOF'
feat: initial release of github.com/jackstrohm/memory

Extracted from github.com/jackstrohm/jot/pkg/memory.
Self-contained Store struct with Embedder and LLMDispatcher interfaces.
Concrete Gemini/Vertex AI implementations in ./gemini sub-package.
Prompt templates in ./prompts sub-package.
EOF
)"
```

- [ ] **Step 3: Push to GitHub**

```bash
git push -u origin main
```

- [ ] **Step 4: Tag `v0.1.0`**

```bash
git tag v0.1.0
git push origin v0.1.0
```

- [ ] **Step 5: Verify the tag is visible**

```bash
gh release view v0.1.0 --repo jackstrohm/memory 2>/dev/null || echo "tag exists but no release — ok"
```

---

### Task 6: Add the new dependency to jot

All remaining steps are in the `../jot-memory-store` worktree.

- [ ] **Step 1: Add the new module to jot**

```bash
cd /Users/jstrohm/code/jot-memory-store
go get github.com/jackstrohm/memory@v0.1.0
```

Expected: `go.mod` and `go.sum` updated.

---

### Task 7: Update import paths in jot

Replace every `github.com/jackstrohm/jot/pkg/memory` import with `github.com/jackstrohm/memory`.

- [ ] **Step 1: Find all occurrences**

```bash
cd /Users/jstrohm/code/jot-memory-store
grep -r '"github.com/jackstrohm/jot/pkg/memory' --include="*.go" -l
```

Expected: ~30 files (tools, agents, services, API handlers, infra/app.go).

- [ ] **Step 2: Replace all occurrences**

```bash
cd /Users/jstrohm/code/jot-memory-store
find . -name "*.go" -exec sed -i '' \
    's|"github.com/jackstrohm/jot/pkg/memory"|"github.com/jackstrohm/memory"|g' {} +
find . -name "*.go" -exec sed -i '' \
    's|"github.com/jackstrohm/jot/pkg/memory/gemini"|"github.com/jackstrohm/memory/gemini"|g' {} +
find . -name "*.go" -exec sed -i '' \
    's|"github.com/jackstrohm/jot/pkg/memory/prompts"|"github.com/jackstrohm/memory/prompts"|g' {} +
```

- [ ] **Step 3: Verify no old references remain**

```bash
grep -r 'jackstrohm/jot/pkg/memory' . --include="*.go"
```

Expected: zero output.

---

### Task 8: Remove `pkg/memory/` from jot

- [ ] **Step 1: Delete the directory**

```bash
cd /Users/jstrohm/code/jot-memory-store
rm -rf pkg/memory/
```

- [ ] **Step 2: Check if `pkg/` is now empty**

```bash
ls pkg/
```

If `pkg/` has no other sub-packages, delete it too:

```bash
rmdir pkg/ 2>/dev/null || echo "pkg/ still has other contents — leave it"
```

---

### Task 9: Build and test jot

- [ ] **Step 1: Run `go mod tidy`**

```bash
cd /Users/jstrohm/code/jot-memory-store && go mod tidy
```

- [ ] **Step 2: Build the full module**

```bash
cd /Users/jstrohm/code/jot-memory-store && go build ./...
```

Expected: 0 errors.

- [ ] **Step 3: Run tests**

```bash
cd /Users/jstrohm/code/jot-memory-store && go test ./... 2>&1 | tail -20
```

Expected: all tests that were passing before still pass.

- [ ] **Step 4: Commit**

```bash
cd /Users/jstrohm/code/jot-memory-store
git add -A
git commit -m "$(cat <<'EOF'
refactor: replace pkg/memory with external github.com/jackstrohm/memory module

Removes pkg/memory/ from the jot repo entirely.
All callers updated to import github.com/jackstrohm/memory.
EOF
)"
```

---

### Task 10: Update documentation

**Files:**
- Modify: `internal/prompts/app_capabilities.txt`
- Modify: `blueprint.md` (if it references `pkg/memory`)

- [ ] **Step 1: Update any references to `pkg/memory` or `jot/pkg/memory`**

```bash
grep -r 'pkg/memory' internal/prompts/ docs/ blueprint.md 2>/dev/null
```

Replace any occurrences with `github.com/jackstrohm/memory`.

- [ ] **Step 2: Commit**

```bash
cd /Users/jstrohm/code/jot-memory-store
git add internal/prompts/app_capabilities.txt blueprint.md 2>/dev/null
git commit -m "docs: update references from pkg/memory to github.com/jackstrohm/memory"
```

---

### Task 11: Merge and close out

- [ ] **Step 1: Move brief to done** (if a brief exists for this work)

```bash
cd /Users/jstrohm/code/jot-memory-store
ls briefs/active/ | grep memory
```

If found, move it:

```bash
mv briefs/active/<brief-file>.md briefs/done/
git add briefs/
git commit -m "chore: mark memory external extraction brief as done"
```

- [ ] **Step 2: Merge to main**

```bash
cd /Users/jstrohm/code/jot
git checkout main
git merge feature/memory-store
```

- [ ] **Step 3: Remove worktree**

```bash
git worktree remove ../jot-memory-store
```

- [ ] **Step 4: Verify**

```bash
git worktree list
go build ./...
```

Expected: `jot-memory-store` worktree gone; jot builds cleanly.

---

## Summary

| Step | What happens |
|------|--------------|
| Tasks 1–5 | New `github.com/jackstrohm/memory` repo created, `v0.1.0` tagged and pushed |
| Task 6 | jot adds `memory@v0.1.0` as an external dependency |
| Tasks 7–8 | Import paths updated; `pkg/memory/` deleted from jot |
| Tasks 9–10 | Build verified; docs updated |
| Task 11 | Merged to main; worktree removed |
