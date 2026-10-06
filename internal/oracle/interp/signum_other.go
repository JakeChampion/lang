//go:build !linux && !darwin

package interp

// A platform with no sigprocmask has no signal delivery either, so these
// stand only to keep signalArg total: every builtin that consults them
// answers ENOSYS before the number is used for anything. js/wasm is the
// one that reaches here, and its syscall package defines neither name —
// which is why they are not read from it.
const (
	sigKill = int64(9)
	sigStop = int64(19)
)
