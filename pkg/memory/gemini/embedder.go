// Package gemini provides Gemini/Vertex AI implementations of memory.Embedder and memory.LLMDispatcher.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2/google"

	"github.com/jackstrohm/jot/pkg/memory"
)

const embeddingBatchSize = 250

type embedder struct {
	projectID string
}

// NewEmbedder returns a memory.Embedder backed by Vertex AI text-embedding-005.
// projectID is the GCP project that hosts the Vertex AI endpoint.
func NewEmbedder(projectID string) memory.Embedder {
	return &embedder{projectID: projectID}
}

func (e *embedder) GenerateEmbedding(ctx context.Context, text string, taskType string) ([]float32, error) {
	if taskType == "" {
		taskType = memory.EmbedTaskRetrievalQuery
	}
	results, err := e.batch(ctx, []string{text}, taskType)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return results[0], nil
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
		vecs, err := e.batch(ctx, chunk, taskType)
		if err != nil {
			return nil, err
		}
		for j, v := range vecs {
			out[i+j] = v
		}
	}
	return out, nil
}

func (e *embedder) batch(ctx context.Context, texts []string, taskType string) ([][]float32, error) {
	endpoint := fmt.Sprintf(
		"https://us-central1-aiplatform.googleapis.com/v1/projects/%s/locations/us-central1/publishers/google/models/text-embedding-005:predict",
		e.projectID,
	)
	instances := make([]map[string]any, len(texts))
	for i, t := range texts {
		instances[i] = map[string]any{"content": t, "task_type": taskType}
	}
	body, err := json.Marshal(map[string]any{"instances": instances})
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	tokenSource, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("get token source: %w", err)
	}
	token, err := tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding API request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embedding API %d: %s", resp.StatusCode, b)
	}

	var result struct {
		Predictions []struct {
			Embeddings struct {
				Values []float32 `json:"values"`
			} `json:"embeddings"`
		} `json:"predictions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	out := make([][]float32, len(result.Predictions))
	for i, p := range result.Predictions {
		out[i] = p.Embeddings.Values
	}
	return out, nil
}
