#!/usr/bin/env bash
set -euo pipefail
producer_binary=$1
artifact_dir=$2
publication_root=$(git rev-parse --show-toplevel)
producer_binary=$(realpath "$producer_binary")
test "$(sha256sum "$producer_binary" | cut -d ' ' -f1)" = bddb8e23b04acf6db660c5217d7472405c1898b80b20853fbf12c7e5762c19ff
mkdir -p "$artifact_dir"
artifact_dir=$(cd "$artifact_dir" && pwd)
git worktree add --detach "$artifact_dir/subject-source" 23c5bc2e4a5993ffe092d04b5ad1dcf771af0269
git worktree add --detach "$artifact_dir/baseline-source" 1506551e149750e34dc5239368683577df0deda9
git worktree add --detach "$artifact_dir/candidate-source" 1506551e149750e34dc5239368683577df0deda9
git -C "$artifact_dir/candidate-source" apply "$publication_root/docs/benchmarks/record-pair-returns-2026-10-27/source-change.patch"
subject_root="$artifact_dir/subject-source"
for version in baseline candidate
do
  source_root="$artifact_dir/$version-source"
  "$producer_binary" -O -target x86-64-linux -o "$artifact_dir/$version-stage2" "$source_root/compiler/fern.fern" "$subject_root/internal/stdlib" > "$artifact_dir/$version-build.log" 2>&1
  "$producer_binary" -O -g -target x86-64-linux -o "$artifact_dir/$version-stage2-g" "$source_root/compiler/fern.fern" "$subject_root/internal/stdlib" > "$artifact_dir/$version-debug-build.log" 2>&1
  nm -n "$artifact_dir/$version-stage2-g" > "$artifact_dir/$version-stage2-g.nm"
  export FERN_RC_CERTIFY_MEMO="$artifact_dir/memo-$version"
  "$artifact_dir/$version-stage2" -O -target x86-64-linux -emit asm -o "$artifact_dir/$version.s" "$subject_root/compiler/checker.fern" "$subject_root/internal/stdlib" > "$artifact_dir/$version-warm.log" 2>&1
  valgrind --tool=callgrind --callgrind-out-file="$artifact_dir/$version.callgrind" "$artifact_dir/$version-stage2" -O -target x86-64-linux -emit asm -o /dev/null "$subject_root/compiler/checker.fern" "$subject_root/internal/stdlib" > "$artifact_dir/$version-profile.log" 2>&1
done
cmp "$producer_binary" "$artifact_dir/baseline-stage2"
sha256sum "$producer_binary" "$artifact_dir/baseline-stage2" "$artifact_dir/candidate-stage2" "$artifact_dir/baseline.s" "$artifact_dir/candidate.s" > "$artifact_dir/hashes.txt"
git -C "$artifact_dir/candidate-source" diff HEAD -- compiler > "$artifact_dir/candidate-uncommitted.patch"
