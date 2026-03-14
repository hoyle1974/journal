Brief: Modernize Core Libraries (Chi, Cobra, Validator, Limiter, Dateparse)
Date: 20260314
Status: in-progress
Branch: feature/modernize-core-libs
Worktree: ../jot-modernize-core-libs

Goal
Replace custom-built infrastructure logic with robust, community-standard third-party libraries. This reduces codebase size, eliminates manual boilerplate (like input validation and routing), improves date-parsing reliability, and sets up a scalable CLI structure.

Scope
In:

Refactor HTTP routing and middleware using github.com/go-chi/chi/v5.

Replace the custom sync.Map rate limiter with github.com/ulule/limiter/v3 (using the in-memory store).

Implement struct-based payload validation using github.com/go-playground/validator/v10.

Standardize the cmd/jot CLI structure using github.com/spf13/cobra.

Refactor date parsing logic using github.com/araddon/dateparse.

Out:

Changes to the core LLM/Agentic ReAct loops.

Changes to the Firestore schema or vector search logic.

Redis integration for the rate limiter (we will stick to in-memory to match existing architecture).

Approach & Key Decisions
1. Dependencies

Execute: go get github.com/go-chi/chi/v5 github.com/ulule/limiter/v3 github.com/go-playground/validator/v10 github.com/spf13/cobra github.com/araddon/dateparse

2. HTTP Routing & Middleware (chi & ulule/limiter)

Target: internal/api/router.go and internal/api/ratelimit.go

Implementation Details:

Router: Replace the monolithic switch path in Router() with a chi.Mux.

Limiter: Since we have route-specific limits (rateLimitConfig), we cannot use a single global ulule/limiter middleware. We must create a factory that returns a Chi middleware for a specific rate:

Go
// internal/api/ratelimit.go
import (
    "github.com/ulule/limiter/v3"
    "github.com/ulule/limiter/v3/drivers/store/memory"
)

var store = memory.NewStore()

// RateLimitMiddleware creates a chi middleware for a specific requests-per-minute limit.
func RateLimitMiddleware(reqsPerMin int) func(http.Handler) http.Handler {
    rate := limiter.Rate{
        Period: 1 * time.Minute,
        Limit:  int64(reqsPerMin),
    }
    instance := limiter.New(store, rate)

    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ip := GetClientIP(r)
            context, err := instance.Get(r.Context(), ip)
            if err != nil {
                http.Error(w, "Internal Server Error", http.StatusInternalServerError)
                return
            }
            if context.Reached {
                WriteJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Rate limit exceeded. Please try again later."})
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
Router Setup Blueprint:

Go
// internal/api/router.go
func NewRouter(s *Server) *chi.Mux {
    r := chi.NewRouter()
    
    // Global Middleware
    r.Use(s.AppCtxMiddleware) // Attaches App to context
    r.Use(TraceMiddleware)
    
    // Public Routes
    r.Group(func(r chi.Router) {
        r.Get("/health", handleHealth)
        r.Get("/metrics", handleMetrics)
        r.Post("/webhook", handleWebhook)
        r.Post("/sms", handleSMS)
    })

    // Protected Routes
    r.Group(func(r chi.Router) {
        r.Use(AuthMiddleware(s.Config.JotAPIKey))
        
        // Route-specific rate limits
        r.With(RateLimitMiddleware(30)).Post("/query", handleQuery)
        r.With(RateLimitMiddleware(60)).Post("/log", handleLog)
        
        // Path params handled natively
        r.With(RateLimitMiddleware(60)).Post("/pending-questions/{id}/resolve", handlePendingQuestionResolve)
    })
    
    return r
}
3. Payload Validation (validator)

Target: internal/api/server.go and all handler_*.go files.

Implementation Details:

Add Validator *validator.Validate to the api.Server struct. Initialize it in api.NewServer using validator.New().

Create a generic decoder/validator helper to strip boilerplate out of handlers:

Go
// internal/api/helpers.go
func DecodeAndValidate(r *http.Request, v interface{}, validate *validator.Validate) error {
    if err := json.NewDecoder(r.Body).Decode(v); err != nil {
        return fmt.Errorf("invalid JSON: %w", err)
    }
    if err := validate.Struct(v); err != nil {
        return fmt.Errorf("validation failed: %w", err)
    }
    return nil
}
Handler Updates: Update structs in handlers (e.g., handleProcessEntry) to use tags like validate:"required" and replace the json.NewDecoder + manual if field == "" checks with DecodeAndValidate.

4. Natural Date Parsing (dateparse)

Target: pkg/utils/date.go (or wherever ResolveDateRange lives).

Implementation Details:

Replace manual format checking logic with dateparse.

Go
import "github.com/araddon/dateparse"

func parseFuzzyDate(expr string) (time.Time, error) {
    // Keep custom natural language logic for "yesterday", "today"
    if t, ok := handleNaturalLanguage(expr); ok {
        return t, nil
    }
    // Let dateparse handle all the YYYY-MM-DD, MM/DD/YYYY, RFC3339 variations
    return dateparse.ParseLocal(expr)
}
5. CLI Modernization (cobra)

Target: cmd/jot/main.go and new cmd/jot/cmd/ package.

Implementation Details:

Create cmd/jot/cmd/root.go for the base jot command.

Port existing subcommands into separate files (e.g., cmd/jot/cmd/query.go, cmd/jot/cmd/log.go).

Rip out the custom argument parsing in internal/tools/args.go if it was exclusively used for the CLI (Note: if args.go is used for LLM tool arguments, leave it alone and only replace the CLI flag parsing).

Example Cobra command:

Go
// cmd/jot/cmd/query.go
var queryCmd = &cobra.Command{
    Use:   "query [question]",
    Short: "Query your semantic memory",
    Args:  cobra.MinimumNArgs(1),
    Run: func(cmd *cobra.Command, args []string) {
        question := strings.Join(args, " ")
        // Execute existing query logic
    },
}
Edge Cases & Pre-Flight Checks
LLM Tool Args vs CLI Args: internal/tools/args.go is used by the LLM tool execution engine (tools.Args). DO NOT replace this with Cobra. Cobra is strictly for cmd/jot/.

Chi Context vs App Context: Ensure infra.GetApp(ctx) still works. The Chi middleware that injects the app into the request context must run before any handler logic.

Dateparse Timezones: dateparse.ParseLocal uses the server's local timezone. Ensure this aligns with the user's expected timezone for journal entry retrieval.

Affected Areas
[ ] Agent / FOH loop

[x] Tools — Date calculations use dateparse.

[ ] Prompts / app_capabilities.txt

[ ] Firestore schema or queries

[x] New dependencies / infra clients — validator.Validate, chi.Mux, limiter.

[x] API routes or cron jobs — Entire HTTP router and middleware chain.

[ ] Memory / journal behavior

Open Questions
[ ] None.

Checklist
Implementation

[ ] go mod tidy run with new dependencies.

[ ] api.Server updated to hold *validator.Validate and *chi.Mux.

[ ] RateLimitMiddleware factory implemented using ulule/limiter/v3.

[ ] router.go refactored to use chi.NewRouter() and group routes with middleware.

[ ] Custom CheckAuth and rate limit maps deleted.

[ ] DecodeAndValidate helper implemented.

[ ] All handler_*.go files updated to use validate tags and DecodeAndValidate.

[ ] parsePendingQuestionPath deleted; replaced by chi.URLParam.

[ ] dateparse.ParseLocal integrated into date resolution logic.

[ ] cmd/jot rebuilt using cobra.Command structure.

[ ] New code passes *infra.App explicitly.

[ ] All logging uses LoggerFrom(ctx).

[ ] Errors wrapped with %w.

Verification (Proof of Work)

[ ] Compilation: go build ./... passes cleanly.

[ ] Tests: go test ./... passes.

[ ] Lint/Format: Code is formatted and passes go vet.

[ ] Manual Smoke Test: Start local server. Hit a protected route without auth (expect 401). Hit with auth but empty payload (expect validator 400). Hit /pending-questions/123/resolve and verify chi.URLParam captures 123.

Wrap-up

[ ] app_capabilities.txt updated if capabilities changed.

[ ] blueprint.md consulted.

[ ] Tests added/updated for routing and validation.

[ ] Brief status set to done and moved to briefs/done/.

Key Files
briefs/active/20260314_modernize-core-libs.md
internal/api/router.go
internal/api/ratelimit.go
internal/api/server.go
internal/api/handler_entries.go
internal/api/handler_interact.go
internal/api/handler_tasks.go
internal/tools/impl/helpers.go
cmd/jot/main.go
cmd/jot/cmd/root.go

Session Log
20260314: Brief created with detailed architectural blueprints for Chi, Validator, Limiter, and Cobra. Ready for Cursor implementation.
