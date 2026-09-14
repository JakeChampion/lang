package wasmbin

import "testing"

// The preview-1 `fdstat` landing area must be 8-ALIGNED. wasmtime's
// preview-1 shim refuses `fd_fdstat_get` outright on a misaligned one —
// "write fdstat: Pointer not aligned to 8" — because the record's two
// rights words are u64, and the refusal is an error return the guest sees
// as a failed call rather than anything the emitter could catch.
//
// It was 172 before `flags()` needed the rights word, and `isatty` (which
// reads only fs_filetype out of the same buffer) would have hit the same
// refusal the moment a host checked. The layout is a chain of `+ 4`s, so
// one added counter above this line is all it takes to break it again.
func TestFdstatBufIsEightAligned(t *testing.T) {
	if fdstatBufAddr%8 != 0 {
		t.Errorf("fdstatBufAddr = %d, which is not 8-aligned; wasmtime refuses fd_fdstat_get into it", fdstatBufAddr)
	}
}
