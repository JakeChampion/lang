#!/bin/sh
# Scratch CI watcher for PR 6864. Not committed.
#
# An empty / errored `gh pr checks` must NOT read as "no pending checks" —
# that is how the first version reported completion within a minute.
while true; do
  s=$(gh pr checks 6864 2>&1)
  n=$(echo "$s" | grep -c 'https://')
  if [ "$n" -lt 20 ]; then
    echo "WATCHER: only $n check rows, retrying"
    sleep 60
    continue
  fi
  echo "$s" | grep -E '	(fail|failure|cancel)'
  if ! echo "$s" | grep -q '	pending	'; then
    echo "ALL CHECKS CONCLUDED ($n rows)"
    exit 0
  fi
  sleep 60
done
