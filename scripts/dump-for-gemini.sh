#!/usr/bin/env bash
# dump-for-gemini.sh — Output all code, scripts, and embedded prompts in a Gemini-parseable format.
# Usage: ./scripts/dump-for-gemini.sh [--ignore-static] [--compact] [repo_root]
#   --ignore-static  omit all embedded assets: .txt, .html, .json, .css, .svg, .yml, .yaml (and internal/static/)
#   --compact        shorter delimiters (--- path ---) and collapse blank lines / trim trailing space (fewer tokens)
# Output: Default = "## path" + fenced code block. With --compact = "--- path ---" + compacted content.

set -euo pipefail

IGNORE_STATIC=0
COMPACT=0
ROOT=""
for arg in "$@"; do
  case "$arg" in
    --ignore-static) IGNORE_STATIC=1 ;;
    --compact)       COMPACT=1 ;;
    *)               ROOT="$arg" ;;
  esac
done
ROOT="${ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
cd "$ROOT"

# Directories and files to skip (aligned with .gitignore and safety)
SKIP_DIRS='\.git|/vendor/|/\.venv|__pycache__|/\.idea|/\.vscode|/bin/|/dist/|/tmp/|/temp/'
SKIP_FILES='\.env$|\.env\.|\.pem$|\.key$|credentials\.json|service-account.*\.json|\.db$|\.sqlite|\.log$|\.cover$|coverage\.|jot_local\.db'

# Extensions to include: code, scripts, config, and embedded content (prompts, HTML)
EXTS='\.go$|\.sh$|\.md$|\.txt$|\.html$|\.mod$|\.sum$'

echo "Prepare for me to ask questions about this codebase:"
echo "# JOT codebase dump for Gemini"
echo "# Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "# Root: $ROOT"
[[ "$IGNORE_STATIC" -eq 1 ]] && echo "# Options: --ignore-static (embedded assets omitted: txt, html, json, css, svg, yml, yaml, internal/static/)"
[[ "$COMPACT" -eq 1 ]] && echo "# Options: --compact (short delimiters, collapsed blanks, trimmed trailing space)"
echo ""

find . -type f \
  \( -name '*.go' -o -name '*.sh' -o -name '*.md' -o -name '*.txt' -o -name '*.html' -o -name 'go.mod' -o -name 'go.sum' \) \
  | grep -vE "$SKIP_DIRS" \
  | grep -vE "$SKIP_FILES" \
  | sort \
  | while IFS= read -r f; do
  # Skip binary/build paths that find might still include
  case "$f" in
    ./jot$|./jot-local$|./server$|./local$|./migrate_knowledge_metadata$|./clean-test-data$|./ca-certificates.crt) continue ;;
    */.drive_watch_channel) continue ;;
    */twilio.txt) continue ;;
    cmd/jot/jot-cli) continue ;;
    cmd/jot/jot-go) continue ;;
  esac
  path="${f#./}"
  if [[ "$IGNORE_STATIC" -eq 1 ]]; then
    [[ "$path" == internal/static/* ]] && continue
    case "$path" in
      *.txt|*.html|*.json|*.css|*.svg|*.yml|*.yaml) continue ;;
    esac
  fi
  if [[ ! -f "$path" ]]; then continue; fi
  if [[ "$COMPACT" -eq 1 ]]; then
    echo "--- $path ---"
    sed 's/[[:space:]]*$//' "$path" | cat -s
    echo ""
  else
    lang="text"
    case "$path" in
      *.go)   lang="go" ;;
      *.sh)   lang="shell" ;;
      *.md)   lang="markdown" ;;
      *.html) lang="html" ;;
      *.mod|*.sum) lang="plaintext" ;;
      *.txt)  lang="text" ;;
    esac
    echo ""
    echo "## $path"
    echo '```'"$lang"
    cat "$path"
    echo '```'
  fi
done
