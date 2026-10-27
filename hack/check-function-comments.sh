#!/usr/bin/env bash
set -euo pipefail

status=0
while IFS= read -r -d '' file; do
  if ! awk '
    /^func / && previous !~ /^\/\/ / {
      printf "%s:%d: function declaration is missing a documentation comment\n", FILENAME, NR
      failed = 1
    }
    { previous = $0 }
    END { exit failed }
  ' "$file"; then
    status=1
  fi
done < <(find . -type f -name '*.go' -not -path './vendor/*' -print0)

exit "$status"
