// Package timeout defines shared timeout values for CLI and server.
package timeout

// QuerySeconds is the request timeout for query and plan endpoints.
// Used by: CLI (client timeout), deploy.sh (Cloud Run --timeout).
const QuerySeconds = 300

// DreamSeconds is the client timeout for POST /dream. The dream pipeline
// (colloquium, extraction, consolidation, synthesis) often takes 3–5+ minutes.
const DreamSeconds = 600
