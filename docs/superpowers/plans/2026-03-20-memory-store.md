# Memory Store Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `pkg/memory` from 70+ package-level functions taking `env infra.ToolEnv` into methods on a `*Store` struct with clean dependency injection, severing all `internal/infra` and `pkg/utils` imports.

**Architecture:** Single `Store` struct holding `*firestore.Client`, `Embedder` interface, `LLMDispatcher` interface, and `*slog.Logger`. Gemini implementations of both interfaces live in `pkg/memory/gemini/`. Utility functions copied from infra/utils into the package. `infra.App` gains a `Memory *memory.Store` field. All 38 callers updated in one branch.

**Tech Stack:** Go, `cloud.google.com/go/firestore`, `google.golang.org/genai` (in gemini sub-package only), Vertex AI REST (embeddings), `log/slog`

**Spec:** `docs/superpowers/specs/2026-03-20-memory-store-extraction-design.md`

**Worktree:** `../jot-memory-store` (branch `feature/memory-store`)

> **Note:** The branch will not compile between Task 1 and Task 9. That is expected. It compiles again after Task 9.

---

## File Map

**New files:**
- `pkg/memory/memory.go` — `Store` struct, `New()`, `WithLogger` option
- `pkg/memory/interfaces.go` — `Embedder`, `LLMDispatcher`, `LLMRequest`
- `pkg/memory/errors.go` — `wrapFirestoreIndexError` (unexported copy from infra)
- `pkg/memory/firestore.go` — `queryDocuments`, `getStringField`, etc. (unexported copies from infra)
- `pkg/memory/math.go` — `cosineSimilarity` (unexported copy from utils)
- `pkg/memory/text.go` — `parseKeyValueMap`, `truncateString`, `sanitizePrompt`, `wrapAsUserData`, `truncateToMaxBytes`
- `pkg/memory/log.go` — `logVectorSearchFailed`, `logFoundNode`, `logFoundEntry`, `logRAGQuality` (unexported)
- `pkg/memory/prompts/embed.go` — `//go:embed` directives
- `pkg/memory/prompts/journal_analyze.go` — `JournalAnalyzeData`, `BuildJournalAnalyze()`
- `pkg/memory/prompts/context_analyze.go` — `ContextAnalyzeData`, `BuildContextAnalyze()`
- `pkg/memory/prompts/executive_summary.go` — `ExecutiveSummary()`
- `pkg/memory/prompts/journal_analyze.txt` — copied from `internal/prompts/journal_analyze.txt`
- `pkg/memory/prompts/context_analyze.txt` — copied from `internal/prompts/context_analyze.txt`
- `pkg/memory/prompts/executive_summary.txt` — copied from `internal/prompts/executive_summary.txt`
- `pkg/memory/gemini/embedder.go` — `NewEmbedder(projectID string) memory.Embedder`
- `pkg/memory/gemini/dispatcher.go` — `NewDispatcher(client *genai.Client, model string) memory.LLMDispatcher`

**Modified files (pkg/memory — function→method conversion):**
- `pkg/memory/analysis.go`
- `pkg/memory/context.go`
- `pkg/memory/entry_format.go`
- `pkg/memory/entry_nodes.go`
- `pkg/memory/entry_nodes_extended.go`
- `pkg/memory/graph.go`
- `pkg/memory/incubation.go`
- `pkg/memory/janitor.go`
- `pkg/memory/knowledge.go` (also gains private `evaluateFactCollision` method)
- `pkg/memory/migrate.go`
- `pkg/memory/pending.go` (Telegram functions renamed)
- `pkg/memory/query_format.go`
- `pkg/memory/query_nodes.go`
- `pkg/memory/rag.go`
- `pkg/memory/rerank.go`
- `pkg/memory/rollup.go`
- `pkg/memory/schema.go`
- `pkg/memory/task_engine.go`
- `pkg/memory/task_nodes.go`
- `pkg/memory/task_query.go`
- All `pkg/memory/*_test.go` files — construct `*Store` instead of mocking `ToolEnv`

**Modified files (wiring + callers):**
- `internal/infra/app.go` — add `Memory *memory.Store` field, construct in `NewApp`
- All 38 caller files in `cmd/`, `internal/agent/`, `internal/api/`, `internal/service/`, `internal/tools/impl/`

---

## Task 1: Scaffold interfaces, utilities, and Store struct

**Files:**
- Create: `pkg/memory/interfaces.go`
- Create: `pkg/memory/memory.go`
- Create: `pkg/memory/errors.go`
- Create: `pkg/memory/firestore.go`
- Create: `pkg/memory/math.go`
- Create: `pkg/memory/text.go`
- Create: `pkg/memory/log.go`

- [ ] Create `pkg/memory/interfaces.go`:

```go
package memory

import "context"

// Embedder generates vector embeddings for semantic search.
type Embedder interface {
	GenerateEmbedding(ctx context.Context, text string, taskType string) ([]float32, error)
	GenerateEmbeddingsBatch(ctx context.Context, texts []string, taskType string) ([][]float32, error)
}

// LLMDispatcher makes single-shot LLM calls and returns the text response.
type LLMDispatcher interface {
	Dispatch(ctx context.Context, req LLMRequest) (string, error)
}

// LLMRequest is a simple text-in/text-out LLM request.
// MaxTokens 0 means the dispatcher uses its default (8192).
type LLMRequest struct {
	SystemPrompt string
	UserPrompt   string
	MaxTokens    int
	JSONMode     bool
}

// Embedding task type constants (mirrors Vertex AI text-embedding-005 task types).
const (
	EmbedTaskRetrievalQuery    = "RETRIEVAL_QUERY"
	EmbedTaskRetrievalDocument = "RETRIEVAL_DOCUMENT"
)
```

- [ ] Create `pkg/memory/memory.go`:

```go
package memory

import (
	"io"
	"log/slog"

	"cloud.google.com/go/firestore"
)

// Store is the memory layer for jot's GraphRAG system.
// Construct with New(); all methods require a context.
type Store struct {
	db       *firestore.Client
	embedder Embedder
	llm      LLMDispatcher
	log      *slog.Logger
}

// Option configures a Store at construction time.
type Option func(*Store)

// WithLogger sets a structured logger. If not provided, logs are discarded.
func WithLogger(l *slog.Logger) Option {
	return func(s *Store) { s.log = l }
}

// New creates a Store with the given Firestore client, embedder, and LLM dispatcher.
func New(db *firestore.Client, embedder Embedder, llm LLMDispatcher, opts ...Option) *Store {
	s := &Store{
		db:       db,
		embedder: embedder,
		llm:      llm,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}
```

- [ ] Create `pkg/memory/errors.go` (copied from `internal/infra/firestore.go`):

```go
package memory

import (
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// wrapFirestoreIndexError wraps "query requires an index" errors with a user-facing message.
// Copied from internal/infra/firestore.go.
func wrapFirestoreIndexError(err error) error {
	if err == nil {
		return nil
	}
	if status.Code(err) != codes.FailedPrecondition {
		return err
	}
	if !strings.Contains(err.Error(), "index") {
		return err
	}
	return fmt.Errorf("Firestore query requires a composite index. Add the needed index to firestore.indexes.json and run: firebase deploy --only firestore:indexes — %w", err)
}
```

- [ ] Create `pkg/memory/firestore.go` (copied from `internal/infra/firestore.go`):

```go
package memory

import (
	"context"

	"cloud.google.com/go/firestore"
	"github.com/google/uuid"
	"google.golang.org/api/iterator"
)

// queryDocuments runs a Firestore query and maps each document with mapDoc.
// Copied from internal/infra/firestore.go.
func queryDocuments[T any](ctx context.Context, query firestore.Query, mapDoc func(*firestore.DocumentSnapshot) (T, error)) ([]T, error) {
	iter := query.Documents(ctx)
	defer iter.Stop()
	var results []T
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		t, err := mapDoc(doc)
		if err != nil {
			continue
		}
		results = append(results, t)
	}
	return results, nil
}

func generateUUID() string { return uuid.New().String() }

func getStringField(data map[string]any, field string) string {
	if v, ok := data[field].(string); ok {
		return v
	}
	return ""
}

func getStringSliceField(data map[string]any, field string) []string {
	v, ok := data[field].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(v))
	for _, e := range v {
		if s, ok := e.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func getFloat32SliceField(data map[string]any, field string) []float32 {
	v, ok := data[field].([]any)
	if !ok {
		return nil
	}
	out := make([]float32, 0, len(v))
	for _, e := range v {
		if f, ok := e.(float64); ok {
			out = append(out, float32(f))
		}
	}
	return out
}
```

- [ ] Create `pkg/memory/math.go`:

```go
package memory

import "math"

// cosineSimilarity returns the cosine similarity between two float32 vectors.
// Copied from pkg/utils.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
```

- [ ] Create `pkg/memory/text.go` (exact copies from `pkg/utils` — match behavior precisely):

```go
package memory

import (
	"strings"
	"unicode/utf8"
)

// truncateString truncates s to at most maxRunes runes. Does NOT append "...".
// Copied from pkg/utils.TruncateString.
func truncateString(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}

// truncateToMaxBytes truncates s to at most maxBytes UTF-8 bytes. Does NOT append "...".
// Copied from pkg/utils.TruncateToMaxBytes.
func truncateToMaxBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	runes := []rune(s)
	n := 0
	for i, r := range runes {
		n += utf8.RuneLen(r)
		if n > maxBytes {
			if i == 0 {
				return ""
			}
			return string(runes[:i])
		}
	}
	return s
}

// sanitizePrompt ensures s is valid UTF-8. Copied from pkg/utils.SanitizePrompt.
func sanitizePrompt(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "")
}

// wrapAsUserData wraps s in <user_data> delimiters. Copied from pkg/utils.WrapAsUserData.
func wrapAsUserData(s string) string {
	if s == "" {
		return "<user_data></user_data>"
	}
	return "<user_data>\n" + s + "\n</user_data>"
}

// parseKeyValueMap parses key/value text (no JSON). Returns:
//   - simple: key -> value for lines like "key: value" (keys lowercased)
//   - sections: section name -> lines for block sections like "entities:" followed by items
//
// Copied from pkg/utils.ParseKeyValueMap.
func parseKeyValueMap(text string) (simple map[string]string, sections map[string][]string) {
	simple = make(map[string]string)
	sections = make(map[string][]string)
	lines := strings.Split(text, "\n")
	var currentSection string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if currentSection != "" {
			sections[currentSection] = append(sections[currentSection], line)
			continue
		}
		idx := strings.Index(line, ":")
		if idx >= 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			keyLower := strings.ToLower(key)
			if value == "" {
				currentSection = keyLower
				continue
			}
			currentSection = ""
			simple[keyLower] = value
		}
	}
	return simple, sections
}
```

- [ ] Create `pkg/memory/log.go` (adapted from `internal/infra/llm_metrics.go`):

```go
package memory

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

// RAG confidence status labels.
const (
	ragStatusHighConfidence   = "HIGH_CONFIDENCE_MATCH"
	ragStatusMediumConfidence = "MEDIUM_CONFIDENCE_MATCH"
	ragStatusLowConfidence    = "LOW_CONFIDENCE_MATCH"
	ragStatusNoResults        = "NO_RESULTS"
)

func (s *Store) logVectorSearchFailed(index string, err error, retries int) {
	reason := "unknown"
	errStr := ""
	if err != nil {
		errStr = err.Error()
		reason = errStr
		if strings.Contains(reason, "deadline exceeded") {
			reason = "deadline_exceeded"
		} else if strings.Contains(reason, "not found") || strings.Contains(reason, "NotFound") {
			reason = "index_not_found"
		} else if strings.Contains(reason, "Permission denied") || strings.Contains(reason, "permission_denied") {
			reason = "permission_denied"
		}
	}
	attrs := []any{slog.String("index", index), slog.String("reason", reason), slog.Int("retries", retries)}
	if errStr != "" {
		attrs = append(attrs, slog.String("error", errStr))
	}
	s.log.Error(fmt.Sprintf("vector search failed | index=%s | reason=%s | retries=%d", index, reason, retries), attrs...)
}

func (s *Store) logFoundNode(id string, score float64, textPreview string) {
	s.log.Debug(fmt.Sprintf("found node | id=%s | score=%.2f | text=%q", id, score, textPreview),
		slog.String("id", id), slog.Float64("score", score), slog.String("text", textPreview))
}

func (s *Store) logFoundEntry(id string, score float64, textPreview string) {
	s.log.Debug(fmt.Sprintf("found entry | id=%s | score=%.2f | text=%q", id, score, textPreview),
		slog.String("id", id), slog.Float64("score", score), slog.String("text", textPreview))
}

func (s *Store) logRAGQuality(topK int, scores []float64) {
	if len(scores) == 0 {
		s.log.Debug(fmt.Sprintf("RAG_QUALITY | top_k=%d | median_score=N/A | p90_score=N/A | status=%s", topK, ragStatusNoResults),
			slog.String("event", "RAG_QUALITY"), slog.Int("top_k", topK), slog.String("status", ragStatusNoResults))
		return
	}
	sorted := make([]float64, len(scores))
	copy(sorted, scores)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 && len(sorted) >= 2 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	p90Idx := int(0.9 * float64(len(sorted)))
	if p90Idx >= len(sorted) {
		p90Idx = len(sorted) - 1
	}
	p90 := sorted[p90Idx]
	maxScore := sorted[len(sorted)-1]
	ragStatus := ragStatusLowConfidence
	if p90 >= 0.6 {
		ragStatus = ragStatusHighConfidence
	} else if median >= 0.5 || maxScore >= 0.6 {
		ragStatus = ragStatusMediumConfidence
	}
	s.log.Debug(fmt.Sprintf("RAG_QUALITY | top_k=%d | median_score=%.2f | p90_score=%.2f | status=%s", topK, median, p90, ragStatus),
		slog.String("event", "RAG_QUALITY"), slog.Int("top_k", topK),
		slog.Float64("median_score", median), slog.Float64("p90_score", p90), slog.String("status", ragStatus))
}
```

- [ ] Commit:

```bash
cd ../jot-memory-store
git add pkg/memory/interfaces.go pkg/memory/memory.go pkg/memory/errors.go \
        pkg/memory/firestore.go pkg/memory/math.go pkg/memory/text.go pkg/memory/log.go
git commit -m "feat(memory): scaffold Store struct, interfaces, and utility files"
```

---

## Task 2: Move prompt templates into pkg/memory/prompts/

**Files:**
- Create: `pkg/memory/prompts/embed.go`
- Create: `pkg/memory/prompts/journal_analyze.go`
- Create: `pkg/memory/prompts/context_analyze.go`
- Create: `pkg/memory/prompts/executive_summary.go`
- Create: `pkg/memory/prompts/journal_analyze.txt` (copy)
- Create: `pkg/memory/prompts/context_analyze.txt` (copy)
- Create: `pkg/memory/prompts/executive_summary.txt` (copy)

- [ ] Copy the three `.txt` files:

```bash
cp internal/prompts/journal_analyze.txt pkg/memory/prompts/
cp internal/prompts/context_analyze.txt pkg/memory/prompts/
cp internal/prompts/executive_summary.txt pkg/memory/prompts/
```

- [ ] Create `pkg/memory/prompts/embed.go`:

```go
package prompts

import _ "embed"

//go:embed journal_analyze.txt
var journalAnalyzeTxt string

//go:embed context_analyze.txt
var contextAnalyzeTxt string

//go:embed executive_summary.txt
var executiveSummaryTxt string
```

- [ ] Create `pkg/memory/prompts/journal_analyze.go`:

```go
package prompts

import (
	"bytes"
	"fmt"
	"text/template"
)

// JournalAnalyzeData holds the data for the journal-analyze prompt template.
type JournalAnalyzeData struct {
	EntryID   string
	Date      string
	EntryText string
}

var journalAnalyzeTmpl = template.Must(template.New("journal_analyze").Parse(journalAnalyzeTxt))

// BuildJournalAnalyze executes the journal-analyze template.
func BuildJournalAnalyze(data JournalAnalyzeData) (string, error) {
	var buf bytes.Buffer
	if err := journalAnalyzeTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute journal analyze: %w", err)
	}
	return buf.String(), nil
}
```

- [ ] Create `pkg/memory/prompts/context_analyze.go`:

```go
package prompts

import (
	"bytes"
	"fmt"
	"text/template"
)

// ContextAnalyzeData holds the entry content for context analysis.
type ContextAnalyzeData struct {
	EntryContent string
}

var contextAnalyzeTmpl = template.Must(template.New("context_analyze").Parse(contextAnalyzeTxt))

// BuildContextAnalyze executes the context-analyze template.
func BuildContextAnalyze(data ContextAnalyzeData) (string, error) {
	var buf bytes.Buffer
	if err := contextAnalyzeTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute context analyze: %w", err)
	}
	return buf.String(), nil
}
```

- [ ] Create `pkg/memory/prompts/executive_summary.go`:

```go
package prompts

// ExecutiveSummary returns the living-context executive summary prompt.
func ExecutiveSummary() string { return executiveSummaryTxt }
```

- [ ] Commit:

```bash
git add pkg/memory/prompts/
git commit -m "feat(memory): add prompts sub-package with embedded templates"
```

---

## Task 3: Create Gemini implementations

**Files:**
- Create: `pkg/memory/gemini/embedder.go`
- Create: `pkg/memory/gemini/dispatcher.go`

- [ ] Create `pkg/memory/gemini/embedder.go`:

```go
// Package gemini provides Gemini/Vertex AI implementations of memory.Embedder and memory.LLMDispatcher.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2/google"

	"github.com/jackstrohm/jot/pkg/memory"
)

const embeddingBatchSize = 250

type embedder struct {
	projectID string
}

// NewEmbedder returns a memory.Embedder backed by Vertex AI text-embedding-005.
// projectID is the GCP project that hosts the Vertex AI endpoint.
func NewEmbedder(projectID string) memory.Embedder {
	return &embedder{projectID: projectID}
}

func (e *embedder) GenerateEmbedding(ctx context.Context, text string, taskType string) ([]float32, error) {
	if taskType == "" {
		taskType = memory.EmbedTaskRetrievalQuery
	}
	results, err := e.batch(ctx, []string{text}, taskType)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return results[0], nil
}

func (e *embedder) GenerateEmbeddingsBatch(ctx context.Context, texts []string, taskType string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if taskType == "" {
		taskType = memory.EmbedTaskRetrievalQuery
	}
	out := make([][]float32, len(texts))
	for i := 0; i < len(texts); i += embeddingBatchSize {
		end := i + embeddingBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		chunk := texts[i:end]
		vecs, err := e.batch(ctx, chunk, taskType)
		if err != nil {
			return nil, err
		}
		for j, v := range vecs {
			out[i+j] = v
		}
	}
	return out, nil
}

func (e *embedder) batch(ctx context.Context, texts []string, taskType string) ([][]float32, error) {
	endpoint := fmt.Sprintf(
		"https://us-central1-aiplatform.googleapis.com/v1/projects/%s/locations/us-central1/publishers/google/models/text-embedding-005:predict",
		e.projectID,
	)
	instances := make([]map[string]any, len(texts))
	for i, t := range texts {
		instances[i] = map[string]any{"content": t, "task_type": taskType}
	}
	body, err := json.Marshal(map[string]any{"instances": instances})
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	tokenSource, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("get token source: %w", err)
	}
	token, err := tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding API request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embedding API %d: %s", resp.StatusCode, b)
	}

	var result struct {
		Predictions []struct {
			Embeddings struct {
				Values []float32 `json:"values"`
			} `json:"embeddings"`
		} `json:"predictions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	out := make([][]float32, len(result.Predictions))
	for i, p := range result.Predictions {
		out[i] = p.Embeddings.Values
	}
	return out, nil
}
```

- [ ] Create `pkg/memory/gemini/dispatcher.go`:

```go
package gemini

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"github.com/jackstrohm/jot/pkg/memory"
)

const (
	defaultMaxOutputTokens = 8192
	minMaxOutputTokens     = 4096
)

type dispatcher struct {
	client *genai.Client
	model  string
}

// NewDispatcher returns a memory.LLMDispatcher backed by the Gemini SDK.
// model is the resolved model name (e.g. "gemini-2.0-flash").
func NewDispatcher(client *genai.Client, model string) memory.LLMDispatcher {
	return &dispatcher{client: client, model: model}
}

func (d *dispatcher) Dispatch(ctx context.Context, req memory.LLMRequest) (string, error) {
	maxOut := req.MaxTokens
	if maxOut <= 0 {
		maxOut = defaultMaxOutputTokens
	}
	if maxOut < minMaxOutputTokens {
		maxOut = minMaxOutputTokens
	}

	cfg := &genai.GenerateContentConfig{
		MaxOutputTokens: int32(maxOut),
		SafetySettings: []*genai.SafetySetting{
			{Category: genai.HarmCategoryHarassment, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategorySexuallyExplicit, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryDangerousContent, Threshold: genai.HarmBlockThresholdBlockNone},
		},
	}
	if req.JSONMode {
		cfg.ResponseMIMEType = "application/json"
	}
	if req.SystemPrompt != "" {
		cfg.SystemInstruction = &genai.Content{
			Role:  genai.RoleUser,
			Parts: []*genai.Part{{Text: req.SystemPrompt}},
		}
	}

	resp, err := d.client.Models.GenerateContent(ctx, d.model,
		genai.Text(req.UserPrompt), cfg)
	if err != nil {
		return "", fmt.Errorf("gemini dispatch: %w", err)
	}
	text := strings.TrimSpace(resp.Text())
	if text == "" {
		return "", fmt.Errorf("gemini returned empty response")
	}
	return text, nil
}
```

> **Note on `NewDispatcher` signature:** It takes `*genai.Client` (not `projectID string`) because `infra.App` already holds the Gemini client. This avoids creating a second client.

- [ ] Commit:

```bash
git add pkg/memory/gemini/
git commit -m "feat(memory): add Gemini Embedder and LLMDispatcher implementations"
```

---

## Task 4: Convert domain files — pure/formatting files

These files have functions with no `env infra.ToolEnv` parameter (or only use it for Firestore). The mechanical change: add `(s *Store)` receiver, replace `infra.X` calls with local equivalents, remove `infra` import.

**Files:** `entry_format.go`, `query_format.go`, `rag.go`, `schema.go`

- [ ] In each file, for every exported function: add `(s *Store)` receiver if it needs db/embedder/llm, OR keep as package-level if it's a pure utility (no receiver needed). Check: `entry_format.go` and `query_format.go` — these just format slices of structs, no env needed, keep as package-level functions. `rag.go` — `FuseKnowledgeNodes` and `FuseEntries` are pure, keep as package-level. `schema.go` — `ValidateMetadata`, `NormalizeMetadata`, `ParseSPOTriple`, etc. are pure, keep as package-level.

- [ ] Verify none of these files import `internal/infra`:

```bash
grep "internal/infra" pkg/memory/entry_format.go pkg/memory/query_format.go pkg/memory/rag.go pkg/memory/schema.go
```

Expected: no output (these are already infra-free). If any do import infra, apply the receiver pattern and replace usages.

- [ ] Replace `infra.CosineSimilarity` with `cosineSimilarity` in `rag.go` if present.

- [ ] `entry_format.go` and `query_format.go` import `pkg/utils`. Replace every `utils.TruncateString(...)` → `truncateString(...)`, `utils.SanitizePrompt(...)` → `sanitizePrompt(...)`, etc. Then remove the `pkg/utils` import from both files.

```bash
grep "pkg/utils" pkg/memory/entry_format.go pkg/memory/query_format.go
```

Expected: no output after replacement.

- [ ] Commit:

```bash
git add pkg/memory/entry_format.go pkg/memory/query_format.go pkg/memory/rag.go pkg/memory/schema.go
git commit -m "refactor(memory): replace pkg/utils calls with local copies in pure files"
```

---

## Task 5: Convert `entry_nodes.go` and `entry_nodes_extended.go`

**Pattern for every file in Tasks 5–11:**
1. Add `(s *Store)` receiver to every exported function
2. Replace `env infra.ToolEnv` parameter with nothing (use `s.db`, `s.embedder`, `s.llm`, `s.log`)
3. Replace `infra.GetStringField(...)` → `getStringField(...)`
4. Replace `infra.GetStringSliceField(...)` → `getStringSliceField(...)`
5. Replace `infra.GetFloat32SliceField(...)` → `getFloat32SliceField(...)`
6. Replace `infra.QueryDocuments(...)` → `queryDocuments(...)`
7. Replace `infra.GenerateUUID()` → `generateUUID()`
8. Replace `infra.WrapFirestoreIndexError(...)` → `wrapFirestoreIndexError(...)`
9. Replace `infra.GenerateEmbedding(ctx, cfg.GoogleCloudProject, ...)` → `s.embedder.GenerateEmbedding(ctx, ...)`
10. Replace `infra.GenerateEmbeddingsBatch(ctx, cfg.GoogleCloudProject, ...)` → `s.embedder.GenerateEmbeddingsBatch(ctx, ...)`
11. Replace `infra.LoggerFrom(ctx)` → `s.log`
12. Replace `infra.LogFoundEntry(...)` / `infra.LogFoundNode(...)` → `s.logFoundEntry(...)` / `s.logFoundNode(...)`
13. Replace `infra.LogVectorSearchFailed(...)` → `s.logVectorSearchFailed(...)`
14. Replace `infra.LogRAGQuality(...)` → `s.logRAGQuality(...)`
15. Replace `infra.StartSpan(ctx, ...)` / `span.End()` → remove entirely (no spans in library)
16. Remove `env.Config().GoogleCloudProject` — project ID now inside embedder
17. Remove the `internal/infra` import

**Files:** `pkg/memory/entry_nodes.go`, `pkg/memory/entry_nodes_extended.go`

- [ ] Apply the pattern above to `entry_nodes.go`
- [ ] Apply the pattern above to `entry_nodes_extended.go`
- [ ] Verify no infra import remains:

```bash
grep "internal/infra" pkg/memory/entry_nodes.go pkg/memory/entry_nodes_extended.go
```

- [ ] Commit:

```bash
git add pkg/memory/entry_nodes.go pkg/memory/entry_nodes_extended.go
git commit -m "refactor(memory): convert entry_nodes files to Store methods"
```

---

## Task 6: Convert `query_nodes.go`, `graph.go`, `rollup.go`, `incubation.go`, `janitor.go`

Apply the same pattern from Task 5 to each file.

- [ ] Convert `query_nodes.go`
- [ ] Convert `graph.go`
- [ ] Convert `rollup.go`
- [ ] Convert `incubation.go`
- [ ] Convert `janitor.go`
- [ ] Verify:

```bash
grep "internal/infra" pkg/memory/query_nodes.go pkg/memory/graph.go pkg/memory/rollup.go pkg/memory/incubation.go pkg/memory/janitor.go
```

- [ ] Commit:

```bash
git add pkg/memory/query_nodes.go pkg/memory/graph.go pkg/memory/rollup.go pkg/memory/incubation.go pkg/memory/janitor.go
git commit -m "refactor(memory): convert query_nodes, graph, rollup, incubation, janitor to Store methods"
```

---

## Task 7: Convert `pending.go` (with Telegram rename)

Apply the pattern from Task 5, plus rename the Telegram functions:

- [ ] Apply the standard conversion pattern
- [ ] Rename: `GetTelegramActiveQuestion` → `GetActiveQuestion(ctx, clientID string)`
- [ ] Rename: `SetTelegramActiveQuestion` → `SetActiveQuestion(ctx, clientID, questionUUID string)`
- [ ] Rename: `ClearTelegramActiveQuestion` → `ClearActiveQuestion(ctx, clientID string)`
- [ ] Rename constant: `TelegramQuestionStateCollection` → `ActiveQuestionStateCollection`
- [ ] Verify:

```bash
grep "internal/infra\|Telegram" pkg/memory/pending.go
```

Expected: no output.

- [ ] Commit:

```bash
git add pkg/memory/pending.go
git commit -m "refactor(memory): convert pending.go to Store methods, generalize Telegram functions"
```

---

## Task 8: Convert `analysis.go`, `context.go`, `task_engine.go`, `task_nodes.go`, `task_query.go`, `rerank.go`, `migrate.go`

Apply the pattern from Task 5. Additional notes for these files:

**`analysis.go`, `context.go`, `task_engine.go`** — replace `infra.LLMRequest{Parts: []*genai.Part{{Text: ...}}}` + `env.Dispatch(ctx, req)` + `infra.ExtractText(resp)` with:
```go
text, err := s.llm.Dispatch(ctx, memory.LLMRequest{
    SystemPrompt: systemPrompt,
    UserPrompt:   userPrompt,
    MaxTokens:    N,
})
```

**`analysis.go`, `context.go`** — replace `prompts.BuildJournalAnalyze(...)` / `prompts.BuildContextAnalyze(...)` / `prompts.ExecutiveSummary()` with the new `memoryprompts "github.com/jackstrohm/jot/pkg/memory/prompts"` import and `memoryprompts.BuildJournalAnalyze(...)` etc.

**`task_nodes.go`, `rerank.go`** — these use `infra.GenerateContentSimple(ctx, env, sysPrompt, userPrompt, cfg, genConfig)`. Replace with `s.llm.Dispatch(ctx, memory.LLMRequest{SystemPrompt: ..., UserPrompt: ..., MaxTokens: ...})`.

**`migrate.go`** — currently takes `*firestore.Client` directly; after conversion it takes no extra parameter (uses `s.db`).

- [ ] Convert `analysis.go`
- [ ] Convert `context.go`
- [ ] Convert `task_engine.go`
- [ ] Convert `task_nodes.go`
- [ ] Convert `task_query.go`
- [ ] Convert `rerank.go`
- [ ] Convert `migrate.go`
- [ ] Verify:

```bash
grep "internal/infra\|internal/prompts" pkg/memory/analysis.go pkg/memory/context.go \
  pkg/memory/task_engine.go pkg/memory/task_nodes.go pkg/memory/task_query.go \
  pkg/memory/rerank.go pkg/memory/migrate.go
```

Expected: no output.

- [ ] Commit:

```bash
git add pkg/memory/analysis.go pkg/memory/context.go pkg/memory/task_engine.go \
        pkg/memory/task_nodes.go pkg/memory/task_query.go pkg/memory/rerank.go \
        pkg/memory/migrate.go
git commit -m "refactor(memory): convert LLM-dispatch files to Store methods"
```

---

## Task 9: Convert `knowledge.go` (largest file, includes `evaluateFactCollision`)

Apply the pattern from Task 5, plus:

- [ ] Add private method `evaluateFactCollision` to `knowledge.go` (replaces `infra.EvaluateFactCollision`):

```go
const factCollisionSystemPrompt = `You are a logic engine. Compare New Fact to Existing Fact. If they mean the exact same thing or New Fact is a direct update to Existing Fact, return 'update'. If they contradict each other or refer to different specific details, return 'insert'. If Existing Fact is empty, return 'update'. Reply with ONLY 'update' or 'insert'.`

func (s *Store) evaluateFactCollision(ctx context.Context, newFact, existingFact string) (string, error) {
	userPrompt := fmt.Sprintf("New Fact:\n%s\n\nExisting Fact:\n%s",
		wrapAsUserData(newFact), wrapAsUserData(existingFact))
	text, err := s.llm.Dispatch(ctx, LLMRequest{SystemPrompt: factCollisionSystemPrompt, UserPrompt: userPrompt, MaxTokens: 16})
	if err != nil {
		return "", err
	}
	if strings.Contains(strings.ToLower(text), "update") {
		return "update", nil
	}
	return "insert", nil
}
```

- [ ] Replace the two `infra.EvaluateFactCollision(ctx, env, cfg, ...)` call sites with `s.evaluateFactCollision(ctx, ...)`
- [ ] Apply the full Task 5 pattern to all other functions in `knowledge.go`
- [ ] Verify:

```bash
grep "internal/infra" pkg/memory/knowledge.go
```

Expected: no output.

- [ ] Commit:

```bash
git add pkg/memory/knowledge.go
git commit -m "refactor(memory): convert knowledge.go to Store methods, inline evaluateFactCollision"
```

---

## Task 10: Verify pkg/memory compiles clean

- [ ] Run:

```bash
cd ../jot-memory-store
go build ./pkg/memory/...
```

Expected: success. Fix any remaining `infra` references if found.

- [ ] Confirm no internal/infra or pkg/utils imports remain:

```bash
grep -r "internal/infra\|jackstrohm/jot/pkg/utils" pkg/memory/ --include="*.go" | grep -v "_test.go"
```

Expected: no output.

- [ ] Commit if any fixes were needed.

---

## Task 11: Update test files to use *Store directly

The test files currently construct a mock or real `infra.ToolEnv`. Update them to construct a `*memory.Store` directly.

**Files:** `pkg/memory/*_test.go`

For tests that use a real Firestore emulator, the pattern becomes:
```go
// Before
env := infra.NewTestEnv(t)
result, err := memory.GetEntry(ctx, env, id)

// After
store := memory.New(testFirestoreClient(t), fakeEmbedder{}, fakeDispatcher{})
result, err := store.GetEntry(ctx, id)
```

Where `fakeEmbedder` and `fakeDispatcher` are simple test doubles:
```go
type fakeEmbedder struct{}
func (f fakeEmbedder) GenerateEmbedding(_ context.Context, _ string, _ string) ([]float32, error) {
    return make([]float32, 768), nil
}
func (f fakeEmbedder) GenerateEmbeddingsBatch(_ context.Context, texts []string, _ string) ([][]float32, error) {
    out := make([][]float32, len(texts))
    for i := range out { out[i] = make([]float32, 768) }
    return out, nil
}

type fakeDispatcher struct{ response string }
func (f fakeDispatcher) Dispatch(_ context.Context, _ memory.LLMRequest) (string, error) {
    if f.response != "" { return f.response, nil }
    return "update", nil
}
```

Add these fakes to a `pkg/memory/testhelpers_test.go` file.

- [ ] Create `pkg/memory/testhelpers_test.go` with `fakeEmbedder` and `fakeDispatcher`
- [ ] Update each `*_test.go` file to construct `*Store` instead of using `infra.ToolEnv`
- [ ] `pending_dedup_test.go` is `package memory_test` (external test package) and calls `utils.CosineSimilarity` directly. It cannot access the unexported `cosineSimilarity`. Leave its `pkg/utils` import intact — test files may have their own imports without affecting the library's public API.
- [ ] Run tests (they will fail until callers are wired up, but package-level compile should pass):

```bash
go test ./pkg/memory/... -count=1
```

- [ ] Commit:

```bash
git add pkg/memory/
git commit -m "test(memory): update tests to construct *Store directly"
```

---

## Task 12: Wire Store into infra.App

**File:** `internal/infra/app.go`

- [ ] Add `Memory *memory.Store` field to `App` struct:

```go
import "github.com/jackstrohm/jot/pkg/memory"
import memorygem "github.com/jackstrohm/jot/pkg/memory/gemini"

type App struct {
    // existing fields...
    Memory *memory.Store
}
```

- [ ] In `NewApp`, after Firestore and Gemini clients are created, construct the Store:

```go
// After: app.geminiClient, ... = factory(ctx, cfg)
memEmbedder := memorygem.NewEmbedder(cfg.GoogleCloudProject)
memDispatcher := memorygem.NewDispatcher(app.geminiClient, app.effectiveGeminiModel)
app.Memory = memory.New(app.firestoreClient, memEmbedder, memDispatcher,
    memory.WithLogger(app.Logger))
```

> Place this after the `if app.geminiErr != nil { return app, app.geminiErr }` check so we only construct Memory when both clients are healthy.

- [ ] Verify `internal/infra` compiles:

```bash
go build ./internal/infra/...
```

- [ ] Commit:

```bash
git add internal/infra/app.go
git commit -m "feat(infra): wire memory.Store into infra.App"
```

---

## Task 13: Migrate callers — tools/impl

These are the most frequent callers. Pattern: `memory.FuncName(ctx, env, ...)` → `env.App().Memory.FuncName(ctx, ...)` or `app.Memory.FuncName(ctx, ...)` depending on what the tool impl receives.

**Files:** all files in `internal/tools/impl/`

- [ ] For each file, find all `memory.X(ctx, env, ...)` calls and replace with `app.Memory.X(ctx, ...)` where `app` is the `*infra.App` available in scope.
- [ ] For `memory.GetTelegramActiveQuestion` / `SetTelegramActiveQuestion` / `ClearTelegramActiveQuestion` — replace with `GetActiveQuestion` / `SetActiveQuestion` / `ClearActiveQuestion`.
- [ ] Compile check after each file:

```bash
go build ./internal/tools/...
```

- [ ] Commit:

```bash
git add internal/tools/
git commit -m "refactor(tools): migrate callers to app.Memory Store methods"
```

---

## Task 14: Migrate callers — agent, api, service, cmd

**Files:** all files in `internal/agent/`, `internal/api/`, `internal/service/`, `cmd/`

Apply the same pattern as Task 13. For each call site:
- `memory.FuncName(ctx, env, ...)` → `app.Memory.FuncName(ctx, ...)`
- `memory.GetTelegramActiveQuestion(ctx, env, chatID)` → `app.Memory.GetActiveQuestion(ctx, strconv.FormatInt(chatID, 10))`

- [ ] Migrate `internal/agent/` files
- [ ] Migrate `internal/api/` files
- [ ] Migrate `internal/service/` files (note Telegram type conversion)
- [ ] Migrate `cmd/` files

- [ ] Full compile check:

```bash
go build ./...
```

Fix any remaining errors.

- [ ] Run tests:

```bash
go test ./... -count=1 -timeout 120s
```

- [ ] Commit:

```bash
git add internal/agent/ internal/api/ internal/service/ cmd/
git commit -m "refactor: migrate all callers to app.Memory Store methods"
```

---

## Task 15: Final cleanup and verification

- [ ] Confirm no package-level `memory.X(ctx, env, ...)` calls remain:

```bash
grep -r "memory\.[A-Z][a-zA-Z]*(ctx, " --include="*.go" . | grep -v "_test.go" | grep -v "pkg/memory/"
```

Expected: only legitimate non-migrated calls (e.g. type assertions, struct literals). Investigate any hits.

- [ ] Confirm no `internal/infra` imports in `pkg/memory`:

```bash
grep -r "internal/infra\|jackstrohm/jot/pkg/utils" pkg/memory/ --include="*.go"
```

Expected: no output.

- [ ] Full test run:

```bash
go test ./... -count=1 -timeout 120s
```

- [ ] Commit any final fixes, then open PR from `feature/memory-store` → `main`.
