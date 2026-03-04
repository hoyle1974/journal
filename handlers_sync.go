package jot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sanitizeResponseForDoc ensures the LLM response never contains a standalone "done" or "done." line.
// Writing that into the doc would create a new sync trigger and cause an infinite sync loop.
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

// syncLine represents one unbolded line (used when collecting the block).
type syncLine struct {
	elem *docs.ParagraphElement
	text string
	kind int
}

// syncBlock is the entire unbolded text before "done." as a single entry (kind: 0=plain, 1=question, 2=action).
type syncBlock struct {
	text        string
	startIndex  int64
	endIndex    int64
	kind        int
}

func syncCreateDocsService(ctx context.Context) (*docs.Service, error) {
	if ServiceAccountFile != "" {
		return docs.NewService(ctx, option.WithCredentialsFile(ServiceAccountFile))
	}
	return docs.NewService(ctx)
}

func syncFetchDoc(ctx context.Context, docsService *docs.Service, documentID string) (*docs.Document, error) {
	return docsService.Documents.Get(documentID).Do()
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

func buildSyncRequests(ctx context.Context, doneStartIndex, doneEndIndex int64, block *syncBlock) ([]*docs.Request, int, int, int) {
	source := "gdoc"
	var requests []*docs.Request
	// Apply "done." updates first (original indices); then block updates. Block is before "done." so
	// inserting at block.endIndex does not shift the "done." range we already used.
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
	switch block.kind {
	case 1:
		question := strings.TrimPrefix(text, "?")
		question = strings.TrimSpace(question)
		if question != "" {
			LoggerFrom(ctx).Info("processing question", "question", truncateString(question, 80))
			queryStart := time.Now()
			answer := GetAnswer(ctx, question, source)
			LoggerFrom(ctx).Info("question answered", "duration_ms", time.Since(queryStart).Milliseconds())
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
			LoggerFrom(ctx).Info("processing action", "action", truncateString(action, 80))
			actionStart := time.Now()
			result := GetAnswer(ctx, "Execute this action and confirm what you did: "+action, source)
			LoggerFrom(ctx).Info("action executed", "duration_ms", time.Since(actionStart).Milliseconds())
			inserted := "\n✓ " + sanitizeResponseForDoc(result)
			requests = append(requests,
				&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.startIndex, EndIndex: block.endIndex}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
				&docs.Request{InsertText: &docs.InsertTextRequest{Location: &docs.Location{Index: block.endIndex}, Text: inserted}},
				&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.endIndex, EndIndex: block.endIndex + docIndexLen(inserted)}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
			)
			actionsExecuted++
		}
	default:
		LoggerFrom(ctx).Info("processing input", "text", truncateString(text, 80))
		processStart := time.Now()
		response := GetAnswer(ctx, text, source)
		LoggerFrom(ctx).Info("input processed", "duration_ms", time.Since(processStart).Milliseconds())
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

func handleSync(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	ctx := r.Context()
	ctx = WithSyncInProgress(ctx) // don't forward logs to doc during sync; appends would shift indices

	ctx, span := StartSpan(ctx, "sync.gdoc")
	defer span.End()

	LoggerFrom(ctx).Info("sync started")

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	if DocumentID == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "DOCUMENT_ID not configured"})
		return
	}

	lockAcquired, lockErr := acquireSyncLock(ctx)
	if lockErr != nil {
		LoggerFrom(ctx).Error("failed to check sync lock", "error", lockErr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to check sync lock"})
		return
	}
	if !lockAcquired {
		LoggerFrom(ctx).Info("sync skipped", "reason", "already in progress")
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Another sync is already in progress. Please wait and try again."})
		return
	}
	defer releaseSyncLock(ctx)

	var docsService *docs.Service
	var doc *docs.Document
	var err error
	docsService, err = syncCreateDocsService(ctx)
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("sync failed", "stage", "DocsService", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to create Docs service: %v", err)})
		return
	}

	docFetchStart := time.Now()
	doc, err = syncFetchDoc(ctx, docsService, DocumentID)
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("sync failed", "stage", "FetchDoc", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to fetch document: %v", err)})
		return
	}
	LoggerFrom(ctx).Debug("doc fetched", "duration_ms", time.Since(docFetchStart).Milliseconds())

	if doc.Body == nil || len(doc.Body.Content) == 0 {
		LoggerFrom(ctx).Info("sync skipped", "reason", "document has no body")
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"message":   "Document has no body",
			"processed": 0,
		})
		return
	}

	doneElem, doneStartIndex, doneEndIndex := findSyncDoneTrigger(doc)
	if doneElem == nil {
		LoggerFrom(ctx).Info("sync skipped", "reason", "no done. trigger")
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"message":   "No 'done.' trigger found",
			"processed": 0,
		})
		return
	}

	LoggerFrom(ctx).Info("found 'done.' trigger, processing document")

	block := collectSyncBlock(doc, doneEndIndex)
	blockToProcess := block
	if block != nil && strings.TrimSpace(block.text) != "" {
		h := sha256.Sum256([]byte(block.text))
		blockHash := hex.EncodeToString(h[:])
		fsClient, err := GetFirestoreClient(ctx)
		if err == nil && fsClient != nil {
			stateRef := fsClient.Collection(SystemCollection).Doc(syncStateDocument)
			stateDoc, err := stateRef.Get(ctx)
			if err == nil && stateDoc.Exists() {
				if lastHash, ok := stateDoc.Data()["last_block_hash"].(string); ok && lastHash == blockHash {
					LoggerFrom(ctx).Info("sync skipped", "reason", "duplicate block (already processed)")
					blockToProcess = nil
				}
			}
		}
	}
	requests, entriesAdded, questionsAnswered, actionsExecuted := buildSyncRequests(ctx, doneStartIndex, doneEndIndex, blockToProcess)

	if len(requests) > 0 {
		updateStart := time.Now()
		err = syncApplyBatchUpdate(docsService, DocumentID, requests)
		if err != nil {
			LoggerFrom(ctx).Error("doc update failed", "error", err)
			span.RecordError(err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to update document: %v", err)})
			return
		}
		LoggerFrom(ctx).Debug("doc updated", "duration_ms", time.Since(updateStart).Milliseconds())
		if blockToProcess != nil && strings.TrimSpace(blockToProcess.text) != "" {
			h := sha256.Sum256([]byte(blockToProcess.text))
			blockHash := hex.EncodeToString(h[:])
			fsClient, err := GetFirestoreClient(ctx)
			if err == nil && fsClient != nil {
				_, err = fsClient.Collection(SystemCollection).Doc(syncStateDocument).Set(ctx, map[string]interface{}{
					"last_block_hash":   blockHash,
					"last_processed_at": time.Now(),
				})
				if err != nil {
					LoggerFrom(ctx).Warn("failed to store sync_state", "error", err)
				}
			}
		}
	}

	totalTime := time.Since(startTime)
	totalProcessed := entriesAdded + questionsAnswered + actionsExecuted
	LoggerFrom(ctx).Info("sync completed",
		"entries_added", entriesAdded,
		"questions_answered", questionsAnswered,
		"actions_executed", actionsExecuted,
		"duration_ms", totalTime.Milliseconds(),
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":            true,
		"entries_added":      entriesAdded,
		"questions_answered": questionsAnswered,
		"actions_executed":   actionsExecuted,
		"total_processed":    totalProcessed,
	})
}

const (
	syncLockCollection = "system"
	syncLockDocument   = "sync_lock"
	syncStateDocument  = "sync_state"
	syncLockTimeout    = 15 * time.Minute
)

func acquireSyncLock(ctx context.Context) (bool, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil || client == nil {
		return true, nil
	}

	lockRef := client.Collection(syncLockCollection).Doc(syncLockDocument)

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

func releaseSyncLock(ctx context.Context) {
	client, err := GetFirestoreClient(ctx)
	if err != nil || client == nil {
		return
	}

	lockRef := client.Collection(syncLockCollection).Doc(syncLockDocument)
	_, err = lockRef.Delete(ctx)
	if err != nil {
		LoggerFrom(ctx).Error("failed to release sync lock", "error", err)
	}
}
