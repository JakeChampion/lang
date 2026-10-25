package interp

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/tables/strerror"
	"github.com/jakechampion/lang/internal/tools/tty"
)

type tcpListenerHandle = net.Listener
type tcpConnHandle = net.Conn

func tcpNetListen(network, address string) (tcpListenerHandle, error) {
	return net.Listen(network, address)
}

type Value interface {
	String() string
}

type Number int64
type Bool bool
type String string
type Void struct{}

type Func struct{ Decl *ast.FuncDecl }

func ExitStatus(v Value) (int, bool) {
	switch x := v.(type) {
	case Number:
		return int(x) & 0xFF, true
	case Bool:
		if x {
			return 1, true
		}
		return 0, true
	case Void:
		return 0, true
	}
	return 0, false
}

// An Array is a compiled array's box, or a view onto part of one. Every holder
// of the array shares the box, so a builtin that fills it in place or sets its
// length reaches them all.
type Array struct {
	b   *arrBox
	off int
	n   int // the view's length, or wholeBox
}

const wholeBox = -1

// len(e) is the array's length and cap(e) its storage. A box made by
// appending into spare capacity shares that storage with the box it grew
// from until one of them is filled in place (privatise).
type arrBox struct {
	e      []Value
	st     *arrStore
	owners int32
	paths  int32
	held   int // the most elements the box has had; later slots are not its own
}

// arrStore sums owners and paths over every box on one storage.
type arrStore struct {
	owners int32
	paths  int32
	boxes  int32
}

func boxOf(e []Value) Array {
	return Array{b: &arrBox{e: e, st: &arrStore{boxes: 1}, held: len(e)}, n: wholeBox}
}

func newArray(n int) Array { return boxOf(make([]Value, n)) }

func arrayOf(vs ...Value) Array { return boxOf(vs) }

func (a Array) Elems() []Value {
	if a.n == wholeBox {
		return a.b.e
	}
	return a.b.e[a.off : a.off+a.n : a.off+a.n]
}

func (a Array) view(lo, hi int) Array {
	if a.n != wholeBox {
		lo, hi = a.off+lo, a.off+hi
	}
	return Array{b: a.b, off: lo, n: hi - lo}
}

func (a Array) exclusive() bool { return a.b.st.owners == 0 }

// intoElems returns the elements a builtin fills in place.
func (a Array) intoElems() []Value {
	a.b.privatise(0)
	return a.Elems()
}

// privatise gives the box storage of its own, of at least capacity slots,
// when it shares its storage with another box or has fewer slots.
func (b *arrBox) privatise(capacity int) {
	if b.st.boxes == 1 && capacity <= cap(b.e) {
		return
	}
	s := make([]Value, max(capacity, cap(b.e)))
	copy(s, b.e[:b.held])
	for k := b.held; k < len(s); k++ {
		s[k] = unclaimedSlot
	}
	b.st.boxes--
	b.st.owners -= b.owners
	b.st.paths -= b.paths
	b.st = &arrStore{owners: b.owners, paths: b.paths, boxes: 1}
	b.e = s[:len(b.e)]
}

type Cell struct{ V Value }

func (*Cell) String() string { return "<cell>" }

type Float struct {
	V     float64
	Width int
}

type DynInt struct {
	N        Number
	TypeName string
}

func (d DynInt) String() string { return d.N.String() }

type Struct struct {
	TypeName string
	Fields   map[string]Value
}

type Enum struct {
	EnumName    string
	VariantName string
	Index       int
	Payloads    []Value
	counts      uint8
}

const (
	countsSome uint8 = 1
	countsNone uint8 = 2
)

func reachesCounted(v Value) bool {
	switch x := v.(type) {
	case *Map, Array, *Struct:
		return true
	case *Enum:
		if x.counts == 0 {
			x.counts = countsNone
			for _, p := range x.Payloads {
				if reachesCounted(p) {
					x.counts = countsSome
					break
				}
			}
		}
		return x.counts == countsSome
	}
	return false
}

type Map struct {
	keys []Value
	vals []Value
	rc   int
}

func (m *Map) clone() *Map {
	return &Map{
		keys: append([]Value(nil), m.keys...),
		vals: append([]Value(nil), m.vals...),
	}
}

var rcWalked atomic.Int64

func retain(v Value)  { adjustRC(v, +1) }
func release(v Value) { adjustRC(v, -1) }

func retainReplacing(v, old Value) {
	if tail, ok := appendedTail(old, v); ok {
		v.(Array).b.paths++
		old.(Array).b.paths--
		for _, e := range tail {
			retain(e)
		}
		return
	}
	retain(v)
	release(old)
}

func appendedTail(old, next Value) ([]Value, bool) {
	na, ok := next.(Array)
	if !ok {
		return nil, false
	}
	oa, ok := old.(Array)
	if !ok {
		return nil, false
	}
	ne, oe := na.Elems(), oa.Elems()
	if oa.b.st != na.b.st || len(ne) < len(oe) {
		return nil, false
	}
	return ne[len(oe):], true
}

func arrOwn(v Value) {
	if a, ok := v.(Array); ok {
		a.b.owners++
		a.b.st.owners++
	}
}

func arrDisown(v Value) {
	if a, ok := v.(Array); ok {
		a.b.owners--
		a.b.st.owners--
	}
}

func storeArray(v Value) { arrOwn(v) }

func adjustRC(v Value, delta int) {
	switch x := v.(type) {
	case *Map:
		x.rc += delta
		for _, kv := range x.keys {
			adjustRC(kv, delta)
		}
		for _, vv := range x.vals {
			adjustRC(vv, delta)
		}
	case Array:
		x.b.paths += int32(delta)
		x.b.st.paths += int32(delta)
		els := x.Elems()
		rcWalked.Add(int64(len(els)))
		for _, e := range els {
			adjustRC(e, delta)
		}
	case *Struct:
		for _, f := range x.Fields {
			adjustRC(f, delta)
		}
	case *Enum:
		if !reachesCounted(x) {
			return
		}
		rcWalked.Add(int64(len(x.Payloads)))
		for _, p := range x.Payloads {
			adjustRC(p, delta)
		}
	}
}

func (i *Interp) findKey(m *Map, k Value) (int, error) {
	eq := i.keyEq(k)
	for idx, kk := range m.keys {
		if eq == nil {
			if valuesEqual(kk, k) {
				return idx, nil
			}
			continue
		}
		same, err := i.callFunc(eq, []Value{kk, k})
		if err != nil {
			return -1, err
		}
		if b, ok := same.(Bool); ok && bool(b) {
			return idx, nil
		}
	}
	return -1, nil
}

func (i *Interp) keyEq(k Value) *ast.FuncDecl {
	var tn string
	switch x := k.(type) {
	case *Struct:
		tn = x.TypeName
	case *Enum:
		tn = x.EnumName
	default:
		return nil
	}
	return i.Funcs[i.keyEqs[tn]]
}

const (
	CauseArrOOB     = "fern: array index out of range"
	CauseStrOOB     = "fern: string index out of range"
	CauseSliceRange = "fern: slice range out of bounds"
	CauseAllocSize  = "fern: allocation size out of range"
)

const AbortStatus = 134

type Abort struct{ Cause, Detail string }

func (a *Abort) Error() string { return a.Cause + "\n  " + a.Detail }

func indexAbort(cause, idx string, n int) error {
	return &Abort{cause, fmt.Sprintf("index %s, length %d", idx, n)}
}

func sliceAbort(cause, what string, lo, hi, n int64) error {
	return &Abort{cause, fmt.Sprintf("%s [%d:%d], length %d", what, lo, hi, n)}
}

type Builtin struct {
	Fn func(*Interp, []Value) (Value, error)
}

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
	case DynInt:
		return x.TypeName, true
	case Number:
		return "i32", true
	}
	return "", false
}

func dynPayload(v Value) Value {
	if d, ok := v.(DynInt); ok {
		return d.N
	}
	return v
}

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
	for i, v := range a.Elems() {
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

type Interp struct {
	Funcs           map[string]*ast.FuncDecl
	Enums           map[string]*ast.EnumDecl
	Builtins        map[string]*Builtin
	Stdout          io.Writer
	Stderr          io.Writer
	Stdin           io.Reader
	Env             map[string]string
	Exiter          func(code int)
	openFiles       map[int64]*os.File
	nextFd          int64
	closedStd       map[int64]bool
	deferStack      [][]deferEntry
	tcpListeners    map[int64]tcpListenerHandle
	tcpConns        map[int64]tcpConnHandle
	tcpNextHandle   int64
	udpSocks        map[int64]int
	timerFds        map[int64]int
	tcpNonblocking  map[int64]bool
	reactors        map[int64]*reactor
	reactorNext     int64
	Args            []string
	Global          *env
	bufs            map[int64][]byte
	nextBuf         int64
	traitMethods    map[string]string
	keyEqs          map[string]string
	dynSites        map[ast.Expr]checker.DynCoercion
	arrayGrowCopies int
	arraySetCopies  int
	arrayCOW        arrayCOWMode
	envs            []*env
	moving          []Value
}

type arrayCOWMode int

const (
	arrayCOWCounted arrayCOWMode = iota
	arrayCOWCopy
	arrayCOWVerify
)

func arrayCOWFromEnv() arrayCOWMode {
	switch os.Getenv("FERN_INTERP_ARRAY_COW") {
	case "copy":
		return arrayCOWCopy
	case "verify":
		return arrayCOWVerify
	}
	return arrayCOWCounted
}

func New() *Interp {
	i := &Interp{
		Funcs:        map[string]*ast.FuncDecl{},
		arrayCOW:     arrayCOWFromEnv(),
		traitMethods: map[string]string{},
		keyEqs:       map[string]string{},
		Enums:        map[string]*ast.EnumDecl{},
		Builtins:     map[string]*Builtin{},
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		Stdin:        os.Stdin,
		Exiter:       os.Exit,
		openFiles:    map[int64]*os.File{},
		nextFd:       100,
		bufs:         map[int64][]byte{},
		nextBuf:      1,
		Global:       newEnv(nil),
	}
	i.Builtins["print"] = &Builtin{Fn: builtinPrint}
	i.Builtins["write"] = &Builtin{Fn: builtinWrite}
	i.Builtins["eprint"] = &Builtin{Fn: builtinEprint}
	i.Builtins["putchar"] = &Builtin{Fn: builtinPutchar}
	i.Builtins["poll"] = &Builtin{Fn: builtinPoll}
	i.Builtins["isatty"] = &Builtin{Fn: builtinIsatty}
	i.Builtins["window_size"] = &Builtin{Fn: builtinWindowSize}
	i.Builtins["set_window_size"] = &Builtin{Fn: builtinSetWindowSize}
	i.Builtins["termios_get"] = &Builtin{Fn: builtinTermiosGet}
	i.Builtins["termios_set"] = &Builtin{Fn: builtinTermiosSet}
	i.Builtins["set_extproc"] = &Builtin{Fn: builtinSetExtproc}
	i.Builtins["target_os"] = &Builtin{Fn: builtinTargetOS}
	i.Builtins["target_arch"] = &Builtin{Fn: builtinTargetArch}
	i.Builtins["buf_new"] = &Builtin{Fn: builtinBufNew}
	i.Builtins["buf_push"] = &Builtin{Fn: builtinBufPush}
	i.Builtins["buf_push_range"] = &Builtin{Fn: builtinBufPushRange}
	i.Builtins["buf_push_bytes_range"] = &Builtin{Fn: builtinBufPushBytesRange}
	i.Builtins["buf_push_bytes_mapped"] = &Builtin{Fn: func(i *Interp, args []Value) (Value, error) { return builtinBufPushBytesTable(i, args, "mapped") }}
	i.Builtins["buf_push_mapped"] = &Builtin{Fn: builtinBufPushMapped}
	i.Builtins["buf_push_bytes_filtered"] = &Builtin{Fn: func(i *Interp, args []Value) (Value, error) { return builtinBufPushBytesTable(i, args, "filtered") }}
	i.Builtins["buf_push_filtered"] = &Builtin{Fn: builtinBufPushFiltered}
	i.Builtins["buf_push_bytes_expanded"] = &Builtin{Fn: func(i *Interp, args []Value) (Value, error) { return builtinBufPushBytesTable(i, args, "expanded") }}
	i.Builtins["buf_push_expanded"] = &Builtin{Fn: builtinBufPushExpanded}
	i.Builtins["buf_push_byte"] = &Builtin{Fn: builtinBufPushByte}
	i.Builtins["buf_push_u64"] = &Builtin{Fn: builtinBufPushU64}
	i.Builtins["buf_len"] = &Builtin{Fn: builtinBufLen}
	i.Builtins["buf_take"] = &Builtin{Fn: builtinBufTake}
	i.Builtins["buf_take_bytes"] = &Builtin{Fn: builtinBufTakeBytes}
	i.Builtins["buf_free"] = &Builtin{Fn: builtinBufFree}
	i.Builtins["buf_clear"] = &Builtin{Fn: builtinBufClear}
	i.Builtins["__method_string_len"] = &Builtin{Fn: builtinLen}
	i.Builtins["__method_Array_len"] = &Builtin{Fn: builtinLen}
	i.Builtins["__method_slice_len"] = &Builtin{Fn: builtinLen}
	i.Builtins["args"] = &Builtin{Fn: builtinArgs}
	i.Builtins["set_args"] = &Builtin{Fn: builtinSetArgs}
	i.Builtins["env"] = &Builtin{Fn: builtinEnv}
	i.Builtins["environ"] = &Builtin{Fn: builtinEnviron}
	i.Builtins["read_file"] = &Builtin{Fn: builtinReadFile}
	i.Builtins["read_file_bytes"] = &Builtin{Fn: builtinReadFileBytes}
	i.Builtins["write_file"] = &Builtin{Fn: builtinWriteFile}
	i.Builtins["write_file_bytes"] = &Builtin{Fn: builtinWriteFileBytes}
	i.Builtins["write_file_exec"] = &Builtin{Fn: builtinWriteFileExec}
	i.Builtins["open_reader"] = &Builtin{Fn: builtinOpenReader}
	i.Builtins["open_writer"] = &Builtin{Fn: builtinOpenWriter}
	i.Builtins["open_appender"] = &Builtin{Fn: builtinOpenAppender}
	i.Builtins["open_exclusive"] = &Builtin{Fn: builtinOpenExclusive}
	i.Builtins["open_reader_with"] = &Builtin{Fn: builtinOpenReaderWith}
	i.Builtins["open_writer_with"] = &Builtin{Fn: builtinOpenWriterWith}
	i.Builtins["__method_Reader_read_chunk"] = &Builtin{Fn: builtinReaderReadChunk}
	i.Builtins["__method_Reader_read_chunk_bytes"] = &Builtin{Fn: builtinReaderReadChunkBytes}
	i.Builtins["__method_Reader_close"] = &Builtin{Fn: builtinReaderClose}
	i.Builtins["open_dir"] = &Builtin{Fn: builtinOpenDir}
	i.Builtins["__method_Dir_open_dir"] = &Builtin{Fn: builtinDirOpenDir}
	i.Builtins["__method_Dir_entries"] = &Builtin{Fn: builtinDirEntries}
	i.Builtins["__method_Dir_entries_bytes"] = &Builtin{Fn: builtinDirEntriesBytes}
	i.Builtins["__method_Dir_stat"] = &Builtin{Fn: builtinDirStat(true)}
	i.Builtins["__method_Dir_lstat"] = &Builtin{Fn: builtinDirStat(false)}
	i.Builtins["__method_Dir_access"] = &Builtin{Fn: builtinDirAccess}
	i.Builtins["__method_Dir_remove_file"] = &Builtin{Fn: builtinDirRemove(false)}
	i.Builtins["__method_Dir_remove_dir"] = &Builtin{Fn: builtinDirRemove(true)}
	i.Builtins["__method_Dir_chmod"] = &Builtin{Fn: builtinDirChmod}
	i.Builtins["__method_Dir_chown"] = &Builtin{Fn: builtinDirChown}
	i.Builtins["__method_Dir_close"] = &Builtin{Fn: builtinReaderClose}
	i.Builtins["__method_Reader_stat"] = &Builtin{Fn: builtinFdStat}
	i.Builtins["__method_Writer_stat"] = &Builtin{Fn: builtinFdStat}
	i.Builtins["__method_Reader_seek"] = &Builtin{Fn: builtinHandleSeek}
	i.Builtins["__method_Writer_seek"] = &Builtin{Fn: builtinHandleSeek}
	i.Builtins["__method_Reader_splice_to"] = &Builtin{Fn: builtinReaderSpliceTo}
	i.Builtins["__method_Reader_copy_range_to"] = &Builtin{Fn: builtinReaderSpliceTo}
	i.Builtins["__method_Reader_flags"] = &Builtin{Fn: builtinFdFlags}
	i.Builtins["__method_Writer_flags"] = &Builtin{Fn: builtinFdFlags}
	i.Builtins["__method_Reader_isatty"] = &Builtin{Fn: builtinHandleIsatty}
	i.Builtins["__method_Reader_window_size"] = &Builtin{Fn: builtinHandleWindowSize}
	i.Builtins["__method_Reader_set_window_size"] = &Builtin{Fn: builtinHandleSetWindowSize}
	i.Builtins["__method_Reader_termios_get"] = &Builtin{Fn: builtinHandleTermiosGet}
	i.Builtins["__method_Reader_termios_set"] = &Builtin{Fn: builtinHandleTermiosSet}
	i.Builtins["__method_Reader_set_extproc"] = &Builtin{Fn: builtinHandleSetExtproc}
	i.Builtins["__method_Writer_isatty"] = &Builtin{Fn: builtinHandleIsatty}
	i.Builtins["__method_Writer_write"] = &Builtin{Fn: builtinWriterWrite}
	i.Builtins["__method_Writer_write_some"] = &Builtin{Fn: builtinWriterWriteSome}
	i.Builtins["__method_Writer_splice_forever"] = &Builtin{Fn: builtinWriterSpliceForever}
	i.Builtins["__method_Writer_write_bytes"] = &Builtin{Fn: builtinWriterWriteBytes}
	i.Builtins["__method_Writer_write_some_bytes"] = &Builtin{Fn: builtinWriterWriteSomeBytes}
	i.Builtins["__method_Writer_close"] = &Builtin{Fn: builtinWriterClose}
	i.Builtins["__method_Reader_fsync"] = &Builtin{Fn: builtinFsync}
	i.Builtins["__method_Writer_fsync"] = &Builtin{Fn: builtinFsync}
	i.Builtins["__method_Reader_fdatasync"] = &Builtin{Fn: builtinFdatasync}
	i.Builtins["__method_Writer_fdatasync"] = &Builtin{Fn: builtinFdatasync}
	i.Builtins["__method_Reader_drop_cache"] = &Builtin{Fn: builtinDropCache}
	i.Builtins["__method_Writer_drop_cache"] = &Builtin{Fn: builtinDropCache}
	i.Builtins["__method_Reader_syncfs"] = &Builtin{Fn: builtinSyncfs}
	i.Builtins["__method_Writer_syncfs"] = &Builtin{Fn: builtinSyncfs}
	i.Builtins["__method_Writer_truncate"] = &Builtin{Fn: builtinWriterTruncate}
	i.Builtins["__method_Reader_dup_onto"] = &Builtin{Fn: builtinDupOnto}
	i.Builtins["__method_Writer_dup_onto"] = &Builtin{Fn: builtinDupOnto}
	i.Builtins["__method_Array_push"] = &Builtin{Fn: builtinArrayPush}
	i.Builtins["__method_Array_set"] = &Builtin{Fn: builtinArraySet}
	i.Builtins["map_new"] = &Builtin{Fn: builtinMapNew}
	i.Builtins["cell_new"] = &Builtin{Fn: builtinCellNew}
	i.Builtins["__method_Cell_get"] = &Builtin{Fn: builtinCellGet}
	i.Builtins["__method_Cell_set"] = &Builtin{Fn: builtinCellSet}
	i.Builtins["__method_Map_len"] = &Builtin{Fn: builtinMapLen}
	i.Builtins["__method_Map_contains"] = &Builtin{Fn: builtinMapHas}
	i.Builtins["__method_Map_get"] = &Builtin{Fn: builtinMapGet}
	i.Builtins["__method_Map_set"] = &Builtin{Fn: builtinMapSet}
	i.Builtins["__method_Map_remove"] = &Builtin{Fn: builtinMapDelete}
	i.Builtins["__method_Map_clear"] = &Builtin{Fn: builtinMapClear}
	i.Builtins["__method_Map_get_or"] = &Builtin{Fn: builtinMapGetOr}
	i.Builtins["__method_Map_keys"] = &Builtin{Fn: builtinMapKeys}
	i.Builtins["__method_Map_values"] = &Builtin{Fn: builtinMapValues}
	i.Builtins["__alloc_u8"] = &Builtin{Fn: builtinAllocFilled("__alloc_u8", Number(0))}
	i.Builtins["__arr_set_len"] = &Builtin{Fn: builtinArrSetLen}
	i.Builtins["__alloc_i32"] = &Builtin{Fn: builtinAllocFilled("__alloc_i32", Number(0))}
	i.Builtins["__alloc_i64"] = &Builtin{Fn: builtinAllocFilled("__alloc_i64", Number(0))}
	i.Builtins["__alloc_bool"] = &Builtin{Fn: builtinAllocFilled("__alloc_bool", Bool(false))}
	i.Builtins["string_from_bytes_unchecked"] = &Builtin{Fn: builtinStringFromBytes}
	i.Builtins["string_from_bytes_range_unchecked"] = &Builtin{Fn: builtinStringFromBytesRange}
	i.Builtins["slice_unchecked"] = &Builtin{Fn: builtinSliceUnchecked}
	i.Builtins["__method_string_bytes"] = &Builtin{Fn: builtinStringBytes}
	i.Builtins["__method_string_as_bytes"] = &Builtin{Fn: builtinStringBytes}
	i.Builtins["__method_boolean_to_string"] = &Builtin{Fn: builtinBoolToString}
	i.Builtins["stdin"] = &Builtin{Fn: builtinStdin}
	i.Builtins["stdout"] = &Builtin{Fn: builtinStdout}
	i.Builtins["stderr"] = &Builtin{Fn: builtinStderr}
	i.Builtins["exit"] = &Builtin{Fn: builtinExit}
	i.Builtins["random_bytes"] = &Builtin{Fn: builtinRandomBytes}
	i.Builtins["random_i32"] = &Builtin{Fn: builtinRandomI32}
	i.Builtins["__heap_bump_bytes"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
	i.Builtins["__heap_alloc_count"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
	ctMark := &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Void{}, nil }}
	i.Builtins["__ct_secret"] = ctMark
	i.Builtins["__ct_public"] = ctMark
	i.Builtins["__bytes_owned"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		v, err := byteViews("__bytes_owned", args, 1)
		if err != nil {
			return nil, err
		}
		return byteArray(v[0]), nil
	}}
	i.Builtins["__bytes_range"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("__bytes_range: expected 3 arguments, got %d", len(args))
		}
		v, err := byteViews("__bytes_range", args[:1], 1)
		if err != nil {
			return nil, err
		}
		from, ok1 := args[1].(Number)
		to, ok2 := args[2].(Number)
		if !ok1 || !ok2 || from < 0 || to < from || int(to) > len(v[0]) {
			return nil, fmt.Errorf("__bytes_range: range out of bounds")
		}
		return byteArray(v[0][int(from):int(to)]), nil
	}}
	i.Builtins["__bytes_into"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 5 {
			return nil, fmt.Errorf("__bytes_into: expected 5 arguments, got %d", len(args))
		}
		v, err := byteViews("__bytes_into", []Value{args[0], args[2]}, 2)
		if err != nil {
			return nil, err
		}
		at, ok1 := args[1].(Number)
		from, ok2 := args[3].(Number)
		n, ok3 := args[4].(Number)
		if !ok1 || !ok2 || !ok3 || n < 0 || at < 0 || from < 0 || int(at)+int(n) > len(v[0]) || int(from)+int(n) > len(v[1]) {
			return nil, fmt.Errorf("__bytes_into: range out of bounds")
		}
		out := append([]byte(nil), v[0]...)
		copy(out[int(at):int(at)+int(n)], v[1][int(from):int(from)+int(n)])
		return byteArray(out), nil
	}}
	registerAESGCM(i)
	i.Builtins["__rc_underflow_count"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
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
	i.Builtins["__mulhi_u64"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("__mulhi_u64: expected 2 args, got %d", len(args))
		}
		a, ok1 := args[0].(Number)
		b, ok2 := args[1].(Number)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("__mulhi_u64: expected integers, got %T, %T", args[0], args[1])
		}
		hi, _ := bits.Mul64(uint64(int64(a)), uint64(int64(b)))
		return Number(int64(hi)), nil
	}}
	i.Builtins["__memchr"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("__memchr: expected 3 args, got %d", len(args))
		}
		s, ok := args[0].(String)
		if !ok {
			return nil, fmt.Errorf("__memchr: expected a string, got %T", args[0])
		}
		bn, ok := args[1].(Number)
		if !ok {
			return nil, fmt.Errorf("__memchr: expected an integer byte, got %T", args[1])
		}
		fn, ok := args[2].(Number)
		if !ok {
			return nil, fmt.Errorf("__memchr: expected an integer start, got %T", args[2])
		}
		want := int64(bn)
		from := int(int64(fn))
		if from < 0 {
			from = 0
		}
		if want < 0 || want > 255 {
			return Number(-1), nil
		}
		b := []byte(string(s))
		for idx := from; idx < len(b); idx++ {
			if int64(b[idx]) == want {
				return Number(idx), nil
			}
		}
		return Number(-1), nil
	}}
	for _, name := range []string{"__sum_bytes_array", "__bsd_sum_bytes"} {
		i.Builtins[name] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			bsd := name == "__bsd_sum_bytes"
			argc := 1
			if bsd {
				argc = 2
			}
			if len(args) != argc {
				return nil, fmt.Errorf("%s: expected %d args, got %d", name, argc, len(args))
			}
			bytes, ok := args[0].(Array)
			if !ok {
				return nil, fmt.Errorf("%s: expected an array, got %T", name, args[0])
			}
			var sum uint32
			if bsd {
				seed, ok := args[1].(Number)
				if !ok {
					return nil, fmt.Errorf("%s: expected an integer checksum, got %T", name, args[1])
				}
				sum = uint32(seed) & 0xffff
			}
			for at, value := range bytes.Elems() {
				b, ok := value.(Number)
				if !ok || b < 0 || b > 255 {
					return nil, fmt.Errorf("%s: element %d is not u8", name, at)
				}
				if bsd {
					sum = ((sum >> 1) | (sum << 15)) & 0xffff
				}
				sum += uint32(b)
				if bsd {
					sum &= 0xffff
				}
			}
			return Number(int32(sum)), nil
		}}
	}
	i.Builtins["__count_byte_bytes"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("__count_byte_bytes: expected 2 args, got %d", len(args))
		}
		bytes, ok := args[0].(Array)
		if !ok {
			return nil, fmt.Errorf("__count_byte_bytes: expected an array, got %T", args[0])
		}
		needle, ok := args[1].(Number)
		if !ok {
			return nil, fmt.Errorf("__count_byte_bytes: expected an integer byte, got %T", args[1])
		}
		if needle < 0 || needle > 255 {
			return Number(0), nil
		}
		count := 0
		for at := 0; at < len(bytes.Elems()); at++ {
			b, ok := bytes.Elems()[at].(Number)
			if !ok {
				return nil, fmt.Errorf("__count_byte_bytes: element %d is %T, not u8", at, bytes.Elems()[at])
			}
			if b == needle {
				count++
			}
		}
		return Number(count), nil
	}}
	i.Builtins["__memchr_bytes"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("__memchr_bytes: expected 3 args, got %d", len(args))
		}
		bytes, ok := args[0].(Array)
		if !ok {
			return nil, fmt.Errorf("__memchr_bytes: expected an array, got %T", args[0])
		}
		needle, ok := args[1].(Number)
		if !ok {
			return nil, fmt.Errorf("__memchr_bytes: expected an integer byte, got %T", args[1])
		}
		start, ok := args[2].(Number)
		if !ok {
			return nil, fmt.Errorf("__memchr_bytes: expected an integer start, got %T", args[2])
		}
		if needle < 0 || needle > 255 {
			return Number(-1), nil
		}
		from := max(int(start), 0)
		for at := from; at < len(bytes.Elems()); at++ {
			b, ok := bytes.Elems()[at].(Number)
			if !ok {
				return nil, fmt.Errorf("__memchr_bytes: element %d is %T, not u8", at, bytes.Elems()[at])
			}
			if b == needle {
				return Number(at), nil
			}
		}
		return Number(-1), nil
	}}
	i.Builtins["__rmemchr_bytes"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("__rmemchr_bytes: expected 3 args, got %d", len(args))
		}
		bytes, ok := args[0].(Array)
		if !ok {
			return nil, fmt.Errorf("__rmemchr_bytes: expected an array, got %T", args[0])
		}
		needle, ok := args[1].(Number)
		if !ok {
			return nil, fmt.Errorf("__rmemchr_bytes: expected an integer byte, got %T", args[1])
		}
		start, ok := args[2].(Number)
		if !ok {
			return nil, fmt.Errorf("__rmemchr_bytes: expected an integer start, got %T", args[2])
		}
		if needle < 0 || needle > 255 {
			return Number(-1), nil
		}
		from := min(int(start), len(bytes.Elems())-1)
		for at := from; at >= 0; at-- {
			b, ok := bytes.Elems()[at].(Number)
			if !ok {
				return nil, fmt.Errorf("__rmemchr_bytes: element %d is %T, not u8", at, bytes.Elems()[at])
			}
			if b == needle {
				return Number(at), nil
			}
		}
		return Number(-1), nil
	}}
	i.Builtins["__mismatch_bytes"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 5 {
			return nil, fmt.Errorf("__mismatch_bytes: expected 5 args, got %d", len(args))
		}
		a, ok := args[0].(Array)
		if !ok {
			return nil, fmt.Errorf("__mismatch_bytes: expected an array, got %T", args[0])
		}
		b, ok := args[2].(Array)
		if !ok {
			return nil, fmt.Errorf("__mismatch_bytes: expected an array, got %T", args[2])
		}
		nums := [3]int{}
		for k, at := range [3]int{1, 3, 4} {
			v, ok := args[at].(Number)
			if !ok {
				return nil, fmt.Errorf("__mismatch_bytes: expected an integer at %d, got %T", at, args[at])
			}
			nums[k] = int(int64(v))
		}
		ao, bo := clampOffset(nums[0], len(a.Elems())), clampOffset(nums[1], len(b.Elems()))
		n := max(0, min(nums[2], len(a.Elems())-ao, len(b.Elems())-bo))
		for at := 0; at < n; at++ {
			av, aok := a.Elems()[ao+at].(Number)
			bv, bok := b.Elems()[bo+at].(Number)
			if !aok || !bok {
				return nil, fmt.Errorf("__mismatch_bytes: non-byte element at %d", at)
			}
			if av != bv {
				return Number(at), nil
			}
		}
		return Number(n), nil
	}}
	i.Builtins["__mismatch_bytes_str"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 5 {
			return nil, fmt.Errorf("__mismatch_bytes_str: expected 5 args, got %d", len(args))
		}
		a, ok := args[0].(Array)
		if !ok {
			return nil, fmt.Errorf("__mismatch_bytes_str: expected an array, got %T", args[0])
		}
		bs, ok := args[2].(String)
		if !ok {
			return nil, fmt.Errorf("__mismatch_bytes_str: expected a string, got %T", args[2])
		}
		nums := [3]int{}
		for k, at := range [3]int{1, 3, 4} {
			v, ok := args[at].(Number)
			if !ok {
				return nil, fmt.Errorf("__mismatch_bytes_str: expected an integer at %d, got %T", at, args[at])
			}
			nums[k] = int(int64(v))
		}
		bb := []byte(string(bs))
		ao, bo := clampOffset(nums[0], len(a.Elems())), clampOffset(nums[1], len(bb))
		n := max(0, min(nums[2], len(a.Elems())-ao, len(bb)-bo))
		for at := 0; at < n; at++ {
			av, aok := a.Elems()[ao+at].(Number)
			if !aok {
				return nil, fmt.Errorf("__mismatch_bytes_str: non-byte element at %d", at)
			}
			if av != Number(bb[bo+at]) {
				return Number(at), nil
			}
		}
		return Number(n), nil
	}}
	i.Builtins["__mismatch"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 5 {
			return nil, fmt.Errorf("__mismatch: expected 5 args, got %d", len(args))
		}
		as, ok := args[0].(String)
		if !ok {
			return nil, fmt.Errorf("__mismatch: expected a string, got %T", args[0])
		}
		bs, ok := args[2].(String)
		if !ok {
			return nil, fmt.Errorf("__mismatch: expected a string, got %T", args[2])
		}
		nums := [3]int{}
		for k, at := range [3]int{1, 3, 4} {
			v, ok := args[at].(Number)
			if !ok {
				return nil, fmt.Errorf("__mismatch: expected an integer at %d, got %T", at, args[at])
			}
			nums[k] = int(int64(v))
		}
		ab, bb := []byte(string(as)), []byte(string(bs))
		ao, bo, n := clampOffset(nums[0], len(ab)), clampOffset(nums[1], len(bb)), nums[2]
		if n > len(ab)-ao {
			n = len(ab) - ao
		}
		if n > len(bb)-bo {
			n = len(bb) - bo
		}
		if n < 0 {
			n = 0
		}
		for idx := 0; idx < n; idx++ {
			if ab[ao+idx] != bb[bo+idx] {
				return Number(idx), nil
			}
		}
		return Number(n), nil
	}}
	i.Builtins["__ascii_run"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("__ascii_run: expected 2 args, got %d", len(args))
		}
		s, ok := args[0].(String)
		if !ok {
			return nil, fmt.Errorf("__ascii_run: expected a string, got %T", args[0])
		}
		fn, ok := args[1].(Number)
		if !ok {
			return nil, fmt.Errorf("__ascii_run: expected an integer start, got %T", args[1])
		}
		from := int(int64(fn))
		if from < 0 {
			from = 0
		}
		b := []byte(string(s))
		for idx := from; idx < len(b); idx++ {
			if b[idx] >= 0x80 {
				return Number(idx), nil
			}
		}
		return Number(len(b)), nil
	}}
	i.Builtins["__rmemchr"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("__rmemchr: expected 3 args, got %d", len(args))
		}
		s, ok := args[0].(String)
		if !ok {
			return nil, fmt.Errorf("__rmemchr: expected a string, got %T", args[0])
		}
		bn, ok := args[1].(Number)
		if !ok {
			return nil, fmt.Errorf("__rmemchr: expected an integer byte, got %T", args[1])
		}
		fn, ok := args[2].(Number)
		if !ok {
			return nil, fmt.Errorf("__rmemchr: expected an integer start, got %T", args[2])
		}
		want := int(int64(bn))
		if want < 0 || want > 255 {
			return Number(-1), nil
		}
		b := []byte(string(s))
		from := int(int64(fn))
		if from > len(b)-1 {
			from = len(b) - 1
		}
		for idx := from; idx >= 0; idx-- {
			if int(b[idx]) == want {
				return Number(idx), nil
			}
		}
		return Number(-1), nil
	}}
	i.Builtins["__count_byte"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("__count_byte: expected 2 args, got %d", len(args))
		}
		s, ok := args[0].(String)
		if !ok {
			return nil, fmt.Errorf("__count_byte: expected a string, got %T", args[0])
		}
		bn, ok := args[1].(Number)
		if !ok {
			return nil, fmt.Errorf("__count_byte: expected an integer byte, got %T", args[1])
		}
		want := int(int64(bn))
		if want < 0 || want > 255 {
			return Number(0), nil
		}
		n := 0
		for _, c := range []byte(string(s)) {
			if int(c) == want {
				n++
			}
		}
		return Number(n), nil
	}}
	i.Builtins["__bsd_sum"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("__bsd_sum: expected 2 args, got %d", len(args))
		}
		s, ok := args[0].(String)
		if !ok {
			return nil, fmt.Errorf("__bsd_sum: expected a string, got %T", args[0])
		}
		n, ok := args[1].(Number)
		if !ok {
			return nil, fmt.Errorf("__bsd_sum: expected an integer sum, got %T", args[1])
		}
		sum := uint32(int64(n)) & 0xffff
		for _, c := range []byte(string(s)) {
			sum = (sum>>1 | sum<<15) & 0xffff
			sum = (sum + uint32(c)) & 0xffff
		}
		return Number(sum), nil
	}}
	i.Builtins["__str_hash"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("__str_hash: expected 2 args, got %d", len(args))
		}
		s, ok := args[0].(String)
		if !ok {
			return nil, fmt.Errorf("__str_hash: expected a string, got %T", args[0])
		}
		seed, ok := args[1].(Number)
		if !ok {
			return nil, fmt.Errorf("__str_hash: expected an integer seed, got %T", args[1])
		}
		return Number(int32(StrHash([]byte(string(s)), int32(int64(seed))))), nil
	}}
	i.Builtins["__count_runs"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("__count_runs: expected 3 args, got %d", len(args))
		}
		s, ok := args[0].(String)
		if !ok {
			return nil, fmt.Errorf("__count_runs: expected a string, got %T", args[0])
		}
		inside, ok := args[1].(Number)
		if !ok {
			return nil, fmt.Errorf("__count_runs: expected an integer flag, got %T", args[1])
		}
		set, ok := args[2].(Array)
		if !ok {
			return nil, fmt.Errorf("__count_runs: expected a u8[] set, got %T", args[2])
		}
		prev := int64(inside) != 0
		runs := 0
		for _, c := range []byte(string(s)) {
			member := false
			if int(c) < len(set.Elems()) {
				e, ok := set.Elems()[c].(Number)
				if !ok {
					return nil, fmt.Errorf("__count_runs: set element %d is %T, not a byte", c, set.Elems()[c])
				}
				member = int64(e) != 0
			}
			if member && !prev {
				runs++
			}
			prev = member
		}
		return Number(runs), nil
	}}
	i.Builtins["__count_runs_bytes"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("__count_runs_bytes: expected 3 args, got %d", len(args))
		}
		s, ok := args[0].(Array)
		if !ok {
			return nil, fmt.Errorf("__count_runs_bytes: expected a byte array, got %T", args[0])
		}
		inside, ok := args[1].(Number)
		if !ok {
			return nil, fmt.Errorf("__count_runs_bytes: expected an integer flag, got %T", args[1])
		}
		set, ok := args[2].(Array)
		if !ok {
			return nil, fmt.Errorf("__count_runs_bytes: expected a u8[] set, got %T", args[2])
		}
		prev := int64(inside) != 0
		runs := 0
		for idx, value := range s.Elems() {
			n, ok := value.(Number)
			if !ok {
				return nil, fmt.Errorf("__count_runs_bytes: element %d is %T, not u8", idx, value)
			}
			c := uint8(n)
			member := false
			if int(c) < len(set.Elems()) {
				e, ok := set.Elems()[c].(Number)
				if !ok {
					return nil, fmt.Errorf("__count_runs_bytes: set element %d is %T, not a byte", c, set.Elems()[c])
				}
				member = int64(e) != 0
			}
			if member && !prev {
				runs++
			}
			prev = member
		}
		return Number(runs), nil
	}}
	i.Builtins["__scan_set"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("__scan_set: expected 3 args, got %d", len(args))
		}
		s, ok := args[0].(String)
		if !ok {
			return nil, fmt.Errorf("__scan_set: expected a string, got %T", args[0])
		}
		return scanSet("__scan_set", []byte(string(s)), args[1], args[2])
	}}
	i.Builtins["__scan_set_bytes"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("__scan_set_bytes: expected 3 args, got %d", len(args))
		}
		arr, ok := args[0].(Array)
		if !ok {
			return nil, fmt.Errorf("__scan_set_bytes: expected an array, got %T", args[0])
		}
		b := make([]byte, len(arr.Elems()))
		for at, e := range arr.Elems() {
			n, ok := e.(Number)
			if !ok {
				return nil, fmt.Errorf("__scan_set_bytes: element %d is %T, not u8", at, e)
			}
			b[at] = byte(n)
		}
		return scanSet("__scan_set_bytes", b, args[1], args[2])
	}}
	i.Builtins["__sum_bytes"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("__sum_bytes: expected 1 arg, got %d", len(args))
		}
		s, ok := args[0].(String)
		if !ok {
			return nil, fmt.Errorf("__sum_bytes: expected a string, got %T", args[0])
		}
		var sum uint32
		for _, c := range []byte(string(s)) {
			sum += uint32(c)
		}
		return Number(int32(sum)), nil
	}}
	i.Builtins["__scale_f64"] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("__scale_f64: expected 2 args, got %d", len(args))
		}
		xs, ok := args[0].(Array)
		if !ok {
			return nil, fmt.Errorf("__scale_f64: expected an array, got %T", args[0])
		}
		k, ok := args[1].(Float)
		if !ok {
			return nil, fmt.Errorf("__scale_f64: expected an f64 factor, got %T", args[1])
		}
		out := newArray(len(xs.Elems()))
		for j, e := range xs.Elems() {
			f, ok := e.(Float)
			if !ok {
				return nil, fmt.Errorf("__scale_f64: element %d is %T, not f64", j, e)
			}
			out.Elems()[j] = Float{V: f.V * k.V, Width: 64}
		}
		return out, nil
	}}
	i.Builtins["__outer_mul_f64"] = &Builtin{Fn: builtinOuterMulF64}
	i.Builtins["__inner_mul_add_f64"] = &Builtin{Fn: builtinInnerMulAddF64}
	for _, name := range []string{"__crc32_cksum", "__crc32_cksum_array"} {
		i.Builtins[name] = &Builtin{Fn: func(_ *Interp, args []Value) (Value, error) {
			if len(args) != 2 {
				return nil, fmt.Errorf("%s: expected 2 args, got %d", name, len(args))
			}
			n, ok := args[0].(Number)
			if !ok {
				return nil, fmt.Errorf("%s: expected a number crc, got %T", name, args[0])
			}
			var data []byte
			if name == "__crc32_cksum_array" {
				a, ok := args[1].(Array)
				if !ok {
					return nil, fmt.Errorf("%s: expected an array, got %T", name, args[1])
				}
				data = make([]byte, len(a.Elems()))
				for at, value := range a.Elems() {
					b, ok := value.(Number)
					if !ok || b < 0 || b > 255 {
						return nil, fmt.Errorf("%s: element %d is not u8", name, at)
					}
					data[at] = byte(b)
				}
			} else {
				s, ok := args[1].(String)
				if !ok {
					return nil, fmt.Errorf("%s: expected a string, got %T", name, args[1])
				}
				data = []byte(string(s))
			}
			crc := uint32(int32(n))
			for _, c := range data {
				crc ^= uint32(c) << 24
				for k := 0; k < 8; k++ {
					if crc&0x80000000 != 0 {
						crc = (crc << 1) ^ 0x04C11DB7
					} else {
						crc <<= 1
					}
				}
			}
			return Number(int32(crc)), nil
		}}
	}
	i.Builtins["__arr_push_shared_count"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
	i.Builtins["__arr_push_shared_bytes"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
	mapHashSeed := int32(0)
	i.Builtins["__map_hash_seed"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) {
		if mapHashSeed == 0 {
			var b [4]byte
			if _, err := cryptorand.Read(b[:]); err != nil {
				return nil, fmt.Errorf("__map_hash_seed: %v", err)
			}
			v := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
			mapHashSeed = int32(v) | 1
		}
		return Number(int64(mapHashSeed)), nil
	}}
	i.Builtins["__heap_mark"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
	i.Builtins["__heap_release_to"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return nil, nil }}
	i.Builtins["__task_new"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(1), nil }}
	i.Builtins["__task_free"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return nil, nil }}
	i.Builtins["__task_cur"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
	i.Builtins["__task_enter"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return nil, nil }}
	i.Builtins["__task_leave"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
	i.Builtins["__task_park"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(-1), nil }}
	i.Builtins["__task_wait"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return newArray(0), nil }}
	i.Builtins["__task_timeout"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(-1), nil }}
	i.Builtins["__task_set_ready"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return nil, nil }}
	i.Builtins["__task_set_cancelled"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return nil, nil }}
	i.Builtins["__task_cancelled"] = &Builtin{Fn: func(_ *Interp, _ []Value) (Value, error) { return Number(0), nil }}
	i.Builtins["f32_bits"] = &Builtin{Fn: builtinF32Bits}
	i.Builtins["f32_from_bits"] = &Builtin{Fn: builtinF32FromBits}
	i.Builtins["f64_bits"] = &Builtin{Fn: builtinF64Bits}
	i.Builtins["f64_from_bits"] = &Builtin{Fn: builtinF64FromBits}
	i.Builtins["__sqrt_f64"] = mkUnaryF64Builtin("__sqrt_f64", math.Sqrt)
	i.Builtins["__floor_f64"] = mkUnaryF64Builtin("__floor_f64", math.Floor)
	i.Builtins["__ceil_f64"] = mkUnaryF64Builtin("__ceil_f64", math.Ceil)
	i.Builtins["__round_f64"] = mkUnaryF64Builtin("__round_f64", math.Round)
	i.Builtins["__trunc_f64"] = mkUnaryF64Builtin("__trunc_f64", math.Trunc)
	i.Builtins["__abs_f64"] = mkUnaryF64Builtin("__abs_f64", math.Abs)
	i.registerLaneBuiltins()
	i.Builtins["now_unix_ms"] = &Builtin{Fn: builtinNowUnixMS}
	i.Builtins["now_ns"] = &Builtin{Fn: builtinNowNS}
	i.Builtins["monotonic_ns"] = &Builtin{Fn: builtinMonotonicNS}
	i.Builtins["clock_resolution"] = &Builtin{Fn: builtinClockResolution}
	i.Builtins["clock_set"] = &Builtin{Fn: builtinClockSet}
	i.Builtins["sleep_ms"] = &Builtin{Fn: builtinSleepMS}
	i.Builtins["sleep_ns"] = &Builtin{Fn: builtinSleepNS}
	i.Builtins["proc_fork"] = &Builtin{Fn: builtinProcFork}
	i.Builtins["proc_waitpid"] = &Builtin{Fn: builtinProcWaitpid}
	i.Builtins["proc_waitpid_nohang"] = &Builtin{Fn: builtinProcWaitpidNohang}
	i.Builtins["proc_waitpid_status"] = &Builtin{Fn: builtinProcWaitpidStatus}
	i.Builtins["proc_exec"] = &Builtin{Fn: builtinProcExec}
	i.Builtins["proc_exec_as"] = &Builtin{Fn: builtinProcExecAs}
	i.Builtins["pipe"] = &Builtin{Fn: builtinPipe}
	i.Builtins["process_alive"] = &Builtin{Fn: builtinProcessAlive}
	i.Builtins["signal_send"] = &Builtin{Fn: builtinSignalSend}
	i.Builtins["set_process_group"] = &Builtin{Fn: builtinSetProcessGroup}
	i.Builtins["rlimit_nofile"] = &Builtin{Fn: builtinRlimitNofile}
	i.Builtins["disable_core_dumps"] = &Builtin{Fn: builtinDisableCoreDumps}
	i.Builtins["statfs"] = &Builtin{Fn: builtinStatfs}
	i.Builtins["mounts"] = &Builtin{Fn: builtinMounts}
	i.Builtins["sysctl"] = &Builtin{Fn: builtinSysctl}
	i.Builtins["temp_dir"] = &Builtin{Fn: builtinTempDir}
	i.Builtins["read_dir_bytes"] = &Builtin{Fn: builtinReadDirBytes}
	i.Builtins["read_dir"] = &Builtin{Fn: builtinReadDir}
	i.Builtins["read_dir_all"] = &Builtin{Fn: builtinReadDirAll}
	i.Builtins["read_dir_ino"] = &Builtin{Fn: builtinReadDirIno}
	i.Builtins["read_dir_ino_all"] = &Builtin{Fn: builtinReadDirInoAll}
	i.Builtins["stat"] = &Builtin{Fn: builtinStat}
	i.Builtins["lstat"] = &Builtin{Fn: builtinLstat}
	i.Builtins["access"] = &Builtin{Fn: builtinAccess}
	i.Builtins["geteuid"] = &Builtin{Fn: builtinGeteuid}
	i.Builtins["getegid"] = &Builtin{Fn: builtinGetegid}
	i.Builtins["getuid"] = &Builtin{Fn: builtinGetuid}
	i.Builtins["__account_entry"] = &Builtin{Fn: builtinAccountEntry}
	i.Builtins["getgid"] = &Builtin{Fn: builtinGetgid}
	i.Builtins["getgroups"] = &Builtin{Fn: builtinGetgroups}
	i.Builtins["hostname"] = &Builtin{Fn: builtinHostname}
	i.Builtins["uname_field"] = &Builtin{Fn: builtinUnameField}
	i.Builtins["getcwd"] = &Builtin{Fn: builtinGetcwd}
	i.Builtins["chdir"] = &Builtin{Fn: builtinChdir}
	i.Builtins["chroot"] = &Builtin{Fn: builtinChroot}
	i.Builtins["setuid"] = &Builtin{Fn: builtinSetuid}
	i.Builtins["setgid"] = &Builtin{Fn: builtinSetgid}
	i.Builtins["setgroups"] = &Builtin{Fn: builtinSetgroups}
	i.Builtins["cpu_count"] = &Builtin{Fn: builtinCPUCount}
	i.Builtins["signal_ignore"] = &Builtin{Fn: builtinSignalIgnore}
	i.Builtins["signal_default"] = &Builtin{Fn: builtinSignalDefault}
	i.Builtins["signal_catch"] = &Builtin{Fn: builtinSignalCatch}
	i.Builtins["signal_taken"] = &Builtin{Fn: builtinSignalTaken}
	i.Builtins["signal_catch_interrupting"] = &Builtin{Fn: builtinSignalCatchInterrupting}
	i.Builtins["signal_raise"] = &Builtin{Fn: builtinSignalRaise}
	i.Builtins["signal_mask"] = &Builtin{Fn: builtinSignalMask}
	i.Builtins["signal_disposition"] = &Builtin{Fn: builtinSignalDisposition}
	i.Builtins["remove_file"] = &Builtin{Fn: builtinRemoveFile}
	i.Builtins["create_dir"] = &Builtin{Fn: builtinCreateDir}
	i.Builtins["remove_dir"] = &Builtin{Fn: builtinRemoveDir}
	i.Builtins["create_link"] = &Builtin{Fn: builtinCreateLink}
	i.Builtins["clone_file"] = &Builtin{Fn: builtinCloneFile}
	i.Builtins["create_symlink"] = &Builtin{Fn: builtinCreateSymlink}
	i.Builtins["read_link"] = &Builtin{Fn: builtinReadLink}
	i.Builtins["read_link_bytes"] = &Builtin{Fn: builtinReadLinkBytes}
	i.Builtins["getxattr"] = &Builtin{Fn: builtinGetxattr}
	i.Builtins["lgetxattr"] = &Builtin{Fn: builtinLgetxattr}
	i.Builtins["setxattr"] = &Builtin{Fn: builtinSetxattr}
	i.Builtins["lsetxattr"] = &Builtin{Fn: builtinLsetxattr}
	i.Builtins["getxattr_bytes"] = &Builtin{Fn: builtinGetxattrBytes}
	i.Builtins["lgetxattr_bytes"] = &Builtin{Fn: builtinLgetxattrBytes}
	i.Builtins["setxattr_bytes"] = &Builtin{Fn: builtinSetxattrBytes}
	i.Builtins["lsetxattr_bytes"] = &Builtin{Fn: builtinLsetxattrBytes}
	i.Builtins["umask"] = &Builtin{Fn: builtinUmask}
	i.Builtins["priority"] = &Builtin{Fn: builtinPriority}
	i.Builtins["set_priority"] = &Builtin{Fn: builtinSetPriority}
	i.Builtins["rename"] = &Builtin{Fn: builtinRename}
	i.Builtins["rename_noreplace"] = &Builtin{Fn: builtinRenameNoreplace}
	i.Builtins["rename_exchange"] = &Builtin{Fn: builtinRenameExchange}
	i.Builtins["chmod"] = &Builtin{Fn: builtinChmod}
	i.Builtins["chmod_at"] = &Builtin{Fn: builtinChmodAt}
	i.Builtins["truncate"] = &Builtin{Fn: builtinTruncate}
	i.Builtins["mknod"] = &Builtin{Fn: builtinMknod}
	i.Builtins["sync"] = &Builtin{Fn: builtinSync}
	i.Builtins["chown_at"] = &Builtin{Fn: builtinChownAt}
	i.Builtins["set_file_times"] = &Builtin{Fn: builtinSetFileTimes}
	i.Builtins["create_dir_all"] = &Builtin{Fn: builtinCreateDirAll}
	i.Builtins["remove_dir_all"] = &Builtin{Fn: builtinRemoveDirAll}
	i.Builtins["subprocess"] = &Builtin{Fn: builtinSubprocess}
	i.Builtins["int_to_string"] = &Builtin{Fn: builtinIntToString}
	i.Builtins["int__int_to_string"] = &Builtin{Fn: builtinIntToString}
	i.Builtins["__int_to_string_u64"] = &Builtin{Fn: builtinIntToStringU64}
	i.Builtins["int____int_to_string_u64"] = &Builtin{Fn: builtinIntToStringU64}
	i.Builtins["tcp_listen"] = &Builtin{Fn: builtinTcpListen}
	i.Builtins["tcp_accept"] = &Builtin{Fn: builtinTcpAccept}
	i.Builtins["tcp_local_port"] = &Builtin{Fn: builtinTcpLocalPort}
	i.Builtins["tcp_listen_with"] = &Builtin{Fn: builtinTcpListenWith}
	i.Builtins["tcp_socket_ctl"] = &Builtin{Fn: builtinTcpSocketCtl}
	i.Builtins["tcp_send_bytes"] = &Builtin{Fn: builtinTcpSendBytes}
	i.Builtins["tcp_sendfile"] = &Builtin{Fn: builtinTcpSendfile}
	i.Builtins["tcp_send_buf"] = &Builtin{Fn: builtinTcpSendBuf}
	i.Builtins["tcp_connect_with"] = &Builtin{Fn: builtinTcpConnectWith}
	i.Builtins["unix_listen"] = &Builtin{Fn: builtinUnixListen}
	i.Builtins["unix_connect"] = &Builtin{Fn: builtinUnixConnect}
	i.Builtins["reactor_new"] = &Builtin{Fn: builtinReactorNew}
	i.Builtins["reactor_ctl"] = &Builtin{Fn: builtinReactorCtl}
	i.Builtins["reactor_wait"] = &Builtin{Fn: builtinReactorWait}
	i.Builtins["ring_new"] = &Builtin{Fn: builtinRingUnsupported}
	i.Builtins["ring_ctl"] = &Builtin{Fn: builtinRingUnsupported}
	i.Builtins["ring_wait"] = &Builtin{Fn: builtinRingUnsupported}
	i.Builtins["ring_recv"] = &Builtin{Fn: builtinRingUnsupported}
	i.Builtins["ring_send_zc"] = &Builtin{Fn: builtinRingUnsupported}
	i.Builtins["tcp_recv_into"] = &Builtin{Fn: builtinTcpRecvInto}
	i.Builtins["tcp_ktls"] = &Builtin{Fn: builtinKtlsUnsupported}
	i.Builtins["udp_bind"] = &Builtin{Fn: builtinUdpBind}
	i.Builtins["udp_bind_with"] = &Builtin{Fn: builtinUdpBindWith}
	i.Builtins["udp_connect"] = &Builtin{Fn: builtinUdpConnect}
	i.Builtins["udp_sendto"] = &Builtin{Fn: builtinUdpSendto}
	i.Builtins["udp_sendto_bytes"] = &Builtin{Fn: builtinUdpSendtoBytes}
	i.Builtins["timer_fd"] = &Builtin{Fn: builtinTimerFd}
	i.Builtins["udp_recvfrom"] = &Builtin{Fn: builtinUdpRecvfrom}
	i.Builtins["tcp_close"] = &Builtin{Fn: builtinTcpClose}
	i.Builtins["tcp_pollable"] = &Builtin{Fn: builtinTcpPollable}
	i.Builtins["wasm_pollable_drop"] = &Builtin{Fn: builtinWasmPollableDrop}
	i.Builtins["wasm_timer_pollable"] = &Builtin{Fn: builtinWasmTimerPollable}
	i.Builtins["wasm_block"] = &Builtin{Fn: builtinWasmBlock}
	i.Builtins["wasm_poll"] = &Builtin{Fn: builtinWasmPoll}
	i.inheritListener()
	return i
}

func builtinTcpListen(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("tcp_listen: expected 1 arg, got %d", len(args))
	}
	port, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_listen: expected number arg, got %T", args[0])
	}
	ln, err := tcpNetListen("tcp4", fmt.Sprintf("0.0.0.0:%d", int(port)))
	if err != nil {
		return negErrno(err), nil
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

func builtinTcpSendfile(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("tcp_sendfile: expected 3 args, got %d", len(args))
	}
	id, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_sendfile: expected number fd arg, got %T", args[0])
	}
	file, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_sendfile: expected number file arg, got %T", args[1])
	}
	max, ok := args[2].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_sendfile: expected number max arg, got %T", args[2])
	}
	conn, ok := i.tcpConns[int64(id)]
	if !ok {
		return Number(-9), nil
	}
	f, ok := i.openFiles[int64(file)]
	if !ok {
		return Number(-9), nil
	}
	if max <= 0 {
		return Number(0), nil
	}
	buf := make([]byte, int(max))
	n, err := f.Read(buf)
	if n == 0 {
		if err == nil || err == io.EOF {
			return Number(0), nil
		}
		return Number(errnoOf(err)), nil
	}
	sent, werr := writeSocket(conn, buf[:n], i.tcpNonblocking[int64(id)])
	if sent < n {
		if _, serr := f.Seek(int64(sent-n), io.SeekCurrent); serr != nil {
			return Number(errnoOf(serr)), nil
		}
	}
	if werr != nil && sent == 0 {
		return Number(errnoOf(werr)), nil
	}
	return Number(sent), nil
}

func builtinTcpSendBytes(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("tcp_send_bytes: expected 2 args, got %d", len(args))
	}
	id, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_send_bytes: expected number fd arg, got %T", args[0])
	}
	data, ok := args[1].(Array)
	if !ok {
		return nil, fmt.Errorf("tcp_send_bytes: expected u8[] data arg, got %T", args[1])
	}
	bytes := make([]byte, len(data.Elems()))
	for n, value := range data.Elems() {
		bytes[n] = byte(value.(Number))
	}
	return tcpSendData(i, id, bytes)
}

func builtinTcpSendBuf(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("tcp_send_buf: expected 3 args, got %d", len(args))
	}
	id, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_send_buf: expected number fd arg, got %T", args[0])
	}
	_, b, err := bufHandle(i, "tcp_send_buf", args[1])
	if err != nil {
		return nil, err
	}
	from, ok := args[2].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_send_buf: expected number from arg, got %T", args[2])
	}
	if from < 0 || int(from) >= len(b) {
		return Number(0), nil
	}
	return tcpSendData(i, id, b[int(from):])
}

func tcpSendData(i *Interp, id Number, data []byte) (Value, error) {
	conn, ok := i.tcpConns[int64(id)]
	if !ok {
		return Number(-1), nil
	}
	n, err := writeSocket(conn, data, i.tcpNonblocking[int64(id)])
	if err != nil {
		if n > 0 {
			return Number(n), nil
		}
		return Number(errnoOf(err)), nil
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
	if r, ok := i.udpClose(int64(id)); ok {
		return r, nil
	}
	if r, ok := i.timerClose(int64(id)); ok {
		return r, nil
	}
	return Number(-1), nil
}

var soReusePort = func() int {
	if runtime.GOOS == "darwin" {
		return 0x200
	}
	return 15
}()

func negErrno(err error) Value {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return Number(-int64(errno))
	}
	return Number(-1)
}

func (i *Interp) inheritListener() {
	n, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil || n < 1 {
		return
	}
	f := os.NewFile(3, "LISTEN_FDS")
	ln, err := net.FileListener(f)
	f.Close()
	if err != nil {
		return
	}
	i.tcpListeners = map[int64]tcpListenerHandle{3: ln}
	i.tcpNextHandle = 3
}

func builtinTcpListenWith(i *Interp, args []Value) (Value, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("tcp_listen_with: expected 4 args, got %d", len(args))
	}
	ip, err := ipArg("tcp_listen_with", args[0])
	if err != nil {
		return nil, err
	}
	if ip == nil {
		return Number(-int64(syscall.EAFNOSUPPORT)), nil
	}
	port, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_listen_with: expected number port arg, got %T", args[1])
	}
	reuse, ok := args[3].(Bool)
	if !ok {
		return nil, fmt.Errorf("tcp_listen_with: expected boolean reuse_port arg, got %T", args[3])
	}
	lc := net.ListenConfig{}
	lc.SetMultipathTCP(false)
	if reuse {
		lc.Control = func(_, _ string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = syscall.SetsockoptInt(int(fd), solSocket, soReusePort, 1)
			}); err != nil {
				return err
			}
			return serr
		}
	}
	network := "tcp"
	if len(ip) == 4 {
		network = "tcp4"
	}
	ln, err := lc.Listen(context.Background(), network, net.JoinHostPort(ip.String(), fmt.Sprint(int(port))))
	if err != nil {
		return negErrno(err), nil
	}
	if i.tcpListeners == nil {
		i.tcpListeners = map[int64]tcpListenerHandle{}
	}
	i.tcpNextHandle++
	id := i.tcpNextHandle
	i.tcpListeners[id] = ln
	return Number(id), nil
}

func builtinTcpConnectWith(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("tcp_connect_with: expected 3 args, got %d", len(args))
	}
	ip, err := ipArg("tcp_connect_with", args[0])
	if err != nil {
		return nil, err
	}
	if ip == nil {
		return Number(-int64(syscall.EAFNOSUPPORT)), nil
	}
	port, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_connect_with: expected number port arg, got %T", args[1])
	}
	nonblocking, ok := args[2].(Bool)
	if !ok {
		return nil, fmt.Errorf("tcp_connect_with: expected boolean nonblocking arg, got %T", args[2])
	}
	got, err := i.dialTcp(ip, int(port))
	if err != nil {
		return nil, err
	}
	if id, ok := got.(Number); ok && id >= 0 && bool(nonblocking) {
		if i.tcpNonblocking == nil {
			i.tcpNonblocking = map[int64]bool{}
		}
		i.tcpNonblocking[int64(id)] = true
	}
	return got, nil
}

func ipArg(name string, v Value) (net.IP, error) {
	arr, ok := v.(Array)
	if !ok {
		return nil, fmt.Errorf("%s: expected u8[] addr arg, got %T", name, v)
	}
	if len(arr.Elems()) != 4 && len(arr.Elems()) != 16 {
		return nil, nil
	}
	ip := make(net.IP, len(arr.Elems()))
	for k, e := range arr.Elems() {
		n, ok := e.(Number)
		if !ok {
			return nil, fmt.Errorf("%s: expected u8 in addr, got %T", name, e)
		}
		ip[k] = byte(n)
	}
	return ip, nil
}

func (i *Interp) dialTcp(ip net.IP, port int) (Value, error) {
	conn, err := net.Dial("tcp", net.JoinHostPort(ip.String(), fmt.Sprint(port)))
	if err != nil {
		return negErrno(err), nil
	}
	if i.tcpConns == nil {
		i.tcpConns = map[int64]tcpConnHandle{}
	}
	i.tcpNextHandle++
	id := i.tcpNextHandle
	i.tcpConns[id] = conn
	return Number(id), nil
}

func builtinTcpSocketCtl(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("tcp_socket_ctl: expected 3 args, got %d", len(args))
	}
	id, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_socket_ctl: expected number fd arg, got %T", args[0])
	}
	op, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_socket_ctl: expected number op arg, got %T", args[1])
	}
	arg, ok := args[2].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_socket_ctl: expected number arg, got %T", args[2])
	}
	if op < 1 || op > 10 {
		return Number(-22), nil
	}
	if op == 9 || op == 10 {
		if r, ok := i.udpSocketCtl(int64(id), int64(op), int64(arg)); ok {
			return r, nil
		}
		fd, ok := i.rawFd(int64(id))
		if !ok {
			return Number(-1), nil
		}
		return nameGroup(fd, int64(op), int64(arg)), nil
	}
	if op == 8 {
		fd, ok := i.rawFd(int64(id))
		if !ok {
			return Number(-1), nil
		}
		if err := hostSteerByCPU(fd); err != nil {
			return Number(errnoOf(err)), nil
		}
		return Number(0), nil
	}
	if op == 7 {
		if _, isDatagram := i.udpSocks[int64(id)]; isDatagram {
			return Number(0), nil
		}
		conn, ok := i.tcpConns[int64(id)]
		if !ok {
			return Number(0), nil
		}
		ta, ok := conn.RemoteAddr().(*net.TCPAddr)
		if !ok {
			return Number(0), nil
		}
		return Number(peerKey(ta.IP)), nil
	}
	if r, ok := i.udpSocketCtl(int64(id), int64(op), int64(arg)); ok {
		return r, nil
	}
	conn, ok := i.tcpConns[int64(id)].(*net.TCPConn)
	if !ok {
		return Number(-1), nil
	}
	var err error
	switch op {
	case 5:
	case 6:
		fd, ok := i.rawFd(int64(id))
		if !ok {
			return Number(-1), nil
		}
		queued, err := hostSendQueued(fd)
		if err != nil {
			return Number(errnoOf(err)), nil
		}
		return Number(queued), nil
	case 1:
		err = conn.SetNoDelay(arg != 0)
	case 2:
		err = conn.SetKeepAlive(arg != 0)
	case 3:
		if i.tcpNonblocking == nil {
			i.tcpNonblocking = map[int64]bool{}
		}
		i.tcpNonblocking[int64(id)] = arg != 0
	case 4:
		switch arg {
		case 0:
			err = conn.CloseRead()
		case 1:
			err = conn.CloseWrite()
		default:
			if err = conn.CloseRead(); err == nil {
				err = conn.CloseWrite()
			}
		}
	}
	if err != nil {
		return Number(-1), nil
	}
	return Number(0), nil
}

func nameGroup(fd int, op, which int64) Value {
	if which < 0 || which > 9 {
		return Number(-int64(syscall.EINVAL))
	}
	var sa syscall.Sockaddr
	var err error
	if op == 9 {
		sa, err = getsockname(fd)
	} else {
		sa, err = getpeername(fd)
	}
	if err != nil {
		return negErrno(err)
	}
	switch a := sa.(type) {
	case *syscall.SockaddrInet4:
		switch {
		case which == 9:
			return Number(a.Port)
		case which == 8:
			return Number(4)
		case which < 2:
			return Number(int64(a.Addr[2*which])<<8 | int64(a.Addr[2*which+1]))
		}
		return Number(0)
	case *syscall.SockaddrInet6:
		switch {
		case which == 9:
			return Number(a.Port)
		case which == 8:
			return Number(6)
		}
		return Number(int64(a.Addr[2*which])<<8 | int64(a.Addr[2*which+1]))
	}
	return Number(-int64(syscall.EAFNOSUPPORT))
}

func builtinWasmPollableDrop(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wasm_pollable_drop: expected 1 arg, got %d", len(args))
	}
	if _, ok := args[0].(Number); !ok {
		return nil, fmt.Errorf("wasm_pollable_drop: expected number arg, got %T", args[0])
	}
	return Number(0), nil
}

func builtinWasmBlock(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wasm_block: expected 1 arg, got %d", len(args))
	}
	if _, ok := args[0].(Number); !ok {
		return nil, fmt.Errorf("wasm_block: expected number arg, got %T", args[0])
	}
	return Number(0), nil
}

func builtinWasmPoll(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wasm_poll: expected 1 arg, got %d", len(args))
	}
	if _, ok := args[0].(Array); !ok {
		return nil, fmt.Errorf("wasm_poll: expected array arg, got %T", args[0])
	}
	return Number(-1), nil
}

func builtinWasmTimerPollable(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("wasm_timer_pollable: expected 1 arg, got %d", len(args))
	}
	return Number(-1), nil
}

func peerKey(ip net.IP) int32 {
	word := func(b []byte) int32 {
		return int32(uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24)
	}
	if v4 := ip.To4(); v4 != nil {
		return word(v4)
	}
	v6 := ip.To16()
	if v6 == nil {
		return 0
	}
	if k := word(v6[0:4]) ^ word(v6[4:8]); k != 0 {
		return k
	}
	return 1
}

func builtinTcpLocalPort(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("tcp_local_port: expected 1 arg, got %d", len(args))
	}
	id, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("tcp_local_port: expected number arg, got %T", args[0])
	}
	if r, ok := i.udpLocalPort(int64(id)); ok {
		return r, nil
	}
	var addr net.Addr
	if ln, ok := i.tcpListeners[int64(id)]; ok {
		addr = ln.Addr()
	} else if conn, ok := i.tcpConns[int64(id)]; ok {
		addr = conn.LocalAddr()
	} else {
		return Number(-1), nil
	}
	ta, ok := addr.(*net.TCPAddr)
	if !ok {
		return Number(-1), nil
	}
	return Number(ta.Port), nil
}

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
	out := newArray(len(buf))
	for i, b := range buf {
		out.Elems()[i] = Number(b)
	}
	return out, nil
}

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
	out := strconv.FormatUint(uint64(int64(mag)), 10)
	if int64(neg) != 0 {
		out = "-" + out
	}
	return String(out), nil
}

type unclaimed struct{}

func (unclaimed) String() string { return "<unclaimed>" }

var unclaimedSlot Value = unclaimed{}

func growArray(arr Array) Array {
	want := len(arr.Elems()) + 1
	capacity := 2 * want
	if capacity < 8 {
		capacity = 8
	}
	e := make([]Value, capacity)
	n := copy(e, arr.Elems())
	for i := n; i < capacity; i++ {
		e[i] = unclaimedSlot
	}
	return boxOf(e[:want])
}

func builtinArrayPush(ip *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("__method_Array_push: expected 2 args (arr, v), got %d", len(args))
	}
	arr, ok := args[0].(Array)
	if !ok {
		return nil, fmt.Errorf("__method_Array_push: receiver must be array, got %T", args[0])
	}
	storeArray(args[1])
	e := arr.Elems()
	n := len(e)
	if arr.n == wholeBox && n < cap(e) && e[:n+1][n] == unclaimedSlot {
		e = e[:n+1]
		e[n] = args[1]
		arr.b.st.boxes++
		return Array{b: &arrBox{e: e, st: arr.b.st, held: n + 1}, n: wholeBox}, nil
	}
	out := growArray(arr)
	out.Elems()[n] = args[1]
	ip.arrayGrowCopies += n
	return out, nil
}

func builtinArrSetLen(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("__arr_set_len: expected 2 args (arr, n), got %d", len(args))
	}
	a, ok := args[0].(Array)
	if !ok {
		return nil, fmt.Errorf("__arr_set_len: expected an array, got %T", args[0])
	}
	if a.n != wholeBox {
		return nil, fmt.Errorf("__arr_set_len: expected an array, not a slice of one")
	}
	n, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("__arr_set_len: expected number length, got %T", args[1])
	}
	if n < 0 {
		return nil, fmt.Errorf("__arr_set_len: length %d below zero", int64(n))
	}
	b := a.b
	if int(n) <= b.held {
		b.e = b.e[:n]
		return Void{}, nil
	}
	b.privatise(int(n))
	b.e = b.e[:n]
	for k := b.held; k < int(n); k++ {
		b.e[k] = Number(0)
	}
	b.held = int(n)
	return Void{}, nil
}

func builtinArraySet(ip *Interp, args []Value) (Value, error) {
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
	if idx < 0 || int(idx) >= len(arr.Elems()) {
		return nil, indexAbort(CauseArrOOB, strconv.Itoa(int(idx)), len(arr.Elems()))
	}
	storeArray(args[2])
	inPlace, err := ip.arrayWriteInPlace(arr)
	if err != nil {
		return nil, err
	}
	if inPlace {
		adjustRC(args[2], int(arr.b.st.paths))
		adjustRC(arr.Elems()[int(idx)], -int(arr.b.st.paths))
		arr.Elems()[int(idx)] = args[2]
		return arr, nil
	}
	out := newArray(len(arr.Elems()))
	copy(out.Elems(), arr.Elems())
	out.Elems()[int(idx)] = args[2]
	ip.arraySetCopies += len(arr.Elems())
	return out, nil
}

func (i *Interp) arrayWriteInPlace(arr Array) (bool, error) {
	if i.arrayCOW == arrayCOWCopy || !arr.exclusive() {
		return false, nil
	}
	if i.arrayCOW == arrayCOWVerify {
		if err := i.verifyExclusive(arr); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (i *Interp) verifyExclusive(arr Array) error {
	allowed := 0
	for _, m := range i.moving {
		if ma, ok := m.(Array); ok && ma.b.st == arr.b.st {
			allowed++
		}
	}
	holders := 0
	seen := make(map[*binding]bool)
	for _, e := range i.envs {
		for _, v := range e.vars {
			if slot, ok := v.(*binding); ok {
				if seen[slot] {
					continue
				}
				seen[slot] = true
				v = slot.value
			}
			holders += coversBuffer(v, arr.b.st, 0)
		}
	}
	if holders <= allowed {
		return nil
	}
	return fmt.Errorf("FERN_INTERP_ARRAY_COW=verify: in-place write to a %d-element array covered by %d live bindings, %d of them moving",
		len(arr.Elems()), holders, allowed)
}

func coversBuffer(v Value, st *arrStore, depth int) int {
	if depth > 12 {
		return 0
	}
	n := 0
	switch x := v.(type) {
	case Array:
		if x.b.st == st {
			n++
		}
		for _, e := range x.Elems() {
			n += coversBuffer(e, st, depth+1)
		}
	case *Struct:
		for _, f := range x.Fields {
			n += coversBuffer(f, st, depth+1)
		}
	case *Enum:
		for _, p := range x.Payloads {
			n += coversBuffer(p, st, depth+1)
		}
	case *Map:
		for _, mv := range x.vals {
			n += coversBuffer(mv, st, depth+1)
		}
	case *Cell:
		n += coversBuffer(x.V, st, depth+1)
	}
	return n
}

func builtinMapNew(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("map_new: expected 1 arg (cap), got %d", len(args))
	}
	return &Map{}, nil
}

func builtinCellNew(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("cell_new: expected 1 arg, got %d", len(args))
	}
	storeArray(args[0])
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
	storeArray(args[1])
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

func builtinMapHas(ip *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_contains", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("__method_Map_contains: expected 2 args (m, k), got %d", len(args))
	}
	idx, err := ip.findKey(m, args[1])
	if err != nil {
		return nil, err
	}
	return Bool(idx >= 0), nil
}

func builtinMapGet(ip *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_get", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("__method_Map_get: expected 2 args (m, k), got %d", len(args))
	}
	idx, err := ip.findKey(m, args[1])
	if err != nil {
		return nil, err
	}
	if idx >= 0 {
		return optionSome(m.vals[idx]), nil
	}
	return optionNone(), nil
}

func builtinMapSet(ip *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_set", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 3 {
		return nil, fmt.Errorf("__method_Map_set: expected 3 args (m, k, v), got %d", len(args))
	}
	t := ip.cowTarget(m)
	storeArray(args[1])
	storeArray(args[2])
	idx, err := ip.findKey(t, args[1])
	if err != nil {
		return nil, err
	}
	if idx >= 0 {
		adjustRC(args[2], t.rc)
		adjustRC(t.vals[idx], -t.rc)
		t.vals[idx] = args[2]
	} else {
		adjustRC(args[1], t.rc)
		adjustRC(args[2], t.rc)
		t.keys = append(t.keys, args[1])
		t.vals = append(t.vals, args[2])
	}
	return t, nil
}

func (i *Interp) cowTarget(m *Map) *Map {
	if m.rc <= i.movingHolds(m) {
		return m
	}
	return m.clone()
}

func (i *Interp) movingHolds(m *Map) int {
	n := 0
	for _, v := range i.moving {
		if v == Value(m) {
			n++
		}
	}
	return n
}

func builtinMapDelete(ip *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_remove", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("__method_Map_remove: expected 2 args (m, k), got %d", len(args))
	}
	idx, err := ip.findKey(m, args[1])
	if err != nil {
		return nil, err
	}
	if idx < 0 {
		return m, nil
	}
	t := ip.cowTarget(m)
	last := len(t.keys) - 1
	t.keys[idx] = t.keys[last]
	t.vals[idx] = t.vals[last]
	t.keys = t.keys[:last]
	t.vals = t.vals[:last]
	return t, nil
}

func builtinMapClear(ip *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_clear", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_Map_clear: expected 1 arg (receiver), got %d", len(args))
	}
	if m.rc > ip.movingHolds(m) {
		return &Map{}, nil
	}
	m.keys = m.keys[:0]
	m.vals = m.vals[:0]
	return m, nil
}

func builtinMapGetOr(ip *Interp, args []Value) (Value, error) {
	m, err := mapReceiver("__method_Map_get_or", args)
	if err != nil {
		return nil, err
	}
	if len(args) != 3 {
		return nil, fmt.Errorf("__method_Map_get_or: expected 3 args (m, k, default), got %d", len(args))
	}
	idx, err := ip.findKey(m, args[1])
	if err != nil {
		return nil, err
	}
	if idx >= 0 {
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
	out := newArray(len(m.keys))
	copy(out.Elems(), m.keys)
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
	out := newArray(len(m.vals))
	copy(out.Elems(), m.vals)
	return out, nil
}

func builtinAllocFilled(name string, zero Value) func(*Interp, []Value) (Value, error) {
	return func(_ *Interp, args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("%s: expected 1 arg (n), got %d", name, len(args))
		}
		n, ok := args[0].(Number)
		if !ok {
			return nil, fmt.Errorf("%s: arg must be number, got %T", name, args[0])
		}
		if n < 0 {
			return nil, &Abort{CauseAllocSize, fmt.Sprintf("%s length %d", name, int64(n))}
		}
		out := newArray(int(n))
		for i := range out.Elems() {
			out.Elems()[i] = zero
		}
		return out, nil
	}
}

func builtinStringFromBytes(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("string_from_bytes_unchecked: expected 1 arg (bs), got %d", len(args))
	}
	arr, ok := args[0].(Array)
	if !ok {
		return nil, fmt.Errorf("string_from_bytes_unchecked: arg must be array, got %T", args[0])
	}
	buf := make([]byte, len(arr.Elems()))
	for i, v := range arr.Elems() {
		n, ok := v.(Number)
		if !ok {
			return nil, fmt.Errorf("string_from_bytes_unchecked: element %d not a number (%T)", i, v)
		}
		buf[i] = byte(int64(n) & 0xff)
	}
	return String(buf), nil
}

func builtinStringFromBytesRange(in *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("string_from_bytes_range_unchecked: expected 3 args (bs, from, end), got %d", len(args))
	}
	arr, ok := args[0].(Array)
	if !ok {
		return nil, fmt.Errorf("string_from_bytes_range_unchecked: arg must be array, got %T", args[0])
	}
	from, fok := args[1].(Number)
	end, eok := args[2].(Number)
	if !fok || !eok {
		return nil, fmt.Errorf("string_from_bytes_range_unchecked: bounds must be numbers")
	}
	if from < 0 || int(end) > len(arr.Elems()) || from > end {
		return nil, fmt.Errorf("string_from_bytes_range_unchecked: range [%d:%d] out of bounds for length %d", int(from), int(end), len(arr.Elems()))
	}
	return builtinStringFromBytes(in, []Value{arr.view(int(from), int(end))})
}

func builtinSliceUnchecked(_ *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("slice_unchecked: expected 3 args (s, a, b), got %d", len(args))
	}
	s, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("slice_unchecked: arg must be string, got %T", args[0])
	}
	a, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("slice_unchecked: low bound must be number, got %T", args[1])
	}
	b, ok := args[2].(Number)
	if !ok {
		return nil, fmt.Errorf("slice_unchecked: high bound must be number, got %T", args[2])
	}
	low, high, slen := int64(a), int64(b), int64(len(s))
	if low < 0 || high > slen || low > high {
		return nil, sliceAbort(CauseStrOOB, "slice_unchecked", low, high, slen)
	}
	return String(string(s)[low:high]), nil
}

func builtinStringBytes(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("__method_string_bytes: expected 1 arg (s), got %d", len(args))
	}
	s, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("__method_string_bytes: receiver must be string, got %T", args[0])
	}
	raw := []byte(s)
	out := newArray(len(raw))
	for i, b := range raw {
		out.Elems()[i] = Number(int64(b))
	}
	return out, nil
}

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

func optionSome(v Value) *Enum {
	return &Enum{EnumName: "Option", VariantName: "Some", Index: 0, Payloads: []Value{v}}
}

func optionNone() *Enum {
	return &Enum{EnumName: "Option", VariantName: "None", Index: 1}
}

func interpCharBoundary(s String, i int64) bool {
	if i == 0 || i == int64(len(s)) {
		return true
	}
	b := s[i]
	return b < 0x80 || b >= 0xC0
}

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
	if !utf8.Valid(data) {
		return resultErr(&Enum{EnumName: "IoError", VariantName: "InvalidUtf8", Index: 3,
			Payloads: []Value{path}}), nil
	}
	return resultOk(String(string(data))), nil
}

func builtinReadFileBytes(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("read_file_bytes: expected 1 arg, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("read_file_bytes: expected string arg, got %T", args[0])
	}
	data, err := os.ReadFile(string(path))
	if err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	out := newArray(len(data))
	for i, b := range data {
		out.Elems()[i] = Number(b)
	}
	return resultOk(out), nil
}

func builtinWriteFileExec(i *Interp, args []Value) (Value, error) {
	v, err := writeFileMode("write_file_exec", args, 0o755)
	if err != nil {
		return v, err
	}
	if e, ok := v.(*Enum); !ok || e.VariantName != "Ok" {
		return v, nil
	}
	path := args[0].(String)
	if err := os.Chmod(string(path), 0o755); err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	return v, nil
}

func builtinWriteFile(_ *Interp, args []Value) (Value, error) {
	return writeFileMode("write_file", args, 0o644)
}

func builtinWriteFileBytes(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("write_file_bytes: expected 2 args, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("write_file_bytes: expected string path, got %T", args[0])
	}
	content, ok := args[1].(Array)
	if !ok {
		return nil, fmt.Errorf("write_file_bytes: expected byte array, got %T", args[1])
	}
	data := make([]byte, len(content.Elems()))
	for n, value := range content.Elems() {
		data[n] = byte(value.(Number))
	}
	if err := os.WriteFile(string(path), data, 0o644); err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	return resultOk(unitValue()), nil
}

func writeFileMode(name string, args []Value, mode os.FileMode) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("%s: expected 2 args, got %d", name, len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("%s: expected string path, got %T", name, args[0])
	}
	content, ok := args[1].(String)
	if !ok {
		return nil, fmt.Errorf("%s: expected string content, got %T", name, args[1])
	}
	if err := os.WriteFile(string(path), []byte(content), mode); err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	return resultOk(unitValue()), nil
}

func builtinNowUnixMS(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("now_unix_ms: expected 0 args, got %d", len(args))
	}
	return Number(time.Now().UnixMilli()), nil
}

func builtinNowNS(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("now_ns: expected 0 args, got %d", len(args))
	}
	return Number(time.Now().UnixNano()), nil
}

func builtinMonotonicNS(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("monotonic_ns: expected 0 args, got %d", len(args))
	}
	return Number(time.Now().UnixNano()), nil
}

func builtinClockResolution(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("clock_resolution: expected 0 args, got %d", len(args))
	}
	return Number(hostClockResolution()), nil
}

func builtinClockSet(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("clock_set: expected 2 args, got %d", len(args))
	}
	sec, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("clock_set: expected number seconds, got %T", args[0])
	}
	nsec, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("clock_set: expected number nanoseconds, got %T", args[1])
	}
	return ioResult("", hostClockSet(int64(sec), int64(nsec))), nil
}

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

func builtinSleepNS(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("sleep_ns: expected 1 arg, got %d", len(args))
	}
	ns, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("sleep_ns: expected number arg, got %T", args[0])
	}
	if int64(ns) > 0 {
		time.Sleep(time.Duration(int64(ns)))
	}
	return Void{}, nil
}

func builtinProcFork(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("proc_fork: expected 0 args, got %d", len(args))
	}
	return Number(-38), nil
}

func builtinProcExec(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("proc_exec: expected 2 args, got %d", len(args))
	}
	return Number(-38), nil
}

func builtinProcExecAs(_ *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("proc_exec_as: expected 3 args, got %d", len(args))
	}
	return Number(-38), nil
}

func builtinProcWaitpid(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("proc_waitpid: expected 1 arg, got %d", len(args))
	}
	if _, ok := args[0].(Number); !ok {
		return nil, fmt.Errorf("proc_waitpid: expected number arg, got %T", args[0])
	}
	return Number(-10), nil
}

func builtinProcWaitpidNohang(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("proc_waitpid_nohang: expected 1 arg, got %d", len(args))
	}
	if _, ok := args[0].(Number); !ok {
		return nil, fmt.Errorf("proc_waitpid_nohang: expected number arg, got %T", args[0])
	}
	return Number(-10), nil
}

func builtinProcWaitpidStatus(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("proc_waitpid_status: expected 1 arg, got %d", len(args))
	}
	if _, ok := args[0].(Number); !ok {
		return nil, fmt.Errorf("proc_waitpid_status: expected number arg, got %T", args[0])
	}
	return Number(-10), nil
}

func builtinTempDir(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("temp_dir: expected 1 arg, got %d", len(args))
	}
	prefix, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("temp_dir: expected string prefix, got %T", args[0])
	}
	if strings.ContainsRune(string(prefix), '/') {
		return resultErr(ioErrorOther(string(prefix), syscall.EINVAL)), nil
	}
	tempRoot := os.TempDir()
	if !utf8.ValidString(tempRoot) {
		return resultErr(ioErrorOfErrno(string(prefix), syscall.EILSEQ)), nil
	}
	dir, err := os.MkdirTemp(tempRoot, string(prefix)+"-*")
	if err != nil {
		return resultErr(classifyIoError(string(prefix), err)), nil
	}
	return resultOk(String(dir)), nil
}

func builtinReadDirBytes(_ *Interp, args []Value) (Value, error) {
	entries, res, err := readDirEntriesMode("read_dir_bytes", true, args, true)
	if entries == nil {
		return res, err
	}
	out := newArray(len(entries))
	for k, e := range entries {
		out.Elems()[k] = byteArray([]byte(e.name))
	}
	return resultOk(out), nil
}

func builtinReadDir(_ *Interp, args []Value) (Value, error) {
	return readDirLike("read_dir", true, args)
}

func builtinReadDirAll(_ *Interp, args []Value) (Value, error) {
	return readDirLike("read_dir_all", false, args)
}

func builtinReadDirIno(_ *Interp, args []Value) (Value, error) {
	return readDirInoLike("read_dir_ino", true, args)
}

func builtinReadDirInoAll(_ *Interp, args []Value) (Value, error) {
	return readDirInoLike("read_dir_ino_all", false, args)
}

func readDirInoLike(name string, skipDots bool, args []Value) (Value, error) {
	entries, res, err := readDirEntries(name, skipDots, args)
	if entries == nil {
		return res, err
	}
	out := newArray(len(entries))
	for i, e := range entries {
		out.Elems()[i] = &Struct{
			TypeName: "DirEntry",
			Fields:   map[string]Value{"name": String(e.name), "ino": Number(int64(e.ino)), "kind": Number(int64(e.kind))},
		}
	}
	return resultOk(out), nil
}

func readDirLike(name string, skipDots bool, args []Value) (Value, error) {
	entries, res, err := readDirEntries(name, skipDots, args)
	if entries == nil {
		return res, err
	}
	out := newArray(len(entries))
	for i, e := range entries {
		out.Elems()[i] = String(e.name)
	}
	return resultOk(out), nil
}

type dirent struct {
	name string
	ino  uint64
	kind uint32
}

func readDirEntries(name string, skipDots bool, args []Value) ([]dirent, Value, error) {
	return readDirEntriesMode(name, skipDots, args, false)
}

func readDirEntriesMode(name string, skipDots bool, args []Value, raw bool) ([]dirent, Value, error) {
	if len(args) != 1 {
		return nil, nil, fmt.Errorf("%s: expected 1 arg, got %d", name, len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, nil, fmt.Errorf("%s: expected string path, got %T", name, args[0])
	}
	entries, err := readDirAll(string(path))
	if err != nil {
		return nil, resultErr(classifyIoError(string(path), err)), nil
	}
	kept := []dirent{}
	for _, e := range entries {
		if !raw && !utf8.ValidString(e.name) {
			return nil, resultErr(ioErrorOfErrno(string(path), syscall.EILSEQ)), nil
		}
		if !skipDots || e.name != "." && e.name != ".." {
			kept = append(kept, e)
		}
	}
	return kept, nil, nil
}

func builtinStat(_ *Interp, args []Value) (Value, error) {
	return statLike("stat", os.Stat, true, args)
}

func builtinLstat(_ *Interp, args []Value) (Value, error) {
	return statLike("lstat", os.Lstat, false, args)
}

func statLike(name string, resolve func(string) (os.FileInfo, error), follow bool, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s: expected 1 arg, got %d", name, len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("%s: expected string path, got %T", name, args[0])
	}
	info, err := resolve(string(path))
	if err == nil && strings.HasSuffix(string(path), "/") {
		info, err = slashResolve(string(path), info)
	}
	if err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	return resultOk(fileStatValue(info, statOrigin{path: string(path), follow: follow})), nil
}

func slashResolve(path string, info os.FileInfo) (os.FileInfo, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		followed, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		info = followed
	}
	if !info.IsDir() {
		return nil, &os.PathError{Op: "stat", Path: path, Err: syscall.ENOTDIR}
	}
	return info, nil
}

func fileStatValue(info os.FileInfo, at statOrigin) *Struct {
	raw := statFields(info, at)
	return &Struct{
		TypeName: "FileStat",
		Fields: map[string]Value{
			"is_file":    Bool(info.Mode().IsRegular()),
			"is_dir":     Bool(info.IsDir()),
			"size":       Number(info.Size()),
			"mode":       Number(raw.mode),
			"nlink":      Number(raw.nlink),
			"uid":        Number(raw.uid),
			"gid":        Number(raw.gid),
			"dev":        Number(raw.dev),
			"rdev":       Number(raw.rdev),
			"ino":        Number(raw.ino),
			"blksize":    Number(raw.blksize),
			"blocks":     Number(raw.blocks),
			"atime":      Number(raw.atime),
			"atime_nsec": Number(raw.atimeNsec),
			"mtime":      Number(raw.mtime),
			"mtime_nsec": Number(raw.mtimeNsec),
			"ctime":      Number(raw.ctime),
			"ctime_nsec": Number(raw.ctimeNsec),
			"btime":      Number(raw.btime),
			"btime_nsec": Number(raw.btimeNsec),
		},
	}
}

var errClosedHandle = errors.New("handle closed")

func streamFile(i *Interp, v Value) (*os.File, error) {
	fd, err := streamFd(v)
	if err != nil {
		return nil, err
	}
	if i.closedStd[fd] {
		return nil, errClosedHandle
	}
	var s any
	switch fd {
	case 0:
		s = i.Stdin
	case 1:
		s = i.Stdout
	case 2:
		s = i.Stderr
	default:
		f, ok := i.openFiles[fd]
		if !ok {
			return nil, errClosedHandle
		}
		return f, nil
	}
	f, _ := s.(*os.File)
	return f, nil
}

func builtinFdStat(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("stat: expected 1 arg")
	}
	f, err := streamFile(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return resultErr(ioErrorOther("", syscall.EBADF)), nil
	}
	if err != nil {
		return nil, err
	}
	if f == nil {
		return resultErr(&Enum{EnumName: "IoError", VariantName: "Unsupported", Index: 5}), nil
	}
	info, serr := f.Stat()
	if serr != nil {
		return resultErr(classifyIoError("", serr)), nil
	}
	return resultOk(fileStatValue(info, statOrigin{file: f})), nil
}

func builtinFdFlags(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("flags: expected 1 arg")
	}
	f, err := streamFile(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return resultErr(ioErrorOther("", syscall.EBADF)), nil
	}
	if err != nil {
		return nil, err
	}
	if f == nil {
		return resultErr(&Enum{EnumName: "IoError", VariantName: "Unsupported", Index: 5}), nil
	}
	const fGetfl = 3
	raw, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), fGetfl, 0)
	if errno != 0 {
		return resultErr(classifyIoError("", errno)), nil
	}
	return resultOk(Number(fernHandleFlags(int(raw)))), nil
}

func builtinHandleIsatty(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("isatty: expected 1 arg")
	}
	f, err := streamFile(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return Bool(false), nil
	}
	if err != nil {
		return nil, err
	}
	if f == nil {
		return Bool(false), nil
	}
	return Bool(tty.IsTerminal(int(f.Fd()))), nil
}

func fernHandleFlags(raw int) int {
	access := raw & 3
	bits := 0
	if access != 1 {
		bits |= 1
	}
	if access != 0 {
		bits |= 2
	}
	if raw&syscall.O_APPEND != 0 {
		bits |= 4
	}
	return bits
}

func syncMethod(i *Interp, name string, args []Value, call func(int) error) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s: expected 1 arg", name)
	}
	f, err := streamFile(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return resultErr(ioErrorOther("", syscall.EBADF)), nil
	}
	if err != nil {
		return nil, err
	}
	if f == nil {
		return resultErr(&Enum{EnumName: "IoError", VariantName: "Unsupported", Index: 5}), nil
	}
	if err := call(int(f.Fd())); err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	return resultOk(unitValue()), nil
}

func builtinFsync(i *Interp, args []Value) (Value, error) {
	return syncMethod(i, "fsync", args, hostFsync)
}

func builtinFdatasync(i *Interp, args []Value) (Value, error) {
	return syncMethod(i, "fdatasync", args, hostFdatasync)
}

func builtinSyncfs(i *Interp, args []Value) (Value, error) {
	return syncMethod(i, "syncfs", args, hostSyncfs)
}

var errNoFadvise = errors.New("no fadvise on this host")

func builtinDropCache(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("drop_cache: expected 3 args")
	}
	off, ok1 := args[1].(Number)
	n, ok2 := args[2].(Number)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("drop_cache: offset and length must be numbers")
	}
	unsupported := false
	v, err := syncMethod(i, "drop_cache", args[:1], func(fd int) error {
		e := hostDropCache(fd, int64(off), int64(n))
		if errors.Is(e, errNoFadvise) {
			unsupported = true
			return nil
		}
		return e
	})
	if unsupported {
		return resultErr(&Enum{EnumName: "IoError", VariantName: "Unsupported", Index: 5}), nil
	}
	return v, err
}

func builtinSync(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("sync: expected 0 args, got %d", len(args))
	}
	hostSync()
	return Void{}, nil
}

func builtinHandleSeek(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("seek: expected 3 args")
	}
	f, err := streamFile(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return resultErr(ioErrorOther("", syscall.EBADF)), nil
	}
	if err != nil {
		return nil, err
	}
	off, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("seek: offset must be a number")
	}
	whence, ok := args[2].(Number)
	if !ok {
		return nil, fmt.Errorf("seek: whence must be a number")
	}
	if whence < 0 || whence > 4 {
		return resultErr(ioErrorOther("", syscall.EINVAL)), nil
	}
	if f == nil {
		return resultErr(ioErrorOther("", syscall.ESPIPE)), nil
	}
	pos, serr := f.Seek(int64(off), hostWhence(int(whence)))
	if serr != nil {
		return resultErr(classifyIoError("", serr)), nil
	}
	return resultOk(Number(pos)), nil
}

func hostWhence(w int) int {
	if runtime.GOOS == "darwin" && (w == 3 || w == 4) {
		return 7 - w
	}
	return w
}

func builtinReaderSpliceTo(_ *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("splice_to: expected 3 args")
	}
	return resultErr(&Enum{EnumName: "IoError", VariantName: "Unsupported", Index: 5}), nil
}

func builtinAccess(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("access: expected 2 args, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("access: expected string path, got %T", args[0])
	}
	mode, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("access: expected number mode, got %T", args[1])
	}
	if err := accessEffective(string(path), int(mode)); err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	return resultOk(unitValue()), nil
}

func builtinGeteuid(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("geteuid: expected 0 args, got %d", len(args))
	}
	return Number(os.Geteuid()), nil
}

func builtinGetegid(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("getegid: expected 0 args, got %d", len(args))
	}
	return Number(os.Getegid()), nil
}

func builtinGetuid(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("getuid: expected 0 args, got %d", len(args))
	}
	return Number(os.Getuid()), nil
}

func builtinAccountEntry(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("__account_entry: expected 2 args, got %d", len(args))
	}
	return Number(0), nil
}

func builtinGetgid(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("getgid: expected 0 args, got %d", len(args))
	}
	return Number(os.Getgid()), nil
}

func builtinGetgroups(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("getgroups: expected 0 args, got %d", len(args))
	}
	gs, err := os.Getgroups()
	if err != nil {
		return newArray(0), nil
	}
	out := newArray(len(gs))
	for k, g := range gs {
		out.Elems()[k] = Number(g)
	}
	return out, nil
}

func builtinEnviron(i *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("environ: expected 0 args, got %d", len(args))
	}
	if i.Env != nil {
		names := make([]string, 0, len(i.Env))
		for k := range i.Env {
			names = append(names, k)
		}
		sort.Strings(names)
		out := newArray(len(names))
		for k, n := range names {
			out.Elems()[k] = String(n + "=" + i.Env[n])
		}
		return out, nil
	}
	e := os.Environ()
	out := newArray(len(e))
	for k, entry := range e {
		out.Elems()[k] = String(entry)
	}
	return out, nil
}

func clampOffset(off, n int) int {
	if off < 0 {
		return 0
	}
	if off > n {
		return n
	}
	return off
}

func builtinHostname(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("hostname: expected 0 args, got %d", len(args))
	}
	h, err := os.Hostname()
	if err != nil {
		return String(""), nil
	}
	if !utf8.ValidString(h) {
		return String(""), nil
	}
	return String(h), nil
}

func builtinUnameField(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("uname_field: expected 1 arg, got %d", len(args))
	}
	n, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("uname_field: expected a number, got %T", args[0])
	}
	i := int(n)
	fields := unameFields()
	if i < 0 || i >= len(fields) {
		return String(""), nil
	}
	if !utf8.ValidString(fields[i]) {
		return String(""), nil
	}
	return String(fields[i]), nil
}

func builtinGetcwd(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("getcwd: expected 0 args, got %d", len(args))
	}
	wd, err := syscall.Getwd()
	if err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	if !utf8.ValidString(wd) {
		return resultErr(ioErrorOfErrno("", syscall.EILSEQ)), nil
	}
	return resultOk(String(wd)), nil
}

func builtinChdir(_ *Interp, args []Value) (Value, error) {
	p, err := pathArgs("chdir", args, 1)
	if err != nil {
		return nil, err
	}
	return ioResult(p[0], syscall.Chdir(p[0])), nil
}

func builtinChroot(_ *Interp, args []Value) (Value, error) {
	p, err := pathArgs("chroot", args, 1)
	if err != nil {
		return nil, err
	}
	return ioResult(p[0], syscall.ENOSYS), nil
}

func builtinSetuid(_ *Interp, args []Value) (Value, error) {
	return credRefusal("setuid", args)
}

func builtinSetgid(_ *Interp, args []Value) (Value, error) {
	return credRefusal("setgid", args)
}

func builtinSetgroups(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("setgroups: expected 1 arg, got %d", len(args))
	}
	if _, ok := args[0].(Array); !ok {
		return nil, fmt.Errorf("setgroups: expected array arg, got %T", args[0])
	}
	return ioResult("", syscall.ENOSYS), nil
}

func credRefusal(name string, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s: expected 1 arg, got %d", name, len(args))
	}
	if _, ok := args[0].(Number); !ok {
		return nil, fmt.Errorf("%s: expected number arg, got %T", name, args[0])
	}
	return ioResult("", syscall.ENOSYS), nil
}

func builtinCPUCount(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("cpu_count: expected 0 args, got %d", len(args))
	}
	return Number(runtime.NumCPU()), nil
}

func signalArg(name string, args []Value) (syscall.Signal, bool, error) {
	if len(args) != 1 {
		return 0, false, fmt.Errorf("%s: expected 1 arg, got %d", name, len(args))
	}
	n, ok := args[0].(Number)
	if !ok {
		return 0, false, fmt.Errorf("%s: expected number arg, got %T", name, args[0])
	}
	v := int64(n)
	if v == sigKill || v == sigStop {
		return 0, false, nil
	}
	return syscall.Signal(int32(v)), v >= 1 && v <= maxSignal, nil
}

const maxSignal = 64

func builtinStatfs(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("statfs: expected 1 arg, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("statfs: expected string path, got %T", args[0])
	}
	raw, err := fsStatFields(string(path))
	if err != nil {
		return resultErr(classifyIoError(string(path), err)), nil
	}
	return resultOk(&Struct{
		TypeName: "FsStat",
		Fields: map[string]Value{
			"block_size":   Number(raw.blockSize),
			"blocks":       Number(raw.blocks),
			"blocks_free":  Number(raw.blocksFree),
			"blocks_avail": Number(raw.blocksAvail),
			"files":        Number(raw.files),
			"files_free":   Number(raw.filesFree),
			"name_max":     Number(raw.nameMax),
			"path_max":     Number(raw.pathMax),
			"fs_type":      Number(raw.fsType),
			"fsid":         Number(raw.fsid),
			"frag_size":    Number(raw.fragSize),
			"fs_type_name": String(raw.fsTypeName),
		},
	}), nil
}

func builtinSysctl(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("sysctl: expected 1 arg, got %d", len(args))
	}
	arr, ok := args[0].(Array)
	if !ok {
		return nil, fmt.Errorf("sysctl: expected i32[] arg, got %T", args[0])
	}
	mib := make([]int32, len(arr.Elems()))
	for k, v := range arr.Elems() {
		n, ok := v.(Number)
		if !ok {
			return nil, fmt.Errorf("sysctl: expected i32 element, got %T", v)
		}
		mib[k] = int32(n)
	}
	data, err := sysctlMIB(mib)
	if err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	out := newArray(len(data))
	for k, b := range data {
		out.Elems()[k] = Number(b)
	}
	return resultOk(out), nil
}

func builtinMounts(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("mounts: expected 0 args, got %d", len(args))
	}
	rows, err := mountRows()
	if err != nil {
		return resultErr(classifyIoError(mountTablePath, err)), nil
	}
	out := newArray(len(rows))
	for i, m := range rows {
		if !utf8.ValidString(m.source) || !utf8.ValidString(m.target) || !utf8.ValidString(m.fstype) {
			return resultErr(ioErrorOfErrno("", syscall.EILSEQ)), nil
		}
		out.Elems()[i] = &Struct{
			TypeName: "MountEntry",
			Fields: map[string]Value{
				"source": String(m.source),
				"target": String(m.target),
				"fstype": String(m.fstype),
				"dev":    Number(m.dev),
			},
		}
	}
	return resultOk(out), nil
}

func builtinRlimitNofile(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("rlimit_nofile: expected 0 args, got %d", len(args))
	}
	cur, err := nofileSoft()
	if err == syscall.ENOSYS {
		return nil, fmt.Errorf("rlimit_nofile: no getrlimit(2) on %s", runtime.GOOS)
	}
	if err != nil {
		return Number(math.MaxInt64), nil
	}
	if cur > math.MaxInt64 {
		return Number(math.MaxInt64), nil
	}
	return Number(int64(cur)), nil
}

func builtinDisableCoreDumps(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("disable_core_dumps: expected 0 args, got %d", len(args))
	}
	return Bool(disableCoreDumps()), nil
}

func builtinProcessAlive(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("process_alive: expected 1 arg, got %d", len(args))
	}
	pid, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("process_alive: expected number pid, got %T", args[0])
	}
	if int64(pid) <= 0 {
		return Bool(false), nil
	}
	err := syscall.Kill(int(pid), 0)
	return Bool(err == nil || err == syscall.EPERM), nil
}

func builtinSignalSend(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("signal_send: expected 2 args, got %d", len(args))
	}
	pid, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("signal_send: expected number pid, got %T", args[0])
	}
	sig, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("signal_send: expected number signal, got %T", args[1])
	}
	return ioResult("", syscall.Kill(int(pid), syscall.Signal(sig))), nil
}

func builtinSetProcessGroup(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("set_process_group: expected 2 args, got %d", len(args))
	}
	pid, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("set_process_group: expected number pid, got %T", args[0])
	}
	pgid, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("set_process_group: expected number pgid, got %T", args[1])
	}
	return ioResult("", hostSetProcessGroup(int(pid), int(pgid))), nil
}

func builtinSignalIgnore(_ *Interp, args []Value) (Value, error) {
	sig, ok, err := signalArg("signal_ignore", args)
	if err != nil {
		return nil, err
	}
	if !ok {
		return Number(-22), nil
	}
	signal.Ignore(sig)
	uncatch(sig)
	return Number(0), nil
}

func builtinSignalDefault(_ *Interp, args []Value) (Value, error) {
	sig, ok, err := signalArg("signal_default", args)
	if err != nil {
		return nil, err
	}
	if !ok {
		return Number(-22), nil
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sig)
	signal.Stop(ch)
	signal.Reset(sig)
	uncatch(sig)
	return Number(0), nil
}

func builtinSignalMask(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("signal_mask: expected 2 args, got %d", len(args))
	}
	how, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("signal_mask: expected number how, got %T", args[0])
	}
	mask, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("signal_mask: expected number mask, got %T", args[1])
	}
	prev, errno := sigprocmaskSwap(uintptr(uint64(how)), int64(mask))
	if errno != 0 {
		return Number(-int64(errno)), nil
	}
	return Number(prev), nil
}

func builtinSignalDisposition(_ *Interp, args []Value) (Value, error) {
	sig, ok, err := signalArg("signal_disposition", args)
	if err != nil {
		return nil, err
	}
	if !ok {
		return Number(-22), nil
	}
	if signal.Ignored(sig) {
		return Number(1), nil
	}
	if isCaught(sig) {
		return Number(2), nil
	}
	return Number(0), nil
}

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

func pathArgs(name string, args []Value, n int) ([]string, error) {
	if len(args) != n {
		return nil, fmt.Errorf("%s: expected %d arg(s), got %d", name, n, len(args))
	}
	out := make([]string, n)
	for i, a := range args {
		s, ok := a.(String)
		if !ok {
			return nil, fmt.Errorf("%s: expected string path, got %T", name, a)
		}
		out[i] = string(s)
	}
	return out, nil
}

func ioResult(path string, err error) Value {
	if err != nil {
		return resultErr(classifyIoError(path, err))
	}
	return resultOk(unitValue())
}

func builtinCreateDir(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("create_dir: expected 2 args, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("create_dir: expected string path, got %T", args[0])
	}
	mode, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("create_dir: expected number mode, got %T", args[1])
	}
	return ioResult(string(path), syscall.Mkdir(string(path), uint32(int(mode))&0o7777)), nil
}

func builtinRemoveDir(_ *Interp, args []Value) (Value, error) {
	p, err := pathArgs("remove_dir", args, 1)
	if err != nil {
		return nil, err
	}
	return ioResult(p[0], syscall.Rmdir(p[0])), nil
}

func builtinCreateLink(_ *Interp, args []Value) (Value, error) {
	p, err := pathArgs("create_link", args, 2)
	if err != nil {
		return nil, err
	}
	return ioResult(p[1], syscall.Link(p[0], p[1])), nil
}

func builtinCloneFile(_ *Interp, args []Value) (Value, error) {
	p, err := pathArgs("clone_file", args, 2)
	if err != nil {
		return nil, err
	}
	return ioResult(p[1], cloneFile(p[0], p[1])), nil
}

func builtinCreateSymlink(_ *Interp, args []Value) (Value, error) {
	p, err := pathArgs("create_symlink", args, 2)
	if err != nil {
		return nil, err
	}
	return ioResult(p[1], syscall.Symlink(p[0], p[1])), nil
}

func builtinReadLink(_ *Interp, args []Value) (Value, error) {
	return readLinkResult(args, false)
}

func builtinReadLinkBytes(_ *Interp, args []Value) (Value, error) {
	return readLinkResult(args, true)
}

func readLinkResult(args []Value, raw bool) (Value, error) {
	name := "read_link"
	if raw {
		name = "read_link_bytes"
	}
	p, err := pathArgs(name, args, 1)
	if err != nil {
		return nil, err
	}
	for size := 256; size <= 65536; size *= 2 {
		buf := make([]byte, size)
		n, err := syscall.Readlink(p[0], buf)
		if err != nil {
			return resultErr(classifyIoError(p[0], err)), nil
		}
		if n < size {
			if raw {
				out := newArray(n)
				for j, b := range buf[:n] {
					out.Elems()[j] = Number(b)
				}
				return resultOk(out), nil
			}
			if !utf8.Valid(buf[:n]) {
				return resultErr(ioErrorOfErrno(p[0], syscall.EILSEQ)), nil
			}
			return resultOk(String(buf[:n])), nil
		}
	}
	return resultErr(ioErrorOther(p[0], syscall.ENAMETOOLONG)), nil
}

func builtinGetxattr(_ *Interp, args []Value) (Value, error) {
	return xattrResult("getxattr", args, true, false)
}

func builtinLgetxattr(_ *Interp, args []Value) (Value, error) {
	return xattrResult("lgetxattr", args, false, false)
}

func builtinGetxattrBytes(_ *Interp, args []Value) (Value, error) {
	return xattrResult("getxattr_bytes", args, true, true)
}

func builtinLgetxattrBytes(_ *Interp, args []Value) (Value, error) {
	return xattrResult("lgetxattr_bytes", args, false, true)
}

func xattrResult(name string, args []Value, follow, raw bool) (Value, error) {
	p, err := pathArgs(name, args, 2)
	if err != nil {
		return nil, err
	}
	v, err := getxattrBytes(p[0], p[1], follow)
	if err != nil {
		return resultErr(classifyIoError(p[0], err)), nil
	}
	if raw {
		out := newArray(len(v))
		for i, b := range v {
			out.Elems()[i] = Number(b)
		}
		return resultOk(out), nil
	}
	if !utf8.Valid(v) {
		return resultErr(&Enum{EnumName: "IoError", VariantName: "InvalidUtf8", Index: 3,
			Payloads: []Value{String(p[0])}}), nil
	}
	return resultOk(String(v)), nil
}

func builtinSetxattr(_ *Interp, args []Value) (Value, error) {
	return setxattrResult("setxattr", args, true)
}

func builtinLsetxattr(_ *Interp, args []Value) (Value, error) {
	return setxattrResult("lsetxattr", args, false)
}

func setxattrResult(name string, args []Value, follow bool) (Value, error) {
	p, err := pathArgs(name, args, 3)
	if err != nil {
		return nil, err
	}
	return ioResult(p[0], setxattrBytes(p[0], p[1], []byte(p[2]), follow)), nil
}

func builtinSetxattrBytes(_ *Interp, args []Value) (Value, error) {
	return setxattrBytesResult("setxattr_bytes", args, true)
}

func builtinLsetxattrBytes(_ *Interp, args []Value) (Value, error) {
	return setxattrBytesResult("lsetxattr_bytes", args, false)
}

func setxattrBytesResult(name string, args []Value, follow bool) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("%s: expected 3 args, got %d", name, len(args))
	}
	p, err := pathArgs(name, args[:2], 2)
	if err != nil {
		return nil, err
	}
	content, ok := args[2].(Array)
	if !ok {
		return nil, fmt.Errorf("%s: expected byte array, got %T", name, args[2])
	}
	data := make([]byte, len(content.Elems()))
	for n, value := range content.Elems() {
		data[n] = byte(value.(Number))
	}
	return ioResult(p[0], setxattrBytes(p[0], p[1], data, follow)), nil
}

func builtinRename(_ *Interp, args []Value) (Value, error) {
	p, err := pathArgs("rename", args, 2)
	if err != nil {
		return nil, err
	}
	return ioResult(p[1], syscall.Rename(p[0], p[1])), nil
}

func builtinRenameNoreplace(_ *Interp, args []Value) (Value, error) {
	p, err := pathArgs("rename_noreplace", args, 2)
	if err != nil {
		return nil, err
	}
	return ioResult(p[1], renameNoReplace(p[0], p[1])), nil
}

func builtinRenameExchange(_ *Interp, args []Value) (Value, error) {
	p, err := pathArgs("rename_exchange", args, 2)
	if err != nil {
		return nil, err
	}
	return ioResult(p[1], renameExchangeNames(p[0], p[1])), nil
}

func builtinChmod(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("chmod: expected 2 args, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("chmod: expected string path, got %T", args[0])
	}
	mode, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("chmod: expected number mode, got %T", args[1])
	}
	return ioResult(string(path), syscall.Chmod(string(path), uint32(int(mode))&0o7777)), nil
}

func builtinChmodAt(_ *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("chmod_at: expected 3 args, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("chmod_at: expected string path, got %T", args[0])
	}
	mode, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("chmod_at: expected number mode, got %T", args[1])
	}
	follow, ok := args[2].(Bool)
	if !ok {
		return nil, fmt.Errorf("chmod_at: expected bool follow, got %T", args[2])
	}
	return ioResult(string(path), chmodAt(string(path), uint32(int(mode))&0o7777, bool(follow))), nil
}

func builtinTruncate(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("truncate: expected 2 args, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("truncate: expected string path, got %T", args[0])
	}
	length, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("truncate: expected number length, got %T", args[1])
	}
	return ioResult(string(path), syscall.Truncate(string(path), int64(length))), nil
}

const badSIFMT = 0o170000

func builtinMknod(_ *Interp, args []Value) (Value, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("mknod: expected 4 args, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("mknod: expected string path, got %T", args[0])
	}
	var n [3]uint32
	for i := 1; i < 4; i++ {
		v, ok := args[i].(Number)
		if !ok {
			return nil, fmt.Errorf("mknod: expected number arg %d, got %T", i, args[i])
		}
		n[i-1] = uint32(int64(v))
	}
	return ioResult(string(path), mknodAt(string(path), n[0], n[1], n[2])), nil
}

func builtinChownAt(_ *Interp, args []Value) (Value, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("chown_at: expected 4 args, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("chown_at: expected string path, got %T", args[0])
	}
	var ids [2]int
	for i := 1; i < 3; i++ {
		v, ok := args[i].(Number)
		if !ok {
			return nil, fmt.Errorf("chown_at: expected number arg %d, got %T", i, args[i])
		}
		ids[i-1] = int(int32(int64(v)))
	}
	follow, ok := args[3].(Bool)
	if !ok {
		return nil, fmt.Errorf("chown_at: expected bool follow, got %T", args[3])
	}
	return ioResult(string(path), chownAt(string(path), ids[0], ids[1], bool(follow))), nil
}

const (
	timesNoFollow  = 1
	timesOmitAtime = 2
	timesOmitMtime = 4
	timesNowAtime  = 8
	timesNowMtime  = 16
)

func builtinSetFileTimes(_ *Interp, args []Value) (Value, error) {
	if len(args) != 6 {
		return nil, fmt.Errorf("set_file_times: expected 6 args, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("set_file_times: expected string path, got %T", args[0])
	}
	var n [5]int64
	for i := 1; i < 6; i++ {
		v, ok := args[i].(Number)
		if !ok {
			return nil, fmt.Errorf("set_file_times: expected number arg %d, got %T", i, args[i])
		}
		n[i-1] = int64(v)
	}
	flags := n[4]
	times := [2]syscall.Timespec{
		{Sec: n[0], Nsec: n[1]},
		{Sec: n[2], Nsec: n[3]},
	}
	if flags&timesNowAtime != 0 {
		times[0] = syscall.Timespec{Nsec: utimeNow}
	}
	if flags&timesNowMtime != 0 {
		times[1] = syscall.Timespec{Nsec: utimeNow}
	}
	if flags&timesOmitAtime != 0 {
		times[0] = syscall.Timespec{Nsec: utimeOmit}
	}
	if flags&timesOmitMtime != 0 {
		times[1] = syscall.Timespec{Nsec: utimeOmit}
	}
	err := setFileTimes(string(path), &times, flags&timesNoFollow != 0)
	return ioResult(string(path), err), nil
}

func builtinUmask(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("umask: expected 1 arg, got %d", len(args))
	}
	mask, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("umask: expected number mask, got %T", args[0])
	}
	return Number(setUmask(int(mask) & 0o7777)), nil
}

func builtinPriority(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("priority: expected 0 args, got %d", len(args))
	}
	return Number(getProcPriority()), nil
}

func builtinSetPriority(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("set_priority: expected 1 arg, got %d", len(args))
	}
	nice, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("set_priority: expected number nice value, got %T", args[0])
	}
	return ioResult("", setProcPriority(int(nice))), nil
}

func builtinCreateDirAll(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("create_dir_all: expected 1 arg, got %d", len(args))
	}
	path, ok := args[0].(String)
	if !ok {
		return nil, fmt.Errorf("create_dir_all: expected string path, got %T", args[0])
	}
	if err := os.MkdirAll(string(path), 0o777); err != nil {
		if _, serr := os.Lstat(string(path)); serr == nil {
			return resultOk(unitValue()), nil
		}
		return resultErr(classifyIoError(string(path), err)), nil
	}
	return resultOk(unitValue()), nil
}

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
	argv := make([]string, len(argsArr.Elems()))
	for i, a := range argsArr.Elems() {
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
			exitCode = 127
			errno := syscall.ENOENT
			errors.As(runErr, &errno)
			errBuf.WriteString(string(cmdStr) + ": " + strerror.Text(runtime.GOOS, int(errno)) + "\n")
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

func classifyIoError(path string, err error) *Enum {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return ioErrorOfErrno(path, errno)
	}
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
	switch {
	case errors.Is(err, os.ErrClosed):
		return ioErrorOther(path, syscall.EBADF)
	}
	return &Enum{EnumName: "IoError", VariantName: "Other", Index: 6,
		Payloads: []Value{String(path), String(err.Error()), Number(0)}}
}

func ioErrorOfErrno(path string, errno syscall.Errno) *Enum {
	switch errno {
	case syscall.ENOENT:
		return &Enum{EnumName: "IoError", VariantName: "NotFound", Index: 0,
			Payloads: []Value{String(path)}}
	case syscall.EACCES:
		return &Enum{EnumName: "IoError", VariantName: "PermissionDenied", Index: 1,
			Payloads: []Value{String(path)}}
	case syscall.EEXIST:
		return &Enum{EnumName: "IoError", VariantName: "AlreadyExists", Index: 2,
			Payloads: []Value{String(path)}}
	case syscall.EILSEQ:
		return &Enum{EnumName: "IoError", VariantName: "InvalidUtf8", Index: 3,
			Payloads: []Value{String(path)}}
	case syscall.EINTR:
		return &Enum{EnumName: "IoError", VariantName: "Interrupted", Index: 4}
	}
	return ioErrorOther(path, errno)
}

func ioErrorOther(path string, errno syscall.Errno) *Enum {
	return &Enum{EnumName: "IoError", VariantName: "Other", Index: 6,
		Payloads: []Value{String(path), String(strerror.Text(runtime.GOOS, int(errno))),
			Number(strerror.Carried(runtime.GOOS, int(errno)))}}
}

func unitValue() Value { return Number(0) }

func resultOk(v Value) *Enum {
	return &Enum{EnumName: "Result", VariantName: "Ok", Index: 0, Payloads: []Value{v}}
}

func resultErr(v Value) *Enum {
	return &Enum{EnumName: "Result", VariantName: "Err", Index: 1, Payloads: []Value{v}}
}

func builtinOpenReader(i *Interp, args []Value) (Value, error) {
	return openHelper(i, args, "Reader", os.O_RDONLY, 0)
}

func builtinOpenWriter(i *Interp, args []Value) (Value, error) {
	return openHelper(i, args, "Writer", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
}

func builtinOpenAppender(i *Interp, args []Value) (Value, error) {
	return openHelper(i, args, "Writer", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
}

func builtinOpenExclusive(i *Interp, args []Value) (Value, error) {
	return openHelper(i, args, "Writer", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func builtinOpenReaderWith(i *Interp, args []Value) (Value, error) {
	return openWithHelper(i, args, "Reader", os.O_RDONLY)
}

func builtinOpenWriterWith(i *Interp, args []Value) (Value, error) {
	return openWithHelper(i, args, "Writer", os.O_WRONLY)
}

func openWithHelper(i *Interp, args []Value, structName string, access int) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("open_*_with: expected 2 args, got %d", len(args))
	}
	flags, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("open_*_with: expected number flags, got %T", args[1])
	}
	flag := access
	if int(flags)&1 != 0 {
		flag |= os.O_CREATE
	}
	if int(flags)&4 != 0 {
		flag |= os.O_EXCL
	}
	for _, b := range []struct{ bit, word int }{
		{2, oNonblock}, {8, oDirect}, {16, oDirectory}, {32, oDsync},
		{64, oSync}, {128, oNoatime}, {256, oNoctty}, {512, oNofollow},
	} {
		if int(flags)&b.bit == 0 {
			continue
		}
		if b.word == 0 && b.bit != 2 {
			return resultErr(&Enum{EnumName: "IoError", VariantName: "Unsupported", Index: 5}), nil
		}
		flag |= b.word
	}
	return openHelper(i, args[:1], structName, flag, 0o666)
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

func builtinPipe(i *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("pipe: expected 0 args, got %d", len(args))
	}
	rf, wf, err := os.Pipe()
	if err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	end := func(name string, f *os.File) Value {
		id := i.nextFd
		i.nextFd++
		i.openFiles[id] = f
		return &Struct{TypeName: name, Fields: map[string]Value{"fd": Number(id)}}
	}
	return resultOk(&Struct{
		TypeName: "Pipe",
		Fields:   map[string]Value{"r": end("Reader", rf), "w": end("Writer", wf)},
	}), nil
}

func readerStream(i *Interp, v Value) (io.Reader, error) {
	fd, err := streamFd(v)
	if err != nil {
		return nil, err
	}
	if i.closedStd[fd] {
		return nil, errClosedHandle
	}
	switch fd {
	case 0:
		return interruptibleReader(i.Stdin), nil
	}
	f, ok := i.openFiles[fd]
	if !ok {
		return nil, errClosedHandle
	}
	return interruptibleReader(f), nil
}

func writerStream(i *Interp, v Value) (io.Writer, error) {
	fd, err := streamFd(v)
	if err != nil {
		return nil, err
	}
	if i.closedStd[fd] {
		return nil, errClosedHandle
	}
	switch fd {
	case 1:
		return i.Stdout, nil
	case 2:
		return i.Stderr, nil
	}
	f, ok := i.openFiles[fd]
	if !ok {
		return nil, errClosedHandle
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

func builtinReaderReadChunk(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("Reader.read_chunk: expected 2 args")
	}
	size, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("Reader.read_chunk: size must be a number")
	}
	if size < 0 {
		return resultErr(classifyIoError("", syscall.EINVAL)), nil
	}
	r, err := readerStream(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return resultErr(ioErrorOther("", syscall.EBADF)), nil
	}
	if err != nil {
		return nil, err
	}
	buf := make([]byte, int(size))
	n, rerr := r.Read(buf)
	if n == 0 && rerr != nil && !errors.Is(rerr, io.EOF) {
		return resultErr(classifyIoError("", rerr)), nil
	}
	if !utf8.Valid(buf[:n]) {
		return resultErr(ioErrorOfErrno("", syscall.EILSEQ)), nil
	}
	return resultOk(String(string(buf[:n]))), nil
}

func builtinReaderReadChunkBytes(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("Reader.read_chunk_bytes: expected 2 args")
	}
	size, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("Reader.read_chunk_bytes: size must be a number")
	}
	if size < 0 {
		return resultErr(classifyIoError("", syscall.EINVAL)), nil
	}
	r, err := readerStream(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return resultErr(classifyIoError("", syscall.EBADF)), nil
	}
	if err != nil {
		return nil, err
	}
	buf := make([]byte, int(size))
	n, rerr := r.Read(buf)
	if n == 0 && rerr != nil && !errors.Is(rerr, io.EOF) {
		return resultErr(classifyIoError("", rerr)), nil
	}
	out := newArray(n)
	for j, b := range buf[:n] {
		out.Elems()[j] = Number(b)
	}
	return resultOk(out), nil
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
	if fd == 0 || fd == 1 || fd == 2 {
		if i.closedStd[fd] {
			return resultErr(ioErrorOther("", syscall.EBADF)), nil
		}
		if i.closedStd == nil {
			i.closedStd = map[int64]bool{}
		}
		i.closedStd[fd] = true
		return resultOk(unitValue()), nil
	}
	f, ok := i.openFiles[fd]
	if !ok {
		return resultErr(ioErrorOther("", syscall.EBADF)), nil
	}
	if err := f.Close(); err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	delete(i.openFiles, fd)
	return resultOk(unitValue()), nil
}

func builtinWriterWrite(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("Writer.write: expected 2 args")
	}
	w, err := writerStream(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return resultErr(ioErrorOther("", syscall.EBADF)), nil
	}
	if err != nil {
		return nil, err
	}
	s, ok := args[1].(String)
	if !ok {
		return nil, fmt.Errorf("Writer.write: content must be a string")
	}
	if _, err := w.Write([]byte(s)); err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	return resultOk(unitValue()), nil
}

func builtinWriterWriteSome(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("Writer.write_some: expected 2 args")
	}
	s, ok := args[1].(String)
	if !ok {
		return nil, fmt.Errorf("Writer.write_some: content must be a string")
	}
	f, err := streamFile(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return resultErr(ioErrorOther("", syscall.EBADF)), nil
	}
	if err != nil {
		return nil, err
	}
	if f == nil {
		w, werr := writerStream(i, args[0])
		if werr != nil {
			return nil, werr
		}
		n, e := w.Write([]byte(s))
		if e != nil {
			return resultErr(classifyIoError("", e)), nil
		}
		return resultOk(Number(n)), nil
	}
	n, e := syscall.Write(int(f.Fd()), []byte(s))
	if e != nil {
		return resultErr(classifyIoError("", e)), nil
	}
	return resultOk(Number(n)), nil
}

func builtinWriterTruncate(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("Writer.truncate: expected 2 args")
	}
	f, err := streamFile(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return resultErr(ioErrorOther("", syscall.EBADF)), nil
	}
	if err != nil {
		return nil, err
	}
	length, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("Writer.truncate: length must be a number")
	}
	if f == nil {
		return resultErr(ioErrorOther("", syscall.EINVAL)), nil
	}
	if terr := f.Truncate(int64(length)); terr != nil {
		return resultErr(classifyIoError("", terr)), nil
	}
	return resultOk(unitValue()), nil
}

func builtinWriterSpliceForever(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("splice_forever: expected 2 args")
	}
	return resultErr(&Enum{EnumName: "IoError", VariantName: "Unsupported", Index: 5}), nil
}

func builtinDupOnto(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("dup_onto: expected 2 args")
	}
	f, err := streamFile(i, args[0])
	if errors.Is(err, errClosedHandle) {
		return resultErr(ioErrorOther("", syscall.EBADF)), nil
	}
	if err != nil {
		return nil, err
	}
	target, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("dup_onto: descriptor must be a number")
	}
	if f == nil {
		return resultErr(&Enum{EnumName: "IoError", VariantName: "Unsupported", Index: 5}), nil
	}
	if derr := hostDupOnto(int(f.Fd()), int(target)); derr != nil {
		return resultErr(classifyIoError("", derr)), nil
	}
	return resultOk(unitValue()), nil
}

func handleFd(i *Interp, recv Value) (int, Value, error) {
	f, err := streamFile(i, recv)
	if errors.Is(err, errClosedHandle) {
		return 0, resultErr(ioErrorOther("", syscall.EBADF)), nil
	}
	if err != nil {
		return 0, nil, err
	}
	if f == nil {
		return 0, resultErr(&Enum{EnumName: "IoError", VariantName: "Unsupported", Index: 5}), nil
	}
	return int(f.Fd()), nil, nil
}

func builtinHandleWindowSize(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("window_size: expected 1 arg, got %d", len(args))
	}
	fd, refusal, err := handleFd(i, args[0])
	if refusal != nil || err != nil {
		return refusal, err
	}
	return builtinWindowSize(i, []Value{Number(fd)})
}

func builtinHandleSetWindowSize(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("set_window_size: expected 3 args, got %d", len(args))
	}
	fd, refusal, err := handleFd(i, args[0])
	if refusal != nil || err != nil {
		return refusal, err
	}
	return builtinSetWindowSize(i, []Value{Number(fd), args[1], args[2]})
}

func builtinHandleTermiosGet(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("termios_get: expected 1 arg, got %d", len(args))
	}
	fd, refusal, err := handleFd(i, args[0])
	if refusal != nil || err != nil {
		return refusal, err
	}
	return builtinTermiosGet(i, []Value{Number(fd)})
}

func builtinHandleTermiosSet(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("termios_set: expected 3 args, got %d", len(args))
	}
	fd, refusal, err := handleFd(i, args[0])
	if refusal != nil || err != nil {
		return refusal, err
	}
	return builtinTermiosSet(i, []Value{Number(fd), args[1], args[2]})
}

func builtinHandleSetExtproc(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("set_extproc: expected 2 args, got %d", len(args))
	}
	fd, refusal, err := handleFd(i, args[0])
	if refusal != nil || err != nil {
		return refusal, err
	}
	return builtinSetExtproc(i, []Value{Number(fd), args[1]})
}

func builtinSetExtproc(_ *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("set_extproc: expected 2 args, got %d", len(args))
	}
	fd, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("set_extproc: expected number fd, got %T", args[0])
	}
	on, ok := args[1].(Bool)
	if !ok {
		return nil, fmt.Errorf("set_extproc: expected boolean, got %T", args[1])
	}
	if err := tty.SetExtproc(int(fd), bool(on)); err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	return resultOk(unitValue()), nil
}

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

func builtinInnerMulAddF64(i *Interp, args []Value) (Value, error) {
	if len(args) != 6 {
		return nil, fmt.Errorf("__inner_mul_add_f64: expected 6 args")
	}
	a, aok := args[0].(Array)
	b, bok := args[1].(Array)
	rows, rok := args[2].(Number)
	extent, eok := args[3].(Number)
	columns, cok := args[4].(Number)
	init, iok := args[5].(Float)
	if !aok || !bok || !rok || !eok || !cok || !iok {
		return nil, fmt.Errorf("__inner_mul_add_f64: invalid argument types")
	}
	m, k, n := int64(rows), int64(extent), int64(columns)
	if m < 0 || k < 0 || n < 0 || m > 2147483647 || k > 2147483647 || n > 2147483647 || m*k != int64(len(a.Elems())) || k*n != int64(len(b.Elems())) || m*n > 2147483647 {
		return builtinExit(i, []Value{Number(134)})
	}
	out := newArray(int(m * n))
	if len(out.Elems()) == 0 {
		return out, nil
	}
	for row := int64(0); row < m; row++ {
		for col := int64(0); col < n; col++ {
			acc := init.V
			for at := int64(0); at < k; at++ {
				x, okx := a.Elems()[row*k+at].(Float)
				y, oky := b.Elems()[at*n+col].(Float)
				if !okx || !oky {
					return nil, fmt.Errorf("__inner_mul_add_f64: elements must be f64")
				}
				product := float64(x.V * y.V)
				acc = acc + product
			}
			out.Elems()[row*n+col] = Float{V: acc, Width: 64}
		}
	}
	return out, nil
}

func builtinOuterMulF64(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("__outer_mul_f64: expected 2 args, got %d", len(args))
	}
	a, aok := args[0].(Array)
	b, bok := args[1].(Array)
	if !aok || !bok {
		return nil, fmt.Errorf("__outer_mul_f64: inputs must be arrays")
	}
	if len(b.Elems()) > 0 && len(a.Elems()) > 2147483647/len(b.Elems()) {
		return builtinExit(i, []Value{Number(134)})
	}
	out := newArray(len(a.Elems()) * len(b.Elems()))
	for ai, left := range a.Elems() {
		x, ok := left.(Float)
		if !ok {
			return nil, fmt.Errorf("__outer_mul_f64: elements must be f64")
		}
		for bi, right := range b.Elems() {
			y, ok := right.(Float)
			if !ok {
				return nil, fmt.Errorf("__outer_mul_f64: elements must be f64")
			}
			out.Elems()[ai*len(b.Elems())+bi] = Float{V: x.V * y.V, Width: 64}
		}
	}
	return out, nil
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
	fmt.Fprint(i.stdOut(1), string(s))
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
	fmt.Fprintln(i.stdOut(2), string(s))
	return Void{}, nil
}

func builtinArgs(i *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("args: expected 0 args, got %d", len(args))
	}
	out := newArray(len(i.Args))
	for k, a := range i.Args {
		out.Elems()[k] = String(a)
	}
	return out, nil
}

func builtinSetArgs(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("set_args: expected 1 arg, got %d", len(args))
	}
	arr, ok := args[0].(Array)
	if !ok {
		return nil, fmt.Errorf("set_args: expected string[] arg, got %T", args[0])
	}
	argv := make([]string, len(arr.Elems()))
	for k, e := range arr.Elems() {
		s, ok := e.(String)
		if !ok {
			return nil, fmt.Errorf("set_args: element %d is %T, not a string", k, e)
		}
		argv[k] = string(s)
	}
	i.Args = argv
	return Void{}, nil
}

func builtinBoolToString(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("boolean.to_string(): expected 1 arg (receiver), got %d", len(args))
	}
	b, ok := args[0].(Bool)
	if !ok {
		return nil, fmt.Errorf("boolean.to_string(): receiver is %T, not a boolean", args[0])
	}
	if b {
		return String("true"), nil
	}
	return String("false"), nil
}

func builtinLen(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf(".len(): expected 1 arg (receiver), got %d", len(args))
	}
	switch v := args[0].(type) {
	case String:
		return Number(int64(len(string(v)))), nil
	case Array:
		return Number(int64(len(v.Elems()))), nil
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
	fmt.Fprintln(i.stdOut(1), string(s))
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
	i.stdOut(1).Write([]byte{byte(int64(n))})
	return Void{}, nil
}

func (i *Interp) stdOut(fd int64) io.Writer {
	if i.closedStd[fd] {
		return io.Discard
	}
	if fd == 2 {
		return i.Stderr
	}
	return i.Stdout
}

func builtinPoll(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("poll: expected 2 args, got %d", len(args))
	}
	fds, ok := args[0].(Array)
	if !ok {
		return nil, fmt.Errorf("poll: expected array arg 0, got %T", args[0])
	}
	timeout, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("poll: expected number arg 1, got %T", args[1])
	}
	set, err := reactorCreate()
	if err != nil {
		return Number(-1), nil
	}
	defer syscall.Close(set)
	r := &reactor{fd: set, handles: map[int]int64{}}
	index := map[int]int{}
	hit := -1
	for k, v := range fds.Elems() {
		handle, ok := v.(Number)
		if !ok || handle < 0 {
			continue
		}
		raw, ok := i.rawFd(int64(handle))
		if !ok {
			if hit < 0 {
				hit = k
			}
			continue
		}
		if _, seen := index[raw]; seen {
			continue
		}
		if err := r.watch(raw, 1); err != nil {
			if hit < 0 {
				hit = k
			}
			continue
		}
		index[raw] = k
	}
	wait := int(timeout)
	if hit >= 0 {
		wait = 0
	}
	if len(index) == 0 {
		if hit < 0 && timeout > 0 {
			time.Sleep(time.Duration(timeout) * time.Millisecond)
		}
		return Number(hit), nil
	}
	got, err := r.wait(len(index), wait)
	if err != nil {
		return Number(hit), nil
	}
	for _, e := range got {
		if k, ok := index[e.raw]; ok && (hit < 0 || k < hit) {
			hit = k
		}
	}
	return Number(hit), nil
}

func builtinIsatty(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("isatty: expected 1 arg, got %d", len(args))
	}
	fd, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("isatty: expected number arg, got %T", args[0])
	}
	return Bool(tty.IsTerminal(int(fd))), nil
}

func builtinWindowSize(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("window_size: expected 1 arg, got %d", len(args))
	}
	fd, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("window_size: expected number arg, got %T", args[0])
	}
	rows, cols, err := tty.WindowSize(int(fd))
	if err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	return resultOk(&Struct{
		TypeName: "WinSize",
		Fields: map[string]Value{
			"rows": Number(int64(rows)),
			"cols": Number(int64(cols)),
		},
	}), nil
}

func builtinSetWindowSize(_ *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("set_window_size: expected 3 args, got %d", len(args))
	}
	nums := make([]int64, 3)
	for i, name := range []string{"fd", "rows", "cols"} {
		n, ok := args[i].(Number)
		if !ok {
			return nil, fmt.Errorf("set_window_size: expected number %s, got %T", name, args[i])
		}
		nums[i] = int64(n)
	}
	if err := tty.SetWindowSize(int(nums[0]), int(nums[1]), int(nums[2])); err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	return resultOk(unitValue()), nil
}

func builtinTermiosGet(_ *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("termios_get: expected 1 arg, got %d", len(args))
	}
	fd, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("termios_get: expected number arg, got %T", args[0])
	}
	words, err := tty.Termios(int(fd))
	if err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	out := newArray(len(words))
	for i, w := range words {
		out.Elems()[i] = Number(w)
	}
	return resultOk(out), nil
}

func builtinTermiosSet(_ *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("termios_set: expected 3 args, got %d", len(args))
	}
	fd, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("termios_set: expected number fd, got %T", args[0])
	}
	when, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("termios_set: expected number action, got %T", args[1])
	}
	arr, ok := args[2].(Array)
	if !ok {
		return nil, fmt.Errorf("termios_set: expected number[] words, got %T", args[2])
	}
	words := make([]int64, len(arr.Elems()))
	for i, e := range arr.Elems() {
		n, ok := e.(Number)
		if !ok {
			return nil, fmt.Errorf("termios_set: word %d is %T, not a number", i, e)
		}
		words[i] = int64(n)
	}
	if err := tty.SetTermios(int(fd), int(when), words); err != nil {
		return resultErr(classifyIoError("", err)), nil
	}
	return resultOk(unitValue()), nil
}

func builtinTargetOS(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("target_os: expected 0 args, got %d", len(args))
	}
	return String(runtime.GOOS), nil
}

func builtinTargetArch(_ *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("target_arch: expected 0 args, got %d", len(args))
	}
	switch runtime.GOARCH {
	case "arm64":
		return String("arm64"), nil
	case "amd64":
		return String("x86-64"), nil
	}
	return String(runtime.GOARCH), nil
}

func bufHandle(i *Interp, name string, v Value) (int64, []byte, error) {
	h, ok := v.(Number)
	if !ok {
		return 0, nil, fmt.Errorf("%s: expected a builder handle, got %T", name, v)
	}
	b, live := i.bufs[int64(h)]
	if !live {
		return 0, nil, fmt.Errorf("%s: %d is not a live builder handle", name, int64(h))
	}
	return int64(h), b, nil
}

func builtinBufNew(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("buf_new: expected 1 arg (cap), got %d", len(args))
	}
	c, ok := args[0].(Number)
	if !ok {
		return nil, fmt.Errorf("buf_new: expected number cap, got %T", args[0])
	}
	capacity := int64(c)
	if capacity < 0 {
		capacity = 0
	}
	h := i.nextBuf
	i.nextBuf++
	i.bufs[h] = make([]byte, 0, capacity)
	return Number(h), nil
}

func builtinBufPush(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("buf_push: expected 2 args (b, s), got %d", len(args))
	}
	h, b, err := bufHandle(i, "buf_push", args[0])
	if err != nil {
		return nil, err
	}
	s, ok := args[1].(String)
	if !ok {
		return nil, fmt.Errorf("buf_push: expected string arg, got %T", args[1])
	}
	i.bufs[h] = append(b, string(s)...)
	return Void{}, nil
}

func builtinBufPushBytesRange(i *Interp, args []Value) (Value, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("buf_push_bytes_range: expected 4 args, got %d", len(args))
	}
	h, b, err := bufHandle(i, "buf_push_bytes_range", args[0])
	if err != nil {
		return nil, err
	}
	a, ok := args[1].(Array)
	if !ok {
		return nil, fmt.Errorf("buf_push_bytes_range: expected byte array, got %T", args[1])
	}
	lo, lok := args[2].(Number)
	hi, hik := args[3].(Number)
	if !lok || !hik {
		return nil, fmt.Errorf("buf_push_bytes_range: bounds must be numbers")
	}
	low, high := max(int64(lo), 0), min(int64(hi), int64(len(a.Elems())))
	for at := low; at < high; at++ {
		n, ok := a.Elems()[at].(Number)
		if !ok {
			return nil, fmt.Errorf("buf_push_bytes_range: element %d is %T", at, a.Elems()[at])
		}
		b = append(b, byte(n))
	}
	i.bufs[h] = b
	return Void{}, nil
}

func builtinBufPushRange(i *Interp, args []Value) (Value, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("buf_push_range: expected 4 args (b, s, lo, hi), got %d", len(args))
	}
	h, b, err := bufHandle(i, "buf_push_range", args[0])
	if err != nil {
		return nil, err
	}
	s, ok := args[1].(String)
	if !ok {
		return nil, fmt.Errorf("buf_push_range: expected string arg, got %T", args[1])
	}
	lo, ok := args[2].(Number)
	if !ok {
		return nil, fmt.Errorf("buf_push_range: low bound must be number, got %T", args[2])
	}
	hi, ok := args[3].(Number)
	if !ok {
		return nil, fmt.Errorf("buf_push_range: high bound must be number, got %T", args[3])
	}
	low, high, slen := int64(lo), int64(hi), int64(len(s))
	if low < 0 || high > slen || low > high {
		return nil, sliceAbort(CauseSliceRange, "buf_push_range", low, high, slen)
	}
	i.bufs[h] = append(b, string(s)[low:high]...)
	return Void{}, nil
}

func builtinBufPushBytesTable(i *Interp, args []Value, mode string) (Value, error) {
	name := "buf_push_bytes_" + mode
	if len(args) != 3 {
		return nil, fmt.Errorf("%s: expected 3 arguments", name)
	}
	h, b, err := bufHandle(i, name, args[0])
	if err != nil {
		return nil, err
	}
	src, ok := args[1].(Array)
	if !ok {
		return nil, fmt.Errorf("%s: expected a byte array", name)
	}
	table, ok := args[2].(Array)
	if !ok {
		return nil, fmt.Errorf("%s: expected a byte table", name)
	}
	entry := func(k int) (byte, error) {
		v, ok := table.Elems()[k].(Number)
		if !ok {
			return 0, fmt.Errorf("%s: table element %d is not a byte", name, k)
		}
		return byte(int64(v)), nil
	}
	for at, value := range src.Elems() {
		v, ok := value.(Number)
		if !ok {
			return nil, fmt.Errorf("%s: input element %d is not a byte", name, at)
		}
		c := byte(int64(v))
		if mode == "expanded" {
			start := int(c) * 8
			if start+8 <= len(table.Elems()) {
				n, err := entry(start)
				if err != nil {
					return nil, err
				}
				if n > 7 {
					n = 7
				}
				for k := 1; k <= int(n); k++ {
					x, err := entry(start + k)
					if err != nil {
						return nil, err
					}
					b = append(b, x)
				}
				continue
			}
		} else if int(c) < len(table.Elems()) {
			x, err := entry(int(c))
			if err != nil {
				return nil, err
			}
			if mode == "filtered" {
				if x != 0 {
					continue
				}
			} else {
				c = x
			}
		}
		b = append(b, c)
	}
	i.bufs[h] = b
	return Void{}, nil
}

func builtinBufPushMapped(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("buf_push_mapped: expected 3 args (b, s, table), got %d", len(args))
	}
	h, b, err := bufHandle(i, "buf_push_mapped", args[0])
	if err != nil {
		return nil, err
	}
	s, ok := args[1].(String)
	if !ok {
		return nil, fmt.Errorf("buf_push_mapped: expected string arg, got %T", args[1])
	}
	table, ok := args[2].(Array)
	if !ok {
		return nil, fmt.Errorf("buf_push_mapped: expected a u8[] table, got %T", args[2])
	}
	for _, c := range []byte(string(s)) {
		if int(c) < len(table.Elems()) {
			e, ok := table.Elems()[c].(Number)
			if !ok {
				return nil, fmt.Errorf("buf_push_mapped: table element %d is %T, not a byte", c, table.Elems()[c])
			}
			c = byte(int64(e))
		}
		b = append(b, c)
	}
	i.bufs[h] = b
	return Void{}, nil
}

func builtinBufPushFiltered(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("buf_push_filtered: expected 3 args (b, s, drop), got %d", len(args))
	}
	h, b, err := bufHandle(i, "buf_push_filtered", args[0])
	if err != nil {
		return nil, err
	}
	s, ok := args[1].(String)
	if !ok {
		return nil, fmt.Errorf("buf_push_filtered: expected string arg, got %T", args[1])
	}
	drop, ok := args[2].(Array)
	if !ok {
		return nil, fmt.Errorf("buf_push_filtered: expected a u8[] table, got %T", args[2])
	}
	for _, c := range []byte(string(s)) {
		if int(c) < len(drop.Elems()) {
			e, ok := drop.Elems()[c].(Number)
			if !ok {
				return nil, fmt.Errorf("buf_push_filtered: table element %d is %T, not a byte", c, drop.Elems()[c])
			}
			if int64(e) != 0 {
				continue
			}
		}
		b = append(b, c)
	}
	i.bufs[h] = b
	return Void{}, nil
}

func builtinBufPushExpanded(i *Interp, args []Value) (Value, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("buf_push_expanded: expected 3 args (b, s, table), got %d", len(args))
	}
	h, b, err := bufHandle(i, "buf_push_expanded", args[0])
	if err != nil {
		return nil, err
	}
	s, ok := args[1].(String)
	if !ok {
		return nil, fmt.Errorf("buf_push_expanded: expected string arg, got %T", args[1])
	}
	table, ok := args[2].(Array)
	if !ok {
		return nil, fmt.Errorf("buf_push_expanded: expected a u8[] table, got %T", args[2])
	}
	entry := func(k int) (byte, error) {
		e, ok := table.Elems()[k].(Number)
		if !ok {
			return 0, fmt.Errorf("buf_push_expanded: table element %d is %T, not a byte", k, table.Elems()[k])
		}
		return byte(int64(e)), nil
	}
	for _, c := range []byte(string(s)) {
		at := int(c) * 8
		if at+8 > len(table.Elems()) {
			b = append(b, c)
			continue
		}
		n, err := entry(at)
		if err != nil {
			return nil, err
		}
		if n > 7 {
			n = 7
		}
		for k := 1; k <= int(n); k++ {
			x, err := entry(at + k)
			if err != nil {
				return nil, err
			}
			b = append(b, x)
		}
	}
	i.bufs[h] = b
	return Void{}, nil
}

func builtinBufPushByte(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("buf_push_byte: expected 2 args (b, x), got %d", len(args))
	}
	h, b, err := bufHandle(i, "buf_push_byte", args[0])
	if err != nil {
		return nil, err
	}
	x, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("buf_push_byte: expected number byte, got %T", args[1])
	}
	i.bufs[h] = append(b, byte(int64(x)&0xff))
	return Void{}, nil
}

func builtinBufPushU64(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("buf_push_u64: expected 2 args (b, v), got %d", len(args))
	}
	h, b, err := bufHandle(i, "buf_push_u64", args[0])
	if err != nil {
		return nil, err
	}
	v, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("buf_push_u64: expected number, got %T", args[1])
	}
	u := uint64(int64(v))
	var eight [8]byte
	binary.LittleEndian.PutUint64(eight[:], u)
	i.bufs[h] = append(b, eight[:]...)
	return Void{}, nil
}

func builtinBufLen(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("buf_len: expected 1 arg (b), got %d", len(args))
	}
	_, b, err := bufHandle(i, "buf_len", args[0])
	if err != nil {
		return nil, err
	}
	return Number(len(b)), nil
}

func builtinBufTake(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("buf_take: expected 1 arg (b), got %d", len(args))
	}
	h, b, err := bufHandle(i, "buf_take", args[0])
	if err != nil {
		return nil, err
	}
	s := builderText(b)
	i.bufs[h] = make([]byte, 0, cap(b))
	return s, nil
}

func builtinBufTakeBytes(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("buf_take_bytes: expected 1 arg (b), got %d", len(args))
	}
	h, b, err := bufHandle(i, "buf_take_bytes", args[0])
	if err != nil {
		return nil, err
	}
	out := newArray(len(b))
	for j, value := range b {
		out.Elems()[j] = Number(value)
	}
	i.bufs[h] = b[:0]
	return out, nil
}

func builtinBufClear(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("buf_clear: expected 1 arg (b), got %d", len(args))
	}
	h, b, err := bufHandle(i, "buf_clear", args[0])
	if err != nil {
		return nil, err
	}
	i.bufs[h] = b[:0]
	return Void{}, nil
}

func builtinBufFree(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("buf_free: expected 1 arg (b), got %d", len(args))
	}
	h, _, err := bufHandle(i, "buf_free", args[0])
	if err != nil {
		return nil, err
	}
	delete(i.bufs, h)
	return Void{}, nil
}

func (i *Interp) Register(fn *ast.FuncDecl) {
	i.Funcs[fn.Name] = fn
	if fn.Name == "__method_boolean_to_string" {
		delete(i.Builtins, fn.Name)
	}
	if fn.ImplTrait != "" && fn.MethodRecv != "" && fn.MethodSimpleName != "" {
		i.traitMethods[fn.ImplTrait+"."+fn.MethodRecv+"."+fn.MethodSimpleName] = fn.Name
		if fn.MethodSimpleName == "eq" && checker.SimpleTraitName(fn.ImplTrait) == "Eq" {
			i.keyEqs[fn.MethodRecv] = fn.Name
		}
	}
}

func (i *Interp) RegisterEnum(ed *ast.EnumDecl) { i.Enums[ed.Name] = ed }

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

func (i *Interp) CallByName(name string, args []Value) (Value, error) {
	if fn, ok := i.Funcs[name]; ok {
		return i.callFunc(fn, args)
	}
	if b, ok := i.Builtins[name]; ok {
		return b.Fn(i, args)
	}
	return nil, fmt.Errorf("undefined function %q", name)
}

type env struct {
	parent   *env
	vars     map[string]Value
	unwalked map[string]bool
}

type binding struct {
	value Value
}

func (b *binding) String() string { return b.value.String() }

func newEnv(parent *env) *env { return &env{parent: parent} }

func (e *env) capture(names []ast.Param) *env {
	out := newEnv(nil)
	if names != nil {
		for _, param := range names {
			if slot, ok := e.captureCell(param.Name); ok {
				out.captureBinding(param.Name, slot)
			}
		}
		return out
	}
	for cur := e; cur != nil; cur = cur.parent {
		for name := range cur.vars {
			if _, found := out.vars[name]; !found {
				slot, _ := cur.captureCell(name)
				out.captureBinding(name, slot)
			}
		}
	}
	return out
}

func (e *env) captureBinding(name string, slot *binding) {
	e.bindValue(name, slot)
}

func (e *env) captureCell(name string) (*binding, bool) {
	for cur := e; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
			if slot, ok := v.(*binding); ok {
				return slot, true
			}
			slot := &binding{value: v}
			cur.captureBinding(name, slot)
			return slot, true
		}
	}
	return nil, false
}

func (e *env) get(name string) (Value, bool) {
	for cur := e; cur != nil; cur = cur.parent {
		if v, ok := cur.vars[name]; ok {
			if slot, ok := v.(*binding); ok {
				return slot.value, true
			}
			return v, true
		}
	}
	return nil, false
}

func (e *env) set(name string, v Value) {
	for cur := e; cur != nil; cur = cur.parent {
		if old, ok := cur.vars[name]; ok {
			if slot, ok := old.(*binding); ok {
				slot.value = v
			} else {
				cur.vars[name] = v
			}
			return
		}
	}
	e.bindValue(name, v)
}

func (e *env) bindValue(name string, v Value) {
	if e.vars == nil {
		e.vars = make(map[string]Value)
	}
	e.vars[name] = v
}

func (e *env) declare(name string, v Value) {
	delete(e.unwalked, name)
	e.bindValue(name, v)
	retain(v)
	arrOwn(v)
}

func (e *env) declareTyped(name string, v Value, t ast.Type) {
	if _, isArray := v.(Array); !isArray || !holdsNoMap(t) {
		e.declare(name, v)
		return
	}
	e.bindValue(name, v)
	arrOwn(v)
	if e.unwalked == nil {
		e.unwalked = make(map[string]bool)
	}
	e.unwalked[name] = true
}

func holdsNoMap(t ast.Type) bool {
	switch x := t.(type) {
	case ast.NumberType, ast.BoolType, ast.StringType, ast.StrType, ast.CharType, ast.FloatType:
		return true
	case ast.ArrayType:
		return holdsNoMap(x.Elem)
	case ast.SliceType:
		return holdsNoMap(x.Elem)
	case ast.TupleType:
		for _, el := range x.Elems {
			if !holdsNoMap(el) {
				return false
			}
		}
		return true
	}
	return false
}

func (e *env) releaseScope() {
	for name, v := range e.vars {
		if _, captured := v.(*binding); captured {
			continue
		}
		if !e.unwalked[name] {
			release(v)
		}
		arrDisown(v)
	}
}

type tryOpEarlyReturn struct{ val Value }

func (t *tryOpEarlyReturn) Error() string { return "interp: TryOp early return" }

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
	flow  flowKind
	val   Value
	label string
}

func (i *Interp) callFunc(fn *ast.FuncDecl, args []Value) (Value, error) {
	if len(args) != len(fn.Params) {
		return nil, fmt.Errorf("%s: expected %d args, got %d", fn.Name, len(fn.Params), len(args))
	}
	e := newEnv(nil)
	for k, p := range fn.Params {
		e.declareTyped(p.Name, args[k], p.Type)
	}
	defer e.releaseScope()
	i.envs = append(i.envs, e)
	defer func() { i.envs = i.envs[:len(i.envs)-1] }()
	i.deferStack = append(i.deferStack, nil)
	r, err := i.execBlock(fn.Body, e)
	defers := i.deferStack[len(i.deferStack)-1]
	i.deferStack = i.deferStack[:len(i.deferStack)-1]
	if err != nil {
		if early, ok := err.(*tryOpEarlyReturn); ok {
			i.runDefers(defers, true)
			return early.val, nil
		}
		i.runDefers(defers, false)
		return nil, err
	}
	i.runDefers(defers, r.flow == flowReturn && i.isErrReturnValue(r.val))
	if r.flow == flowReturn {
		return r.val, nil
	}
	return Void{}, nil
}

type deferEntry struct {
	expr    ast.Expr
	onError bool
	env     *env
}

func (i *Interp) runDefers(defers []deferEntry, errorExit bool) {
	for k := len(defers) - 1; k >= 0; k-- {
		if defers[k].onError {
			continue
		}
		_, _ = i.evalExpr(defers[k].expr, defers[k].env)
	}
	if !errorExit {
		return
	}
	for k := len(defers) - 1; k >= 0; k-- {
		if !defers[k].onError {
			continue
		}
		_, _ = i.evalExpr(defers[k].expr, defers[k].env)
	}
}

func (i *Interp) deferMark() int {
	if len(i.deferStack) == 0 {
		return 0
	}
	return len(i.deferStack[len(i.deferStack)-1])
}

func (i *Interp) runIterDefers(mark int) {
	if len(i.deferStack) == 0 {
		return
	}
	top := len(i.deferStack) - 1
	pending := i.deferStack[top]
	if len(pending) <= mark {
		return
	}
	for k := len(pending) - 1; k >= mark; k-- {
		if pending[k].onError {
			continue
		}
		_, _ = i.evalExpr(pending[k].expr, pending[k].env)
	}
	i.deferStack[top] = pending[:mark]
}

func (i *Interp) isErrReturnValue(v Value) bool {
	ev, ok := v.(*Enum)
	if !ok || ev.Index != 1 {
		return false
	}
	if ev.EnumName == "Option" || ev.EnumName == "Result" {
		return true
	}
	ed, ok := i.Enums[ev.EnumName]
	return ok && ed.Try
}

func (i *Interp) execBlock(b *ast.Block, parent *env) (result, error) {
	e := newEnv(parent)
	defer e.releaseScope()
	i.envs = append(i.envs, e)
	defer func() { i.envs = i.envs[:len(i.envs)-1] }()
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
	case *ast.While:
		for {
			c, err := i.evalExpr(x.Cond, e)
			if err != nil {
				return result{}, err
			}
			if !asBool(c) {
				break
			}
			mark := i.deferMark()
			r, err := i.execStmt(x.Body, e)
			if err != nil {
				return result{}, err
			}
			if r.flow == flowReturn {
				return r, nil
			}
			i.runIterDefers(mark)
			if r.flow == flowBreak {
				if r.label != "" && r.label != x.Label {
					return r, nil
				}
				break
			}
			if r.flow == flowContinue && r.label != "" && r.label != x.Label {
				return r, nil
			}
		}
		return result{flow: flowNormal}, nil
	case *ast.Loop:
		for {
			mark := i.deferMark()
			r, err := i.execStmt(x.Body, e)
			if err != nil {
				return result{}, err
			}
			if r.flow == flowReturn {
				return r, nil
			}
			i.runIterDefers(mark)
			if r.flow == flowBreak {
				if r.label != "" && r.label != x.Label {
					return r, nil
				}
				break
			}
			if r.flow == flowContinue && r.label != "" && r.label != x.Label {
				return r, nil
			}
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
			mark := i.deferMark()
			r, err := i.execStmt(x.Body, inner)
			if err != nil {
				return result{}, err
			}
			if r.flow == flowReturn {
				return r, nil
			}
			i.runIterDefers(mark)
			if r.flow == flowBreak {
				if r.label != "" && r.label != x.Label {
					return r, nil
				}
				break
			}
			if r.flow == flowContinue && r.label != "" && r.label != x.Label {
				return r, nil
			}
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
		e.declareTyped(x.Name, v, x.Type)
		return result{flow: flowNormal}, nil
	case *ast.Destructure:
		v, err := i.evalExpr(x.Init, e)
		if err != nil {
			return result{}, err
		}
		if x.AtName != "" {
			e.declare(x.AtName, v)
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
		if len(arr.Elems()) != len(x.Names) {
			return result{}, fmt.Errorf("tuple has %d elements, but %d names given", len(arr.Elems()), len(x.Names))
		}
		for i2, name := range x.Names {
			e.declare(name, arr.Elems()[i2])
		}
		for i2 := range x.Nested {
			if x.Nested[i2] == nil {
				continue
			}
			if _, err := i.execStmtInner(x.Nested[i2], e); err != nil {
				return result{}, err
			}
		}
		return result{flow: flowNormal}, nil
	case *ast.ExprStmt:
		if _, err := i.evalExpr(x.Expr, e); err != nil {
			return result{}, err
		}
		return result{flow: flowNormal}, nil
	case *ast.FuncDecl:
		closure := &Closure{Decl: x}
		e.declare(x.Name, closure)
		closure.Env = e.capture(x.Captures)
		self, _ := e.captureCell(x.Name)
		closure.Env.captureBinding(x.Name, self)
		return result{flow: flowNormal}, nil
	case *ast.Match:
		tag, err := i.evalExpr(x.Tag, e)
		if err != nil {
			return result{}, err
		}
		if ev, ok := tag.(*Enum); ok {
			for _, arm := range x.Arms {
				if arm.IsWildcard || arm.VariantName == ev.VariantName {
					if !arm.IsWildcard && arm.Payloads != nil {
						ok, err := i.armPayloadsMatch(arm.Payloads, ev, e)
						if err != nil {
							return result{}, err
						}
						if !ok {
							continue
						}
					}
					armEnv := newEnv(e)
					if arm.AtBinding != "" {
						armEnv.declare(arm.AtBinding, tag)
					}
					if !arm.IsWildcard {
						bindArmPayloads(armEnv, arm.Bindings, arm.Payloads, ev)
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
			}
			return result{}, fmt.Errorf("interp: match did not cover variant %s (this should have been a checker error)", ev.VariantName)
		}
		if st, ok := tag.(*Struct); ok && x.StructMatch != "" {
			for _, arm := range x.Arms {
				armEnv := newEnv(e)
				if !arm.IsWildcard {
					if arm.AtBinding != "" {
						armEnv.declare(arm.AtBinding, st)
					}
					ok, err := i.structArmMatches(arm.Bindings, arm.FieldNames, arm.Payloads, st, e)
					if err != nil {
						return result{}, err
					}
					if !ok {
						continue
					}
					if err := bindStructArm(armEnv, arm.Bindings, arm.FieldNames, arm.Payloads, st); err != nil {
						return result{}, err
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
		if arr, isArr := tag.(Array); isArr && matchArmsAllowTuple(x.Arms) {
			for _, arm := range x.Arms {
				armEnv := newEnv(e)
				if !arm.IsWildcard {
					matched, err := i.tupleElemsMatch(arm.TupleElems, arr, e)
					if err != nil {
						return result{}, err
					}
					if !matched {
						continue
					}
					if arm.AtBinding != "" {
						armEnv.declare(arm.AtBinding, arr)
					}
					for k, el := range arm.TupleElems {
						bindTupleElem(armEnv, el, arr.Elems()[k])
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
		switch tag.(type) {
		case Number, Float, Bool, String:
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
			if arm.AtBinding != "" {
				armEnv.declare(arm.AtBinding, tag)
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
	case *ast.Defer:
		if n := len(i.deferStack); n > 0 {
			i.deferStack[n-1] = append(i.deferStack[n-1], deferEntry{expr: x.Expr, onError: x.OnError, env: e})
		}
		return result{flow: flowNormal}, nil
	}
	return result{}, fmt.Errorf("interp: unsupported statement %T", s)
}

func matchArmsAllowTuple(arms []*ast.MatchArm) bool {
	for _, arm := range arms {
		if !arm.IsWildcard && arm.TupleElems == nil {
			return false
		}
	}
	return len(arms) != 0
}

func matchExprArmsAllowTuple(arms []*ast.MatchExprArm) bool {
	for _, arm := range arms {
		if !arm.IsWildcard && arm.TupleElems == nil {
			return false
		}
	}
	return len(arms) != 0
}

func tupleElemVariantMatches(v Value, variantName string) bool {
	ev, isEnum := v.(*Enum)
	return isEnum && ev.VariantName == variantName
}

func (i *Interp) tupleElemsMatch(elems []ast.TuplePatElem, arr Array, e *env) (bool, error) {
	if len(elems) != len(arr.Elems()) {
		return false, nil
	}
	for k, el := range elems {
		ok, err := i.tupleElemMatches(el, arr.Elems()[k], e)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func (i *Interp) tupleElemMatches(el ast.TuplePatElem, v Value, e *env) (bool, error) {
	switch {
	case el.Nested != nil:
		sub, isArr := v.(Array)
		if !isArr {
			return false, nil
		}
		return i.tupleElemsMatch(el.Nested, sub, e)
	case el.IsStruct:
		st, isStruct := v.(*Struct)
		if !isStruct {
			return false, nil
		}
		return i.structArmMatches(el.VariantBindings, el.VariantFieldNames, el.VariantPayloads, st, e)
	case el.VariantName != "":
		if !tupleElemVariantMatches(v, el.VariantName) {
			return false, nil
		}
		ev, isEnum := v.(*Enum)
		if !isEnum {
			return false, nil
		}
		for idx, sub := range el.VariantPayloads {
			if sub == nil || idx >= len(ev.Payloads) {
				continue
			}
			ok, err := i.tupleElemMatches(*sub, ev.Payloads[idx], e)
			if err != nil || !ok {
				return false, err
			}
		}
	case el.Literal != nil:
		if el.RangeHi != nil {
			return i.armMatchesScalar(el.Literal, el.RangeHi, el.RangeInclusive, v, e)
		}
		lv, err := i.evalExpr(el.Literal, e)
		if err != nil {
			return false, err
		}
		if !valuesEqual(v, lv) {
			return false, nil
		}
	}
	return true, nil
}

func (i *Interp) structArmMatches(bindings, fieldNames []string, payloads []*ast.TuplePatElem, st *Struct, e *env) (bool, error) {
	for k, sub := range payloads {
		if sub == nil {
			continue
		}
		field := structArmField(bindings, fieldNames, k)
		fv, ok := st.Fields[field]
		if !ok {
			return false, fmt.Errorf("interp: struct %s has no field %q", st.TypeName, field)
		}
		m, err := i.tupleElemMatches(*sub, fv, e)
		if err != nil || !m {
			return false, err
		}
	}
	return true, nil
}

func bindStructArm(armEnv *env, bindings, fieldNames []string, payloads []*ast.TuplePatElem, st *Struct) error {
	for k, b := range bindings {
		field := structArmField(bindings, fieldNames, k)
		fv, ok := st.Fields[field]
		if !ok {
			return fmt.Errorf("interp: struct %s has no field %q", st.TypeName, field)
		}
		if k < len(payloads) && payloads[k] != nil {
			bindTupleElem(armEnv, *payloads[k], fv)
			continue
		}
		armEnv.declare(b, fv)
	}
	return nil
}

func structArmField(bindings, fieldNames []string, k int) string {
	if k < len(fieldNames) && fieldNames[k] != "" {
		return fieldNames[k]
	}
	if k < len(bindings) {
		return bindings[k]
	}
	return ""
}

func (i *Interp) armPayloadsMatch(payloads []*ast.TuplePatElem, ev *Enum, e *env) (bool, error) {
	for idx, sub := range payloads {
		if sub == nil || idx >= len(ev.Payloads) {
			continue
		}
		ok, err := i.tupleElemMatches(*sub, ev.Payloads[idx], e)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func bindArmPayloads(armEnv *env, bindings []string, payloads []*ast.TuplePatElem, ev *Enum) {
	for j, name := range bindings {
		if j >= len(ev.Payloads) {
			continue
		}
		if j < len(payloads) && payloads[j] != nil {
			bindTupleElem(armEnv, *payloads[j], ev.Payloads[j])
			continue
		}
		if name == "" {
			continue
		}
		armEnv.declare(name, ev.Payloads[j])
	}
}

func bindTupleElem(armEnv *env, el ast.TuplePatElem, v Value) {
	if el.AtBinding != "" {
		armEnv.declare(el.AtBinding, v)
	}
	if el.Nested != nil {
		sub, isArr := v.(Array)
		if !isArr {
			return
		}
		for k, nel := range el.Nested {
			if k < len(sub.Elems()) {
				bindTupleElem(armEnv, nel, sub.Elems()[k])
			}
		}
		return
	}
	if el.IsStruct {
		if st, isStruct := v.(*Struct); isStruct {
			_ = bindStructArm(armEnv, el.VariantBindings, el.VariantFieldNames, el.VariantPayloads, st)
		}
		return
	}
	if el.VariantName != "" {
		ev, isEnum := v.(*Enum)
		if !isEnum {
			return
		}
		for i, name := range el.VariantBindings {
			if i >= len(ev.Payloads) {
				continue
			}
			if i < len(el.VariantPayloads) && el.VariantPayloads[i] != nil {
				bindTupleElem(armEnv, *el.VariantPayloads[i], ev.Payloads[i])
				continue
			}
			if name == "" {
				continue
			}
			armEnv.declare(name, ev.Payloads[i])
		}
		return
	}
	if el.Name != "" {
		armEnv.declare(el.Name, v)
	}
}

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
		bx, ok := b.(Array)
		if !ok || len(ax.Elems()) != len(bx.Elems()) {
			return false
		}
		for i := range ax.Elems() {
			if !valuesEqual(ax.Elems()[i], bx.Elems()[i]) {
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

func (i *Interp) SetDynCoercions(sites map[ast.Expr]checker.DynCoercion) {
	i.dynSites = sites
}

func (i *Interp) evalExpr(e ast.Expr, env *env) (Value, error) {
	if i.dynSites == nil {
		return i.evalBare(e, env)
	}
	dc, ok := i.dynSites[e]
	if !ok || dc.Concrete == "i32" {
		return i.evalBare(e, env)
	}
	v, err := i.evalBare(e, env)
	if n, isNum := v.(Number); isNum && err == nil {
		return DynInt{N: n, TypeName: dc.Concrete}, nil
	}
	return v, err
}

func (i *Interp) evalBare(e ast.Expr, env *env) (Value, error) {
	switch x := e.(type) {
	case *ast.NumberLit:
		if x.IsFloat {
			w := x.FloatWidth
			if w == 0 {
				w = 64
			}
			v := float64(x.Value)
			if x.ExceedsI64 {
				v = float64(uint64(x.Value))
			}
			if w == 32 {
				v = float64(float32(v))
			}
			return Float{V: v, Width: w}, nil
		}
		return Number(x.Value), nil
	case *ast.FloatLit:
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
		v, err := i.evalExpr(x.Inner, env)
		if err != nil {
			return nil, err
		}
		if tgt, ok := x.Target.(ast.NumberType); ok {
			tw := resolvePtrWidth(tgt.NormalWidth())
			if src, ok := v.(Number); ok {
				return Number(narrowInt(int64(src), tw, !tgt.IsSigned())), nil
			}
			if src, ok := v.(Float); ok {
				sw := tw
				if sw < 32 {
					sw = 32
				}
				n := saturateFloatToInt(src.V, sw, tgt.IsSigned())
				return Number(narrowInt(n, tw, !tgt.IsSigned())), nil
			}
		}
		if tgt, ok := x.Target.(ast.FloatType); ok {
			if src, ok := v.(Number); ok {
				w := tgt.Width
				if w == 0 {
					w = 32
				}
				fv := float64(int64(src))
				if srcInt, ok := x.InnerType.(ast.NumberType); ok && !srcInt.IsSigned() {
					fv = float64(uint64(int64(src)))
				}
				if w == 32 {
					fv = float64(float32(fv))
				}
				return Float{V: fv, Width: w}, nil
			}
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
		if _, ok := x.Target.(ast.NumberType); !ok {
			if _, ok := x.Target.(ast.FloatType); !ok {
				return v, nil
			}
		}
		return nil, fmt.Errorf("cast from %T to %s not supported in the interpreter", v, x.Target)
	case *ast.BoolLit:
		return Bool(x.Value), nil
	case *ast.UnitLit:
		return Number(0), nil
	case *ast.StringLit:
		return String(x.Value), nil
	case *ast.CharLit:
		return Number(x.Value), nil
	case *ast.FString:
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
		if ed, idx, ok := i.findVariantOn(x.Name, x.EnumName); ok {
			if len(ed.Variants[idx].Payloads) != 0 {
				return nil, fmt.Errorf("interp: variant %s expects %d payload(s); call it instead",
					x.Name, len(ed.Variants[idx].Payloads))
			}
			return &Enum{EnumName: ed.Name, VariantName: x.Name, Index: idx}, nil
		}
		if _, ok := i.Builtins[x.Name]; ok {
			return nil, fmt.Errorf("interp: builtin %q can only be called, not used as a value", x.Name)
		}
		return nil, fmt.Errorf("undefined identifier %q", x.Name)
	case *ast.ArrayLit:
		out := newArray(len(x.Elems))
		for k, el := range x.Elems {
			v, err := i.evalExpr(el, env)
			if err != nil {
				return nil, err
			}
			storeArray(v)
			out.Elems()[k] = v
		}
		return out, nil
	case *ast.MapLit:
		m := &Map{}
		for _, ent := range x.Entries {
			kv, err := i.evalExpr(ent.Key, env)
			if err != nil {
				return nil, err
			}
			storeArray(kv)
			vv, err := i.evalExpr(ent.Value, env)
			if err != nil {
				return nil, err
			}
			storeArray(vv)
			idx, err := i.findKey(m, kv)
			if err != nil {
				return nil, err
			}
			if idx >= 0 {
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
		arrOwn(arrV)
		idxV, err := i.evalExpr(x.Idx, env)
		arrDisown(arrV)
		if err != nil {
			return nil, err
		}
		idx, ok := idxV.(Number)
		if !ok {
			return nil, fmt.Errorf("index must be number, got %T", idxV)
		}
		idxText := strconv.FormatInt(int64(idx), 10)
		if x.IdxUnsigned {
			idxText = strconv.FormatUint(uint64(idx), 10)
		}
		if s, ok := arrV.(String); ok {
			if idx < 0 || idx >= Number(len(string(s))) {
				return nil, indexAbort(CauseStrOOB, idxText, len(string(s)))
			}
			return Number(int64(string(s)[idx])), nil
		}
		arr, ok := arrV.(Array)
		if !ok {
			return nil, fmt.Errorf("indexing non-array %T", arrV)
		}
		if idx < 0 || idx >= Number(len(arr.Elems())) {
			return nil, indexAbort(CauseArrOOB, idxText, len(arr.Elems()))
		}
		return arr.Elems()[idx], nil
	case *ast.SliceExpr:
		srcV, err := i.evalExpr(x.Source, env)
		if err != nil {
			return nil, err
		}
		arrOwn(srcV)
		defer arrDisown(srcV)
		var slen int64
		switch v := srcV.(type) {
		case Array:
			slen = int64(len(v.Elems()))
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
		switch v := srcV.(type) {
		case Array:
			if low < 0 || high > slen || low > high {
				return nil, sliceAbort(CauseSliceRange, "slice", low, high, slen)
			}
			return v.view(int(low), int(high)), nil
		case String:
			if low < 0 || high > slen || low > high {
				return optionNone(), nil
			}
			if !interpCharBoundary(v, low) || !interpCharBoundary(v, high) {
				return optionNone(), nil
			}
			return optionSome(String(string(v)[low:high])), nil
		}
		return nil, fmt.Errorf("unreachable: srcV type %T not Array/String after pre-check", srcV)
	case *ast.Lambda:
		decl := &ast.FuncDecl{
			P:          x.P,
			Name:       "",
			Params:     x.Params,
			ReturnType: x.ReturnType,
			Body:       x.Body,
		}
		captures := x.Captures
		if x.Synthetic != nil && captures == nil {
			captures = []ast.Param{}
		}
		return &Closure{Decl: decl, Env: env.capture(captures)}, nil
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
				if typ, checked := x.CheckedType.(ast.NumberType); checked {
					return Number(narrowInt(-int64(n), resolvePtrWidth(typ.NormalWidth()), !typ.IsSigned())), nil
				}
				return -n, nil
			}
			if f, ok := v.(Float); ok {
				nv := -f.V
				if f.Width == 32 {
					nv = float64(float32(nv))
				}
				return Float{V: nv, Width: f.Width}, nil
			}
			if l, ok := v.(Lanes); ok {
				return lanesNeg(l), nil
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
		blockEnv := newEnv(env)
		defer blockEnv.releaseScope()
		for _, s := range x.Stmts {
			r, err := i.execStmt(s, blockEnv)
			if err != nil {
				return nil, err
			}
			if r.flow != flowNormal {
				return nil, &controlFlowSignal{r: r}
			}
		}
		if x.Tail == nil {
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
				if !arm.IsWildcard && arm.Payloads != nil {
					ok, err := i.armPayloadsMatch(arm.Payloads, ev, env)
					if err != nil {
						return nil, err
					}
					if !ok {
						continue
					}
				}
				armEnv := newEnv(env)
				if arm.AtBinding != "" {
					armEnv.declare(arm.AtBinding, tag)
				}
				if !arm.IsWildcard {
					bindArmPayloads(armEnv, arm.Bindings, arm.Payloads, ev)
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
		if st, ok := tag.(*Struct); ok && x.StructMatch != "" {
			for _, arm := range x.Arms {
				armEnv := newEnv(env)
				if !arm.IsWildcard {
					if arm.AtBinding != "" {
						armEnv.declare(arm.AtBinding, st)
					}
					ok, err := i.structArmMatches(arm.Bindings, arm.FieldNames, arm.Payloads, st, env)
					if err != nil {
						return nil, err
					}
					if !ok {
						continue
					}
					if err := bindStructArm(armEnv, arm.Bindings, arm.FieldNames, arm.Payloads, st); err != nil {
						return nil, err
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
		if arr, isArr := tag.(Array); isArr && matchExprArmsAllowTuple(x.Arms) {
			for _, arm := range x.Arms {
				armEnv := newEnv(env)
				if !arm.IsWildcard {
					matched, err := i.tupleElemsMatch(arm.TupleElems, arr, env)
					if err != nil {
						return nil, err
					}
					if !matched {
						continue
					}
					if arm.AtBinding != "" {
						armEnv.declare(arm.AtBinding, arr)
					}
					for k, el := range arm.TupleElems {
						bindTupleElem(armEnv, el, arr.Elems()[k])
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
		switch tag.(type) {
		case Number, Float, Bool, String:
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
			if arm.AtBinding != "" {
				armEnv.declare(arm.AtBinding, tag)
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
		return nil, fmt.Errorf("interp: match-expression non-exhaustive at runtime (no literal arm matched)")
	case *ast.TryOp:
		inner, err := i.evalExpr(x.Inner, env)
		if err != nil {
			return nil, err
		}
		ev, ok := inner.(*Enum)
		if !ok {
			return nil, fmt.Errorf("interp: `?` applied to non-enum %T", inner)
		}
		switch ev.Index {
		case 0:
			if len(ev.Payloads) != 1 {
				return nil, fmt.Errorf("interp: `?` success variant %q payload arity %d", ev.VariantName, len(ev.Payloads))
			}
			return ev.Payloads[0], nil
		case 1:
			return nil, &tryOpEarlyReturn{val: ev}
		}
		return nil, fmt.Errorf("interp: `?` on variant %q of %s (index %d): not a two-variant `?` shape", ev.VariantName, ev.EnumName, ev.Index)
	case *ast.StructLit:
		s := &Struct{TypeName: x.TypeName, Fields: map[string]Value{}}
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
				storeArray(v)
				s.Fields[k] = v
			}
		}
		for _, f := range x.Fields {
			v, err := i.evalExpr(f.Value, env)
			if err != nil {
				return nil, err
			}
			storeArray(v)
			s.Fields[f.Name] = v
		}
		return s, nil
	case *ast.TupleLit:
		out := newArray(len(x.Elems))
		for i2, e := range x.Elems {
			v, err := i.evalExpr(e, env)
			if err != nil {
				return nil, err
			}
			storeArray(v)
			out.Elems()[i2] = v
		}
		return out, nil
	case *ast.FieldAccess:
		if tid, ok := x.Target.(*ast.Ident); ok {
			if _, local := env.get(tid.Name); !local {
				if ed, idx, ok := i.findVariantOn(x.Field, tid.Name); ok {
					if len(ed.Variants[idx].Payloads) != 0 {
						return nil, fmt.Errorf("interp: variant %s.%s expects %d payload(s); call it instead",
							tid.Name, x.Field, len(ed.Variants[idx].Payloads))
					}
					return &Enum{EnumName: ed.Name, VariantName: x.Field, Index: idx}, nil
				}
			}
		}
		tv, err := i.evalExpr(x.Target, env)
		if err != nil {
			return nil, err
		}
		if arr, ok := tv.(Array); ok {
			idx, err := strconv.Atoi(x.Field)
			if err != nil {
				return nil, fmt.Errorf("tuple access requires numeric index, got %q", x.Field)
			}
			if idx < 0 || idx >= len(arr.Elems()) {
				return nil, fmt.Errorf("tuple has %d elements; index %d out of range", len(arr.Elems()), idx)
			}
			return arr.Elems()[idx], nil
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
	held := 0
	for k, a := range c.Args {
		v, err := i.evalExpr(a, env)
		if err != nil {
			disownAll(args[:held])
			return nil, err
		}
		args[k] = v
		if k+1 < len(c.Args) {
			arrOwn(v)
			held = k + 1
		}
	}
	disownAll(args[:held])
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
		mangled, ok := i.traitMethods[c.DynTrait+"."+tn+"."+fa.Field]
		if !ok {
			mangled = "__method_" + tn + "_" + fa.Field
		}
		callArgs := append([]Value{dynPayload(recv)}, args...)
		if b, ok := i.Builtins[mangled]; ok {
			return b.Fn(i, callArgs)
		}
		if fn, ok := i.Funcs[mangled]; ok {
			return i.callFunc(fn, callArgs)
		}
		return nil, fmt.Errorf("interp: no impl of %s.%s for runtime type %s", c.DynTrait, fa.Field, tn)
	}
	if id, ok := c.Callee.(*ast.Ident); ok {
		if ed, idx, ok := i.findVariantOn(id.Name, id.EnumName); ok {
			_, shadowed := env.get(id.Name)
			_, isFn := i.Funcs[id.Name]
			if c.IsVariantCall || (!shadowed && !isFn) {
				if got, want := len(args), len(ed.Variants[idx].Payloads); got != want {
					return nil, fmt.Errorf("interp: variant %s expects %d argument(s), got %d",
						id.Name, want, got)
				}
				for _, p := range args {
					storeArray(p)
				}
				return &Enum{EnumName: ed.Name, VariantName: id.Name, Index: idx, Payloads: args}, nil
			}
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
		if b, ok := i.Builtins[id.Name]; ok {
			return b.Fn(i, args)
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

func disownAll(vs []Value) {
	for _, v := range vs {
		arrDisown(v)
	}
}

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
		e.declareTyped(p.Name, args[k], p.Type)
	}
	defer e.releaseScope()
	i.envs = append(i.envs, e)
	defer func() { i.envs = i.envs[:len(i.envs)-1] }()
	i.deferStack = append(i.deferStack, nil)
	r, err := i.execBlock(c.Decl.Body, e)
	defers := i.deferStack[len(i.deferStack)-1]
	i.deferStack = i.deferStack[:len(i.deferStack)-1]
	if err != nil {
		if early, ok := err.(*tryOpEarlyReturn); ok {
			i.runDefers(defers, true)
			return early.val, nil
		}
		i.runDefers(defers, false)
		return nil, err
	}
	i.runDefers(defers, r.flow == flowReturn && i.isErrReturnValue(r.val))
	if r.flow == flowReturn {
		return r.val, nil
	}
	return Void{}, nil
}

func shiftCount(rn Number, width int) Number {
	if width == 64 || width == 0 {
		return rn & 63
	}
	return rn & 31
}

func saturateFloatToInt(f float64, width int, signed bool) int64 {
	if f != f {
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
	if t <= 0.0 {
		return 0
	}
	if width == 64 {
		if t >= 18446744073709551616.0 {
			return -1
		}
		return int64(uint64(t))
	}
	if t >= 4294967296.0 {
		return 4294967295
	}
	return int64(uint32(t))
}

var hostPtrBits = strconv.IntSize

func resolvePtrWidth(width int) int {
	if width == ast.WidthPtr {
		return hostPtrBits
	}
	return width
}

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
			switch {
			case ur >= uint64(width):
				if ul != 0 {
					res = maxU
				}
			case ul > maxU>>ur:
				res = maxU
			default:
				res = ul << ur
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
		if ir < 0 || ir >= int64(width) {
			switch {
			case il < 0:
				return Number(minS)
			case il > 0:
				return Number(maxS)
			}
			return 0
		}
		c := uint64(ir)
		if il < 0 {
			if uint64(-(il+1))+1 > (uint64(maxS)+1)>>c {
				return Number(minS)
			}
		} else if uint64(il) > uint64(maxS)>>c {
			return Number(maxS)
		}
		return Number(il << c)
	}
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
	if ls, lok := l.(String); lok {
		if rs, rok := r.(String); rok {
			switch b.Op {
			case "==":
				return Bool(ls == rs), nil
			case "!=":
				return Bool(ls != rs), nil
			case "<":
				return Bool(ls < rs), nil
			case "<=":
				return Bool(ls <= rs), nil
			case ">":
				return Bool(ls > rs), nil
			case ">=":
				return Bool(ls >= rs), nil
			}
		}
	}
	ln, lOk := l.(Number)
	rn, rOk := r.(Number)
	if lOk && rOk {
		width := resolvePtrWidth(b.IntWidth)
		mask := func(v Number) Number {
			switch width {
			case 8:
				return Number(uint8(int64(v)))
			case 32:
				return Number(uint32(int64(v)))
			case 64:
				return v
			}
			return v
		}
		signExtend := func(v Number) Number {
			switch width {
			case 8:
				return Number(uint8(int64(v)))
			case 32:
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
			if rn == 0 {
				return signExtend(0), nil
			}
			if b.IsUnsigned {
				return signExtend(Number(uint64(ln) / uint64(rn))), nil
			}
			return signExtend(signExtend(ln) / signExtend(rn)), nil
		case "%":
			if rn == 0 {
				return signExtend(ln), nil
			}
			if b.IsUnsigned {
				return signExtend(Number(uint64(ln) % uint64(rn))), nil
			}
			return signExtend(signExtend(ln) % signExtend(rn)), nil
		case "+|", "-|", "*|", "<<|":
			return signExtend(satArith(b.Op, ln, rn, width, b.IsUnsigned, signExtend)), nil
		case "&":
			return signExtend(ln & rn), nil
		case "|":
			return signExtend(ln | rn), nil
		case "^":
			return signExtend(ln ^ rn), nil
		case "<<":
			return signExtend(ln << shiftCount(rn, width)), nil
		case ">>":
			sc := shiftCount(rn, width)
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
	if ll, ok := l.(Lanes); ok {
		if rl, ok := r.(Lanes); ok {
			return lanesBinary(b.Op, ll, rl)
		}
	}
	if lf, ok := l.(Float); ok {
		if rf, ok := r.(Float); ok {
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
	if t, ok := a.Target.(*ast.Ident); ok {
		return i.assignIdent(a, t.Name, env)
	}
	v, err := i.evalExpr(a.Value, env)
	if err != nil {
		return nil, err
	}
	switch t := a.Target.(type) {
	case *ast.FieldAccess:
		tv, err := i.evalExpr(t.Target, env)
		if err != nil {
			return nil, err
		}
		s, ok := tv.(*Struct)
		if !ok {
			return nil, fmt.Errorf("field assignment on non-struct %T", tv)
		}
		retainReplacing(v, s.Fields[t.Field])
		storeArray(v)
		s.Fields[t.Field] = v
		return v, nil
	}
	return nil, fmt.Errorf("interp: invalid assignment target %T", a.Target)
}

func (i *Interp) assignIdent(a *ast.Assign, name string, env *env) (Value, error) {
	old, _ := env.get(name)
	move := movesFirst(a.Value, name)
	if move {
		arrDisown(old)
		i.moving = append(i.moving, old)
	}
	v, err := i.evalExpr(a.Value, env)
	if move {
		i.moving = i.moving[:len(i.moving)-1]
	}
	if err != nil {
		if move {
			arrOwn(old)
		}
		return nil, err
	}
	retainReplacing(v, old)
	arrOwn(v)
	env.set(name, v)
	return v, nil
}

func movesFirst(rhs ast.Expr, name string) bool {
	reads := 0
	ast.Walk(rhs, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			reads++
		}
		return true
	})
	if reads <= 1 {
		return true
	}
	e := rhs
	for {
		if fa, ok := e.(*ast.FieldAccess); ok {
			e = fa.Target
		}
		c, ok := e.(*ast.Call)
		if !ok || !isUpdateCall(c) {
			return false
		}
		if id, ok := c.Args[0].(*ast.Ident); ok && id.Name == name {
			return true
		}
		for _, arg := range c.Args[1:] {
			mentioned := false
			ast.Walk(arg, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name == name {
					mentioned = true
				}
				return !mentioned
			})
			if mentioned {
				return false
			}
		}
		e = c.Args[0]
	}
}

func isUpdateCall(c *ast.Call) bool {
	id, ok := c.Callee.(*ast.Ident)
	if !ok || len(c.Args) == 0 {
		return false
	}
	switch id.Name {
	case "__method_Array_set", "__method_Map_set", "__method_Map_remove", "__method_Map_clear":
		return true
	}
	return false
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

func scanSet(name string, b []byte, fromArg, setArg Value) (Value, error) {
	fn, ok := fromArg.(Number)
	if !ok {
		return nil, fmt.Errorf("%s: expected an integer start, got %T", name, fromArg)
	}
	set, ok := setArg.(Array)
	if !ok {
		return nil, fmt.Errorf("%s: expected a u8[] set, got %T", name, setArg)
	}
	from := max(int(int64(fn)), 0)
	for idx := from; idx < len(b); idx++ {
		c := int(b[idx])
		if c >= len(set.Elems()) {
			continue
		}
		e, ok := set.Elems()[c].(Number)
		if !ok {
			return nil, fmt.Errorf("%s: set element %d is %T, not a byte", name, c, set.Elems()[c])
		}
		if int64(e) != 0 {
			return Number(idx), nil
		}
	}
	return Number(len(b)), nil
}

func StrHash(s []byte, seed int32) uint32 {
	const prime = 1099511628211
	h := uint64(0xcbf29ce484222325) ^ uint64(int64(seed)) ^ uint64(len(s))
	word := func(b []byte) uint64 {
		var w uint64
		for k := len(b) - 1; k >= 0; k-- {
			w = w<<8 | uint64(b[k])
		}
		return w
	}
	i := 0
	for ; i+8 <= len(s); i += 8 {
		h = (h ^ word(s[i:i+8])) * prime
	}
	if i < len(s) {
		if len(s) >= 8 {
			h = (h ^ word(s[len(s)-8:])) * prime
		} else {
			h = (h ^ word(s)) * prime
		}
	}
	h ^= h >> 32
	return uint32(h)
}
