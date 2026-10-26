# Compiler CI repairs

Date: 2026-10-26. PR: #13561.

The source commit is `3de29207a3a83e9008ef78d25667bec904f22fa2`.
It includes main at `e10c59f381362f0fd5f9595e9496dd2a3a7639fd`.
The stage0 pin is `stage0-20261025-433d93a`.
Its SHA-256 is `efafedd32a9223a2c87b385e8425d155d5f59c10fab7e3bf9e09509254fce748`.

The checker now checks each nested union array element against its destination type.
It also rejects enum variant patterns on arrays and slices with `E035`.
The parser uses the checked result type for a bounded generic lambda call.
Scalar field projections no longer make a factory result retain a whole record box.
The factory regression fails with `E068` on main before this change.
It passes on all three targets after this change.

The heap proof fixtures now use five scalar fields for each flat heap child.
Small records can travel as scalar values and no longer test that heap protocol.
The origin fixtures retain their original payload widths and force whole values through an array.
The fixtures preserve their rejection checks for malformed graphs and invalid fields.

## Bootstrap

All three stages use the same source tree.
The final two stages have identical bytes.

| Stage | Bytes | SHA-256 |
| --- | ---: | --- |
| stage1 | 14976176 | `e733af7fcb6e03d6043aa48421346a2c15965973911c99689e6adceda3a6171c` |
| stage2 | 14885376 | `7478e2d816b8fab374bf061311dca15c82b5fe95527b8b9e9128725c6d7ff21b` |
| stage3 | 14885376 | `7478e2d816b8fab374bf061311dca15c82b5fe95527b8b9e9128725c6d7ff21b` |

## Driver sizes

The stage0 pin builds the ten driver rows.
The second bootstrap stage supplies the tracked compiler row.
The strict size gate passed for all 11 rows.
The baseline records every measured change in both directions.

| Driver | Previous bytes | Measured bytes | Change |
| --- | ---: | ---: | ---: |
| `fern.fern/stage2` | 14881456 | 14885376 | +3920 |
| `fern.fern` | 14972080 | 14976176 | +4096 |
| `drivers/asm_load_run.fern` | 11952000 | 11956096 | +4096 |
| `drivers/asm_modload_run.fern` | 10600480 | 10604576 | +4096 |
| `drivers/asm_ir_run.fern` | 10196992 | 10201088 | +4096 |
| `drivers/asm_run.fern` | 9355664 | 9359760 | +4096 |
| `drivers/wasm_ir_run.fern` | 9298912 | 9303008 | +4096 |
| `drivers/wasm_run.fern` | 9299072 | 9307264 | +8192 |
| `drivers/wasm_runio_run.fern` | 9280448 | 9288640 | +8192 |
| `drivers/asm_pathprobe_run.fern` | 6195824 | 6199936 | +4112 |
| `drivers/checker_modload_run.fern` | 3291504 | 3295600 | +4096 |

## Worker qualification

The workflow uses cgroupfs to preserve kernel OOM events.
The local qualification passed all 27 cases, with three kernel OOM kills.
It preserves every deadline, cleanup, cgroup, and leak assertion.
The source hashes did not change during the run.
The worker binary matches the binary from the failed systemd CI run.
Its SHA-256 is `2b9864d2eff84f48e921e06fc5dad7d9166a846dad3423539e9bfaada16e2cbe`.

The local result does not qualify production workers or aggregate memory limits.
Full CI must pass on the final head.
