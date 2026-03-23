package prompts

import (
	"bytes"
	"fmt"
	"text/template"
)

// JournalAnalyzeData holds the data for the journal-analyze prompt template.
type JournalAnalyzeData struct {
	EntryID   string
	Date      string
	EntryText string
}

var journalAnalyzeTmpl = template.Must(template.New("journal_analyze").Parse(journalAnalyzeTxt))

// BuildJournalAnalyze executes the journal-analyze template.
func BuildJournalAnalyze(data JournalAnalyzeData) (string, error) {
	var buf bytes.Buffer
	if err := journalAnalyzeTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute journal analyze: %w", err)
	}
	return buf.String(), nil
}
