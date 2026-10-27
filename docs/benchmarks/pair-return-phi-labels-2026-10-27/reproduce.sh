#!/usr/bin/env bash
set -euo pipefail
stage0_binary=$1
artifact_dir=$2
candidate_root=$(git rev-parse --show-toplevel)
mkdir -p "$artifact_dir"
artifact_dir=$(cd "$artifact_dir" && pwd)
git worktree add --detach "$artifact_dir/producer-source" 23c5bc2e4a5993ffe092d04b5ad1dcf771af0269
git worktree add --detach "$artifact_dir/baseline-source" 6a90987759ae910877e7816c2645de6e1b9b3390
git worktree add --detach "$artifact_dir/candidate-source" fa5546746c4801c29d2670b175b192715895686d
git -C "$artifact_dir/candidate-source" apply "$candidate_root/docs/benchmarks/pair-return-phi-labels-2026-10-27/source-change.patch"
producer_root="$artifact_dir/producer-source"
baseline_root="$artifact_dir/baseline-source"
frozen_candidate_root="$artifact_dir/candidate-source"
"$stage0_binary" -O -target x86-64-linux -o "$artifact_dir/stage1" "$producer_root/compiler/fern.fern" "$producer_root/internal/stdlib" > "$artifact_dir/stage1-build.log" 2>&1
"$artifact_dir/stage1" -O -target x86-64-linux -o "$artifact_dir/baseline-stage2" "$baseline_root/compiler/fern.fern" "$producer_root/internal/stdlib" > "$artifact_dir/baseline-build.log" 2>&1
"$artifact_dir/stage1" -O -target x86-64-linux -o "$artifact_dir/candidate-stage2" "$frozen_candidate_root/compiler/fern.fern" "$producer_root/internal/stdlib" > "$artifact_dir/candidate-build.log" 2>&1
for version in baseline candidate
do
  export FERN_RC_CERTIFY_MEMO="$artifact_dir/memo-$version"
  "$artifact_dir/$version-stage2" -O -target x86-64-linux -emit asm -o "$artifact_dir/$version.s" "$producer_root/compiler/checker.fern" "$producer_root/internal/stdlib" > "$artifact_dir/$version-warm.log" 2>&1
  valgrind --tool=callgrind --callgrind-out-file="$artifact_dir/$version.callgrind" "$artifact_dir/$version-stage2" -O -target x86-64-linux -emit asm -o /dev/null "$producer_root/compiler/checker.fern" "$producer_root/internal/stdlib" > "$artifact_dir/$version-profile.log" 2>&1
done
sha256sum "$stage0_binary" "$artifact_dir/stage1" "$artifact_dir/baseline-stage2" "$artifact_dir/candidate-stage2" "$artifact_dir/baseline.s" "$artifact_dir/candidate.s" > "$artifact_dir/hashes.txt"
git -C "$frozen_candidate_root" status --short > "$artifact_dir/candidate-status.txt"
git -C "$frozen_candidate_root" diff HEAD -- compiler internal/stdlib > "$artifact_dir/candidate-uncommitted.patch"
git -C "$frozen_candidate_root" rev-parse HEAD > "$artifact_dir/candidate-head.txt"
git rev-parse HEAD > "$artifact_dir/publication-head.txt"
