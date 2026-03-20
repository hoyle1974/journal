# Journal Export & Replay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `export-journal` and `replay-journal` admin subcommands plus a `/internal/replay` HTTP endpoint so that prod journal entries can be exported to a local archive and replayed through the full processing pipeline on dev.

**Architecture:** The export command calls a new `pkg/memory.GetAllLogEntries` function (keeping raw Firestore queries in `pkg/memory`), downloads GCS media using the existing `GCSImageStorage.DownloadImage` (works for any `gs://` URI — no new GCS methods needed), and writes a `manifest.jsonl` + media directory. The replay command reads the manifest and POSTs each entry to a new `/internal/replay` endpoint. That handler follows the established `handler_tasks.go` pattern: type-assert `s.App.(*infra.App)` to access `ImageStorage().UploadImage` and `UploadAudio`, then call `agent.AddEntryAndEnqueue` with the original timestamp, triggering the full processing pipeline.

**Tech Stack:** Go, `cloud.google.com/go/firestore`, `pkg/storage.GCSImageStorage.DownloadImage`, `agent.AddEntryAndEnqueue`, `memory.UpdateEntryAudio`, `encoding/json`, `mime/multipart`, chi router.

**Spec:** `docs/superpowers/specs/2026-03-20-journal-export-replay-design.md`

**Note on spec divergence:** The spec called for a new `DownloadAudio` method on `GCSImageStorage`. We instead reuse `DownloadImage` since it reads any `gs://` URI regardless of content type. No changes to `pkg/storage/gcs.go` or `internal/infra/app.go` are needed.

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| Modify | `pkg/memory/entry_nodes.go` | Add `GetAllLogEntries` — unbounded ascending query |
| Create | `cmd/admin/export_journal.go` | Export subcommand: call `GetAllLogEntries`, download GCS, write archive |
| Create | `cmd/admin/replay_journal.go` | Replay subcommand: read manifest, POST to `/internal/replay` |
| Modify | `cmd/admin/main.go` | Register `export-journal` and `replay-journal` subcommands |
| Create | `internal/api/handler_replay.go` | `/internal/replay` HTTP handler |
| Modify | `internal/api/router.go` | Register `/internal/replay` route |
| Create | `scripts/export-journal.sh` | Shell wrapper: `./scripts/export-journal.sh <dev\|prod>` |
| Create | `scripts/replay-journal.sh` | Shell wrapper: `./scripts/replay-journal.sh <dev\|prod> <archive-dir>` |

---

## Task 1: Add `GetAllLogEntries` to `pkg/memory/entry_nodes.go`

**Files:**
- Modify: `pkg/memory/entry_nodes.go`

- [ ] **Step 1: Add the function after `GetEntriesAsc`**

Open `pkg/memory/entry_nodes.go`. After the `GetEntriesAsc` function (around line 145), add:

```go
// GetAllLogEntries fetches every node_type="log" entry sorted ascending by timestamp.
// No limit is applied — intended for admin export operations.
func GetAllLogEntries(ctx context.Context, env infra.ToolEnv) ([]Entry, error) {
	if env == nil {
		return nil, fmt.Errorf("env is required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("firestore client: %w", err)
	}
	query := client.Collection(KnowledgeCollection).
		Where("node_type", "==", NodeTypeLog).
		OrderBy("timestamp", firestore.Asc)
	return infra.QueryDocuments(ctx, query, func(doc *firestore.DocumentSnapshot) (Entry, error) {
		var e Entry
		if err := doc.DataTo(&e); err != nil {
			return Entry{}, fmt.Errorf("decode entry: %w", err)
		}
		e.UUID = doc.Ref.ID
		return e, nil
	})
}
```

- [ ] **Step 2: Build to verify it compiles**

```bash
go build ./pkg/memory/...
```
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add pkg/memory/entry_nodes.go
git commit -m "feat(memory): add GetAllLogEntries for unbounded export query"
```

---

## Task 2: Export subcommand (`cmd/admin/export_journal.go`)

**Files:**
- Create: `cmd/admin/export_journal.go`
- Modify: `cmd/admin/main.go`

- [ ] **Step 1: Write `cmd/admin/export_journal.go`**

```go
// export_journal downloads all journal entries (node_type="log") from Firestore
// to a local archive directory for later replay on dev.
// Usage: go run ./cmd/admin export-journal --output ./jot-export-2026-03-20
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/storage"
)

// manifestEntry is one line of manifest.jsonl.
type manifestEntry struct {
	UUID      string  `json:"uuid"`
	Content   string  `json:"content"`
	Source    string  `json:"source"`
	Timestamp string  `json:"timestamp"`
	ImageFile *string `json:"image_file"`
	AudioFile *string `json:"audio_file"`
}

func runExportJournal(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("export-journal", flag.ExitOnError)
	output := fs.String("output", "", "Directory to write archive into (must not already exist)")
	_ = fs.Parse(args)

	if *output == "" {
		log.Fatal("--output is required")
	}
	if _, err := os.Stat(*output); err == nil {
		log.Fatalf("output directory %q already exists — remove it or choose a different path", *output)
	}

	if err := os.MkdirAll(filepath.Join(*output, "images"), 0o755); err != nil {
		log.Fatalf("create images dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(*output, "audio"), 0o755); err != nil {
		log.Fatalf("create audio dir: %v", err)
	}

	entries, err := memory.GetAllLogEntries(ctx, app)
	if err != nil {
		log.Fatalf("fetch entries: %v", err)
	}
	log.Printf("Found %d journal entries to export", len(entries))

	gcs, hasGCS := app.ImageStorage().(*storage.GCSImageStorage)

	var manifest []manifestEntry
	for i, e := range entries {
		if (i+1)%50 == 0 || i+1 == len(entries) {
			fmt.Printf("exported %d/%d entries...\n", i+1, len(entries))
		}

		me := manifestEntry{
			UUID:      e.UUID,
			Content:   e.Content,
			Source:    e.Source,
			Timestamp: e.Timestamp,
		}

		if e.ImageURL != "" {
			if !hasGCS {
				log.Printf("warning: entry %s has image_url but GCS not configured (set JOT_IMAGES_BUCKET); skipping", e.UUID)
			} else {
				data, mimeType, dlErr := gcs.DownloadImage(ctx, e.ImageURL)
				if dlErr != nil {
					log.Printf("warning: entry %s image download failed: %v — skipping", e.UUID, dlErr)
				} else {
					ext := extFromMIME(mimeType)
					relPath := filepath.Join("images", e.UUID+ext)
					if writeErr := os.WriteFile(filepath.Join(*output, relPath), data, 0o644); writeErr != nil {
						log.Fatalf("write image file: %v", writeErr)
					}
					me.ImageFile = &relPath
				}
			}
		}

		if e.AudioURL != "" {
			if !hasGCS {
				log.Printf("warning: entry %s has audio_url but GCS not configured; skipping", e.UUID)
			} else {
				// DownloadImage reads any gs:// URI — reuse it for audio bytes.
				data, _, dlErr := gcs.DownloadImage(ctx, e.AudioURL)
				if dlErr != nil {
					log.Printf("warning: entry %s audio download failed: %v — skipping", e.UUID, dlErr)
				} else {
					relPath := filepath.Join("audio", e.UUID+".ogg")
					if writeErr := os.WriteFile(filepath.Join(*output, relPath), data, 0o644); writeErr != nil {
						log.Fatalf("write audio file: %v", writeErr)
					}
					me.AudioFile = &relPath
				}
			}
		}

		manifest = append(manifest, me)
	}

	// Write manifest.jsonl last — its presence signals a complete export.
	mf, err := os.Create(filepath.Join(*output, "manifest.jsonl"))
	if err != nil {
		log.Fatalf("create manifest: %v", err)
	}
	defer mf.Close()
	enc := json.NewEncoder(mf)
	for _, me := range manifest {
		if err := enc.Encode(me); err != nil {
			log.Fatalf("write manifest line: %v", err)
		}
	}

	fmt.Printf("Export complete: %d entries written to %s\n", len(manifest), *output)
}

func extFromMIME(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".bin"
	}
}
```

- [ ] **Step 2: Register in `cmd/admin/main.go`**

Add to the usage comment block at the top of the file:
```
//   export-journal   - export all journal entries to a local archive (--output=required)
```

Add to the `fmt.Fprintf` usage line in `main()` (the error case for unknown subcommand).

Add to the `switch` in `main()`:
```go
case "export-journal":
    runExportJournal(ctx, app, args)
```

- [ ] **Step 3: Build to verify it compiles**

```bash
go build ./cmd/admin/...
```
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add cmd/admin/export_journal.go cmd/admin/main.go
git commit -m "feat(admin): add export-journal subcommand"
```

---

## Task 3: Export shell script (`scripts/export-journal.sh`)

**Files:**
- Create: `scripts/export-journal.sh`

- [ ] **Step 1: Write `scripts/export-journal.sh`**

```bash
#!/bin/bash
#
# Export all journal entries to a local archive directory.
# Usage: ./scripts/export-journal.sh <dev|prod>
# Output directory: ./jot-export-YYYY-MM-DD (in repo root)
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
source "$REPO_ROOT/scripts/lib/env-confirm.sh"
require_env_and_confirm "$1"
shift

if [ -f "$ENV_FILE" ]; then
  set -a
  source "$ENV_FILE"
  set +a
else
  echo "Error: $ENV_FILE not found."
  exit 1
fi

OUTPUT="$REPO_ROOT/jot-export-$(date +%Y-%m-%d)"

if [ -d "$OUTPUT" ]; then
  echo "Error: Archive $OUTPUT already exists. Remove it or rename it before running again."
  exit 1
fi

echo "Exporting journal to $OUTPUT ..."
JOT_PROFILE="$ENV_TARGET" go run ./cmd/admin export-journal --output "$OUTPUT"
echo "Done. Archive: $OUTPUT"
```

- [ ] **Step 2: Make executable**

```bash
chmod +x scripts/export-journal.sh
```

- [ ] **Step 3: Commit**

```bash
git add scripts/export-journal.sh
git commit -m "feat(scripts): add export-journal.sh wrapper"
```

---

## Task 4: Replay HTTP handler (`internal/api/handler_replay.go`)

**Files:**
- Create: `internal/api/handler_replay.go`
- Modify: `internal/api/router.go`

The handler follows the same `s.App.(*infra.App)` pattern used in `handler_tasks.go` (e.g. `handleProcessTelegramQuery`) to access `ImageStorage().UploadImage` and `UploadAudio`. The type assertion is only checked when media is actually present.

- [ ] **Step 1: Write `internal/api/handler_replay.go`**

```go
package api

import (
	"io"
	"net/http"
	"strings"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/memory"
)

const replayMultipartMaxBytes = 10 << 20 // 10 MB

// handleReplay accepts a multipart form with content, source, timestamp, and optional
// image/audio files, inserting the entry through the full processing pipeline
// (AddEntryAndEnqueue → /internal/process-entry) with the original timestamp preserved.
// Intended for dev replay of prod journal archives.
func handleReplay(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()

	if err := r.ParseMultipartForm(replayMultipartMaxBytes); err != nil {
		return nil, handlerError(http.StatusBadRequest, "invalid multipart form")
	}

	content := strings.TrimSpace(r.FormValue("content"))
	source := strings.TrimSpace(r.FormValue("source"))
	timestamp := strings.TrimSpace(r.FormValue("timestamp"))

	if content == "" {
		return nil, handlerError(http.StatusBadRequest, "content is required")
	}
	if source == "" {
		return nil, handlerError(http.StatusBadRequest, "source is required")
	}
	if timestamp == "" {
		return nil, handlerError(http.StatusBadRequest, "timestamp is required")
	}

	// Upload image if provided.
	var imageURL string
	if f, _, err := r.FormFile("image"); err == nil {
		defer f.Close()
		imageBytes, readErr := io.ReadAll(f)
		if readErr != nil {
			return nil, handlerError(http.StatusBadRequest, "could not read image file")
		}
		if len(imageBytes) > 0 {
			app, ok := s.App.(*infra.App)
			if !ok {
				return nil, handlerError(http.StatusInternalServerError, "image storage not available")
			}
			imageURL, err = app.ImageStorage().UploadImage(ctx, imageBytes)
			if err != nil {
				infra.LoggerFrom(ctx).Warn("replay: image upload failed", "error", err)
				return nil, handlerError(http.StatusInternalServerError, "image upload failed")
			}
		}
	}

	// Upload audio if provided.
	var audioURL string
	if f, _, err := r.FormFile("audio"); err == nil {
		defer f.Close()
		audioBytes, readErr := io.ReadAll(f)
		if readErr != nil {
			return nil, handlerError(http.StatusBadRequest, "could not read audio file")
		}
		if len(audioBytes) > 0 {
			app, ok := s.App.(*infra.App)
			if !ok {
				return nil, handlerError(http.StatusInternalServerError, "audio storage not available")
			}
			audioURL, err = app.UploadAudio(ctx, audioBytes)
			if err != nil {
				infra.LoggerFrom(ctx).Warn("replay: audio upload failed", "error", err)
				return nil, handlerError(http.StatusInternalServerError, "audio upload failed")
			}
		}
	}

	// Require *infra.App only if we got past the media blocks above — if we reach
	// here without media, we can call agent.AddEntryAndEnqueue directly since it
	// only needs *infra.App (which implements infra.ToolEnv).
	app, ok := s.App.(*infra.App)
	if !ok {
		return nil, handlerError(http.StatusInternalServerError, "app not available")
	}

	infra.LoggerFrom(ctx).Info("replay: inserting entry",
		"source", source, "timestamp", timestamp,
		"has_image", imageURL != "", "has_audio", audioURL != "")

	entryUUID, err := agent.AddEntryAndEnqueue(ctx, app, content, source, &timestamp, imageURL)
	if err != nil {
		infra.LoggerFrom(ctx).Error("replay: AddEntryAndEnqueue failed", "error", err)
		return nil, err
	}

	// Persist audio URL and transcript (content IS the transcript for audio entries).
	if audioURL != "" {
		if updateErr := memory.UpdateEntryAudio(ctx, app, entryUUID, audioURL, content); updateErr != nil {
			infra.LoggerFrom(ctx).Warn("replay: UpdateEntryAudio failed", "entry_uuid", entryUUID, "error", updateErr)
		}
	}

	infra.LoggerFrom(ctx).Info("replay: entry inserted", "entry_uuid", entryUUID)
	return map[string]interface{}{"success": true, "uuid": entryUUID}, nil
}
```

- [ ] **Step 2: Register route in `internal/api/router.go`**

In the protected routes group, after the existing `/internal/dream-run` line, add:
```go
r.With(RateLimitMiddleware(300)).Post("/internal/replay", wrapAPI(handleReplay))
```

- [ ] **Step 3: Build to verify it compiles**

```bash
go build ./internal/...
```
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/api/handler_replay.go internal/api/router.go
git commit -m "feat(api): add /internal/replay endpoint for journal playback"
```

---

## Task 5: Replay admin subcommand (`cmd/admin/replay_journal.go`)

**Files:**
- Create: `cmd/admin/replay_journal.go`
- Modify: `cmd/admin/main.go`

Note: `runReplayJournal` does not use `ctx` or `app` (it makes pure HTTP calls), but takes them to match the admin CLI pattern `runXxx(ctx context.Context, app *infra.App, args []string)`.

- [ ] **Step 1: Write `cmd/admin/replay_journal.go`**

```go
// replay_journal reads a local journal archive produced by export-journal and
// replays each entry through the /internal/replay HTTP endpoint in timestamp order.
// Usage: go run ./cmd/admin replay-journal --archive ./jot-export-2026-03-20 [--api-url http://localhost:8080] [--api-key <key>]
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jackstrohm/jot/internal/infra"
)

func runReplayJournal(ctx context.Context, _ *infra.App, args []string) {
	fs := flag.NewFlagSet("replay-journal", flag.ExitOnError)
	archive := fs.String("archive", "", "Path to the archive directory produced by export-journal")
	apiURL := fs.String("api-url", os.Getenv("JOT_API_URL"), "Base URL of the Jot API server (defaults to JOT_API_URL env var)")
	apiKey := fs.String("api-key", os.Getenv("JOT_API_KEY"), "API key for the Jot server (defaults to JOT_API_KEY env var)")
	_ = fs.Parse(args)

	if *archive == "" {
		log.Fatal("--archive is required")
	}
	if *apiURL == "" {
		log.Fatal("--api-url is required (or set JOT_API_URL in your .env file)")
	}
	if *apiKey == "" {
		log.Fatal("--api-key is required (or set JOT_API_KEY in your .env file)")
	}

	manifestPath := filepath.Join(*archive, "manifest.jsonl")
	f, err := os.Open(manifestPath)
	if err != nil {
		log.Fatalf("open manifest: %v (run export-journal first, or check --archive path)", err)
	}
	defer f.Close()

	var entries []manifestEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var me manifestEntry
		if err := json.Unmarshal(line, &me); err != nil {
			log.Fatalf("parse manifest line: %v", err)
		}
		entries = append(entries, me)
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("read manifest: %v", err)
	}

	fmt.Printf("Replaying %d entries to %s ...\n", len(entries), *apiURL)

	client := &http.Client{Timeout: 30 * time.Second}
	endpoint := *apiURL + "/internal/replay"

	for i, me := range entries {
		if err := replayEntry(ctx, client, endpoint, *apiKey, *archive, me); err != nil {
			log.Printf("warning: entry %s failed: %v — continuing", me.UUID, err)
		}
		if (i+1)%50 == 0 || i+1 == len(entries) {
			fmt.Printf("replayed %d/%d entries...\n", i+1, len(entries))
		}
	}

	fmt.Printf("Replay complete: %d entries submitted.\n", len(entries))
}

func replayEntry(ctx context.Context, client *http.Client, endpoint, apiKey, archiveDir string, me manifestEntry) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	_ = w.WriteField("content", me.Content)
	_ = w.WriteField("source", me.Source)
	_ = w.WriteField("timestamp", me.Timestamp)

	if me.ImageFile != nil {
		if err := attachFile(w, "image", filepath.Join(archiveDir, *me.ImageFile)); err != nil {
			log.Printf("warning: could not attach image for %s: %v", me.UUID, err)
		}
	}
	if me.AudioFile != nil {
		if err := attachFile(w, "audio", filepath.Join(archiveDir, *me.AudioFile)); err != nil {
			log.Printf("warning: could not attach audio for %s: %v", me.UUID, err)
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST replay: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, body)
	}
	return nil
}

func attachFile(w *multipart.Writer, fieldName, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file %q: %w", path, err)
	}
	part, err := w.CreateFormFile(fieldName, filepath.Base(path))
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	_, err = part.Write(data)
	return err
}
```

- [ ] **Step 2: Register in `cmd/admin/main.go`**

Add to the usage comment block:
```
//   replay-journal   - replay a local archive to the Jot API (--archive=required [--api-url] [--api-key])
```

Add to the `switch`:
```go
case "replay-journal":
    runReplayJournal(ctx, app, args)
```

- [ ] **Step 3: Build to verify it compiles**

```bash
go build ./cmd/admin/...
```
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add cmd/admin/replay_journal.go cmd/admin/main.go
git commit -m "feat(admin): add replay-journal subcommand"
```

---

## Task 6: Replay shell script (`scripts/replay-journal.sh`)

**Files:**
- Create: `scripts/replay-journal.sh`

- [ ] **Step 1: Write `scripts/replay-journal.sh`**

```bash
#!/bin/bash
#
# Replay a local journal archive through the Jot API.
# Usage: ./scripts/replay-journal.sh <dev|prod> <archive-dir>
# JOT_API_URL and JOT_API_KEY are read from the sourced .env file.
#
set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
source "$REPO_ROOT/scripts/lib/env-confirm.sh"
require_env_and_confirm "$1" " <archive-dir>"
shift

ARCHIVE="${1:-}"
if [ -z "$ARCHIVE" ]; then
  echo "Usage: $0 <dev|prod> <archive-dir>"
  exit 1
fi

if [ -f "$ENV_FILE" ]; then
  set -a
  source "$ENV_FILE"
  set +a
else
  echo "Error: $ENV_FILE not found."
  exit 1
fi

if [ -z "${JOT_API_URL:-}" ]; then
  echo "Error: JOT_API_URL is not set in $ENV_FILE."
  exit 1
fi

if [ -z "${JOT_API_KEY:-}" ]; then
  echo "Error: JOT_API_KEY is not set in $ENV_FILE."
  exit 1
fi

if [ ! -f "$ARCHIVE/manifest.jsonl" ]; then
  echo "Error: $ARCHIVE/manifest.jsonl not found. Is this a valid export archive?"
  exit 1
fi

echo "Replaying archive $ARCHIVE to $JOT_API_URL ..."
JOT_PROFILE="$ENV_TARGET" go run ./cmd/admin replay-journal --archive "$ARCHIVE"
echo "Done."
```

- [ ] **Step 2: Make executable**

```bash
chmod +x scripts/replay-journal.sh
```

- [ ] **Step 3: Commit**

```bash
git add scripts/replay-journal.sh
git commit -m "feat(scripts): add replay-journal.sh wrapper"
```

---

## Task 7: Smoke test end-to-end

- [ ] **Step 1: Build everything one final time**

```bash
go build ./...
```
Expected: no errors.

- [ ] **Step 2: Test the replay endpoint directly against a running dev server**

```bash
# In one terminal: go run ./cmd/server
# In another (source your dev .env first):
source .env
curl -s -X POST http://localhost:8080/internal/replay \
  -H "X-API-Key: $JOT_API_KEY" \
  -F "content=test replay entry" \
  -F "source=test" \
  -F "timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)" | jq .
```
Expected: `{"success":true,"uuid":"..."}` with HTTP 200.

- [ ] **Step 3: Verify entry appeared in dev Firestore**

```bash
go run ./cmd/jot entries --limit 5
```
Expected: the test replay entry is present.

- [ ] **Step 4: Run a dev export (requires dev Firestore + GCS access)**

```bash
./scripts/export-journal.sh dev
```
Expected: `jot-export-YYYY-MM-DD/` created, `manifest.jsonl` present, progress printed.

- [ ] **Step 5: Spot-check manifest**

```bash
head -3 jot-export-*/manifest.jsonl | python3 -m json.tool
```
Expected: valid JSON with `uuid`, `content`, `source`, `timestamp` fields on each line.

- [ ] **Step 6: Run a full replay from the dev archive back to dev**

```bash
./scripts/replay-journal.sh dev ./jot-export-YYYY-MM-DD
```
Expected: `Replaying N entries...` progress lines, `Replay complete: N entries submitted.`
