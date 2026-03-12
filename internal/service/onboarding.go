package service

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/memory"
)

// onboardingQuestions is the seed set injected on first run.
// Extend this slice to add more onboarding prompts.
var onboardingQuestions = []struct {
	Question string
	Kind     string
}{
	{"What is your name?", "onboarding"},
	{"Describe your family.", "onboarding"},
	{"Who do you work for?", "onboarding"},
	{"What is your job or role?", "onboarding"},
}

const onboardingContext = "Initial setup — your answers will be stored as long-term identity facts."

// RunFirstRunOnboarding checks _system/onboarding and seeds pending_questions if this
// is the first time the system has started. Safe to call on every cold start.
// Writes _system/onboarding only after all questions are committed (write-last for idempotency).
func RunFirstRunOnboarding(ctx context.Context, app *infra.App) error {
	ctx = infra.WithApp(ctx, app)
	client, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("onboarding firestore client: %w", err)
	}
	ref := client.Collection(infra.SystemCollection).Doc(infra.OnboardingDoc)
	_, err = ref.Get(ctx)
	if err == nil {
		infra.LoggerFrom(ctx).Debug("first-run onboarding skipped", "reason", "already_complete")
		return nil
	}
	if status.Code(err) != codes.NotFound {
		return fmt.Errorf("onboarding check: %w", err)
	}
	// Doc not found → first run. Seed questions then write the doc last.

	now := time.Now().Format(time.RFC3339)
	questions := make([]memory.PendingQuestion, 0, len(onboardingQuestions))
	for _, q := range onboardingQuestions {
		questions = append(questions, memory.PendingQuestion{
			Question:  q.Question,
			Kind:      q.Kind,
			Context:   onboardingContext,
			CreatedAt: now,
		})
	}
	if err := memory.InsertPendingQuestions(ctx, questions); err != nil {
		return fmt.Errorf("onboarding seed questions: %w", err)
	}
	infra.LoggerFrom(ctx).Info("first-run onboarding seeded", "count", len(questions))

	_, err = ref.Set(ctx, map[string]interface{}{
		"status":    "complete",
		"seeded_at": now,
		"version":   1,
	})
	if err != nil {
		return fmt.Errorf("onboarding mark complete: %w", err)
	}
	return nil
}
