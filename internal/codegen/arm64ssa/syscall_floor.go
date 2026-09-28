package arm64ssa

import "fmt"

// emitSyscallHelper returns the emitter for `__syscall<n>(nr, a, …)`, the
// runtime's syscall floor on this backend: the number arrives in x0 and the
// n arguments in x1..xn, so the body moves the number to x8 and slides the
// arguments down to x0..x(n-1) before trapping. Linux only, like the rest of
// this backend: the result is -errno in x0 on failure. Leaf.
func emitSyscallHelper(n int) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(fmt.Sprintf("__syscall%d", n)))
		w("\tmov x8, x0")
		for i := 0; i < n; i++ {
			w("\tmov x%d, x%d", i, i+1)
		}
		w("\tsvc #0")
		w("\tret")
	}
}
