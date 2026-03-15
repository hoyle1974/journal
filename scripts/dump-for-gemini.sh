#!/usr/bin/env bash

gitingest . --output /tmp/digest.txt --exclude-pattern ".gitkeep" --exclude-pattern "go.mod"  2>/dev/null 
cat /tmp/digest.txt
