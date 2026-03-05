package jot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackstrohm/jot/internal/api"
)

func handleLog(s *api.Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var data struct {
		Content   string  `json:"content"`
		Source    string  `json:"source"`
		Timestamp *string `json:"timestamp"`
	}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		LoggerFrom(ctx).Warn("log request decode error", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Invalid JSON: %v", err)})
		return
	}

	content := strings.TrimSpace(data.Content)
	source := data.Source
	if source == "" {
		source = "api"
	}

	if content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}

	EntriesTotal.Inc()
	entryUUID, err := AddEntry(ctx, content, source, data.Timestamp)
	if err != nil {
		ErrorsTotal.Inc()
		LoggerFrom(ctx).Error("entry failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LoggerFrom(ctx).Info("entry logged", "uuid", entryUUID, "source", source, "content", truncateString(content, 50))

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"uuid":    entryUUID,
		"message": "Entry logged successfully",
	})
}

func handleQuery(s *api.Server, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var data struct {
		Question string `json:"question"`
		Source   string `json:"source"`
	}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}

	question := strings.TrimSpace(data.Question)
	source := data.Source
	if source == "" {
		source = "api"
	}

	if question == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question is required"})
		return
	}

	ctx := r.Context()
	LoggerFrom(ctx).Info("query", "question", truncateString(question, 80), "source", source)
	result := RunQuery(ctx, question, source)

	if result.Error {
		LoggerFrom(ctx).Error("query error", "answer", result.Answer)
	} else {
		LoggerFrom(ctx).Info("query done", "answer", truncateString(result.Answer, 120))
	}

	writeJSON(w, http.StatusOK, result)
}

func handlePlan(s *api.Server, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var data struct {
		Goal string `json:"goal"`
	}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
		return
	}

	goal := strings.TrimSpace(data.Goal)
	if goal == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "goal is required"})
		return
	}

	ctx := r.Context()
	LoggerFrom(ctx).Info("plan started", "goal", truncateString(goal, 60))

	result, err := CreateAndSavePlan(ctx, goal)
	if err != nil {
		ErrorsTotal.Inc()
		LoggerFrom(ctx).Error("plan failed", "error", err)
		code := http.StatusInternalServerError
		if IsLLMQuotaOrBillingError(err) {
			code = http.StatusTooManyRequests
		} else if IsLLMPermissionOrBillingDenied(err) {
			code = http.StatusForbidden
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	LoggerFrom(ctx).Info("plan completed")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"plan":    result,
	})
}
