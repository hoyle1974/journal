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
	"google.golang.org/protobuf/types/known/timestamppb"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	"cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"github.com/google/uuid"
)

var googleDocIDRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

const (
	syncLockDocument   = "sync_lock"
	syncStateDocument  = "sync_state"
	syncLockTimeout    = 15 * time.Minute
)

func sanitizeResponseForDoc(response string) string {
	lines := strings.Split(response, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(strings.ToLower(line))
		if trimmed == "done." || trimmed == "done" {
			lines[i] = "[logged]"
		} else if trimmed == "logged." || trimmed == "logged" {
			lines[i] = "[logged]"
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

// findSyncDoneTriggerInElements finds the first non-bold line that is "done." or "done" (any case).
// Recurses into table cells. The "done." phrase is used only as the trigger and is not included in the processed block.
func findSyncDoneTriggerInElements(ctx context.Context, content []*docs.StructuralElement) (elem *docs.TextRun, startIndex, endIndex int64) {
	if content == nil {
		return nil, 0, 0
	}
	for i, element := range content {
		if element.Paragraph != nil {
			for j, e := range element.Paragraph.Elements {
				if e.TextRun == nil {
					continue
				}
				raw := e.TextRun.Content
				text := strings.TrimSpace(strings.ToLower(raw))
				isBold := e.TextRun.TextStyle != nil && e.TextRun.TextStyle.Bold
				isMatch := (text == "done." || text == "done") && !isBold
				infra.LoggerFrom(ctx).Debug("sync trigger scan",
					"element", i, "run", j,
					"text_preview", utils.TruncateString(text, 60),
					"text_len", len(text), "bold", isBold, "is_match", isMatch)
				if isMatch {
					infra.LoggerFrom(ctx).Debug("sync trigger found", "start_index", e.StartIndex, "end_index", e.EndIndex)
					return e.TextRun, e.StartIndex, e.EndIndex
				}
			}
			continue
		}
		if element.Table != nil {
			rows := len(element.Table.TableRows)
			cells := 0
			for _, row := range element.Table.TableRows {
				if row != nil {
					cells += len(row.TableCells)
				}
			}
			infra.LoggerFrom(ctx).Debug("sync trigger scan", "element", i, "type", "table", "rows", rows, "cells", cells)
			for _, row := range element.Table.TableRows {
				if row == nil {
					continue
				}
				for _, cell := range row.TableCells {
					if cell == nil || len(cell.Content) == 0 {
						continue
					}
					if run, s, e := findSyncDoneTriggerInElements(ctx, cell.Content); run != nil {
						return run, s, e
					}
				}
			}
		}
	}
	return nil, 0, 0
}

func findSyncDoneTrigger(ctx context.Context, doc *docs.Document) (elem *docs.TextRun, startIndex, endIndex int64) {
	if doc.Body == nil {
		return nil, 0, 0
	}
	content := doc.Body.Content
	infra.LoggerFrom(ctx).Debug("sync trigger scan start", "body_elements", len(content))
	return findSyncDoneTriggerInElements(ctx, content)
}

func collectSyncLinesFromElements(content []*docs.StructuralElement, beforeEndIndex int64, lines *[]syncLine) {
	if content == nil || lines == nil {
		return
	}
	for _, element := range content {
		if element.Paragraph != nil {
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
				*lines = append(*lines, syncLine{elem: e, text: text, kind: kind})
			}
			continue
		}
		if element.Table != nil {
			for _, row := range element.Table.TableRows {
				if row == nil {
					continue
				}
				for _, cell := range row.TableCells {
					if cell == nil || len(cell.Content) == 0 {
						continue
					}
					collectSyncLinesFromElements(cell.Content, beforeEndIndex, lines)
				}
			}
		}
	}
}

func collectSyncBlock(doc *docs.Document, beforeEndIndex int64) *syncBlock {
	var lines []syncLine
	if doc.Body == nil {
		return nil
	}
	collectSyncLinesFromElements(doc.Body.Content, beforeEndIndex, &lines)
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
				Location: &docs.Location{Index: doneEndIndex},
				Text:     "\n[processed]",
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
			answer := s.Agent.RunQuery(ctx, question, source).Answer
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
			result := s.Agent.RunQuery(ctx, "Execute this action and confirm what you did: "+action, source).Answer
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
		response := s.Agent.RunQuery(ctx, text, source).Answer
		infra.LoggerFrom(ctx).Info("input processed", "duration_ms", time.Since(processStart).Milliseconds())
		sanitized := sanitizeResponseForDoc(response)
		inserted := "\n→ " + sanitized
		if strings.TrimSpace(sanitized) == "[logged]" || strings.TrimSpace(sanitized) == "[logged]." {
			inserted = "\n[logged]"
		}
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

func acquireSyncLock(ctx context.Context, s *Server) (bool, error) {
	client, err := s.App.Firestore(ctx)
	if err != nil || client == nil {
		return true, nil
	}
	lockRef := client.Collection(infra.SystemCollection).Doc(syncLockDocument)
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

func releaseSyncLock(ctx context.Context, s *Server) {
	client, err := s.App.Firestore(ctx)
	if err != nil || client == nil {
		return
	}
	lockRef := client.Collection(infra.SystemCollection).Doc(syncLockDocument)
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
	ctx = infra.WithSyncInProgress(ctx)

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

	lockAcquired, lockErr := acquireSyncLock(ctx, s)
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
	defer releaseSyncLock(ctx, s)

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
	infra.LoggerFrom(ctx).Debug("doc fetched", "duration_ms", time.Since(docFetchStart).Milliseconds(),
		"body_elements", len(doc.Body.Content))

	// Triage: log doc identity so we can confirm server is reading the same doc and version as the user sees.
	docURL := "https://docs.google.com/document/d/" + documentID + "/edit"
	infra.LoggerFrom(ctx).Info("sync doc triage",
		"document_id_requested", documentID,
		"document_id_from_api", doc.DocumentId,
		"title", doc.Title,
		"revision_id", doc.RevisionId,
		"doc_url", docURL,
		"body_elements", len(doc.Body.Content))

	if doc.Body == nil || len(doc.Body.Content) == 0 {
		infra.LoggerFrom(ctx).Info("sync skipped", "reason", "document has no body")
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "processed", 0, "reason", "no body")
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"message":   "Document has no body",
			"processed": 0,
		})
		return
	}

	doneElem, doneStartIndex, doneEndIndex := findSyncDoneTrigger(ctx, doc)
	if doneElem == nil {
		infra.LoggerFrom(ctx).Debug("sync trigger scan complete", "found", false)
		infra.LoggerFrom(ctx).Info("sync skipped", "reason", "no done. trigger")
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "processed", 0, "reason", "no done trigger")
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"message":   "No 'done.' trigger found",
			"processed": 0,
		})
		return
	}

	infra.LoggerFrom(ctx).Debug("sync trigger scan complete", "found", true, "start_index", doneStartIndex, "end_index", doneEndIndex)
	infra.LoggerFrom(ctx).Info("found 'done.' trigger, processing document")

	// Collect only content before "done." so the trigger text is not included in the parsed block.
	block := collectSyncBlock(doc, doneStartIndex)
	if block != nil {
		infra.LoggerFrom(ctx).Debug("sync block collected", "block_preview", utils.TruncateString(block.text, 120), "start_index", block.startIndex, "end_index", block.endIndex)
	} else {
		infra.LoggerFrom(ctx).Debug("sync block collected", "has_block", false)
	}
	blockToProcess := block
	if block != nil && strings.TrimSpace(block.text) != "" {
		h := sha256.Sum256([]byte(block.text))
		blockHash := hex.EncodeToString(h[:])
		fsClient, err := s.App.Firestore(ctx)
		if err == nil && fsClient != nil {
			stateRef := fsClient.Collection(infra.SystemCollection).Doc(syncStateDocument)
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
			fsClient, err := s.App.Firestore(ctx)
			if err == nil && fsClient != nil {
				_, err = fsClient.Collection(infra.SystemCollection).Doc(syncStateDocument).Set(ctx, map[string]interface{}{
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

func handleWebhook(s *Server, w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	resourceState := r.Header.Get("X-Goog-Resource-State")
	LogHandlerRequest(ctx, r.Method, path, "resource_state", resourceState)
	ctx, span := infra.StartSpan(ctx, "webhook.gdrive")
	defer span.End()
	if resourceState == "sync" {
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "sync acknowledged")
		WriteJSON(w, http.StatusOK, map[string]string{"status": "sync acknowledged"})
		return
	}
	span.SetAttributes(map[string]string{"resource_state": resourceState})
	if resourceState != "change" && resourceState != "update" {
		infra.LoggerFrom(ctx).Info("webhook ignored", "resource_state", resourceState)
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ignored", "reason", "resource_state")
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"status": "ignored", "reason": fmt.Sprintf("resource_state=%s", resourceState),
		})
		return
	}
	if s.Config.SyncGDocURL == "" {
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", "SYNC_GDOC_URL not configured")
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "SYNC_GDOC_URL not configured"})
		return
	}
	debounceSeconds := 5
	tasksClient, err := cloudtasks.NewClient(ctx)
	if err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to create Tasks client: %v", err)})
		return
	}
	defer tasksClient.Close()
	parent := fmt.Sprintf("projects/%s/locations/%s/queues/%s", s.Config.GoogleCloudProject, s.Config.CloudTasksLocation, s.Config.CloudTasksQueue)
	fsClient, err := s.App.Firestore(ctx)
	if err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to get Firestore client: %v", err)})
		return
	}
	debounceRef := fsClient.Collection(infra.SystemCollection).Doc("sync_debounce")
	if doc, err := debounceRef.Get(ctx); err == nil && doc.Exists() {
		data := doc.Data()
		if oldTaskName, ok := data["task_name"].(string); ok && oldTaskName != "" {
			if err := tasksClient.DeleteTask(ctx, &cloudtaskspb.DeleteTaskRequest{Name: oldTaskName}); err != nil {
				infra.LoggerFrom(ctx).Debug("failed to delete old task (may have already executed)", "error", err)
			} else {
				infra.LoggerFrom(ctx).Debug("cancelled previous sync task")
			}
		}
	}
	taskID := fmt.Sprintf("jot-sync-%s", uuid.New().String()[:8])
	taskName := fmt.Sprintf("%s/tasks/%s", parent, taskID)
	scheduleTime := time.Now().Add(time.Duration(debounceSeconds) * time.Second)
	task := &cloudtaskspb.Task{
		Name: taskName,
		MessageType: &cloudtaskspb.Task_HttpRequest{
			HttpRequest: &cloudtaskspb.HttpRequest{
				HttpMethod: cloudtaskspb.HttpMethod_POST,
				Url:        s.Config.SyncGDocURL,
				Headers:    map[string]string{"Content-Type": "application/json", "X-API-Key": s.Config.JotAPIKey},
			},
		},
		ScheduleTime: timestamppb.New(scheduleTime),
	}
	_, err = tasksClient.CreateTask(ctx, &cloudtaskspb.CreateTaskRequest{Parent: parent, Task: task})
	if err != nil {
		span.RecordError(err)
		infra.LoggerFrom(ctx).Error("webhook failed to schedule sync", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to create task: %v", err)})
		return
	}
	infra.LoggerFrom(ctx).Info("webhook", "event", "Drive change, sync scheduled", "delay_seconds", debounceSeconds, "task_id", taskID)
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "scheduled", "task_id", taskID, "delay_seconds", debounceSeconds)
	s.App.SubmitAsync(func() {
		if _, err := debounceRef.Set(ctx, map[string]interface{}{
			"task_name": taskName, "scheduled_time": scheduleTime.Format(time.RFC3339),
		}); err != nil {
			infra.LoggerFrom(ctx).Warn("failed to store debounce state", "error", err)
		}
	})
	infra.LoggerFrom(ctx).Debug("webhook completed", "duration_ms", time.Since(startTime).Milliseconds())
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status": "scheduled", "message": fmt.Sprintf("Sync scheduled for %d seconds from now", debounceSeconds),
		"scheduled_time": scheduleTime.Format(time.RFC3339),
	})
}
