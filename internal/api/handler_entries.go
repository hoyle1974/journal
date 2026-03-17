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

func handleEntries(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	path := pathForLog(r.URL.Path)
	match := EntryUUIDRegex.FindStringSubmatch(r.URL.Path)
	switch r.Method {
	case http.MethodGet:
		if match != nil {
			entry, err := s.Journal.GetEntry(ctx, match[1])
			if err != nil {
				return nil, handlerError(http.StatusNotFound, "Entry not found")
			}
			return entry, nil
		}
		entryUUID := r.URL.Query().Get("uuid")
		if entryUUID != "" {
			entry, err := s.Journal.GetEntry(ctx, entryUUID)
			if err != nil {
				return nil, handlerError(http.StatusNotFound, "Entry not found")
			}
			return entry, nil
		}
		limit := 10
		if l := r.URL.Query().Get("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil {
				limit = parsed
			}
		}
		LogHandlerRequest(ctx, r.Method, path, "limit", limit)
		entries, err := s.Journal.GetEntries(ctx, limit)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"entries": entries, "count": len(entries)}, nil

	case http.MethodPatch:
		var data struct {
			UUID    string `json:"uuid"`
			Content string `json:"content" validate:"required"`
		}
		if err := DecodeAndValidate(r, &data, s.Validator); err != nil {
			return nil, handlerError(http.StatusBadRequest, err.Error())
		}
		entryUUID := data.UUID
		if match != nil {
			entryUUID = match[1]
		}
		if entryUUID == "" {
			return nil, handlerError(http.StatusBadRequest, "uuid is required (in body or path)")
		}
		newContent := strings.TrimSpace(data.Content)
		if newContent == "" {
			return nil, handlerError(http.StatusBadRequest, "content cannot be only whitespace")
		}
		LogHandlerRequest(ctx, r.Method, path, "uuid", entryUUID, "content_length", len(newContent))
		if err := s.Journal.UpdateEntry(ctx, entryUUID, newContent); err != nil {
			return nil, err
		}
		return map[string]interface{}{"success": true, "message": "Entry updated"}, nil

	case http.MethodDelete:
		var uuids []string
		if match != nil {
			uuids = []string{match[1]}
		} else {
			var data struct {
				UUIDs interface{} `json:"uuids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
				return nil, handlerError(http.StatusBadRequest, "Invalid JSON")
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
			return nil, handlerError(http.StatusBadRequest, "uuids is required")
		}
		LogHandlerRequest(ctx, r.Method, path, "uuid_count", len(uuids))
		if err := s.Journal.DeleteEntries(ctx, uuids); err != nil {
			return nil, err
		}
		return map[string]interface{}{"success": true, "deleted": len(uuids)}, nil

	default:
		return nil, handlerError(http.StatusMethodNotAllowed, "Method not allowed")
	}
}
