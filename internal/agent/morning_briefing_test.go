package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/jackstrohm/jot/memory"
)

// ── formatGravelEntries ──────────────────────────────────────────────────────

func TestBriefingFormatGravelEntriesEmpty(t *testing.T) {
	out := formatEntriesForPrompt(nil)
	if out != "(no recent entries)" {
		t.Fatalf("expected '(no recent entries)', got %q", out)
	}
}

func TestBriefingFormatGravelEntriesIncludesContent(t *testing.T) {
	entries := []memory.Entry{
		{Content: "went for a run", Timestamp: "2026-03-29T07:00:00Z"},
		{Content: "felt anxious about project X", Timestamp: "2026-03-29T09:00:00Z"},
	}
	out := formatEntriesForPrompt(entries)
	if !strings.Contains(out, "went for a run") {
		t.Errorf("expected output to contain first entry content")
	}
	if !strings.Contains(out, "felt anxious about project X") {
		t.Errorf("expected output to contain second entry content")
	}
	if !strings.Contains(out, "2026-03-29") {
		t.Errorf("expected output to contain date prefix")
	}
}

// ── formatGoldNodes ──────────────────────────────────────────────────────────

func TestBriefingFormatGoldNodesEmpty(t *testing.T) {
	out := briefingFormatGoldNodes(nil)
	if out != "(no active goals or projects)" {
		t.Fatalf("expected '(no active goals or projects)', got %q", out)
	}
}

func TestBriefingFormatGoldNodesIncludesNodeInfo(t *testing.T) {
	nodes := []briefingGoldNode{
		{Content: "Launch v2 by Q2", NodeType: "goal", Status: "active", SignificanceWeight: 0.9},
		{Content: "Refactor auth service", NodeType: "project", Status: "planning", SignificanceWeight: 0.75},
	}
	out := briefingFormatGoldNodes(nodes)
	if !strings.Contains(out, "Launch v2 by Q2") {
		t.Errorf("expected output to contain first node content")
	}
	if !strings.Contains(out, "Refactor auth service") {
		t.Errorf("expected output to contain second node content")
	}
	if !strings.Contains(out, "goal") {
		t.Errorf("expected output to contain node type")
	}
	if !strings.Contains(out, "active") {
		t.Errorf("expected output to contain status")
	}
}

// ── formatBriefingMessage ────────────────────────────────────────────────────

func TestBriefingFormatMessageContainsAllSections(t *testing.T) {
	msg := briefingFormatMessage("2026-03-29", "• 3 days of poor sleep", "• Project X is stalled", "What needs to happen today?")
	if !strings.Contains(msg, "Morning Briefing") {
		t.Error("expected message to contain 'Morning Briefing'")
	}
	if !strings.Contains(msg, "Observed Patterns") {
		t.Error("expected message to contain 'Observed Patterns'")
	}
	if !strings.Contains(msg, "Project Alignment") {
		t.Error("expected message to contain 'Project Alignment'")
	}
	if !strings.Contains(msg, "What needs to happen today?") {
		t.Error("expected message to contain coaching question")
	}
	if !strings.Contains(msg, "2026-03-29") {
		t.Error("expected message to contain the date")
	}
}

// ── formatFallbackMessage ────────────────────────────────────────────────────

func TestBriefingFormatFallbackNoNodes(t *testing.T) {
	msg := briefingFormatFallbackMessage("2026-03-29", nil)
	if !strings.Contains(msg, "Morning Briefing") {
		t.Error("expected fallback to contain 'Morning Briefing'")
	}
	if !strings.Contains(msg, "Nothing new") {
		t.Error("expected fallback to mention nothing new")
	}
}

func TestBriefingFormatFallbackListsTopNodes(t *testing.T) {
	nodes := []briefingGoldNode{
		{Content: "Finish the API", NodeType: "project", Status: "active", SignificanceWeight: 0.9},
		{Content: "Write tests", NodeType: "goal", Status: "active", SignificanceWeight: 0.8},
		{Content: "Deploy to prod", NodeType: "project", Status: "planning", SignificanceWeight: 0.7},
		{Content: "Should be excluded", NodeType: "goal", Status: "active", SignificanceWeight: 0.6},
	}
	msg := briefingFormatFallbackMessage("2026-03-29", nodes)
	if !strings.Contains(msg, "Finish the API") {
		t.Error("expected top project to appear")
	}
	if !strings.Contains(msg, "Write tests") {
		t.Error("expected second project to appear")
	}
	if !strings.Contains(msg, "Deploy to prod") {
		t.Error("expected third project to appear")
	}
	if strings.Contains(msg, "Should be excluded") {
		t.Error("fourth node should not appear in fallback (capped at 3)")
	}
}

// ── RunMorningBriefing nil-app guard ─────────────────────────────────────────

func TestRunMorningBriefingNilApp(t *testing.T) {
	_, err := RunMorningBriefing(context.Background(), nil, false)
	if err == nil {
		t.Fatal("expected error for nil app, got nil")
	}
}
