package gdoc

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/jackstrohm/jot/internal/config"
	"github.com/jackstrohm/jot/internal/infra"
	"google.golang.org/api/docs/v1"
	"google.golang.org/api/option"
)

// NewGDocLogFunc returns a function that writes log lines to the configured Google Doc [LOGS] section.
// Pass the returned function to infra.InitDefaultApp as the gdocLog callback.
func NewGDocLogFunc(cfg *config.Config) func(ctx context.Context, message string) {
	if cfg == nil {
		return func(context.Context, string) {}
	}
	return func(ctx context.Context, message string) {
		logToGDocSync(ctx, cfg, message)
	}
}

func docIndexLen(s string) int64 {
	return int64(len(utf16.Encode([]rune(s))))
}

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

func logToGDocSync(ctx context.Context, cfg *config.Config, message string) {
	ctx = infra.WithGDocLogging(ctx)
	ctx, span := infra.StartSpan(ctx, "gdoc.log")
	defer span.End()

	var docsService *docs.Service
	var err error

	if cfg.ServiceAccountFile != "" {
		docsService, err = docs.NewService(ctx, option.WithCredentialsFile(cfg.ServiceAccountFile))
	} else {
		docsService, err = docs.NewService(ctx)
	}
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to create Docs service for logging", "error", err)
		span.RecordError(err)
		return
	}

	doc, err := docsService.Documents.Get(cfg.DocumentID).Do()
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to fetch document for logging", "error", err)
		span.RecordError(err)
		return
	}

	var logsStartIndex, logsEndTagStart int64 = -1, -1
	scanForLogsStanza(doc.Body.Content, &logsStartIndex, &logsEndTagStart)

	if logsStartIndex == -1 || logsEndTagStart == -1 {
		infra.LoggerFrom(ctx).Debug("gdoc logging skipped (no [LOGS] [/LOGS] section in document)")
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

	_, err = docsService.Documents.BatchUpdate(cfg.DocumentID, &docs.BatchUpdateDocumentRequest{
		Requests: requests,
	}).Do()
	if err != nil {
		infra.LoggerFrom(ctx).Error("failed to write log to gdoc", "error", err)
		span.RecordError(err)
		return
	}

	infra.LoggerFrom(ctx).Debug("logged to gdoc", "message", message)
}
