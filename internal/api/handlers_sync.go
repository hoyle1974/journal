package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var googleDocIDRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

const (
	syncLockDocument  = "sync_lock"
	syncStateDocument = "sync_state"
	syncLockTimeout   = 15 * time.Minute
)

func sanitizeResponseForDoc(response string) string {
	lines := strings.Split(response, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(strings.ToLower(line))
		if trimmed == "done." || trimmed == "done" {
			lines[i] = "[logged]."
		}
	}
	return strings.Join(lines, "\n")
}

func docIndexLen(s string) int64 {
	return int64(len(utf16.Encode([]rune(s))))
}

type syncLine struct {
	elem *docs.ParagraphElement
	text string
	kind int
}

type syncBlock struct {
	text       string
	startIndex int64
	endIndex   int64
	kind       int
}

func syncCreateDocsService(ctx context.Context, cfg *config.Config) (*docs.Service, error) {
	if cfg != nil && cfg.ServiceAccountFile != "" {
		return docs.NewService(ctx, option.WithCredentialsFile(cfg.ServiceAccountFile))
	}
	return docs.NewService(ctx)
}

func syncFetchDoc(ctx context.Context, docsService *docs.Service, documentID string) (*docs.Document, error) {
	return docsService.Documents.Get(documentID).Do()
}

func syncFetchDocErrMessage(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	htmlErr := strings.Contains(s, "400") && (strings.Contains(s, "DOCTYPE") || strings.Contains(s, "<html"))
	pageNotFound := strings.Contains(s, "Page Not Found") || strings.Contains(s, "unable to open the file")
	if htmlErr || pageNotFound {
		return "Document not found or access denied. Share the Google Doc with the service account (see log line 'sync fetch identity' for the email) and ensure DOCUMENT_ID is the ID from docs.google.com/document/d/ID/edit."
	}
	if strings.Contains(s, "403") || strings.Contains(s, "Forbidden") {
		return "Access denied to document. Share the Google Doc with the service account (see log line 'sync fetch identity' for the email)."
	}
	if strings.Contains(s, "404") || strings.Contains(s, "Not Found") {
		return "Document not found. Check DOCUMENT_ID and that the document has not been deleted."
	}
	return ""
}

func syncIdentityForLog(cfg *config.Config) string {
	if cfg != nil && cfg.ServiceAccountFile != "" {
		return "SERVICE_ACCOUNT_FILE=" + cfg.ServiceAccountFile
	}
	if metadata.OnGCE() {
		if email, err := metadata.Email("default"); err == nil && email != "" {
			return "Share the Google Doc with this account: " + email
		}
	}
	return "Application Default Credentials (share the Google Doc with the Cloud Run service account email from GCP Console > Cloud Run > jot-api-go > Security)"
}

func findSyncDoneTrigger(doc *docs.Document) (elem *docs.TextRun, startIndex, endIndex int64) {
	if doc.Body == nil {
		return nil, 0, 0
	}
	for _, element := range doc.Body.Content {
		if element.Paragraph == nil {
			continue
		}
		for _, e := range element.Paragraph.Elements {
			if e.TextRun == nil {
				continue
			}
			text := strings.TrimSpace(strings.ToLower(e.TextRun.Content))
			if (text == "done." || text == "done") && (e.TextRun.TextStyle == nil || !e.TextRun.TextStyle.Bold) {
				return e.TextRun, e.StartIndex, e.EndIndex
			}
		}
	}
	return nil, 0, 0
}

func collectSyncBlock(doc *docs.Document, beforeEndIndex int64) *syncBlock {
	var lines []syncLine
	if doc.Body == nil {
		return nil
	}
	for _, element := range doc.Body.Content {
		if element.Paragraph == nil {
			continue
		}
		for _, e := range element.Paragraph.Elements {
			if e.TextRun == nil || e.StartIndex >= beforeEndIndex {
				continue
			}
			textContent := e.TextRun.Content
			if textContent == "" {
				continue
			}
			text := strings.TrimSpace(textContent)
			if text == "" || (e.TextRun.TextStyle != nil && e.TextRun.TextStyle.Bold) {
				continue
			}
			kind := 0
			if strings.HasPrefix(text, "?") {
				kind = 1
			} else if strings.HasPrefix(text, "!") {
				kind = 2
			}
			lines = append(lines, syncLine{elem: e, text: text, kind: kind})
		}
	}
	if len(lines) == 0 {
		return nil
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].elem.StartIndex < lines[j].elem.StartIndex })
	parts := make([]string, len(lines))
	var startIndex, endIndex int64 = lines[0].elem.StartIndex, lines[0].elem.EndIndex
	for i, l := range lines {
		parts[i] = l.text
		if l.elem.StartIndex < startIndex {
			startIndex = l.elem.StartIndex
		}
		if l.elem.EndIndex > endIndex {
			endIndex = l.elem.EndIndex
		}
	}
	kind := lines[0].kind
	return &syncBlock{
		text:       strings.Join(parts, "\n"),
		startIndex: startIndex,
		endIndex:   endIndex,
		kind:       kind,
	}
}

func buildSyncRequests(ctx context.Context, s *Server, doneStartIndex, doneEndIndex int64, block *syncBlock) ([]*docs.Request, int, int, int) {
	var requests []*docs.Request
	requests = append(requests,
		&docs.Request{
			InsertText: &docs.InsertTextRequest{
				Location: &docs.Location{Index: doneEndIndex - 1},
				Text:     " [processed]",
			},
		},
		&docs.Request{
			UpdateTextStyle: &docs.UpdateTextStyleRequest{
				Range:     &docs.Range{StartIndex: doneStartIndex, EndIndex: doneEndIndex},
				TextStyle: &docs.TextStyle{Bold: true},
				Fields:    "bold",
			},
		},
	)
	entriesAdded := 0
	questionsAnswered := 0
	actionsExecuted := 0
	if block == nil || block.text == "" {
		return requests, entriesAdded, questionsAnswered, actionsExecuted
	}
	text := strings.TrimSpace(block.text)
	source := "gdoc"
	switch block.kind {
	case 1:
		question := strings.TrimPrefix(text, "?")
		question = strings.TrimSpace(question)
		if question != "" {
			infra.LoggerFrom(ctx).Info("processing question", "question", utils.TruncateString(question, 80))
			queryStart := time.Now()
			answer := s.Backend.RunQuery(ctx, question, source).Answer
			infra.LoggerFrom(ctx).Info("question answered", "duration_ms", time.Since(queryStart).Milliseconds())
			inserted := "\n" + sanitizeResponseForDoc(answer)
			requests = append(requests,
				&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.startIndex, EndIndex: block.endIndex}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
				&docs.Request{InsertText: &docs.InsertTextRequest{Location: &docs.Location{Index: block.endIndex}, Text: inserted}},
				&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.endIndex, EndIndex: block.endIndex + docIndexLen(inserted)}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
			)
			questionsAnswered++
		}
	case 2:
		action := strings.TrimPrefix(text, "!")
		action = strings.TrimSpace(action)
		if action != "" {
			infra.LoggerFrom(ctx).Info("processing action", "action", utils.TruncateString(action, 80))
			actionStart := time.Now()
			result := s.Backend.RunQuery(ctx, "Execute this action and confirm what you did: "+action, source).Answer
			infra.LoggerFrom(ctx).Info("action executed", "duration_ms", time.Since(actionStart).Milliseconds())
			inserted := "\n✓ " + sanitizeResponseForDoc(result)
			requests = append(requests,
				&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.startIndex, EndIndex: block.endIndex}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
				&docs.Request{InsertText: &docs.InsertTextRequest{Location: &docs.Location{Index: block.endIndex}, Text: inserted}},
				&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.endIndex, EndIndex: block.endIndex + docIndexLen(inserted)}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
			)
			actionsExecuted++
		}
	default:
		infra.LoggerFrom(ctx).Info("processing input", "text", utils.TruncateString(text, 80))
		processStart := time.Now()
		response := s.Backend.RunQuery(ctx, text, source).Answer
		infra.LoggerFrom(ctx).Info("input processed", "duration_ms", time.Since(processStart).Milliseconds())
		inserted := "\n→ " + sanitizeResponseForDoc(response)
		requests = append(requests,
			&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.startIndex, EndIndex: block.endIndex}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
			&docs.Request{InsertText: &docs.InsertTextRequest{Location: &docs.Location{Index: block.endIndex}, Text: inserted}},
			&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.endIndex, EndIndex: block.endIndex + docIndexLen(inserted)}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
		)
		entriesAdded++
	}
	return requests, entriesAdded, questionsAnswered, actionsExecuted
}

func syncApplyBatchUpdate(docsService *docs.Service, documentID string, requests []*docs.Request) error {
	_, err := docsService.Documents.BatchUpdate(documentID, &docs.BatchUpdateDocumentRequest{
		Requests: requests,
	}).Do()
	return err
}

func acquireSyncLock(ctx context.Context, backend Backend) (bool, error) {
	client, err := backend.GetFirestoreClient(ctx)
	if err != nil || client == nil {
		return true, nil
	}
	coll := backend.SystemCollection()
	lockRef := client.Collection(coll).Doc(syncLockDocument)
	err = client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(lockRef)
		now := time.Now()
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return tx.Set(lockRef, map[string]interface{}{
					"locked_at": now,
					"locked_by": "sync",
				})
			}
			return err
		}
		if lockedAt, ok := doc.Data()["locked_at"].(time.Time); ok {
			if now.Sub(lockedAt) > syncLockTimeout {
				return tx.Set(lockRef, map[string]interface{}{
					"locked_at": now,
					"locked_by": "sync",
				})
			}
		}
		return fmt.Errorf("lock held")
	})
	if err != nil {
		if err.Error() == "lock held" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func releaseSyncLock(ctx context.Context, backend Backend) {
	client, err := backend.GetFirestoreClient(ctx)
	if err != nil || client == nil {
		return
	}
	lockRef := client.Collection(backend.SystemCollection()).Doc(syncLockDocument)
	_, err = lockRef.Delete(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to release sync lock", "error", err)
	}
}

func handleSync(s *Server, w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	ctx = s.Backend.WithSyncInProgress(ctx)

	ctx, span := infra.StartSpan(ctx, "sync.gdoc")
	defer span.End()

	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	if s.Config.DocumentID == "" {
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", "DOCUMENT_ID not configured")
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "DOCUMENT_ID not configured"})
		return
	}
	documentID := strings.TrimSpace(s.Config.DocumentID)
	if !googleDocIDRe.MatchString(documentID) {
		infra.LoggerFrom(ctx).Error("sync failed", "stage", "Config", "reason", "invalid DOCUMENT_ID format")
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "invalid DOCUMENT_ID format")
		WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "DOCUMENT_ID must be the document ID only (e.g. from docs.google.com/document/d/ID/edit), not a full URL or path.",
		})
		return
	}

	lockAcquired, lockErr := acquireSyncLock(ctx, s.Backend)
	if lockErr != nil {
		infra.LoggerFrom(ctx).Error("failed to check sync lock", "error", lockErr)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", "Failed to check sync lock")
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to check sync lock"})
		return
	}
	if !lockAcquired {
		infra.LoggerFrom(ctx).Info("sync skipped", "reason", "already in progress")
		LogHandlerResponse(ctx, r.Method, path, http.StatusConflict, "error", "sync already in progress")
		WriteJSON(w, http.StatusConflict, map[string]string{"error": "Another sync is already in progress. Please wait and try again."})
		return
	}
	defer releaseSyncLock(ctx, s.Backend)

	docsService, err := syncCreateDocsService(ctx, s.Config)
	if err != nil {
		span.RecordError(err)
		infra.LoggerFrom(ctx).Error("sync failed", "stage", "DocsService", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to create Docs service: %v", err)})
		return
	}

	docFetchStart := time.Now()
	doc, err := syncFetchDoc(ctx, docsService, documentID)
	if err != nil {
		span.RecordError(err)
		infra.LoggerFrom(ctx).Error("sync failed", "stage", "FetchDoc", "error", err)
		infra.LoggerFrom(ctx).Info("sync fetch identity", "identity", syncIdentityForLog(s.Config))
		msg := fmt.Sprintf("Failed to fetch document: %v", err)
		if hint := syncFetchDocErrMessage(err); hint != "" {
			infra.LoggerFrom(ctx).Info("sync fetch hint", "hint", hint)
			msg = hint
		}
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", msg)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
		return
	}
	infra.LoggerFrom(ctx).Debug("doc fetched", "duration_ms", time.Since(docFetchStart).Milliseconds())

	if doc.Body == nil || len(doc.Body.Content) == 0 {
		infra.LoggerFrom(ctx).Info("sync skipped", "reason", "document has no body")
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "processed", 0, "reason", "no body")
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"message":   "Document has no body",
			"processed": 0,
		})
		return
	}

	doneElem, doneStartIndex, doneEndIndex := findSyncDoneTrigger(doc)
	if doneElem == nil {
		infra.LoggerFrom(ctx).Info("sync skipped", "reason", "no done. trigger")
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "processed", 0, "reason", "no done trigger")
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"message":   "No 'done.' trigger found",
			"processed": 0,
		})
		return
	}

	infra.LoggerFrom(ctx).Info("found 'done.' trigger, processing document")

	// Collect only content before "done." so the trigger text is not included in the parsed block.
	block := collectSyncBlock(doc, doneStartIndex)
	blockToProcess := block
	if block != nil && strings.TrimSpace(block.text) != "" {
		h := sha256.Sum256([]byte(block.text))
		blockHash := hex.EncodeToString(h[:])
		fsClient, err := s.Backend.GetFirestoreClient(ctx)
		if err == nil && fsClient != nil {
			stateRef := fsClient.Collection(s.Backend.SystemCollection()).Doc(syncStateDocument)
			stateDoc, err := stateRef.Get(ctx)
			if err == nil && stateDoc.Exists() {
				if lastHash, ok := stateDoc.Data()["last_block_hash"].(string); ok && lastHash == blockHash {
					infra.LoggerFrom(ctx).Info("sync skipped", "reason", "duplicate block (already processed)")
					blockToProcess = nil
				}
			}
		}
	}
	requests, entriesAdded, questionsAnswered, actionsExecuted := buildSyncRequests(ctx, s, doneStartIndex, doneEndIndex, blockToProcess)

	if len(requests) > 0 {
		updateStart := time.Now()
		err = syncApplyBatchUpdate(docsService, documentID, requests)
		if err != nil {
			infra.LoggerFrom(ctx).Error("doc update failed", "error", err)
			span.RecordError(err)
			LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
			WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to update document: %v", err)})
			return
		}
		infra.LoggerFrom(ctx).Debug("doc updated", "duration_ms", time.Since(updateStart).Milliseconds())
		if blockToProcess != nil && strings.TrimSpace(blockToProcess.text) != "" {
			h := sha256.Sum256([]byte(blockToProcess.text))
			blockHash := hex.EncodeToString(h[:])
			fsClient, err := s.Backend.GetFirestoreClient(ctx)
			if err == nil && fsClient != nil {
				_, err = fsClient.Collection(s.Backend.SystemCollection()).Doc(syncStateDocument).Set(ctx, map[string]interface{}{
					"last_block_hash":   blockHash,
					"last_processed_at": time.Now(),
				})
				if err != nil {
					infra.LoggerFrom(ctx).Warn("failed to store sync_state", "error", err)
				}
			}
		}
	}

	totalTime := time.Since(startTime)
	totalProcessed := entriesAdded + questionsAnswered + actionsExecuted
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK,
		"success", true,
		"entries_added", entriesAdded,
		"questions_answered", questionsAnswered,
		"actions_executed", actionsExecuted,
		"total_processed", totalProcessed,
		"duration_ms", totalTime.Milliseconds(),
	)

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success":            true,
		"entries_added":      entriesAdded,
		"questions_answered": questionsAnswered,
		"actions_executed":   actionsExecuted,
		"total_processed":    totalProcessed,
	})
}
