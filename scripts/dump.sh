#!/bin/bash

NO_TOOLS=0
for arg in "$@"; do
    case "$arg" in
        --no-tools) NO_TOOLS=1 ;;
    esac
done

# Define specific files and patterns to exclude based on previous analysis
# - go.sum: Massive checksum noise 
# - briefs/done/: Historical records
# - credentials-dev.json: Sensitive keys [cite: 46]
# - firestore.indexes.json: Verbose infrastructure [cite: 49-53]
# - internal/static/: Legal boilerplate [cite: 12]
# - *_test.go: Optional - remove if you don't need test logic analysis [cite: 6-7, 13]
EXCLUDE_PATTERNS=(
    "go.sum"
    "go.mod"              # New: dependency manifest
    "briefs/done/"
    "credentials-dev.json"
    "firestore.indexes.json"
    "internal/static/"
    "cmd/admin/"
    "twilio.txt"
    "docs/telegram-setup.md" # New: one-time setup guide
    "docs/superpowers/plans/" # Executed implementation plans — historical, not needed for context
    "_test.go"            # New: prunes all test files for leaner logic
)
# Function to check if a file should be excluded
is_excluded() {
    local file=$1
    if [ "$NO_TOOLS" -eq 1 ]; then
        [[ "$file" == internal/tools/* ]] && return 0
        [[ "$file" == tools/* ]] && return 0
    fi
    for pattern in "${EXCLUDE_PATTERNS[@]}"; do
        if [[ "$file" == *"$pattern"* ]]; then
            return 0 # True, it is excluded
        fi
    done
    return 1 # False, keep it
}

# 1. Use git ls-files to honor .gitignore
# 2. Filter out the specific "verbose" files mentioned
# 3. Output in a clear format for Gemini
git ls-files | while read -r file; do
    if [ -f "$file" ] && ! is_excluded "$file"; then
        echo "================================================"
        echo "FILE: $file"
        echo "================================================"
        cat "$file"
        echo -e "\n"
    fi
done
