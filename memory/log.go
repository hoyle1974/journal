package memory

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

// RAG confidence status labels.
const (
	ragStatusHighConfidence   = "HIGH_CONFIDENCE_MATCH"
	ragStatusMediumConfidence = "MEDIUM_CONFIDENCE_MATCH"
	ragStatusLowConfidence    = "LOW_CONFIDENCE_MATCH"
	ragStatusNoResults        = "NO_RESULTS"
)

func (s *Store) logVectorSearchFailed(index string, err error, retries int) {
	reason := "unknown"
	errStr := ""
	if err != nil {
		errStr = err.Error()
		reason = errStr
		if strings.Contains(reason, "deadline exceeded") {
			reason = "deadline_exceeded"
		} else if strings.Contains(reason, "not found") || strings.Contains(reason, "NotFound") {
			reason = "index_not_found"
		} else if strings.Contains(reason, "Permission denied") || strings.Contains(reason, "permission_denied") {
			reason = "permission_denied"
		}
	}
	attrs := []any{slog.String("index", index), slog.String("reason", reason), slog.Int("retries", retries)}
	if errStr != "" {
		attrs = append(attrs, slog.String("error", errStr))
	}
	s.log.Error(fmt.Sprintf("vector search failed | index=%s | reason=%s | retries=%d", index, reason, retries), attrs...)
}

func (s *Store) logFoundNode(id string, score float64, textPreview string) {
	s.log.Debug(fmt.Sprintf("found node | id=%s | score=%.2f | text=%q", id, score, textPreview),
		slog.String("id", id), slog.Float64("score", score), slog.String("text", textPreview))
}

func (s *Store) logFoundEntry(id string, score float64, textPreview string) {
	s.log.Debug(fmt.Sprintf("found entry | id=%s | score=%.2f | text=%q", id, score, textPreview),
		slog.String("id", id), slog.Float64("score", score), slog.String("text", textPreview))
}

func (s *Store) logRAGQuality(topK int, scores []float64) {
	if len(scores) == 0 {
		s.log.Debug(fmt.Sprintf("RAG_QUALITY | top_k=%d | median_score=N/A | p90_score=N/A | status=%s", topK, ragStatusNoResults),
			slog.String("event", "RAG_QUALITY"), slog.Int("top_k", topK), slog.String("status", ragStatusNoResults))
		return
	}
	sorted := make([]float64, len(scores))
	copy(sorted, scores)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 && len(sorted) >= 2 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	p90Idx := int(0.9 * float64(len(sorted)))
	if p90Idx >= len(sorted) {
		p90Idx = len(sorted) - 1
	}
	p90 := sorted[p90Idx]
	maxScore := sorted[len(sorted)-1]
	ragStatus := ragStatusLowConfidence
	if p90 >= 0.6 {
		ragStatus = ragStatusHighConfidence
	} else if median >= 0.5 || maxScore >= 0.6 {
		ragStatus = ragStatusMediumConfidence
	}
	s.log.Debug(fmt.Sprintf("RAG_QUALITY | top_k=%d | median_score=%.2f | p90_score=%.2f | status=%s", topK, median, p90, ragStatus),
		slog.String("event", "RAG_QUALITY"), slog.Int("top_k", topK),
		slog.Float64("median_score", median), slog.Float64("p90_score", p90), slog.String("status", ragStatus))
}
