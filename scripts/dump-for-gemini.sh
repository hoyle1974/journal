#!/usr/bin/env bash

gitingest . --output /tmp/digest.txt --exclude-pattern "ca-certificates.crt" --include-pattern "**" --exclude-pattern "**/go.sum" --exclude-pattern ".venv-drive-watch" --include-gitignored  2>/dev/null  -- exclude-pattern "briefs/done/**" --exclude-pattern "./credentials-dev.json" --exclude-pattern "**/*_test.go" --exclude-pattern "internal/static" --exclude-pattern "cmd/admin/**"
cat /tmp/digest.txt
