package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// EntryUUIDRegex matches /entries/{uuid} for path parsing. Exported for tests.
var EntryUUIDRegex = regexp.MustCompile(`^/entries/([a-f0-9-]+)$`)

func handleEntries(s *Server, w http.ResponseWriter, r *http.Request, path string) {
	ctx := r.Context()
	pathForLogVal := pathForLog(r.URL.Path)
	LogHandlerRequest(ctx, r.Method, pathForLogVal)
	match := EntryUUIDRegex.FindStringSubmatch(path)
	switch r.Method {
	case http.MethodGet:
		if match != nil {
			entry, err := s.Journal.GetEntry(ctx, match[1])
			if err != nil {
				LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusNotFound, "error", "Entry not found")
				WriteJSON(w, http.StatusNotFound, map[string]string{"error": "Entry not found"})
				return
			}
			LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusOK, "entry_uuid", match[1])
			WriteJSON(w, http.StatusOK, entry)
			return
		}
		entryUUID := r.URL.Query().Get("uuid")
		if entryUUID != "" {
			entry, err := s.Journal.GetEntry(ctx, entryUUID)
			if err != nil {
				LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusNotFound, "error", "Entry not found")
				WriteJSON(w, http.StatusNotFound, map[string]string{"error": "Entry not found"})
				return
			}
			LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusOK, "entry_uuid", entryUUID)
			WriteJSON(w, http.StatusOK, entry)
			return
		}
		limit := 10
		if l := r.URL.Query().Get("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil {
				limit = parsed
			}
		}
		LogHandlerRequest(ctx, r.Method, pathForLogVal, "limit", limit)
		entries, err := s.Journal.GetEntries(ctx, limit)
		if err != nil {
			LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusInternalServerError, "error", err.Error())
			WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusOK, "count", len(entries))
		WriteJSON(w, http.StatusOK, map[string]interface{}{"entries": entries, "count": len(entries)})
	case http.MethodPatch:
		var data struct {
			UUID    string `json:"uuid"`
			Content string `json:"content" validate:"required"`
		}
		if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
			LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusBadRequest, "error", err.Error())
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		entryUUID := data.UUID
		if match != nil {
			entryUUID = match[1]
		}
		if entryUUID == "" {
			LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusBadRequest, "error", "uuid is required (in body or path)")
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "uuid is required (in body or path)"})
			return
		}
		newContent := strings.TrimSpace(data.Content)
		if newContent == "" {
			LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusBadRequest, "error", "content cannot be only whitespace")
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "content cannot be only whitespace"})
			return
		}
		LogHandlerRequest(ctx, r.Method, pathForLogVal, "uuid", entryUUID, "content_length", len(newContent))
		if err := s.Journal.UpdateEntry(ctx, entryUUID, newContent); err != nil {
			LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusInternalServerError, "error", err.Error())
			WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusOK, "success", true, "uuid", entryUUID)
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
				LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusBadRequest, "error", "Invalid JSON")
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
			LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusBadRequest, "error", "uuids is required")
			WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "uuids is required"})
			return
		}
		LogHandlerRequest(ctx, r.Method, pathForLogVal, "uuid_count", len(uuids))
		if err := s.Journal.DeleteEntries(ctx, uuids); err != nil {
			LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusInternalServerError, "error", err.Error())
			WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusOK, "success", true, "deleted", len(uuids))
		WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "deleted": len(uuids)})
	default:
		LogHandlerResponse(ctx, r.Method, pathForLogVal, http.StatusMethodNotAllowed, "error", "Method not allowed")
		WriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}
