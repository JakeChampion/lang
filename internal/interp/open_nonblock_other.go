//go:build !unix

package interp

// oNonblock is the O_NONBLOCK the open_*_with builtins' bit 1 becomes.
// The js/wasm port of `syscall` has no such flag and no FIFO for it to
// matter on, so the bit is accepted and does nothing there.
const oNonblock = 0
