package utils

import (
	"fmt"
	"regexp"
	"strings"
)

// AnalyzeText returns statistics about a text.
func AnalyzeText(text string) string {
	charCount := len(text)
	charCountNoSpaces := len(strings.ReplaceAll(text, " ", ""))

	words := strings.Fields(text)
	wordCount := len(words)

	sentenceRegex := regexp.MustCompile(`[.!?]+`)
	sentences := sentenceRegex.FindAllString(text, -1)
	sentenceCount := len(sentences)
	if sentenceCount == 0 && wordCount > 0 {
		sentenceCount = 1
	}

	readingMinutes := float64(wordCount) / 200.0

	paragraphs := strings.Split(text, "\n\n")
	paraCount := 0
	for _, p := range paragraphs {
		if strings.TrimSpace(p) != "" {
			paraCount++
		}
	}

	return fmt.Sprintf("Text Statistics:\n- Characters: %d (without spaces: %d)\n- Words: %d\n- Sentences: %d\n- Paragraphs: %d\n- Reading time: %.1f minutes",
		charCount, charCountNoSpaces, wordCount, sentenceCount, paraCount, readingMinutes)
}
