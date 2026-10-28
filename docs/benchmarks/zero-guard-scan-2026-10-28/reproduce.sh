#!/usr/bin/env bash
set -euo pipefail
producer_binary=$(realpath "$1")
artifact_dir=$2
publication_root=$(git rev-parse --show-toplevel)
test "$(sha256sum "$producer_binary" | cut -d ' ' -f1)" = 64f876ff80e4db91d1eb614605a7fe567bbad13ecb061dce204defb5c0b975a0
mkdir -p "$artifact_dir"
artifact_dir=$(cd "$artifact_dir" && pwd)
git worktree add --detach "$artifact_dir/subject-source" 23c5bc2e4a5993ffe092d04b5ad1dcf771af0269
git worktree add --detach "$artifact_dir/baseline-source" b0958981729ecb84c9fc91b1f49e2ed7ff1171ec
git worktree add --detach "$artifact_dir/candidate-source" b0958981729ecb84c9fc91b1f49e2ed7ff1171ec
git -C "$artifact_dir/candidate-source" apply "$publication_root/docs/benchmarks/zero-guard-scan-2026-10-28/source-change.patch"
subject_root="$artifact_dir/subject-source"
for version in baseline candidate
do
  source_root="$artifact_dir/$version-source"
  "$producer_binary" -O -target x86-64-linux -o "$artifact_dir/$version-stage2" "$source_root/compiler/fern.fern" "$subject_root/internal/stdlib" > "$artifact_dir/$version-build.log" 2>&1
  "$producer_binary" -O -g -target x86-64-linux -o "$artifact_dir/$version-stage2-g" "$source_root/compiler/fern.fern" "$subject_root/internal/stdlib" > "$artifact_dir/$version-debug-build.log" 2>&1
  nm -n "$artifact_dir/$version-stage2-g" > "$artifact_dir/$version-stage2-g.nm"
  export FERN_RC_CERTIFY_MEMO="$artifact_dir/memo-$version"
  for target in x86-64-linux arm64-linux wasm32-wasi
  do
    "$artifact_dir/$version-stage2" -O -target "$target" -emit asm -o "$artifact_dir/$version-$target.s" "$subject_root/compiler/checker.fern" "$subject_root/internal/stdlib" > "$artifact_dir/$version-$target-output.log" 2>&1
  done
  valgrind --tool=callgrind --callgrind-out-file="$artifact_dir/$version.callgrind" "$artifact_dir/$version-stage2" -O -target x86-64-linux -emit asm -o /dev/null "$subject_root/compiler/checker.fern" "$subject_root/internal/stdlib" > "$artifact_dir/$version-profile.log" 2>&1
done
cmp "$producer_binary" "$artifact_dir/baseline-stage2"
for target in x86-64-linux arm64-linux wasm32-wasi
do
  cmp "$artifact_dir/baseline-$target.s" "$artifact_dir/candidate-$target.s"
done
sha256sum "$producer_binary" "$artifact_dir/baseline-stage2" "$artifact_dir/candidate-stage2" > "$artifact_dir/hashes.txt"
