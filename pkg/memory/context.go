// Package memory provides context nodes (briefings, user_profile, etc.) in the knowledge graph.
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/genai"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/journal"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/api/iterator"
)

func truncateForLogContext(s string, maxLen int) string {
	if len([]rune(s)) <= maxLen {
		return s
	}
	return utils.TruncateString(s, maxLen) + "..."
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
// env supplies Firestore and Config; pass from the caller (e.g. ToolEnv).
func CreateContext(ctx context.Context, env infra.ToolEnv, name, content, contextType string, entities, sourceEntries []string) (string, error) {
	ctx, span := infra.StartSpan(ctx, "context.create")
	defer span.End()

	infra.LoggerFrom(ctx).Info("creating context", "name", name, "type", contextType)

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

	if env == nil {
		return "", fmt.Errorf("env required")
	}
	nodeUUID, err := UpsertKnowledge(ctx, env, content, ContextNodeType, string(metadataJSON), nil)
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	infra.LoggerFrom(ctx).Info("context created", "uuid", nodeUUID, "name", name)
	span.SetAttributes(map[string]string{
		"context_uuid": nodeUUID,
		"context_name": name,
	})

	return nodeUUID, nil
}

// TouchContext updates a context's last_touched timestamp and optionally boosts relevance.
// env supplies Firestore; pass from the caller (e.g. ToolEnv).
func TouchContext(ctx context.Context, env infra.ToolEnv, contextUUID string, newSourceEntry *string, relevanceBoost float64) error {
	ctx, span := infra.StartSpan(ctx, "context.touch")
	defer span.End()

	infra.LoggerFrom(ctx).Info("touching context", "uuid", contextUUID, "boost", relevanceBoost)

	if env == nil {
		return fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
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
	metadataStr := infra.GetStringField(data, "metadata")

	var metadata ContextMetadata
	if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
		span.RecordError(err)
		return fmt.Errorf("failed to parse context metadata: %w", err)
	}

	metadata.LastTouched = time.Now().Format(time.RFC3339)
	metadata.Relevance = min(1.0, metadata.Relevance+relevanceBoost)

	if newSourceEntry != nil && *newSourceEntry != "" {
		metadata.SourceEntries = append(metadata.SourceEntries, *newSourceEntry)
	}

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

	infra.LoggerFrom(ctx).Info("context touched", "uuid", contextUUID, "new_relevance", metadata.Relevance)
	return nil
}

func normalizeContextName(name string) string {
	s := strings.TrimSpace(strings.ToLower(name))
	return strings.ReplaceAll(s, " ", "_")
}

// EnsureContextExists finds a context by name or creates it with placeholder content.
// env supplies Firestore; pass from the caller (e.g. ToolEnv).
func EnsureContextExists(ctx context.Context, env infra.ToolEnv, name string) (string, error) {
	ctx, span := infra.StartSpan(ctx, "context.ensure_exists")
	defer span.End()

	norm := normalizeContextName(name)
	if norm == "" {
		return "", fmt.Errorf("context name is empty after normalization")
	}

	existing, _, err := FindContextByName(ctx, env, norm)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return existing.UUID, nil
	}

	placeholderContent := "Ongoing: " + norm
	uuid, err := CreateContext(ctx, env, norm, placeholderContent, "auto", nil, nil)
	if err != nil {
		span.RecordError(err)
		return "", err
	}
	infra.LoggerFrom(ctx).Info("ensure_context_created", "name", norm, "uuid", uuid)
	return uuid, nil
}

// TouchContextBatch appends multiple entry UUIDs to a context's SourceEntries and updates last_touched.
// env supplies Firestore; pass from the caller (e.g. ToolEnv).
func TouchContextBatch(ctx context.Context, env infra.ToolEnv, contextUUID string, entryUUIDs []string, relevanceBoost float64) error {
	ctx, span := infra.StartSpan(ctx, "context.touch_batch")
	defer span.End()

	if len(entryUUIDs) == 0 {
		return nil
	}

	if env == nil {
		return fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
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
	metadataStr := infra.GetStringField(data, "metadata")
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
	infra.LoggerFrom(ctx).Info("context touched batch", "uuid", contextUUID, "entries_added", len(entryUUIDs))
	return nil
}

var permanentContextsOnce sync.Once

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
// env supplies Firestore; pass from the caller (e.g. ToolEnv).
func GetActiveContexts(ctx context.Context, env infra.ToolEnv, limit int) ([]KnowledgeNode, []ContextMetadata, error) {
	ctx, span := infra.StartSpan(ctx, "context.get_active")
	defer span.End()
	infra.LoggerFrom(ctx).Debug("get active contexts", "limit", limit, "reason", "for system prompt and context-aware answers")

	if env == nil {
		return nil, nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, nil, err
	}

	permanentContextsOnce.Do(func() {
		if env == nil {
			return
		}
		envCopy := env
		bgCtx := context.Background()
		go func() {
			if err := InitializePermanentContexts(bgCtx, envCopy); err != nil {
				infra.LoggerFrom(bgCtx).Warn("failed to initialize permanent contexts", "error", err)
			}
		}()
	})

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
			Content:   infra.GetStringField(data, "content"),
			NodeType:  infra.GetStringField(data, "node_type"),
			Metadata:  infra.GetStringField(data, "metadata"),
			Timestamp: infra.GetStringField(data, "timestamp"),
		}

		var meta ContextMetadata
		if err := json.Unmarshal([]byte(node.Metadata), &meta); err != nil {
			infra.LoggerFrom(ctx).Warn("failed to parse context metadata", "uuid", node.UUID, "error", err)
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

	infra.LoggerFrom(ctx).Info("active contexts retrieved", "count", len(nodes))
	span.SetAttributes(map[string]string{
		"context_count": fmt.Sprintf("%d", len(nodes)),
	})

	return nodes, metas, nil
}

// DecayContexts applies time-based decay to all auto contexts.
func DecayContexts(ctx context.Context, env infra.ToolEnv) (int, error) {
	ctx, span := infra.StartSpan(ctx, "context.decay")
	defer span.End()

	infra.LoggerFrom(ctx).Info("starting context decay")

	client, err := env.Firestore(ctx)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}

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
		metadataStr := infra.GetStringField(data, "metadata")

		var meta ContextMetadata
		if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
			infra.LoggerFrom(ctx).Warn("failed to parse context metadata for decay", "uuid", doc.Ref.ID, "error", err)
			continue
		}

		if meta.DecayRate == 0 {
			continue
		}

		lastTouched, err := time.Parse(time.RFC3339, meta.LastTouched)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("failed to parse last_touched", "uuid", doc.Ref.ID, "error", err)
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

		_, err = client.Collection(KnowledgeCollection).Doc(doc.Ref.ID).Update(ctx, []firestore.Update{
			{Path: "metadata", Value: string(updatedMetadataJSON)},
		})
		if err != nil {
			infra.LoggerFrom(ctx).Warn("failed to update decayed context", "uuid", doc.Ref.ID, "error", err)
			continue
		}

		decayedCount++
		infra.LoggerFrom(ctx).Debug("context decayed",
			"uuid", doc.Ref.ID,
			"name", meta.ContextName,
			"old_relevance", meta.Relevance/decayFactor,
			"new_relevance", newRelevance,
		)
	}

	infra.LoggerFrom(ctx).Info("context decay completed", "decayed_count", decayedCount)
	span.SetAttributes(map[string]string{
		"decayed_count": fmt.Sprintf("%d", decayedCount),
	})

	return decayedCount, nil
}

// DeleteContext deletes a context by UUID.
func DeleteContext(ctx context.Context, env infra.ToolEnv, contextUUID string) error {
	ctx, span := infra.StartSpan(ctx, "context.delete")
	defer span.End()

	client, err := env.Firestore(ctx)
	if err != nil {
		span.RecordError(err)
		return err
	}

	_, err = client.Collection(KnowledgeCollection).Doc(contextUUID).Delete(ctx)
	if err != nil {
		span.RecordError(err)
		return err
	}

	infra.LoggerFrom(ctx).Info("context deleted", "uuid", contextUUID)
	return nil
}

// GetContextMetadata returns metadata for a context by UUID. env supplies Firestore; pass from the caller (e.g. ToolEnv).
func GetContextMetadata(ctx context.Context, env infra.ToolEnv, contextUUID string) (*ContextMetadata, error) {
	if env == nil {
		return nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := client.Collection(KnowledgeCollection).Doc(contextUUID).Get(ctx)
	if err != nil {
		return nil, err
	}
	data := doc.Data()
	metadataStr := infra.GetStringField(data, "metadata")
	var meta ContextMetadata
	if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
		return nil, fmt.Errorf("parse context metadata: %w", err)
	}
	return &meta, nil
}

// FindContextByName finds a context by its name. env supplies Firestore; pass from the caller (e.g. ToolEnv).
func FindContextByName(ctx context.Context, env infra.ToolEnv, name string) (*KnowledgeNode, *ContextMetadata, error) {
	ctx, span := infra.StartSpan(ctx, "context.find_by_name")
	defer span.End()

	if env == nil {
		return nil, nil, fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, nil, err
	}

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
		metadataStr := infra.GetStringField(data, "metadata")

		var meta ContextMetadata
		if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
			continue
		}

		if strings.ToLower(meta.ContextName) == nameLower {
			node := &KnowledgeNode{
				UUID:      doc.Ref.ID,
				Content:   infra.GetStringField(data, "content"),
				NodeType:  infra.GetStringField(data, "node_type"),
				Metadata:  metadataStr,
				Timestamp: infra.GetStringField(data, "timestamp"),
			}
			return node, &meta, nil
		}
	}

	return nil, nil, nil
}

// MatchEntryToContexts finds contexts that semantically match the entry content.
// env supplies Firestore and Config; pass from the caller (e.g. ToolEnv).
func MatchEntryToContexts(ctx context.Context, env infra.ToolEnv, entryContent string, threshold float64) ([]string, []float64, error) {
	ctx, span := infra.StartSpan(ctx, "context.match_entry")
	defer span.End()

	if env == nil || env.Config() == nil {
		return nil, nil, fmt.Errorf("env and config required")
	}
	cfg := env.Config()
	entryVector, err := infra.GenerateEmbedding(ctx, cfg.GoogleCloudProject, entryContent)
	if err != nil {
		span.RecordError(err)
		return nil, nil, err
	}

	client, err := env.Firestore(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, nil, err
	}

	distanceThreshold := 1 - threshold
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
			infra.LogVectorSearchFailed(ctx, KnowledgeCollection, err, 0)
			break
		}

		data := doc.Data()
		metadataStr := infra.GetStringField(data, "metadata")

		var meta ContextMetadata
		if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
			continue
		}

		if meta.Relevance >= MinActiveRelevance {
			uuids = append(uuids, doc.Ref.ID)
			scores = append(scores, meta.Relevance)
		}
	}

	infra.LoggerFrom(ctx).Info("matched contexts", "entry_preview", truncateForLogContext(entryContent, 50), "matches", len(uuids))
	return uuids, scores, nil
}

// DetectOrCreateContext analyzes entry content and either links to existing context or creates new.
// env supplies Firestore and Config; pass from the caller (e.g. ToolEnv).
func DetectOrCreateContext(ctx context.Context, env infra.ToolEnv, entryContent, entryUUID string) ([]string, error) {
	ctx, span := infra.StartSpan(ctx, "context.detect_or_create")
	defer span.End()

	infra.LoggerFrom(ctx).Info("detecting context for entry", "entry_uuid", entryUUID, "content", truncateForLogContext(entryContent, 50))

	matchedUUIDs, scores, err := MatchEntryToContexts(ctx, env, entryContent, 0.6)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("context matching failed", "error", err)
	}

	if len(matchedUUIDs) > 0 {
		for i, uuid := range matchedUUIDs {
			relevanceBoost := 0.05
			if scores[i] > 0.8 {
				relevanceBoost = 0.10
			}
			if err := TouchContext(ctx, env, uuid, &entryUUID, relevanceBoost); err != nil {
				infra.LoggerFrom(ctx).Warn("failed to touch matched context", "uuid", uuid, "error", err)
			}
		}
		infra.LoggerFrom(ctx).Info("linked entry to existing contexts", "entry_uuid", entryUUID, "context_count", len(matchedUUIDs))
		return matchedUUIDs, nil
	}

	shouldCreate, contextName, entities := analyzeForNewContext(ctx, env, entryContent)
	if !shouldCreate {
		infra.LoggerFrom(ctx).Debug("no new context needed", "entry_uuid", entryUUID)
		return nil, nil
	}

	existingNode, _, err := FindContextByName(ctx, env, contextName)
	if err == nil && existingNode != nil {
		infra.LoggerFrom(ctx).Info("context with name already exists, touching instead", "name", contextName, "uuid", existingNode.UUID)
		if err := TouchContext(ctx, env, existingNode.UUID, &entryUUID, 0.05); err != nil {
			infra.LoggerFrom(ctx).Warn("failed to touch existing context", "uuid", existingNode.UUID, "error", err)
		}
		return []string{existingNode.UUID}, nil
	}

	nodeUUID, err := CreateContext(ctx, env, contextName, entryContent, "auto", entities, []string{entryUUID})
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to create context: %w", err)
	}

	infra.LoggerFrom(ctx).Info("created new context from entry", "entry_uuid", entryUUID, "context_name", contextName)
	return []string{nodeUUID}, nil
}

func analyzeForNewContext(ctx context.Context, env infra.ToolEnv, entryContent string) (bool, string, []string) {
	ctx, span := infra.StartSpan(ctx, "context.analyze_for_new")
	defer span.End()

	if len(strings.TrimSpace(entryContent)) < 20 {
		return false, "", nil
	}
	if env == nil || env.Config() == nil {
		infra.LoggerFrom(ctx).Warn("env required for context analysis")
		return false, "", nil
	}
	prompt, err := prompts.BuildContextAnalyze(prompts.ContextAnalyzeData{
		EntryContent: utils.WrapAsUserData(utils.SanitizePrompt(entryContent)),
	})
	if err != nil {
		infra.LoggerFrom(ctx).Warn("context analysis prompt build failed", "error", err)
		return false, "", nil
	}
	req := &infra.LLMRequest{
		Parts:     []*genai.Part{{Text: prompt}},
		Model:     env.Config().GeminiModel,
		GenConfig: &infra.GenConfig{MaxOutputTokens: 512},
	}
	resp, err := env.Dispatch(ctx, req)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("context analysis failed", "error", err)
		return false, "", nil
	}

	text := strings.TrimSpace(infra.ExtractText(resp))
	simple, sections := utils.ParseKeyValueMap(text)
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
// env supplies Firestore and Config; pass from the caller (e.g. ToolEnv).
func SynthesizeContext(ctx context.Context, env infra.ToolEnv, contextUUID string) error {
	ctx, span := infra.StartSpan(ctx, "context.synthesize")
	defer span.End()

	if env == nil {
		return fmt.Errorf("env required")
	}
	client, err := env.Firestore(ctx)
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
	oldContent := infra.GetStringField(data, "content")
	metadataStr := infra.GetStringField(data, "metadata")

	var meta ContextMetadata
	if err := json.Unmarshal([]byte(metadataStr), &meta); err != nil {
		span.RecordError(err)
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
		entry, err := journal.GetEntry(ctx, client, uuid)
		if err != nil || entry == nil {
			continue
		}
		part := fmt.Sprintf("[%s] %s", entry.Timestamp, entry.Content)
		if totalLen+len(part) > maxRawLogsChars {
			part = utils.TruncateToMaxBytes(part, maxRawLogsChars-totalLen)
		}
		rawParts = append(rawParts, part)
		totalLen += len(part)
	}
	rawLogs := strings.Join(rawParts, "\n\n")

	if synthesisHasNoNewInfo(rawLogs, oldContent) {
		infra.LoggerFrom(ctx).Info("context synthesis skipped (no new info)", "uuid", contextUUID, "name", meta.ContextName)
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

	cfg := env.Config()
	if cfg == nil {
		return fmt.Errorf("no app config for synthesis")
	}
	userPrompt := fmt.Sprintf("Current Briefing:\n%s\n\nNew Information:\n%s\n\nTask: Write a new briefing (max 250 words) that preserves active Open Loops, critical dates, and key stakeholder preferences. Use bullet points for status.",
		utils.WrapAsUserData(oldContent), utils.WrapAsUserData(rawLogs))

	newContent, err := infra.GenerateContentSimple(ctx, env, prompts.ExecutiveSummary(), userPrompt, cfg, &infra.GenConfig{MaxOutputTokens: 512})
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

	infra.LoggerFrom(ctx).Info("context synthesized", "uuid", contextUUID, "name", meta.ContextName)
	span.SetAttributes(map[string]string{"context_uuid": contextUUID, "context_name": meta.ContextName})
	return nil
}

// InitializePermanentContexts ensures permanent contexts exist.
// env supplies Firestore and Config; pass from the caller (e.g. ToolEnv).
func InitializePermanentContexts(ctx context.Context, env infra.ToolEnv) error {
	ctx, span := infra.StartSpan(ctx, "context.init_permanent")
	defer span.End()

	if env == nil {
		return fmt.Errorf("env required")
	}
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
		existing, _, err := FindContextByName(ctx, env, pc.Name)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("error checking permanent context", "name", pc.Name, "error", err)
			continue
		}
		if existing != nil {
			continue
		}
		_, err = CreateContext(ctx, env, pc.Name, pc.Content, "permanent", nil, nil)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("failed to create permanent context", "name", pc.Name, "error", err)
			continue
		}
		infra.LoggerFrom(ctx).Info("created permanent context", "name", pc.Name)
	}

	return nil
}
