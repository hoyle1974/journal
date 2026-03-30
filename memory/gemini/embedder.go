// Package gemini provides Gemini SDK implementations of memory.Embedder and memory.LLMDispatcher.
package gemini

import (
	"context"
	"fmt"

	"google.golang.org/genai"

	"github.com/jackstrohm/jot/memory"
)

const (
	embeddingModel     = "gemini-embedding-2-preview"
	embeddingDimension = int32(1536)
	embeddingBatchSize = 250
)

type embedder struct {
	client *genai.Client
}

// NewEmbedder returns a memory.Embedder backed by gemini-embedding-2-preview at 1536 dimensions.
// client is the existing genai.Client from infra.App (Gemini API key, BackendGeminiAPI).
func NewEmbedder(client *genai.Client) memory.Embedder {
	return &embedder{client: client}
}

func (e *embedder) GenerateEmbedding(ctx context.Context, text string, taskType string) ([]float32, error) {
	if taskType == "" {
		taskType = memory.EmbedTaskRetrievalQuery
	}
	return e.embedText(ctx, text, taskType)
}

func (e *embedder) GenerateEmbeddingsBatch(ctx context.Context, texts []string, taskType string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if taskType == "" {
		taskType = memory.EmbedTaskRetrievalQuery
	}
	out := make([][]float32, len(texts))
	for i := 0; i < len(texts); i += embeddingBatchSize {
		end := i + embeddingBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		chunk := texts[i:end]
		contents := make([]*genai.Content, len(chunk))
		for j, t := range chunk {
			contents[j] = &genai.Content{Parts: []*genai.Part{genai.NewPartFromText(t)}}
		}
		vecs, err := e.callBatch(ctx, contents, taskType)
		if err != nil {
			return nil, err
		}
		for j, v := range vecs {
			out[i+j] = v
		}
	}
	return out, nil
}

func (e *embedder) EmbedContent(ctx context.Context, parts []memory.EmbedPart) ([]float32, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("EmbedContent: at least one part required")
	}
	if e.client == nil {
		return nil, fmt.Errorf("EmbedContent: gemini client is nil")
	}
	gParts := make([]*genai.Part, 0, len(parts))
	for _, p := range parts {
		if len(p.Bytes) > 0 {
			gParts = append(gParts, genai.NewPartFromBytes(p.Bytes, p.MIMEType))
		} else if p.Text != "" {
			gParts = append(gParts, genai.NewPartFromText(p.Text))
		}
	}
	if len(gParts) == 0 {
		return nil, fmt.Errorf("EmbedContent: no non-empty parts provided")
	}
	dim := embeddingDimension
	cfg := &genai.EmbedContentConfig{OutputDimensionality: &dim}
	resp, err := e.client.Models.EmbedContent(ctx, embeddingModel,
		[]*genai.Content{{Parts: gParts}},
		cfg,
	)
	if err != nil {
		return nil, fmt.Errorf("EmbedContent: %w", err)
	}
	if len(resp.Embeddings) == 0 || len(resp.Embeddings[0].Values) == 0 {
		return nil, fmt.Errorf("EmbedContent: no embedding returned")
	}
	return resp.Embeddings[0].Values, nil
}

// embedText embeds a single text string with the given task type.
func (e *embedder) embedText(ctx context.Context, text string, taskType string) ([]float32, error) {
	if e.client == nil {
		return nil, fmt.Errorf("GenerateEmbedding: gemini client is nil")
	}
	contents := []*genai.Content{{Parts: []*genai.Part{genai.NewPartFromText(text)}}}
	vecs, err := e.callBatch(ctx, contents, taskType)
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return vecs[0], nil
}

// callBatch sends a batch of contents to the embedding model and returns their vectors.
func (e *embedder) callBatch(ctx context.Context, contents []*genai.Content, taskType string) ([][]float32, error) {
	dim := embeddingDimension
	cfg := &genai.EmbedContentConfig{
		TaskType:             taskType,
		OutputDimensionality: &dim,
	}
	resp, err := e.client.Models.EmbedContent(ctx, embeddingModel, contents, cfg)
	if err != nil {
		return nil, fmt.Errorf("embedding API: %w", err)
	}
	if len(resp.Embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	out := make([][]float32, len(resp.Embeddings))
	for i, emb := range resp.Embeddings {
		out[i] = emb.Values
	}
	return out, nil
}
