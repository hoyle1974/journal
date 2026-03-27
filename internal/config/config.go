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
	ServiceAccountFile string
	CloudTasksQueue    string
	CloudTasksLocation string
	JotAPIBaseURL      string // Base URL for Cloud Task targets (JOT_API_URL)
	GeminiAPIKey       string
	JotAPIKey          string
	GeminiModel        string

	// Telegram
	TelegramBotToken      string
	TelegramSecretToken   string
	AllowedTelegramUserID string

	// ImagesBucket is the GCS bucket name for journal image uploads (optional). Set JOT_IMAGES_BUCKET to enable --attach and Telegram photo ingestion.
	ImagesBucket string

	// Env is the deployment environment (e.g. production, staging, development). Set via JOT_ENV or GO_ENV; defaults to "production" when K_SERVICE is set, else "development".
	Env string

	// DebugReportEnabled controls whether a first-person narrative debug report is generated after each query
	// and logged asynchronously at debug level. Default true; set JOT_DEBUG_REPORT_DISABLED=true to disable.
	DebugReportEnabled bool
}

// Load reads environment and Secret Manager into a Config. Call once at startup.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{}
	cfg.GoogleCloudProject = loadEnv("GOOGLE_CLOUD_PROJECT", "")
	cfg.ServiceAccountFile = os.Getenv("SERVICE_ACCOUNT_FILE")
	cfg.CloudTasksQueue = loadEnv("CLOUD_TASKS_QUEUE", "jot-sync-queue")
	cfg.CloudTasksLocation = loadEnv("CLOUD_TASKS_LOCATION", "us-central1")
	cfg.JotAPIBaseURL = os.Getenv("JOT_API_URL")

	// Secrets (env first, then Secret Manager using project from env)
	cfg.GeminiAPIKey = loadSecret(cfg.GoogleCloudProject, "GEMINI_API_KEY")
	cfg.JotAPIKey = loadSecret(cfg.GoogleCloudProject, "JOT_API_KEY")
	cfg.GeminiModel = normalizeToFlash(loadEnv("GEMINI_MODEL", "gemini-2.5-flash"))
	if telegramWanted() {
		cfg.TelegramBotToken = loadSecretOptional(cfg.GoogleCloudProject, "TELEGRAM_BOT_TOKEN")
		cfg.TelegramSecretToken = loadSecretOptionalWithDefault(cfg.GoogleCloudProject, "TELEGRAM_SECRET_TOKEN", "")
		cfg.AllowedTelegramUserID = loadSecretOptionalWithDefault(cfg.GoogleCloudProject, "ALLOWED_TELEGRAM_USER_ID", "")
	}

	cfg.ImagesBucket = loadEnv("JOT_IMAGES_BUCKET", "")
	cfg.Env = loadEnv("JOT_ENV", loadEnv("GO_ENV", ""))
	if cfg.Env == "" {
		if os.Getenv("K_SERVICE") != "" {
			cfg.Env = "production"
		} else {
			cfg.Env = "development"
		}
	}

	// Debug report: default ON; set JOT_DEBUG_REPORT_DISABLED=true or 1 to disable.
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv("JOT_DEBUG_REPORT_DISABLED"))); v {
	case "true", "1":
		cfg.DebugReportEnabled = false
	default:
		cfg.DebugReportEnabled = true
	}

	return cfg, nil
}

func loadEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// telegramWanted returns true if any Telegram-related env var is set (caller intends to use Telegram).
func telegramWanted() bool {
	return os.Getenv("TELEGRAM_BOT_TOKEN") != "" ||
		os.Getenv("TELEGRAM_SECRET_TOKEN") != "" ||
		os.Getenv("ALLOWED_TELEGRAM_USER_ID") != ""
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
