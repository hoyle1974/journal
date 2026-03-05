package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackstrohm/jot/pkg/infra"
)

func handleDecayContexts(s *Server, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	ctx := r.Context()
	infra.LoggerFrom(ctx).Info("decay-contexts started")
	if err := s.Backend.InitializePermanentContexts(ctx); err != nil {
		infra.LoggerFrom(ctx).Warn("failed to initialize permanent contexts", "error", err)
	}
	decayedCount, err := s.Backend.DecayContexts(ctx)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("decay-contexts failed", "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	infra.LoggerFrom(ctx).Info("decay-contexts completed", "decayed_count", decayedCount)
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "decayed_count": decayedCount})
}

func handleBackfillEmbeddings(s *Server, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}
	ctx := r.Context()
	infra.LoggerFrom(ctx).Info("backfill-embeddings started", "limit", limit)
	processed, err := s.Backend.BackfillEntryEmbeddings(ctx, limit)
	if err != nil {
		infra.ErrorsTotal.Inc()
		infra.LoggerFrom(ctx).Error("backfill-embeddings failed", "error", err)
		WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	infra.LoggerFrom(ctx).Info("backfill-embeddings completed", "processed", processed)
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "processed": processed})
}

// EntryUUIDRegex matches /entries/{uuid} for path parsing. Exported for tests.
var EntryUUIDRegex = regexp.MustCompile(`^/entries/([a-f0-9-]+)$`)

func handleEntries(s *Server, w http.ResponseWriter, r *http.Request, path string) {
	ctx := r.Context()
	match := EntryUUIDRegex.FindStringSubmatch(path)
	switch r.Method {
	case http.MethodGet:
		if match != nil {
			entry, err := s.Backend.GetEntry(ctx, match[1])
			if err != nil {
				WriteJSON(w, http.StatusNotFound, map[string]string{"error": "Entry not found"})
				return
			}
			WriteJSON(w, http.StatusOK, entry)
			return
		}
		entryUUID := r.URL.Query().Get("uuid")
		if entryUUID != "" {
			entry, err := s.Backend.GetEntry(ctx, entryUUID)
			if err != nil {
				WriteJSON(w, http.StatusNotFound, map[string]string{"error": "Entry not found"})
				return
			}
			WriteJSON(w, http.StatusOK, entry)
			return
		}
		limit := 10
		if l := r.URL.Query().Get("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil {
				limit = parsed
			}
		}
		entries, err := s.Backend.GetEntries(ctx, limit)
		if err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"entries": entries, "count": len(entries)})
	case http.MethodPatch:
		var data struct {
			UUID    string `json:"uuid"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
			return
		}
		entryUUID := data.UUID
		if match != nil {
			entryUUID = match[1]
		}
		newContent := strings.TrimSpace(data.Content)
		if entryUUID == "" {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "uuid is required"})
			return
		}
		if newContent == "" {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
			return
		}
		if err := s.Backend.UpdateEntry(ctx, entryUUID, newContent); err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "message": "Entry updated"})
	case http.MethodDelete:
		var uuids []string
		if match != nil {
			uuids = []string{match[1]}
		} else {
			var data struct {
				UUIDs interface{} `json:"uuids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
				return
			}
			switch v := data.UUIDs.(type) {
			case string:
				uuids = []string{v}
			case []interface{}:
				for _, item := range v {
					if s, ok := item.(string); ok {
						uuids = append(uuids, s)
					}
				}
			}
		}
		if len(uuids) == 0 {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "uuids is required"})
			return
		}
		if err := s.Backend.DeleteEntries(ctx, uuids); err != nil {
			WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "deleted": len(uuids)})
	default:
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}
