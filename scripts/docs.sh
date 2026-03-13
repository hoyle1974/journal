#!/bin/bash

echo "
Generate a new brief for this project using briefs/TEMPLATE.md as your template.

Analyze all documentation within this project for correctness.  Assume the code is the source of truth
and update docs accordingly.  We prefer docs are concise and exist in organized md files.

Produce detailed updates and instructions  with specific changes that need to occur.

While cleaning up docs also call out any duplicate code that should be consolidated.

"
gitingest . --output /tmp/digest.txt --exclude-pattern "briefs/done/*" --exclude-pattern ".gitkeep" --exclude-pattern "go.sum" --exclude-pattern "go.mod"  2>/dev/null 
cat /tmp/digest.txt
