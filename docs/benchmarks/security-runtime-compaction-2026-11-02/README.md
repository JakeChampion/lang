# Security runtime compaction

PR #14010 adds double-free checks and bounded backtraces.
Its first CI run exceeds the original native instruction limits.
The repair shares the status-134 report entry and raw-free frame setup.
The hex loops use their digit counters as output indexes.
The ARM loop retains its 64-frame limit, stack bounds, and one-use overflow floor.
All original performance limits remain unchanged.

The Darwin signal-context fixture now checks the new handler contract.
It requires the handler to preserve the stack top and record the faulting stack pointer as the backtrace floor.
The overflow test also checks the maximum frame count.

The large-push decoder recognizes the conditional duplicate-head abort boundary.
The existing machine check still proves valid pushes over the original size intervals.
The additional guarded check requires duplicate heads to abort before either free-list store.
Its negative controls remove the guard, reverse its condition, change its width or target, clobber flags, and move a store before it.
Positive controls use the equal-branch alias and reverse the pointer comparison.
The original reference cases and size domains remain unchanged.

The evidence records bind compiler bytes, source hashes, commands, and results.
The static report covers all 78 native benchmark rows under the original strict comparator.
Local dynamic instruction counts cover ARM64 Linux.
Native x86 execution, the full seccomp corpus, and the remaining suites require current-head GitHub CI.
