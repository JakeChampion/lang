# CLI complexity repair

PR #14016's x86 self-host shard rejects the compiler complexity total.
The native counter also finds main above the unchanged allowance.
The repair separates formatter execution and coverage, shared-output, and backend admission from the main CLI function.
It preserves the validation order and diagnostic text.
No baseline or lint suppression changes.

The latest owner commit and main merge remain intact.
The repaired source measures compiler excess 18,313 and worst-function complexity 223.
The original limits accept both values.
All fourteen targeted test groups pass in 194.305 seconds without skips, including the actual x86 ratchet.
Nineteen differential CLI controls match exactly in status, stdout, and stderr.

All five runtime targets, feature and fixture checks, and all 87 source drivers pass.
Full lint passes.
Compiler stages 2, 3, and 4 match exactly.
The manifest binds the source, commands, compiler, and raw logs.
Full current-head GitHub CI remains required before merge.
