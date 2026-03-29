package memory

import (
	"io"
	"log/slog"

	"cloud.google.com/go/firestore"
)

// Store is the memory layer for jot's GraphRAG system.
// Construct with New(); all methods require a context.
type Store struct {
	db       *firestore.Client
	embedder Embedder
	llm      LLMDispatcher
	log      *slog.Logger
}

// Option configures a Store at construction time.
type Option func(*Store)

// WithLogger sets a structured logger. If not provided, logs are discarded.
func WithLogger(l *slog.Logger) Option {
	return func(s *Store) { s.log = l }
}

// Embedder returns the embedder used by this store.
func (s *Store) Embedder() Embedder {
	return s.embedder
}

// New creates a Store with the given Firestore client, embedder, and LLM dispatcher.
func New(db *firestore.Client, embedder Embedder, llm LLMDispatcher, opts ...Option) *Store {
	s := &Store{
		db:       db,
		embedder: embedder,
		llm:      llm,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}
