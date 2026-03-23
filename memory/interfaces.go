package memory

import "context"

// Embedder generates vector embeddings for semantic search.
type Embedder interface {
	GenerateEmbedding(ctx context.Context, text string, taskType string) ([]float32, error)
	GenerateEmbeddingsBatch(ctx context.Context, texts []string, taskType string) ([][]float32, error)
}

// LLMDispatcher makes single-shot LLM calls and returns the text response.
type LLMDispatcher interface {
	Dispatch(ctx context.Context, req LLMRequest) (string, error)
}

// LLMRequest is a simple text-in/text-out LLM request.
// MaxTokens 0 means the dispatcher uses its default (8192).
type LLMRequest struct {
	SystemPrompt string
	UserPrompt   string
	MaxTokens    int
	JSONMode     bool
}

// Embedding task type constants (mirrors Vertex AI text-embedding-005 task types).
const (
	EmbedTaskRetrievalQuery    = "RETRIEVAL_QUERY"
	EmbedTaskRetrievalDocument = "RETRIEVAL_DOCUMENT"
)
