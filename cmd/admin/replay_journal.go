// replay_journal reads a local journal archive produced by export-journal and
// replays each entry through the /internal/replay HTTP endpoint in timestamp order.
// Usage: go run ./cmd/admin replay-journal --archive ./jot-export-2026-03-20 [--api-url http://localhost:8080] [--api-key <key>]
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jackstrohm/jot/internal/infra"
)

func runReplayJournal(ctx context.Context, _ *infra.App, args []string) {
	fs := flag.NewFlagSet("replay-journal", flag.ExitOnError)
	archive := fs.String("archive", "", "Path to the archive directory produced by export-journal")
	apiURL := fs.String("api-url", os.Getenv("JOT_API_URL"), "Base URL of the Jot API server (defaults to JOT_API_URL env var)")
	apiKey := fs.String("api-key", os.Getenv("JOT_API_KEY"), "API key for the Jot server (defaults to JOT_API_KEY env var)")
	_ = fs.Parse(args)

	if *archive == "" {
		log.Fatal("--archive is required")
	}
	if *apiURL == "" {
		log.Fatal("--api-url is required (or set JOT_API_URL in your .env file)")
	}
	if *apiKey == "" {
		log.Fatal("--api-key is required (or set JOT_API_KEY in your .env file)")
	}

	manifestPath := filepath.Join(*archive, "manifest.jsonl")
	f, err := os.Open(manifestPath)
	if err != nil {
		log.Fatalf("open manifest: %v (run export-journal first, or check --archive path)", err)
	}
	defer f.Close()

	var entries []manifestEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var me manifestEntry
		if err := json.Unmarshal(line, &me); err != nil {
			log.Fatalf("parse manifest line: %v", err)
		}
		entries = append(entries, me)
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("read manifest: %v", err)
	}

	fmt.Printf("Replaying %d entries to %s ...\n", len(entries), *apiURL)

	client := &http.Client{Timeout: 30 * time.Second}
	endpoint := *apiURL + "/internal/replay"

	for i, me := range entries {
		if err := replayEntry(ctx, client, endpoint, *apiKey, *archive, me); err != nil {
			log.Printf("warning: entry %s failed: %v — continuing", me.UUID, err)
		}
		if (i+1)%50 == 0 || i+1 == len(entries) {
			fmt.Printf("replayed %d/%d entries...\n", i+1, len(entries))
		}
	}

	fmt.Printf("Replay complete: %d entries submitted.\n", len(entries))
}

func replayEntry(ctx context.Context, client *http.Client, endpoint, apiKey, archiveDir string, me manifestEntry) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	_ = w.WriteField("content", me.Content)
	_ = w.WriteField("source", me.Source)
	_ = w.WriteField("timestamp", me.Timestamp)

	if me.ImageFile != nil {
		if err := attachFile(w, "image", filepath.Join(archiveDir, *me.ImageFile)); err != nil {
			log.Printf("warning: could not attach image for %s: %v", me.UUID, err)
		}
	}
	if me.AudioFile != nil {
		if err := attachFile(w, "audio", filepath.Join(archiveDir, *me.AudioFile)); err != nil {
			log.Printf("warning: could not attach audio for %s: %v", me.UUID, err)
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST replay: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, body)
	}
	return nil
}

func attachFile(w *multipart.Writer, fieldName, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file %q: %w", path, err)
	}
	part, err := w.CreateFormFile(fieldName, filepath.Base(path))
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	_, err = part.Write(data)
	return err
}
