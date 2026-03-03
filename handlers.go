package jot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf16"

	cloudtasks "cloud.google.com/go/cloudtasks/apiv2"
	"cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	"cloud.google.com/go/firestore"
	"github.com/google/uuid"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"project":   GoogleCloudProject,
	})
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"queries_total":      QueriesTotal.Value(),
		"entries_total":      EntriesTotal.Value(),
		"tool_calls_total":   ToolCallsTotal.Value(),
		"gemini_calls_total": GeminiCallsTotal.Value(),
		"errors_total":       ErrorsTotal.Value(),
		"timestamp":          time.Now().Format(time.RFC3339),
	})
}

func handlePrivacyPolicy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(privacyPolicyHTML))
}

func handleTermsAndConditions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(termsAndConditionsHTML))
}

const privacyPolicyHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Privacy Policy - Jot Journal</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 800px; margin: 0 auto; padding: 20px; line-height: 1.6; color: #333; }
        h1 { color: #1a1a1a; border-bottom: 2px solid #eee; padding-bottom: 10px; }
        h2 { color: #444; margin-top: 30px; }
        .last-updated { color: #666; font-size: 0.9em; }
    </style>
</head>
<body>
    <h1>Privacy Policy</h1>
    <p class="last-updated">Last Updated: February 24, 2026</p>

    <h2>Introduction</h2>
    <p>This Privacy Policy describes how Jot Journal ("we", "us", or "our") collects, uses, and protects information for our personal journal SMS service. This service is exclusively operated by and for Harry Jack Strohm III.</p>

    <h2>Information We Collect</h2>
    <p>When you use the Jot Journal SMS service, we collect:</p>
    <ul>
        <li><strong>Phone Number:</strong> Your mobile phone number used to send and receive SMS messages</li>
        <li><strong>Message Content:</strong> The text content of journal entries and queries you send via SMS</li>
        <li><strong>Timestamps:</strong> The date and time of each message</li>
        <li><strong>Message Metadata:</strong> Technical information necessary for message delivery</li>
    </ul>

    <h2>How We Use Your Information</h2>
    <p>Your information is used solely for:</p>
    <ul>
        <li>Storing your personal journal entries</li>
        <li>Processing queries about your journal content</li>
        <li>Generating summaries of your journal entries</li>
        <li>Managing your todo items</li>
        <li>Sending SMS responses to your requests</li>
    </ul>

    <h2>Data Storage and Security</h2>
    <p>Your data is stored securely using Google Cloud Platform services including Firestore and Cloud Functions. We implement industry-standard security measures to protect your information.</p>

    <h2>Third-Party Sharing</h2>
    <p><strong>We do not sell, trade, rent, or share your personal information with third parties.</strong></p>
    <p><strong>We do not use your information for marketing purposes.</strong></p>
    <p>Your data is never shared with advertisers or data brokers. The only third-party services involved are:</p>
    <ul>
        <li>Twilio - for SMS message transmission (subject to their privacy policy)</li>
        <li>Google Cloud Platform - for secure data storage and processing</li>
    </ul>

    <h2>Data Retention</h2>
    <p>Your journal entries and associated data are retained indefinitely unless you request deletion. You may request deletion of your data at any time by contacting us.</p>

    <h2>Your Rights</h2>
    <p>You have the right to:</p>
    <ul>
        <li>Access your stored data</li>
        <li>Request correction of inaccurate data</li>
        <li>Request deletion of your data</li>
        <li>Opt out of the SMS service at any time</li>
    </ul>

    <h2>Contact Information</h2>
    <p>For questions about this Privacy Policy or to exercise your rights, contact:</p>
    <p>Harry Jack Strohm III<br>
    Email: jack@strohm.org</p>

    <h2>Changes to This Policy</h2>
    <p>We may update this Privacy Policy from time to time. Changes will be posted on this page with an updated revision date.</p>
</body>
</html>`

const termsAndConditionsHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Terms and Conditions - Jot Journal SMS</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 800px; margin: 0 auto; padding: 20px; line-height: 1.6; color: #333; }
        h1 { color: #1a1a1a; border-bottom: 2px solid #eee; padding-bottom: 10px; }
        h2 { color: #444; margin-top: 30px; }
        .last-updated { color: #666; font-size: 0.9em; }
        .highlight { background-color: #fff3cd; padding: 15px; border-radius: 5px; margin: 20px 0; }
        .bold-keyword { font-weight: bold; font-size: 1.1em; }
    </style>
</head>
<body>
    <h1>Terms and Conditions</h1>
    <p class="last-updated">Last Updated: February 24, 2026</p>

    <h2>Program Name</h2>
    <p><strong>Jot Journal SMS Service</strong></p>

    <h2>Program Description</h2>
    <p>Jot Journal is a personal journaling service that allows you to log journal entries, query your journal history, manage todo items, and receive AI-generated summaries via SMS text messaging. This is a private service operated exclusively by and for Harry Jack Strohm III.</p>

    <h2>Message Frequency</h2>
    <p>Message frequency varies based on your usage. You will receive SMS messages only in direct response to messages you send to the service. Typical usage includes:</p>
    <ul>
        <li>Confirmation messages when logging journal entries</li>
        <li>Responses to journal queries</li>
        <li>Todo list updates and confirmations</li>
        <li>Summary reports when requested</li>
    </ul>
    <p>You control the frequency of messages by how often you interact with the service.</p>

    <h2>Message and Data Rates</h2>
    <p><strong>Message and data rates may apply.</strong> Standard messaging rates from your mobile carrier apply to all SMS messages sent to and received from Jot Journal. Please consult your mobile service provider for details about your messaging plan.</p>

    <div class="highlight">
        <h2>Opt-Out Instructions</h2>
        <p>You can opt out of receiving SMS messages at any time:</p>
        <p>Text <span class="bold-keyword">STOP</span> to cancel and stop receiving messages from Jot Journal.</p>
        <p>After texting STOP, you will receive one final confirmation message and will no longer receive SMS messages from this service.</p>
    </div>

    <div class="highlight">
        <h2>Help and Support</h2>
        <p>For assistance with the Jot Journal SMS service:</p>
        <p>Text <span class="bold-keyword">HELP</span> to receive help information and support contact details.</p>
        <p>You can also contact support directly:</p>
        <p>Email: jack@strohm.org</p>
    </div>

    <h2>Eligibility</h2>
    <p>This is a private, personal service. Access is restricted to the service owner, Harry Jack Strohm III. Unauthorized use is prohibited.</p>

    <h2>User Consent</h2>
    <p>By using the Jot Journal SMS service, you consent to receive text messages related to your journal entries, queries, and service notifications. You confirm that you are the account holder or have authorization to use the mobile number registered with this service.</p>

    <h2>Service Availability</h2>
    <p>While we strive to maintain consistent service availability, Jot Journal is provided "as is" without guarantees of uptime or availability. The service may be temporarily unavailable due to maintenance, updates, or technical issues.</p>

    <h2>Privacy</h2>
    <p>Your privacy is important to us. Please review our <a href="/privacy-policy">Privacy Policy</a> for details on how we collect, use, and protect your information.</p>

    <h2>Modifications</h2>
    <p>We reserve the right to modify these Terms and Conditions at any time. Changes will be posted on this page with an updated revision date. Continued use of the service after changes constitutes acceptance of the modified terms.</p>

    <h2>Contact Information</h2>
    <p>For questions about these Terms and Conditions:</p>
    <p>Harry Jack Strohm III<br>
    Email: jack@strohm.org</p>
</body>
</html>`

func handleLog(w http.ResponseWriter, r *http.Request) {
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

func handleQuery(w http.ResponseWriter, r *http.Request) {
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

	// Log errors to Google Doc
	if result.Error {
		LoggerFrom(ctx).Error("query error", "answer", result.Answer)
	} else {
		LoggerFrom(ctx).Info("query done", "answer", truncateString(result.Answer, 120))
	}

	writeJSON(w, http.StatusOK, result)
}

func handlePlan(w http.ResponseWriter, r *http.Request) {
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

	// Direct call to the planning logic, bypassing the general query agent
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

func handleDecayContexts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	ctx := r.Context()
	LoggerFrom(ctx).Info("decay-contexts started")

	// Ensure permanent contexts exist (idempotent)
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

	// Check for UUID in path: /entries/{uuid}
	match := entryUUIDRegex.FindStringSubmatch(path)

	switch r.Method {
	case http.MethodGet:
		if match != nil {
			// Get single entry by UUID
			entryUUID := match[1]
			entry, err := GetEntry(ctx, entryUUID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "Entry not found"})
				return
			}
			writeJSON(w, http.StatusOK, entry)
			return
		}

		// Check for UUID in query param
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

		// List entries
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

// syncLine represents one unbolded line (used when collecting the block).
type syncLine struct {
	elem *docs.ParagraphElement
	text string
	kind int
}

// syncBlock is the entire unbolded text before "done." as a single entry (kind: 0=plain, 1=question, 2=action).
type syncBlock struct {
	text        string
	startIndex  int64
	endIndex    int64
	kind        int
}

// syncCreateDocsService creates a Google Docs API service for sync.
func syncCreateDocsService(ctx context.Context) (*docs.Service, error) {
	if ServiceAccountFile != "" {
		return docs.NewService(ctx, option.WithCredentialsFile(ServiceAccountFile))
	}
	return docs.NewService(ctx)
}

// syncFetchDoc fetches the configured Google Doc by ID.
func syncFetchDoc(ctx context.Context, docsService *docs.Service, documentID string) (*docs.Document, error) {
	return docsService.Documents.Get(documentID).Do()
}

// findSyncDoneTrigger finds the first unbolded "done" trigger in the document body. Returns (nil, 0, 0) if none found.
func findSyncDoneTrigger(doc *docs.Document) (elem *docs.TextRun, startIndex, endIndex int64) {
	if doc.Body == nil {
		return nil, 0, 0
	}
	for _, element := range doc.Body.Content {
		if element.Paragraph == nil {
			continue
		}
		for _, e := range element.Paragraph.Elements {
			if e.TextRun == nil {
				continue
			}
			text := strings.TrimSpace(e.TextRun.Content)
			if strings.HasPrefix(strings.ToLower(text), "done") && (e.TextRun.TextStyle == nil || !e.TextRun.TextStyle.Bold) {
				return e.TextRun, e.StartIndex, e.EndIndex
			}
		}
	}
	return nil, 0, 0
}

// collectSyncBlock gathers all unbolded lines before the given end index (e.g. before "done.") as one block.
// Returns nil if there are no unbolded lines.
func collectSyncBlock(doc *docs.Document, beforeEndIndex int64) *syncBlock {
	var lines []syncLine
	if doc.Body == nil {
		return nil
	}
	for _, element := range doc.Body.Content {
		if element.Paragraph == nil {
			continue
		}
		for _, e := range element.Paragraph.Elements {
			if e.TextRun == nil || e.StartIndex >= beforeEndIndex {
				continue
			}
			textContent := e.TextRun.Content
			if textContent == "" {
				continue
			}
			text := strings.TrimSpace(textContent)
			if text == "" || (e.TextRun.TextStyle != nil && e.TextRun.TextStyle.Bold) {
				continue
			}
			kind := 0
			if strings.HasPrefix(text, "?") {
				kind = 1
			} else if strings.HasPrefix(text, "!") {
				kind = 2
			}
			lines = append(lines, syncLine{elem: e, text: text, kind: kind})
		}
	}
	if len(lines) == 0 {
		return nil
	}
	// Sort by document order (ascending start index) so combined text reads top-to-bottom.
	sort.Slice(lines, func(i, j int) bool { return lines[i].elem.StartIndex < lines[j].elem.StartIndex })
	parts := make([]string, len(lines))
	var startIndex, endIndex int64 = lines[0].elem.StartIndex, lines[0].elem.EndIndex
	for i, l := range lines {
		parts[i] = l.text
		if l.elem.StartIndex < startIndex {
			startIndex = l.elem.StartIndex
		}
		if l.elem.EndIndex > endIndex {
			endIndex = l.elem.EndIndex
		}
	}
	kind := lines[0].kind
	return &syncBlock{
		text:       strings.Join(parts, "\n"),
		startIndex: startIndex,
		endIndex:   endIndex,
		kind:       kind,
	}
}

// buildSyncRequests builds Docs API requests for "done." and one sync block (single entry: question, action, or plain).
func buildSyncRequests(ctx context.Context, doneStartIndex, doneEndIndex int64, block *syncBlock) ([]*docs.Request, int, int, int) {
	source := "gdoc"
	var requests []*docs.Request
	requests = append(requests,
		&docs.Request{
			InsertText: &docs.InsertTextRequest{
				Location: &docs.Location{Index: doneEndIndex - 1},
				Text:     " [processed]",
			},
		},
		&docs.Request{
			UpdateTextStyle: &docs.UpdateTextStyleRequest{
				Range:     &docs.Range{StartIndex: doneStartIndex, EndIndex: doneEndIndex},
				TextStyle: &docs.TextStyle{Bold: true},
				Fields:    "bold",
			},
		},
	)
	entriesAdded := 0
	questionsAnswered := 0
	actionsExecuted := 0
	if block == nil || block.text == "" {
		return requests, entriesAdded, questionsAnswered, actionsExecuted
	}
	text := strings.TrimSpace(block.text)
	switch block.kind {
	case 1:
		question := strings.TrimPrefix(text, "?")
		question = strings.TrimSpace(question)
		if question != "" {
			LoggerFrom(ctx).Info("processing question", "question", truncateString(question, 80))
			queryStart := time.Now()
			answer := GetAnswer(ctx, question, source)
			LoggerFrom(ctx).Info("question answered", "duration_ms", time.Since(queryStart).Milliseconds())
			inserted := "\n" + answer
			// Order: bold block first, then insert after block, then bold inserted text (so indices stay valid).
			requests = append(requests,
				&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.startIndex, EndIndex: block.endIndex}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
				&docs.Request{InsertText: &docs.InsertTextRequest{Location: &docs.Location{Index: block.endIndex}, Text: inserted}},
				&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.endIndex, EndIndex: block.endIndex + docIndexLen(inserted)}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
			)
			questionsAnswered++
		}
	case 2:
		action := strings.TrimPrefix(text, "!")
		action = strings.TrimSpace(action)
		if action != "" {
			LoggerFrom(ctx).Info("processing action", "action", truncateString(action, 80))
			actionStart := time.Now()
			result := GetAnswer(ctx, "Execute this action and confirm what you did: "+action, source)
			LoggerFrom(ctx).Info("action executed", "duration_ms", time.Since(actionStart).Milliseconds())
			inserted := "\n✓ " + result
			requests = append(requests,
				&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.startIndex, EndIndex: block.endIndex}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
				&docs.Request{InsertText: &docs.InsertTextRequest{Location: &docs.Location{Index: block.endIndex}, Text: inserted}},
				&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.endIndex, EndIndex: block.endIndex + docIndexLen(inserted)}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
			)
			actionsExecuted++
		}
	default:
		LoggerFrom(ctx).Info("processing input", "text", truncateString(text, 80))
		processStart := time.Now()
		response := GetAnswer(ctx, text, source)
		LoggerFrom(ctx).Info("input processed", "duration_ms", time.Since(processStart).Milliseconds())
		inserted := "\n→ " + response
		requests = append(requests,
			&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.startIndex, EndIndex: block.endIndex}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
			&docs.Request{InsertText: &docs.InsertTextRequest{Location: &docs.Location{Index: block.endIndex}, Text: inserted}},
			&docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: &docs.Range{StartIndex: block.endIndex, EndIndex: block.endIndex + docIndexLen(inserted)}, TextStyle: &docs.TextStyle{Bold: true}, Fields: "bold"}},
		)
		entriesAdded++
	}
	return requests, entriesAdded, questionsAnswered, actionsExecuted
}

// syncApplyBatchUpdate applies batch update requests to the document.
func syncApplyBatchUpdate(docsService *docs.Service, documentID string, requests []*docs.Request) error {
	_, err := docsService.Documents.BatchUpdate(documentID, &docs.BatchUpdateDocumentRequest{
		Requests: requests,
	}).Do()
	return err
}

func handleSync(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	ctx := r.Context()

	ctx, span := StartSpan(ctx, "sync.gdoc")
	defer span.End()

	LoggerFrom(ctx).Info("sync started")

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	if DocumentID == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "DOCUMENT_ID not configured"})
		return
	}

	// Acquire distributed lock to prevent concurrent syncs
	lockAcquired, lockErr := acquireSyncLock(ctx)
	if lockErr != nil {
		LoggerFrom(ctx).Error("failed to check sync lock", "error", lockErr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to check sync lock"})
		return
	}
	if !lockAcquired {
		LoggerFrom(ctx).Info("sync skipped", "reason", "already in progress")
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Another sync is already in progress. Please wait and try again."})
		return
	}
	defer releaseSyncLock(ctx)

	var docsService *docs.Service
	var doc *docs.Document
	var err error
	docsService, err = syncCreateDocsService(ctx)
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("sync failed", "stage", "DocsService", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to create Docs service: %v", err)})
		return
	}

	docFetchStart := time.Now()
	doc, err = syncFetchDoc(ctx, docsService, DocumentID)
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("sync failed", "stage", "FetchDoc", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to fetch document: %v", err)})
		return
	}
	LoggerFrom(ctx).Debug("doc fetched", "duration_ms", time.Since(docFetchStart).Milliseconds())

	if doc.Body == nil || len(doc.Body.Content) == 0 {
		LoggerFrom(ctx).Info("sync skipped", "reason", "document has no body")
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"message":   "Document has no body",
			"processed": 0,
		})
		return
	}

	doneElem, doneStartIndex, doneEndIndex := findSyncDoneTrigger(doc)
	if doneElem == nil {
		LoggerFrom(ctx).Info("sync skipped", "reason", "no done. trigger")
		LoggerFrom(ctx).Debug("no unbolded 'done.' found, skipping sync")
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"message":   "No 'done.' trigger found",
			"processed": 0,
		})
		return
	}
	_ = doneElem // used only to confirm presence

	LoggerFrom(ctx).Info("found 'done.' trigger, processing document")

	block := collectSyncBlock(doc, doneEndIndex)
	requests, entriesAdded, questionsAnswered, actionsExecuted := buildSyncRequests(ctx, doneStartIndex, doneEndIndex, block)

	if len(requests) > 0 {
		updateStart := time.Now()
		err = syncApplyBatchUpdate(docsService, DocumentID, requests)
		if err != nil {
			LoggerFrom(ctx).Error("doc update failed", "error", err)
			span.RecordError(err)
			LoggerFrom(ctx).Error("sync failed", "stage", "UpdateDoc", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to update document: %v", err)})
			return
		}
		LoggerFrom(ctx).Debug("doc updated", "duration_ms", time.Since(updateStart).Milliseconds())
	}

	totalTime := time.Since(startTime)
	totalProcessed := entriesAdded + questionsAnswered + actionsExecuted
	LoggerFrom(ctx).Info("sync completed",
		"entries_added", entriesAdded,
		"questions_answered", questionsAnswered,
		"actions_executed", actionsExecuted,
		"duration_ms", totalTime.Milliseconds(),
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":            true,
		"entries_added":      entriesAdded,
		"questions_answered": questionsAnswered,
		"actions_executed":   actionsExecuted,
		"total_processed":    totalProcessed,
	})
}

func handleDream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	ctx := r.Context()
	LoggerFrom(ctx).Info("dream started")

	// Bound total dream time (specialists + fact writes)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	result, err := RunDreamer(ctx)
	if err != nil {
		ErrorsTotal.Inc()
		LoggerFrom(ctx).Error("dreamer failed", "error", err)
		code := http.StatusInternalServerError
		if IsLLMQuotaOrBillingError(err) {
			code = http.StatusTooManyRequests // 429
		} else if IsLLMPermissionOrBillingDenied(err) {
			code = http.StatusForbidden // 403
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}

	// Run Pulse Audit after Dreamer to flag stale high-value nodes
	pulseResult, pulseErr := RunPulseAudit(ctx)
	if pulseErr != nil {
		LoggerFrom(ctx).Warn("pulse audit failed after dreamer", "error", pulseErr)
	}

	LoggerFrom(ctx).Info("dream completed", "entries_processed", result.EntriesProcessed, "facts_extracted", result.FactsExtracted, "facts_written", result.FactsWritten)
	if pulseResult != nil && (pulseResult.Signals > 0 || len(pulseResult.StaleNodes) > 0) {
		LoggerFrom(ctx).Info("dream pulse", "signals", pulseResult.Signals, "stale_nodes", len(pulseResult.StaleNodes))
	}

	resp := map[string]interface{}{
		"success":           true,
		"entries_processed": result.EntriesProcessed,
		"facts_extracted":   result.FactsExtracted,
		"facts_written":     result.FactsWritten,
	}
	if pulseResult != nil {
		resp["pulse_signals"] = pulseResult.Signals
		resp["pulse_stale_nodes"] = len(pulseResult.StaleNodes)
	}

	writeJSON(w, http.StatusOK, resp)
}

func handleJanitor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	ctx := r.Context()
	LoggerFrom(ctx).Info("janitor started")
	deleted, err := RunJanitor(ctx)
	if err != nil {
		ErrorsTotal.Inc()
		LoggerFrom(ctx).Error("janitor failed", "error", err)
		code := http.StatusInternalServerError
		if IsLLMQuotaOrBillingError(err) {
			code = http.StatusTooManyRequests // 429
		} else if IsLLMPermissionOrBillingDenied(err) {
			code = http.StatusForbidden // 403
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	LoggerFrom(ctx).Info("janitor completed", "deleted", deleted)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"deleted": deleted,
	})
}

// SubmitAsync submits a task to the app's async fire-and-forget pool (no-op if no App in context).
func SubmitAsync(ctx context.Context, task func()) {
	if app := GetApp(ctx); app != nil {
		app.SubmitAsync(task)
	}
}

// SubmitToolExec submits a task to the app's tool execution pool (no-op if no App in context).
func SubmitToolExec(ctx context.Context, task func()) {
	if app := GetApp(ctx); app != nil {
		app.SubmitToolExec(task)
	}
}

// SubmitSummaryGen submits a task to the app's summary generation pool (no-op if no App in context).
func SubmitSummaryGen(ctx context.Context, task func()) {
	if app := GetApp(ctx); app != nil {
		app.SubmitSummaryGen(task)
	}
}

// SubmitGDocLog submits a message to the Google Doc log pool. Uses App from context when present.
// If ctx has no App (e.g. Logger.Info() during a request), uses GetDefaultApp() only after the app
// has been created by a request (MarkDefaultAppReady), so we never trigger Firestore/Gemini init during package init (Cloud Run startup).
func SubmitGDocLog(ctx context.Context, msg string) {
	app := GetApp(ctx)
	if app == nil && atomic.LoadUint32(&defaultAppReady) != 0 {
		app, _ = GetDefaultApp()
	}
	if app != nil {
		app.SubmitGDocLog(ctx, msg)
	}
}

// gdocLogPayload carries context and message for async GDoc logging.
type gdocLogPayload struct {
	ctx context.Context
	msg string
}

// docIndexLen returns the length of s in UTF-16 code units, which is what the Google Docs API
// uses for StartIndex/EndIndex. Use this for any index math involving inserted text (emojis and
// other characters outside the BMP count as 2 units).
func docIndexLen(s string) int64 {
	return int64(len(utf16.Encode([]rune(s))))
}

// scanForLogsStanza finds [LOGS] and [/LOGS] in body content and nested table cells, updating the given indices.
func scanForLogsStanza(content []*docs.StructuralElement, logsStartIndex, logsEndTagStart *int64) {
	if content == nil {
		return
	}
	for _, element := range content {
		if element == nil {
			continue
		}
		if element.Paragraph != nil {
			for _, elem := range element.Paragraph.Elements {
				if elem.TextRun == nil {
					continue
				}
				text := strings.TrimSpace(elem.TextRun.Content)
				if text == "[LOGS]" {
					*logsStartIndex = elem.StartIndex
				}
				if text == "[/LOGS]" {
					*logsEndTagStart = elem.StartIndex
				}
			}
			continue
		}
		if element.Table != nil {
			for _, row := range element.Table.TableRows {
				if row == nil {
					continue
				}
				for _, cell := range row.TableCells {
					if cell == nil || len(cell.Content) == 0 {
						continue
					}
					scanForLogsStanza(cell.Content, logsStartIndex, logsEndTagStart)
				}
			}
		}
	}
}

// logToGDocSync is the synchronous implementation of writing a log line to the Google Doc (called by the gdoc appender pool).
func logToGDocSync(ctx context.Context, message string) {
	ctx = WithGDocLogging(ctx) // prevent forwarding our own logs to gdoc
	ctx, span := StartSpan(ctx, "gdoc.log")
	defer span.End()

	// Create Docs service
	var docsService *docs.Service
	var err error

	if ServiceAccountFile != "" {
		docsService, err = docs.NewService(ctx, option.WithCredentialsFile(ServiceAccountFile))
	} else {
		docsService, err = docs.NewService(ctx)
	}
	if err != nil {
		LoggerFrom(ctx).Error("failed to create Docs service for logging", "error", err)
		span.RecordError(err)
		return
	}

	// Get document
	doc, err := docsService.Documents.Get(DocumentID).Do()
	if err != nil {
		LoggerFrom(ctx).Error("failed to fetch document for logging", "error", err)
		span.RecordError(err)
		return
	}

	// Find the [LOGS] and [/LOGS] section (must both exist to log). Search body and tables.
	var logsStartIndex, logsEndTagStart int64 = -1, -1
	scanForLogsStanza(doc.Body.Content, &logsStartIndex, &logsEndTagStart)

	// Only log if [LOGS] and [/LOGS] already exist; do not create the section
	if logsStartIndex == -1 || logsEndTagStart == -1 {
		LoggerFrom(ctx).Debug("gdoc logging skipped (no [LOGS] [/LOGS] section in document)")
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logLine := fmt.Sprintf("%s: %s", timestamp, message)
	insertText := logLine + "\n"

	requests := []*docs.Request{
		{
			InsertText: &docs.InsertTextRequest{
				Location: &docs.Location{Index: logsEndTagStart},
				Text:     insertText,
			},
		},
		{
			UpdateTextStyle: &docs.UpdateTextStyleRequest{
				Range: &docs.Range{
					StartIndex: logsEndTagStart,
					EndIndex:   logsEndTagStart + docIndexLen(insertText),
				},
				TextStyle: &docs.TextStyle{Bold: true},
				Fields:    "bold",
			},
		},
	}

	_, err = docsService.Documents.BatchUpdate(DocumentID, &docs.BatchUpdateDocumentRequest{
		Requests: requests,
	}).Do()
	if err != nil {
		LoggerFrom(ctx).Error("failed to write log to gdoc", "error", err)
		span.RecordError(err)
		return
	}

	LoggerFrom(ctx).Debug("logged to gdoc", "message", message)
}

func handleProcessEntry(w http.ResponseWriter, r *http.Request) {
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

// ProcessEntry runs evaluator, context detection, and embedding for an entry. Used by handleProcessEntry and by AddEntry when enqueue fails (e.g. JOT_API_URL not set).
func ProcessEntry(ctx context.Context, entryUUID, content, timestamp, source string) error {
	LoggerFrom(ctx).Info("process-entry start", "entry_uuid", entryUUID, "content", truncateString(content, 50), "source", source)
	RunEvaluator(ctx, content, entryUUID, timestamp)

	contextUUIDs, err := DetectOrCreateContext(ctx, content, entryUUID)
	if err != nil {
		LoggerFrom(ctx).Warn("context detection failed", "error", err)
	}
	contextCount := len(contextUUIDs)

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
	_, err = client.Collection(EntriesCollection).Doc(entryUUID).Update(ctx, []firestore.Update{
		{Path: "embedding", Value: firestore.Vector32(vector)},
	})
	if err != nil {
		LoggerFrom(ctx).Warn("failed to store entry embedding", "entry_uuid", entryUUID, "error", err)
		return err
	}
	LoggerFrom(ctx).Info("process-entry done", "entry_uuid", entryUUID, "contexts_linked", contextCount, "embedding_dims", len(vector))
	return nil
}

func handleSaveQuery(w http.ResponseWriter, r *http.Request) {
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

func handleWebhook(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	ctx := r.Context()

	ctx, span := StartSpan(ctx, "webhook.gdrive")
	defer span.End()

	// Respond to sync request (initial subscription verification)
	if r.Header.Get("X-Goog-Resource-State") == "sync" {
		LoggerFrom(ctx).Info("webhook", "event", "Drive sync verification (ack)")
		writeJSON(w, http.StatusOK, map[string]string{"status": "sync acknowledged"})
		return
	}

	resourceState := r.Header.Get("X-Goog-Resource-State")
	span.SetAttributes(map[string]string{"resource_state": resourceState})
	if resourceState != "change" && resourceState != "update" {
		LoggerFrom(ctx).Info("webhook ignored", "resource_state", resourceState)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "ignored",
			"reason": fmt.Sprintf("resource_state=%s", resourceState),
		})
		return
	}

	if SyncGDocURL == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "SYNC_GDOC_URL not configured"})
		return
	}

	// Fixed 5-second debounce
	debounceSeconds := 5

	// Create Cloud Tasks client
	tasksClient, err := cloudtasks.NewClient(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to create Tasks client: %v", err)})
		return
	}
	defer tasksClient.Close()

	parent := fmt.Sprintf("projects/%s/locations/%s/queues/%s", GoogleCloudProject, CloudTasksLocation, CloudTasksQueue)

	// Use Firestore to track current pending task
	fsClient, err := GetFirestoreClient(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to get Firestore client: %v", err)})
		return
	}

	debounceRef := fsClient.Collection(SystemCollection).Doc("sync_debounce")

	// Try to delete the previous pending task (reset the timer)
	if doc, err := debounceRef.Get(ctx); err == nil && doc.Exists() {
		data := doc.Data()
		if oldTaskName, ok := data["task_name"].(string); ok && oldTaskName != "" {
			if err := tasksClient.DeleteTask(ctx, &cloudtaskspb.DeleteTaskRequest{Name: oldTaskName}); err != nil {
				LoggerFrom(ctx).Debug("failed to delete old task (may have already executed)", "error", err)
			} else {
				LoggerFrom(ctx).Debug("cancelled previous sync task")
			}
		}
	}

	// Create new task with unique name
	taskID := fmt.Sprintf("jot-sync-%s", uuid.New().String()[:8])
	taskName := fmt.Sprintf("%s/tasks/%s", parent, taskID)

	scheduleTime := time.Now().Add(time.Duration(debounceSeconds) * time.Second)

	task := &cloudtaskspb.Task{
		Name: taskName,
		MessageType: &cloudtaskspb.Task_HttpRequest{
			HttpRequest: &cloudtaskspb.HttpRequest{
				HttpMethod: cloudtaskspb.HttpMethod_POST,
				Url:        SyncGDocURL,
				Headers: map[string]string{
					"Content-Type": "application/json",
					"X-API-Key":    JotAPIKey,
				},
			},
		},
		ScheduleTime: timestamppb.New(scheduleTime),
	}

	_, err = tasksClient.CreateTask(ctx, &cloudtaskspb.CreateTaskRequest{
		Parent: parent,
		Task:   task,
	})
	if err != nil {
		span.RecordError(err)
		LoggerFrom(ctx).Error("webhook failed to schedule sync", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to create task: %v", err)})
		return
	}
	LoggerFrom(ctx).Info("webhook", "event", "Drive change, sync scheduled", "delay_seconds", debounceSeconds, "task_id", taskID)

	// Store the new task name for future cancellation (async via pool, fire-and-forget)
	SubmitAsync(ctx, func() {
		if _, err := debounceRef.Set(ctx, map[string]interface{}{
			"task_name":      taskName,
			"scheduled_time": scheduleTime.Format(time.RFC3339),
		}); err != nil {
			LoggerFrom(ctx).Warn("failed to store debounce state", "error", err)
		}
	})

	totalTime := time.Since(startTime)
	LoggerFrom(ctx).Debug("webhook completed", "duration_ms", totalTime.Milliseconds())

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         "scheduled",
		"message":        fmt.Sprintf("Sync scheduled for %d seconds from now", debounceSeconds),
		"scheduled_time": scheduleTime.Format(time.RFC3339),
	})
}

func handleSMS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ctx, span := StartSpan(ctx, "sms.webhook")
	defer span.End()

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	// Get the webhook URL for signature validation
	webhookURL := fmt.Sprintf("https://us-central1-%s.cloudfunctions.net/jot-api-go/sms", GoogleCloudProject)

	// Validate Twilio signature
	if !ValidateTwilioSignature(r, webhookURL) {
		LoggerFrom(ctx).Warn("invalid Twilio signature")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid signature"})
		return
	}

	// Parse the webhook
	msg, err := ParseTwilioWebhook(r)
	if err != nil {
		LoggerFrom(ctx).Error("failed to parse Twilio webhook", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		return
	}

	span.SetAttributes(map[string]string{
		"sms.from":        msg.From,
		"sms.message_sid": msg.MessageSid,
	})

	// Validate sender phone number
	if !IsAllowedPhoneNumber(msg.From) {
		LoggerFrom(ctx).Warn("SMS from unauthorized number", "from", msg.From)
		// Return 200 to Twilio but don't process
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`))
		return
	}

	bodyPreview := strings.TrimSpace(msg.Body)
	if bodyPreview == "" {
		bodyPreview = "(empty)"
	} else {
		bodyPreview = truncateString(bodyPreview, 80)
	}
	LoggerFrom(ctx).Info("sms webhook", "from", msg.From, "sid", msg.MessageSid, "body", bodyPreview)

	// Respond immediately so Twilio (and you) know the webhook was called.
	// Twilio expects a response within ~15s; processing (jot entry/query) runs in background.
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`))

	LoggerFrom(ctx).Info("sms responded 200, processing in background")

	// Process in background and send the actual reply via SMS
	go func() {
		bgCtx := context.Background()
		LoggerFrom(bgCtx).Info("sms processing", "from", msg.From)
		response := ProcessIncomingSMS(bgCtx, msg)
		if response != "" {
			if err := SendSMS(bgCtx, msg.From, response); err != nil {
				LoggerFrom(bgCtx).Error("sms reply failed", "to", msg.From, "error", err)
			} else {
				LoggerFrom(bgCtx).Info("sms reply sent", "to", msg.From, "preview", truncateString(response, 60))
			}
		} else {
			LoggerFrom(bgCtx).Info("sms processed", "from", msg.From, "reply", "none")
		}
	}()
}

// Sync lock constants
const (
	syncLockCollection = "system"
	syncLockDocument   = "sync_lock"
	syncLockTimeout    = 15 * time.Minute // Auto-release lock after this duration (covers multiple RunQuery calls, each up to timeout.QuerySeconds)
)

// acquireSyncLock attempts to acquire a distributed lock for sync operations.
// Returns true if lock was acquired, false if another sync is in progress.
// Lock automatically expires after syncLockTimeout to prevent deadlocks.
func acquireSyncLock(ctx context.Context) (bool, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil || client == nil {
		// No Firestore = no locking, allow sync
		return true, nil
	}

	lockRef := client.Collection(syncLockCollection).Doc(syncLockDocument)

	err = client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(lockRef)

		now := time.Now()

		if err != nil {
			if status.Code(err) == codes.NotFound {
				// No lock exists, create one
				return tx.Set(lockRef, map[string]interface{}{
					"locked_at": now,
					"locked_by": "sync",
				})
			}
			return err
		}

		// Check if existing lock has expired
		if lockedAt, ok := doc.Data()["locked_at"].(time.Time); ok {
			if now.Sub(lockedAt) > syncLockTimeout {
				// Lock expired, acquire it
				return tx.Set(lockRef, map[string]interface{}{
					"locked_at": now,
					"locked_by": "sync",
				})
			}
		}

		// Lock is held by another sync
		return fmt.Errorf("lock held")
	})

	if err != nil {
		if err.Error() == "lock held" {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// releaseSyncLock releases the distributed sync lock.
func releaseSyncLock(ctx context.Context) {
	client, err := GetFirestoreClient(ctx)
	if err != nil || client == nil {
		return
	}

	lockRef := client.Collection(syncLockCollection).Doc(syncLockDocument)
	_, err = lockRef.Delete(ctx)
	if err != nil {
		LoggerFrom(ctx).Error("failed to release sync lock", "error", err)
	}
}
