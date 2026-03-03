package jot

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

func handleDecayContexts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	ctx := r.Context()
	LoggerFrom(ctx).Info("decay-contexts started")

	if err := InitializePermanentContexts(ctx); err != nil {
		LoggerFrom(ctx).Warn("failed to initialize permanent contexts", "error", err)
	}

	decayedCount, err := DecayContexts(ctx)
	if err != nil {
		ErrorsTotal.Inc()
		LoggerFrom(ctx).Error("decay-contexts failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LoggerFrom(ctx).Info("decay-contexts completed", "decayed_count", decayedCount)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":       true,
		"decayed_count": decayedCount,
	})
}

func handleBackfillEmbeddings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}

	ctx := r.Context()
	LoggerFrom(ctx).Info("backfill-embeddings started", "limit", limit)
	processed, err := BackfillEntryEmbeddings(ctx, limit)
	if err != nil {
		ErrorsTotal.Inc()
		LoggerFrom(ctx).Error("backfill-embeddings failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	LoggerFrom(ctx).Info("backfill-embeddings completed", "processed", processed)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"processed": processed,
	})
}

var entryUUIDRegex = regexp.MustCompile(`^/entries/([a-f0-9-]+)$`)

func handleEntries(w http.ResponseWriter, r *http.Request, path string) {
	ctx := r.Context()

	match := entryUUIDRegex.FindStringSubmatch(path)

	switch r.Method {
	case http.MethodGet:
		if match != nil {
			entryUUID := match[1]
			entry, err := GetEntry(ctx, entryUUID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "Entry not found"})
				return
			}
			writeJSON(w, http.StatusOK, entry)
			return
		}

		entryUUID := r.URL.Query().Get("uuid")
		if entryUUID != "" {
			entry, err := GetEntry(ctx, entryUUID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "Entry not found"})
				return
			}
			writeJSON(w, http.StatusOK, entry)
			return
		}

		limit := 10
		if l := r.URL.Query().Get("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil {
				limit = parsed
			}
		}

		entries, err := GetEntries(ctx, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"entries": entries,
			"count":   len(entries),
		})

	case http.MethodPatch:
		var data struct {
			UUID    string `json:"uuid"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
			return
		}

		entryUUID := data.UUID
		if match != nil {
			entryUUID = match[1]
		}
		newContent := strings.TrimSpace(data.Content)

		if entryUUID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "uuid is required"})
			return
		}
		if newContent == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
			return
		}

		if err := UpdateEntry(ctx, entryUUID, newContent); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"message": "Entry updated",
		})

	case http.MethodDelete:
		var uuids []string
		if match != nil {
			uuids = []string{match[1]}
		} else {
			var data struct {
				UUIDs interface{} `json:"uuids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON"})
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
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "uuids is required"})
			return
		}

		if err := DeleteEntries(ctx, uuids); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"deleted": len(uuids),
		})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}
