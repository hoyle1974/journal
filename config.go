// Package jot provides the Jot journal cloud function.
package jot

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

// Collection names for Firestore
const (
	EntriesCollection  = "entries"
	QueriesCollection  = "queries"
	SystemCollection   = "_system"
)

// Configuration loaded from environment/secrets
var (
	GoogleCloudProject string
	DocumentID         string
	ServiceAccountFile string
	CloudTasksQueue    string
	CloudTasksLocation string
	SyncGDocURL        string
	JotAPIBaseURL     string // Base URL for Cloud Task targets (JOT_API_URL); must be set for enqueue to work
	GeminiAPIKey       string
	JotAPIKey          string
	GeminiModel        string
	DreamerModel       string // Faster model for dreamer (default: flash)

	// Twilio configuration
	TwilioAccountSID   string
	TwilioAuthToken    string
	TwilioPhoneNumber  string
	AllowedPhoneNumber string
)

func init() {
	// Load .env for local dev (no-op if file missing; Cloud Run uses runtime env)
	_ = godotenv.Load()

	GoogleCloudProject = getEnv("GOOGLE_CLOUD_PROJECT", "")
	DocumentID = os.Getenv("DOCUMENT_ID")
	ServiceAccountFile = os.Getenv("SERVICE_ACCOUNT_FILE")
	CloudTasksQueue = getEnv("CLOUD_TASKS_QUEUE", "jot-sync-queue")
	CloudTasksLocation = getEnv("CLOUD_TASKS_LOCATION", "us-central1")
	SyncGDocURL = os.Getenv("SYNC_GDOC_URL")
	JotAPIBaseURL = os.Getenv("JOT_API_URL")

	// Load secrets (from Secret Manager in cloud, env vars locally)
	GeminiAPIKey = getSecret("GEMINI_API_KEY")
	JotAPIKey = getSecret("JOT_API_KEY")

	// Model configuration (can be overridden via environment). We use flash only; pro is not used.
	// 2.0-flash deprecated for new users; use 2.5-flash. At runtime we resolve if 404 (see gemini.go).
	GeminiModel = normalizeToFlash(getEnv("GEMINI_MODEL", "gemini-2.5-flash"))
	DreamerModel = normalizeToFlash(getEnv("DREAMER_MODEL", "gemini-2.5-flash"))

	// Twilio configuration (from Secret Manager in cloud, env vars locally)
	TwilioAccountSID = getSecret("TWILIO_ACCOUNT_SID")
	TwilioAuthToken = getSecret("TWILIO_AUTH_TOKEN")
	TwilioPhoneNumber = getSecretWithDefault("TWILIO_PHONE_NUMBER", "")
	AllowedPhoneNumber = getSecretWithDefault("ALLOWED_PHONE_NUMBER", "")
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// normalizeToFlash ensures we never use gemini-2.5-pro; use gemini-2.5-flash everywhere.
func normalizeToFlash(model string) string {
	if model == "" {
		return "gemini-2.5-flash"
	}
	if strings.Contains(model, "2.5-pro") {
		return "gemini-2.5-flash"
	}
	return model
}

// getSecretWithDefault loads a secret, returning defaultValue if not found.
func getSecretWithDefault(secretID, defaultValue string) string {
	if v := getSecret(secretID); v != "" {
		return v
	}
	return defaultValue
}

// getSecret loads a secret from Google Secret Manager.
// Falls back to environment variable if Secret Manager fails.
func getSecret(secretID string) string {
	// First check environment variable (for local dev)
	if v := os.Getenv(secretID); v != "" {
		return v
	}

	// Try Secret Manager
	ctx := context.Background()
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		log.Printf("Warning: Failed to create Secret Manager client: %v", err)
		return ""
	}
	defer client.Close()

	name := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", GoogleCloudProject, secretID)
	req := &secretmanagerpb.AccessSecretVersionRequest{Name: name}

	result, err := client.AccessSecretVersion(ctx, req)
	if err != nil {
		log.Printf("Warning: Failed to load secret %s from Secret Manager: %v", secretID, err)
		return ""
	}

	return string(result.Payload.Data)
}
