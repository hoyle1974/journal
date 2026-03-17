# Brief: Refactoring Tool Registration via Go Reflection

**Date:** 20250316
**Status:** done
**Branch:** `feature/refactor-tool-registration-reflection` (merged to main)
**Worktree:** removed

---

## Goal

Reduce boilerplate and prevent schema–code mismatch by generating Gemini tool definitions (JSON Schema) from Go structs using reflection and struct tags. The Go Args struct becomes the single source of truth: one definition drives both the LLM schema and the execution payload.

---

## Scope

**In:**
- New `Tool` shape: `Args` is a pointer to a struct (prototype); `Execute(ctx, env, args interface{}) Result` receives the concrete typed struct.
- Reflection helper (in `internal/infra` or `tools`) to convert a struct to `*genai.Schema` using `json`, `description`, `required`, `default`, and (where feasible) `min`/`max`/`enum` tags.
- Refactor all tools in `internal/tools/impl/*.go` to use per-tool Args structs and remove all `Params: []Param{...}` and `tools.Args` usage in Execute.
- Remove `Param` struct and `Params` field from `tools/types.go`; remove or repurpose `tools/params.go` (CountParam, LimitParam, etc.) in favor of struct-tag-driven schema.
- Registry: `toolToDeclaration` builds schema from `Tool.Args` via reflection; `Execute(ctx, env, name, arguments map[string]interface{})` unmarshals the map into the tool’s Args type and calls `Execute` with the typed struct.

**Out:**
- Changing FOH/agent loop behavior (only how tools are defined and invoked).
- Adding new tools or changing `app_capabilities.txt` content (unless tool names/params change).

---

## Approach & Key Decisions

1. **Tool type**
   - `Tool.Args` holds a pointer to a struct (e.g. `&WeatherArgs{}`). Reflection is done on `reflect.TypeOf(tool.Args).Elem()`.
   - `Execute func(ctx context.Context, env infra.ToolEnv, args interface{}) Result` — implementers type-assert `args.(*WeatherArgs)` (or use a generic helper if we introduce generics for type safety where possible).

2. **Struct tags**
   - `json:"field_name"` — name in schema and in LLM payload.
   - `description:"..."` — schema description.
   - `required:"true"` — include in `required` array.
   - `default:"..."` — document in description and/or apply when unmarshaling if absent (genai.Schema does not support default in schema; prompt or post-fill).
   - For `min`/`max`/`enum`: support via tags (e.g. `min:"1"`, `max:"50"`, `enum:"a,b,c"`) and map to genai.Schema where supported (Enum is; min/max are not in genai.Schema — document in description and enforce in code).

3. **Invocation path**
   - Today: `registry.Execute(ctx, env, name, arguments map[string]interface{})` → `NewArgs(arguments)` → `tool.Execute(ctx, env, args *Args)`.
   - After: same entry point; registry looks up tool, gets Args type from `tool.Args`, unmarshals `arguments` into a new instance of that type (e.g. `mapstructure` or round-trip via JSON), then calls `tool.Execute(ctx, env, typedArgs)`.

4. **Backward compatibility**
   - No API change for callers of `tools.Execute` or `tools.GetDefinitions`; only the internal Tool shape and impls change.

5. **Optional: keep `tools.Args` for transition**
   - Brief assumes we remove `*Args` from Execute and use only typed structs. If we need a hybrid during migration, we could support both "legacy" tools (Params + Args) and "reflected" tools (Args struct only) until all tools are migrated, then remove legacy path.

---

## Edge Cases & Pre-Flight Checks

1. **genai.Schema limits**: genai.Schema does not support `min`/`max`/`pattern`/`maxLength`. For count/limit params we must document constraints in the field description and enforce in Execute (e.g. clamp or validate). Enum is supported.

2. **Unmarshal from map**: LLM returns `map[string]interface{}`; JSON numbers are float64. Ensure reflection/unmarshal handles float64→int for integer fields and that required validation happens after unmarshal (or via a validate method).

3. **Nil Args**: Tools with no parameters (e.g. `get_latest_dream`) currently have `Params: nil`. In the new model, use an empty struct `struct{}{}` or a dedicated `NoArgs struct{}` so every tool has an Args type and schema is `type: object, properties: {}`.

---

## Affected Areas

- [x] Tools — register via `tools.Register()` in `init()`, co-locate by domain
- [ ] Agent / FOH loop — no change to loop; only tool definition and registry internals
- [ ] Prompts / `app_capabilities.txt` — update only if tool names or parameter semantics change
- [ ] Firestore schema or queries
- [ ] New dependencies / infra clients — reflection is stdlib; optional `mapstructure` for map→struct
- [ ] API routes or cron jobs
- [ ] Memory / journal behavior

---

## Open Questions

- [x] Prefer reflection in `tools` package vs `internal/infra` (e.g. `tool_adapter.go`). **Decision:** keep schema generation in `tools` so the package stays self-contained; infra only consumes `*genai.FunctionDeclaration`.
- [x] Use `encoding/json` for map→struct (after converting map to JSON bytes) or `github.com/mitchellh/mapstructure` for direct map→struct. **Decision:** JSON round-trip used; handles float64→int and keeps deps minimal.

---

## Checklist

**Implementation**
- [x] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [x] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [x] Debug logs pass full strings — no truncation at Debug level
- [x] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [x] LLM output parsed as key/value lines via `pkg/utils.ParseKeyValueMap` (no JSON)
- [x] Every significant agentic step has `StartSpan` / `defer span.End()`
- [x] Errors wrapped with `%w`, not `%v`
- [x] No file exceeds 400 lines

**Firestore (if applicable)**  
N/A

**Verification (Proof of Work)**
- [x] **Compilation:** `go build ./...` passes cleanly.
- [x] **Tests:** `go test ./...` passes.
- [x] **Lint/Format:** Code is formatted and passes `go vet`.
- [x] **Manual Smoke Test:** Behavior unchanged; tool names and parameter semantics unchanged so no app_capabilities change.

**Wrap-up**
- [x] `app_capabilities.txt` updated if capabilities or parameter semantics changed (no change needed)
- [x] `blueprint.md` consulted if core agentic loop was touched (not touched)
- [x] Tests added/updated for reflection schema and registry Execute path (TestStructToGenaiSchema)
- [x] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/done/20250316_refactor_tool_registration_reflection.md` (this file)
- `tools/types.go` — Tool struct, Param removed, Args + Execute(ctx, env, args any)
- `tools/registry.go` — toolToDeclaration from reflection; Execute unmarshal map→struct
- `tools/schema.go` — StructToGenaiSchema, MapToTypedArgs, ParamInfosFromArgs, ParamNamesFromArgs, ApplyDefaults
- `tools/args.go` — kept for tests / backward compat
- `internal/tools/impl/*.go` — per-tool Args structs and typed Execute

---

## Session Log

<!-- YYYYMMDD -->
- 20250316: Implemented reflection-based tool registration. Added tools/schema.go (StructToGenaiSchema, MapToTypedArgs, ParamInfosFromArgs, ParamNamesFromArgs, ApplyDefaults). Updated tools/types.go: removed Param, Tool.Args is any (pointer to struct), Execute(ctx, env, args any). Updated tools/registry.go: toolToDeclaration uses StructToGenaiSchema(tool.Args); Execute unmarshals map→typed struct and calls tool.Execute; SearchRegistry/FormatDiscoveryResultFull use ParamNamesFromArgs/ParamInfosFromArgs. Deleted tools/params.go. Refactored all tools in internal/tools/impl to use per-tool Args structs and typed Execute. Replaced TestParamHelpers with TestStructToGenaiSchema. go build ./... and go test ./... pass.
- 20250316: Closeout: committed on feature branch, merged to main, removed worktree, moved brief to briefs/done/.
