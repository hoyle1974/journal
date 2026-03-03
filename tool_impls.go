package jot

// Tool registration has been moved to internal/tools/impl.
// Entry points (cmd/local, cmd/server) must import that package for side effect:
//
//	_ "github.com/jackstrohm/jot/internal/tools/impl"
//
// so that init() in each domain file runs and registers tools with the global registry.
