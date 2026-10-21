# Filesystem selection for df operands

The GNU consumer checks for the Stdio byte migration exposed 149 `df`
mismatches in the Linux validation container. The unchanged parent
`fff2cc803` fails the same 149 cases. Fern reports `/proc` where GNU 9.12
reports a later mount of the same device, `/proc/sysrq-trigger`.

Fern selected only the longest path prefix. GNU's
[operand lookup](https://github.com/coreutils/coreutils/blob/v9.12/src/df.c#L1345)
prefers non-pseudo mounts, verifies the selected mount's device against the
operand, and falls back to an accessible entry for the matching device.
The last eligible entry wins in that fallback. The correction follows that
selection, using the pseudo-filesystem classification from GNU 9.12's
[pinned gnulib](https://github.com/coreutils/gnulib/blob/106e9b2384d08a1696fcbd40cbab52237943f208/lib/mountlist.c#L174).

The existing GNU `TestDf` corpus and the primary compiler's df comparisons
pass on the integration with main `f4de66fd5`, including all 149 cases that
failed on the original parent. They run within the Stdio consumer groups;
the full Linux unit suite and every lint gate also pass on this source.

The focused size comparison uses the original parent and its operand-selection
correction, compiled with the same reproduced compiler, SHA-256
`a518ac78b011ab3eb8ae4ddc1f66a04d04269d74cac00333956743a74b229595`.
The libraries are identical; only df's operand selection changes.
ARM64 Linux files grow
from 208,240 to 208,752 bytes. Darwin files grow from 182,769 to 182,801
bytes, with code increasing by 1,888 bytes, unwind information by 160 bytes
and data by 256 bytes. The added mount classification, device validation
and fallback account for the new functionality; no size baseline changes.
Darwin compilation is only a size check: this utility reads Linux mount
tables, and the broader Darwin GNU corpus still fails on the unchanged
parent as well as this candidate. It is not a Darwin parity claim.
