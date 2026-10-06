//go:build !unix

package interp

// The js/wasm port of `syscall` has none of these, and no FIFO or
// terminal for them to matter on: non-blocking is accepted and does
// nothing there (its reason to exist, the FIFO, cannot be made), the rest
// are refused as on WASI.
const (
	oNonblock  = 0
	oDirectory = 0
	oDsync     = 0
	oSync      = 0
	oNoctty    = 0
	oNofollow  = 0
)
