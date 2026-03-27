package agent

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/hoyle1974/memory"
	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/internal/prompts"
	"github.com/jackstrohm/jot/pkg/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	canonicalMapCollection = "_config"
	canonicalMapDocID      = "canonical_map"
	maxHotEdges            = 20
)

type refineryTriple struct {
	Subject   string
	SubType   string
	Predicate string
	Object    string
	ObjType   string
	RawLine   string
	ParseErr  string
}

// fetchCanonicalMap retrieves the live CanonicalMapConfig from Firestore at
// _config/canonical_map. Falls back to memory.AllowedPredicates on cold start
// (doc not found) or any fetch error.
func fetchCanonicalMap(ctx context.Context, app *infra.App) (memory.CanonicalMapConfig, error) {
	client, err := app.Firestore(ctx)
	if err != nil {
		return fallbackCanonicalMap(), fmt.Errorf("fetchCanonicalMap: firestore client: %w", err)
	}
	doc, err := client.Collection(canonicalMapCollection).Doc(canonicalMapDocID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			infra.LoggerFrom(ctx).Info("canonical map doc not found, using defaults")
			return fallbackCanonicalMap(), nil
		}
		return fallbackCanonicalMap(), fmt.Errorf("fetchCanonicalMap: get doc: %w", err)
	}
	data := doc.Data()
	cfg := memory.CanonicalMapConfig{}
	if v, ok := data["allowed_predicates"].([]any); ok {
		cfg.AllowedPredicates = make([]string, 0, len(v))
		for _, p := range v {
			if s, ok := p.(string); ok && s != "" {
				cfg.AllowedPredicates = append(cfg.AllowedPredicates, s)
			}
		}
	}
	if v, ok := data["entity_types"].([]any); ok {
		cfg.EntityTypes = make([]string, 0, len(v))
		for _, t := range v {
			if s, ok := t.(string); ok && s != "" {
				cfg.EntityTypes = append(cfg.EntityTypes, s)
			}
		}
	}
	if len(cfg.AllowedPredicates) == 0 {
		cfg.AllowedPredicates = memory.AllowedPredicates
	}
	return cfg, nil
}

func fallbackCanonicalMap() memory.CanonicalMapConfig {
	return memory.CanonicalMapConfig{
		AllowedPredicates: memory.AllowedPredicates,
		EntityTypes: []string{
			memory.NodeTypePerson, memory.NodeTypePlace, memory.NodeTypeProject,
			memory.NodeTypeEvent, memory.NodeTypeTool, memory.NodeTypeAsset,
			memory.NodeTypeObject,
		},
	}
}

// appendPredicateToCanonicalMap appends a new predicate to the canonical_map singleton.
// If the doc doesn't exist, it is created with the default predicates plus the new one.
func appendPredicateToCanonicalMap(ctx context.Context, app *infra.App, predicate string) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("appendPredicateToCanonicalMap: firestore client: %w", err)
	}
	ref := client.Collection(canonicalMapCollection).Doc(canonicalMapDocID)
	_, err = ref.Update(ctx, []firestore.Update{
		{Path: "allowed_predicates", Value: firestore.ArrayUnion(predicate)},
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			initial := map[string]any{
				"allowed_predicates": append(memory.AllowedPredicates, predicate),
				"entity_types":       fallbackCanonicalMap().EntityTypes,
			}
			_, err = ref.Set(ctx, initial)
			return err
		}
		return fmt.Errorf("appendPredicateToCanonicalMap: update: %w", err)
	}
	return nil
}

func runRefineryPipeline(ctx context.Context, app *infra.App, entryUUID, content string) ([]string, error) {
	if app == nil {
		return nil, fmt.Errorf("runRefineryPipeline: app required")
	}

	canonMap, err := fetchCanonicalMap(ctx, app)
	if err != nil {
		// Non-fatal: fallback was already returned; log and proceed.
		infra.LoggerFrom(ctx).Warn("refinery: canonical map fetch failed, using fallback", "error", err)
	}

	// Fetch the owner identity so pronouns can be resolved rather than dropped.
	ownerName, err := app.Memory.FetchOwnerName(ctx)
	if err != nil {
		infra.LoggerFrom(ctx).Warn("refinery: could not fetch owner name, pronoun triples will be dropped", "error", err)
	}

	// NOTE: Pre-refinery Discovery (12-node vector search for prior context) is removed
	// per Project Loom spec. Context retrieval is now Stage 4 (Response Worker) only.

	triples, detectedOwner, err := refineryExtract(ctx, app, entryUUID, content, canonMap, ownerName)
	if err != nil {
		return nil, fmt.Errorf("refinery extract: %w", err)
	}

	// If the LLM detected a self-introduction and we don't yet have an identity anchor, persist it.
	if detectedOwner != "" && ownerName == "" {
		if uErr := app.Memory.UpsertOwnerName(ctx, detectedOwner); uErr != nil {
			infra.LoggerFrom(ctx).Warn("refinery: failed to persist detected owner name", "detected", detectedOwner, "error", uErr)
		} else {
			ownerName = detectedOwner
			infra.LoggerFrom(ctx).Info("refinery: owner identity established", "owner_name", ownerName)
		}
	}

	// If owner is still unknown and any triples were dropped due to pronouns, ask the user their name.
	if ownerName == "" && hasPronounDrops(triples) {
		q := memory.PendingQuestion{
			Question:       "I noticed you're writing about yourself in first person, but I don't know your name yet. What should I call you?",
			Kind:           "onboarding",
			Context:        "First-person pronouns were detected in a journal entry but no identity anchor exists.",
			SourceEntryIDs: []string{entryUUID},
		}
		if qErr := app.Memory.InsertPendingQuestions(ctx, []memory.PendingQuestion{q}); qErr != nil {
			infra.LoggerFrom(ctx).Warn("refinery: failed to enqueue owner name question", "error", qErr)
		}
	}

	if len(triples) == 0 {
		infra.LoggerFrom(ctx).Debug("refinery: no triples", "entry_uuid", entryUUID)
		return nil, nil
	}
	return refineryResolveCommit(ctx, app, entryUUID, triples, canonMap)
}

func refineryExtract(ctx context.Context, app *infra.App, entryUUID, content string, canonMap memory.CanonicalMapConfig, ownerName string) ([]refineryTriple, string, error) {
	predicateList := strings.Join(canonMap.AllowedPredicates, ", ")
	prompt, err := prompts.BuildRefinery(prompts.RefineryData{
		Entry:             utils.WrapAsUserData(utils.SanitizePrompt(content)),
		AllowedPredicates: utils.WrapAsUserData(predicateList),
		OwnerName:         ownerName,
	})
	if err != nil {
		return nil, "", fmt.Errorf("build refinery prompt: %w", err)
	}
	raw, err := infra.GenerateContentSimple(ctx, app, "", prompt+prompts.DataSafety(), app.Config(), &infra.GenConfig{MaxOutputTokens: 300})
	if err != nil {
		return nil, "", fmt.Errorf("refinery llm call: %w", err)
	}
	infra.LoggerFrom(ctx).Debug("refinery raw output", "entry_uuid", entryUUID, "output", raw)
	simple, sections := utils.ParseKeyValueMap(raw)
	if strings.EqualFold(simple["status"], "none") {
		return nil, "", nil
	}
	detectedOwner := strings.TrimSpace(simple["owner"])
	lines := sections["triples"]
	return parseRefineryTriples(lines, ownerName), detectedOwner, nil
}

func refineryResolveCommit(ctx context.Context, app *infra.App, entryUUID string, triples []refineryTriple, canonMap memory.CanonicalMapConfig) ([]string, error) {
	nodeIDs := make([]string, 0, len(triples)*3)
	for _, t := range triples {
		if t.ParseErr != "" {
			infra.LoggerFrom(ctx).Warn("refinery rejected triple", "entry_uuid", entryUUID, "reason", t.ParseErr, "raw_line", t.RawLine)
			continue
		}

		// Handle NEW: prefix — LLM has proposed a novel high-value predicate.
		rawPred := t.Predicate
		if strings.HasPrefix(strings.ToUpper(rawPred), "NEW:") {
			newPred := memory.NormalizedPredicate(strings.TrimPrefix(strings.TrimPrefix(rawPred, "NEW:"), "new:"))
			if newPred != "" {
				if appendErr := appendPredicateToCanonicalMap(ctx, app, newPred); appendErr != nil {
					infra.LoggerFrom(ctx).Warn("refinery: failed to append new predicate to canonical map",
						"predicate", newPred, "error", appendErr)
				} else {
					infra.LoggerFrom(ctx).Info("refinery: new predicate appended to canonical map", "predicate", newPred)
				}
				t.Predicate = newPred
			}
		}

		predicate := memory.CanonicalizePredicate(t.Predicate)
		if predicate == "" {
			infra.LoggerFrom(ctx).Warn("refinery rejected triple", "entry_uuid", entryUUID, "reason", "empty predicate after canonicalization", "raw_line", t.RawLine)
			continue
		}
		if !memory.IsAllowedPredicate(predicate) {
			infra.LoggerFrom(ctx).Warn("refinery accepted non-ontology predicate", "entry_uuid", entryUUID, "raw_predicate", t.Predicate, "canonical_predicate", predicate, "raw_line", t.RawLine)
		}
		subType := memory.CanonicalEntityNodeType(t.SubType)
		objType := memory.CanonicalEntityNodeType(t.ObjType)
		subj, err := app.Memory.EnsureNode(ctx, t.Subject, subType, entryUUID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("refinery ensure subject failed", "entry_uuid", entryUUID, "subject", t.Subject, "subject_type", subType, "error", err)
			continue
		}
		obj, err := app.Memory.EnsureNode(ctx, t.Object, objType, entryUUID)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("refinery ensure object failed", "entry_uuid", entryUUID, "object", t.Object, "object_type", objType, "error", err)
			continue
		}
		relID, err := app.Memory.CreateRelationshipNode(ctx, subj.UUID, predicate, obj.UUID, entryUUID, subj.Content, obj.Content)
		if err != nil {
			infra.LoggerFrom(ctx).Warn("refinery create relationship failed", "entry_uuid", entryUUID, "subject_uuid", subj.UUID, "predicate", predicate, "object_uuid", obj.UUID, "error", err)
			continue
		}
		nodeIDs = append(nodeIDs, subj.UUID, obj.UUID, relID)
		if err := app.Memory.AddEntityLink(ctx, subj.UUID, relID); err != nil {
			infra.LoggerFrom(ctx).Warn("refinery subject backlink failed", "entry_uuid", entryUUID, "node_uuid", subj.UUID, "relationship_uuid", relID, "error", err)
		}
		if err := app.Memory.AddEntityLink(ctx, obj.UUID, relID); err != nil {
			infra.LoggerFrom(ctx).Warn("refinery object backlink failed", "entry_uuid", entryUUID, "node_uuid", obj.UUID, "relationship_uuid", relID, "error", err)
		}
		if err := app.Memory.AddEntityLink(ctx, entryUUID, relID); err != nil {
			infra.LoggerFrom(ctx).Warn("refinery source-log backlink failed", "entry_uuid", entryUUID, "relationship_uuid", relID, "error", err)
		}
		// Update hot-edges on the object node for Loom graph cache maintenance.
		if heErr := updateHotEdges(ctx, app, obj.UUID, relID); heErr != nil {
			infra.LoggerFrom(ctx).Warn("refinery: updateHotEdges failed (non-fatal)", "object_uuid", obj.UUID, "rel_id", relID, "error", heErr)
		}
	}
	return nodeIDs, nil
}

// updateHotEdges maintains a bounded 20-slot hot_edges array on objectNodeID.
// The new relationship node is assigned relevance_score = 1.0.
// When the array is full, the existing edge with the lowest relevance_score is evicted.
func updateHotEdges(ctx context.Context, app *infra.App, objectNodeID, newRelationshipID string) error {
	client, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("updateHotEdges: firestore client: %w", err)
	}
	col := client.Collection(memory.KnowledgeCollection)

	// Set the new relationship's relevance_score to 1.0 (freshly observed).
	if _, updErr := col.Doc(newRelationshipID).Update(ctx, []firestore.Update{
		{Path: "relevance_score", Value: 1.0},
	}); updErr != nil {
		infra.LoggerFrom(ctx).Warn("updateHotEdges: failed to set new rel score", "rel_id", newRelationshipID, "error", updErr)
	}

	// Fetch the object node's current hot_edges.
	objDoc, err := col.Doc(objectNodeID).Get(ctx)
	if err != nil {
		return fmt.Errorf("updateHotEdges: fetch object node: %w", err)
	}
	data := objDoc.Data()
	var hotEdges []string
	if v, ok := data["hot_edges"].([]any); ok {
		hotEdges = make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				hotEdges = append(hotEdges, s)
			}
		}
	}

	if len(hotEdges) < maxHotEdges {
		// Slot available — append and write.
		hotEdges = append(hotEdges, newRelationshipID)
		_, err = col.Doc(objectNodeID).Update(ctx, []firestore.Update{
			{Path: "hot_edges", Value: hotEdges},
		})
		return err
	}

	// Array full — fetch relevance_scores of all existing edges and evict the lowest.
	type edgeScore struct {
		id    string
		score float64
	}
	scores := make([]edgeScore, 0, len(hotEdges))
	for _, edgeID := range hotEdges {
		edgeDoc, edgeErr := col.Doc(edgeID).Get(ctx)
		if edgeErr != nil {
			// Treat unfetchable edges as score 0 (safe to evict).
			scores = append(scores, edgeScore{id: edgeID, score: 0})
			infra.LoggerFrom(ctx).Warn("updateHotEdges: fetch edge score failed, treating as 0", "edge_id", edgeID, "error", edgeErr)
			continue
		}
		var score float64
		if v, ok := edgeDoc.Data()["relevance_score"].(float64); ok {
			score = v
		}
		scores = append(scores, edgeScore{id: edgeID, score: score})
	}

	// Find lowest-scored edge index.
	lowestIdx := 0
	for i, s := range scores {
		if s.score < scores[lowestIdx].score {
			lowestIdx = i
		}
	}
	infra.LoggerFrom(ctx).Info("updateHotEdges: evicting low-score edge",
		"object_node_id", objectNodeID,
		"evicted_edge_id", scores[lowestIdx].id,
		"evicted_score", scores[lowestIdx].score,
	)
	hotEdges[lowestIdx] = newRelationshipID
	_, err = col.Doc(objectNodeID).Update(ctx, []firestore.Update{
		{Path: "hot_edges", Value: hotEdges},
	})
	return err
}

// hasPronounDrops reports whether any triple was rejected due to an unknown pronoun subject/object.
func hasPronounDrops(triples []refineryTriple) bool {
	for _, t := range triples {
		if t.ParseErr == "pronoun subject or object rejected (owner identity unknown)" {
			return true
		}
	}
	return false
}

// pronounSet contains first- and second-person pronouns that should never
// become knowledge graph nodes. Entries are lowercase for case-insensitive matching.
var pronounSet = map[string]bool{
	"i": true, "me": true, "my": true, "myself": true, "mine": true,
	"we": true, "us": true, "our": true, "ours": true, "ourselves": true,
	"you": true, "your": true, "yours": true, "yourself": true, "yourselves": true,
}

// resolvePronoun replaces a bare first/second-person pronoun with ownerName when
// the identity is known, or returns ("", true) to signal the triple should be dropped.
func resolvePronoun(term, ownerName string) (resolved string, drop bool) {
	if !pronounSet[strings.ToLower(term)] {
		return term, false
	}
	if ownerName != "" {
		return ownerName, false
	}
	return "", true
}

func parseRefineryTriples(lines []string, ownerName string) []refineryTriple {
	out := make([]refineryTriple, 0, len(lines))
	for _, line := range lines {
		rawLine := strings.TrimSpace(line)
		if rawLine == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) != 5 && len(parts) != 3 {
			out = append(out, refineryTriple{
				RawLine:  rawLine,
				ParseErr: fmt.Sprintf("expected 3 or 5 pipe-separated fields, got %d", len(parts)),
			})
			continue
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		sub := parts[0]
		pred := parts[1]
		obj := parts[2]
		if sub == "" || pred == "" || obj == "" {
			out = append(out, refineryTriple{
				Subject:   sub,
				Predicate: pred,
				Object:    obj,
				RawLine:   rawLine,
				ParseErr:  "subject, predicate, and object must all be non-empty",
			})
			continue
		}
		// Resolve or drop pronoun subjects/objects.
		resolvedSub, dropSub := resolvePronoun(sub, ownerName)
		resolvedObj, dropObj := resolvePronoun(obj, ownerName)
		if dropSub || dropObj {
			out = append(out, refineryTriple{
				Subject:   sub,
				Predicate: pred,
				Object:    obj,
				RawLine:   rawLine,
				ParseErr:  "pronoun subject or object rejected (owner identity unknown)",
			})
			continue
		}
		sub = resolvedSub
		obj = resolvedObj
		subType := memory.NodeTypeGeneric
		objType := memory.NodeTypeGeneric
		if len(parts) == 5 {
			subType = memory.CanonicalEntityNodeType(parts[3])
			objType = memory.CanonicalEntityNodeType(parts[4])
		}
		out = append(out, refineryTriple{
			Subject:   sub,
			SubType:   subType,
			Predicate: pred,
			Object:    obj,
			ObjType:   objType,
			RawLine:   rawLine,
		})
	}
	return out
}
