package memory

import "context"

// EmbedPart is a single piece of content for embedding — either text or binary.
// Set Text for text input. Set Bytes + MIMEType for binary (image, audio, etc.).
type EmbedPart struct {
	Text     string // set for text input
	Bytes    []byte // set for binary input
	MIMEType string // required when Bytes is set
}

// Embedder generates vector embeddings for semantic search.
type Embedder interface {
	GenerateEmbedding(ctx context.Context, text string, taskType string) ([]float32, error)
	GenerateEmbeddingsBatch(ctx context.Context, texts []string, taskType string) ([][]float32, error)
	// EmbedContent embeds one or more content parts (text or binary) into a single vector.
	// Use for multimodal inputs such as images and audio.
	EmbedContent(ctx context.Context, parts []EmbedPart) ([]float32, error)
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
