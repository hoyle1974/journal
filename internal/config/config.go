package config

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/joho/godotenv"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Config holds configuration loaded from environment and Secret Manager.
type Config struct {
	GoogleCloudProject string
	DocumentID         string
	ServiceAccountFile string
	CloudTasksQueue    string
	CloudTasksLocation string
	SyncGDocURL        string
	JotAPIBaseURL      string // Base URL for Cloud Task targets (JOT_API_URL)
	GeminiAPIKey       string
	JotAPIKey          string
	GeminiModel        string
	DreamerModel       string // Faster model for dreamer (default: flash)

	// Twilio
	TwilioAccountSID   string
	TwilioAuthToken    string
	TwilioPhoneNumber  string
	AllowedPhoneNumber string

	// Env is the deployment environment (e.g. production, staging, development). Set via JOT_ENV or GO_ENV; defaults to "production" when K_SERVICE is set, else "development".
	Env string
}

// Load reads environment and Secret Manager into a Config. Call once at startup.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{}
	cfg.GoogleCloudProject = loadEnv("GOOGLE_CLOUD_PROJECT", "")
	cfg.DocumentID = os.Getenv("DOCUMENT_ID")
	cfg.ServiceAccountFile = os.Getenv("SERVICE_ACCOUNT_FILE")
	cfg.CloudTasksQueue = loadEnv("CLOUD_TASKS_QUEUE", "jot-sync-queue")
	cfg.CloudTasksLocation = loadEnv("CLOUD_TASKS_LOCATION", "us-central1")
	cfg.SyncGDocURL = os.Getenv("SYNC_GDOC_URL")
	cfg.JotAPIBaseURL = os.Getenv("JOT_API_URL")

	// Secrets (env first, then Secret Manager using project from env)
	cfg.GeminiAPIKey = loadSecret(cfg.GoogleCloudProject, "GEMINI_API_KEY")
	cfg.JotAPIKey = loadSecret(cfg.GoogleCloudProject, "JOT_API_KEY")
	cfg.GeminiModel = normalizeToFlash(loadEnv("GEMINI_MODEL", "gemini-2.5-flash"))
	cfg.DreamerModel = normalizeToFlash(loadEnv("DREAMER_MODEL", "gemini-2.5-flash"))
	// Only load Twilio secrets if at least one Twilio env var is set; otherwise skip Secret Manager to avoid NotFound warnings.
	if twilioWanted() {
		cfg.TwilioAccountSID = loadSecretOptional(cfg.GoogleCloudProject, "TWILIO_ACCOUNT_SID")
		cfg.TwilioAuthToken = loadSecretOptional(cfg.GoogleCloudProject, "TWILIO_AUTH_TOKEN")
		cfg.TwilioPhoneNumber = loadSecretOptionalWithDefault(cfg.GoogleCloudProject, "TWILIO_PHONE_NUMBER", "")
		cfg.AllowedPhoneNumber = loadSecretOptionalWithDefault(cfg.GoogleCloudProject, "ALLOWED_PHONE_NUMBER", "")
	}

	cfg.Env = loadEnv("JOT_ENV", loadEnv("GO_ENV", ""))
	if cfg.Env == "" {
		if os.Getenv("K_SERVICE") != "" {
			cfg.Env = "production"
		} else {
			cfg.Env = "development"
		}
	}

	return cfg, nil
}

func loadEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// twilioWanted returns true if any Twilio-related env var is set (caller intends to use Twilio).
func twilioWanted() bool {
	return os.Getenv("TWILIO_ACCOUNT_SID") != "" ||
		os.Getenv("TWILIO_AUTH_TOKEN") != "" ||
		os.Getenv("TWILIO_PHONE_NUMBER") != "" ||
		os.Getenv("ALLOWED_PHONE_NUMBER") != ""
}

func normalizeToFlash(model string) string {
	if model == "" {
		return "gemini-2.5-flash"
	}
	if strings.Contains(model, "2.5-pro") {
		return "gemini-2.5-flash"
	}
	return model
}

func loadSecretWithDefault(projectID, secretID, defaultValue string) string {
	if v := loadSecret(projectID, secretID); v != "" {
		return v
	}
	return defaultValue
}

// loadSecretOptional is like loadSecret but does not log when the secret is not found (NotFound).
// Use for optional features (e.g. Twilio) so production logs are not spammed.
func loadSecretOptional(projectID, secretID string) string {
	if v := os.Getenv(secretID); v != "" {
		return v
	}
	if projectID == "" {
		return ""
	}
	ctx := context.Background()
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		log.Printf("Warning: Failed to create Secret Manager client: %v", err)
		return ""
	}
	defer client.Close()
	name := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", projectID, secretID)
	req := &secretmanagerpb.AccessSecretVersionRequest{Name: name}
	result, err := client.AccessSecretVersion(ctx, req)
	if err != nil {
		if status.Code(err) != codes.NotFound {
			log.Printf("Warning: Failed to load secret %s from Secret Manager: %v", secretID, err)
		}
		return ""
	}
	return string(result.Payload.Data)
}

func loadSecretOptionalWithDefault(projectID, secretID, defaultValue string) string {
	if v := loadSecretOptional(projectID, secretID); v != "" {
		return v
	}
	return defaultValue
}

func loadSecret(projectID, secretID string) string {
	if v := os.Getenv(secretID); v != "" {
		return v
	}
	if projectID == "" {
		return ""
	}
	ctx := context.Background()
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		log.Printf("Warning: Failed to create Secret Manager client: %v", err)
		return ""
	}
	defer client.Close()
	name := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", projectID, secretID)
	req := &secretmanagerpb.AccessSecretVersionRequest{Name: name}
	result, err := client.AccessSecretVersion(ctx, req)
	if err != nil {
		log.Printf("Warning: Failed to load secret %s from Secret Manager: %v", secretID, err)
		return ""
	}
	return string(result.Payload.Data)
}
