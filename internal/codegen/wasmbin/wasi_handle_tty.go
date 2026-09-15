package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
)

// buildHandleTtyRefusal is the four terminal questions' handle forms on both
// previews: `r.window_size()`, `r.set_window_size(rows, cols)`,
// `r.termios_get()` and `r.termios_set(when, words)` (#9363). Neither world
// has a terminal to measure or configure, so each is ENOTSUP
// unconditionally, which `__build_io_error` turns into
// `IoError::Unsupported` — the refusal `syncfs` and `dup_onto` report.
//
// The FREE forms of the same four are refused at compile time instead, by the
// `tty` capability in internal/platforms. A method cannot be: the call reaches
// that scan already rewritten to `__method_Reader_termios_get(r)`, and it
// inspects only plain identifiers. Each of these carries an IoError, so the
// refusal has somewhere to go, which is the same argument that put `syncfs`
// on this side of the line and `sync` on the other.
//
// Every parameter is ignored, the receiver included. Locals after them:
// +0: $errno  +1: $err_ptr  +2: $box
func buildHandleTtyRefusal(params int) func(idxs map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		allocRc1 := idxs["__fern_alloc_rc1"]
		buildIoErr := idxs["__build_io_error"]

		errno := uint32(params)
		var body []byte
		body = inst.InstI32Const(body, errnoNoTsup)
		body = inst.InstLocalSet(body, errno)
		body = emitHandleResultErr(body, buildIoErr, allocRc1, errno, errno+1, errno+2)

		locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
		return inst.PutFunctionBody(nil, locals, body)
	}
}
