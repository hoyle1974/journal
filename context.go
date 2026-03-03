package jot

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/llmjson"
	"google.golang.org/api/iterator"
)

// Context system constants
const (
	ContextNodeType    = "context"
	DefaultDecayRate   = 0.05 // 5% per day
	DefaultRelevance   = 0.85
	MinActiveRelevance = 0.20 // Below this, context is not included in prompt
)

// ContextMetadata represents the metadata stored with context nodes.
type ContextMetadata struct {
	ContextType                 string   `json:"context_type"`                   // "permanent", "auto", "derived"
	ContextName                 string   `json:"context_name"`                   // e.g., "party_planning"
	Relevance                   float64  `json:"relevance"`                      // 0-1, decays over time
	LastTouched                 string   `json:"last_touched"`                    // RFC3339 timestamp
	Entities                    []string `json:"entities"`                        // Related entities/keywords
	SourceEntries               []string `json:"source_entries"`                  // Entry UUIDs that contributed
	DecayRate                   float64  `json:"decay_rate"`                       // Per day, 0 for permanent
	LastSynthesizedAt           string   `json:"last_synthesized_at"`             // RFC3339; when context was last synthesized
	SourceEntryCountAtSynthesis int      `json:"source_entry_count_at_synthesis"` // len(SourceEntries) at last synthesis
}

// CreateContext creates a new context node in the knowledge graph.
func CreateContext(ctx context.Context, name, content, contextType string, entities, sourceEntries []string) (string, error) {
	ctx, span := StartSpan(ctx, "context.create")
	defer span.End()

	LoggerFrom(ctx).Info("creating context", "name", name, "type", contextType)

	// Set defaults
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
		span.RecordError(err)
		return "", fmt.Errorf("failed to marshal context metadata: %w", err)
	}

	// Use UpsertKnowledge which handles embedding generation
	nodeUUID, err := UpsertKnowledge(ctx, content, ContextNodeType, string(metadataJSON))
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	LoggerFrom(ctx).Info("context created", "uuid", nodeUUID, "name", name)
	span.SetAttributes(map[string]string{
		"context_uuid": nodeUUID,
		"context_name": name,
	})

	return nodeUUID, nil
}

// TouchContext updates a context's last_touched timestamp and optionally boosts relevance.
func TouchContext(ctx context.Context, contextUUID string, newSourceEntry *string, relevanceBoost float64) error {
	ctx, span := StartSpan(ctx, "context.touch")
	defer span.End()

	LoggerFrom(ctx).Info("touching context", "uuid", contextUUID, "boost", relevanceBoost)

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return err
	}

	// Get current context
	doc, err := client.Collection(KnowledgeCollection).Doc(contextUUID).Get(ctx)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("context not found: %w", err)
	}

	// Parse current metadata
	data := doc.Data()
	metadataStr := getStringField(data, "metadata")

	var metadata ContextMetadata
	if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to parse context metadata: %w", err)
	}

	// Update metadata
	metadata.LastTouched = time.Now().Format(time.RFC3339)
	metadata.Relevance = min(1.0, metadata.Relevance+relevanceBoost)

	if newSourceEntry != nil && *newSourceEntry != "" {
		metadata.SourceEntries = append(metadata.SourceEntries, *newSourceEntry)
	}

	// Save updated metadata
	updatedMetadataJSON, err := json.Marshal(metadata)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to marshal updated metadata: %w", err)
	}

	_, err = client.Collection(KnowledgeCollection).Doc(contextUUID).Update(ctx, []firestore.Update{
		{Path: "metadata", Value: string(updatedMetadataJSON)},
		{Path: "timestamp", Value: time.Now().Format(time.RFC3339)},
	})
	if err != nil {
		span.RecordError(err)
		return err
	}

	LoggerFrom(ctx).Info("context touched", "uuid", contextUUID, "new_relevance", metadata.Relevance)
	return nil
}

// normalizeContextName trims, lowercases, and converts spaces to underscores for consistency with FindContextByName.
func normalizeContextName(name string) string {
	s := strings.TrimSpace(strings.ToLower(name))
	return strings.ReplaceAll(s, " ", "_")
}

// EnsureContextExists finds a context by name or creates it with placeholder content. No LLM calls.
// Returns the context UUID for use in TouchContext/TouchContextBatch and synthesis.
func EnsureContextExists(ctx context.Context, name string) (string, error) {
	ctx, span := StartSpan(ctx, "context.ensure_exists")
	defer span.End()

	norm := normalizeContextName(name)
	if norm == "" {
		return "", fmt.Errorf("context name is empty after normalization")
	}

	existing, _, err := FindContextByName(ctx, norm)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return existing.UUID, nil
	}

	placeholderContent := "Ongoing: " + norm
	uuid, err := CreateContext(ctx, norm, placeholderContent, "auto", nil, nil)
	if err != nil {
		span.RecordError(err)
		return "", err
	}
	LoggerFrom(ctx).Info("ensure_context_created", "name", norm, "uuid", uuid)
	return uuid, nil
}

// TouchContextBatch appends multiple entry UUIDs to a context's SourceEntries and updates last_touched in one read/update.
func TouchContextBatch(ctx context.Context, contextUUID string, entryUUIDs []string, relevanceBoost float64) error {
	ctx, span := StartSpan(ctx, "context.touch_batch")
	defer span.End()

	if len(entryUUIDs) == 0 {
		return nil
	}

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return err
	}

	doc, err := client.Collection(KnowledgeCollection).Doc(contextUUID).Get(ctx)
	if err != nil {
		span.RecordError(err)
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

	_, err = client.Collection(KnowledgeCollection).Doc(contextUUID).Update(ctx, []firestore.Update{
		{Path: "metadata", Value: string(updatedMetadataJSON)},
		{Path: "timestamp", Value: time.Now().Format(time.RFC3339)},
	})
	if err != nil {
		span.RecordError(err)
		return err
	}
	LoggerFrom(ctx).Info("context touched batch", "uuid", contextUUID, "entries_added", len(entryUUIDs))
	return nil
}

// permanentContextsOnce ensures permanent contexts are initialized only once per cold start.
var permanentContextsOnce sync.Once

// byRelevanceDesc implements sort.Interface to sort nodes and metas by relevance descending.
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
func GetActiveContexts(ctx context.Context, limit int) ([]KnowledgeNode, []ContextMetadata, error) {
	ctx, span := StartSpan(ctx, "context.get_active")
	defer span.End()

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, nil, err
	}

	// Lazy initialization of permanent contexts (only once per cold start). Use a background context
	// that carries the same App so the goroutine can use Firestore without "no app in context".
	permanentContextsOnce.Do(func() {
		app := GetApp(ctx)
		if app == nil {
			return
		}
		bgCtx := WithApp(context.Background(), app)
		go func() {
			if err := InitializePermanentContexts(bgCtx); err != nil {
				LoggerFrom(bgCtx).Warn("failed to initialize permanent contexts", "error", err)
			}
		}()
	})

	// Query all context nodes
	iter := client.Collection(KnowledgeCollection).
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
			span.RecordError(err)
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

		// Parse metadata
		var meta ContextMetadata
		if err := json.Unmarshal([]byte(node.Metadata), &meta); err != nil {
			LoggerFrom(ctx).Warn("failed to parse context metadata", "uuid", node.UUID, "error", err)
			continue
		}

		// Filter by relevance
		if meta.Relevance >= MinActiveRelevance {
			nodes = append(nodes, node)
			metas = append(metas, meta)
		}
	}

	// Sort by relevance (descending)
	sort.Sort(byRelevanceDesc{nodes: nodes, metas: metas})

	// Apply limit
	if limit > 0 && len(nodes) > limit {
		nodes = nodes[:limit]
		metas = metas[:limit]
	}

	LoggerFrom(ctx).Info("active contexts retrieved", "count", len(nodes))
	span.SetAttributes(map[string]string{
		"context_count": fmt.Sprintf("%d", len(nodes)),
	})

	return nodes, metas, nil
}

// DecayContexts applies time-based decay to all auto contexts.
// Returns the number of contexts that were decayed.
func DecayContexts(ctx context.Context) (int, error) {
	ctx, span := StartSpan(ctx, "context.decay")
	defer span.End()

	LoggerFrom(ctx).Info("starting context decay")

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}

	// Query all context nodes
	iter := client.Collection(KnowledgeCollection).
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
			span.RecordError(err)
			return decayedCount, err
		}

		data := doc.Data()
		metadataStr := getStringField(data, "metadata")

		var meta ContextMetadata
		if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
			LoggerFrom(ctx).Warn("failed to parse context metadata for decay", "uuid", doc.Ref.ID, "error", err)
			continue
		}

		// Skip permanent contexts (decay_rate == 0)
		if meta.DecayRate == 0 {
			continue
		}

		// Calculate days since last touched
		lastTouched, err := time.Parse(time.RFC3339, meta.LastTouched)
		if err != nil {
			LoggerFrom(ctx).Warn("failed to parse last_touched", "uuid", doc.Ref.ID, "error", err)
			continue
		}

		daysSince := now.Sub(lastTouched).Hours() / 24
		if daysSince < 1 {
			continue // Only decay if at least a day has passed
		}

		// Apply decay: new_relevance = old_relevance * (1 - decay_rate)^days
		decayFactor := 1.0
		for i := 0; i < int(daysSince); i++ {
			decayFactor *= (1 - meta.DecayRate)
		}
		newRelevance := meta.Relevance * decayFactor

		// Update metadata
		meta.Relevance = newRelevance
		meta.LastTouched = now.Format(time.RFC3339)

		updatedMetadataJSON, err := json.Marshal(meta)
		if err != nil {
			continue
		}

		_, err = client.Collection(KnowledgeCollection).Doc(doc.Ref.ID).Update(ctx, []firestore.Update{
			{Path: "metadata", Value: string(updatedMetadataJSON)},
		})
		if err != nil {
			LoggerFrom(ctx).Warn("failed to update decayed context", "uuid", doc.Ref.ID, "error", err)
			continue
		}

		decayedCount++
		LoggerFrom(ctx).Debug("context decayed",
			"uuid", doc.Ref.ID,
			"name", meta.ContextName,
			"old_relevance", meta.Relevance/decayFactor,
			"new_relevance", newRelevance,
		)
	}

	LoggerFrom(ctx).Info("context decay completed", "decayed_count", decayedCount)
	span.SetAttributes(map[string]string{
		"decayed_count": fmt.Sprintf("%d", decayedCount),
	})

	return decayedCount, nil
}

// DeleteContext deletes a context by UUID.
func DeleteContext(ctx context.Context, contextUUID string) error {
	ctx, span := StartSpan(ctx, "context.delete")
	defer span.End()

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return err
	}

	_, err = client.Collection(KnowledgeCollection).Doc(contextUUID).Delete(ctx)
	if err != nil {
		span.RecordError(err)
		return err
	}

	LoggerFrom(ctx).Info("context deleted", "uuid", contextUUID)
	return nil
}

// GetContextMetadata returns metadata for a context by UUID. Returns nil, nil if doc not found.
func GetContextMetadata(ctx context.Context, contextUUID string) (*ContextMetadata, error) {
	client, err := GetFirestoreClient(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := client.Collection(KnowledgeCollection).Doc(contextUUID).Get(ctx)
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
func FindContextByName(ctx context.Context, name string) (*KnowledgeNode, *ContextMetadata, error) {
	ctx, span := StartSpan(ctx, "context.find_by_name")
	defer span.End()

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, nil, err
	}

	// Query all context nodes
	iter := client.Collection(KnowledgeCollection).
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
			span.RecordError(err)
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

	return nil, nil, nil // Not found
}

// =============================================================================
// CONTEXT DETECTION (Phase 2)
// =============================================================================

// MatchEntryToContexts finds contexts that semantically match the entry content.
// Returns context UUIDs and their similarity scores above the threshold.
func MatchEntryToContexts(ctx context.Context, entryContent string, threshold float64) ([]string, []float64, error) {
	ctx, span := StartSpan(ctx, "context.match_entry")
	defer span.End()

	// Generate embedding for the entry content
	entryVector, err := GenerateEmbedding(ctx, entryContent)
	if err != nil {
		span.RecordError(err)
		return nil, nil, err
	}

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, nil, err
	}

	// Use vector search to find similar context nodes
	// We use a distance threshold to find semantically related contexts
	distanceThreshold := 1 - threshold // Convert similarity threshold to distance
	vectorQuery := client.Collection(KnowledgeCollection).
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
			// Vector search may not be available, fall back gracefully
			LoggerFrom(ctx).Debug("vector search error (may not be supported)", "error", err)
			break
		}

		// Parse metadata to check relevance
		data := doc.Data()
		metadataStr := getStringField(data, "metadata")

		var meta ContextMetadata
		if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
			continue
		}

		// Only include contexts above minimum relevance
		if meta.Relevance >= MinActiveRelevance {
			uuids = append(uuids, doc.Ref.ID)
			// Approximate score from distance (cosine similarity = 1 - cosine distance)
			scores = append(scores, meta.Relevance) // Use relevance as proxy for match quality
		}
	}

	LoggerFrom(ctx).Info("matched contexts", "entry_preview", truncateForLog(entryContent, 50), "matches", len(uuids))
	return uuids, scores, nil
}

// DetectOrCreateContext analyzes entry content and either links to existing context or creates new.
// It returns the list of context UUIDs that were touched or created (for synthesis queuing).
func DetectOrCreateContext(ctx context.Context, entryContent, entryUUID string) ([]string, error) {
	ctx, span := StartSpan(ctx, "context.detect_or_create")
	defer span.End()

	LoggerFrom(ctx).Info("detecting context for entry", "entry_uuid", entryUUID, "content", truncateForLog(entryContent, 50))

	matchedUUIDs, scores, err := MatchEntryToContexts(ctx, entryContent, 0.6) // 60% similarity threshold
	if err != nil {
		LoggerFrom(ctx).Warn("context matching failed", "error", err)
	}

	if len(matchedUUIDs) > 0 {
		for i, uuid := range matchedUUIDs {
			relevanceBoost := 0.05
			if scores[i] > 0.8 {
				relevanceBoost = 0.10
			}
			if err := TouchContext(ctx, uuid, &entryUUID, relevanceBoost); err != nil {
				LoggerFrom(ctx).Warn("failed to touch matched context", "uuid", uuid, "error", err)
			}
		}
		LoggerFrom(ctx).Info("linked entry to existing contexts", "entry_uuid", entryUUID, "context_count", len(matchedUUIDs))
		return matchedUUIDs, nil
	}

	shouldCreate, contextName, entities := analyzeForNewContext(ctx, entryContent)
	if !shouldCreate {
		LoggerFrom(ctx).Debug("no new context needed", "entry_uuid", entryUUID)
		return nil, nil
	}

	existingNode, _, err := FindContextByName(ctx, contextName)
	if err == nil && existingNode != nil {
		LoggerFrom(ctx).Info("context with name already exists, touching instead", "name", contextName, "uuid", existingNode.UUID)
		if err := TouchContext(ctx, existingNode.UUID, &entryUUID, 0.05); err != nil {
			LoggerFrom(ctx).Warn("failed to touch existing context", "uuid", existingNode.UUID, "error", err)
		}
		return []string{existingNode.UUID}, nil
	}

	nodeUUID, err := CreateContext(ctx, contextName, entryContent, "auto", entities, []string{entryUUID})
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to create context: %w", err)
	}

	LoggerFrom(ctx).Info("created new context from entry", "entry_uuid", entryUUID, "context_name", contextName)
	return []string{nodeUUID}, nil
}

// analyzeForNewContext uses the LLM to determine if content warrants a new context.
// Returns (shouldCreate, contextName, entities).
func analyzeForNewContext(ctx context.Context, entryContent string) (bool, string, []string) {
	ctx, span := StartSpan(ctx, "context.analyze_for_new")
	defer span.End()

	// Skip very short content
	if len(strings.TrimSpace(entryContent)) < 20 {
		return false, "", nil
	}

	// Use Gemini to analyze if this content represents a new project/plan/topic
	client, err := GetGeminiClient(ctx)
	if err != nil {
		LoggerFrom(ctx).Warn("failed to get Gemini client for context analysis", "error", err)
		return false, "", nil
	}

	model := client.GenerativeModel(GetEffectiveModel(ctx, GeminiModel))
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(512)
	model.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"is_project_or_plan": {
				Type:        genai.TypeBoolean,
				Description: "True if this content describes a new project, plan, event, or ongoing activity that spans multiple sessions",
			},
			"context_name": {
				Type:        genai.TypeString,
				Description: "Short snake_case name for the context (e.g., party_planning, job_search, vacation_research)",
			},
			"entities": {
				Type:        genai.TypeArray,
				Items:       &genai.Schema{Type: genai.TypeString},
				Description: "Key entities, dates, or topics mentioned",
			},
		},
	}

	prompt := prompts.FormatContextAnalyze(WrapAsUserData(SanitizePrompt(entryContent)))

	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		LoggerFrom(ctx).Warn("context analysis failed", "error", err)
		return false, "", nil
	}

	jsonText := extractTextFromResponse(resp)

	var result struct {
		IsProjectOrPlan bool     `json:"is_project_or_plan"`
		ContextName     string   `json:"context_name"`
		Entities        []string `json:"entities"`
	}

	if err := json.Unmarshal([]byte(jsonText), &result); err != nil {
		if err := llmjson.RepairAndUnmarshal(jsonText, &result); err != nil {
			partial, _ := llmjson.PartialUnmarshalObject(jsonText, []string{"is_project_or_plan", "context_name", "entities"})
			if len(partial) > 0 {
				if raw, ok := partial["is_project_or_plan"]; ok && len(raw) > 0 {
					_ = json.Unmarshal(raw, &result.IsProjectOrPlan)
				}
				if raw, ok := partial["context_name"]; ok && len(raw) > 0 {
					_ = json.Unmarshal(raw, &result.ContextName)
				}
				if raw, ok := partial["entities"]; ok && len(raw) > 0 {
					_ = json.Unmarshal(raw, &result.Entities)
				}
			}
			if !result.IsProjectOrPlan && result.ContextName == "" && len(result.Entities) == 0 {
				LoggerFrom(ctx).Warn("failed to parse context analysis response", "error", err)
				return false, "", nil
			}
		}
	}

	return result.IsProjectOrPlan, result.ContextName, result.Entities
}

// maxSourceEntriesForSynthesis is the number of most recent source entries to use when synthesizing a context.
const maxSourceEntriesForSynthesis = 10

// maxRawLogsChars caps the total raw log text sent to the LLM for synthesis.
const maxRawLogsChars = 6000

// synthesisNoNewInfoOverlap is the word-set overlap ratio above which we skip synthesis (0.85 = 85% of new words already in briefing).
const synthesisNoNewInfoOverlap = 0.85

// synthesisNoNewInfoMinRawLen: if rawLogs is shorter than this and contained in oldContent, skip synthesis.
const synthesisNoNewInfoMinRawLen = 50

// synthesisHasNoNewInfo returns true if rawLogs does not add substantial information over oldContent (local check, no API).
func synthesisHasNoNewInfo(rawLogs, oldContent string) bool {
	rawLogs = strings.TrimSpace(rawLogs)
	oldContent = strings.TrimSpace(oldContent)
	if rawLogs == "" {
		return true
	}
	// Option B: very short new logs already contained in briefing
	if len(rawLogs) < synthesisNoNewInfoMinRawLen && oldContent != "" {
		if strings.Contains(oldContent, rawLogs) {
			return true
		}
		// Normalize for loose containment (e.g. ignore timestamps)
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
	// Option A: word-set overlap
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

// SynthesizeContext loads the context node and its source entries, then uses the LLM to produce a high-density briefing and overwrites the node's content.
func SynthesizeContext(ctx context.Context, contextUUID string) error {
	ctx, span := StartSpan(ctx, "context.synthesize")
	defer span.End()

	client, err := GetFirestoreClient(ctx)
	if err != nil {
		span.RecordError(err)
		return err
	}

	doc, err := client.Collection(KnowledgeCollection).Doc(contextUUID).Get(ctx)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("context not found: %w", err)
	}

	data := doc.Data()
	oldContent := getStringField(data, "content")
	metadataStr := getStringField(data, "metadata")

	var meta ContextMetadata
	if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to parse context metadata: %w", err)
	}

	// Take last N source entry UUIDs (most recent in append order)
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
		entry, err := GetEntry(ctx, uuid)
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

	// Local similarity filter: skip LLM if new logs don't add substantial information.
	if synthesisHasNoNewInfo(rawLogs, oldContent) {
		LoggerFrom(ctx).Info("context synthesis skipped (no new info)", "uuid", contextUUID, "name", meta.ContextName)
		now := time.Now().Format(time.RFC3339)
		meta.LastTouched = now
		meta.LastSynthesizedAt = now
		meta.SourceEntryCountAtSynthesis = len(meta.SourceEntries)
		updatedMetadataJSON, err := json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("marshal metadata: %w", err)
		}
		_, err = client.Collection(KnowledgeCollection).Doc(contextUUID).Update(ctx, []firestore.Update{
			{Path: "metadata", Value: string(updatedMetadataJSON)},
		})
		if err != nil {
			span.RecordError(err)
			return err
		}
		return nil
	}

	userPrompt := fmt.Sprintf("Current Briefing:\n%s\n\nNew Information:\n%s\n\nTask: Write a new briefing (max 250 words) that preserves active Open Loops, critical dates, and key stakeholder preferences. Use bullet points for status.",
		WrapAsUserData(oldContent), WrapAsUserData(rawLogs))

	newContent, err := GenerateContentSimple(ctx, prompts.ExecutiveSummary(), userPrompt, &GenConfig{MaxOutputTokens: 512})
	if err != nil {
		span.RecordError(err)
		return err
	}

	newContent = strings.TrimSpace(newContent)
	if newContent == "" {
		return nil
	}

	now := time.Now().Format(time.RFC3339)
	meta.LastSynthesizedAt = now
	meta.SourceEntryCountAtSynthesis = len(meta.SourceEntries)
	updatedMetadataJSON, err := json.Marshal(meta)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("marshal metadata: %w", err)
	}

	_, err = client.Collection(KnowledgeCollection).Doc(contextUUID).Update(ctx, []firestore.Update{
		{Path: "content", Value: newContent},
		{Path: "timestamp", Value: now},
		{Path: "metadata", Value: string(updatedMetadataJSON)},
	})
	if err != nil {
		span.RecordError(err)
		return err
	}

	LoggerFrom(ctx).Info("context synthesized", "uuid", contextUUID, "name", meta.ContextName)
	span.SetAttributes(map[string]string{"context_uuid": contextUUID, "context_name": meta.ContextName})
	return nil
}

// firstSentence returns the first sentence of s, or up to maxChars runes if no period found.
func firstSentence(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	runes := []rune(s)
	for i, r := range runes {
		if r == '.' || r == '!' || r == '?' {
			return strings.TrimSpace(string(runes[:i+1]))
		}
	}
	if len(runes) <= maxChars {
		return string(runes)
	}
	return string(runes[:maxChars]) + "..."
}

// =============================================================================
// PERMANENT CONTEXTS (Phase 5)
// =============================================================================

// InitializePermanentContexts ensures permanent contexts exist. It never overwrites content of existing
// contexts so that user_profile and others remain dynamic (updated only by synthesis/profiling).
func InitializePermanentContexts(ctx context.Context) error {
	ctx, span := StartSpan(ctx, "context.init_permanent")
	defer span.End()

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
		existing, _, err := FindContextByName(ctx, pc.Name)
		if err != nil {
			LoggerFrom(ctx).Warn("error checking permanent context", "name", pc.Name, "error", err)
			continue
		}
		if existing != nil {
			continue
		}
		_, err = CreateContext(ctx, pc.Name, pc.Content, "permanent", nil, nil)
		if err != nil {
			LoggerFrom(ctx).Warn("failed to create permanent context", "name", pc.Name, "error", err)
			continue
		}
		LoggerFrom(ctx).Info("created permanent context", "name", pc.Name)
	}

	return nil
}
