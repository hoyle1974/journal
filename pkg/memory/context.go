// Package memory provides context nodes (briefings, user_profile, etc.) in the knowledge graph.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	memoryprompts "github.com/jackstrohm/jot/pkg/memory/prompts"
	"google.golang.org/api/iterator"
)

func truncateForLogContext(s string, maxLen int) string {
	if len([]rune(s)) <= maxLen {
		return s
	}
	return truncateString(s, maxLen) + "..."
}

// Context system constants
const (
	ContextNodeType    = "context"
	DefaultDecayRate   = 0.05 // 5% per day
	DefaultRelevance   = 0.85
	MinActiveRelevance = 0.20 // Below this, context is not included in prompt
)

// ContextMetadata represents the metadata stored with context nodes.
type ContextMetadata struct {
	ContextType                 string   `json:"context_type"`
	ContextName                 string   `json:"context_name"`
	Relevance                   float64  `json:"relevance"`
	LastTouched                 string   `json:"last_touched"`
	Entities                    []string `json:"entities"`
	SourceEntries               []string `json:"source_entries"`
	DecayRate                   float64  `json:"decay_rate"`
	LastSynthesizedAt           string   `json:"last_synthesized_at"`
	SourceEntryCountAtSynthesis int      `json:"source_entry_count_at_synthesis"`
}

// CreateContext creates a new context node in the knowledge graph.
func (s *Store) CreateContext(ctx context.Context, name, content, contextType string, entities, sourceEntries []string) (string, error) {
	s.log.Info("creating context", "name", name, "type", contextType)

	decayRate := DefaultDecayRate
	if contextType == "permanent" {
		decayRate = 0
	}

	metadata := ContextMetadata{
		ContextType:   contextType,
		ContextName:   name,
		Relevance:     DefaultRelevance,
		LastTouched:   time.Now().Format(time.RFC3339),
		Entities:      entities,
		SourceEntries: sourceEntries,
		DecayRate:     decayRate,
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("failed to marshal context metadata: %w", err)
	}

	nodeUUID, err := s.UpsertKnowledge(ctx, content, ContextNodeType, string(metadataJSON), nil)
	if err != nil {
		return "", err
	}

	s.log.Info("context created", "uuid", nodeUUID, "name", name)
	return nodeUUID, nil
}

// TouchContext updates a context's last_touched timestamp and optionally boosts relevance.
func (s *Store) TouchContext(ctx context.Context, contextUUID string, newSourceEntry *string, relevanceBoost float64) error {
	s.log.Info("touching context", "uuid", contextUUID, "boost", relevanceBoost)

	doc, err := s.db.Collection(KnowledgeCollection).Doc(contextUUID).Get(ctx)
	if err != nil {
		return fmt.Errorf("context not found: %w", err)
	}

	data := doc.Data()
	metadataStr := getStringField(data, "metadata")

	var metadata ContextMetadata
	if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
		return fmt.Errorf("failed to parse context metadata: %w", err)
	}

	metadata.LastTouched = time.Now().Format(time.RFC3339)
	metadata.Relevance = min(1.0, metadata.Relevance+relevanceBoost)

	if newSourceEntry != nil && *newSourceEntry != "" {
		metadata.SourceEntries = append(metadata.SourceEntries, *newSourceEntry)
	}

	updatedMetadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal updated metadata: %w", err)
	}

	_, err = s.db.Collection(KnowledgeCollection).Doc(contextUUID).Update(ctx, []firestore.Update{
		{Path: "metadata", Value: string(updatedMetadataJSON)},
		{Path: "timestamp", Value: time.Now().Format(time.RFC3339)},
	})
	if err != nil {
		return err
	}

	s.log.Info("context touched", "uuid", contextUUID, "new_relevance", metadata.Relevance)
	return nil
}

func normalizeContextName(name string) string {
	s := strings.TrimSpace(strings.ToLower(name))
	return strings.ReplaceAll(s, " ", "_")
}

// EnsureContextExists finds a context by name or creates it with placeholder content.
func (s *Store) EnsureContextExists(ctx context.Context, name string) (string, error) {
	norm := normalizeContextName(name)
	if norm == "" {
		return "", fmt.Errorf("context name is empty after normalization")
	}

	existing, _, err := s.FindContextByName(ctx, norm)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return existing.UUID, nil
	}

	placeholderContent := "Ongoing: " + norm
	uuid, err := s.CreateContext(ctx, norm, placeholderContent, "auto", nil, nil)
	if err != nil {
		return "", err
	}
	s.log.Info("ensure_context_created", "name", norm, "uuid", uuid)
	return uuid, nil
}

// TouchContextBatch appends multiple entry UUIDs to a context's SourceEntries and updates last_touched.
func (s *Store) TouchContextBatch(ctx context.Context, contextUUID string, entryUUIDs []string, relevanceBoost float64) error {
	if len(entryUUIDs) == 0 {
		return nil
	}

	doc, err := s.db.Collection(KnowledgeCollection).Doc(contextUUID).Get(ctx)
	if err != nil {
		return fmt.Errorf("context not found: %w", err)
	}

	data := doc.Data()
	metadataStr := getStringField(data, "metadata")
	var metadata ContextMetadata
	if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
		return fmt.Errorf("failed to parse context metadata: %w", err)
	}

	metadata.LastTouched = time.Now().Format(time.RFC3339)
	metadata.Relevance = min(1.0, metadata.Relevance+relevanceBoost)
	metadata.SourceEntries = append(metadata.SourceEntries, entryUUIDs...)

	updatedMetadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal updated metadata: %w", err)
	}

	_, err = s.db.Collection(KnowledgeCollection).Doc(contextUUID).Update(ctx, []firestore.Update{
		{Path: "metadata", Value: string(updatedMetadataJSON)},
		{Path: "timestamp", Value: time.Now().Format(time.RFC3339)},
	})
	if err != nil {
		return err
	}
	s.log.Info("context touched batch", "uuid", contextUUID, "entries_added", len(entryUUIDs))
	return nil
}

type byRelevanceDesc struct {
	nodes []KnowledgeNode
	metas []ContextMetadata
}

func (b byRelevanceDesc) Len() int           { return len(b.nodes) }
func (b byRelevanceDesc) Less(i, j int) bool { return b.metas[i].Relevance > b.metas[j].Relevance }
func (b byRelevanceDesc) Swap(i, j int) {
	b.nodes[i], b.nodes[j] = b.nodes[j], b.nodes[i]
	b.metas[i], b.metas[j] = b.metas[j], b.metas[i]
}

// GetActiveContexts returns contexts with relevance above MinActiveRelevance, sorted by relevance.
func (s *Store) GetActiveContexts(ctx context.Context, limit int) ([]KnowledgeNode, []ContextMetadata, error) {
	s.log.Debug("get active contexts", "limit", limit, "reason", "for system prompt and context-aware answers")

	s.permanentOnce.Do(func() {
		bgCtx := context.Background()
		go func() {
			if err := s.InitializePermanentContexts(bgCtx); err != nil {
				s.log.Warn("failed to initialize permanent contexts", "error", err)
			}
		}()
	})

	iter := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", ContextNodeType).
		Documents(ctx)
	defer iter.Stop()

	var nodes []KnowledgeNode
	var metas []ContextMetadata

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, nil, err
		}

		data := doc.Data()
		node := KnowledgeNode{
			UUID:      doc.Ref.ID,
			Content:   getStringField(data, "content"),
			NodeType:  getStringField(data, "node_type"),
			Metadata:  getStringField(data, "metadata"),
			Timestamp: getStringField(data, "timestamp"),
		}

		var meta ContextMetadata
		if err := json.Unmarshal([]byte(node.Metadata), &meta); err != nil {
			s.log.Warn("failed to parse context metadata", "uuid", node.UUID, "error", err)
			continue
		}

		if meta.Relevance >= MinActiveRelevance {
			nodes = append(nodes, node)
			metas = append(metas, meta)
		}
	}

	sort.Sort(byRelevanceDesc{nodes: nodes, metas: metas})

	if limit > 0 && len(nodes) > limit {
		nodes = nodes[:limit]
		metas = metas[:limit]
	}

	s.log.Info("active contexts retrieved", "count", len(nodes))
	return nodes, metas, nil
}

// DecayContexts applies time-based decay to all auto contexts.
func (s *Store) DecayContexts(ctx context.Context) (int, error) {
	s.log.Info("starting context decay")

	iter := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", ContextNodeType).
		Documents(ctx)
	defer iter.Stop()

	decayedCount := 0
	now := time.Now()

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return decayedCount, err
		}

		data := doc.Data()
		metadataStr := getStringField(data, "metadata")

		var meta ContextMetadata
		if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
			s.log.Warn("failed to parse context metadata for decay", "uuid", doc.Ref.ID, "error", err)
			continue
		}

		if meta.DecayRate == 0 {
			continue
		}

		lastTouched, err := time.Parse(time.RFC3339, meta.LastTouched)
		if err != nil {
			s.log.Warn("failed to parse last_touched", "uuid", doc.Ref.ID, "error", err)
			continue
		}

		daysSince := now.Sub(lastTouched).Hours() / 24
		if daysSince < 1 {
			continue
		}

		decayFactor := 1.0
		for i := 0; i < int(daysSince); i++ {
			decayFactor *= (1 - meta.DecayRate)
		}
		newRelevance := meta.Relevance * decayFactor

		meta.Relevance = newRelevance
		meta.LastTouched = now.Format(time.RFC3339)

		updatedMetadataJSON, err := json.Marshal(meta)
		if err != nil {
			continue
		}

		_, err = s.db.Collection(KnowledgeCollection).Doc(doc.Ref.ID).Update(ctx, []firestore.Update{
			{Path: "metadata", Value: string(updatedMetadataJSON)},
		})
		if err != nil {
			s.log.Warn("failed to update decayed context", "uuid", doc.Ref.ID, "error", err)
			continue
		}

		decayedCount++
		s.log.Debug("context decayed",
			"uuid", doc.Ref.ID,
			"name", meta.ContextName,
			"old_relevance", meta.Relevance/decayFactor,
			"new_relevance", newRelevance,
		)
	}

	s.log.Info("context decay completed", "decayed_count", decayedCount)
	return decayedCount, nil
}

// DeleteContext deletes a context by UUID.
func (s *Store) DeleteContext(ctx context.Context, contextUUID string) error {
	_, err := s.db.Collection(KnowledgeCollection).Doc(contextUUID).Delete(ctx)
	if err != nil {
		return err
	}
	s.log.Info("context deleted", "uuid", contextUUID)
	return nil
}

// GetContextMetadata returns metadata for a context by UUID.
func (s *Store) GetContextMetadata(ctx context.Context, contextUUID string) (*ContextMetadata, error) {
	doc, err := s.db.Collection(KnowledgeCollection).Doc(contextUUID).Get(ctx)
	if err != nil {
		return nil, err
	}
	data := doc.Data()
	metadataStr := getStringField(data, "metadata")
	var meta ContextMetadata
	if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
		return nil, fmt.Errorf("parse context metadata: %w", err)
	}
	return &meta, nil
}

// FindContextByName finds a context by its name.
func (s *Store) FindContextByName(ctx context.Context, name string) (*KnowledgeNode, *ContextMetadata, error) {
	iter := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", ContextNodeType).
		Documents(ctx)
	defer iter.Stop()

	nameLower := strings.ToLower(name)

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, nil, err
		}

		data := doc.Data()
		metadataStr := getStringField(data, "metadata")

		var meta ContextMetadata
		if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
			continue
		}

		if strings.ToLower(meta.ContextName) == nameLower {
			node := &KnowledgeNode{
				UUID:      doc.Ref.ID,
				Content:   getStringField(data, "content"),
				NodeType:  getStringField(data, "node_type"),
				Metadata:  metadataStr,
				Timestamp: getStringField(data, "timestamp"),
			}
			return node, &meta, nil
		}
	}

	return nil, nil, nil
}

// MatchEntryToContexts finds contexts that semantically match the entry content.
func (s *Store) MatchEntryToContexts(ctx context.Context, entryContent string, threshold float64) ([]string, []float64, error) {
	entryVector, err := s.embedder.GenerateEmbedding(ctx, entryContent, EmbedTaskRetrievalQuery)
	if err != nil {
		return nil, nil, err
	}

	distanceThreshold := 1 - threshold
	vectorQuery := s.db.Collection(KnowledgeCollection).
		Where("node_type", "==", ContextNodeType).
		FindNearest("embedding", firestore.Vector32(entryVector), 10, firestore.DistanceMeasureCosine,
			&firestore.FindNearestOptions{DistanceThreshold: &distanceThreshold})

	iter := vectorQuery.Documents(ctx)
	defer iter.Stop()

	var uuids []string
	var scores []float64

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			s.logVectorSearchFailed(KnowledgeCollection, err, 0)
			break
		}

		data := doc.Data()
		metadataStr := getStringField(data, "metadata")

		var meta ContextMetadata
		if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
			continue
		}

		if meta.Relevance >= MinActiveRelevance {
			uuids = append(uuids, doc.Ref.ID)
			scores = append(scores, meta.Relevance)
		}
	}

	s.log.Info("matched contexts", "entry_preview", truncateForLogContext(entryContent, 50), "matches", len(uuids))
	return uuids, scores, nil
}

// DetectOrCreateContext analyzes entry content and either links to existing context or creates new.
func (s *Store) DetectOrCreateContext(ctx context.Context, entryContent, entryUUID string) ([]string, error) {
	s.log.Info("detecting context for entry", "entry_uuid", entryUUID, "content", truncateForLogContext(entryContent, 50))

	matchedUUIDs, scores, err := s.MatchEntryToContexts(ctx, entryContent, 0.6)
	if err != nil {
		s.log.Warn("context matching failed", "error", err)
	}

	if len(matchedUUIDs) > 0 {
		for i, uuid := range matchedUUIDs {
			relevanceBoost := 0.05
			if scores[i] > 0.8 {
				relevanceBoost = 0.10
			}
			if err := s.TouchContext(ctx, uuid, &entryUUID, relevanceBoost); err != nil {
				s.log.Warn("failed to touch matched context", "uuid", uuid, "error", err)
			}
		}
		s.log.Info("linked entry to existing contexts", "entry_uuid", entryUUID, "context_count", len(matchedUUIDs))
		return matchedUUIDs, nil
	}

	shouldCreate, contextName, entities := s.analyzeForNewContext(ctx, entryContent)
	if !shouldCreate {
		s.log.Debug("no new context needed", "entry_uuid", entryUUID)
		return nil, nil
	}

	existingNode, _, err := s.FindContextByName(ctx, contextName)
	if err == nil && existingNode != nil {
		s.log.Info("context with name already exists, touching instead", "name", contextName, "uuid", existingNode.UUID)
		if err := s.TouchContext(ctx, existingNode.UUID, &entryUUID, 0.05); err != nil {
			s.log.Warn("failed to touch existing context", "uuid", existingNode.UUID, "error", err)
		}
		return []string{existingNode.UUID}, nil
	}

	nodeUUID, err := s.CreateContext(ctx, contextName, entryContent, "auto", entities, []string{entryUUID})
	if err != nil {
		return nil, fmt.Errorf("failed to create context: %w", err)
	}

	s.log.Info("created new context from entry", "entry_uuid", entryUUID, "context_name", contextName)
	return []string{nodeUUID}, nil
}

func (s *Store) analyzeForNewContext(ctx context.Context, entryContent string) (bool, string, []string) {
	if len(strings.TrimSpace(entryContent)) < 20 {
		return false, "", nil
	}

	prompt, err := memoryprompts.BuildContextAnalyze(memoryprompts.ContextAnalyzeData{
		EntryContent: wrapAsUserData(sanitizePrompt(entryContent)),
	})
	if err != nil {
		s.log.Warn("context analysis prompt build failed", "error", err)
		return false, "", nil
	}

	text, err := s.llm.Dispatch(ctx, LLMRequest{
		UserPrompt: prompt,
		MaxTokens:  512,
	})
	if err != nil {
		s.log.Warn("context analysis failed", "error", err)
		return false, "", nil
	}

	text = strings.TrimSpace(text)
	simple, sections := parseKeyValueMap(text)
	isProject := strings.EqualFold(strings.TrimSpace(simple["is_project_or_plan"]), "true")
	contextName := strings.TrimSpace(simple["context_name"])
	entities := sections["entities"]
	return isProject, contextName, entities
}

const maxSourceEntriesForSynthesis = 10
const maxRawLogsChars = 6000
const synthesisNoNewInfoOverlap = 0.85
const synthesisNoNewInfoMinRawLen = 50

func synthesisHasNoNewInfo(rawLogs, oldContent string) bool {
	rawLogs = strings.TrimSpace(rawLogs)
	oldContent = strings.TrimSpace(oldContent)
	if rawLogs == "" {
		return true
	}
	if len(rawLogs) < synthesisNoNewInfoMinRawLen && oldContent != "" {
		if strings.Contains(oldContent, rawLogs) {
			return true
		}
		rawWords := strings.Fields(strings.ToLower(rawLogs))
		if len(rawWords) <= 5 {
			oldLower := strings.ToLower(oldContent)
			allIn := true
			for _, w := range rawWords {
				if len(w) < 3 {
					continue
				}
				if !strings.Contains(oldLower, w) {
					allIn = false
					break
				}
			}
			if allIn {
				return true
			}
		}
	}
	newWords := strings.Fields(strings.ToLower(rawLogs))
	oldSet := make(map[string]struct{})
	for _, w := range strings.Fields(strings.ToLower(oldContent)) {
		if len(w) >= 2 {
			oldSet[w] = struct{}{}
		}
	}
	var significantNew, inOld int
	for _, w := range newWords {
		if len(w) < 2 {
			continue
		}
		significantNew++
		if _, ok := oldSet[w]; ok {
			inOld++
		}
	}
	if significantNew == 0 {
		return true
	}
	overlapRatio := float64(inOld) / float64(significantNew)
	return overlapRatio >= synthesisNoNewInfoOverlap
}

// SynthesizeContext loads the context node and its source entries, then uses the LLM to produce a briefing.
func (s *Store) SynthesizeContext(ctx context.Context, contextUUID string) error {
	doc, err := s.db.Collection(KnowledgeCollection).Doc(contextUUID).Get(ctx)
	if err != nil {
		return fmt.Errorf("context not found: %w", err)
	}

	data := doc.Data()
	oldContent := getStringField(data, "content")
	metadataStr := getStringField(data, "metadata")

	var meta ContextMetadata
	if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
		return fmt.Errorf("failed to parse context metadata: %w", err)
	}

	entryUUIDs := meta.SourceEntries
	if len(entryUUIDs) > maxSourceEntriesForSynthesis {
		entryUUIDs = entryUUIDs[len(entryUUIDs)-maxSourceEntriesForSynthesis:]
	}

	var rawParts []string
	totalLen := 0
	for _, uuid := range entryUUIDs {
		if totalLen >= maxRawLogsChars {
			break
		}
		entry, err := s.GetEntry(ctx, uuid)
		if err != nil || entry == nil {
			continue
		}
		part := fmt.Sprintf("[%s] %s", entry.Timestamp, entry.Content)
		if totalLen+len(part) > maxRawLogsChars {
			part = truncateToMaxBytes(part, maxRawLogsChars-totalLen)
		}
		rawParts = append(rawParts, part)
		totalLen += len(part)
	}
	rawLogs := strings.Join(rawParts, "\n\n")

	if synthesisHasNoNewInfo(rawLogs, oldContent) {
		s.log.Info("context synthesis skipped (no new info)", "uuid", contextUUID, "name", meta.ContextName)
		now := time.Now().Format(time.RFC3339)
		meta.LastTouched = now
		meta.LastSynthesizedAt = now
		meta.SourceEntryCountAtSynthesis = len(meta.SourceEntries)
		updatedMetadataJSON, err := json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("marshal metadata: %w", err)
		}
		_, err = s.db.Collection(KnowledgeCollection).Doc(contextUUID).Update(ctx, []firestore.Update{
			{Path: "metadata", Value: string(updatedMetadataJSON)},
		})
		return err
	}

	userPrompt := fmt.Sprintf("Current Briefing:\n%s\n\nNew Information:\n%s\n\nTask: Write a new briefing (max 250 words) that preserves active Open Loops, critical dates, and key stakeholder preferences. Use bullet points for status.",
		wrapAsUserData(oldContent), wrapAsUserData(rawLogs))

	newContent, err := s.llm.Dispatch(ctx, LLMRequest{
		SystemPrompt: memoryprompts.ExecutiveSummary(),
		UserPrompt:   userPrompt,
		MaxTokens:    512,
	})
	if err != nil {
		return err
	}
	if newContent == "" {
		return nil
	}

	now := time.Now().Format(time.RFC3339)
	meta.LastSynthesizedAt = now
	meta.SourceEntryCountAtSynthesis = len(meta.SourceEntries)
	updatedMetadataJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	_, err = s.db.Collection(KnowledgeCollection).Doc(contextUUID).Update(ctx, []firestore.Update{
		{Path: "content", Value: newContent},
		{Path: "timestamp", Value: now},
		{Path: "metadata", Value: string(updatedMetadataJSON)},
	})
	if err != nil {
		return err
	}

	s.log.Info("context synthesized", "uuid", contextUUID, "name", meta.ContextName)
	return nil
}

// InitializePermanentContexts ensures permanent contexts exist.
func (s *Store) InitializePermanentContexts(ctx context.Context) error {
	permanentContexts := []struct {
		Name    string
		Content string
	}{
		{"user_profile", "User preferences, facts, and personal information"},
		{"upcoming_deadlines", "Due dates, appointments, and time-sensitive items"},
		{"active_plans", "Goals and plans currently in progress"},
		{"system_evolution", "Recommended system and tool improvements from the Cognitive Engineer (nightly audit)."},
	}

	for _, pc := range permanentContexts {
		existing, _, err := s.FindContextByName(ctx, pc.Name)
		if err != nil {
			s.log.Warn("error checking permanent context", "name", pc.Name, "error", err)
			continue
		}
		if existing != nil {
			continue
		}
		_, err = s.CreateContext(ctx, pc.Name, pc.Content, "permanent", nil, nil)
		if err != nil {
			s.log.Warn("failed to create permanent context", "name", pc.Name, "error", err)
			continue
		}
		s.log.Info("created permanent context", "name", pc.Name)
	}

	return nil
}
