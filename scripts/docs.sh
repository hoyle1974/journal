#!/bin/bash

echo "Generate a new brief for this project to update the docs. Use briefs/TEMPLATE.md to guide you."
echo "All txt and md files in this project should be analyzed for correctness and then detailed instructions "
echo "for updating the various files to correctly reflect what is in code should be laid out in the brief"
gitingest . --output /tmp/digest.txt --exclude-pattern "briefs/done/*" --exclude-pattern ".gitkeep" --exclude-pattern "go.sum" --exclude-pattern "go.mod"  2>/dev/null 
cat /tmp/digest.txt
