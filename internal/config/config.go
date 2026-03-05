package config

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
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
	cfg.TwilioAccountSID = loadSecret(cfg.GoogleCloudProject, "TWILIO_ACCOUNT_SID")
	cfg.TwilioAuthToken = loadSecret(cfg.GoogleCloudProject, "TWILIO_AUTH_TOKEN")
	cfg.TwilioPhoneNumber = loadSecretWithDefault(cfg.GoogleCloudProject, "TWILIO_PHONE_NUMBER", "")
	cfg.AllowedPhoneNumber = loadSecretWithDefault(cfg.GoogleCloudProject, "ALLOWED_PHONE_NUMBER", "")

	return cfg, nil
}

func loadEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
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
