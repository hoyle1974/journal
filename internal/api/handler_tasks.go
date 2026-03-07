package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

func handleProcessEntry(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		UUID          string `json:"uuid"`
		Content       string `json:"content"`
		Timestamp     string `json:"timestamp"`
		Source        string `json:"source"`
		TaskID        string `json:"task_id"`
		ParentTraceID string `json:"parent_trace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "Invalid JSON")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Invalid JSON: %v", err)})
		return
	}
	if data.UUID == "" || data.Content == "" || data.Source == "" {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "uuid, content, and source are required")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "uuid, content, and source are required"})
		return
	}
	if data.TaskID != "" || data.ParentTraceID != "" {
		ctx = infra.WithCorrelation(ctx, data.TaskID, data.ParentTraceID)
	}
	LogHandlerRequest(ctx, r.Method, path, "uuid", data.UUID, "source", data.Source, "content_length", len(data.Content), "task_id", data.TaskID, "parent_trace_id", data.ParentTraceID)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	breakdown, err := s.Agent.ProcessEntry(ctx, data.UUID, data.Content, data.Timestamp, data.Source)
	if setter, ok := w.(interface{ SetLatencyBreakdown(*infra.LatencyBreakdown) }); ok && breakdown != nil {
		setter.SetLatencyBreakdown(breakdown)
	}
	if err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ok", "uuid", data.UUID)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleProcessSMSQuery runs the query for an incoming SMS (FOH) and sends the reply via Twilio.
// Invoked by a Cloud Task enqueued from the SMS webhook so the work runs in a request-scoped context (Cloud Run keeps the request alive until done).
func handleProcessSMSQuery(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		From       string `json:"from"`
		Body       string `json:"body"`
		MessageSid string `json:"message_sid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "Invalid JSON")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Invalid JSON: %v", err)})
		return
	}
	if data.From == "" || data.Body == "" {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "from and body are required")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "from and body are required"})
		return
	}
	msg := &infra.TwilioWebhookRequest{
		MessageSid: data.MessageSid,
		From:       data.From,
		To:         "",
		Body:       data.Body,
	}
	LogHandlerRequest(ctx, r.Method, path, "from", data.From, "message_sid", data.MessageSid, "body_length", len(data.Body))
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	response := s.SMS.ProcessIncomingSMS(ctx, msg)
	if response == "" {
		response = "I couldn't process that. Please try again."
	}
	if err := s.SMS.SendSMS(ctx, data.From, response); err != nil {
		infra.LoggerFrom(ctx).Error("process-sms-query: send reply failed", "to", data.From, "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send SMS reply"})
		return
	}
	infra.LoggerFrom(ctx).Info("process-sms-query: reply sent", "to", data.From, "preview", utils.TruncateString(response, 60))
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ok", "to", data.From)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleSaveQuery(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	var data struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
		Source   string `json:"source"`
		IsGap    bool   `json:"is_gap"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "Invalid JSON")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Invalid JSON: %v", err)})
		return
	}
	if data.Question == "" || data.Source == "" {
		LogHandlerResponse(ctx, r.Method, path, http.StatusBadRequest, "error", "question and source are required")
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "question and source are required"})
		return
	}
	LogHandlerRequest(ctx, r.Method, path, "question_preview", utils.TruncateString(data.Question, 60), "source", data.Source, "is_gap", data.IsGap)
	if _, err := s.Journal.SaveQuery(ctx, data.Question, data.Answer, data.Source, data.IsGap); err != nil {
		infra.LoggerFrom(ctx).Error("save-query failed", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "status", "ok", "source", data.Source)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleBackfillEmbeddings(s *Server, w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, path)
	if r.Method != http.MethodPost {
		LogHandlerResponse(ctx, r.Method, path, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}
	LogHandlerRequest(ctx, r.Method, path, "limit", limit)
	processed, err := s.Journal.BackfillEntryEmbeddings(ctx, limit)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("backfill-embeddings failed", "error", err)
		LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError, "error", err.Error())
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LogHandlerResponse(ctx, r.Method, path, http.StatusOK, "success", true, "processed", processed)
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "processed": processed})
}
