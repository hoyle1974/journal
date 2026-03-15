#!/usr/bin/env bash

gitingest . --output /tmp/digest.txt --exclude-pattern "ca-certificates.crt" --include-pattern "**" --exclude-pattern ".gitkeep" --exclude-pattern "go.mod" --exclude-pattern ".venv-drive-watch" --include-gitignored  2>/dev/null 
cat /tmp/digest.txt
