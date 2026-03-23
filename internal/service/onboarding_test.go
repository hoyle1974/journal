package service

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/infra"
	"google.golang.org/genai"
)

// TestRunFirstRunOnboarding_Integration runs against the Firestore emulator when
// FIRESTORE_EMULATOR_HOST is set. Covers first-run (writes _system/onboarding; may seed zero questions) and
// already-run (skips, idempotent).
func TestRunFirstRunOnboarding_Integration(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("Set FIRESTORE_EMULATOR_HOST to run (e.g. localhost:8080)")
	}
	ctx := context.Background()
	cfg := &config.Config{GoogleCloudProject: "test-project"}
	// Gemini factory that fails so we don't need API keys; app still has Firestore.
	noGemini := func(context.Context, *config.Config) (*genai.Client, string, error) {
		return nil, "", errors.New("no gemini in test")
	}
	app, _ := infra.NewApp(ctx, cfg, noGemini)
	if app == nil {
		t.Fatal("NewApp returned nil app")
	}
	client, err := app.Firestore(ctx)
	if err != nil {
		t.Fatalf("app.Firestore: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	// First run: should write _system/onboarding (onboarding seed list may be empty).
	if err := RunFirstRunOnboarding(ctx, app); err != nil {
		t.Fatalf("first RunFirstRunOnboarding: %v", err)
	}
	ref := client.Collection(infra.SystemCollection).Doc(infra.OnboardingDoc)
	doc, err := ref.Get(ctx)
	if err != nil {
		t.Fatalf("get _system/onboarding: %v", err)
	}
	if !doc.Exists() {
		t.Fatal("_system/onboarding doc should exist after first run")
	}
	data := doc.Data()
	if infra.GetStringField(data, "status") != "complete" {
		t.Errorf("status = %q, want complete", infra.GetStringField(data, "status"))
	}
	snap, err := client.Collection("pending_questions").Documents(ctx).GetAll()
	if err != nil {
		t.Fatalf("list pending_questions: %v", err)
	}
	if len(snap) != 0 {
		t.Errorf("pending_questions count = %d, want 0 (no onboarding seed questions)", len(snap))
	}

	// Second run: should skip (idempotent).
	if err := RunFirstRunOnboarding(ctx, app); err != nil {
		t.Fatalf("second RunFirstRunOnboarding: %v", err)
	}
	snap2, err := client.Collection("pending_questions").Documents(ctx).GetAll()
	if err != nil {
		t.Fatalf("list pending_questions after second run: %v", err)
	}
	if len(snap2) != 0 {
		t.Errorf("after second run pending_questions count = %d, want 0 (idempotent)", len(snap2))
	}
}
