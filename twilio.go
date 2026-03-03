package jot

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// TwilioWebhookRequest represents an incoming SMS from Twilio.
type TwilioWebhookRequest struct {
	MessageSid string
	From       string
	To         string
	Body       string
}

// ValidateTwilioSignature validates that a request is from Twilio.
func ValidateTwilioSignature(r *http.Request, webhookURL string) bool {
	ctx := r.Context()
	if TwilioAuthToken == "" {
		LoggerFrom(ctx).Warn("no TWILIO_AUTH_TOKEN configured, skipping signature validation")
		return true
	}

	signature := r.Header.Get("X-Twilio-Signature")
	if signature == "" {
		LoggerFrom(ctx).Warn("missing X-Twilio-Signature header")
		return false
	}

	// Read and restore the body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		LoggerFrom(ctx).Error("failed to read request body", "error", err)
		return false
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))

	// Parse form values
	values, err := url.ParseQuery(string(body))
	if err != nil {
		LoggerFrom(ctx).Error("failed to parse form values", "error", err)
		return false
	}

	// Build the string to sign: URL + sorted params
	var keys []string
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var paramStr strings.Builder
	for _, k := range keys {
		paramStr.WriteString(k)
		paramStr.WriteString(values.Get(k))
	}

	stringToSign := webhookURL + paramStr.String()

	// Calculate HMAC-SHA1
	mac := hmac.New(sha1.New, []byte(TwilioAuthToken))
	mac.Write([]byte(stringToSign))
	expectedSig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		LoggerFrom(ctx).Warn("invalid Twilio signature",
			"expected", expectedSig,
			"received", signature,
			"url", webhookURL,
		)
		return false
	}

	return true
}

// ParseTwilioWebhook parses an incoming Twilio webhook request.
func ParseTwilioWebhook(r *http.Request) (*TwilioWebhookRequest, error) {
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("failed to parse form: %w", err)
	}

	return &TwilioWebhookRequest{
		MessageSid: r.FormValue("MessageSid"),
		From:       r.FormValue("From"),
		To:         r.FormValue("To"),
		Body:       r.FormValue("Body"),
	}, nil
}

// NormalizePhoneNumber normalizes a phone number to E.164 format.
func NormalizePhoneNumber(phone string) string {
	// Remove all non-digit characters except leading +
	var result strings.Builder
	for i, r := range phone {
		if r == '+' && i == 0 {
			result.WriteRune(r)
		} else if r >= '0' && r <= '9' {
			result.WriteRune(r)
		}
	}

	normalized := result.String()

	// Add + prefix if missing and looks like a full number
	if !strings.HasPrefix(normalized, "+") && len(normalized) >= 10 {
		// Assume US number if 10 digits
		if len(normalized) == 10 {
			normalized = "+1" + normalized
		} else {
			normalized = "+" + normalized
		}
	}

	return normalized
}

// IsAllowedPhoneNumber checks if the phone number is allowed.
func IsAllowedPhoneNumber(phone string) bool {
	if AllowedPhoneNumber == "" {
		return true
	}

	normalized := NormalizePhoneNumber(phone)
	allowed := NormalizePhoneNumber(AllowedPhoneNumber)

	return normalized == allowed
}

// SendSMS sends an SMS via Twilio.
func SendSMS(ctx context.Context, to, body string) error {
	ctx, span := StartSpan(ctx, "twilio.send_sms")
	defer span.End()

	if TwilioAccountSID == "" || TwilioAuthToken == "" {
		return fmt.Errorf("twilio credentials not configured")
	}

	if TwilioPhoneNumber == "" {
		return fmt.Errorf("twilio phone number not configured")
	}

	// Truncate message if too long (SMS limit is 1600 chars for concatenated)
	if len(body) > 1500 {
		body = truncateToMaxBytes(body, 1497) + "..."
	}

	apiURL := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", TwilioAccountSID)

	data := url.Values{}
	data.Set("To", to)
	data.Set("From", TwilioPhoneNumber)
	data.Set("Body", body)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(TwilioAccountSID, TwilioAuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send SMS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("twilio API error: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	LoggerFrom(ctx).Info("SMS sent", "to", to, "length", len(body))
	return nil
}

// ProcessIncomingSMS processes an incoming SMS and returns a response.
func ProcessIncomingSMS(ctx context.Context, msg *TwilioWebhookRequest) string {
	ctx, span := StartSpan(ctx, "twilio.process_sms")
	defer span.End()

	text := strings.TrimSpace(msg.Body)
	if text == "" {
		return "Empty message received."
	}

	LoggerFrom(ctx).Info("processing incoming SMS",
		"from", msg.From,
		"message_sid", msg.MessageSid,
		"body_length", len(text),
	)

	// Check for query prefix
	if strings.HasPrefix(text, "?") {
		query := strings.TrimSpace(strings.TrimPrefix(text, "?"))
		if query == "" {
			return "Please include a question after the ?"
		}
		return processQuerySMS(ctx, query, msg.From)
	}

	// Default: create a journal entry
	return processEntrySMS(ctx, text, msg.From)
}

func processQuerySMS(ctx context.Context, query, from string) string {
	ctx, span := StartSpan(ctx, "twilio.process_query")
	defer span.End()

	// Run the query agent (RunQuery already increments QueriesTotal)
	result := RunQuery(ctx, query, "sms")
	if result.Error {
		LoggerFrom(ctx).Error("query failed", "answer", result.Answer)
		return "Sorry, I couldn't process your query. Please try again."
	}

	return result.Answer
}

func processEntrySMS(ctx context.Context, text, from string) string {
	ctx, span := StartSpan(ctx, "twilio.process_entry")
	defer span.End()

	EntriesTotal.Inc()

	id, err := AddEntry(ctx, text, "sms", nil)
	if err != nil {
		LoggerFrom(ctx).Error("failed to store entry", "error", err)
		ErrorsTotal.Inc()
		return "Failed to save entry. Please try again."
	}

	LoggerFrom(ctx).Info("entry created via SMS", "id", id, "content_length", len(text))
	return "Noted!"
}
