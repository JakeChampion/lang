// Package interp is a small tree-walking interpreter for the lang AST.
//
// It's used by the REPL (cmd/fern -repl) and by tests; production
// builds still go through the ARM64 / WASM code generators.
//
// Control flow inside a function uses a flow-tagged result value
// rather than panics: each statement returns a stmtResult whose Flow
// field tells the surrounding loop / block whether to keep going,
// break, continue, or unwind to the enclosing call site.
package interp

import (
	"bytes"
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"math"
	"math/bits"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jakechampion/lang/internal/ast"
)

// tcpListenerHandle / tcpConnHandle abstract the host TCP
// types so the interpreter doesn't directly drag `net.*`
// into every value-handling site. Aliases keep the call
// shapes identical to net.Listener / net.Conn.
type tcpListenerHandle = net.Listener
type tcpConnHandle = net.Conn

// tcpNetListen is a thin indirection so tests can substitute
// a deterministic listener if needed (currently just calls
// net.Listen).
func tcpNetListen(network, address string) (tcpListenerHandle, error) {
	return net.Listen(network, address)
}

// Value is the runtime tagged-union of every type the language
// evaluates to. Concrete kinds: Number, Bool, String, Void, Array,
// Func (a user-defined function reference), and Builtin.
type Value interface {
	String() string
}

type Number int64
type Bool bool
type String string
type Void struct{}
type Array []Value
type Func struct{ Decl *ast.FuncDecl }

// Cell is the runtime value of Cell[T] (docs/CELL-TYPE-PLAN.md): a
// single-slot mutable box held by pointer, so `set` mutates the shared
// box in place (the deliberate, RC-safe-because-cycle-free escape from
// value semantics). No CoW — a cell mutates in place by design.
type Cell struct{ V Value }

func (*Cell) String() string { return "<cell>" }

// Float carries f32 / f64 IEEE-754 values for the interp. The
// width tag is preserved because the language distinguishes f32
// from f64 in type errors and `to_string` rendering (std/float
// uses different digit budgets per width); the underlying
// storage is float64 either way — for f32 we round-trip through
// `float32` during arithmetic and at f32_bits boundary points
// to keep the visible precision honest.
//
// The interp had no float values for most of its lifetime
// (raw-memory ops in the Lang stdlib bodies couldn't be modeled
// either way) — the native + wasm backends owned floats end-to-
// end. Adding Float here unblocks unit tests that exercise
// float arithmetic / formatting / property checks without
// needing to compile to a backend.
type Float struct {
	V     float64
	Width int // 32 or 64
}

// Struct is a heap-allocated record. The map preserves nothing about
// declaration order — formatting walks the StructDecl when available
// (the interpreter doesn't currently have access, so String() is a
// best-effort summary).
type Struct struct {
	TypeName string
	Fields   map[string]Value
}

// Enum is an interpreted tagged-union value: a variant index plus
// its payload values. EnumName is the owning enum's declaration
// name (for diagnostics / equality); VariantName + Index identify
// the constructor; Payloads holds the evaluated arguments in
// declaration order.
type Enum struct {
	EnumName    string
	VariantName string
	Index       int
	Payloads    []Value
}

// Map is the interpreter's `Map[K, V]` value. Two parallel slices
// hold entries in the SAME order the IR / codegen runtime uses, so the
// differential oracle sees identical `keys()` / `values()` / iteration
// output across the interpreter and native backends: `set` appends new
// keys (insertion order) and `delete` swaps the last entry into the
// removed slot (swap-with-last), exactly mirroring core/map.fern's
// __map_delete_impl. See docs/ADVERSARIAL-REVIEW-2026-06.md (M3). A
// `map[Value]Value` would be faster but Go's map can't key on
// non-comparable interface values (Array, Struct, *Enum, *Map), and we
// want any K shape that type-checks to work end-to-end.
type Map struct {
	keys []Value
	vals []Value
	// rc is the copy-on-write reference count: the number of owning
	// slots (variables, struct fields, array/tuple elements, closure
	// captures) that hold this *Map. A freshly produced map (map_new /
	// clone) starts at 0 — an unowned temporary — and is bumped to 1 by
	// the store that binds it. Mutating methods (set/delete/clear)
	// mutate in place when rc <= 1 and copy when rc > 1, mirroring the
	// compiled runtime's rc-based COW (core/map.fern __map_cow_inplace)
	// so `fern -interp` agrees with every backend. See
	// docs/INTERP-MAP-COW-PLAN.md (M1).
	rc int
}

// clone returns an independent copy of the map (rc 0 — an unowned
// temporary the binding store will retain to 1). Used by set/delete/
// clear when the receiver is shared (rc > 1) so the mutation can't bleed
// into an aliased holder.
func (m *Map) clone() *Map {
	return &Map{
		keys: append([]Value(nil), m.keys...),
		vals: append([]Value(nil), m.vals...),
	}
}

// retain bumps the COW reference count of every Map reachable from v
// (v itself, or maps nested inside a struct / array / tuple / enum /
// map). Called at every store site — a binding, assignment, function
// argument, container element, or capture — because each creates a new
// owning slot. release is the exact inverse, called when an owning slot
// dies (scope exit, reassignment of the old value, function return). The
// net count is what set/delete/clear consult to decide mutate-in-place
// vs copy. See docs/INTERP-MAP-COW-PLAN.md (M1).
func retain(v Value)  { adjustRC(v, +1) }
func release(v Value) { adjustRC(v, -1) }

func adjustRC(v Value, delta int) {
	switch x := v.(type) {
	case *Map:
		x.rc += delta
		// Map values/keys can themselves be maps (Map[K, Map[...]]);
		// the owning map shares them, so the count flows through.
		for _, kv := range x.keys {
			adjustRC(kv, delta)
		}
		for _, vv := range x.vals {
			adjustRC(vv, delta)
		}
	case Array:
		for _, e := range x {
			adjustRC(e, delta)
		}
	case *Struct:
		for _, f := range x.Fields {
			adjustRC(f, delta)
		}
	case *Enum:
		for _, p := range x.Payloads {
			adjustRC(p, delta)
		}
	}
}

func (m *Map) findKey(k Value) int {
	for i, kk := range m.keys {
		if valuesEqual(kk, k) {
			return i
		}
	}
	return -1
}

// MapIter is the interp's `MapIter[K, V]` cursor — pairs the
// owning Map with the index of the entry the next `key()` /
// `value()` will report. `has_next` / `key` / `value` /
// `advance` walk the parallel slices in insertion order,
// matching what `keys()` / `values()` return.
//
// Live binding: the iterator holds the *Map by pointer, so
// set / delete on the underlying map during iteration is
// observed. Codegen has the same shape, so interp and native
// stay aligned even if user code mutates mid-loop.
type MapIter struct {
	m   *Map
	pos int
}

func (it *MapIter) String() string { return "<MapIter>" }

// Builtin is a host-provided function callable from interpreted code.
// It receives evaluated arguments and may emit output via the
// interpreter's stdout.
type Builtin struct {
	Fn func(*Interp, []Value) (Value, error)
}

// Closure pairs a function declaration with the lexical
// environment captured at its definition site. Used for two
// surface forms: local function declarations (`function f(...) {
// ... }` as a stmt) and Lambda expressions (`function (x): T
// { ... }` in expression position — the parser builds an
// `*ast.Lambda` which the interpreter wraps in a synthetic
// FuncDecl + the current env).
//
// Distinct from Func (a bare *ast.FuncDecl) — Func references a
// top-level decl whose body resolves identifiers against the
// global registry, while Closure carries its own captured env
// so reads of outer-scope names hit the right values.
type Closure struct {
	Decl *ast.FuncDecl
	Env  *env
}

func (c *Closure) String() string {
	if c.Decl != nil && c.Decl.Name != "" {
		return "<closure " + c.Decl.Name + ">"
	}
	return "<closure>"
}

// valueTypeName recovers the `methodTypeName` dispatch key from a
// runtime value — used by `dyn Trait` dynamic dispatch to resolve the
// concrete `__method_<Type>_<name>` from the receiver's runtime type.
// The interpreter's Number carries no width, so an integer maps to
// "i32" (dyn over wider integer types in the interpreter is a known
// slice-1 limitation; struct / enum / string trait objects — the
// primary use case — dispatch exactly). See docs/DYN-TRAITS.md §4.1.
func valueTypeName(v Value) (string, bool) {
	switch x := v.(type) {
	case *Struct:
		return x.TypeName, true
	case *Enum:
		return x.EnumName, true
	case String:
		return "string", true
	case Bool:
		return "boolean", true
	case Float:
		if x.Width == 64 {
			return "f64", true
		}
		return "f32", true
	case Number:
		return "i32", true
	}
	return "", false
}

// downcastTargetName returns the runtime type-name key for a downcast
// target type (`e as? T`). Slice 1 targets are struct/enum types, whose
// names line up with the TypeName/EnumName carried on the boxed value.
func downcastTargetName(t ast.Type) string {
	switch x := t.(type) {
	case ast.StructType:
		return x.Name
	case ast.EnumType:
		return x.Name
	}
	return ""
}

func (n Number) String() string { return fmt.Sprintf("%d", int64(n)) }
func (f Float) String() string {
	if f.Width == 32 {
		return strconv.FormatFloat(float64(float32(f.V)), 'g', -1, 32)
	}
	return strconv.FormatFloat(f.V, 'g', -1, 64)
}
func (b Bool) String() string {
	if b {
		return "true"
	}
	return "false"
}
func (s String) String() string { return string(s) }
func (Void) String() string     { return "" }
func (a Array) String() string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range a {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(v.String())
	}
	b.WriteByte(']')
	return b.String()
}
func (f Func) String() string  { return "function " + f.Decl.Name }
func (Builtin) String() string { return "<builtin>" }
func (s *Struct) String() string {
	var b strings.Builder
	b.WriteString(s.TypeName)
	b.WriteString(" { ")
	first := true
	for k, v := range s.Fields {
		if !first {
			b.WriteString(", ")
		}
		first = false
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v.String())
	}
	b.WriteString(" }")
	return b.String()
}

func (m *Map) String() string {
	var b strings.Builder
	b.WriteString("Map { ")
	for i, k := range m.keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k.String())
		b.WriteString(": ")
		b.WriteString(m.vals[i].String())
	}
	b.WriteString(" }")
	return b.String()
}

func (e *Enum) String() string {
	if len(e.Payloads) == 0 {
		return e.VariantName
	}
	var b strings.Builder
	b.WriteString(e.VariantName)
	b.WriteByte('(')
	for i, p := range e.Payloads {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.String())
	}
	b.WriteByte(')')
	return b.String()
}

// Interp owns global state: top-level user functions, host built-ins,
// the persistent REPL environment, and the writer used for `print` /
// `putchar` output.
type Interp struct {
	Funcs    map[string]*ast.FuncDecl
	Enums    map[string]*ast.EnumDecl
	Builtins map[string]*Builtin
	Stdout   io.Writer
	// Stderr is where `eprint` writes. Defaults to os.Stderr;
	// tests / the REPL can override it to capture diagnostic
	// output independently of Stdout.
	Stderr io.Writer
	// Stdin is where `read_line` reads. Defaults to os.Stdin;
	// tests / the REPL can override it to feed scripted input.
	Stdin io.Reader
	// Env is the source of `env(name)` lookups. nil means
	// fall through to os.LookupEnv; a populated map shadows the
	// process environment so tests can run hermetically.
	Env map[string]string
	// Exiter is invoked by the `exit(code)` builtin. Defaults to
	// os.Exit; tests override it to capture the requested code
	// without actually killing the process. A non-returning
	// function is expected — the interpreter has no story for
	// resuming after an exit call.
	Exiter func(code int)
	// openFiles maps the `fd` field of a Reader / Writer Struct
	// value back to the *os.File the interpreter is using on
	// the host side. The codegen backends use real OS file
	// descriptors directly; the interpreter shadows that with
	// a ticket-counter scheme so the same Struct-with-fd shape
	// works in both worlds.
	openFiles map[int64]*os.File
	nextFd    int64
	// deferStack is a per-call list of expressions to evaluate at
	// function exit, in LIFO order. callFunc / callClosure push
	// a fresh empty frame on entry, the `*ast.Defer` stmt
	// appends to the current frame, and the call's exit paths
	// (`return val`, falloff to Void) iterate the frame in
	// reverse before popping it. Nested calls get their own
	// frame — defers inside a callee don't run in the caller.
	// `onError` marks an `errdefer`: it runs only on an error
	// exit (`?` propagation or a `return` of None/Err).
	deferStack [][]deferEntry
	// tcpListeners / tcpConns are the interpreter's analogue
	// of OS socket fds. The AOT backends return raw kernel
	// fds; the interpreter returns opaque integer IDs into
	// these maps. The numeric value space is disjoint from
	// the AOT side, but every program treats the returned
	// number as an opaque token.
	tcpListeners  map[int64]tcpListenerHandle
	tcpConns      map[int64]tcpConnHandle
	tcpNextHandle int64
	// Args is what the `args()` builtin returns, in source-program
	// order (argv[0] first). REPL / test callers can override this
	// to feed scripted argv without going through os.Args.
	Args []string
	// Global is the env used by REPL-typed top-level statements;
	// `var x = 7` at the prompt declares x here so the next prompt
	// can read it.
	Global *env
	// strbuf is the interpreter's analogue of the compiled backends'
	// single global string-builder scratch buffer: strbuf_reset zeroes
	// it, strbuf_append adds a string's bytes, strbuf_take returns the
	// accumulated string and resets. (The AOT backends use a 64 MiB BSS
	// buffer; the interp just grows a []byte.)
	strbuf []byte
}

func New() *Interp {
	i := &Interp{
		Funcs:     map[string]*ast.FuncDecl{},
		Enums:     map[string]*ast.EnumDecl{},
		Builtins:  map[string]*Builtin{},
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Stdin:     os.Stdin,
		Exiter:    os.Exit,
		openFiles: map[int64]*os.File{},
		nextFd:    100,
		Global:    newEnv(nil),
	}
	i.Builtins["print"] = &Builtin{Fn: builtinPrint}
	i.Builtins["write"] = &Builtin{Fn: builtinWrite}
	i.Builtins["eprint"] = &Builtin{Fn: builtinEprint}
	i.Builtins["putchar"] = &Builtin{Fn: builtinPutchar}
	i.Builtins["poll"] = &Builtin{Fn: builtinPoll}
	// strbuf_reset() / strbuf_append(s) / strbuf_take() — the global
	// string-builder primitive (see checker FuncSigs); the compiled
	// backends back it with a 64 MiB BSS scratch buffer.
	i.Builtins["strbuf_reset"] = &Builtin{Fn: builtinStrbufReset}
	i.Builtins["strbuf_append"] = &Builtin{Fn: builtinStrbufAppend}
	i.Builtins["strbuf_take"] = &Builtin{Fn: builtinStrbufTake}
	// `x.len()` dispatches through three mangled names (one per
	// receiver type the checker registers a method on); all three
	// route to a single shared implementation that switches on the
	// value's runtime tag. Slices in the interpreter are
	// represented as Arrays, so the slice form joins the array path.
	i.Builtins["__method_string_len"] = &Builtin{Fn: builtinLen}
	i.Builtins["__method_Array_len"] = &Builtin{Fn: builtinLen}
	i.Builtins["__method_slice_len"] = &Builtin{Fn: builtinLen}
	i.Builtins["args"] = &Builtin{Fn: builtinArgs}
	i.Builtins["env"] = &Builtin{Fn: builtinEnv}
	i.Builtins["read_file"] = &Builtin{Fn: builtinReadFile}
	i.Builtins["write_file"] = &Builtin{Fn: builtinWriteFile}
	i.Builtins["open_reader"] = &Builtin{Fn: builtinOpenReader}
	i.Builtins["open_writer"] = &Builtin{Fn: builtinOpenWriter}
	i.Builtins["open_appender"] = &Builtin{Fn: builtinOpenAppender}
	i.Builtins["__method_Reader_read_line"] = &Builtin{Fn: builtinReaderReadLine}
	i.Builtins["__method_Reader_read_chunk"] = &Builtin{Fn: builtinReaderReadChunk}
	i.Builtins["__method_Reader_close"] = &Builtin{Fn: builtinReaderClose}
	i.Builtins["__method_Writer_write"] = &Builtin{Fn: builtinWriterWrite}
	i.Builtins["__method_Writer_close"] = &Builtin{Fn: builtinWriterClose}
	i.Builtins["__method_Array_push"] = &Builtin{Fn: builtinArrayPush}
	i.Builtins["__method_Array_set"] = &Builtin{Fn: builtinArraySet}
	// Map builtins. `map_new(cap)` returns an empty Map; the
	// per-method shims walk the parallel-slice representation
	// directly. Mirror the codegen surface from the checker's
	// `registerMapMethod` calls so user programs that go
	// through the interp see the same API as the native /
	// wasm backends.
	i.Builtins["map_new"] = &Builtin{Fn: builtinMapNew}
	// Cell[T] (docs/CELL-TYPE-PLAN.md).
	i.Builtins["cell_new"] = &Builtin{Fn: builtinCellNew}
	i.Builtins["__method_Cell_get"] = &Builtin{Fn: builtinCellGet}
	i.Builtins["__method_Cell_set"] = &Builtin{Fn: builtinCellSet}
	i.Builtins["__method_Map_len"] = &Builtin{Fn: builtinMapLen}
	i.Builtins["__method_Map_has"] = &Builtin{Fn: builtinMapHas}
	i.Builtins["__method_Map_get"] = &Builtin{Fn: builtinMapGet}
	i.Builtins["__method_Map_set"] = &Builtin{Fn: builtinMapSet}
	i.Builtins["__method_Map_delete"] = &Builtin{Fn: builtinMapDelete}
	i.Builtins["__method_Map_clear"] = &Builtin{Fn: builtinMapClear}
	i.Builtins["__method_Map_get_or"] = &Builtin{Fn: builtinMapGetOr}
	i.Builtins["__method_Map_keys"] = &Builtin{Fn: builtinMapKeys}
	i.Builtins["__method_Map_values"] = &Builtin{Fn: builtinMapValues}
	i.Builtins["__method_Map_iter"] = &Builtin{Fn: builtinMapIter}
	i.Builtins["__method_MapIter_has_next"] = &Builtin{Fn: builtinMapIterHasNext}
	i.Builtins["__method_MapIter_key"] = &Builtin{Fn: builtinMapIterKey}
	i.Builtins["__method_MapIter_value"] = &Builtin{Fn: builtinMapIterValue}
	i.Builtins["__method_MapIter_advance"] = &Builtin{Fn: builtinMapIterAdvance}
	// Low-level stdlib primitives the codegen lowers to inline
	// alloc / memcpy / store-byte sequences. The interpreter
	// implements them directly so stdlib functions that lean
	// on them (`__string_case_fold` for `to_upper`/`to_lower`,
	// `s.bytes()`, `string_from_bytes_unchecked`, etc.) round-trip
	// through the script-mode + playground path. Map runtime
	// primitives (`__alloc`, `__load_ptr`, `__store_ptr`,
	// `__memcpy`, `__memset`, `__store_i32`, `__load_i32`,
	// `__ptr_width`) are NOT included here — they pretend to
	// be a flat byte address space the interpreter doesn't
	// model, so Map operations stay codegen-only for now.
	i.Builtins["__alloc_u8"] = &Builtin{Fn: builtinAllocU8}
	i.Builtins["string_from_bytes_unchecked"] = &Builtin{Fn: builtinStringFromBytes}
	// `s.bytes()` and `s.as_bytes()` round-trip bytes through
	// raw memory in the stdlib / wat-emitted helper (the
	// former does `__memcpy(out as i32, s.as_bytes() as i32, n)`,
	// the latter aliases the string payload via a slice header).
	// Both are unrepresentable in the interp's value-tree heap,
	// so override the mangled entry points with direct
	// String→Array conversions that copy each byte. The two
	// methods diverge on aliasing semantics under codegen
	// (bytes() copies, as_bytes() shares); the interp returns
	// independent arrays for both, which is observably
	// equivalent under the value-tree model where Array is
	// already copy-on-write at the language level.
	i.Builtins["__method_string_bytes"] = &Builtin{Fn: builtinStringBytes}
	i.Builtins["__method_string_as_bytes"] = &Builtin{Fn: builtinStringBytes}
	i.Builtins["stdin"] = &Builtin{Fn: builtinStdin}
	i.Builtins["read_line"] = &Builtin{Fn: builtinReadLine}
	i.Builtins["stdout"] = &Builtin{Fn: builtinStdout}
	i.Builtins["stderr"] = &Builtin{Fn: builtinStderr}
	i.Builtins["exit"] = &Builtin{Fn: builtinExit}
	i.Builtins["random_bytes"] = &Builtin{Fn: builtinRandomBytes}
	i.Builtins["random_i32"] = &Builtin{Fn: builtinRandomI32}
	// `f32_bits(x)` / `f32_from_bits(n)` — reinterpret-cast pair.
	// The checker exposes them for raw-IEEE manipulation in user
	// code (Float-to-byte buffer encoders, NaN bit-pattern tests).
	// Now that the interp models floats, route them through Go's
	// math.Float32bits / math.Float32frombits at full precision.
	// __heap_bump_bytes(): the bump high-water mark on the compiled
	// backends. The interpreter uses Go's allocator (no bump cursor), so
	// there's nothing to report — return 0 so programs that probe it run
	// under -interp without erroring (the metric is only meaningful in
	// codegen).
	i.Builtins["__heap_bump_bytes"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
	// Bit-counting intrinsics. The interpreter is the ORACLE these are
	// differentialled against, so it uses math/bits rather than replicating
	// the SWAR sequence — an independent implementation is the point, since a
	// shared one would agree with a shared bug.
	//
	// Values arrive as float64-backed Numbers, so each masks to its declared
	// width before counting; without that a 32-bit clz would see the sign
	// extension of a negative value in the upper half and report 0.
	registerBitCount := func(name string, width int, count func(uint64, int) int) {
		i.Builtins[name] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			if len(args) != 1 {
				return nil, fmt.Errorf("%s: expected 1 arg, got %d", name, len(args))
			}
			n, ok := args[0].(Number)
			if !ok {
				return nil, fmt.Errorf("%s: expected an integer, got %T", name, args[0])
			}
			v := uint64(int64(n))
			if width == 32 {
				v &= 0xFFFFFFFF
			}
			return Number(count(v, width)), nil
		}}
	}
	clzOf := func(v uint64, width int) int {
		if v == 0 {
			return width
		}
		return bits.LeadingZeros64(v) - (64 - width)
	}
	ctzOf := func(v uint64, width int) int {
		if v == 0 {
			return width
		}
		return bits.TrailingZeros64(v)
	}
	popOf := func(v uint64, _ int) int { return bits.OnesCount64(v) }
	registerBitCount("__clz32", 32, clzOf)
	registerBitCount("__clz64", 64, clzOf)
	registerBitCount("__ctz32", 32, ctzOf)
	registerBitCount("__ctz64", 64, ctzOf)
	registerBitCount("__popcount32", 32, popOf)
	registerBitCount("__popcount64", 64, popOf)
	// __arr_push_shared_count(): the rc==1 cliff counter on the compiled
	// backends — appends that copied a buffer which still had room, so the
	// copy was bought by an extra reference. The interpreter has no refcounts
	// and copies nothing, so there is no cliff to cross: return 0, which is
	// also the healthy value on every backend. A program asserting the count
	// is 0 therefore passes under -interp for the right reason, and one
	// asserting it is NON-zero is a codegen-only test by construction.
	i.Builtins["__arr_push_shared_count"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
	// __arr_push_shared_bytes(): the same cliff weighted by bytes copied. No
	// cliff to cross under the interpreter, so no bytes copied either — 0,
	// for exactly the reason the counter beside it reports 0.
	i.Builtins["__arr_push_shared_bytes"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
	// __heap_mark() / __heap_release_to(mark): the arena checkpoint pair. The
	// interpreter is GC'd, so there is no cursor to capture or rewind — mark
	// hands back the same "no checkpoint" 0 the codegen backends use when
	// nothing has been allocated yet, and release_to is a no-op. A program
	// driving the pair therefore runs correctly under -interp; it just does
	// not observe reclamation, exactly as with __heap_bump_bytes.
	i.Builtins["__heap_mark"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
	i.Builtins["__heap_release_to"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return nil, nil }}
	i.Builtins["f32_bits"] = &Builtin{Fn: builtinF32Bits}
	i.Builtins["f32_from_bits"] = &Builtin{Fn: builtinF32FromBits}
	i.Builtins["f64_bits"] = &Builtin{Fn: builtinF64Bits}
	i.Builtins["f64_from_bits"] = &Builtin{Fn: builtinF64FromBits}
	// f64 math primitives. The Go-side implementation routes
	// through `math.*` for hardware-precise results. User code
	// reaches these through receiver methods in `std/float`
	// (`(x).sqrt()`, `.floor()`, …); the underscore-prefixed
	// bare names are the IR-level entry points.
	i.Builtins["__sqrt_f64"] = mkUnaryF64Builtin("__sqrt_f64", math.Sqrt)
	i.Builtins["__floor_f64"] = mkUnaryF64Builtin("__floor_f64", math.Floor)
	i.Builtins["__ceil_f64"] = mkUnaryF64Builtin("__ceil_f64", math.Ceil)
	i.Builtins["__round_f64"] = mkUnaryF64Builtin("__round_f64", math.Round)
	i.Builtins["__trunc_f64"] = mkUnaryF64Builtin("__trunc_f64", math.Trunc)
	i.Builtins["__abs_f64"] = mkUnaryF64Builtin("__abs_f64", math.Abs)
	i.Builtins["__log_f64"] = mkUnaryF64Builtin("__log_f64", math.Log)
	i.Builtins["__exp_f64"] = mkUnaryF64Builtin("__exp_f64", math.Exp)
	i.Builtins["__sin_f64"] = mkUnaryF64Builtin("__sin_f64", math.Sin)
	i.Builtins["__cos_f64"] = mkUnaryF64Builtin("__cos_f64", math.Cos)
	i.Builtins["__pow_f64"] = &Builtin{Fn: builtinPowF64}
	// `temp_dir(prefix)` + `exec(cmd, args, stdin)` back the
	// test-runner migration: ports of the Go-side e2e suite need
	// somewhere to write fixture files and a way to spawn the
	// compiler binary they're testing. Lang-the-language has
	// neither today (per `docs/ROADMAP-AND-SELF-HOSTING.md`
	// "Process spawning"), so these are interp-only — native /
	// wasm backends would fail at codegen for now. That's the
	// right trade for the migration: tests run under
	// `fern -interp` regardless of which backend they exercise.
	i.Builtins["now_unix_ms"] = &Builtin{Fn: builtinNowUnixMS}
	i.Builtins["now_ns"] = &Builtin{Fn: builtinNowNS}
	i.Builtins["monotonic_ns"] = &Builtin{Fn: builtinMonotonicNS}
	i.Builtins["sleep_ms"] = &Builtin{Fn: builtinSleepMS}
	i.Builtins["proc_fork"] = &Builtin{Fn: builtinProcFork}
	i.Builtins["proc_waitpid"] = &Builtin{Fn: builtinProcWaitpid}
	i.Builtins["proc_exec"] = &Builtin{Fn: builtinProcExec}
	i.Builtins["temp_dir"] = &Builtin{Fn: builtinTempDir}
	i.Builtins["read_dir"] = &Builtin{Fn: builtinReadDir}
	i.Builtins["stat"] = &Builtin{Fn: builtinStat}
	i.Builtins["remove_file"] = &Builtin{Fn: builtinRemoveFile}
	i.Builtins["remove_dir_all"] = &Builtin{Fn: builtinRemoveDirAll}
	i.Builtins["subprocess"] = &Builtin{Fn: builtinSubprocess}
	// `int_to_string` is the one Lang-defined stdlib function with
	// an interp Go override (the body uses raw-memory primitives
	// like `scratch as i32` that the interp can't model). Two
	// keys cover both load paths:
	//
	//   - bare `int_to_string` — the flat-load (LoadStdlibFlat) path, the
	//     usual single-file case.
	//   - mangled `int__int_to_string` — modload's name-mangling
	//     prefix when the user (or a transitively-imported stdlib
	//     module) explicitly `import`s a path that pulls in
	//     core/int. modload's combine step prepends `<modname>__`
	//     to non-receiver-method functions; `importLocalName` of
	//     `stdlib://core/int.fern` is `int`, so the form is
	//     `int__int_to_string`. Without the alias, the interp
	//     would fall through to the Lang body and crash on the
	//     `scratch as i32` cast.
	//
	// Add new aliases here whenever a stdlib function gains an
	// interp Go override or moves to a new module path.
	i.Builtins["int_to_string"] = &Builtin{Fn: builtinIntToString}
	i.Builtins["int__int_to_string"] = &Builtin{Fn: builtinIntToString}
	// `__int_to_string_u64` is the i64 / u32 / u64 formatter the
	// `(n).to_string()` method on each width dispatches to. Same
	// "Lang body uses raw-memory ops the interp can't model"
	// shape as int_to_string above, so it gets the same dual-key
	// registration (bare + modload-mangled).
	i.Builtins["__int_to_string_u64"] = &Builtin{Fn: builtinIntToStringU64}
	i.Builtins["int____int_to_string_u64"] = &Builtin{Fn: builtinIntToStringU64}
	i.Builtins["tcp_listen"] = &Builtin{Fn: builtinTcpListen}
	i.Builtins["tcp_accept"] = &Builtin{Fn: builtinTcpAccept}
	i.Builtins["tcp_recv"] = &Builtin{Fn: builtinTcpRecv}
	i.Builtins["tcp_send"] = &Builtin{Fn: builtinTcpSend}
	i.Builtins["tcp_close"] = &Builtin{Fn: builtinTcpClose}
	i.Builtins["tcp_pollable"] = &Builtin{Fn: builtinTcpPollable}
	i.Builtins["wasm_pollable_drop"] = &Builtin{Fn: builtinWasmPollableDrop}
	i.Builtins["wasm_timer_pollable"] = &Builtin{Fn: builtinWasmTimerPollable}
	i.Builtins["wasm_block"] = &Builtin{Fn: builtinWasmBlock}
	i.Builtins["wasm_poll"] = &Builtin{Fn: builtinWasmPoll}
	return i
}

// TCP socket builtins for the interpreter — implemented via
// Go's net package so REPL / test runs match the AOT
// backends' behaviour. Listener / connection handles are
// represented as integer IDs into per-Interp tables; the
// AOT backends use raw OS fds, the interpreter uses opaque
// indices into Go-managed maps. The numeric value space is
// disjoint between the two, but every program treats the
// returned number as an opaque token.

func builtinTcpListen(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("tcp_listen: expected 1 arg, got %d", len(args))
	}
	port, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_listen: expected number arg, got %T", args[0])
	}
	ln, err := tcpNetListen("tcp", fmt.Sprintf("0.0.0.0:%d", int(port)))
	if err != nil {
		return Number(-1), nil
	}
	if i.tcpListeners == nil {
		i.tcpListeners = map[int64]tcpListenerHandle{}
	}
	i.tcpNextHandle++
	id := i.tcpNextHandle
	i.tcpListeners[id] = ln
	return Number(id), nil
}

func builtinTcpAccept(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("tcp_accept: expected 1 arg, got %d", len(args))
	}
	id, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_accept: expected number arg, got %T", args[0])
	}
	ln, ok := i.tcpListeners[int64(id)]
	if !ok {
		return Number(-1), nil
	}
	conn, err := ln.Accept()
	if err != nil {
		return Number(-1), nil
	}
	if i.tcpConns == nil {
		i.tcpConns = map[int64]tcpConnHandle{}
	}
	i.tcpNextHandle++
	cid := i.tcpNextHandle
	i.tcpConns[cid] = conn
	return Number(cid), nil
}

func builtinTcpRecv(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("tcp_recv: expected 2 args, got %d", len(args))
	}
	id, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_recv: expected number fd arg, got %T", args[0])
	}
	max, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_recv: expected number max arg, got %T", args[1])
	}
	conn, ok := i.tcpConns[int64(id)]
	if !ok {
		return String(""), nil
	}
	buf := make([]byte, int(max))
	n, err := conn.Read(buf)
	if err != nil || n <= 0 {
		return String(""), nil
	}
	return String(buf[:n]), nil
}

func builtinTcpSend(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("tcp_send: expected 2 args, got %d", len(args))
	}
	id, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_send: expected number fd arg, got %T", args[0])
	}
	data, ok := args[1].(String)
	if !ok {
		return nil, fmt.Errorf("tcp_send: expected string data arg, got %T", args[1])
	}
	conn, ok := i.tcpConns[int64(id)]
	if !ok {
		return Number(-1), nil
	}
	n, err := conn.Write([]byte(data))
	if err != nil {
		return Number(-1), nil
	}
	return Number(n), nil
}

func builtinTcpClose(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("tcp_close: expected 1 arg, got %d", len(args))
	}
	id, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_close: expected number arg, got %T", args[0])
	}
	if conn, ok := i.tcpConns[int64(id)]; ok {
		conn.Close()
		delete(i.tcpConns, int64(id))
		return Number(0), nil
	}
	if ln, ok := i.tcpListeners[int64(id)]; ok {
		ln.Close()
		delete(i.tcpListeners, int64(id))
		return Number(0), nil
	}
	return Number(-1), nil
}

// builtinWasmPollableDrop is the interpreter's `wasm_pollable_drop(p)` — a
// no-op (returns 0), like the native backends: a pollable is just an fd, with
// no separate resource to drop. Present so std/async's fetch_future (which
// drops the wasm pollable before close) runs portably under interp.
func builtinWasmPollableDrop(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wasm_pollable_drop: expected 1 arg, got %d", len(args))
	}
	if _, ok := args[0].(Number); !ok {
		return nil, fmt.Errorf("wasm_pollable_drop: expected number arg, got %T", args[0])
	}
	return Number(0), nil
}

// builtinWasmBlock is the interpreter's `wasm_block(p)` — a no-op (returns 0),
// like the native backends: there's no pollable to wait on (a deadline is
// poll(2)'s timeout arg). Present so std/async's with_deadline (which blocks on
// a timer pollable on wasm) runs portably under interp.
func builtinWasmBlock(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wasm_block: expected 1 arg, got %d", len(args))
	}
	if _, ok := args[0].(Number); !ok {
		return nil, fmt.Errorf("wasm_block: expected number arg, got %T", args[0])
	}
	return Number(0), nil
}

// builtinWasmPoll is the interpreter's `wasm_poll(pollables)` — returns -1 (no
// ready index), matching the native stub: the interp has no real pollables (a
// wasm_timer_pollable is -1), so nothing is ever ready. On wasm this is the real
// wasi:io/poll.poll(list<pollable>) readiness multiplexer. std/async's reactor
// uses `poll` (real) natively and `wasm_poll` on wasm; this keeps the wasm path
// portable under interp.
func builtinWasmPoll(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wasm_poll: expected 1 arg, got %d", len(args))
	}
	if _, ok := args[0].(Array); !ok {
		return nil, fmt.Errorf("wasm_poll: expected array arg, got %T", args[0])
	}
	return Number(-1), nil
}

// builtinWasmTimerPollable is the interpreter's `wasm_timer_pollable(ns)` —
// returns -1 (no real pollable / no real poll under interp), matching the
// native stub. std/async's with_deadline appends it to the poll set; the interp
// `poll` stub returns -1 regardless, so timed waits never resolve here anyway.
func builtinWasmTimerPollable(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wasm_timer_pollable: expected 1 arg, got %d", len(args))
	}
	return Number(-1), nil
}

// builtinTcpPollable is the interpreter's `tcp_pollable(fd)` — identity, like
// the native backends (a socket's readiness token IS its fd). It lets
// std/async's `fetch_future` be portable; the interp has no real poll, so a
// Pending future built from it never resolves (the in-interp `poll` stub
// returns -1), exactly like the native/wasm portability story.
func builtinTcpPollable(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("tcp_pollable: expected 1 arg, got %d", len(args))
	}
	fd, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_pollable: expected number arg, got %T", args[0])
	}
	return fd, nil
}

// builtinRandomBytes returns a string of n cryptographic-
// quality random bytes from `crypto/rand`. Mirrors the AOT
// backends' `getrandom(2)` / WASI `random_get` behaviour.
func builtinRandomBytes(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("random_bytes: expected 1 arg, got %d", len(args))
	}
	n, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("random_bytes: expected number arg, got %T", args[0])
	}
	if n < 0 {
		return nil, fmt.Errorf("random_bytes: negative length %d", int(n))
	}
	buf := make([]byte, int(n))
	if _, err := cryptorand.Read(buf); err != nil {
		return nil, fmt.Errorf("random_bytes: %v", err)
	}
	return String(buf), nil
}

// builtinRandomI32 returns a single cryptographic-quality random
// i32 from `crypto/rand`. Mirrors the AOT backends'
// `getrandom(2)` / WASI `random_get` behaviour (read 4 bytes,
// reinterpret as a little-endian signed i32). Use when a single
// small random value is needed without the heap-allocation
// overhead of random_bytes.
func builtinRandomI32(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("random_i32: expected 0 args, got %d", len(args))
	}
	var buf [4]byte
	if _, err := cryptorand.Read(buf[:]); err != nil {
		return nil, fmt.Errorf("random_i32: %v", err)
	}
	v := uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
	return Number(int64(int32(v))), nil
}

// builtinF32Bits reinterprets a 32-bit float's bit pattern as
// a signed 32-bit integer. Round-trips through Go's
// `math.Float32bits` which canonicalises the value to its
// in-memory representation (matches what `f32.reinterpret_i32`
// produces on wasm and the ARM64 `fmov` equivalent).
func builtinF32Bits(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("f32_bits: expected 1 arg, got %d", len(args))
	}
	f, ok := args[0].(Float)
	if !ok {
		return nil, fmt.Errorf("f32_bits: expected float arg, got %T", args[0])
	}
	bits := math.Float32bits(float32(f.V))
	return Number(int64(int32(bits))), nil
}

// builtinF32FromBits is the inverse: takes a signed 32-bit
// integer interpreted as the IEEE-754 bit pattern and yields
// the matching f32 value. Round-trips NaN payloads (Go's
// `math.Float32frombits` does the no-op bit transfer).
func builtinF32FromBits(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("f32_from_bits: expected 1 arg, got %d", len(args))
	}
	n, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("f32_from_bits: expected number arg, got %T", args[0])
	}
	bits := uint32(int32(int64(n)))
	v := float64(math.Float32frombits(bits))
	return Float{V: v, Width: 32}, nil
}

// builtinF64Bits / builtinF64FromBits — same shape as the
// f32 pair but for double-precision. The bit pattern is a
// signed 64-bit integer (lang's `i64`); negative-bit-13
// quiet NaN payloads round-trip cleanly via Go's `math`
// package which does the no-op bit transfer.
func builtinF64Bits(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("f64_bits: expected 1 arg, got %d", len(args))
	}
	f, ok := args[0].(Float)
	if !ok {
		return nil, fmt.Errorf("f64_bits: expected float arg, got %T", args[0])
	}
	return Number(int64(math.Float64bits(f.V))), nil
}

func builtinF64FromBits(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("f64_from_bits: expected 1 arg, got %d", len(args))
	}
	n, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("f64_from_bits: expected number arg, got %T", args[0])
	}
	return Float{V: math.Float64frombits(uint64(int64(n))), Width: 64}, nil
}

// mkUnaryF64Builtin packages a unary `f64 -> f64` Go function
// as an interpreter `Builtin`. Used to register the f64 math
// primitives — `__sqrt_f64`, `__floor_f64`, etc. — which all
// share this shape. Width is preserved at 64; NaN / ±Inf
// propagate naturally via the underlying Go math.* call.
func mkUnaryF64Builtin(name string, fn func(float64) float64) *Builtin {
	return &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("%s: expected 1 arg, got %d", name, len(args))
		}
		f, ok := args[0].(Float)
		if !ok {
			return nil, fmt.Errorf("%s: expected float arg, got %T", name, args[0])
		}
		return Float{V: fn(f.V), Width: 64}, nil
	}}
}

// builtinPowF64 is the one binary f64 math primitive — the
// rest fit `unaryF64`'s shape. Mirrors `math.Pow` semantics,
// including the IEEE-754 special cases (pow(NaN, 0) == 1,
// pow(±0, negative) == ±Inf, etc.).
func builtinPowF64(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("__pow_f64: expected 2 args, got %d", len(args))
	}
	x, ok := args[0].(Float)
	if !ok {
		return nil, fmt.Errorf("__pow_f64: expected float arg 0, got %T", args[0])
	}
	y, ok := args[1].(Float)
	if !ok {
		return nil, fmt.Errorf("__pow_f64: expected float arg 1, got %T", args[1])
	}
	return Float{V: math.Pow(x.V, y.V), Width: 64}, nil
}

func builtinIntToString(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("int_to_string: expected 1 arg, got %d", len(args))
	}
	n, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("int_to_string: expected number arg, got %T", args[0])
	}
	return String(strconv.Itoa(int(n))), nil
}

// builtinIntToStringU64 formats the (mag i64, neg i32) shape
// the std/i64 / std/u32 / std/u64 receiver-method `to_string`
// dispatches into. `mag` is the absolute value; `neg` is the
// sign flag (1 for negative, 0 for positive — accommodates
// the INT64_MIN special case where two's-complement negation
// wraps to itself, so the caller passes the unsigned u64
// magnitude separately). Output is `"-" + decimal(mag)` when
// `neg != 0`, plain decimal otherwise.
//
// Mirrors the Lang body in `core/int.fern` byte-for-byte;
// only difference is the Lang version pokes raw memory which
// the interp can't model, hence the Go override.
func builtinIntToStringU64(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("__int_to_string_u64: expected 2 args, got %d", len(args))
	}
	mag, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("__int_to_string_u64: expected number mag, got %T", args[0])
	}
	neg, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("__int_to_string_u64: expected number neg, got %T", args[1])
	}
	// `Number` is int64 under the hood; treat as unsigned for
	// the u64 / u32 callers by re-reading the bits via uint64.
	out := strconv.FormatUint(uint64(int64(mag)), 10)
	if int64(neg) != 0 {
		out = "-" + out
	}
	return String(out), nil
}

// `(arr: T[]) push(v: T): T[]` — functional append. The codegen
// path implements push as "alloc a fresh T[] of len+1, memcpy
// the old elements, store the new one at the tail, return the
// new array"; the interpreter mirrors that with a fresh Go
// slice so the source array stays untouched. Matches the
// receiver-as-first-arg convention the checker uses for every
// `__method_*` mangled name.
func builtinArrayPush(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("__method_Array_push: expected 2 args (arr, v), got %d", len(args))
	}
	arr, ok := args[0].(Array)
	if !ok {
		return nil, fmt.Errorf("__method_Array_push: receiver must be array, got %T", args[0])
	}
	out := make(Array, len(arr)+1)
	copy(out, arr)
	out[len(arr)] = args[1]
	return out, nil
}

// builtinArraySet is the value-returning element set behind
// `arr.set(i, v)` / `arr.with(i, v)` — codegen lowers it to the CoW
// `__fern_arr_cow_inplace` shape (a possibly-fresh array with element
// i replaced); the interpreter mirrors that with a fresh Go slice so
// the source array stays untouched. Receiver-as-first-arg, matching
// the codegen surface registered in checker.go.
func builtinArraySet(_ *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("__method_Array_set: expected 3 args (arr, i, v), got %d", len(args))
	}
	arr, ok := args[0].(Array)
	if !ok {
		return nil, fmt.Errorf("__method_Array_set: receiver must be array, got %T", args[0])
	}
	idx, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("__method_Array_set: index must be number, got %T", args[1])
	}
	if idx < 0 || int(idx) >= len(arr) {
		return nil, fmt.Errorf("__method_Array_set: index %d out of range [0, %d)", int(idx), len(arr))
	}
	out := make(Array, len(arr))
	copy(out, arr)
	out[int(idx)] = args[2]
	return out, nil
}

// Map builtins. `map_new(cap)` ignores the capacity hint (the
// parallel-slice rep grows on demand) and returns a fresh
// empty *Map. The `__method_Map_*` shims mirror the codegen
// surface registered in internal/checker/checker.go's
// registerMapMethod calls — same signatures, same return
// shapes (Option[V] for get, boolean for has/delete, etc.).
// Entry order matches the runtime exactly — append on set,
// swap-with-last on delete — so keys() / values() / iteration
// round-trip identically across the diff oracle.
func builtinMapNew(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("map_new: expected 1 arg (cap), got %d", len(args))
	}
	return &Map{}, nil
}

// Cell[T] builtins — a single-slot mutable box. `set` mutates the shared
// *Cell in place and returns Void (it's the sanctioned in-place mutation).
func builtinCellNew(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("cell_new: expected 1 arg, got %d", len(args))
	}
	return &Cell{V: args[0]}, nil
}

func cellReceiver(name string, args []Value) (*Cell, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("%s: expected at least 1 arg (receiver)", name)
	}
	c, ok := args[0].(*Cell)
	if !ok {
		return nil, fmt.Errorf("%s: receiver must be Cell, got %T", name, args[0])
	}
	return c, nil
}

func builtinCellGet(_ *Interp, args []Value) (Value, error) {
	c, err := cellReceiver("Cell.get", args)
	if err != nil {
		return nil, err
	}
	return c.V, nil
}

func builtinCellSet(_ *Interp, args []Value) (Value, error) {
	c, err := cellReceiver("Cell.set", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("Cell.set: expected 2 args, got %d", len(args))
	}
	c.V = args[1]
	return Void{}, nil
}

func mapReceiver(name string, args []Value) (*Map, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("%s: expected at least 1 arg (receiver)", name)
	}
	m, ok := args[0].(*Map)
	if !ok {
		return nil, fmt.Errorf("%s: receiver must be Map, got %T", name, args[0])
	}
	return m, nil
}

func builtinMapLen(_ *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_len", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_Map_len: expected 1 arg (receiver), got %d", len(args))
	}
	return Number(int64(len(m.keys))), nil
}

func builtinMapHas(_ *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_has", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("__method_Map_has: expected 2 args (m, k), got %d", len(args))
	}
	return Bool(m.findKey(args[1]) >= 0), nil
}

func builtinMapGet(_ *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_get", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("__method_Map_get: expected 2 args (m, k), got %d", len(args))
	}
	if idx := m.findKey(args[1]); idx >= 0 {
		return optionSome(m.vals[idx]), nil
	}
	return optionNone(), nil
}

func builtinMapSet(_ *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_set", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 3 {
		return nil, fmt.Errorf("__method_Map_set: expected 3 args (m, k, v), got %d", len(args))
	}
	t := cowTarget(m)
	if idx := t.findKey(args[1]); idx >= 0 {
		t.vals[idx] = args[2]
	} else {
		t.keys = append(t.keys, args[1])
		t.vals = append(t.vals, args[2])
	}
	return t, nil // value-returning; t is m (in-place) or a fresh copy (shared)
}

// cowTarget returns the map a mutating method should write to: the
// receiver itself when it has at most one owner (mutate in place), or a
// fresh copy when it is shared (rc > 1) so the mutation can't bleed into
// an aliased holder. Mirrors core/map.fern's __map_cow_inplace.
func cowTarget(m *Map) *Map {
	if m.rc <= 1 {
		return m
	}
	return m.clone()
}

func builtinMapDelete(_ *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_delete", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("__method_Map_delete: expected 2 args (m, k), got %d", len(args))
	}
	idx := m.findKey(args[1])
	if idx < 0 {
		return Array{m, Bool(false)}, nil // unchanged; in-place receiver is fine
	}
	t := cowTarget(m)
	if t != m {
		idx = t.findKey(args[1]) // re-find in the copy (same order, but be explicit)
	}
	// Swap-with-last, mirroring core/map.fern's __map_delete_impl: move
	// the final entry into the removed slot and truncate. This keeps the
	// interpreter's iteration order identical to the compiled runtime's
	// after a delete (the runtime can't cheaply shift-down its open-
	// addressed entries array). See docs/ADVERSARIAL-REVIEW-2026-06.md (M3).
	last := len(t.keys) - 1
	t.keys[idx] = t.keys[last]
	t.vals[idx] = t.vals[last]
	t.keys = t.keys[:last]
	t.vals = t.vals[:last]
	return Array{t, Bool(true)}, nil
}

func builtinMapClear(_ *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_clear", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_Map_clear: expected 1 arg (receiver), got %d", len(args))
	}
	if m.rc > 1 {
		return &Map{}, nil // shared: hand back a fresh empty map, leave the holders intact
	}
	m.keys = m.keys[:0]
	m.vals = m.vals[:0]
	return m, nil // Map.clear is value-returning; return the map handle.
}

func builtinMapGetOr(_ *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_get_or", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 3 {
		return nil, fmt.Errorf("__method_Map_get_or: expected 3 args (m, k, default), got %d", len(args))
	}
	if idx := m.findKey(args[1]); idx >= 0 {
		return m.vals[idx], nil
	}
	return args[2], nil
}

func builtinMapKeys(_ *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_keys", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_Map_keys: expected 1 arg (receiver), got %d", len(args))
	}
	out := make(Array, len(m.keys))
	copy(out, m.keys)
	return out, nil
}

func builtinMapValues(_ *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_values", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_Map_values: expected 1 arg (receiver), got %d", len(args))
	}
	out := make(Array, len(m.vals))
	copy(out, m.vals)
	return out, nil
}

func builtinMapIter(_ *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_iter", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_Map_iter: expected 1 arg (receiver), got %d", len(args))
	}
	return &MapIter{m: m, pos: 0}, nil
}

func mapIterReceiver(name string, args []Value) (*MapIter, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("%s: expected at least 1 arg (receiver)", name)
	}
	it, ok := args[0].(*MapIter)
	if !ok {
		return nil, fmt.Errorf("%s: receiver must be MapIter, got %T", name, args[0])
	}
	return it, nil
}

func builtinMapIterHasNext(_ *Interp, args []Value) (Value, error) {
	it, err := mapIterReceiver("__method_MapIter_has_next", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_MapIter_has_next: expected 1 arg (receiver), got %d", len(args))
	}
	return Bool(it.pos < len(it.m.keys)), nil
}

func builtinMapIterKey(_ *Interp, args []Value) (Value, error) {
	it, err := mapIterReceiver("__method_MapIter_key", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_MapIter_key: expected 1 arg (receiver), got %d", len(args))
	}
	if it.pos >= len(it.m.keys) {
		return nil, fmt.Errorf("__method_MapIter_key: iterator exhausted")
	}
	return it.m.keys[it.pos], nil
}

func builtinMapIterValue(_ *Interp, args []Value) (Value, error) {
	it, err := mapIterReceiver("__method_MapIter_value", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_MapIter_value: expected 1 arg (receiver), got %d", len(args))
	}
	if it.pos >= len(it.m.vals) {
		return nil, fmt.Errorf("__method_MapIter_value: iterator exhausted")
	}
	return it.m.vals[it.pos], nil
}

func builtinMapIterAdvance(_ *Interp, args []Value) (Value, error) {
	it, err := mapIterReceiver("__method_MapIter_advance", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_MapIter_advance: expected 1 arg (receiver), got %d", len(args))
	}
	it.pos++
	return Void{}, nil
}

// `__alloc_u8(n: i32): u8[]` — codegen lowers to `__fern_alloc(n)
// + length-prefix poke`; the interp returns a fresh Array of n
// Number(0) values. The stdlib uses this as the staging buffer
// for `__string_case_fold`, `string_from_bytes_unchecked`'s round-trip
// counterpart, and any user code that wants a zero-initialised
// byte slab.
func builtinAllocU8(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("__alloc_u8: expected 1 arg (n), got %d", len(args))
	}
	n, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("__alloc_u8: arg must be number, got %T", args[0])
	}
	if n < 0 {
		return nil, fmt.Errorf("__alloc_u8: negative length %d", int64(n))
	}
	out := make(Array, int(n))
	for i := range out {
		out[i] = Number(0)
	}
	return out, nil
}

// `string_from_bytes_unchecked(bs: u8[]): string` — joins the byte
// values into a fresh String. Codegen path mmap-allocates a
// length-prefixed string buffer and memcpys; the interp
// builds the string directly from the Number values in the
// Array, narrowing each to a single byte (low 8 bits) so a
// caller-side width mismatch doesn't leak garbage into the
// result.
func builtinStringFromBytes(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("string_from_bytes_unchecked: expected 1 arg (bs), got %d", len(args))
	}
	arr, ok := args[0].(Array)
	if !ok {
		return nil, fmt.Errorf("string_from_bytes_unchecked: arg must be array, got %T", args[0])
	}
	buf := make([]byte, len(arr))
	for i, v := range arr {
		n, ok := v.(Number)
		if !ok {
			return nil, fmt.Errorf("string_from_bytes_unchecked: element %d not a number (%T)", i, v)
		}
		buf[i] = byte(int64(n) & 0xff)
	}
	return String(buf), nil
}

// `__method_string_bytes` / `__method_string_as_bytes` —
// String → Array<Number> conversion, one Number per UTF-8
// byte. Sidesteps the stdlib's `__memcpy(out as i32,
// s.as_bytes() as i32, n)` path which can't be modelled
// without a flat byte address space.
func builtinStringBytes(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_string_bytes: expected 1 arg (s), got %d", len(args))
	}
	s, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("__method_string_bytes: receiver must be string, got %T", args[0])
	}
	raw := []byte(s)
	out := make(Array, len(raw))
	for i, b := range raw {
		out[i] = Number(int64(b))
	}
	return out, nil
}

// builtinEnv looks up an environment variable. An explicit i.Env
// map shadows the process environment so tests can run with a
// scripted environment; nil falls through to os.LookupEnv.
// Missing keys return `None`; present keys (including those set
// to empty) return `Some(value)`.
func builtinEnv(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("env: expected 1 arg, got %d", len(args))
	}
	name, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("env: expected string arg, got %T", args[0])
	}
	if i.Env != nil {
		v, present := i.Env[string(name)]
		if !present {
			return optionNone(), nil
		}
		return optionSome(String(v)), nil
	}
	v, present := os.LookupEnv(string(name))
	if !present {
		return optionNone(), nil
	}
	return optionSome(String(v)), nil
}

// optionSome / optionNone wrap a value into the canonical
// Option enum's variant — index 0 for Some, 1 for None. The
// interpreter mirrors the codegen's tag convention so a match
// arm `Some(v)` extracts the right payload.
func optionSome(v Value) *Enum {
	return &Enum{EnumName: "Option", VariantName: "Some", Index: 0, Payloads: []Value{v}}
}

func optionNone() *Enum {
	return &Enum{EnumName: "Option", VariantName: "None", Index: 1}
}

// builtinReadFile is the interpreter analogue of the
// $read_file / __fern_read_file runtime helpers. Reads the
// file in one shot, builds a Result[string, IoError] enum
// value, and returns it. Errors come from os.ReadFile and get
// classified via classifyIoError.
func builtinReadFile(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("read_file: expected 1 arg, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("read_file: expected string arg, got %T", args[0])
	}
	data, err := os.ReadFile(string(path))
	if err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	return resultOk(String(string(data))), nil
}

// builtinWriteFile mirrors $write_file / __fern_write_file:
// truncate-write the content, return Option[IoError]
// (None = success).
func builtinWriteFile(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("write_file: expected 2 args, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("write_file: expected string path, got %T", args[0])
	}
	content, ok := args[1].(String)
	if !ok {
		return nil, fmt.Errorf("write_file: expected string content, got %T", args[1])
	}
	if err := os.WriteFile(string(path), []byte(content), 0o644); err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	return resultOk(unitValue()), nil
}

// builtinNowUnixMS returns wall-clock milliseconds since
// 1970-01-01 UTC. NTP-adjustable; use `monotonic_ns` for
// timing. Wraps `time.Now().UnixMilli()`.
func builtinNowUnixMS(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("now_unix_ms: expected 0 args, got %d", len(args))
	}
	return Number(time.Now().UnixMilli()), nil
}

// builtinNowNS returns wall-clock nanoseconds since 1970-01-01
// UTC — the nanosecond-resolution twin of now_unix_ms (same
// realtime clock). NTP-adjustable; use `monotonic_ns` for
// timing. Wraps `time.Now().UnixNano()`.
func builtinNowNS(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("now_ns: expected 0 args, got %d", len(args))
	}
	return Number(time.Now().UnixNano()), nil
}

// builtinMonotonicNS returns nanoseconds from a monotonic
// clock. `time.Now()` in Go carries a monotonic reading on
// every supported platform; `UnixNano` exposes the
// monotonic-aware nanosecond timestamp. The exact reference
// point isn't observable — only deltas between calls matter.
func builtinMonotonicNS(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("monotonic_ns: expected 0 args, got %d", len(args))
	}
	return Number(time.Now().UnixNano()), nil
}

// builtinSleepMS pauses for the given duration (milliseconds).
// Negative / zero inputs yield immediate return (no spurious
// wakeup); Go's runtime may delay actual wakeup under load.
func builtinSleepMS(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("sleep_ms: expected 1 arg, got %d", len(args))
	}
	ms, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("sleep_ms: expected number arg, got %T", args[0])
	}
	if int64(ms) > 0 {
		time.Sleep(time.Duration(int64(ms)) * time.Millisecond)
	}
	return Void{}, nil
}

// builtinProcFork mirrors the native `proc_fork()` builtin
// (docs/CRASH-ONLY-SERVE.md D2') — except the interpreter can
// never actually fork: the Go runtime is threaded, and a raw
// fork(2) in a multithreaded process leaves the child with every
// lock/state snapshot but only one thread — undefined behaviour.
// So the interp's answer is a permanent -38 (ENOSYS). Callers
// (std/tcp's tcp_serve_supervised) detect it and degrade to
// plain single-process serving, keeping the function runnable
// under `fern -interp` and on any future fork-less target.
func builtinProcFork(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("proc_fork: expected 0 args, got %d", len(args))
	}
	return Number(-38), nil // -ENOSYS
}

// builtinProcExec mirrors the native `proc_exec(path, args)`. Exec'ing here
// would replace the interpreter process itself, so it is refused the same way
// proc_fork is: -38 (ENOSYS), letting a caller detect "no process control" and
// degrade rather than mysteriously vanishing.
func builtinProcExec(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("proc_exec: expected 2 args, got %d", len(args))
	}
	return Number(-38), nil // -ENOSYS
}

// builtinProcWaitpid mirrors the native `proc_waitpid(pid)`. The
// interp's proc_fork never creates children, so there is never a
// child to reap: -10 (ECHILD) unconditionally.
func builtinProcWaitpid(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("proc_waitpid: expected 1 arg, got %d", len(args))
	}
	if _, ok := args[0].(Number); !ok {
		return nil, fmt.Errorf("proc_waitpid: expected number arg, got %T", args[0])
	}
	return Number(-10), nil // -ECHILD
}

// builtinTempDir creates a fresh temporary directory and
// returns its absolute path inside `Result[string, IoError]`.
// `prefix` is appended to a unique random suffix the OS picks
// — `MkdirTemp` lays it out under `os.TempDir()` (`/tmp` on
// Linux, the macOS equivalent on Darwin). No automatic
// cleanup: callers are expected to either rely on OS-tier
// scrubbing (CI runners, system tmpfs reboot purge) or to
// build their own delete-on-finish flow once Lang grows a
// `remove_dir` primitive.
func builtinTempDir(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("temp_dir: expected 1 arg, got %d", len(args))
	}
	prefix, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("temp_dir: expected string prefix, got %T", args[0])
	}
	dir, err := os.MkdirTemp("", string(prefix)+"-*")
	if err != nil {
		return resultErr(classifyIoError(string(prefix), err)), nil
	}
	return resultOk(String(dir)), nil
}

// builtinReadDir lists the immediate children of `path` —
// base names only, no recursion, unsorted. Wraps Go's
// `os.ReadDir` which is the same shape; the only translation
// is wrapping the result in `Result[string[], IoError]`.
func builtinReadDir(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("read_dir: expected 1 arg, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("read_dir: expected string path, got %T", args[0])
	}
	entries, err := os.ReadDir(string(path))
	if err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	out := make(Array, len(entries))
	for i, e := range entries {
		out[i] = String(e.Name())
	}
	return resultOk(out), nil
}

// builtinStat returns file metadata wrapped in `Result[FileStat,
// IoError]`. `is_file` / `is_dir` distinguish regular files
// from directories; `size` is the byte size for regular files
// and (on POSIX) the directory-entry size for directories.
// Symlinks resolve through `os.Stat` (follow), matching the
// implicit contract of every other file-touching builtin.
func builtinStat(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("stat: expected 1 arg, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("stat: expected string path, got %T", args[0])
	}
	info, err := os.Stat(string(path))
	if err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	st := &Struct{
		TypeName: "FileStat",
		Fields: map[string]Value{
			"is_file": Bool(info.Mode().IsRegular()),
			"is_dir":  Bool(info.IsDir()),
			"size":    Number(info.Size()),
		},
	}
	return resultOk(st), nil
}

// builtinRemoveFile unlinks `path`. `Option[IoError]` mirrors
// `write_file`'s "None on success" shape. Removing a non-
// existent file surfaces as `Some(NotFound(...))` — Go's
// `os.Remove` errors on missing target, and we preserve that
// so a typo doesn't silently succeed.
func builtinRemoveFile(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("remove_file: expected 1 arg, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("remove_file: expected string path, got %T", args[0])
	}
	if err := os.Remove(string(path)); err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	return resultOk(unitValue()), nil
}

// builtinRemoveDirAll recursively removes `path`. Mirrors
// Go's `os.RemoveAll` semantics: missing target is silently
// OK, permission / read-only-filesystem errors surface via
// `Some(IoError)`. Used by tests to scrub `temp_dir` output
// at the end of a run.
func builtinRemoveDirAll(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("remove_dir_all: expected 1 arg, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("remove_dir_all: expected string path, got %T", args[0])
	}
	if err := os.RemoveAll(string(path)); err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	return resultOk(unitValue()), nil
}

// builtinSubprocess spawns `cmd` with `args` as its argv (NOT
// including the executable name itself; the caller supplies
// it separately as the first arg). `stdin_text` is fed to the
// child's standard input — pass `""` when the child reads
// nothing. Returns a ProcessResult struct populated with
// captured stdout / stderr / exit_code; spawn failures
// surface as exit_code=127 with the OS error in stderr.
//
// Output is captured in memory — there's no streaming API.
// Tests that produce huge output should pipe to a file via
// shell redirection and read it back through `read_file`.
func builtinSubprocess(_ *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("subprocess: expected 3 args (cmd, args, stdin), got %d", len(args))
	}
	cmdStr, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("subprocess: expected string cmd, got %T", args[0])
	}
	argsArr, ok := args[1].(Array)
	if !ok {
		return nil, fmt.Errorf("subprocess: expected string[] args, got %T", args[1])
	}
	stdinText, ok := args[2].(String)
	if !ok {
		return nil, fmt.Errorf("subprocess: expected string stdin, got %T", args[2])
	}
	argv := make([]string, len(argsArr))
	for i, a := range argsArr {
		s, isStr := a.(String)
		if !isStr {
			return nil, fmt.Errorf("subprocess: args[%d] not a string (%T)", i, a)
		}
		argv[i] = string(s)
	}
	c := exec.Command(string(cmdStr), argv...)
	if len(stdinText) > 0 {
		c.Stdin = strings.NewReader(string(stdinText))
	}
	var outBuf, errBuf bytes.Buffer
	c.Stdout = &outBuf
	c.Stderr = &errBuf
	runErr := c.Run()
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Spawn failure (binary not found, permission
			// denied, etc). Use 127 — the POSIX shell "command
			// not found" convention — plus the OS error
			// message in stderr so callers that don't gate on
			// the exact code still see the reason.
			exitCode = 127
			if errBuf.Len() > 0 {
				errBuf.WriteByte('\n')
			}
			errBuf.WriteString(runErr.Error())
		}
	}
	return &Struct{
		TypeName: "ProcessResult",
		Fields: map[string]Value{
			"stdout":    String(outBuf.String()),
			"stderr":    String(errBuf.String()),
			"exit_code": Number(int64(exitCode)),
		},
	}, nil
}

// classifyIoError turns a Go error into the matching IoError
// variant. The mapping mirrors the codegen-side errno tables
// (NotFound, PermissionDenied, AlreadyExists, …) so that the
// interpreter and the backends agree on how to surface the
// same kind of failure.
func classifyIoError(path string, err error) *Enum {
	switch {
	case os.IsNotExist(err):
		return &Enum{EnumName: "IoError", VariantName: "NotFound", Index: 0,
			Payloads: []Value{String(path)}}
	case os.IsPermission(err):
		return &Enum{EnumName: "IoError", VariantName: "PermissionDenied", Index: 1,
			Payloads: []Value{String(path)}}
	case os.IsExist(err):
		return &Enum{EnumName: "IoError", VariantName: "AlreadyExists", Index: 2,
			Payloads: []Value{String(path)}}
	}
	return &Enum{EnumName: "IoError", VariantName: "Other", Index: 6,
		Payloads: []Value{String(path), String(err.Error())}}
}

// resultOk / resultErr wrap a value into the canonical
// Result enum's variant.
// unitValue is `()` — the payload of a Result that succeeded with
// nothing to report. Same representation a UnitLit evaluates to, so
// `Ok(u)` binds the same value however the Ok was built.
func unitValue() Value { return Number(0) }

func resultOk(v Value) *Enum {
	return &Enum{EnumName: "Result", VariantName: "Ok", Index: 0, Payloads: []Value{v}}
}

func resultErr(v Value) *Enum {
	return &Enum{EnumName: "Result", VariantName: "Err", Index: 1, Payloads: []Value{v}}
}

// builtinExit calls i.Exiter, which defaults to os.Exit. Tests
// override Exiter to capture the requested code; the substitute
// is expected to be non-returning (panic, longjmp-style abort,
// or test-only "remember the code and noop").
// builtinOpenReader / builtinOpenWriter / builtinOpenAppender
// open the file with the corresponding os.OpenFile flags,
// register the *os.File in i.openFiles under a fresh ticket id,
// and wrap the id in a Reader / Writer Struct that goes back
// into Result[Reader|Writer, IoError].
func builtinOpenReader(i *Interp, args []Value) (Value, error) {
	return openHelper(i, args, "Reader", os.O_RDONLY, 0)
}

func builtinOpenWriter(i *Interp, args []Value) (Value, error) {
	return openHelper(i, args, "Writer", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
}

func builtinOpenAppender(i *Interp, args []Value) (Value, error) {
	return openHelper(i, args, "Writer", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
}

func openHelper(i *Interp, args []Value, structName string, flag int, perm os.FileMode) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("open_*: expected 1 arg, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("open_*: expected string arg, got %T", args[0])
	}
	f, err := os.OpenFile(string(path), flag, perm)
	if err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	id := i.nextFd
	i.nextFd++
	i.openFiles[id] = f
	s := &Struct{
		TypeName: structName,
		Fields:   map[string]Value{"fd": Number(id)},
	}
	return resultOk(s), nil
}

// readerStream / writerStream return the io.Reader / io.Writer
// the methods should operate on. fd 0 routes to i.Stdin, 1 to
// i.Stdout, 2 to i.Stderr (so tests can override them); other
// fds resolve through the openFiles ticket map populated by
// open_reader / open_writer / open_appender.
func readerStream(i *Interp, v Value) (io.Reader, error) {
	fd, err := streamFd(v)
	if err != nil {
		return nil, err
	}
	switch fd {
	case 0:
		return i.Stdin, nil
	}
	f, ok := i.openFiles[fd]
	if !ok {
		return nil, fmt.Errorf("Reader with fd=%d not registered (closed already?)", fd)
	}
	return f, nil
}

func writerStream(i *Interp, v Value) (io.Writer, error) {
	fd, err := streamFd(v)
	if err != nil {
		return nil, err
	}
	switch fd {
	case 1:
		return i.Stdout, nil
	case 2:
		return i.Stderr, nil
	}
	f, ok := i.openFiles[fd]
	if !ok {
		return nil, fmt.Errorf("Writer with fd=%d not registered (closed already?)", fd)
	}
	return f, nil
}

func streamFd(v Value) (int64, error) {
	s, ok := v.(*Struct)
	if !ok {
		return 0, fmt.Errorf("expected Reader/Writer struct, got %T", v)
	}
	fd, ok := s.Fields["fd"].(Number)
	if !ok {
		return 0, fmt.Errorf("Reader/Writer.fd not a number")
	}
	return int64(fd), nil
}

func builtinReaderReadLine(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("Reader.read_line: expected 1 arg")
	}
	r, err := readerStream(i, args[0])
	if err != nil {
		return nil, err
	}
	var buf []byte
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n > 0 {
			buf = append(buf, one[0])
			if one[0] == '\n' {
				return optionSome(String(string(buf))), nil
			}
		}
		if err != nil {
			if len(buf) == 0 {
				return optionNone(), nil
			}
			return optionSome(String(string(buf))), nil
		}
	}
}

// builtinReadLine implements the bare `read_line(): Option[string]`
// builtin — reads one line from stdin (i.Stdin), including the
// trailing '\n' if present. Returns Some(line) or None at EOF
// before any byte. Mirrors builtinReaderReadLine but reads stdin
// directly rather than a Reader receiver.
func builtinReadLine(i *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("read_line: expected 0 args")
	}
	var buf []byte
	one := make([]byte, 1)
	for {
		n, err := i.Stdin.Read(one)
		if n > 0 {
			buf = append(buf, one[0])
			if one[0] == '\n' {
				return optionSome(String(string(buf))), nil
			}
		}
		if err != nil {
			if len(buf) == 0 {
				return optionNone(), nil
			}
			return optionSome(String(string(buf))), nil
		}
	}
}

func builtinReaderReadChunk(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("Reader.read_chunk: expected 2 args")
	}
	r, err := readerStream(i, args[0])
	if err != nil {
		return nil, err
	}
	size, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("Reader.read_chunk: size must be a number")
	}
	buf := make([]byte, int(size))
	n, _ := r.Read(buf)
	if n == 0 {
		return optionNone(), nil
	}
	return optionSome(String(string(buf[:n]))), nil
}

func builtinReaderClose(i *Interp, args []Value) (Value, error) {
	return closeFile(i, args)
}

func builtinWriterClose(i *Interp, args []Value) (Value, error) {
	return closeFile(i, args)
}

func closeFile(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("close: expected 1 arg")
	}
	fd, err := streamFd(args[0])
	if err != nil {
		return nil, err
	}
	// Closing stdin / stdout / stderr is a no-op in the
	// interpreter — those streams are owned by the host.
	if fd == 0 || fd == 1 || fd == 2 {
		return optionNone(), nil
	}
	f, ok := i.openFiles[fd]
	if !ok {
		return nil, fmt.Errorf("close: fd %d not registered", fd)
	}
	if err := f.Close(); err != nil {
		return optionSome(classifyIoError("", err)), nil
	}
	delete(i.openFiles, fd)
	return optionNone(), nil
}

func builtinWriterWrite(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("Writer.write: expected 2 args")
	}
	w, err := writerStream(i, args[0])
	if err != nil {
		return nil, err
	}
	s, ok := args[1].(String)
	if !ok {
		return nil, fmt.Errorf("Writer.write: content must be a string")
	}
	if _, err := w.Write([]byte(s)); err != nil {
		return optionSome(classifyIoError("", err)), nil
	}
	return optionNone(), nil
}

// stdin / stdout / stderr return Reader / Writer struct values
// with the conventional fds. The methods route fd 0/1/2 to the
// Interp.Stdin/Stdout/Stderr fields so tests can override them.
func builtinStdin(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("stdin: expected 0 args, got %d", len(args))
	}
	return &Struct{TypeName: "Reader", Fields: map[string]Value{"fd": Number(0)}}, nil
}

func builtinStdout(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("stdout: expected 0 args, got %d", len(args))
	}
	return &Struct{TypeName: "Writer", Fields: map[string]Value{"fd": Number(1)}}, nil
}

func builtinStderr(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("stderr: expected 0 args, got %d", len(args))
	}
	return &Struct{TypeName: "Writer", Fields: map[string]Value{"fd": Number(2)}}, nil
}

func builtinExit(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("exit: expected 1 arg, got %d", len(args))
	}
	n, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("exit: expected number arg, got %T", args[0])
	}
	if i.Exiter != nil {
		i.Exiter(int(n))
	}
	return Void{}, nil
}

func builtinWrite(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("write: expected 1 arg, got %d", len(args))
	}
	s, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("write: expected string arg, got %T", args[0])
	}
	fmt.Fprint(i.Stdout, string(s))
	return Void{}, nil
}

func builtinEprint(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("eprint: expected 1 arg, got %d", len(args))
	}
	s, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("eprint: expected string arg, got %T", args[0])
	}
	fmt.Fprintln(i.Stderr, string(s))
	return Void{}, nil
}

func builtinArgs(i *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("args: expected 0 args, got %d", len(args))
	}
	out := make(Array, len(i.Args))
	for k, a := range i.Args {
		out[k] = String(a)
	}
	return out, nil
}

func builtinLen(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf(".len(): expected 1 arg (receiver), got %d", len(args))
	}
	switch v := args[0].(type) {
	case String:
		return Number(int64(len(string(v)))), nil
	case Array:
		return Number(int64(len(v))), nil
	}
	return nil, fmt.Errorf(".len(): expected string, array, or slice receiver, got %T", args[0])
}

func builtinPrint(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("print: expected 1 arg, got %d", len(args))
	}
	s, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("print: expected string arg, got %T", args[0])
	}
	fmt.Fprintln(i.Stdout, string(s))
	return Void{}, nil
}

func builtinPutchar(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("putchar: expected 1 arg, got %d", len(args))
	}
	n, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("putchar: expected number arg, got %T", args[0])
	}
	fmt.Fprintf(i.Stdout, "%c", rune(int64(n)))
	return Void{}, nil
}

// builtinPoll is the interpreter's stub for the `poll(fds, timeout_ms)` readiness
// builtin. The AST interpreter has no real file descriptors (the `tcp_*` socket
// primitives are native-only), so it always reports "no fd ready" (-1). The
// builtin exists here only so modules that reference `poll` (std/reactor, and the
// future real-fd `std/task` reactor) stay compilable + runnable under -interp;
// real readiness lives on the native backends.
func builtinPoll(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("poll: expected 2 args, got %d", len(args))
	}
	return Number(-1), nil
}

// builtinStrbufReset zeroes the global string-builder buffer.
func builtinStrbufReset(i *Interp, args []Value) (Value, error) {
	i.strbuf = i.strbuf[:0]
	return Void{}, nil
}

// builtinStrbufAppend appends a string's bytes to the global builder.
func builtinStrbufAppend(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("strbuf_append: expected 1 arg, got %d", len(args))
	}
	s, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("strbuf_append: expected string arg, got %T", args[0])
	}
	i.strbuf = append(i.strbuf, string(s)...)
	return Void{}, nil
}

// builtinStrbufTake returns the accumulated string and resets the buffer
// (matching the compiled backends: take allocates a fresh string of the
// accumulated bytes and zeroes the length).
func builtinStrbufTake(i *Interp, args []Value) (Value, error) {
	s := String(i.strbuf)
	i.strbuf = i.strbuf[:0]
	return s, nil
}

// Register adds a user-defined function to the interpreter. Subsequent
// declarations of the same name overwrite the previous one (handy for
// REPL redefinitions).
func (i *Interp) Register(fn *ast.FuncDecl) { i.Funcs[fn.Name] = fn }

// RegisterEnum makes an enum decl visible to subsequent eval calls.
// Variant constructors and `match` patterns find their variants by
// walking the registered enums; tests / the REPL must call this
// once per top-level enum after parsing.
func (i *Interp) RegisterEnum(ed *ast.EnumDecl) { i.Enums[ed.Name] = ed }

// findVariantOn restricts the lookup to a specific enum when
// `enumName` is non-empty. Used by the qualified-variant paths
// (`Color.Red` Ident / Call sites) so the lookup is deterministic
// even when two enums share a variant name.
func (i *Interp) findVariantOn(name, enumName string) (*ast.EnumDecl, int, bool) {
	if enumName != "" {
		if ed, ok := i.Enums[enumName]; ok {
			for j, v := range ed.Variants {
				if v.Name == name {
					return ed, j, true
				}
			}
		}
		return nil, 0, false
	}
	for _, ed := range i.Enums {
		for j, v := range ed.Variants {
			if v.Name == name {
				return ed, j, true
			}
		}
	}
	return nil, 0, false
}

// CallByName looks up a user function and invokes it with the given
// arguments.
func (i *Interp) CallByName(name string, args []Value) (Value, error) {
	if fn, ok := i.Funcs[name]; ok {
		return i.callFunc(fn, args)
	}
	if b, ok := i.Builtins[name]; ok {
		return b.Fn(i, args)
	}
	return nil, fmt.Errorf("undefined function %q", name)
}

// ---------- evaluation core ----------

type env struct {
	parent *env
	vars   map[string]Value
}

func newEnv(parent *env) *env { return &env{parent: parent, vars: map[string]Value{}} }

func (e *env) get(name string) (Value, bool) {
	for cur := e; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
			return v, true
		}
	}
	return nil, false
}

// set assigns to the nearest existing binding, or creates one in the
// innermost scope if none exists.
func (e *env) set(name string, v Value) {
	for cur := e; cur != nil; cur = cur.parent {
		if _, ok := cur.vars[name]; ok {
			cur.vars[name] = v
			return
		}
	}
	e.vars[name] = v
}

// declare always binds in the innermost scope (for `var` decls, params,
// match-arm / for / let bindings). Binding is an owning store, so it
// retains any Map reachable from v; the matching release happens when
// the scope is torn down (releaseScope) or the binding is reassigned.
func (e *env) declare(name string, v Value) {
	e.vars[name] = v
	retain(v)
}

// releaseScope drops the COW references held by every binding in a scope
// that is going out of scope. The escaping value (a block's return value)
// is released here too and re-retained by the caller's binding store — a
// transient dec that nets out, since the interpreter never frees at rc 0
// (Go's GC owns memory; rc only drives mutate-in-place vs copy).
func (e *env) releaseScope() {
	for _, v := range e.vars {
		release(v)
	}
}

// tryOpEarlyReturn is the sentinel error the `?` postfix
// operator raises when its receiver is `None` (Option) or
// `Err(e)` (Result). The expression evaluator can't unwind the
// enclosing function on its own — `?` shows up in expression
// position but its failure case is a statement-level early
// return — so the call wrapper (callFunc) catches this and
// turns it back into a normal Value return for the caller.
type tryOpEarlyReturn struct{ val Value }

func (t *tryOpEarlyReturn) Error() string { return "interp: TryOp early return" }

// controlFlowSignal carries a non-normal flow (return / break / continue) out
// of a value-position block-expression through expression evaluation (#4522).
// A `return`/`break`/`continue` statement inside a `{ … }` used as a value
// shows up in expression position but is a statement-level exit, so the
// BlockExpr arm of evalExpr raises this and execStmt catches it, turning it
// back into a normal result{flow} that the existing loop / callFunc
// propagation already understands.
type controlFlowSignal struct{ r result }

func (c *controlFlowSignal) Error() string { return "interp: block-expression control flow" }

type flowKind int

const (
	flowNormal flowKind = iota
	flowReturn
	flowBreak
	flowContinue
)

type result struct {
	flow flowKind
	val  Value
	// label carries the target loop label of a `break`/`continue` (empty
	// for the unlabeled form). A loop consumes the signal only when the
	// label is empty or matches its own; otherwise it propagates so an
	// outer labeled loop handles it.
	label string
}

func (i *Interp) callFunc(fn *ast.FuncDecl, args []Value) (Value, error) {
	if len(args) != len(fn.Params) {
		return nil, fmt.Errorf("%s: expected %d args, got %d", fn.Name, len(fn.Params), len(args))
	}
	e := newEnv(nil)
	for k, p := range fn.Params {
		// Parameters are BORROWED, not owned: the backends pass a map to
		// a function without bumping its COW refcount, so a mutation
		// through the param (e.g. `p = p.set(...)`) hits the caller's map
		// in place (rc stays 1). Bind directly, bypassing declare's
		// retain, so the interp matches. See docs/INTERP-MAP-COW-PLAN.md.
		e.vars[p.Name] = args[k]
	}
	i.deferStack = append(i.deferStack, nil)
	r, err := i.execBlock(fn.Body, e)
	defers := i.deferStack[len(i.deferStack)-1]
	i.deferStack = i.deferStack[:len(i.deferStack)-1]
	if err != nil {
		// `?` postfix in expression position turns failure
		// (None / Err) into a function-level early return. The
		// expression evaluator can't unwind statements on its
		// own, so it raises this sentinel error type and the
		// call wrapper here catches it. That's an error exit, so
		// errdefers fire.
		if early, ok := err.(*tryOpEarlyReturn); ok {
			i.runDefers(defers, e, true)
			return early.val, nil
		}
		i.runDefers(defers, e, false)
		return nil, err
	}
	i.runDefers(defers, e, r.flow == flowReturn && isErrReturnValue(r.val))
	if r.flow == flowReturn {
		return r.val, nil
	}
	return Void{}, nil
}

// deferEntry is one scheduled cleanup: the expression to
// evaluate and whether it came from an `errdefer` (runs only on
// an error exit) rather than a plain `defer` (runs on every
// exit).
type deferEntry struct {
	expr    ast.Expr
	onError bool
}

// runDefers evaluates the LIFO list of expressions a callee
// accumulated via `defer` / `errdefer` statements. Each one runs
// against the env at function exit — closures already capture
// any local bindings they read; deferred expressions just read
// the same env. Errors from a deferred expression are dropped
// (defer is "fire and forget" at function exit) — matching
// codegen which doesn't propagate them either.
//
// Plain defers run on every exit. errdefers run only when
// errorExit is set (the `?` operator propagating, or a `return`
// of a None/Err value), and after the plain defers — matching
// the codegen emit order (emitDeferCleanup then
// emitErrDeferCleanup).
func (i *Interp) runDefers(defers []deferEntry, e *env, errorExit bool) {
	for k := len(defers) - 1; k >= 0; k-- {
		if defers[k].onError {
			continue
		}
		_, _ = i.evalExpr(defers[k].expr, e)
	}
	if !errorExit {
		return
	}
	for k := len(defers) - 1; k >= 0; k-- {
		if !defers[k].onError {
			continue
		}
		_, _ = i.evalExpr(defers[k].expr, e)
	}
}

// isErrReturnValue reports whether a returned value is the
// failure variant of an Option/Result — `None` or `Err` (variant
// index 1) — i.e. whether a plain `return v` of it counts as an
// error exit for `errdefer`.
func isErrReturnValue(v Value) bool {
	ev, ok := v.(*Enum)
	return ok && ev.Index == 1 && (ev.EnumName == "Option" || ev.EnumName == "Result")
}

func (i *Interp) execBlock(b *ast.Block, parent *env) (result, error) {
	e := newEnv(parent)
	defer e.releaseScope()
	for _, s := range b.Stmts {
		r, err := i.execStmt(s, e)
		if err != nil {
			return result{}, err
		}
		if r.flow != flowNormal {
			return r, nil
		}
	}
	return result{flow: flowNormal}, nil
}

// execStmt runs one statement. It wraps execStmtInner to catch a
// controlFlowSignal raised by a value-position block-expression somewhere in
// the statement's expressions (#4522): a `return`/`break`/`continue` inside a
// `{ … }` value unwinds through expression evaluation as that sentinel, and is
// turned back into the normal result{flow} here so the enclosing loop /
// callFunc handles it exactly like a top-level control-flow statement.
func (i *Interp) execStmt(s ast.Stmt, e *env) (result, error) {
	r, err := i.execStmtInner(s, e)
	if err != nil {
		if cf, ok := err.(*controlFlowSignal); ok {
			return cf.r, nil
		}
	}
	return r, err
}

func (i *Interp) execStmtInner(s ast.Stmt, e *env) (result, error) {
	switch x := s.(type) {
	case *ast.Block:
		return i.execBlock(x, e)
	case *ast.If:
		c, err := i.evalExpr(x.Cond, e)
		if err != nil {
			return result{}, err
		}
		if asBool(c) {
			return i.execStmt(x.Then, e)
		}
		if x.Else != nil {
			return i.execStmt(x.Else, e)
		}
		return result{flow: flowNormal}, nil
	case *ast.LetElse:
		src, err := i.evalExpr(x.Source, e)
		if err != nil {
			return result{}, err
		}
		ev, ok := src.(*Enum)
		if !ok {
			return result{}, fmt.Errorf("interp: let-else source is %T, expected enum value", src)
		}
		if ev.VariantName == x.VariantName {
			// Bindings live in the surrounding scope (not a
			// new block) — declare directly into `e`.
			for j, name := range x.Bindings {
				if j < len(ev.Payloads) {
					e.declare(name, ev.Payloads[j])
				}
			}
			return result{flow: flowNormal}, nil
		}
		// Mismatch — execute the else block. The checker
		// requires divergence, so this returns / breaks /
		// continues; we propagate that.
		return i.execBlock(x.Else, e)
	case *ast.IfLet:
		src, err := i.evalExpr(x.Source, e)
		if err != nil {
			return result{}, err
		}
		ev, ok := src.(*Enum)
		if !ok {
			return result{}, fmt.Errorf("interp: if-let source is %T, expected enum value", src)
		}
		if ev.VariantName == x.VariantName {
			thenEnv := newEnv(e)
			for j, name := range x.Bindings {
				if j < len(ev.Payloads) {
					thenEnv.declare(name, ev.Payloads[j])
				}
			}
			return i.execStmt(x.Then, thenEnv)
		}
		if x.Else != nil {
			return i.execStmt(x.Else, e)
		}
		return result{flow: flowNormal}, nil
	case *ast.While:
		for {
			c, err := i.evalExpr(x.Cond, e)
			if err != nil {
				return result{}, err
			}
			if !asBool(c) {
				break
			}
			r, err := i.execStmt(x.Body, e)
			if err != nil {
				return result{}, err
			}
			if r.flow == flowReturn {
				return r, nil
			}
			if r.flow == flowBreak {
				if r.label != "" && r.label != x.Label {
					return r, nil // targets an outer labeled loop
				}
				break
			}
			if r.flow == flowContinue && r.label != "" && r.label != x.Label {
				return r, nil // targets an outer labeled loop
			}
			// flowContinue or flowNormal: re-test the condition.
		}
		return result{flow: flowNormal}, nil
	case *ast.Loop:
		for {
			r, err := i.execStmt(x.Body, e)
			if err != nil {
				return result{}, err
			}
			if r.flow == flowReturn {
				return r, nil
			}
			if r.flow == flowBreak {
				if r.label != "" && r.label != x.Label {
					return r, nil // targets an outer labeled loop
				}
				break
			}
			if r.flow == flowContinue && r.label != "" && r.label != x.Label {
				return r, nil // targets an outer labeled loop
			}
			// flowContinue or flowNormal: loop forever.
		}
		return result{flow: flowNormal}, nil
	case *ast.For:
		inner := newEnv(e)
		if x.Init != nil {
			if _, err := i.execStmt(x.Init, inner); err != nil {
				return result{}, err
			}
		}
		for {
			c, err := i.evalExpr(x.Cond, inner)
			if err != nil {
				return result{}, err
			}
			if !asBool(c) {
				break
			}
			r, err := i.execStmt(x.Body, inner)
			if err != nil {
				return result{}, err
			}
			if r.flow == flowReturn {
				return r, nil
			}
			if r.flow == flowBreak {
				if r.label != "" && r.label != x.Label {
					return r, nil // targets an outer labeled loop
				}
				break
			}
			if r.flow == flowContinue && r.label != "" && r.label != x.Label {
				return r, nil // targets an outer labeled loop
			}
			// flowContinue or flowNormal: run step and re-test.
			if x.Step != nil {
				if _, err := i.execStmt(x.Step, inner); err != nil {
					return result{}, err
				}
			}
		}
		return result{flow: flowNormal}, nil
	case *ast.Break:
		return result{flow: flowBreak, label: x.Label}, nil
	case *ast.Continue:
		return result{flow: flowContinue, label: x.Label}, nil
	case *ast.Return:
		if x.Value == nil {
			return result{flow: flowReturn, val: Void{}}, nil
		}
		v, err := i.evalExpr(x.Value, e)
		if err != nil {
			return result{}, err
		}
		return result{flow: flowReturn, val: v}, nil
	case *ast.Var:
		v, err := i.evalExpr(x.Init, e)
		if err != nil {
			return result{}, err
		}
		e.declare(x.Name, v)
		return result{flow: flowNormal}, nil
	case *ast.Destructure:
		v, err := i.evalExpr(x.Init, e)
		if err != nil {
			return result{}, err
		}
		if x.Fields != nil {
			st, ok := v.(*Struct)
			if !ok {
				return result{}, fmt.Errorf("struct destructure requires a struct, got %T", v)
			}
			for i2, name := range x.Names {
				fv, ok := st.Fields[x.Fields[i2]]
				if !ok {
					return result{}, fmt.Errorf("struct %s has no field %q", st.TypeName, x.Fields[i2])
				}
				e.declare(name, fv)
			}
			return result{flow: flowNormal}, nil
		}
		arr, ok := v.(Array)
		if !ok {
			return result{}, fmt.Errorf("destructure requires a tuple, got %T", v)
		}
		if len(arr) != len(x.Names) {
			return result{}, fmt.Errorf("tuple has %d elements, but %d names given", len(arr), len(x.Names))
		}
		for i2, name := range x.Names {
			e.declare(name, arr[i2])
		}
		return result{flow: flowNormal}, nil
	case *ast.ExprStmt:
		if _, err := i.evalExpr(x.Expr, e); err != nil {
			return result{}, err
		}
		return result{flow: flowNormal}, nil
	case *ast.FuncDecl:
		// Local function declaration: capture the enclosing
		// env at this point in execution and bind the
		// resulting Closure under the function's name in the
		// local scope. Subsequent calls to the name go through
		// the Closure → callClosure path so reads of outer
		// vars hit the captured env.
		e.declare(x.Name, &Closure{Decl: x, Env: e})
		return result{flow: flowNormal}, nil
	case *ast.Match:
		tag, err := i.evalExpr(x.Tag, e)
		if err != nil {
			return result{}, err
		}
		if ev, ok := tag.(*Enum); ok {
			for _, arm := range x.Arms {
				if arm.IsWildcard || arm.VariantName == ev.VariantName {
					armEnv := newEnv(e)
					if arm.AtBinding != "" {
						armEnv.declare(arm.AtBinding, tag)
					}
					if !arm.IsWildcard {
						for j, name := range arm.Bindings {
							if j < len(ev.Payloads) {
								armEnv.declare(name, ev.Payloads[j])
							}
						}
					}
					// Guard runs with bindings in scope; on false,
					// fall through to the next arm.
					if arm.Guard != nil {
						gv, err := i.evalExpr(arm.Guard, armEnv)
						if err != nil {
							return result{}, err
						}
						gb, ok := gv.(Bool)
						if !ok {
							return result{}, fmt.Errorf("interp: match guard yielded %T, expected boolean", gv)
						}
						if !bool(gb) {
							continue
						}
					}
					r, err := i.execBlock(arm.Body, armEnv)
					if err != nil {
						return result{}, err
					}
					if r.flow == flowReturn || r.flow == flowContinue || r.flow == flowBreak {
						return r, nil
					}
					return result{flow: flowNormal}, nil
				}
			}
			return result{}, fmt.Errorf("interp: match did not cover variant %s (this should have been a checker error)", ev.VariantName)
		}
		// Struct scrutinee: struct-pattern arms `S { x, y }` bind the named
		// fields irrefutably; the first arm whose guard passes runs. The
		// checker (checkStructMatch) stamps StructMatch and guarantees an
		// irrefutable arm exists.
		if st, ok := tag.(*Struct); ok && x.StructMatch != "" {
			for _, arm := range x.Arms {
				armEnv := newEnv(e)
				if !arm.IsWildcard {
					if arm.AtBinding != "" {
						armEnv.declare(arm.AtBinding, st)
					}
					for k, b := range arm.Bindings {
						field := b
						if k < len(arm.FieldNames) && arm.FieldNames[k] != "" {
							field = arm.FieldNames[k]
						}
						fv, ok := st.Fields[field]
						if !ok {
							return result{}, fmt.Errorf("interp: struct %s has no field %q", st.TypeName, field)
						}
						armEnv.declare(b, fv)
					}
				}
				if arm.Guard != nil {
					gv, err := i.evalExpr(arm.Guard, armEnv)
					if err != nil {
						return result{}, err
					}
					gb, ok := gv.(Bool)
					if !ok {
						return result{}, fmt.Errorf("interp: match guard yielded %T, expected boolean", gv)
					}
					if !bool(gb) {
						continue
					}
				}
				r, err := i.execBlock(arm.Body, armEnv)
				if err != nil {
					return result{}, err
				}
				if r.flow == flowReturn || r.flow == flowContinue || r.flow == flowBreak {
					return r, nil
				}
				return result{flow: flowNormal}, nil
			}
			return result{flow: flowNormal}, nil
		}
		// Tuple scrutinee: tuple-pattern arms `(p0, p1, …)`. A tuple
		// value is an Array; a literal element dispatches by equality,
		// a binder element always matches and binds, `_` is ignored.
		// The checker (E035/E030) guarantees the arm shapes and that
		// an irrefutable arm exists.
		if arr, isArr := tag.(Array); isArr && matchArmsHaveTuple(x.Arms) {
			for _, arm := range x.Arms {
				armEnv := newEnv(e)
				if !arm.IsWildcard {
					if len(arm.TupleElems) != len(arr) {
						continue
					}
					matched := true
					for k, el := range arm.TupleElems {
						if el.Literal == nil {
							continue
						}
						lv, err := i.evalExpr(el.Literal, e)
						if err != nil {
							return result{}, err
						}
						if !valuesEqual(arr[k], lv) {
							matched = false
							break
						}
					}
					if !matched {
						continue
					}
					if arm.AtBinding != "" {
						armEnv.declare(arm.AtBinding, arr)
					}
					for k, el := range arm.TupleElems {
						if el.Name != "" {
							armEnv.declare(el.Name, arr[k])
						}
					}
				}
				if arm.Guard != nil {
					gv, err := i.evalExpr(arm.Guard, armEnv)
					if err != nil {
						return result{}, err
					}
					gb, ok := gv.(Bool)
					if !ok {
						return result{}, fmt.Errorf("interp: match guard yielded %T, expected boolean", gv)
					}
					if !bool(gb) {
						continue
					}
				}
				r, err := i.execBlock(arm.Body, armEnv)
				if err != nil {
					return result{}, err
				}
				if r.flow == flowReturn || r.flow == flowContinue || r.flow == flowBreak {
					return r, nil
				}
				return result{flow: flowNormal}, nil
			}
			return result{flow: flowNormal}, nil
		}
		// Non-enum scrutinee (i32 / string / bool): literal-pattern
		// match, mirroring the compiled backend's emitLiteralMatch.
		// Each arm is a literal (dispatched by `==`) or the `_`
		// fall-through; the checker (E035/E030) guarantees an
		// unguarded `_` arm exists, so a match is always found. Any
		// other value type reaching here is a type error the checker
		// should have caught — keep the diagnostic.
		switch tag.(type) {
		case Number, Bool, String:
		default:
			return result{}, fmt.Errorf("interp: match scrutinee is %T, expected enum value", tag)
		}
		for _, arm := range x.Arms {
			if !arm.IsWildcard {
				if arm.Literal == nil {
					continue
				}
				matched, err := i.armMatchesScalar(arm.Literal, arm.RangeHi, arm.RangeInclusive, tag, e)
				if err != nil {
					return result{}, err
				}
				if !matched {
					continue
				}
			}
			armEnv := newEnv(e)
			if arm.Guard != nil {
				gv, err := i.evalExpr(arm.Guard, armEnv)
				if err != nil {
					return result{}, err
				}
				gb, ok := gv.(Bool)
				if !ok {
					return result{}, fmt.Errorf("interp: match guard yielded %T, expected boolean", gv)
				}
				if !bool(gb) {
					continue
				}
			}
			r, err := i.execBlock(arm.Body, armEnv)
			if err != nil {
				return result{}, err
			}
			if r.flow == flowReturn || r.flow == flowContinue || r.flow == flowBreak {
				return r, nil
			}
			return result{flow: flowNormal}, nil
		}
		return result{flow: flowNormal}, nil
	case *ast.Defer:
		// Push onto the enclosing call's defer frame. The frame
		// is unwound LIFO at function exit by callFunc /
		// callClosure. No frame = `defer` outside any function
		// call (REPL top level) — treat as a no-op since
		// there's no exit point to run the deferred expr at.
		if n := len(i.deferStack); n > 0 {
			i.deferStack[n-1] = append(i.deferStack[n-1], deferEntry{expr: x.Expr, onError: x.OnError})
		}
		return result{flow: flowNormal}, nil
	}
	return result{}, fmt.Errorf("interp: unsupported statement %T", s)
}

// matchArmsHaveTuple reports whether any arm carries a tuple pattern —
// the dispatch cue for a tuple-typed match scrutinee (whose runtime
// value is an Array, which a plain literal match never produces).
func matchArmsHaveTuple(arms []*ast.MatchArm) bool {
	for _, arm := range arms {
		if arm.TupleElems != nil {
			return true
		}
	}
	return false
}

// matchExprArmsHaveTuple is the MatchExpr-side counterpart.
func matchExprArmsHaveTuple(arms []*ast.MatchExprArm) bool {
	for _, arm := range arms {
		if arm.TupleElems != nil {
			return true
		}
	}
	return false
}

// valuesEqual is a value-equality check used for match-tag / map-key
// comparisons. Numbers, Bools and Strings compare by content; other
// types compare via Go's `==` which is a sensible fallback (Func
// references, Void, etc.).
// armMatchesScalar reports whether a scalar-match arm matches the
// already-evaluated scrutinee value `tag`. A plain literal arm matches on
// equality; a range arm (`lo..hi` / `lo..=hi`, RangeHi non-nil) matches
// when `lo <= tag <op> hi`. Range scrutinees are signed-integer or float
// (the checker restricts them), so the comparisons are signed.
func (i *Interp) armMatchesScalar(lit, rangeHi ast.Expr, inclusive bool, tag Value, e *env) (bool, error) {
	lv, err := i.evalExpr(lit, e)
	if err != nil {
		return false, err
	}
	if rangeHi == nil {
		return valuesEqual(tag, lv), nil
	}
	hv, err := i.evalExpr(rangeHi, e)
	if err != nil {
		return false, err
	}
	if tn, ok := tag.(Number); ok {
		ln, lok := lv.(Number)
		hn, hok := hv.(Number)
		if lok && hok {
			if int64(ln) > int64(tn) {
				return false, nil
			}
			if inclusive {
				return int64(tn) <= int64(hn), nil
			}
			return int64(tn) < int64(hn), nil
		}
	}
	if tf, ok := tag.(Float); ok {
		lf, lok := lv.(Float)
		hf, hok := hv.(Float)
		if lok && hok {
			if lf.V > tf.V {
				return false, nil
			}
			if inclusive {
				return tf.V <= hf.V, nil
			}
			return tf.V < hf.V, nil
		}
	}
	return false, nil
}

func valuesEqual(a, b Value) bool {
	switch ax := a.(type) {
	case Number:
		bx, ok := b.(Number)
		return ok && ax == bx
	case Bool:
		bx, ok := b.(Bool)
		return ok && ax == bx
	case String:
		bx, ok := b.(String)
		return ok && ax == bx
	case Float:
		bx, ok := b.(Float)
		return ok && ax.V == bx.V && ax.Width == bx.Width
	case Array:
		// Tuples are represented as Array, so this also covers tuple keys.
		// Element-wise by value — the recursion bottoms out on scalars
		// (Fern forbids reference cycles via E048/E049/E057, so no infinite
		// descent).
		bx, ok := b.(Array)
		if !ok || len(ax) != len(bx) {
			return false
		}
		for i := range ax {
			if !valuesEqual(ax[i], bx[i]) {
				return false
			}
		}
		return true
	case *Struct:
		bx, ok := b.(*Struct)
		if !ok || ax.TypeName != bx.TypeName || len(ax.Fields) != len(bx.Fields) {
			return false
		}
		for k, v := range ax.Fields {
			bv, ok := bx.Fields[k]
			if !ok || !valuesEqual(v, bv) {
				return false
			}
		}
		return true
	case *Enum:
		bx, ok := b.(*Enum)
		if !ok || ax.EnumName != bx.EnumName || ax.Index != bx.Index || len(ax.Payloads) != len(bx.Payloads) {
			return false
		}
		for i := range ax.Payloads {
			if !valuesEqual(ax.Payloads[i], bx.Payloads[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

func (i *Interp) evalExpr(e ast.Expr, env *env) (Value, error) {
	switch x := e.(type) {
	case *ast.NumberLit:
		// IsFloat is the checker's record (settleFloat) that a polymorphic
		// integer literal settled into FLOAT context — `var x: f64 = 3`, or an
		// integer literal at an f64 parameter. Ignoring it handed the body a
		// Number where it expected a Float, so the first arithmetic op failed
		// with `"+" on interp.Number and interp.Float not supported` even
		// though the checker had accepted the program. The compiled backends
		// read the same stamp (the IR's NumberLit lowering takes its f-const
		// path on it). #5477.
		if x.IsFloat {
			// FloatWidth is the width settleFloat resolved; 0 means it never
			// ran, in which case f64 is the language's default float.
			w := x.FloatWidth
			if w == 0 {
				w = 64
			}
			v := float64(x.Value)
			if w == 32 {
				v = float64(float32(v))
			}
			return Float{V: v, Width: w}, nil
		}
		return Number(x.Value), nil
	case *ast.FloatLit:
		// Width comes from the checker. An unsettled literal
		// (Width 0 — no expected-type pressure) defaults to f64,
		// the double-precision default and the language's primary
		// float; only an explicit f32 context stamps Width 32.
		// (Matches the IR lowering's Width-0 → OpConstF64 path.)
		w := x.Width
		if w == 0 {
			w = 64
		}
		v := x.Value
		if w == 32 {
			v = float64(float32(v))
		}
		return Float{V: v, Width: w}, nil
	case *ast.DowncastExpr:
		// `e as? T` — fallible downcast of a `dyn Trait` value (which is
		// just the concrete boxed value, already carrying its TypeName)
		// to the concrete type T. Evaluate the inner, recover its runtime
		// type name, and produce Some(value) iff it matches T exactly,
		// else None. The bound value is the concrete value itself, so a
		// `Some(x)` arm sees x as the concrete type (docs/DYN-TRAITS.md §9).
		v, err := i.evalExpr(x.Inner, env)
		if err != nil {
			return nil, err
		}
		want := downcastTargetName(x.Target)
		if got, ok := valueTypeName(v); ok && want != "" && got == want {
			return optionSome(v), nil
		}
		return optionNone(), nil
	case *ast.CastExpr:
		// Numeric casts: integer → integer, float → float (width
		// change), and the cross-family int↔float conversions.
		// All integers live in `Number` (int64) at interp time;
		// floats live in `Float` (float64 storage tagged with the
		// source width).
		v, err := i.evalExpr(x.Inner, env)
		if err != nil {
			return nil, err
		}
		if tgt, ok := x.Target.(ast.NumberType); ok {
			// int → int: narrow (wrap) to the destination width.
			// Sub-i32 targets must wrap too — `5000000000 as u8`
			// is 0 on every codegen backend; the interpreter used
			// to leave it unnarrowed.
			if src, ok := v.(Number); ok {
				return Number(narrowInt(int64(src), tgt.NormalWidth(), !tgt.IsSigned())), nil
			}
			// float → int (truncate-toward-zero, saturating: NaN
			// → 0, out-of-range clamps to the destination's
			// min/max). Matches wasm `trunc_sat` and the native
			// backends; a plain Go `int64(f)` is implementation-
			// defined for out-of-range inputs and diverged from
			// them (it yielded INT_MIN where they saturate). The
			// hardware trunc saturates at 32 bits, so a sub-i32
			// destination saturates to i32 and then wraps to its
			// width — matching the codegen backends.
			if src, ok := v.(Float); ok {
				sw := tgt.NormalWidth()
				if sw < 32 {
					sw = 32
				}
				n := saturateFloatToInt(src.V, sw, tgt.IsSigned())
				return Number(narrowInt(n, tgt.NormalWidth(), !tgt.IsSigned())), nil
			}
		}
		if tgt, ok := x.Target.(ast.FloatType); ok {
			// int → float
			if src, ok := v.(Number); ok {
				w := tgt.Width
				if w == 0 {
					w = 32
				}
				// An unsigned source converts from its unsigned
				// magnitude: a u64 max rides as the bit pattern -1,
				// which a signed conversion would turn into -1.0
				// instead of ~1.8e19 (the codegen backends' ucvtf /
				// convert_i64_u result). Source type from InnerType.
				fv := float64(int64(src))
				if srcInt, ok := x.InnerType.(ast.NumberType); ok && !srcInt.IsSigned() {
					fv = float64(uint64(int64(src)))
				}
				if w == 32 {
					fv = float64(float32(fv))
				}
				return Float{V: fv, Width: w}, nil
			}
			// float → float (width change). Demote / promote
			// rounds through the target width's representation.
			if src, ok := v.(Float); ok {
				w := tgt.Width
				if w == 0 {
					w = 32
				}
				fv := src.V
				if w == 32 {
					fv = float64(float32(fv))
				}
				return Float{V: fv, Width: w}, nil
			}
		}
		// Type ascription (`expr as T` where the inner is already
		// assignable to T — None as Option[i32], [] as i32[],
		// Ok(1) as Result[i32, string], etc.). Zero-cost at runtime
		// because the value already carries the right shape; the
		// cast is a checker-side annotation only.
		if _, ok := x.Target.(ast.NumberType); !ok {
			if _, ok := x.Target.(ast.FloatType); !ok {
				return v, nil
			}
		}
		return nil, fmt.Errorf("cast from %T to %s not supported in the interpreter", v, x.Target)
	case *ast.BoolLit:
		return Bool(x.Value), nil
	case *ast.UnitLit:
		// `()` carries no information; it rides the same zero every
		// backend stores in the payload slot.
		return Number(0), nil
	case *ast.StringLit:
		return String(x.Value), nil
	case *ast.FString:
		// The checker desugars `f"foo {x} bar"` into the equivalent
		// `"foo " + (x).to_string() + " bar"` `+`-chain and stamps
		// it on x.Desugared. We just evaluate that. When the
		// program reaches the interpreter without going through
		// the checker (e.g. REPL), fall back to assembling the
		// pieces directly so the literal segments at least work
		// — interpolant values without a checker-resolved
		// `.to_string()` are stringified through Value.String().
		if x.Desugared != nil {
			return i.evalExpr(x.Desugared, env)
		}
		var sb strings.Builder
		for _, p := range x.Parts {
			if p.Expr == nil {
				sb.WriteString(p.Lit)
				continue
			}
			v, err := i.evalExpr(p.Expr, env)
			if err != nil {
				return nil, err
			}
			sb.WriteString(v.String())
		}
		return String(sb.String()), nil
	case *ast.Ident:
		if v, ok := env.get(x.Name); ok {
			return v, nil
		}
		if fn, ok := i.Funcs[x.Name]; ok {
			return Func{Decl: fn}, nil
		}
		// Payload-less variant (`Red`, `EOF`) used as a value.
		// Variants with payloads must be called explicitly. The
		// checker stamps `x.EnumName` when the bare reference was
		// disambiguated by a qualifier or by context, so the
		// lookup stays deterministic across multi-enum same-name
		// variants.
		if ed, idx, ok := i.findVariantOn(x.Name, x.EnumName); ok {
			if len(ed.Variants[idx].Payloads) != 0 {
				return nil, fmt.Errorf("interp: variant %s expects %d payload(s); call it instead",
					x.Name, len(ed.Variants[idx].Payloads))
			}
			return &Enum{EnumName: ed.Name, VariantName: x.Name, Index: idx}, nil
		}
		if _, ok := i.Builtins[x.Name]; ok {
			// Builtins aren't first-class for now; only callable.
			return nil, fmt.Errorf("interp: builtin %q can only be called, not used as a value", x.Name)
		}
		return nil, fmt.Errorf("undefined identifier %q", x.Name)
	case *ast.ArrayLit:
		out := make(Array, len(x.Elems))
		for k, el := range x.Elems {
			v, err := i.evalExpr(el, env)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	case *ast.MapLit:
		// `Map { k1: v1, k2: v2, ... }` — sequence of inserts in
		// declaration order. Matches the codegen lowering shape
		// (`map_new(N) + .set(k, v) ...`) so the interp's
		// insertion-order rep produces the same `.keys()` /
		// `.values()` output as native backends.
		m := &Map{}
		for _, ent := range x.Entries {
			kv, err := i.evalExpr(ent.Key, env)
			if err != nil {
				return nil, err
			}
			vv, err := i.evalExpr(ent.Value, env)
			if err != nil {
				return nil, err
			}
			if idx := m.findKey(kv); idx >= 0 {
				m.vals[idx] = vv
			} else {
				m.keys = append(m.keys, kv)
				m.vals = append(m.vals, vv)
			}
		}
		return m, nil
	case *ast.Index:
		arrV, err := i.evalExpr(x.Array, env)
		if err != nil {
			return nil, err
		}
		idxV, err := i.evalExpr(x.Idx, env)
		if err != nil {
			return nil, err
		}
		idx, ok := idxV.(Number)
		if !ok {
			return nil, fmt.Errorf("index must be number, got %T", idxV)
		}
		// String indexing returns the byte at offset i as a Number,
		// matching the codegen lowering of `s[i]`.
		if s, ok := arrV.(String); ok {
			if idx < 0 || int(idx) >= len(string(s)) {
				return nil, fmt.Errorf("string index %d out of range [0, %d)", idx, len(string(s)))
			}
			return Number(int64(string(s)[idx])), nil
		}
		arr, ok := arrV.(Array)
		if !ok {
			return nil, fmt.Errorf("indexing non-array %T", arrV)
		}
		if idx < 0 || int(idx) >= len(arr) {
			return nil, fmt.Errorf("array index %d out of range [0, %d)", idx, len(arr))
		}
		return arr[idx], nil
	case *ast.SliceExpr:
		// Slices on arrays are stored as `Array` at interp time
		// — same type because the interpreter doesn't model
		// alias / ownership. String slicing returns a fresh
		// String holding the byte range — matches the codegen
		// `__str_slice` runtime semantics. Both share the same
		// SliceExpr AST shape; we dispatch on the source value's
		// runtime type.
		srcV, err := i.evalExpr(x.Source, env)
		if err != nil {
			return nil, err
		}
		var slen int64
		switch v := srcV.(type) {
		case Array:
			slen = int64(len(v))
		case String:
			slen = int64(len(v))
		default:
			return nil, fmt.Errorf("cannot slice non-array/string %T", srcV)
		}
		low := int64(0)
		if x.Low != nil {
			lv, err := i.evalExpr(x.Low, env)
			if err != nil {
				return nil, err
			}
			n, ok := lv.(Number)
			if !ok {
				return nil, fmt.Errorf("slice low must be number, got %T", lv)
			}
			low = int64(n)
		}
		high := slen
		if x.High != nil {
			hv, err := i.evalExpr(x.High, env)
			if err != nil {
				return nil, err
			}
			n, ok := hv.(Number)
			if !ok {
				return nil, fmt.Errorf("slice high must be number, got %T", hv)
			}
			high = int64(n)
		}
		if low < 0 || high > slen || low > high {
			return nil, fmt.Errorf("slice [%d:%d] out of range for length %d", low, high, slen)
		}
		switch v := srcV.(type) {
		case Array:
			return Array(v[low:high]), nil
		case String:
			return String(string(v)[low:high]), nil
		}
		return nil, fmt.Errorf("unreachable: srcV type %T not Array/String after pre-check", srcV)
	case *ast.Lambda:
		// Synthesise a FuncDecl shell from the Lambda's params /
		// return / body so callClosure can dispatch through the
		// same callFunc path top-level decls use. Capture the
		// current env so reads of outer vars hit the right
		// values.
		decl := &ast.FuncDecl{
			P:          x.P,
			Name:       "",
			Params:     x.Params,
			ReturnType: x.ReturnType,
			Body:       x.Body,
		}
		return &Closure{Decl: decl, Env: env}, nil
	case *ast.Call:
		return i.evalCall(x, env)
	case *ast.Binary:
		return i.evalBinary(x, env)
	case *ast.Unary:
		v, err := i.evalExpr(x.Operand, env)
		if err != nil {
			return nil, err
		}
		switch x.Op {
		case "-":
			if n, ok := v.(Number); ok {
				return -n, nil
			}
			if f, ok := v.(Float); ok {
				nv := -f.V
				if f.Width == 32 {
					nv = float64(float32(nv))
				}
				return Float{V: nv, Width: f.Width}, nil
			}
			return nil, fmt.Errorf("interp: unary - on %T", v)
		case "!":
			b, _ := v.(Bool)
			return !b, nil
		}
		return nil, fmt.Errorf("interp: unsupported unary %q", x.Op)
	case *ast.Assign:
		return i.evalAssign(x, env)
	case *ast.BlockExpr:
		// Block-expression `{ stmts; tail }` (slice 1): run the
		// statements in a fresh child env, then the trailing expression
		// is the block's value. Locals bound by the statements are
		// visible to Tail but the child env is dropped afterwards, so
		// they don't leak out of the block.
		blockEnv := newEnv(env)
		defer blockEnv.releaseScope()
		for _, s := range x.Stmts {
			r, err := i.execStmt(s, blockEnv)
			if err != nil {
				return nil, err
			}
			if r.flow != flowNormal {
				// A `return` / `break` / `continue` inside a value-position
				// block-expr is a statement-level exit surfacing in expression
				// position (#4522). Unwind it through expression evaluation as
				// a controlFlowSignal; execStmt catches it and turns it back
				// into a result{flow} the enclosing loop / callFunc handles.
				// The block's tail value is intentionally skipped on this path.
				return nil, &controlFlowSignal{r: r}
			}
		}
		if x.Tail == nil {
			// Value-less block in value position — the checker reports
			// E061; if it slipped through, fail loudly.
			return nil, fmt.Errorf("interp: block-expression has no trailing value")
		}
		return i.evalExpr(x.Tail, blockEnv)
	case *ast.IfExpr:
		c, err := i.evalExpr(x.Cond, env)
		if err != nil {
			return nil, err
		}
		b, ok := c.(Bool)
		if !ok {
			return nil, fmt.Errorf("interp: if-expression condition is not a bool: %T", c)
		}
		if bool(b) {
			return i.evalExpr(x.Then, env)
		}
		return i.evalExpr(x.Else, env)
	case *ast.MatchExpr:
		tag, err := i.evalExpr(x.Tag, env)
		if err != nil {
			return nil, err
		}
		if ev, ok := tag.(*Enum); ok {
			for _, arm := range x.Arms {
				if !arm.IsWildcard && arm.VariantName != ev.VariantName {
					continue
				}
				armEnv := newEnv(env)
				if arm.AtBinding != "" {
					armEnv.declare(arm.AtBinding, tag)
				}
				if !arm.IsWildcard {
					for j, name := range arm.Bindings {
						if j < len(ev.Payloads) {
							armEnv.declare(name, ev.Payloads[j])
						}
					}
				}
				if arm.Guard != nil {
					gv, err := i.evalExpr(arm.Guard, armEnv)
					if err != nil {
						return nil, err
					}
					gb, ok := gv.(Bool)
					if !ok {
						return nil, fmt.Errorf("interp: match guard yielded %T, expected boolean", gv)
					}
					if !bool(gb) {
						continue
					}
				}
				return i.evalExpr(arm.Body, armEnv)
			}
			return nil, fmt.Errorf("interp: match-expression non-exhaustive at runtime (variant %q unhandled)", ev.VariantName)
		}
		// Struct scrutinee: struct-pattern arms bind fields irrefutably;
		// the first arm whose guard passes yields the result.
		if st, ok := tag.(*Struct); ok && x.StructMatch != "" {
			for _, arm := range x.Arms {
				armEnv := newEnv(env)
				if !arm.IsWildcard {
					if arm.AtBinding != "" {
						armEnv.declare(arm.AtBinding, st)
					}
					for k, b := range arm.Bindings {
						field := b
						if k < len(arm.FieldNames) && arm.FieldNames[k] != "" {
							field = arm.FieldNames[k]
						}
						fv, ok := st.Fields[field]
						if !ok {
							return nil, fmt.Errorf("interp: struct %s has no field %q", st.TypeName, field)
						}
						armEnv.declare(b, fv)
					}
				}
				if arm.Guard != nil {
					gv, err := i.evalExpr(arm.Guard, armEnv)
					if err != nil {
						return nil, err
					}
					gb, ok := gv.(Bool)
					if !ok {
						return nil, fmt.Errorf("interp: match guard yielded %T, expected boolean", gv)
					}
					if !bool(gb) {
						continue
					}
				}
				return i.evalExpr(arm.Body, armEnv)
			}
			return nil, fmt.Errorf("interp: match-expression non-exhaustive at runtime (no struct arm matched)")
		}
		// Tuple scrutinee: tuple-pattern arms — same element rules as
		// the statement form (literal by equality, binder binds, `_`
		// ignored), but each arm body is an Expr.
		if arr, isArr := tag.(Array); isArr && matchExprArmsHaveTuple(x.Arms) {
			for _, arm := range x.Arms {
				armEnv := newEnv(env)
				if !arm.IsWildcard {
					if len(arm.TupleElems) != len(arr) {
						continue
					}
					matched := true
					for k, el := range arm.TupleElems {
						if el.Literal == nil {
							continue
						}
						lv, err := i.evalExpr(el.Literal, env)
						if err != nil {
							return nil, err
						}
						if !valuesEqual(arr[k], lv) {
							matched = false
							break
						}
					}
					if !matched {
						continue
					}
					if arm.AtBinding != "" {
						armEnv.declare(arm.AtBinding, arr)
					}
					for k, el := range arm.TupleElems {
						if el.Name != "" {
							armEnv.declare(el.Name, arr[k])
						}
					}
				}
				if arm.Guard != nil {
					gv, err := i.evalExpr(arm.Guard, armEnv)
					if err != nil {
						return nil, err
					}
					gb, ok := gv.(Bool)
					if !ok {
						return nil, fmt.Errorf("interp: match guard yielded %T, expected boolean", gv)
					}
					if !bool(gb) {
						continue
					}
				}
				return i.evalExpr(arm.Body, armEnv)
			}
			return nil, fmt.Errorf("interp: match-expression non-exhaustive at runtime (no tuple arm matched)")
		}
		// Non-enum scrutinee (i32 / string / bool): literal-pattern
		// match expression, mirroring emitLiteralMatchExpr. Each arm
		// dispatches via `==` against its literal, or the `_`
		// fall-through. Any other value type is a type error the
		// checker should have caught — keep the diagnostic.
		switch tag.(type) {
		case Number, Bool, String:
		default:
			return nil, fmt.Errorf("interp: match scrutinee is %T, expected enum value", tag)
		}
		for _, arm := range x.Arms {
			if !arm.IsWildcard {
				if arm.Literal == nil {
					continue
				}
				matched, err := i.armMatchesScalar(arm.Literal, arm.RangeHi, arm.RangeInclusive, tag, env)
				if err != nil {
					return nil, err
				}
				if !matched {
					continue
				}
			}
			armEnv := newEnv(env)
			if arm.Guard != nil {
				gv, err := i.evalExpr(arm.Guard, armEnv)
				if err != nil {
					return nil, err
				}
				gb, ok := gv.(Bool)
				if !ok {
					return nil, fmt.Errorf("interp: match guard yielded %T, expected boolean", gv)
				}
				if !bool(gb) {
					continue
				}
			}
			return i.evalExpr(arm.Body, armEnv)
		}
		return nil, fmt.Errorf("interp: match-expression non-exhaustive at runtime (no literal arm matched)")
	case *ast.TryOp:
		// `expr?` desugars to:
		//   match (expr) {
		//     Some(v) => v,   // Option-shape
		//     None    => return None,
		//     Ok(v)   => v,   // Result-shape
		//     Err(_)  => return expr,
		//   }
		// The early-return arms hop the enclosing function via
		// the tryOpEarlyReturn sentinel; callFunc catches it.
		inner, err := i.evalExpr(x.Inner, env)
		if err != nil {
			return nil, err
		}
		ev, ok := inner.(*Enum)
		if !ok {
			return nil, fmt.Errorf("interp: `?` applied to non-enum %T", inner)
		}
		switch ev.VariantName {
		case "Some":
			if len(ev.Payloads) != 1 {
				return nil, fmt.Errorf("interp: Some payload arity %d", len(ev.Payloads))
			}
			return ev.Payloads[0], nil
		case "None":
			return nil, &tryOpEarlyReturn{val: ev}
		case "Ok":
			if len(ev.Payloads) != 1 {
				return nil, fmt.Errorf("interp: Ok payload arity %d", len(ev.Payloads))
			}
			return ev.Payloads[0], nil
		case "Err":
			return nil, &tryOpEarlyReturn{val: ev}
		}
		return nil, fmt.Errorf("interp: `?` on unexpected variant %q", ev.VariantName)
	case *ast.StructLit:
		s := &Struct{TypeName: x.TypeName, Fields: map[string]Value{}}
		// Struct-update `Foo { ...base, field: v }`: seed from the
		// base's fields, then apply the overrides on top. The result
		// is a fresh Struct (immutable-update semantics) — mutating it
		// never touches the base.
		if x.Base != nil {
			bv, err := i.evalExpr(x.Base, env)
			if err != nil {
				return nil, err
			}
			bs, ok := bv.(*Struct)
			if !ok {
				return nil, fmt.Errorf("struct-update base is not a struct: %T", bv)
			}
			for k, v := range bs.Fields {
				s.Fields[k] = v
			}
		}
		for _, f := range x.Fields {
			v, err := i.evalExpr(f.Value, env)
			if err != nil {
				return nil, err
			}
			s.Fields[f.Name] = v
		}
		return s, nil
	case *ast.TupleLit:
		// Reuse Array — tuples are positional by construction, so a
		// flat slice of values is the right shape and avoids a new
		// Value subtype just for tuples.
		out := make(Array, len(x.Elems))
		for i2, e := range x.Elems {
			v, err := i.evalExpr(e, env)
			if err != nil {
				return nil, err
			}
			out[i2] = v
		}
		return out, nil
	case *ast.FieldAccess:
		// Qualified payload-less variant: `Color.Red`. Target is
		// an Ident naming an enum type, not a value — evaluating
		// it as a value would fail. Mirror the checker rewrite
		// and produce the typed Enum directly.
		if tid, ok := x.Target.(*ast.Ident); ok {
			if ed, idx, ok := i.findVariantOn(x.Field, tid.Name); ok {
				if len(ed.Variants[idx].Payloads) != 0 {
					return nil, fmt.Errorf("interp: variant %s.%s expects %d payload(s); call it instead",
						tid.Name, x.Field, len(ed.Variants[idx].Payloads))
				}
				return &Enum{EnumName: ed.Name, VariantName: x.Field, Index: idx}, nil
			}
		}
		tv, err := i.evalExpr(x.Target, env)
		if err != nil {
			return nil, err
		}
		// Tuple field access: numeric field name, target is an
		// Array (tuples piggy-back on Array at interp time).
		if arr, ok := tv.(Array); ok {
			idx, err := strconv.Atoi(x.Field)
			if err != nil {
				return nil, fmt.Errorf("tuple access requires numeric index, got %q", x.Field)
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("tuple has %d elements; index %d out of range", len(arr), idx)
			}
			return arr[idx], nil
		}
		s, ok := tv.(*Struct)
		if !ok {
			return nil, fmt.Errorf("field access on non-struct %T", tv)
		}
		v, ok := s.Fields[x.Field]
		if !ok {
			return nil, fmt.Errorf("struct %s has no field %q", s.TypeName, x.Field)
		}
		return v, nil
	}
	return nil, fmt.Errorf("interp: unsupported expression %T", e)
}

func (i *Interp) evalCall(c *ast.Call, env *env) (Value, error) {
	args := make([]Value, len(c.Args))
	for k, a := range c.Args {
		v, err := i.evalExpr(a, env)
		if err != nil {
			return nil, err
		}
		args[k] = v
	}
	// Dynamic trait-object dispatch: the checker marked this call with
	// the trait name and left the callee a FieldAccess (`d.area()`
	// where `d: dyn Shape`). Resolve the concrete method from the
	// receiver value's runtime type and call it with the receiver
	// prepended. The orphan rule guarantees one impl per (trait, type),
	// so the lookup is unambiguous. See docs/DYN-TRAITS.md §4.1.
	if c.DynTrait != "" {
		fa, ok := c.Callee.(*ast.FieldAccess)
		if !ok {
			return nil, fmt.Errorf("interp: dyn %s call without a field-access callee", c.DynTrait)
		}
		recv, err := i.evalExpr(fa.Target, env)
		if err != nil {
			return nil, err
		}
		tn, ok := valueTypeName(recv)
		if !ok {
			return nil, fmt.Errorf("interp: cannot dispatch dyn %s.%s on a %T value", c.DynTrait, fa.Field, recv)
		}
		mangled := "__method_" + tn + "_" + fa.Field
		callArgs := append([]Value{recv}, args...)
		if b, ok := i.Builtins[mangled]; ok {
			return b.Fn(i, callArgs)
		}
		if fn, ok := i.Funcs[mangled]; ok {
			return i.callFunc(fn, callArgs)
		}
		return nil, fmt.Errorf("interp: no impl of %s.%s for runtime type %s", c.DynTrait, fa.Field, tn)
	}
	if id, ok := c.Callee.(*ast.Ident); ok {
		// Variant constructor: resolve the name (optionally
		// scoped to a stamped enum, set by the checker for
		// qualified `Color.Red(payload)` references and
		// disambiguated bare-name calls) and build an Enum value
		// with the evaluated payloads.
		if ed, idx, ok := i.findVariantOn(id.Name, id.EnumName); ok {
			if _, shadowed := env.get(id.Name); !shadowed {
				if _, isFn := i.Funcs[id.Name]; !isFn {
					if got, want := len(args), len(ed.Variants[idx].Payloads); got != want {
						return nil, fmt.Errorf("interp: variant %s expects %d argument(s), got %d",
							id.Name, want, got)
					}
					return &Enum{EnumName: ed.Name, VariantName: id.Name, Index: idx, Payloads: args}, nil
				}
			}
		}
		if b, ok := i.Builtins[id.Name]; ok {
			return b.Fn(i, args)
		}
		if v, ok := env.get(id.Name); ok {
			switch fv := v.(type) {
			case Func:
				return i.callFunc(fv.Decl, args)
			case *Closure:
				return i.callClosure(fv, args)
			}
			return nil, fmt.Errorf("calling non-function %q (%T)", id.Name, v)
		}
		if fn, ok := i.Funcs[id.Name]; ok {
			return i.callFunc(fn, args)
		}
		return nil, fmt.Errorf("undefined function %q", id.Name)
	}
	cv, err := i.evalExpr(c.Callee, env)
	if err != nil {
		return nil, err
	}
	switch fv := cv.(type) {
	case Func:
		return i.callFunc(fv.Decl, args)
	case *Closure:
		return i.callClosure(fv, args)
	}
	return nil, fmt.Errorf("interp: not a function: %T", cv)
}

// callClosure dispatches a call through a Closure value. The
// param environment chains off the captured env (not a fresh
// nil parent), so reads of free variables hit the values
// snapshot at definition time.
func (i *Interp) callClosure(c *Closure, args []Value) (Value, error) {
	if len(args) != len(c.Decl.Params) {
		name := c.Decl.Name
		if name == "" {
			name = "<closure>"
		}
		return nil, fmt.Errorf("%s: expected %d args, got %d", name, len(c.Decl.Params), len(args))
	}
	e := newEnv(c.Env)
	for k, p := range c.Decl.Params {
		e.declare(p.Name, args[k])
	}
	i.deferStack = append(i.deferStack, nil)
	r, err := i.execBlock(c.Decl.Body, e)
	defers := i.deferStack[len(i.deferStack)-1]
	i.deferStack = i.deferStack[:len(i.deferStack)-1]
	if err != nil {
		// A closure is a function boundary, so a `?` unwind stops HERE and
		// becomes this closure's return value — exactly as callFunc does it.
		// This used to only consult the sentinel to decide whether errdefers
		// fire and then re-raise it, so a `None` / `Err` unwound past the
		// lambda and out of the whole program: `function (o: Option[i32]):
		// Option[i32] { var v: i32 = o?; return Some(v + 1); }` applied to
		// None terminated the interpreter with exit 0 instead of answering
		// None. The compiled backends were right; only this engine was wrong,
		// which matters double because it is the differential ORACLE the
		// cross-validation suite grades the self-host engines against
		// (docs/NATIVE-CONVERGENCE.md §3).
		if early, ok := err.(*tryOpEarlyReturn); ok {
			i.runDefers(defers, e, true)
			return early.val, nil
		}
		i.runDefers(defers, e, false)
		return nil, err
	}
	i.runDefers(defers, e, r.flow == flowReturn && isErrReturnValue(r.val))
	if r.flow == flowReturn {
		return r.val, nil
	}
	return Void{}, nil
}

// shiftCount masks a runtime shift amount to the operand width, the
// same way the hardware backends (x86 `shl eax`/`shl rax`, arm64
// `lsl w`/`lsl x`) and wasm (shift count modulo bit-width) do: a
// 32-bit shift uses count & 31, a 64-bit shift uses count & 63. An
// unmasked count let `-8 >> 33` saturate to -1 in the interpreter
// while every codegen backend (count 33 & 31 == 1) returned -4.
// Width 0 (REPL paths before the checker assigns a width) keeps the
// historical 64-bit masking.
func shiftCount(rn Number, width int) Number {
	// Sub-i32 and i32 values all shift in 32-bit lanes (the codegen
	// backends widen u8 to i32 for arithmetic), so their count masks
	// to 0..31; only i64 masks to 0..63. Width 0 (REPL, pre-checker)
	// keeps the historical 64-bit masking.
	if width == 64 || width == 0 {
		return rn & 63
	}
	return rn & 31
}

// saturateFloatToInt converts a truncated float to a width-bit
// signed/unsigned integer with saturating (non-trapping) semantics:
// NaN → 0, and a magnitude past the destination's range clamps to
// its min (or 0 for unsigned) / max. This mirrors wasm's
// `trunc_sat_*` ops and the native backends' fcvtz / cvtt + fixup,
// so a `f as i32` cast agrees on every backend. The width-32 result
// is stored int32-truncated to match the interpreter's i32/u32
// storage convention (unsigned values ride sign-extended; callers
// reinterpret via uint32 at use). 2^63 / 2^64 are the first floats
// at/above the signed-i64 / unsigned-u64 max, so the `>=` guards
// keep the final Go conversion in range.
func saturateFloatToInt(f float64, width int, signed bool) int64 {
	if f != f { // NaN
		return 0
	}
	t := math.Trunc(f)
	if signed {
		if width == 64 {
			if t <= -9223372036854775808.0 {
				return math.MinInt64
			}
			if t >= 9223372036854775808.0 {
				return math.MaxInt64
			}
			return int64(t)
		}
		if t <= -2147483648.0 {
			return math.MinInt32
		}
		if t >= 2147483647.0 {
			return math.MaxInt32
		}
		return int64(int32(t))
	}
	// unsigned. The max (2^32-1 / 2^64-1) rides as all-ones, which
	// is -1 in the int64/int32-truncated storage the interpreter
	// uses for u32/u64.
	if t <= 0.0 {
		return 0
	}
	if width == 64 {
		if t >= 18446744073709551616.0 { // 2^64
			return -1
		}
		return int64(uint64(t))
	}
	if t >= 4294967296.0 { // 2^32
		return 4294967295 // u32 max, stored as its positive value
	}
	return int64(uint32(t))
}

// narrowInt wraps an int64 to a `width`-bit integer, matching the
// codegen backends' int→int narrowing. An unsigned narrow
// zero-extends (storing the true magnitude so a later widening
// cast zero-extends); a signed narrow sign-extends. Width 64 is
// identity (u64 rides as its bit pattern). Width 8 is always
// unsigned (u8 — i8 was retired in #4408).
func narrowInt(v int64, width int, unsigned bool) int64 {
	switch width {
	case 8:
		return int64(uint8(v))
	case 32:
		if unsigned {
			return int64(uint32(v))
		}
		return int64(int32(v))
	}
	return v
}

// satArith evaluates a saturating arithmetic operator (`+|` / `-|` /
// `*|`, #5542) on two width-masked operands, clamping to the operand
// type's [MIN, MAX] instead of wrapping. `signExtend` is the caller's
// width-and-signedness narrowing closure, used to recover the signed
// value of a masked operand. Mirrors the IR lowering in
// `internal/ir.(*builder).satBinary` — keep the two in step.
func satArith(op string, ln, rn Number, width int, unsigned bool, signExtend func(Number) Number) Number {
	if unsigned {
		var maxU uint64
		switch width {
		case 8:
			maxU = 255
		case 32:
			maxU = 0xFFFFFFFF
		default:
			maxU = ^uint64(0)
		}
		ul, ur := uint64(int64(ln)), uint64(int64(rn))
		var res uint64
		switch op {
		case "+|":
			if ul > maxU-ur {
				res = maxU
			} else {
				res = ul + ur
			}
		case "-|":
			if ul < ur {
				res = 0
			} else {
				res = ul - ur
			}
		case "<<|":
			c := uint64(shiftCount(rn, width))
			if ul > maxU>>c {
				res = maxU
			} else {
				res = ul << c
			}
		default:
			if ul != 0 && ur > maxU/ul {
				res = maxU
			} else {
				res = ul * ur
			}
		}
		return Number(int64(res))
	}
	var minS, maxS int64
	switch width {
	case 8:
		minS, maxS = -128, 127
	case 32:
		minS, maxS = math.MinInt32, math.MaxInt32
	default:
		minS, maxS = math.MinInt64, math.MaxInt64
	}
	il, ir := int64(signExtend(ln)), int64(signExtend(rn))
	switch op {
	case "+|":
		switch {
		case ir > 0 && il > maxS-ir:
			return Number(maxS)
		case ir < 0 && il < minS-ir:
			return Number(minS)
		}
		return Number(il + ir)
	case "-|":
		switch {
		case ir < 0 && il > maxS+ir:
			return Number(maxS)
		case ir > 0 && il < minS+ir:
			return Number(minS)
		}
		return Number(il - ir)
	case "<<|":
		// `a <<| c` overflows iff |a| exceeds the largest magnitude
		// that survives the shift. The two sides differ because |MIN|
		// is one larger than MAX; both bounds are exact divisions of a
		// power of two (and collapse to 0 once the masked count runs
		// past the width, which correctly saturates every non-zero a).
		c := uint64(shiftCount(rn, width))
		if il < 0 {
			if uint64(-(il+1))+1 > (uint64(maxS)+1)>>c {
				return Number(minS)
			}
		} else if uint64(il) > uint64(maxS)>>c {
			return Number(maxS)
		}
		return Number(il << c)
	}
	// `*|`: compare the magnitudes against the clamp limit — |MIN| is
	// one larger than MAX, so a negative product gets the extra headroom.
	absU := func(v int64) uint64 {
		if v < 0 {
			return uint64(-(v + 1)) + 1
		}
		return uint64(v)
	}
	au, bu := absU(il), absU(ir)
	neg := (il < 0) != (ir < 0)
	limit := uint64(maxS)
	if neg {
		limit = uint64(maxS) + 1
	}
	if au != 0 && bu > limit/au {
		if neg {
			return Number(minS)
		}
		return Number(maxS)
	}
	return Number(il * ir)
}

func (i *Interp) evalBinary(b *ast.Binary, env *env) (Value, error) {
	// Short-circuit logical operators.
	switch b.Op {
	case "&&":
		l, err := i.evalExpr(b.Left, env)
		if err != nil {
			return nil, err
		}
		if !asBool(l) {
			return Bool(false), nil
		}
		return i.evalExpr(b.Right, env)
	case "||":
		l, err := i.evalExpr(b.Left, env)
		if err != nil {
			return nil, err
		}
		if asBool(l) {
			return Bool(true), nil
		}
		return i.evalExpr(b.Right, env)
	}
	l, err := i.evalExpr(b.Left, env)
	if err != nil {
		return nil, err
	}
	r, err := i.evalExpr(b.Right, env)
	if err != nil {
		return nil, err
	}
	if b.IsStringConcat {
		ls, _ := l.(String)
		rs, _ := r.(String)
		return ls + rs, nil
	}
	// String comparison works at runtime regardless of whether the
	// checker has been run (so REPL evaluations of `"a" == "b"` give
	// a sensible answer too).
	if ls, lok := l.(String); lok {
		if rs, rok := r.(String); rok {
			switch b.Op {
			case "==":
				return Bool(ls == rs), nil
			case "!=":
				return Bool(ls != rs), nil
			}
		}
	}
	ln, lOk := l.(Number)
	rn, rOk := r.(Number)
	if lOk && rOk {
		// Mask the bit-pattern to the binary op's width so that
		// add/sub/mul wrap the same way wasm does, and so that
		// unsigned compares & shifts see only the canonical bits.
		// Width 0 (REPL paths where the checker hasn't fired)
		// keeps the historical 64-bit semantics.
		mask := func(v Number) Number {
			switch b.IntWidth {
			case 8:
				return Number(uint8(int64(v)))
			case 32:
				return Number(uint32(int64(v)))
			case 64:
				return v
			}
			return v
		}
		// signExtend narrows an arithmetic result back to the op's
		// width so sub-i32 / i32 arithmetic wraps the way every
		// codegen backend does (`255u8 + 1` → 0, not 256). Every
		// case honours signedness: an unsigned narrow zero-extends
		// (an unsigned value stores its true magnitude so a later
		// widening `as i64` zero-extends rather than sign-extends),
		// a signed narrow sign-extends. u64 has no positive int64
		// form, so it rides as its bit pattern (handled by the op's
		// own uint64 view); width 64 here is identity.
		signExtend := func(v Number) Number {
			switch b.IntWidth {
			case 8:
				// Always unsigned (u8 — i8 was retired in #4408).
				return Number(uint8(int64(v)))
			case 32:
				// Unsigned stores its true 0..2^32-1 value (positive
				// int64) so a widening `u32 as i64` zero-extends; a
				// sign-extended store would have widened to a negative
				// i64. Signed stays int32. Both reinterpret correctly
				// at narrowing casts and via uint32 in to_string.
				if b.IsUnsigned {
					return Number(uint32(int64(v)))
				}
				return Number(int32(int64(v)))
			}
			return v
		}
		ln, rn := mask(ln), mask(rn)
		switch b.Op {
		case "+":
			return signExtend(ln + rn), nil
		case "-":
			return signExtend(ln - rn), nil
		case "*":
			return signExtend(ln * rn), nil
		case "/":
			// Division never traps: x / 0 = 0 (the well-defined,
			// no-exceptions contract, matching arm64's sdiv/udiv).
			// INT_MIN / -1 wraps to INT_MIN, which Go's `/` already
			// defines (it only panics on a zero divisor).
			if rn == 0 {
				return signExtend(0), nil
			}
			if b.IsUnsigned {
				return signExtend(Number(uint64(ln) / uint64(rn))), nil
			}
			return signExtend(signExtend(ln) / signExtend(rn)), nil
		case "%":
			// Remainder never traps: x % 0 = x (matching arm64's
			// msub: rem = x - (x/0)*0 = x). INT_MIN % -1 = 0 is
			// already what Go's `%` yields.
			if rn == 0 {
				return signExtend(ln), nil
			}
			if b.IsUnsigned {
				return signExtend(Number(uint64(ln) % uint64(rn))), nil
			}
			return signExtend(signExtend(ln) % signExtend(rn)), nil
		case "+|", "-|", "*|", "<<|":
			// Saturating arithmetic (#5542): clamp to the operand
			// type's [MIN, MAX] rather than wrap. The result is in
			// range by construction, so signExtend only normalises
			// the stored representation.
			return signExtend(satArith(b.Op, ln, rn, b.IntWidth, b.IsUnsigned, signExtend)), nil
		case "&":
			return signExtend(ln & rn), nil
		case "|":
			return signExtend(ln | rn), nil
		case "^":
			return signExtend(ln ^ rn), nil
		case "<<":
			return signExtend(ln << shiftCount(rn, b.IntWidth)), nil
		case ">>":
			sc := shiftCount(rn, b.IntWidth)
			if b.IsUnsigned {
				return signExtend(Number(uint64(ln) >> sc)), nil
			}
			return signExtend(signExtend(ln) >> sc), nil
		case "==":
			return Bool(ln == rn), nil
		case "!=":
			return Bool(ln != rn), nil
		case "<":
			if b.IsUnsigned {
				return Bool(uint64(ln) < uint64(rn)), nil
			}
			return Bool(signExtend(ln) < signExtend(rn)), nil
		case "<=":
			if b.IsUnsigned {
				return Bool(uint64(ln) <= uint64(rn)), nil
			}
			return Bool(signExtend(ln) <= signExtend(rn)), nil
		case ">":
			if b.IsUnsigned {
				return Bool(uint64(ln) > uint64(rn)), nil
			}
			return Bool(signExtend(ln) > signExtend(rn)), nil
		case ">=":
			if b.IsUnsigned {
				return Bool(uint64(ln) >= uint64(rn)), nil
			}
			return Bool(signExtend(ln) >= signExtend(rn)), nil
		}
	}
	if lf, ok := l.(Float); ok {
		if rf, ok := r.(Float); ok {
			// Both sides must be the same width — the checker
			// rejects mismatched-width float ops, so the
			// interp just trusts and uses the LHS width.
			w := lf.Width
			lv := lf.V
			rv := rf.V
			if w == 32 {
				lv = float64(float32(lv))
				rv = float64(float32(rv))
			}
			mkFloat := func(v float64) Float {
				if w == 32 {
					v = float64(float32(v))
				}
				return Float{V: v, Width: w}
			}
			switch b.Op {
			case "+":
				return mkFloat(lv + rv), nil
			case "-":
				return mkFloat(lv - rv), nil
			case "*":
				return mkFloat(lv * rv), nil
			case "/":
				// IEEE-754: division by zero is well-defined
				// (yields ±Inf or NaN). Match that here rather
				// than erroring like the integer path does.
				return mkFloat(lv / rv), nil
			case "==":
				return Bool(lv == rv), nil
			case "!=":
				return Bool(lv != rv), nil
			case "<":
				return Bool(lv < rv), nil
			case "<=":
				return Bool(lv <= rv), nil
			case ">":
				return Bool(lv > rv), nil
			case ">=":
				return Bool(lv >= rv), nil
			}
		}
	}
	if lb, ok := l.(Bool); ok {
		if rb, ok := r.(Bool); ok {
			switch b.Op {
			case "==":
				return Bool(lb == rb), nil
			case "!=":
				return Bool(lb != rb), nil
			}
		}
	}
	return nil, fmt.Errorf("interp: %q on %T and %T not supported", b.Op, l, r)
}

func (i *Interp) evalAssign(a *ast.Assign, env *env) (Value, error) {
	v, err := i.evalExpr(a.Value, env)
	if err != nil {
		return nil, err
	}
	switch t := a.Target.(type) {
	case *ast.Ident:
		// Reassignment: the slot drops its old value and acquires the
		// new one. Retain before release so a self-assign (m = m.set(..)
		// returning the same in-place map) nets zero without dipping to
		// rc 0.
		old, _ := env.get(t.Name)
		retain(v)
		release(old)
		env.set(t.Name, v)
		return v, nil
	case *ast.Index:
		arrV, err := i.evalExpr(t.Array, env)
		if err != nil {
			return nil, err
		}
		idxV, err := i.evalExpr(t.Idx, env)
		if err != nil {
			return nil, err
		}
		arr, ok := arrV.(Array)
		if !ok {
			return nil, fmt.Errorf("array assignment to non-array %T", arrV)
		}
		idx, ok := idxV.(Number)
		if !ok {
			return nil, fmt.Errorf("array index must be number, got %T", idxV)
		}
		if idx < 0 || int(idx) >= len(arr) {
			return nil, fmt.Errorf("array index %d out of range [0, %d)", idx, len(arr))
		}
		retain(v)
		release(arr[idx])
		arr[idx] = v
		return v, nil
	case *ast.FieldAccess:
		tv, err := i.evalExpr(t.Target, env)
		if err != nil {
			return nil, err
		}
		s, ok := tv.(*Struct)
		if !ok {
			return nil, fmt.Errorf("field assignment on non-struct %T", tv)
		}
		retain(v)
		release(s.Fields[t.Field])
		s.Fields[t.Field] = v
		return v, nil
	}
	return nil, fmt.Errorf("interp: invalid assignment target %T", a.Target)
}

func asBool(v Value) bool {
	switch x := v.(type) {
	case Bool:
		return bool(x)
	case Number:
		return x != 0
	}
	return false
}
