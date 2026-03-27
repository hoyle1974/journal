package api

import (
	"errors"
	"net/http"
)

// HandlerError is a sentinel error type for non-500 HTTP errors returned by APIHandlers.
// Use handlerError to construct one; wrapAPI maps it to the given HTTP status code.
type HandlerError struct {
	Code    int
	Message string
}

func (e *HandlerError) Error() string { return e.Message }

// handlerError returns a *HandlerError with the given HTTP status code and message.
func handlerError(code int, msg string) error {
	return &HandlerError{Code: code, Message: msg}
}

// APIHandler is the canonical signature for wrapped API handlers.
// Return (data, nil) on success — data is encoded as JSON with 200.
// Return (nil, err) on failure — use handlerError for specific HTTP codes, plain errors for 500.
type APIHandler func(s *Server, w http.ResponseWriter, r *http.Request) (any, error)

// wrapAPI converts an APIHandler into an http.HandlerFunc, centralising:
//   - Server extraction from context
//   - Initial LogHandlerRequest
//   - Error classification (HandlerError → specific code, other → 500)
//   - LogHandlerResponse + WriteJSON for both success and error paths
//
// Handlers own: DecodeAndValidate, business logic, and any mid-handler
// LogHandlerRequest calls for extra request-specific attributes.
func wrapAPI(h APIHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := ServerFromContext(r.Context())
		if s == nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		ctx := r.Context()
		path := pathForLog(r.URL.Path)
		LogHandlerRequest(ctx, r.Method, path)

		data, err := h(s, w, r)
		if err != nil {
			var he *HandlerError
			code := http.StatusInternalServerError
			if errors.As(err, &he) {
				code = he.Code
			}
			LogHandlerResponse(ctx, r.Method, path, code, "error", err.Error())
			WriteJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		LogHandlerResponse(ctx, r.Method, path, http.StatusOK)
		WriteJSON(w, http.StatusOK, data)
	}
}
