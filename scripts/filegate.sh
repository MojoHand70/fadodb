#!/usr/bin/env bash
# File gate: no tracked file is larger than the limit. The repository
# holds source, documents and test data, the largest of them under
# 100 KB. A build output committed by mistake stays in the history, and
# so in every clone, for good.
set -euo pipefail
cd "$(dirname "$0")/.."

LIMIT_KB="${FILE_LIMIT_KB:-1024}"
limit=$((LIMIT_KB * 1024))
bad=0
while IFS= read -r -d '' file; do
  # A file removed in the working tree and not yet committed.
  [ -f "$file" ] || continue
  bytes="$(wc -c < "$file")"
  if [ "$bytes" -gt "$limit" ]; then
    printf 'filegate: %s is %d bytes; a tracked file is at most %d KB\n' "$file" "$bytes" "$LIMIT_KB"
    bad=1
  fi
done < <(git ls-files -z)

if [ "$bad" -eq 0 ]; then
  echo "filegate: ok"
else
  echo "filegate: a build output belongs in bin/ or outside the repository; a large fixture needs the limit raised on purpose"
fi
exit "$bad"
