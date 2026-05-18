// Package interp is a small tree-walking interpreter for the lang AST.
//
// It's used by the REPL (cmd/lang -repl) and by tests; production
// builds still go through the ARM64 / WASM code generators.
//
// Control flow inside a function uses a flow-tagged result value
// rather than panics: each statement returns a stmtResult whose Flow
// field tells the surrounding loop / block whether to keep going,
// break, continue, or unwind to the enclosing call site.
package interp

import (
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

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
// preserve insertion order — that matches the order the IR /
// codegen lowering uses for `keys()` / `values()` / iteration,
// so the differential oracle sees identical output across the
// interpreter and native backends. A `map[Value]Value` would
// be faster but Go's map can't key on non-comparable interface
// values (Array, Struct, *Enum, *Map), and we want any K
// shape that type-checks to work end-to-end.
type Map struct {
	keys []Value
	vals []Value
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

func (n Number) String() string  { return fmt.Sprintf("%d", int64(n)) }
func (b Bool) String() string {
	if b {
		return "true"
	}
	return "false"
}
func (s String) String() string  { return string(s) }
func (Void) String() string      { return "" }
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
func (f Func) String() string    { return "function " + f.Decl.Name }
func (Builtin) String() string   { return "<builtin>" }
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
		Global:   newEnv(nil),
	}
	i.Builtins["print"] = &Builtin{Fn: builtinPrint}
	i.Builtins["write"] = &Builtin{Fn: builtinWrite}
	i.Builtins["eprint"] = &Builtin{Fn: builtinEprint}
	i.Builtins["putchar"] = &Builtin{Fn: builtinPutchar}
	i.Builtins["len"] = &Builtin{Fn: builtinLen}
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
	// Map builtins. `map_new(cap)` returns an empty Map; the
	// per-method shims walk the parallel-slice representation
	// directly. Mirror the codegen surface from the checker's
	// `registerMapMethod` calls so user programs that go
	// through the interp see the same API as the native /
	// wasm backends.
	i.Builtins["map_new"] = &Builtin{Fn: builtinMapNew}
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
	// Low-level prelude primitives the codegen lowers to inline
	// alloc / memcpy / store-byte sequences. The interpreter
	// implements them directly so prelude functions that lean
	// on them (`__string_case_fold` for `to_upper`/`to_lower`,
	// `s.bytes()`, `string_from_bytes`, etc.) round-trip
	// through the script-mode + playground path. Map runtime
	// primitives (`__alloc`, `__load_ptr`, `__store_ptr`,
	// `__memcpy`, `__memset`, `__store_i32`, `__load_i32`,
	// `__ptr_width`) are NOT included here — they pretend to
	// be a flat byte address space the interpreter doesn't
	// model, so Map operations stay codegen-only for now.
	i.Builtins["__alloc_u8"] = &Builtin{Fn: builtinAllocU8}
	i.Builtins["string_from_bytes"] = &Builtin{Fn: builtinStringFromBytes}
	// `s.bytes()` and `s.as_bytes()` round-trip bytes through
	// raw memory in the prelude / wat-emitted helper (the
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
	i.Builtins["stdout"] = &Builtin{Fn: builtinStdout}
	i.Builtins["stderr"] = &Builtin{Fn: builtinStderr}
	i.Builtins["exit"] = &Builtin{Fn: builtinExit}
	i.Builtins["arena_save"] = &Builtin{Fn: builtinArenaSave}
	i.Builtins["arena_restore"] = &Builtin{Fn: builtinArenaRestore}
	i.Builtins["random_bytes"] = &Builtin{Fn: builtinRandomBytes}
	i.Builtins["int_to_string"] = &Builtin{Fn: builtinIntToString}
	i.Builtins["tcp_listen"] = &Builtin{Fn: builtinTcpListen}
	i.Builtins["tcp_accept"] = &Builtin{Fn: builtinTcpAccept}
	i.Builtins["tcp_recv"] = &Builtin{Fn: builtinTcpRecv}
	i.Builtins["tcp_send"] = &Builtin{Fn: builtinTcpSend}
	i.Builtins["tcp_close"] = &Builtin{Fn: builtinTcpClose}
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

// builtinArenaSave / builtinArenaRestore are no-ops in the
// tree-walking interpreter — Go's GC handles object lifetime.
// The signatures match the ARM64 / WASM runtime helpers
// (`number` opaque handle in/out) so user code that compiles
// to either backend behaves identically when run through
// the REPL.
func builtinArenaSave(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("arena_save: expected 0 args, got %d", len(args))
	}
	return Number(0), nil
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

// Map builtins. `map_new(cap)` ignores the capacity hint (the
// parallel-slice rep grows on demand) and returns a fresh
// empty *Map. The `__method_Map_*` shims mirror the codegen
// surface registered in internal/checker/checker.go's
// registerMapMethod calls — same signatures, same return
// shapes (Option[V] for get, boolean for has/delete, etc.).
// Insertion order is preserved across set/delete so keys() /
// values() round-trip stably across the diff oracle.
func builtinMapNew(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("map_new: expected 1 arg (cap), got %d", len(args))
	}
	return &Map{}, nil
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
	if idx := m.findKey(args[1]); idx >= 0 {
		m.vals[idx] = args[2]
	} else {
		m.keys = append(m.keys, args[1])
		m.vals = append(m.vals, args[2])
	}
	return Void{}, nil
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
		return Bool(false), nil
	}
	m.keys = append(m.keys[:idx], m.keys[idx+1:]...)
	m.vals = append(m.vals[:idx], m.vals[idx+1:]...)
	return Bool(true), nil
}

func builtinMapClear(_ *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_clear", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_Map_clear: expected 1 arg (receiver), got %d", len(args))
	}
	m.keys = m.keys[:0]
	m.vals = m.vals[:0]
	return Void{}, nil
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

// `__alloc_u8(n: i32): u8[]` — codegen lowers to `__lang_alloc(n)
// + length-prefix poke`; the interp returns a fresh Array of n
// Number(0) values. The prelude uses this as the staging buffer
// for `__string_case_fold`, `string_from_bytes`'s round-trip
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

// `string_from_bytes(bs: u8[]): string` — joins the byte
// values into a fresh String. Codegen path mmap-allocates a
// length-prefixed string buffer and memcpys; the interp
// builds the string directly from the Number values in the
// Array, narrowing each to a single byte (low 8 bits) so a
// caller-side u8/u16 width mismatch doesn't leak garbage
// into the result.
func builtinStringFromBytes(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("string_from_bytes: expected 1 arg (bs), got %d", len(args))
	}
	arr, ok := args[0].(Array)
	if !ok {
		return nil, fmt.Errorf("string_from_bytes: arg must be array, got %T", args[0])
	}
	buf := make([]byte, len(arr))
	for i, v := range arr {
		n, ok := v.(Number)
		if !ok {
			return nil, fmt.Errorf("string_from_bytes: element %d not a number (%T)", i, v)
		}
		buf[i] = byte(int64(n) & 0xff)
	}
	return String(buf), nil
}

// `__method_string_bytes` / `__method_string_as_bytes` —
// String → Array<Number> conversion, one Number per UTF-8
// byte. Sidesteps the prelude's `__memcpy(out as i32,
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

func builtinArenaRestore(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("arena_restore: expected 1 arg, got %d", len(args))
	}
	if _, ok := args[0].(Number); !ok {
		return nil, fmt.Errorf("arena_restore: expected number arg, got %T", args[0])
	}
	return Void{}, nil
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
// $read_file / __lang_read_file runtime helpers. Reads the
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

// builtinWriteFile mirrors $write_file / __lang_write_file:
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
		return optionSome(classifyIoError(string(path), err)), nil
	}
	return optionNone(), nil
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
		return nil, fmt.Errorf("len: expected 1 arg, got %d", len(args))
	}
	switch v := args[0].(type) {
	case String:
		return Number(int64(len(string(v)))), nil
	case Array:
		return Number(int64(len(v))), nil
	}
	return nil, fmt.Errorf("len: expected string or array, got %T", args[0])
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

// Register adds a user-defined function to the interpreter. Subsequent
// declarations of the same name overwrite the previous one (handy for
// REPL redefinitions).
func (i *Interp) Register(fn *ast.FuncDecl) { i.Funcs[fn.Name] = fn }

// RegisterEnum makes an enum decl visible to subsequent eval calls.
// Variant constructors and `match` patterns find their variants by
// walking the registered enums; tests / the REPL must call this
// once per top-level enum after parsing.
func (i *Interp) RegisterEnum(ed *ast.EnumDecl) { i.Enums[ed.Name] = ed }

// findVariant returns the owning enum and variant index for an
// unqualified variant name. Tracking lookups on demand keeps
// Interp.RegisterEnum cheap; the cost only shows up at evaluation
// of variant constructors and match arms.
func (i *Interp) findVariant(name string) (*ast.EnumDecl, int, bool) {
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

// declare always binds in the innermost scope (for `var` decls).
func (e *env) declare(name string, v Value) { e.vars[name] = v }

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
}

func (i *Interp) callFunc(fn *ast.FuncDecl, args []Value) (Value, error) {
	if len(args) != len(fn.Params) {
		return nil, fmt.Errorf("%s: expected %d args, got %d", fn.Name, len(fn.Params), len(args))
	}
	e := newEnv(nil)
	for k, p := range fn.Params {
		e.declare(p.Name, args[k])
	}
	r, err := i.execBlock(fn.Body, e)
	if err != nil {
		return nil, err
	}
	if r.flow == flowReturn {
		return r.val, nil
	}
	return Void{}, nil
}

func (i *Interp) execBlock(b *ast.Block, parent *env) (result, error) {
	e := newEnv(parent)
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

func (i *Interp) execStmt(s ast.Stmt, e *env) (result, error) {
	switch x := s.(type) {
	case *ast.Block:
		return i.execBlock(x, e)
	case *ast.Arena:
		// The interp doesn't have a bump allocator to snap;
		// arena {…} just runs the body as a normal scope.
		return i.execBlock(x.Body, e)
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
				break
			}
			// flowContinue or flowNormal: re-test the condition.
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
				break
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
		return result{flow: flowBreak}, nil
	case *ast.Continue:
		return result{flow: flowContinue}, nil
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
	case *ast.Switch:
		tag, err := i.evalExpr(x.Tag, e)
		if err != nil {
			return result{}, err
		}
		matched := false
		for _, k := range x.Cases {
			for _, vexpr := range k.Values {
				v, err := i.evalExpr(vexpr, e)
				if err != nil {
					return result{}, err
				}
				if valuesEqual(tag, v) {
					matched = true
					break
				}
			}
			if matched {
				r, err := i.execBlock(k.Body, e)
				if err != nil {
					return result{}, err
				}
				if r.flow == flowReturn || r.flow == flowContinue {
					return r, nil
				}
				// flowBreak / flowNormal: leave the switch.
				return result{flow: flowNormal}, nil
			}
		}
		if x.Default != nil {
			r, err := i.execBlock(x.Default, e)
			if err != nil {
				return result{}, err
			}
			if r.flow == flowReturn || r.flow == flowContinue {
				return r, nil
			}
		}
		return result{flow: flowNormal}, nil
	case *ast.FuncDecl:
		return result{}, fmt.Errorf("interp: nested functions / closures are not yet supported in the tree-walking interpreter (compile and run via the wasm backend)")
	case *ast.Match:
		tag, err := i.evalExpr(x.Tag, e)
		if err != nil {
			return result{}, err
		}
		ev, ok := tag.(*Enum)
		if !ok {
			return result{}, fmt.Errorf("interp: match scrutinee is %T, expected enum value", tag)
		}
		for _, arm := range x.Arms {
			if arm.IsWildcard || arm.VariantName == ev.VariantName {
				armEnv := newEnv(e)
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
	return result{}, fmt.Errorf("interp: unsupported statement %T", s)
}

// valuesEqual is a switch-tag equality check. Numbers, Bools and
// Strings compare by content; other types compare via Go's `==` which
// is a sensible fallback (Func references, Void, etc.).
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
	}
	return a == b
}

func (i *Interp) evalExpr(e ast.Expr, env *env) (Value, error) {
	switch x := e.(type) {
	case *ast.NumberLit:
		return Number(x.Value), nil
	case *ast.CastExpr:
		// All integers live in `Number` (int64) at interp time, so
		// most numeric casts are no-ops semantically. The exception
		// is i64 → i32, which truncates the high 32 bits. Float
		// casts aren't wired here because the interpreter doesn't
		// currently model float values; the WASM / arm64 backends
		// own floats end-to-end.
		v, err := i.evalExpr(x.Inner, env)
		if err != nil {
			return nil, err
		}
		if tgt, ok := x.Target.(ast.NumberType); ok {
			if src, ok := v.(Number); ok {
				if tgt.NormalWidth() == 32 {
					return Number(int32(int64(src))), nil
				}
				return src, nil
			}
		}
		return nil, fmt.Errorf("cast from %T to %s not supported in the interpreter", v, x.Target)
	case *ast.BoolLit:
		return Bool(x.Value), nil
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
		// Variants with payloads must be called explicitly.
		if ed, idx, ok := i.findVariant(x.Name); ok {
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
			n, _ := v.(Number)
			return -n, nil
		case "!":
			b, _ := v.(Bool)
			return !b, nil
		}
		return nil, fmt.Errorf("interp: unsupported unary %q", x.Op)
	case *ast.Assign:
		return i.evalAssign(x, env)
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
		ev, ok := tag.(*Enum)
		if !ok {
			return nil, fmt.Errorf("interp: match scrutinee is %T, expected enum value", tag)
		}
		for _, arm := range x.Arms {
			if !arm.IsWildcard && arm.VariantName != ev.VariantName {
				continue
			}
			armEnv := newEnv(env)
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
	case *ast.TryOp:
		// The interp's expression evaluator can't unwind the
		// enclosing function early (statement-level flow control
		// uses a result-flow flag the expression layer doesn't
		// thread). The wasm + arm64 backends are what users run;
		// the interp is a sanity-check sandbox so `?` is simply
		// not supported here.
		return nil, fmt.Errorf("interp: postfix `?` operator is not supported in the interpreter; compile to wasm or arm64 instead")
	case *ast.StructLit:
		s := &Struct{TypeName: x.TypeName, Fields: map[string]Value{}}
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
	if id, ok := c.Callee.(*ast.Ident); ok {
		// Variant constructor: resolve the name across all
		// registered enums and build an Enum value with the
		// evaluated payloads.
		if ed, idx, ok := i.findVariant(id.Name); ok {
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
			if fv, ok := v.(Func); ok {
				return i.callFunc(fv.Decl, args)
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
	if fv, ok := cv.(Func); ok {
		return i.callFunc(fv.Decl, args)
	}
	return nil, fmt.Errorf("interp: not a function: %T", cv)
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
			case 32:
				return Number(uint32(int64(v)))
			case 64:
				return v
			}
			return v
		}
		signExtend := func(v Number) Number {
			if b.IntWidth == 32 {
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
			if rn == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			if b.IsUnsigned {
				return signExtend(Number(uint64(ln) / uint64(rn))), nil
			}
			return signExtend(signExtend(ln) / signExtend(rn)), nil
		case "%":
			if rn == 0 {
				return nil, fmt.Errorf("modulo by zero")
			}
			if b.IsUnsigned {
				return signExtend(Number(uint64(ln) % uint64(rn))), nil
			}
			return signExtend(signExtend(ln) % signExtend(rn)), nil
		case "&":
			return signExtend(ln & rn), nil
		case "|":
			return signExtend(ln | rn), nil
		case "^":
			return signExtend(ln ^ rn), nil
		case "<<":
			return signExtend(ln << rn), nil
		case ">>":
			if b.IsUnsigned {
				return signExtend(Number(uint64(ln) >> uint64(rn))), nil
			}
			return signExtend(signExtend(ln) >> rn), nil
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
