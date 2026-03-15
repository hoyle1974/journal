package service

import (
	"context"
	"net/http"

	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/sms"
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
	return sms.ValidateTwilioSignature(r.Context(), s.cfg(), r, webhookURL, infra.LoggerFrom(r.Context()))
}

// ParseTwilioWebhook parses the Twilio webhook request body.
func (s *SMSService) ParseTwilioWebhook(r *http.Request) (*sms.TwilioWebhookRequest, error) {
	return sms.ParseTwilioWebhook(r)
}

// IsAllowedPhoneNumber returns whether the phone number is allowed.
func (s *SMSService) IsAllowedPhoneNumber(phone string) bool {
	return sms.IsAllowedPhoneNumber(s.cfg(), phone)
}

// ProcessIncomingSMS processes an incoming SMS and returns the response body. app must be non-nil.
func (s *SMSService) ProcessIncomingSMS(ctx context.Context, app *infra.App, msg *sms.TwilioWebhookRequest) string {
	if msg == nil {
		return ""
	}
	return ProcessIncomingSMS(ctx, app, msg)
}

// SendSMS sends an SMS via Twilio.
func (s *SMSService) SendSMS(ctx context.Context, to, body string) error {
	return sms.SendSMS(ctx, s.cfg(), to, body, infra.LoggerFrom(ctx))
}
