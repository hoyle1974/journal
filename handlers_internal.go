package jot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/jackstrohm/jot/internal/api"
)

func handleProcessEntry(s *api.Server, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var data struct {
		UUID      string `json:"uuid"`
		Content   string `json:"content"`
		Timestamp string `json:"timestamp"`
		Source    string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Invalid JSON: %v", err)})
		return
	}
	if data.UUID == "" || data.Content == "" || data.Source == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "uuid, content, and source are required"})
		return
	}

	ctx := r.Context()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if err := ProcessEntry(ctx, data.UUID, data.Content, data.Timestamp, data.Source); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ProcessEntry runs evaluator, context detection, journal analysis, and embedding for an entry.
func ProcessEntry(ctx context.Context, entryUUID, content, timestamp, source string) error {
	LoggerFrom(ctx).Info("process-entry start", "entry_uuid", entryUUID, "content", truncateString(content, 50), "source", source)
	RunEvaluator(ctx, content, entryUUID, timestamp)

	contextUUIDs, err := DetectOrCreateContext(ctx, content, entryUUID)
	if err != nil {
		LoggerFrom(ctx).Warn("context detection failed", "error", err)
	}
	contextCount := len(contextUUIDs)

	analysis, err := AnalyzeJournalEntry(ctx, content, entryUUID, timestamp)
	if err != nil {
		LoggerFrom(ctx).Warn("journal analysis failed", "entry_uuid", entryUUID, "error", err)
	}
	var analysisJSON string
	if analysis != nil {
		if b, err := json.Marshal(analysis); err == nil {
			analysisJSON = string(b)
		}
	}

	vector, err := GenerateEmbedding(ctx, content, EmbedTaskRetrievalDocument)
	if err != nil {
		LoggerFrom(ctx).Warn("failed to generate entry embedding", "entry_uuid", entryUUID, "error", err)
		return fmt.Errorf("embedding: %w", err)
	}
	LoggerFrom(ctx).Debug("process-entry embedding generated", "entry_uuid", entryUUID, "dimensions", len(vector))

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		LoggerFrom(ctx).Warn("failed to get firestore for entry embedding", "error", err)
		return err
	}
	updates := []firestore.Update{{Path: "embedding", Value: firestore.Vector32(vector)}}
	if analysisJSON != "" {
		updates = append(updates, firestore.Update{Path: "journal_analysis", Value: analysisJSON})
	}
	_, err = client.Collection(EntriesCollection).Doc(entryUUID).Update(ctx, updates)
	if err != nil {
		LoggerFrom(ctx).Warn("failed to store entry embedding", "entry_uuid", entryUUID, "error", err)
		return err
	}
	LoggerFrom(ctx).Info("process-entry done", "entry_uuid", entryUUID, "contexts_linked", contextCount, "embedding_dims", len(vector), "has_analysis", analysisJSON != "")
	return nil
}

func handleSaveQuery(s *api.Server, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var data struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
		Source   string `json:"source"`
		IsGap    bool   `json:"is_gap"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Invalid JSON: %v", err)})
		return
	}
	if data.Question == "" || data.Source == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question and source are required"})
		return
	}

	ctx := r.Context()
	if _, err := SaveQuery(ctx, data.Question, data.Answer, data.Source, data.IsGap); err != nil {
		LoggerFrom(ctx).Error("save-query failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LoggerFrom(ctx).Info("save-query", "question", truncateString(data.Question, 50))

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
