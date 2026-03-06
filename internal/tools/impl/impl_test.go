package impl_test

import (
	"testing"

	_ "github.com/jackstrohm/jot/internal/tools/impl" // register tools
	"github.com/jackstrohm/jot/tools"
)

func TestToolDefinitionsComplete(t *testing.T) {
	expectedTools := []string{
		"get_recent_entries",
		"get_oldest_entries",
		"get_entries_by_date_range",
		"query_activity_history",
		"query_entities",
		"search_entries",
		"summarize_daily_activities",
		"count_entries",
		"list_sources",
		"get_entries_by_source",
		"get_recent_queries",
		"search_queries",
		"get_queries_by_date",
		"consult_anthropologist",
		"consult_architect",
		"consult_executive",
		"consult_philosopher",
		"upsert_knowledge",
		"semantic_search",
		"list_knowledge",
		"get_entity_network",
		"generate_plan",
		"check_proactive_signals",
		"list_contexts",
		"create_context",
		"touch_context",
		"delete_context",
		"get_system_health_audit",
		"get_project_timeline",
		"update_project_status",
		"calculate",
		"date_calc",
		"fetch_url",
		"github_read",
		"text_stats",
		"convert_units",
		"random",
		"timezone_convert",
		"countdown",
		"define_word",
		"encode_decode",
		"bookmark",
		"wikipedia",
		"web_search",
	}

	toolDefs := tools.GetDefinitions()
	toolNames := make(map[string]bool)
	for _, td := range toolDefs {
		toolNames[td.Name] = true
	}

	for _, expected := range expectedTools {
		if !toolNames[expected] {
			t.Errorf("Missing tool definition: %s", expected)
		}
	}

	if len(toolDefs) != len(expectedTools) {
		t.Errorf("GetDefinitions() has %d tools, expected %d", len(toolDefs), len(expectedTools))
	}
}
