// Imports + WASI-facing helpers for the wasmbin backend.
//
// The lang `print(s)` lowering eventually calls a synthetic
// __lang_print helper. The helper takes a (data, len) string,
// normalises it to a heap buffer (so inline-form strings work
// via the SSO seam), writes a single iovec to a fixed scratch
// region of linear memory, and invokes the imported WASI
// preview-1 fd_write.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// importSpec describes one imported function.
type importSpec struct {
	module  string
	name    string
	params  []byte
	results []byte
}

// importSpecs is the import registry. Each entry corresponds to
// one wasi_snapshot_preview1 (or similar) imported function.
var importSpecs = map[string]importSpec{
	"wasi_fd_write": {
		// (fd, iovs_ptr, iovs_count, nwritten_ptr) → errno
		module:  "wasi_snapshot_preview1",
		name:    "fd_write",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_proc_exit": {
		// (exit_code: i32) → ! (never returns; the wasi spec
		// marks this as a "trap-like" abrupt termination, but
		// the wasm-level signature still says void return).
		module:  "wasi_snapshot_preview1",
		name:    "proc_exit",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_random_get": {
		// (buf_ptr, buf_len) → errno. Fills buf_ptr..+buf_len
		// with cryptographically-strong random bytes (per the
		// wasi spec; host may degrade in sandboxed environments).
		module:  "wasi_snapshot_preview1",
		name:    "random_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_clock_time_get": {
		// (clock_id i32, precision i64, time_ptr i32) → errno i32.
		// Writes the current time as nanoseconds-since-epoch
		// (u64 little-endian) at time_ptr.
		module:  "wasi_snapshot_preview1",
		name:    "clock_time_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI64, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_environ_sizes_get": {
		// (envc_ptr i32, env_buf_size_ptr i32) → errno.
		// Writes the environment-variable count + the total
		// byte size of the concatenated env strings into the
		// two output pointers.
		module:  "wasi_snapshot_preview1",
		name:    "environ_sizes_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_args_sizes_get": {
		// (argc_ptr i32, argv_buf_size_ptr i32) → errno.
		// Writes argv-count + total byte length of the
		// concatenated argv strings (NUL-separated) into the
		// two output pointers.
		module:  "wasi_snapshot_preview1",
		name:    "args_sizes_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_environ_get": {
		// (environ_ptr i32, environ_buf i32) → errno. Writes
		// argc i32 pointers into environ_ptr, followed by the
		// NUL-terminated "KEY=VALUE" strings into environ_buf.
		module:  "wasi_snapshot_preview1",
		name:    "environ_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_args_get": {
		// (argv_ptr i32, argv_buf i32) → errno. Writes argc i32
		// pointers into argv_ptr, followed by the NUL-terminated
		// argv strings into argv_buf.
		module:  "wasi_snapshot_preview1",
		name:    "args_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
}

// importNeeds is parallel to runtimeNeeds but for imports.
type importNeeds struct {
	order []string
	set   map[string]bool
}

func (in *importNeeds) add(name string) {
	if in.set == nil {
		in.set = map[string]bool{}
	}
	if in.set[name] {
		return
	}
	in.set[name] = true
	in.order = append(in.order, name)
}

// scanImports decides which imports the module needs based on
// the helpers in use (and direct IR-op references in a future
// expansion). Each helper that wraps a WASI call adds its import
// here.
func scanImports(prog *ir.Program, helpers runtimeNeeds) importNeeds {
	var in importNeeds
	if helpers.set["__lang_print"] {
		in.add("wasi_fd_write")
	}
	if helpers.set["__lang_exit"] {
		in.add("wasi_proc_exit")
	}
	if helpers.set["__lang_random_i32"] {
		in.add("wasi_random_get")
	}
	if helpers.set["__lang_now_ns"] {
		in.add("wasi_clock_time_get")
	}
	if helpers.set["__lang_env_count"] {
		in.add("wasi_environ_sizes_get")
	}
	if helpers.set["__lang_arg_count"] {
		in.add("wasi_args_sizes_get")
	}
	if helpers.set["__lang_arg_at"] {
		in.add("wasi_args_sizes_get")
		in.add("wasi_args_get")
	}
	if helpers.set["__lang_env_at"] {
		in.add("wasi_environ_sizes_get")
		in.add("wasi_environ_get")
	}
	return in
}

// printIovecAddr is the fixed scratch location in linear memory
// where __lang_print writes the iovec (iov_base, iov_len) pair
// before calling fd_write. 8 bytes total; lives outside the
// allocator's region (which starts at 64 by default, here we
// pick 48..56 in the reserved low-memory window before the
// cursor at 40 and the runtime-reserved area up to 64).
const printIovecAddr = 48

// printRetAddr is the 4-byte scratch where fd_write writes the
// nwritten result.
const printRetAddr = 56

// randomBufAddr is the 4-byte scratch where wasi_random_get
// writes the random bytes consumed by __lang_random_i32. Lives
// in the reserved low-memory window past printRetAddr.
const randomBufAddr = 60

// Cache for __lang_arg_at / __lang_env_at. Both helpers lazily
// initialise on first call: ask the host for sizes, alloc the
// pointer table + string buffer, call args_get / environ_get,
// store the (count, table_ptr) in the cache. Subsequent calls
// short-circuit on the init flag and walk the cached table.
// Lives in the low-memory window 0..39 which was previously
// unused (allocCursorAddr starts at 40).
//
//	 0..3   args_init flag (0 / 1)
//	 4..7   args count (i32)
//	 8..11  argv_ptrs heap pointer
//	12..15  args sizes scratch slot 0 (argc out from args_sizes_get)
//	16..19  args sizes scratch slot 1 (bufsize out from args_sizes_get)
//	20..23  env_init flag (0 / 1)
//	24..27  env count (i32)
//	28..31  environ_ptrs heap pointer
//	32..35  env sizes scratch slot 0
//	36..39  env sizes scratch slot 1
const (
	argsInitAddr      = 0
	argsCountAddr     = 4
	argsPtrsAddr      = 8
	argsSizesArgcAddr = 12
	argsSizesBufAddr  = 16
	envInitAddr       = 20
	envCountAddr      = 24
	envPtrsAddr       = 28
	envSizesArgcAddr  = 32
	envSizesBufAddr   = 36
)

// buildPrintBody assembles the wasm bytes for __lang_print.
//
// Signature: (param $data i32) (param $len i32) (result)
//
// Logical:
//
//	L   = __lang_str_len(data, len)
//	dst = __lang_alloc(L)
//	for i in 0..L: mem[dst+i] = __lang_str_byte(data, len, i)
//	mem[48..52] = dst   ; iov_base
//	mem[52..56] = L     ; iov_len
//	wasi_fd_write(1, 48, 1, 56)
//	drop result
//
// Wasm locals (after the two params):
//
//	2: $L
//	3: $dst
//	4: $i
func buildPrintBody(idxs map[string]uint32) []byte {
	strLen := idxs["__lang_str_len"]
	strByte := idxs["__lang_str_byte"]
	alloc := idxs["__lang_alloc"]
	fdWrite := idxs["wasi_fd_write"]
	var body []byte
	// L = __lang_str_len(data, len)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 2) // $L
	// dst = __lang_alloc(L)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3) // $dst
	// Copy loop: for i in 0..L: mem[dst+i] = __lang_str_byte(data, len, i).
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 4) // $i = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 4)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1) // exit on $i >= $L
		// mem[dst + i] = __lang_str_byte(data, len, i)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, strByte)
		body = memory.InstI32Store8(body, 0, 0)
		// $i++
		body = inst.InstLocalGet(body, 4)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block
	// mem[printIovecAddr] = dst (iov_base)
	body = inst.InstI32Const(body, printIovecAddr)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Store(body, 2, 0)
	// mem[printIovecAddr + 4] = L (iov_len)
	body = inst.InstI32Const(body, printIovecAddr+4)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Store(body, 2, 0)
	// wasi_fd_write(1, iovec_addr, 1, ret_addr); drop result.
	body = inst.InstI32Const(body, 1) // stdout
	body = inst.InstI32Const(body, printIovecAddr)
	body = inst.InstI32Const(body, 1) // iovec count
	body = inst.InstI32Const(body, printRetAddr)
	body = inst.InstCall(body, fdWrite)
	body = inst.InstDrop(body)
	// Three i32 locals: $L, $dst, $i.
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildExitBody assembles the wasm bytes for __lang_exit.
//
// Signature: (param $code i32) (result)
//
// Body is a single call to wasi_proc_exit, which never returns.
// `unreachable` at the end satisfies the wasm verifier (every
// function body must structurally end somewhere even when
// execution can't actually reach it).
func buildExitBody(idxs map[string]uint32) []byte {
	procExit := idxs["wasi_proc_exit"]
	var body []byte
	body = inst.InstLocalGet(body, 0) // $code
	body = inst.InstCall(body, procExit)
	body = inst.InstUnreachable(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildRandomI32Body assembles the wasm bytes for __lang_random_i32.
//
// Signature: () → i32
//
// Body calls wasi_random_get(randomBufAddr, 4); ignores the
// returned errno; reads back the 4 bytes as an i32. Uses a
// fixed-address scratch instead of allocating to avoid leaking
// memory when called in a loop (the bump allocator never frees).
func buildRandomI32Body(idxs map[string]uint32) []byte {
	randomGet := idxs["wasi_random_get"]
	var body []byte
	body = inst.InstI32Const(body, randomBufAddr)
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, randomGet)
	body = inst.InstDrop(body) // ignore errno
	body = inst.InstI32Const(body, randomBufAddr)
	body = memory.InstI32Load(body, 2, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildNowNsBody assembles the wasm bytes for __lang_now_ns.
//
// Signature: () → i64
//
// Body:
//
//	buf = __lang_alloc(8)
//	wasi_clock_time_get(0 /* realtime */, 0 /* precision */, buf)
//	drop errno
//	return i64.load(buf)
//
// Allocates per call so the 8-byte target buffer doesn't clash
// with any other fixed-address scratch.
func buildNowNsBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	clockTime := idxs["wasi_clock_time_get"]
	var body []byte
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 0) // $buf
	body = inst.InstI32Const(body, 0) // clock_id = REALTIME
	body = inst.InstI64Const(body, 0) // precision = 0
	body = inst.InstLocalGet(body, 0) // $buf
	body = inst.InstCall(body, clockTime)
	body = inst.InstDrop(body) // ignore errno
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI64Load(body, 3, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildEnvCountBody assembles the wasm bytes for __lang_env_count.
//
// Signature: () → i32 (envc)
//
// Body:
//
//	buf = __lang_alloc(8)               ; two i32 output slots
//	wasi_environ_sizes_get(buf, buf + 4)
//	drop errno
//	return i32.load(buf)                ; envc lives at +0
func buildEnvCountBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	envSizes := idxs["wasi_environ_sizes_get"]
	var body []byte
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 0) // $buf
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, envSizes)
	body = inst.InstDrop(body)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArgCountBody assembles the wasm bytes for __lang_arg_count.
//
// Signature: () → i32 (argc)
//
// Body:
//
//	buf = __lang_alloc(8)               ; two i32 output slots
//	wasi_args_sizes_get(buf, buf + 4)
//	drop errno
//	return i32.load(buf)                ; argc lives at +0
func buildArgCountBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	argsSizes := idxs["wasi_args_sizes_get"]
	var body []byte
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 0) // $buf
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, argsSizes)
	body = inst.InstDrop(body)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArgAtBody assembles __lang_arg_at.
//
// Signature: (param $i i32) (result i32 i32) — (data, len) pair.
//
// Logic: lazily call wasi_args_sizes_get + wasi_args_get on first
// call, caching (count, argv_ptrs) in low memory. Each call walks
// argv_ptrs[i] until the NUL byte to recover the length, then
// returns (cstr, len) as a heap-form string (top bit of len = 0).
//
// Out-of-range i (signed-negative or i >= argc) returns (0, 0).
//
// Locals (after the one param):
//
//	1: $argc
//	2: $bufsize
//	3: $argv_ptrs
//	4: $argv_buf
//	5: $cstr
//	6: $len
func buildArgAtBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	argsSizes := idxs["wasi_args_sizes_get"]
	argsGet := idxs["wasi_args_get"]
	var body []byte
	body = inst.InstI32Const(body, argsInitAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, argsSizesArgcAddr)
		body = inst.InstI32Const(body, argsSizesBufAddr)
		body = inst.InstCall(body, argsSizes)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, argsSizesArgcAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 1) // $argc
		body = inst.InstI32Const(body, argsSizesBufAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 2) // $bufsize
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 3) // $argv_ptrs
		body = inst.InstLocalGet(body, 2)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 4) // $argv_buf
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, argsGet)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, argsCountAddr)
		body = inst.InstLocalGet(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, argsPtrsAddr)
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, argsInitAddr)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	// Bounds check via unsigned compare: rejects negatives + overshoot.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, argsCountAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// cstr = mem[args_ptrs + i*4]
	body = inst.InstI32Const(body, argsPtrsAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 5) // $cstr
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6) // $len = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 6)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 6)
	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildEnvAtBody — mirror of buildArgAtBody, routed through
// wasi_environ_sizes_get + wasi_environ_get. Each returned
// (data, len) covers a full "KEY=VALUE" entry; user code splits
// on '=' if needed.
func buildEnvAtBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	envSizes := idxs["wasi_environ_sizes_get"]
	envGet := idxs["wasi_environ_get"]
	var body []byte
	body = inst.InstI32Const(body, envInitAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, envSizesArgcAddr)
		body = inst.InstI32Const(body, envSizesBufAddr)
		body = inst.InstCall(body, envSizes)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, envSizesArgcAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 1)
		body = inst.InstI32Const(body, envSizesBufAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 2)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 3)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, envGet)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, envCountAddr)
		body = inst.InstLocalGet(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, envPtrsAddr)
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, envInitAddr)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, envCountAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	body = inst.InstI32Const(body, envPtrsAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 5)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 6)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 6)
	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
