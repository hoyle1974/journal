package impl

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackstrohm/jot/pkg/agent"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/tools"
)

func init() {
	registerSpecialistTools()
}

func registerSpecialistTools() {
	for _, d := range []struct {
		name string
		dom  agent.Domain
		desc string
	}{
		{"consult_anthropologist", agent.DomainRelationship, "Ask the Anthropologist about relationships, people, social debt, influence. Use for 'who is X', 'how do I know Y', relationship questions."},
		{"consult_architect", agent.DomainWork, "Ask the Architect about work, projects, technical context, milestones, blockers. Use for professional/career questions."},
		{"consult_executive", agent.DomainTask, "Ask the Executive about tasks, logistics, planning, deadlines. Use for 'what do I need to do', task-related questions."},
		{"consult_philosopher", agent.DomainThought, "Ask the Philosopher about mood, reflections, personal growth, thought patterns. Use for introspective questions."},
	} {
		name, dom, desc := d.name, d.dom, d.desc
		tools.Register(&tools.Tool{
			Name:        name,
			Description: desc,
			Category:    "specialist",
			Params: []tools.Param{
				tools.RequiredStringParam("query", "The question or topic to ask this specialist about"),
			},
			Execute: func(ctx context.Context, env infra.ToolEnv, args *tools.Args) tools.Result {
				query, ok := args.RequiredString("query")
				if !ok {
					return tools.MissingParam("query")
				}
				journalCtx := ""
				if entries, err := journal.GetEntries(ctx, 5); err == nil && len(entries) > 0 {
					var lines []string
					for _, e := range entries {
						lines = append(lines, fmt.Sprintf("[%s] %s", e.Timestamp, e.Content))
					}
					journalCtx = strings.Join(lines, "\n")
				}
				out, err := agent.RunSpecialist(ctx, dom, &agent.SpecialistInput{
					UserMessage: query,
					Context:     "Answer based on your domain expertise and the journal context.",
					Journal:     journalCtx,
				}, "")
				if err != nil {
					return tools.Fail("Error: %v", err)
				}
				return tools.OK("%s\n\nSummary: %s", dom, out.Summary)
			},
		})
	}
}
