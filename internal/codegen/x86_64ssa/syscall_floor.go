package x86_64ssa

import "fmt"

// emitSyscallHelper returns the emitter for `__syscall<n>(nr, a, …)`, the
// runtime's syscall floor on this backend: the number arrives as the first
// System V argument and the n arguments follow it, so the body slides them
// into the kernel's registers (rdi, rsi, rdx, r10, r8, r9), reads a sixth
// from the caller's stack slot and traps. The result is the kernel's word
// unchanged, -errno on failure. Leaf; `syscall` clobbers only rcx and r11,
// which the call convention already treats as scratch.
func emitSyscallHelper(n int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(fmt.Sprintf("__syscall%d", n)))
		w("\tmov rax, rdi")
		w("\tmov rdi, rsi")
		w("\tmov rsi, rdx")
		w("\tmov rdx, rcx")
		if n >= 4 {
			w("\tmov r10, r8")
		}
		if n >= 5 {
			w("\tmov r8, r9")
		}
		if n >= 6 {
			// The seventh argument is the one past the six registers:
			// the caller pushed it, and it sits just above the return
			// address.
			w("\tmov r9, [rsp + 8]")
		}
		w("\tsyscall")
		w("\tret")
	}
}
