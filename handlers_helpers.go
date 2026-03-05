package jot

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"google.golang.org/api/docs/v1"
	"google.golang.org/api/option"
)

// SubmitAsync submits a task to the app's async fire-and-forget pool (no-op if no App in context).
func SubmitAsync(ctx context.Context, task func()) {
	if app := GetApp(ctx); app != nil {
		app.SubmitAsync(task)
	}
}

// SubmitToolExec submits a task to the app's tool execution pool (no-op if no App in context).
func SubmitToolExec(ctx context.Context, task func()) {
	if app := GetApp(ctx); app != nil {
		app.SubmitToolExec(task)
	}
}

// SubmitSummaryGen submits a task to the app's summary generation pool (no-op if no App in context).
func SubmitSummaryGen(ctx context.Context, task func()) {
	if app := GetApp(ctx); app != nil {
		app.SubmitSummaryGen(task)
	}
}

// SubmitGDocLog submits a message to the Google Doc log pool.
func SubmitGDocLog(ctx context.Context, msg string) {
	app := GetApp(ctx)
	if app == nil {
		app, _ = GetDefaultApp()
	}
	if app != nil {
		app.SubmitGDocLog(ctx, msg)
	}
}

// docIndexLen returns the length of s in UTF-16 code units (Google Docs API StartIndex/EndIndex).
func docIndexLen(s string) int64 {
	return int64(len(utf16.Encode([]rune(s))))
}

// scanForLogsStanza finds [LOGS] and [/LOGS] in body content and nested table cells.
func scanForLogsStanza(content []*docs.StructuralElement, logsStartIndex, logsEndTagStart *int64) {
	if content == nil {
		return
	}
	for _, element := range content {
		if element == nil {
			continue
		}
		if element.Paragraph != nil {
			for _, elem := range element.Paragraph.Elements {
				if elem.TextRun == nil {
					continue
				}
				text := strings.TrimSpace(elem.TextRun.Content)
				if text == "[LOGS]" {
					*logsStartIndex = elem.StartIndex
				}
				if text == "[/LOGS]" {
					*logsEndTagStart = elem.StartIndex
				}
			}
			continue
		}
		if element.Table != nil {
			for _, row := range element.Table.TableRows {
				if row == nil {
					continue
				}
				for _, cell := range row.TableCells {
					if cell == nil || len(cell.Content) == 0 {
						continue
					}
					scanForLogsStanza(cell.Content, logsStartIndex, logsEndTagStart)
				}
			}
		}
	}
}

// logToGDocSync writes a log line to the Google Doc (called by the gdoc appender pool).
func logToGDocSync(ctx context.Context, message string) {
	ctx = WithGDocLogging(ctx)
	ctx, span := StartSpan(ctx, "gdoc.log")
	defer span.End()

	var docsService *docs.Service
	var err error

	if defaultConfig.ServiceAccountFile != "" {
		docsService, err = docs.NewService(ctx, option.WithCredentialsFile(defaultConfig.ServiceAccountFile))
	} else {
		docsService, err = docs.NewService(ctx)
	}
	if err != nil {
		LoggerFrom(ctx).Error("failed to create Docs service for logging", "error", err)
		span.RecordError(err)
		return
	}

	doc, err := docsService.Documents.Get(defaultConfig.DocumentID).Do()
	if err != nil {
		LoggerFrom(ctx).Error("failed to fetch document for logging", "error", err)
		span.RecordError(err)
		return
	}

	var logsStartIndex, logsEndTagStart int64 = -1, -1
	scanForLogsStanza(doc.Body.Content, &logsStartIndex, &logsEndTagStart)

	if logsStartIndex == -1 || logsEndTagStart == -1 {
		LoggerFrom(ctx).Debug("gdoc logging skipped (no [LOGS] [/LOGS] section in document)")
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logLine := fmt.Sprintf("%s: %s", timestamp, message)
	insertText := logLine + "\n"

	requests := []*docs.Request{
		{
			InsertText: &docs.InsertTextRequest{
				Location: &docs.Location{Index: logsEndTagStart},
				Text:     insertText,
			},
		},
		{
			UpdateTextStyle: &docs.UpdateTextStyleRequest{
				Range: &docs.Range{
					StartIndex: logsEndTagStart,
					EndIndex:   logsEndTagStart + docIndexLen(insertText),
				},
				TextStyle: &docs.TextStyle{Bold: true},
				Fields:    "bold",
			},
		},
	}

	_, err = docsService.Documents.BatchUpdate(defaultConfig.DocumentID, &docs.BatchUpdateDocumentRequest{
		Requests: requests,
	}).Do()
	if err != nil {
		LoggerFrom(ctx).Error("failed to write log to gdoc", "error", err)
		span.RecordError(err)
		return
	}

	LoggerFrom(ctx).Debug("logged to gdoc", "message", message)
}
