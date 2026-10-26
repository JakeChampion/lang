# Split debug variables

Date: 2026-10-26. PR: #13561. Issue: #13472.

The public source commit is `0f49f97af24d8d1e2ee43012179d79f212e5c338`.
The measured local commit is `6ae8b10f02f17715c12bfc7aa90b65574ccf0466`.
Both commits have source tree `c2be2c73848807e800a3aa7f1fab3747c061fc55`.
The source includes main at `f21b97e3706b40838681e75035e1bf27a3fb6338`.

The compiler retains a debug variable when a record parameter travels as scalar parts.
The variable keeps one declared record type and a location with `DW_OP_piece`.
Each piece follows its register or stack slot over its live range.
Unavailable pieces keep their position in the declared value.
Unique source constants use `DW_OP_implicit_value`.
The GDB regression reads the new record value after assignment.

DWARF now includes `DW_AT_entry_pc` for the register ABI entry.
The symbol table keeps the stack ABI address.
The function range covers both entries.
Registers with DWARF numbers above 31 use `DW_OP_regx`.
The compiler keeps the same live ranges for ordinary scalar variables.

The native DWARF checks pass on x86-64 and ARM64.
GDB on x86-64 reads `Point`, `FloatPoint`, and `Option[Duration]` parameters.
The checks cover two-piece and three-piece values, nested records, and source constants.
The optional record keeps both its presence flag and its declared payload.
The embedded SSA fixtures include the new empty debug metadata field.
Their semantic assertions remain in place.

## Bootstrap

The official stage0 pin is `stage0-20261025-433d93a`.
All three stages use the same source tree.
The final two stages have identical bytes.

| Stage | Bytes | SHA-256 |
| --- | ---: | --- |
| stage1 | 15037776 | `fca227691ddb01de81298d5fc931280a44274caa602ac03a26f1ff48a1117443` |
| stage2 | 14946960 | `2ae3057cc7e5919037414c5fb70fea9dfdba01675744099a9c19d2c0d0eae076` |
| stage3 | 14946960 | `2ae3057cc7e5919037414c5fb70fea9dfdba01675744099a9c19d2c0d0eae076` |

## Driver sizes

The official stage0 pin builds each of the ten drivers.
The second bootstrap stage supplies the tracked compiler row.
All 11 rows pass the strict size gate.
The baseline records every measured change in both directions.

| Driver | Previous bytes | Measured bytes | Change |
| --- | ---: | ---: | ---: |
| `fern.fern/stage2` | 14885344 | 14946960 | +61616 |
| `fern.fern` | 14980272 | 15037776 | +57504 |
| `drivers/asm_load_run.fern` | 11964288 | 12021792 | +57504 |
| `drivers/asm_modload_run.fern` | 10608672 | 10670288 | +61616 |
| `drivers/asm_ir_run.fern` | 10205184 | 10262688 | +57504 |
| `drivers/asm_run.fern` | 9363856 | 9421376 | +57520 |
| `drivers/wasm_ir_run.fern` | 9307104 | 9360416 | +53312 |
| `drivers/wasm_run.fern` | 9311360 | 9360576 | +49216 |
| `drivers/wasm_runio_run.fern` | 9288640 | 9341952 | +53312 |
| `drivers/asm_pathprobe_run.fern` | 6204032 | 6253248 | +49216 |
| `drivers/checker_modload_run.fern` | 3295600 | 3295600 | +0 |

## Validation

All 109 targeted cases pass, with no skips.
All 617 SSA cases pass, with no skips.
All 201 source lint cases pass, with no skips.
Vet and format checks pass.
The previous source passed all 4666 checker and heap proof cases.
Fresh whole-compiler checks remain in flight on this source.

The final compiler produced worker SHA-256 `33e30c6a6cffbad68db72916788206f72a8aca6433a6fe3f384f5ef8bc2cda78`.
The local cgroupfs qualification passed all 27 cases, with three kernel OOM kills.
Every kernel control, deadline, cleanup, and leak assertion remains unchanged.
The source hashes did not change during qualification.
The earlier record retains the binary identity control for the original systemd failure.
These records do not qualify production workers or aggregate memory limits.

Full CI must pass on the final head before merge.
