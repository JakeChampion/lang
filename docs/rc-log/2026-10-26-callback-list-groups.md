# Stable list groups after callback expansion

A formatter can expand a callback body from one line to several lines. An element after its closing brace can still belong to the same list group.

Both printers previously chose list groups from element start lines alone. After callback expansion, the next element started on a later line. A second format then created a new list group. The write-back guard correctly refused the unstable output.

The printers now keep an element in the previous group when it follows a callback closing brace on the same line. The Go parser retains that closing position in the lambda block. The Fern printer uses the block spans that the parser already supplies. It excludes containing blocks and retains span metadata inside callbacks.

The group also propagates through scalar elements between callbacks. The rule applies to call arguments, array elements, and record fields. Explicit list breaks and comments still determine separate groups. The write-back guard retains its parse, content, and idempotence checks.

## Local validation

Eleven layout controls cover flat and wrapped calls, arrays, record fields, argument comments, successive callbacks, destructured parameters, and nested callbacks. The initial four controls failed before the correction. The reduced runtime source passed type checks but failed the old formatter write-back guard.

The selected parser and printer unit suite passed 731 test nodes with no failures or skips. The corpus tests ran separately. Corpus output type checks remain unverified. Four structural corpus checks passed over all 1,064 files: AST shape, idempotence, comment anchors, and comment blanks.

The focused self-host suite passed 156 test nodes in 58.217 seconds with no failures or skips. It includes formatter parity, malformed inputs, literate sources, divergent match arms, and both unchanged complexity gates. The formatted callback-loop program passed native, ARM64/QEMU10, and Wasm runtime checks with balanced zero-live heap censuses.

No application syntax, baseline, threshold, or validation exemption changed. The unpublished JPEG entropy slice remains separate from this formatter repair. Full current-head CI still requires its own result.

## Clean compiler integration

The clean CLI reports `a7793d0c2a18`.
Its SHA256 is `d285f45c01ed290bdad354a33731017f790342a4ec510e7c52248b5b7861911e`.
It formats the preserved full JPEG test through two write-back operations without a second change.
The primary interpreter also passes the formatted five-argument callback-loop program.
The unpublished JPEG source files remain unchanged.

Both complete image scripts passed all 150 TAP cases plus their smoke checks.
All 61 native and 37 Wasm heap censuses were balanced with zero live bytes.
The executable hash was unchanged after both scripts.
Wasm supplies the leak census without the native over-release and use-after-free detectors.

The unchanged allocation ceiling passed at 34,729,565 allocations and frees.
The separate 1% advisory warning remained at +9.82%.
This runtime has no delegated cgroup, so the report contains no peak RSS value.
All 1,300 tracked Fern format checks, Go build, Go vet, workflow lint, and selector checks passed.
These local results do not establish a full current-head CI pass or JPEG entropy acceptance.
