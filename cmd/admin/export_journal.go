// export_journal downloads all journal entries (node_type="log") from Firestore
// to a local archive directory for later replay on dev.
// Usage: go run ./cmd/admin export-journal --output ./jot-export-2026-03-20
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/memory"
	"github.com/jackstrohm/jot/pkg/storage"
)

// manifestEntry is one line of manifest.jsonl.
type manifestEntry struct {
	UUID      string  `json:"uuid"`
	Content   string  `json:"content"`
	Source    string  `json:"source"`
	Timestamp string  `json:"timestamp"`
	ImageFile *string `json:"image_file"`
	AudioFile *string `json:"audio_file"`
}

func runExportJournal(ctx context.Context, app *infra.App, args []string) {
	fs := flag.NewFlagSet("export-journal", flag.ExitOnError)
	output := fs.String("output", "", "Directory to write archive into (must not already exist)")
	_ = fs.Parse(args)

	if *output == "" {
		log.Fatal("--output is required")
	}
	if _, err := os.Stat(*output); err == nil {
		log.Fatalf("output directory %q already exists — remove it or choose a different path", *output)
	}

	if err := os.MkdirAll(filepath.Join(*output, "images"), 0o755); err != nil {
		log.Fatalf("create images dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(*output, "audio"), 0o755); err != nil {
		log.Fatalf("create audio dir: %v", err)
	}

	entries, err := memory.GetAllLogEntries(ctx, app)
	if err != nil {
		log.Fatalf("fetch entries: %v", err)
	}
	log.Printf("Found %d journal entries to export", len(entries))

	gcs, hasGCS := app.ImageStorage().(*storage.GCSImageStorage)

	var manifest []manifestEntry
	for i, e := range entries {
		if (i+1)%50 == 0 || i+1 == len(entries) {
			fmt.Printf("exported %d/%d entries...\n", i+1, len(entries))
		}

		me := manifestEntry{
			UUID:      e.UUID,
			Content:   e.Content,
			Source:    e.Source,
			Timestamp: e.Timestamp,
		}

		if e.ImageURL != "" {
			if !hasGCS {
				log.Printf("warning: entry %s has image_url but GCS not configured (set JOT_IMAGES_BUCKET); skipping", e.UUID)
			} else {
				data, mimeType, dlErr := gcs.DownloadImage(ctx, e.ImageURL)
				if dlErr != nil {
					log.Printf("warning: entry %s image download failed: %v — skipping", e.UUID, dlErr)
				} else {
					ext := extFromMIME(mimeType)
					relPath := filepath.Join("images", e.UUID+ext)
					if writeErr := os.WriteFile(filepath.Join(*output, relPath), data, 0o644); writeErr != nil {
						log.Fatalf("write image file: %v", writeErr)
					}
					me.ImageFile = &relPath
				}
			}
		}

		if e.AudioURL != "" {
			if !hasGCS {
				log.Printf("warning: entry %s has audio_url but GCS not configured; skipping", e.UUID)
			} else {
				// DownloadImage reads any gs:// URI — reuse it for audio bytes.
				data, _, dlErr := gcs.DownloadImage(ctx, e.AudioURL)
				if dlErr != nil {
					log.Printf("warning: entry %s audio download failed: %v — skipping", e.UUID, dlErr)
				} else {
					relPath := filepath.Join("audio", e.UUID+".ogg")
					if writeErr := os.WriteFile(filepath.Join(*output, relPath), data, 0o644); writeErr != nil {
						log.Fatalf("write audio file: %v", writeErr)
					}
					me.AudioFile = &relPath
				}
			}
		}

		manifest = append(manifest, me)
	}

	// Write manifest.jsonl last — its presence signals a complete export.
	mf, err := os.Create(filepath.Join(*output, "manifest.jsonl"))
	if err != nil {
		log.Fatalf("create manifest: %v", err)
	}
	defer mf.Close()
	enc := json.NewEncoder(mf)
	for _, me := range manifest {
		if err := enc.Encode(me); err != nil {
			log.Fatalf("write manifest line: %v", err)
		}
	}

	fmt.Printf("Export complete: %d entries written to %s\n", len(manifest), *output)
}

func extFromMIME(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".bin"
	}
}
