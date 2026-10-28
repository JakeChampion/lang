# Fixed-register mask validation evidence

The [measurement entry](../../ssa-log/2026-10-28-fixed-mask-scan.md) describes the source change, producer, subject, measurements and validation limits.
The source change moves mask validation outside the register loop.
It preserves every mask guard and caller constraint.

The stage-2 comparison uses one producer and the prior group's complete baseline.
The fixed compile uses 0.130618% fewer instructions with no change in box count.
All three complete checker outputs remain byte-identical.
The small linked driver passes on all targets without a file-size increase.

The valid differential compares complete prefix tables and interval decisions across 2,976 cases per target.
All valid runs retain sanitizers and report balanced allocations with zero live bytes.
Separate plain builds compare full refusal output and the target-specific status for all 60 refusal runs.
The archives also preserve the initial harness failures and their causes.

The whole conformance matrix matches all 4,242 fixture-target rows.
It retains identical hashes for 3,449 emitted artifacts and the same 793 refusals.
The archives preserve the frozen conformance inputs, both matrices and their commands.

The manifests record raw and compressed hashes.
Every archive passes a decompression identity check.
The record retains source snapshots, full profiles, commands, caller counts, symbol resolution and validation logs.
Applicable current-head CI and full issue acceptance remain incomplete.
