package prompts

import (
	"bytes"
	"fmt"
	"text/template"
)

// ContextAnalyzeData holds the entry content for context analysis.
type ContextAnalyzeData struct {
	EntryContent string
}

var contextAnalyzeTmpl = template.Must(template.New("context_analyze").Parse(contextAnalyzeTxt))

// BuildContextAnalyze executes the context-analyze template.
func BuildContextAnalyze(data ContextAnalyzeData) (string, error) {
	var buf bytes.Buffer
	if err := contextAnalyzeTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute context analyze: %w", err)
	}
	return buf.String(), nil
}
