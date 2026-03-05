package jot

// Handler logic lives in internal/api (router + handlers). This package keeps
// handlers_helpers.go (SubmitAsync, SubmitGDocLog, logToGDocSync, etc.) used by jot and api_backend.
// api_backend.go implements api.Backend and is the only jot code invoked by the API layer.
