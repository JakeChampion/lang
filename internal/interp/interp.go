// Package interp is a small tree-walking interpreter for the lang AST.
//
// It's used by the REPL (cmd/lang -repl) and by tests; production
// builds still go through the ARM32 / WASM code generators.
//
// Control flow inside a function uses a flow-tagged result value
// rather than panics: each statement returns a stmtResult whose Flow
// field tells the surrounding loop / block whether to keep going,
// break, continue, or unwind to the enclosing call site.
package interp

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
)

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
	i.Builtins["read_line"] = &Builtin{Fn: builtinReadLine}
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
	i.Builtins["exit"] = &Builtin{Fn: builtinExit}
	return i
}

// builtinReadLine reads bytes from i.Stdin one at a time until it
// hits a newline or EOF. The trailing `\n` is included in the
// returned string when present, so callers can disambiguate
// "blank line" (returns "\n") from EOF (returns "").
func builtinReadLine(i *Interp, args []Value) (Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("read_line: expected 0 args, got %d", len(args))
	}
	if i.Stdin == nil {
		return &Enum{EnumName: "Option", VariantName: "None", Index: 1}, nil
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
			// EOF: empty buffer means we never read a byte, so
			// signal `None`. A non-empty buffer at EOF mid-line
			// returns `Some(line)` so the caller still sees the
			// partial input.
			if len(buf) == 0 {
				return optionNone(), nil
			}
			return optionSome(String(string(buf))), nil
		}
	}
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

// readerFile / writerFile pull the *os.File back out of an
// interpreter Struct via the openFiles ticket map. Methods
// validate the receiver before calling read / write.
func readerFile(i *Interp, v Value) (*os.File, error) {
	s, ok := v.(*Struct)
	if !ok {
		return nil, fmt.Errorf("expected Reader/Writer struct, got %T", v)
	}
	fd, ok := s.Fields["fd"].(Number)
	if !ok {
		return nil, fmt.Errorf("Reader/Writer.fd not a number")
	}
	f, ok := i.openFiles[int64(fd)]
	if !ok {
		return nil, fmt.Errorf("Reader/Writer with fd=%d not registered (closed already?)", fd)
	}
	return f, nil
}

func builtinReaderReadLine(i *Interp, args []Value) (Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("Reader.read_line: expected 1 arg")
	}
	f, err := readerFile(i, args[0])
	if err != nil {
		return nil, err
	}
	var buf []byte
	one := make([]byte, 1)
	for {
		n, err := f.Read(one)
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
	f, err := readerFile(i, args[0])
	if err != nil {
		return nil, err
	}
	size, ok := args[1].(Number)
	if !ok {
		return nil, fmt.Errorf("Reader.read_chunk: size must be a number")
	}
	buf := make([]byte, int(size))
	n, _ := f.Read(buf)
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
	f, err := readerFile(i, args[0])
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return optionSome(classifyIoError("", err)), nil
	}
	if s, ok := args[0].(*Struct); ok {
		if fd, ok := s.Fields["fd"].(Number); ok {
			delete(i.openFiles, int64(fd))
		}
	}
	return optionNone(), nil
}

func builtinWriterWrite(i *Interp, args []Value) (Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("Writer.write: expected 2 args")
	}
	f, err := readerFile(i, args[0])
	if err != nil {
		return nil, err
	}
	s, ok := args[1].(String)
	if !ok {
		return nil, fmt.Errorf("Writer.write: content must be a string")
	}
	if _, err := f.Write([]byte(s)); err != nil {
		return optionSome(classifyIoError("", err)), nil
	}
	return optionNone(), nil
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
	case *ast.BoolLit:
		return Bool(x.Value), nil
	case *ast.StringLit:
		return String(x.Value), nil
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
	case *ast.Ternary:
		c, err := i.evalExpr(x.Cond, env)
		if err != nil {
			return nil, err
		}
		b, ok := c.(Bool)
		if !ok {
			return nil, fmt.Errorf("interp: ternary condition is not a bool: %T", c)
		}
		if bool(b) {
			return i.evalExpr(x.Then, env)
		}
		return i.evalExpr(x.Else, env)
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
	case *ast.FieldAccess:
		tv, err := i.evalExpr(x.Target, env)
		if err != nil {
			return nil, err
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
		switch b.Op {
		case "+":
			return ln + rn, nil
		case "-":
			return ln - rn, nil
		case "*":
			return ln * rn, nil
		case "/":
			if rn == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return ln / rn, nil
		case "%":
			if rn == 0 {
				return nil, fmt.Errorf("modulo by zero")
			}
			return ln % rn, nil
		case "&":
			return ln & rn, nil
		case "|":
			return ln | rn, nil
		case "^":
			return ln ^ rn, nil
		case "<<":
			return ln << rn, nil
		case ">>":
			return ln >> rn, nil
		case "==":
			return Bool(ln == rn), nil
		case "!=":
			return Bool(ln != rn), nil
		case "<":
			return Bool(ln < rn), nil
		case "<=":
			return Bool(ln <= rn), nil
		case ">":
			return Bool(ln > rn), nil
		case ">=":
			return Bool(ln >= rn), nil
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
