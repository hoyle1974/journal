package jot

import (
	"context"

	"github.com/jackstrohm/jot/pkg/agent"
)

// Types re-exported from agent for compatibility.
type (
	EvaluatorExtract     = agent.EvaluatorExtract
	Domain               = agent.Domain
	SpecialistInput      = agent.SpecialistInput
	SpecialistOutput     = agent.SpecialistOutput
	EvolutionAuditOutput = agent.EvolutionAuditOutput
	DecompositionResult  = agent.DecompositionResult
)

// Domain constants re-exported.
const (
	DomainRelationship = agent.DomainRelationship
	DomainWork         = agent.DomainWork
	DomainTask         = agent.DomainTask
	DomainThought      = agent.DomainThought
	DomainSelfModel    = agent.DomainSelfModel
	DomainEvolution    = agent.DomainEvolution
)

// RunEvaluatorExtract runs the evaluator LLM on content and returns significance, domain, and fact_to_store.
func RunEvaluatorExtract(ctx context.Context, content string) (*EvaluatorExtract, error) {
	return agent.RunEvaluatorExtract(ctx, jotFOHEnv{}, content)
}

// RunEvaluator assigns significance to a new entry and optionally upserts high-value facts.
func RunEvaluator(ctx context.Context, content, entryUUID, timestamp string) {
	agent.RunEvaluator(ctx, jotFOHEnv{}, content, entryUUID, timestamp)
}

// RunEvolutionAudit runs the Cognitive Engineer on recent queries.
func RunEvolutionAudit(ctx context.Context, queriesText, journalSummary string) (*EvolutionAuditOutput, error) {
	return agent.RunEvolutionAudit(ctx, queriesText, journalSummary)
}

// RunSpecialist runs a single specialist agent.
func RunSpecialist(ctx context.Context, domain Domain, input *SpecialistInput, modelOverride string) (*SpecialistOutput, error) {
	return agent.RunSpecialist(ctx, domain, input, modelOverride)
}

// RunContextExtractor uses Gemini to extract impacted_contexts from the journal.
func RunContextExtractor(ctx context.Context, journalContext string) ([]string, error) {
	return agent.RunContextExtractor(ctx, journalContext)
}

// RunQueryAnalyzer uses Gemini to analyze recent queries.
func RunQueryAnalyzer(ctx context.Context, recentQueriesText string) (string, error) {
	return agent.RunQueryAnalyzer(ctx, recentQueriesText)
}

// DecomposeMessage uses the LLM to determine which specialists to consult.
func DecomposeMessage(ctx context.Context, userMessage string) ([]Domain, error) {
	return agent.DecomposeMessage(ctx, userMessage)
}

// RunCommittee runs the selected specialists in parallel.
func RunCommittee(ctx context.Context, userMessage, journalContext string, domains []Domain) ([]*SpecialistOutput, error) {
	return agent.RunCommittee(ctx, userMessage, journalContext, domains)
}
