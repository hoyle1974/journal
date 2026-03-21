package api

import (
	"io"
	"net/http"
	"strings"

	"github.com/jackstrohm/jot/internal/agent"
	"github.com/jackstrohm/jot/internal/infra"
)

// handleReplay accepts a multipart form with content, source, timestamp, and optional
// image/audio files, inserting the entry through the full processing pipeline
// (AddEntryAndEnqueue → /internal/process-entry) with the original timestamp preserved.
// Intended for dev replay of prod journal archives.
func handleReplay(s *Server, w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	ctx, span := infra.StartSpan(ctx, "api.replay")
	defer span.End()

	app, ok := s.App.(*infra.App)
	if !ok {
		return nil, handlerError(http.StatusInternalServerError, "app not available")
	}

	if err := r.ParseMultipartForm(logMultipartMaxBytes); err != nil {
		return nil, handlerError(http.StatusBadRequest, "invalid multipart form")
	}

	content := strings.TrimSpace(r.FormValue("content"))
	source := strings.TrimSpace(r.FormValue("source"))
	timestamp := strings.TrimSpace(r.FormValue("timestamp"))

	if content == "" {
		return nil, handlerError(http.StatusBadRequest, "content is required")
	}
	if source == "" {
		return nil, handlerError(http.StatusBadRequest, "source is required")
	}
	if timestamp == "" {
		return nil, handlerError(http.StatusBadRequest, "timestamp is required")
	}

	// readFormFileBytes reads the named multipart field and returns its bytes, or nil if absent.
	readFormFileBytes := func(field string) ([]byte, error) {
		f, _, err := r.FormFile(field)
		if err != nil {
			return nil, nil // field not present
		}
		defer f.Close()
		b, readErr := io.ReadAll(f)
		if readErr != nil {
			return nil, handlerError(http.StatusBadRequest, "could not read "+field+" file")
		}
		return b, nil
	}

	imageBytes, err := readFormFileBytes("image")
	if err != nil {
		return nil, err
	}
	audioBytes, err := readFormFileBytes("audio")
	if err != nil {
		return nil, err
	}

	var imageURL string
	if len(imageBytes) > 0 {
		var uploadErr error
		imageURL, uploadErr = app.ImageStorage().UploadImage(ctx, imageBytes)
		if uploadErr != nil {
			infra.LoggerFrom(ctx).Warn("replay: image upload failed", "error", uploadErr)
			return nil, handlerError(http.StatusInternalServerError, "image upload failed")
		}
	}

	var audioURL string
	if len(audioBytes) > 0 {
		var uploadErr error
		audioURL, uploadErr = app.UploadAudio(ctx, audioBytes)
		if uploadErr != nil {
			infra.LoggerFrom(ctx).Warn("replay: audio upload failed", "error", uploadErr)
			return nil, handlerError(http.StatusInternalServerError, "audio upload failed")
		}
	}

	infra.LoggerFrom(ctx).Info("replay: inserting entry",
		"source", source, "timestamp", timestamp,
		"has_image", imageURL != "", "has_audio", audioURL != "")

	entryUUID, err := agent.AddEntryAndEnqueue(ctx, app, content, source, &timestamp, imageURL)
	if err != nil {
		infra.LoggerFrom(ctx).Error("replay: AddEntryAndEnqueue failed", "error", err)
		return nil, err
	}

	// Persist audio URL and transcript (content IS the transcript for audio entries).
	if audioURL != "" {
		if updateErr := app.Memory.UpdateEntryAudio(ctx, entryUUID, audioURL, content); updateErr != nil {
			infra.LoggerFrom(ctx).Warn("replay: UpdateEntryAudio failed", "entry_uuid", entryUUID, "error", updateErr)
		}
	}

	infra.LoggerFrom(ctx).Info("replay: entry inserted", "entry_uuid", entryUUID)
	return map[string]any{"success": true, "uuid": entryUUID}, nil
}
