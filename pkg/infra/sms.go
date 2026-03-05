package infra

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
	"unicode/utf8"

	"github.com/jackstrohm/jot/internal/config"
)

// TwilioWebhookRequest represents an incoming SMS from Twilio.
type TwilioWebhookRequest struct {
	MessageSid string
	From       string
	To         string
	Body       string
}

// ValidateTwilioSignature validates that a request is from Twilio.
func ValidateTwilioSignature(cfg *config.Config, r *http.Request, webhookURL string) bool {
	ctx := r.Context()
	if cfg == nil || cfg.TwilioAuthToken == "" {
		LoggerFrom(ctx).Warn("no TWILIO_AUTH_TOKEN configured, skipping signature validation")
		return true
	}

	signature := r.Header.Get("X-Twilio-Signature")
	if signature == "" {
		LoggerFrom(ctx).Warn("missing X-Twilio-Signature header")
		return false
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		LoggerFrom(ctx).Error("failed to read request body", "error", err)
		return false
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))

	values, err := url.ParseQuery(string(body))
	if err != nil {
		LoggerFrom(ctx).Error("failed to parse form values", "error", err)
		return false
	}

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
	mac := hmac.New(sha1.New, []byte(cfg.TwilioAuthToken))
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
	var result strings.Builder
	for i, r := range phone {
		if r == '+' && i == 0 {
			result.WriteRune(r)
		} else if r >= '0' && r <= '9' {
			result.WriteRune(r)
		}
	}

	normalized := result.String()
	if !strings.HasPrefix(normalized, "+") && len(normalized) >= 10 {
		if len(normalized) == 10 {
			normalized = "+1" + normalized
		} else {
			normalized = "+" + normalized
		}
	}

	return normalized
}

// IsAllowedPhoneNumber checks if the phone number is allowed.
func IsAllowedPhoneNumber(cfg *config.Config, phone string) bool {
	if cfg == nil || cfg.AllowedPhoneNumber == "" {
		return true
	}
	normalized := NormalizePhoneNumber(phone)
	allowed := NormalizePhoneNumber(cfg.AllowedPhoneNumber)
	return normalized == allowed
}

func truncateToMaxBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	runes := []rune(s)
	n := 0
	for i, r := range runes {
		n += utf8.RuneLen(r)
		if n > maxBytes {
			if i == 0 {
				return ""
			}
			return string(runes[:i])
		}
	}
	return s
}

// SendSMS sends an SMS via Twilio.
func SendSMS(ctx context.Context, cfg *config.Config, to, body string) error {
	ctx, span := StartSpan(ctx, "twilio.send_sms")
	defer span.End()

	if cfg == nil || cfg.TwilioAccountSID == "" || cfg.TwilioAuthToken == "" {
		return fmt.Errorf("twilio credentials not configured")
	}
	if cfg.TwilioPhoneNumber == "" {
		return fmt.Errorf("twilio phone number not configured")
	}

	if len(body) > 1500 {
		body = truncateToMaxBytes(body, 1497) + "..."
	}

	apiURL := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", cfg.TwilioAccountSID)

	data := url.Values{}
	data.Set("To", to)
	data.Set("From", cfg.TwilioPhoneNumber)
	data.Set("Body", body)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth(cfg.TwilioAccountSID, cfg.TwilioAuthToken)
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
