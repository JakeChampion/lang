package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
)

// buildFdDupOntoBody is `__fern_fd_dup_onto` on both previews: neither
// can install a descriptor at a number the caller chose, so it is
// ENOTSUP unconditionally, which `__build_io_error` turns into
// `IoError::Unsupported` — the same refusal `syncfs` reports.
//
// Preview 1's `fd_renumber` looks like the lowering and is not: it
// CLOSES the source, so the handle the caller still holds would be
// dangling and its drop would close a descriptor it no longer owns.
// That is a move rather than a duplicate. Preview 2 has no numbered
// table to renumber at all — a descriptor there is a resource handle.
//
// Params: (handle, fd), both ignored. Locals after them:
// 2: $errno  3: $err_ptr  4: $box
func buildFdDupOntoBody(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]

	var body []byte
	body = inst.InstI32Const(body, errnoNoTsup)
	body = inst.InstLocalSet(body, 2)
	body = emitSomeIoError(body, buildIoErr, allocRc1, 2, 3, 4)

	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
