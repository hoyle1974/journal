package service

import (
	"context"
	"net/http"

	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/pkg/infra"
)

// ConfigGetter returns the current config (allows tests to override).
type ConfigGetter func() *config.Config

// SMSService handles Twilio/SMS operations for the API.
type SMSService struct {
	getConfig ConfigGetter
}

// NewSMSService returns an SMSService. getConfig is for Twilio/config. Callers pass app to ProcessIncomingSMS.
func NewSMSService(getConfig ConfigGetter) *SMSService {
	return &SMSService{getConfig: getConfig}
}

func (s *SMSService) cfg() *config.Config {
	if s.getConfig != nil {
		return s.getConfig()
	}
	return nil
}

// ValidateTwilioSignature validates the Twilio webhook signature.
func (s *SMSService) ValidateTwilioSignature(r *http.Request, webhookURL string) bool {
	return infra.ValidateTwilioSignature(s.cfg(), r, webhookURL)
}

// ParseTwilioWebhook parses the Twilio webhook request body.
func (s *SMSService) ParseTwilioWebhook(r *http.Request) (*infra.TwilioWebhookRequest, error) {
	return infra.ParseTwilioWebhook(r)
}

// IsAllowedPhoneNumber returns whether the phone number is allowed.
func (s *SMSService) IsAllowedPhoneNumber(phone string) bool {
	return infra.IsAllowedPhoneNumber(s.cfg(), phone)
}

// ProcessIncomingSMS processes an incoming SMS and returns the response body. app must be non-nil.
func (s *SMSService) ProcessIncomingSMS(ctx context.Context, app *infra.App, msg *infra.TwilioWebhookRequest) string {
	if msg == nil {
		return ""
	}
	return ProcessIncomingSMS(ctx, app, msg)
}

// SendSMS sends an SMS via Twilio.
func (s *SMSService) SendSMS(ctx context.Context, to, body string) error {
	return infra.SendSMS(ctx, s.cfg(), to, body)
}
