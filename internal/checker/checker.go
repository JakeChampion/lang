// Package checker performs name-resolution and type-checking on a Program.
//
// Each function is checked against an environment chain that starts at the
// top-level (functions) and is extended for parameters and `var`
// declarations. Errors are accumulated rather than fatally aborting on the
// first one, so a single run reports as much as possible.
package checker

import (
	"fmt"
	"strconv"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
)

type Error struct {
	Pos  ast.Position
	Span int    // optional: token length for `^~~~~` underline; 0 = caret only
	Note string // optional: "did you mean foo?" hint
	Msg  string
}

func (e *Error) Error() string          { return fmt.Sprintf("type error at %s: %s", e.Pos, e.Msg) }
func (e *Error) Position() ast.Position { return e.Pos }
func (e *Error) Length() int            { return e.Span }
func (e *Error) Hint() string           { return e.Note }

// Info captures everything codegen needs that the checker discovered:
// the inferred type of every var without an annotation, and a per-function
// list of locals (so codegen can lay out a frame).
type Info struct {
	VarTypes map[*ast.Var]ast.Type
	Locals   map[*ast.FuncDecl][]*ast.Var
	FuncSigs map[string]*ast.FuncType
	// Structs maps a struct name to its declaration (which carries the
	// ordered field list — codegen looks up field offsets here).
	Structs map[string]*ast.StructDecl
	// Enums maps an enum name to its declaration. The variant list +
	// payload types live there; codegen looks up the runtime tag
	// (the variant's index in the variant slice) and the payload
	// shapes via this map.
	Enums map[string]*ast.EnumDecl
	// Methods maps `<StructName>.<MethodName>` to the mangled
	// top-level function name the receiver-rewriting pass introduces
	// (`__method_<StructName>_<MethodName>`). Call-site rewriting
	// uses this map to turn `p.area()` into `__method_Point_area(p)`.
	Methods map[string]string
	// VariantCallPayloads is keyed by every variant-construction
	// Call (`Some(42)`, `Ok(v)`, …) with the variant's payload
	// types AFTER the checker has substituted any type-parameter
	// references with the concrete instantiation. Codegen uses
	// this so a payload declared as `T` inside `Option[T]`
	// resolves to `float` at the construction of `Some(3.14)`,
	// letting the IR pick `OpFStore` instead of `OpStore`.
	VariantCallPayloads map[*ast.Call][]ast.Type
	// GenericFuncs maps a generic function name to its declaration.
	// Populated at the start of Check; used by the call-site
	// inference path to detect "this is a generic call" and to
	// look up TypeParams. The monomorphisation pass also reads
	// this when cloning. Empty for programs without generic
	// functions.
	GenericFuncs map[string]*ast.FuncDecl
	// GenericStructs is the StructDecl analogue of GenericFuncs —
	// tracks struct decls with non-empty TypeParams for the
	// monomorphisation pass.
	GenericStructs map[string]*ast.StructDecl
}

// builtinEnumDecls returns the synthetic enum declarations the
// checker injects into every program: `Option[T]` and
// `Result[T, E]`. Variant order matters — runtime helpers
// (`$read_line`, `$env`) hardcode the tag indices, so `Some` is
// 0, `None` is 1, `Ok` is 0, `Err` is 1.
//
// Users can't shadow these names; trying to declare an `enum
// Option { … }` in user code triggers the existing redeclared-
// enum error. The decls live in the AST, not just the checker
// info, so the formatter / interpreter / IR layers see them
// just like user-written enums.
func builtinEnumDecls() []*ast.EnumDecl {
	return []*ast.EnumDecl{
		{
			Name:       "Option",
			TypeParams: []string{"T"},
			Variants: []ast.EnumVariant{
				{Name: "Some", Payloads: []ast.Type{ast.ParamType{Name: "T"}}},
				{Name: "None"},
			},
		},
		{
			Name:       "Result",
			TypeParams: []string{"T", "E"},
			Variants: []ast.EnumVariant{
				{Name: "Ok", Payloads: []ast.Type{ast.ParamType{Name: "T"}}},
				{Name: "Err", Payloads: []ast.Type{ast.ParamType{Name: "E"}}},
			},
		},
		{
			// Roc-shaped error variants: a small set of named
			// kinds plus a generic Other(path, message) catch-
			// all carrying the offending path and the OS errno
			// text. Variants that always have a path attached
			// keep the API uniform — callers pattern-match on
			// the kind and never have to wrap calls just to add
			// "(while reading X)" context.
			Name: "IoError",
			Variants: []ast.EnumVariant{
				{Name: "NotFound", Payloads: []ast.Type{ast.StringType{}}},
				{Name: "PermissionDenied", Payloads: []ast.Type{ast.StringType{}}},
				{Name: "AlreadyExists", Payloads: []ast.Type{ast.StringType{}}},
				{Name: "InvalidUtf8", Payloads: []ast.Type{ast.StringType{}}},
				{Name: "Interrupted"},
				{Name: "Unsupported"},
				{Name: "Other", Payloads: []ast.Type{ast.StringType{}, ast.StringType{}}},
			},
		},
	}
}

// builtinStructDecls returns the synthetic struct declarations
// the checker injects on every program: `Reader` and `Writer`.
// Both are opaque-by-convention — the user never constructs
// them directly; `open_reader` / `open_writer` / `open_appender`
// (and the future `stdin()` / `stdout()` / `stderr()`) are the
// canonical entry points. The single `fd` field is exposed
// because we don't have opaque types yet, and because users
// may need it for FFI escape hatches; it isn't part of the
// stable surface.
func builtinStructDecls() []*ast.StructDecl {
	return []*ast.StructDecl{
		{
			Name:   "Reader",
			Fields: []ast.Param{{Name: "fd", Type: ast.NumberType{}}},
		},
		{
			Name:   "Writer",
			Fields: []ast.Param{{Name: "fd", Type: ast.NumberType{}}},
		},
		// HttpRequest / HttpResponse back the
		// `lang -target wasi-http` mode (step 5 of
		// docs/WASI-PREVIEW2.md). They're always available so
		// CLI-target programs can construct one for tests, but
		// the wasm backend only emits the
		// `wasi:http/incoming-handler.handle` wrapper +
		// imports under `EmitOptions.HttpHandler`. Keep these
		// fields minimal for now — query params, headers, and
		// trailers are deferred follow-ups.
		{
			Name: "HttpRequest",
			Fields: []ast.Param{
				{Name: "method", Type: ast.StringType{}},
				{Name: "path", Type: ast.StringType{}},
				{Name: "body", Type: ast.StringType{}},
			},
		},
		{
			Name: "HttpResponse",
			Fields: []ast.Param{
				{Name: "status", Type: ast.NumberType{}},
				{Name: "body", Type: ast.StringType{}},
			},
		},
	}
}

// Check type-checks the program. It returns an aggregated error if any
// problems were found.
func Check(prog *ast.Program) (*Info, error) {
	// Prepend the built-in Option / Result decls so user code
	// can reference them without an explicit declaration. Doing
	// this once at check-time keeps the AST `prog.Enums` slice
	// authoritative — subsequent passes (IR, codegen, formatter)
	// see the same shape.
	if len(prog.Enums) == 0 || prog.Enums[0].Name != "Option" {
		prog.Enums = append(builtinEnumDecls(), prog.Enums...)
	}
	// Same shape for the auto-injected Reader / Writer structs.
	if len(prog.Structs) == 0 || prog.Structs[0].Name != "Reader" {
		prog.Structs = append(builtinStructDecls(), prog.Structs...)
	}
	c := &checker{
		info: &Info{
			VarTypes:            map[*ast.Var]ast.Type{},
			Locals:              map[*ast.FuncDecl][]*ast.Var{},
			FuncSigs:            map[string]*ast.FuncType{},
			Structs:             map[string]*ast.StructDecl{},
			Enums:               map[string]*ast.EnumDecl{},
			Methods:             map[string]string{},
			VariantCallPayloads: map[*ast.Call][]ast.Type{},
			GenericFuncs:        map[string]*ast.FuncDecl{},
			GenericStructs:      map[string]*ast.StructDecl{},
		},
		variantOf: map[string]variantRef{},
	}

	// Register every struct declaration up front so that types
	// referenced by name (`function f(p: Point)`) resolve when we
	// check function signatures below.
	for _, sd := range prog.Structs {
		if _, dup := c.info.Structs[sd.Name]; dup {
			c.errf(sd.P, "struct %q redeclared", sd.Name)
			continue
		}
		seen := map[string]bool{}
		for _, f := range sd.Fields {
			if seen[f.Name] {
				c.errf(sd.P, "duplicate field %q in struct %s", f.Name, sd.Name)
			}
			seen[f.Name] = true
		}
		c.info.Structs[sd.Name] = sd
		if len(sd.TypeParams) > 0 {
			// Track generic struct decls so the monomorph pass
			// knows which ones to clone, and post-monomorph
			// callers can tell "we used to be generic".
			c.info.GenericStructs[sd.Name] = sd
		}
	}

	// Register every enum declaration. Variant names are recorded
	// in variantOf so an unqualified `Some(x)` or `Red` can be
	// rewritten into a typed *EnumLit during expression checking.
	// A variant name shared across two enums is ambiguous; we
	// drop the entry so subsequent uses surface a clear error.
	for _, ed := range prog.Enums {
		if _, dup := c.info.Enums[ed.Name]; dup {
			c.errf(ed.P, "enum %q redeclared", ed.Name)
			continue
		}
		c.info.Enums[ed.Name] = ed
		seen := map[string]bool{}
		for i := range ed.Variants {
			v := &ed.Variants[i]
			if seen[v.Name] {
				c.errf(v.P, "duplicate variant %q in enum %s", v.Name, ed.Name)
				continue
			}
			seen[v.Name] = true
			if existing, dup := c.variantOf[v.Name]; dup {
				c.errf(v.P, "variant %q is declared in both %s and %s — qualify references with the enum name (not yet supported, rename one)",
					v.Name, existing.enumName, ed.Name)
				delete(c.variantOf, v.Name)
				continue
			}
			c.variantOf[v.Name] = variantRef{
				enumName: ed.Name,
				index:    i,
				payloads: v.Payloads,
			}
		}
	}

	// Now that the enum set is known, walk every type position in
	// the program and rewrite StructType{Name: X} → EnumType when
	// X resolves to an enum. The parser doesn't know which named
	// types are structs vs. enums; we lazily disambiguate here.
	c.resolveTypeNames(prog)

	// Pre-declare built-ins so user code can call them.
	c.info.FuncSigs["putchar"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.VoidType{},
	}
	// print(s: string): void — appends a newline (lowers to libc puts).
	c.info.FuncSigs["print"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.VoidType{},
	}
	// write(s: string): void — stdout without a trailing newline.
	// Use this when you want to format your own output (status
	// lines, prompts, custom delimiters) instead of one line per
	// call. Pairs with `print` the way Go's `fmt.Print` /
	// `fmt.Println` do.
	c.info.FuncSigs["write"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.VoidType{},
	}
	// eprint(s: string): void — `print` shape but routed to stderr.
	// Useful for error / diagnostic output that shouldn't get
	// mixed in with stdout when the program is being piped to
	// another tool.
	c.info.FuncSigs["eprint"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.VoidType{},
	}
	// args(): string[] — returns the program's command-line argv as a
	// length-prefixed string array. The first element is conventionally
	// the program / module path (matching argv[0] in C and os.Args[0]
	// in Go). Building the array is one-shot and cached: the first
	// `args()` call materialises it from libc / WASI; subsequent calls
	// hand back the same pointer.
	c.info.FuncSigs["args"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.ArrayType{Elem: ast.StringType{}},
	}
	// env(name: string): Option[string] — looks up an environment
	// variable. `Some(value)` for a present key, `None` for
	// missing. (POSIX distinguishes "set to empty" from "not
	// set"; the runtime helper preserves that — `Some("")` is
	// returned for an explicitly empty value.)
	c.info.FuncSigs["env"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Option", Args: []ast.Type{ast.StringType{}}},
	}
	// exit(code: number): void — exits the process immediately with
	// the given status code. Useful for `eprint(msg); exit(2)`-style
	// error paths; the success path can just `return` from main.
	c.info.FuncSigs["exit"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.VoidType{},
	}
	// arena_save(): number — snapshots the bump allocator's
	// current cursor. Pair with arena_restore to free everything
	// allocated in between in O(1). Designed for long-lived
	// servers that want to drop per-request allocations between
	// requests; not safe to retain pointers across the matching
	// arena_restore call.
	c.info.FuncSigs["arena_save"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
	}
	// arena_restore(handle): void — rewinds the bump allocator
	// cursor to the value returned by an earlier arena_save.
	// Anything allocated since that save is reclaimed in one
	// pointer-store; pointers into that region are no longer
	// valid (no compile-time enforcement, just discipline).
	c.info.FuncSigs["arena_restore"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.VoidType{},
	}
	// random_bytes(n: number): string — returns a fresh string
	// of n cryptographic-quality random bytes from the
	// kernel's CSPRNG (`getrandom(2)` on Linux,
	// `wasi_snapshot_preview1.random_get` on WASM). Useful
	// for session IDs, request IDs, nonce generation, etc.
	// The string has no encoding — it's raw bytes — so
	// `s[i]` returns a number 0..255.
	c.info.FuncSigs["random_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.StringType{},
	}
	// int_to_string(n: number): string — formats n as an ASCII
	// decimal string ("42", "-1", "0"). Mostly useful in tests
	// and small CLIs since the language doesn't have a printf
	// equivalent yet; pairing it with `print` gives "println(n)"
	// for free.
	c.info.FuncSigs["int_to_string"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.StringType{},
	}
	// TCP socket builtins. C-style API: each returns a raw
	// fd or a negative errno. A Result-wrapped layer can sit
	// on top in a follow-up.
	//
	// On WASI the host pre-opens the listening socket
	// (`wasmtime --tcp-listen=0.0.0.0:PORT prog.wasm`); the
	// `port` argument is currently ignored and the helper
	// returns the first preopened socket fd (typically 3).
	// On Linux/arm32 the helper opens the socket itself.
	c.info.FuncSigs["tcp_listen"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_accept"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_recv"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		Result: ast.StringType{},
	}
	c.info.FuncSigs["tcp_send"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.StringType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["tcp_close"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	// read_file(path): Result[string, IoError] — reads the entire
	// file into a single string. WASM builds need a preopen
	// directory (e.g. `wasmtime --dir=.`); the path is
	// interpreted relative to the first preopen.
	c.info.FuncSigs["read_file"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StringType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	// write_file(path, content): Option[IoError] — writes the
	// content to the named file, truncating it first. Returns
	// `None` on success and `Some(err)` on failure. We use
	// Option[IoError] rather than Result[void, IoError] because
	// the language doesn't have a usable unit type yet — None /
	// Some(err) pattern reads naturally and matches the Go-style
	// "error or nil" shape.
	c.info.FuncSigs["write_file"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.StringType{}},
		Result: ast.EnumType{Name: "Option", Args: []ast.Type{
			ast.EnumType{Name: "IoError"},
		}},
	}
	// Streaming I/O constructors. open_reader / open_writer /
	// open_appender all return `Result[Reader|Writer, IoError]`
	// — the runtime helpers do the path_open / open(2) and
	// wrap the resulting fd in a Reader or Writer struct.
	readerType := ast.StructType{Name: "Reader"}
	writerType := ast.StructType{Name: "Writer"}
	ioErrType := ast.EnumType{Name: "IoError"}
	optionIoErr := ast.EnumType{Name: "Option", Args: []ast.Type{ioErrType}}
	c.info.FuncSigs["open_reader"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{readerType, ioErrType}},
	}
	c.info.FuncSigs["open_writer"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{writerType, ioErrType}},
	}
	c.info.FuncSigs["open_appender"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{writerType, ioErrType}},
	}
	// stdin / stdout / stderr return Reader / Writer values
	// wrapping the standard fds (0 / 1 / 2). They never fail
	// — fd 0/1/2 are always present in a POSIX / WASI program
	// — so the return is bare Reader / Writer rather than
	// Result. Calling them is conceptually free: the runtime
	// just allocates a 4-byte struct with the right fd.
	c.info.FuncSigs["stdin"] = &ast.FuncType{Params: []ast.Type{}, Result: readerType}
	c.info.FuncSigs["stdout"] = &ast.FuncType{Params: []ast.Type{}, Result: writerType}
	c.info.FuncSigs["stderr"] = &ast.FuncType{Params: []ast.Type{}, Result: writerType}
	// Auto-injected methods on Reader / Writer. The names are
	// the mangled forms the existing method-call rewrite uses
	// (`r.read_line()` → `__method_Reader_read_line(r)`); we
	// pre-populate Methods + FuncSigs so the rewrite finds them
	// and so codegen can resolve the call to a runtime helper
	// emitted at the same name.
	registerMethod := func(structName, methodName string, params []ast.Type, result ast.Type) {
		mangled := "__method_" + structName + "_" + methodName
		c.info.Methods[structName+"."+methodName] = mangled
		// First param is the receiver (the auto-injected struct).
		fullParams := append([]ast.Type{ast.StructType{Name: structName}}, params...)
		c.info.FuncSigs[mangled] = &ast.FuncType{Params: fullParams, Result: result}
	}
	optionString := ast.EnumType{Name: "Option", Args: []ast.Type{ast.StringType{}}}
	registerMethod("Reader", "read_line", nil, optionString)
	registerMethod("Reader", "read_chunk", []ast.Type{ast.NumberType{}}, optionString)
	registerMethod("Reader", "close", nil, optionIoErr)
	registerMethod("Writer", "write", []ast.Type{ast.StringType{}}, optionIoErr)
	registerMethod("Writer", "close", nil, optionIoErr)

	// First pass: gather all top-level signatures so functions can call
	// each other in any order. Methods are hoisted to mangled
	// top-level names (`__method_<Type>_<Name>`) with the receiver
	// prepended to the parameter list, so codegen never has to know
	// about methods.
	for _, fn := range prog.Funcs {
		if fn.Receiver != nil {
			st, ok := fn.Receiver.Type.(ast.StructType)
			if !ok {
				c.errf(fn.P, "method receiver type must be a struct, got %s", fn.Receiver.Type)
				continue
			}
			if _, ok := c.info.Structs[st.Name]; !ok {
				c.errf(fn.P, "method receiver references unknown struct %q", st.Name)
				continue
			}
			methodKey := st.Name + "." + fn.Name
			if _, dup := c.info.Methods[methodKey]; dup {
				c.errf(fn.P, "method %q on struct %s redeclared", fn.Name, st.Name)
				continue
			}
			mangled := "__method_" + st.Name + "_" + fn.Name
			// Rewrite the FuncDecl so codegen sees a regular
			// top-level function with the receiver as its first
			// parameter.
			fn.Name = mangled
			fn.Params = append([]ast.Param{*fn.Receiver}, fn.Params...)
			fn.Receiver = nil
			c.info.Methods[methodKey] = mangled
		}
		if _, dup := c.info.FuncSigs[fn.Name]; dup {
			c.errf(fn.P, "function %q redeclared", fn.Name)
			continue
		}
		params := make([]ast.Type, len(fn.Params))
		for i, p := range fn.Params {
			params[i] = p.Type
		}
		c.info.FuncSigs[fn.Name] = &ast.FuncType{Params: params, Result: fn.ReturnType}
		if len(fn.TypeParams) > 0 {
			// Track generic decls so the call-site inference path
			// can spot them and the monomorphisation pass knows
			// which functions to clone.
			c.info.GenericFuncs[fn.Name] = fn
		}
	}

	// Second pass: check bodies.
	for _, fn := range prog.Funcs {
		c.checkFunction(fn)
	}

	if len(c.errors) > 0 {
		return c.info, diag.Errors(c.errors)
	}
	return c.info, nil
}

type checker struct {
	info        *Info
	errors      []error
	current     *ast.FuncDecl
	loopDepth   int
	switchDepth int

	// variantOf maps a variant's bare name (`Some`, `Err`) to the
	// enum that owns it plus its index. Built during the enum
	// registration pass; ambiguous names (same variant in two
	// enums) generate an error and the entry is left as zero-value
	// so subsequent uses report cleanly.
	variantOf map[string]variantRef

	// Closure-capture plumbing. While checking a local function body,
	// captureSink records each outer-scope name read by the body as
	// a capture; captureOuter is the scope of the immediately
	// enclosing function so we can look those names up. Both are nil
	// outside a local function.
	captureSink  func(name string, t ast.Type)
	captureOuter *scope
}

// resolveTypeNames walks every named-type position the parser may
// have stamped with `StructType{Name: X}` and rewrites it to
// `EnumType{Name: X}` when X turns out to be an enum. The parser
// can't distinguish structs from enums by name alone, so this
// pass runs after the enum decls have been collected. Function
// signatures, parameters, var decls, struct fields, array
// element types, and function-type fields are all visited.
//
// Inside an enum decl with type parameters, payload type
// references that match a parameter name (`T`, `U`) get
// rewritten to ParamType instead of StructType / EnumType. The
// parser can't tell them apart at decl time — both look like
// bare identifiers — so we disambiguate here against the enum's
// declared TypeParams set.
func (c *checker) resolveTypeNames(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		// Collect the function's type parameters so occurrences
		// of those names in the signature / body resolve to
		// ParamType rather than dangling StructType references.
		// Empty for non-generic functions.
		var params map[string]bool
		if len(fn.TypeParams) > 0 {
			params = make(map[string]bool, len(fn.TypeParams))
			for _, n := range fn.TypeParams {
				params[n] = true
			}
		}
		if fn.Receiver != nil {
			c.resolveType(&fn.Receiver.Type, params)
		}
		for i := range fn.Params {
			c.resolveType(&fn.Params[i].Type, params)
		}
		c.resolveType(&fn.ReturnType, params)
		c.resolveTypesInBlock(fn.Body)
	}
	for _, sd := range prog.Structs {
		// Same as functions / enums: register type params so
		// occurrences in field types resolve to ParamType
		// instead of dangling StructType references.
		var params map[string]bool
		if len(sd.TypeParams) > 0 {
			params = make(map[string]bool, len(sd.TypeParams))
			for _, n := range sd.TypeParams {
				params[n] = true
			}
		}
		for i := range sd.Fields {
			c.resolveType(&sd.Fields[i].Type, params)
		}
	}
	for _, ed := range prog.Enums {
		var params map[string]bool
		if len(ed.TypeParams) > 0 {
			params = make(map[string]bool, len(ed.TypeParams))
			for _, n := range ed.TypeParams {
				params[n] = true
			}
		}
		for i := range ed.Variants {
			for j := range ed.Variants[i].Payloads {
				c.resolveType(&ed.Variants[i].Payloads[j], params)
			}
		}
	}
}

func (c *checker) resolveTypesInBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		switch x := st.(type) {
		case *ast.Block:
			c.resolveTypesInBlock(x)
		case *ast.If:
			c.resolveTypesInBlock(asBlock(x.Then))
			c.resolveTypesInBlock(asBlock(x.Else))
		case *ast.IfLet:
			c.resolveTypesInBlock(asBlock(x.Then))
			c.resolveTypesInBlock(asBlock(x.Else))
		case *ast.LetElse:
			c.resolveTypesInBlock(x.Else)
		case *ast.While:
			c.resolveTypesInBlock(asBlock(x.Body))
		case *ast.For:
			c.resolveTypesInBlock(asBlock(x.Body))
		case *ast.Var:
			c.resolveType(&x.Type, nil)
		case *ast.Switch:
			for _, k := range x.Cases {
				c.resolveTypesInBlock(k.Body)
			}
			c.resolveTypesInBlock(x.Default)
		case *ast.Match:
			for _, arm := range x.Arms {
				c.resolveTypesInBlock(arm.Body)
			}
		case *ast.FuncDecl:
			for i := range x.Params {
				c.resolveType(&x.Params[i].Type, nil)
			}
			c.resolveType(&x.ReturnType, nil)
			c.resolveTypesInBlock(x.Body)
		}
	}
}

// asBlock turns a Stmt into a *Block where possible — used to
// reuse resolveTypesInBlock for the if/while/for body slots,
// which are typed as Stmt but in practice always Block. Returns
// nil otherwise so the caller skips.
func asBlock(s ast.Stmt) *ast.Block {
	if b, ok := s.(*ast.Block); ok {
		return b
	}
	return nil
}

// blockDiverges reports whether every control-flow path
// through `b` exits via Return / Break / Continue. Used by
// `let else` to enforce the divergent-else contract — without
// it, fall-through would leave the pattern's bindings
// uninitialised in the surrounding scope. Conservative: a
// block whose last statement is itself a divergent statement
// counts; nested ifs / matches must have ALL arms divergent.
func blockDiverges(b *ast.Block) bool {
	if b == nil || len(b.Stmts) == 0 {
		return false
	}
	return stmtDiverges(b.Stmts[len(b.Stmts)-1])
}

func stmtDiverges(s ast.Stmt) bool {
	switch x := s.(type) {
	case *ast.Return, *ast.Break, *ast.Continue:
		return true
	case *ast.Block:
		return blockDiverges(x)
	case *ast.If:
		// Both arms must diverge (and the else arm must
		// exist) — a one-armed if can fall through.
		if x.Else == nil {
			return false
		}
		return stmtDiverges(x.Then) && stmtDiverges(x.Else)
	case *ast.IfLet:
		if x.Else == nil {
			return false
		}
		return stmtDiverges(x.Then) && stmtDiverges(x.Else)
	case *ast.Match:
		// Every arm must diverge for the match itself to
		// diverge. Wildcard arm is required for the match to
		// be exhaustive at this point (the checker has
		// already verified that), so we don't need a separate
		// "did we see a wildcard" branch.
		for _, arm := range x.Arms {
			if !blockDiverges(arm.Body) {
				return false
			}
		}
		return len(x.Arms) > 0
	}
	return false
}

// resolveType rewrites a single Type slot in place. Handles
// nominal references (StructType promoted to EnumType when
// appropriate, or ParamType when the name matches an enclosing
// enum's type parameter) plus recurses into composite types.
//
// `params` carries the type-parameter names visible at this
// type position. It's nil outside of enum-body contexts. When
// the name is in `params`, we always rewrite to ParamType —
// the parameter wins over a same-named enum or struct.
func (c *checker) resolveType(slot *ast.Type, params map[string]bool) {
	if slot == nil || *slot == nil {
		return
	}
	switch t := (*slot).(type) {
	case ast.StructType:
		if params[t.Name] {
			*slot = ast.ParamType{Name: t.Name}
			return
		}
		if _, isEnum := c.info.Enums[t.Name]; isEnum {
			*slot = ast.EnumType{Name: t.Name}
			return
		}
		// Already a StructType — recurse into Args (populated
		// when the type came back through resolveType for a
		// generic struct's instantiation).
		if len(t.Args) > 0 {
			args := make([]ast.Type, len(t.Args))
			copy(args, t.Args)
			for i := range args {
				c.resolveType(&args[i], params)
			}
			*slot = ast.StructType{Name: t.Name, Args: args}
		}
	case ast.EnumType:
		// `Foo[A, B]` — recurse into args. Params can shadow
		// individual arg names too (e.g. `Option[T]` inside an
		// enum body where T is a parameter). We also enforce the
		// arity here so a wrong-arity instantiation fails before
		// it becomes a "no Args" enum at the assignment site.
		//
		// The parser optimistically wraps every `Name[…]` form as
		// EnumType because at parse time it doesn't know which
		// names are structs vs enums. If the name resolves to a
		// generic struct we rewrite into a StructType with the
		// same Args here. Same arity check.
		args := make([]ast.Type, len(t.Args))
		copy(args, t.Args)
		for i := range args {
			c.resolveType(&args[i], params)
		}
		if sd, ok := c.info.Structs[t.Name]; ok {
			if len(sd.TypeParams) != len(args) {
				c.errf(sd.P, "struct %s has %d type parameter(s), %d supplied",
					t.Name, len(sd.TypeParams), len(args))
			}
			*slot = ast.StructType{Name: t.Name, Args: args}
			return
		}
		if ed, ok := c.info.Enums[t.Name]; ok {
			if len(ed.TypeParams) != len(args) {
				c.errf(ed.P, "enum %s has %d type parameter(s), %d supplied",
					t.Name, len(ed.TypeParams), len(args))
			}
		}
		*slot = ast.EnumType{Name: t.Name, Args: args}
	case ast.ArrayType:
		elem := t.Elem
		c.resolveType(&elem, params)
		*slot = ast.ArrayType{Elem: elem}
	case *ast.FuncType:
		for i := range t.Params {
			c.resolveType(&t.Params[i], params)
		}
		c.resolveType(&t.Result, params)
	}
}

// variantRef is the resolution target for an unqualified variant
// name. The checker uses it to rewrite `Circle(3.0)` /
// `Red` into a typed *EnumLit.
type variantRef struct {
	enumName string
	index    int
	payloads []ast.Type
}

// substituteType returns t with every ParamType reference
// replaced by the type bound to that parameter in `sub`. Unbound
// parameters fall through unchanged so the caller can detect
// "couldn't fully resolve" cases. Recurses into composite types
// (arrays, function types, generic enum args) so a payload like
// `Option[T]` resolves to `Option[number]` when T=number.
func substituteType(t ast.Type, sub map[string]ast.Type) ast.Type {
	if t == nil {
		return nil
	}
	switch x := t.(type) {
	case ast.ParamType:
		if v, ok := sub[x.Name]; ok {
			return v
		}
		return x
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substituteType(x.Args[i], sub)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substituteType(x.Args[i], sub)
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.ArrayType:
		return ast.ArrayType{Elem: substituteType(x.Elem, sub)}
	case ast.SliceType:
		return ast.SliceType{Elem: substituteType(x.Elem, sub)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = substituteType(x.Elems[i], sub)
		}
		return out
	case *ast.FuncType:
		out := &ast.FuncType{Result: substituteType(x.Result, sub)}
		for _, p := range x.Params {
			out.Params = append(out.Params, substituteType(p, sub))
		}
		return out
	}
	return t
}

// unifyType records the substitutions that would make `expected`
// (a type containing ParamType references) equal to `actual` (a
// concrete type). Updates `sub` in place and returns false on
// conflict. Concrete-vs-concrete still goes through ast.Equal,
// so existing strict-checking behaviour is preserved for
// monomorphic enums.
func (c *checker) unifyType(expected, actual ast.Type, sub map[string]ast.Type) bool {
	if expected == nil || actual == nil {
		return false
	}
	if p, ok := expected.(ast.ParamType); ok {
		if existing, bound := sub[p.Name]; bound {
			return ast.Equal(existing, actual)
		}
		sub[p.Name] = actual
		return true
	}
	// Generic enum positions: unify pairwise.
	if e, ok := expected.(ast.EnumType); ok {
		a, ok := actual.(ast.EnumType)
		if !ok || a.Name != e.Name || len(a.Args) != len(e.Args) {
			return false
		}
		for i := range e.Args {
			if !c.unifyType(e.Args[i], a.Args[i], sub) {
				return false
			}
		}
		return true
	}
	// Generic struct positions: same shape as enums.
	if e, ok := expected.(ast.StructType); ok {
		a, ok := actual.(ast.StructType)
		if !ok || a.Name != e.Name || len(a.Args) != len(e.Args) {
			return false
		}
		for i := range e.Args {
			if !c.unifyType(e.Args[i], a.Args[i], sub) {
				return false
			}
		}
		return true
	}
	// Arrays + slices + tuples + function types decompose the same way.
	if e, ok := expected.(ast.ArrayType); ok {
		a, ok := actual.(ast.ArrayType)
		return ok && c.unifyType(e.Elem, a.Elem, sub)
	}
	if e, ok := expected.(ast.SliceType); ok {
		a, ok := actual.(ast.SliceType)
		return ok && c.unifyType(e.Elem, a.Elem, sub)
	}
	if e, ok := expected.(ast.TupleType); ok {
		a, ok := actual.(ast.TupleType)
		if !ok || len(e.Elems) != len(a.Elems) {
			return false
		}
		for i := range e.Elems {
			if !c.unifyType(e.Elems[i], a.Elems[i], sub) {
				return false
			}
		}
		return true
	}
	if e, ok := expected.(*ast.FuncType); ok {
		a, ok := actual.(*ast.FuncType)
		if !ok || len(e.Params) != len(a.Params) {
			return false
		}
		for i := range e.Params {
			if !c.unifyType(e.Params[i], a.Params[i], sub) {
				return false
			}
		}
		return c.unifyType(e.Result, a.Result, sub)
	}
	return ast.Equal(expected, actual)
}

// assignable reports whether a value of type `src` can flow into
// a slot expecting `dst`. It's strictly equal in most cases;
// the one relaxation is for payload-less variants on generic
// enums where the construction site can't infer the type
// arguments. `None` produces `EnumType{"Option", nil}` which
// flows into `Option[number]` here without complaint.
func assignable(dst, src ast.Type) bool {
	if ast.Equal(dst, src) {
		return true
	}
	d, dok := dst.(ast.EnumType)
	s, sok := src.(ast.EnumType)
	if dok && sok && d.Name == s.Name && len(s.Args) == 0 && len(d.Args) > 0 {
		return true
	}
	// Polymorphic numeric literal: NumberType{Polymorphic:true}
	// flows into any concrete integer type. The checker is
	// expected to have stamped the resolved width onto the
	// literal AST node already (via c.settleNumeric); this is
	// the type-system side of that handshake.
	if _, ok := dst.(ast.NumberType); ok {
		if sn, ok := src.(ast.NumberType); ok && sn.Polymorphic {
			return true
		}
	}
	// Mirror behaviour for float literals.
	if _, ok := dst.(ast.FloatType); ok {
		if sf, ok := src.(ast.FloatType); ok && sf.Polymorphic {
			return true
		}
	}
	return false
}

func (c *checker) errf(pos ast.Position, format string, args ...any) {
	c.errors = append(c.errors, &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// errIdent reports an unresolved-name error and tries to attach a
// "did you mean foo?" hint by scanning every name visible in scope
// (locals, params, top-level functions). The error span covers the
// whole identifier so the squiggle underlines the misspelt name.
func (c *checker) errIdent(n *ast.Ident, s *scope, format string, args ...any) {
	cands := c.collectNames(s)
	suggestion := diag.Suggest(n.Name, cands)
	e := &Error{
		Pos:  n.P,
		Span: len(n.Name),
		Msg:  fmt.Sprintf(format, args...),
	}
	if suggestion != "" {
		e.Note = fmt.Sprintf("did you mean %q?", suggestion)
	}
	c.errors = append(c.errors, e)
}

// collectNames flattens every name reachable from s, plus all top-level
// function names, into a single slice for diag.Suggest to scan.
func (c *checker) collectNames(s *scope) []string {
	seen := map[string]bool{}
	var out []string
	for cur := s; cur != nil; cur = cur.parent {
		for name := range cur.names {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	for name := range c.info.FuncSigs {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// isUserFuncOrLocal reports whether name shadows the implicit `len`
// builtin via a user-declared function or in-scope variable. Callers
// use it to decide whether to apply the builtin's special typing
// rules — a user that explicitly defines `len` wins.
func (c *checker) isUserFuncOrLocal(name string, s *scope) bool {
	if _, ok := s.lookup(name); ok {
		return true
	}
	if _, ok := c.info.FuncSigs[name]; ok {
		return true
	}
	return false
}

// scope is an environment of named bindings plus a pointer to its parent.
type scope struct {
	parent *scope
	names  map[string]ast.Type
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, names: map[string]ast.Type{}}
}

func (s *scope) lookup(name string) (ast.Type, bool) {
	for cur := s; cur != nil; cur = cur.parent {
		if t, ok := cur.names[name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (c *checker) checkFunction(fn *ast.FuncDecl) {
	c.current = fn
	defer func() { c.current = nil }()

	root := newScope(nil)
	for _, p := range fn.Params {
		if _, dup := root.names[p.Name]; dup {
			c.errf(fn.P, "duplicate parameter %q", p.Name)
		}
		root.names[p.Name] = p.Type
	}
	c.checkBlock(fn.Body, root)
}

func (c *checker) checkBlock(b *ast.Block, parent *scope) {
	s := newScope(parent)
	for _, st := range b.Stmts {
		c.checkStmt(st, s)
	}
}

func (c *checker) checkStmt(st ast.Stmt, s *scope) {
	switch n := st.(type) {
	case *ast.Block:
		c.checkBlock(n, s)
	case *ast.If:
		t := c.checkExpr(n.Cond, s)
		if t != nil && !ast.Equal(t, ast.BoolType{}) {
			c.errf(n.Cond.Pos(), "if condition must be boolean, got %s", t)
		}
		c.checkStmt(n.Then, s)
		if n.Else != nil {
			c.checkStmt(n.Else, s)
		}
	case *ast.LetElse:
		// Same shape as IfLet but bindings escape into the
		// surrounding scope (= mutate `s` directly) and the
		// else block must diverge.
		st := c.checkExpr(n.Source, s)
		et, ok := st.(ast.EnumType)
		if !ok {
			if st != nil {
				c.errf(n.Source.Pos(), "let-else source must be an enum value, got %s", st)
			}
			c.checkBlock(n.Else, s)
			return
		}
		ed := c.info.Enums[et.Name]
		if ed == nil {
			c.errf(n.Source.Pos(), "unknown enum %q", et.Name)
			c.checkBlock(n.Else, s)
			return
		}
		var sub map[string]ast.Type
		if len(ed.TypeParams) == len(et.Args) && len(et.Args) > 0 {
			sub = make(map[string]ast.Type, len(ed.TypeParams))
			for i, tp := range ed.TypeParams {
				sub[tp] = et.Args[i]
			}
		}
		var variant *ast.EnumVariant
		for i := range ed.Variants {
			if ed.Variants[i].Name == n.VariantName {
				variant = &ed.Variants[i]
				break
			}
		}
		if variant == nil {
			c.errf(n.P, "variant %q is not part of enum %s", n.VariantName, ed.Name)
			c.checkBlock(n.Else, s)
			return
		}
		if len(n.Bindings) != len(variant.Payloads) {
			c.errf(n.P, "variant %s has %d payload(s), got %d binding(s)",
				n.VariantName, len(variant.Payloads), len(n.Bindings))
		}
		// Bindings flow into the ENCLOSING scope so later
		// statements see them. Conceptually: the else branch
		// diverges, so on fall-through to the rest of the
		// block, the bindings are guaranteed initialised.
		n.BindingTypes = make([]ast.Type, len(n.Bindings))
		for k, name := range n.Bindings {
			var bt ast.Type
			if k < len(variant.Payloads) {
				bt = substituteType(variant.Payloads[k], sub)
			}
			n.BindingTypes[k] = bt
			s.names[name] = bt
		}
		// Else runs in its own block scope (the bindings
		// aren't available there — only on the match path).
		c.checkBlock(n.Else, s)
		if !blockDiverges(n.Else) {
			c.errf(n.Else.P, "let-else: else branch must diverge (return / break / continue)")
		}
	case *ast.IfLet:
		// Source must produce an enum whose variant list contains
		// VariantName. Bindings are in scope for Then only.
		st := c.checkExpr(n.Source, s)
		et, ok := st.(ast.EnumType)
		if !ok {
			if st != nil {
				c.errf(n.Source.Pos(), "if-let source must be an enum value, got %s", st)
			}
			c.checkStmt(n.Then, s)
			if n.Else != nil {
				c.checkStmt(n.Else, s)
			}
			return
		}
		ed := c.info.Enums[et.Name]
		if ed == nil {
			c.errf(n.Source.Pos(), "unknown enum %q", et.Name)
			c.checkStmt(n.Then, s)
			if n.Else != nil {
				c.checkStmt(n.Else, s)
			}
			return
		}
		// Resolve type-arg substitution so generic enums
		// (`Option[i32]`, `Result[T, E]`) bind payloads to the
		// concrete instantiated types instead of the abstract
		// parameters.
		var sub map[string]ast.Type
		if len(ed.TypeParams) == len(et.Args) && len(et.Args) > 0 {
			sub = make(map[string]ast.Type, len(ed.TypeParams))
			for i, tp := range ed.TypeParams {
				sub[tp] = et.Args[i]
			}
		}
		var variant *ast.EnumVariant
		for i := range ed.Variants {
			if ed.Variants[i].Name == n.VariantName {
				variant = &ed.Variants[i]
				break
			}
		}
		if variant == nil {
			c.errf(n.P, "variant %q is not part of enum %s", n.VariantName, ed.Name)
			c.checkStmt(n.Then, s)
			if n.Else != nil {
				c.checkStmt(n.Else, s)
			}
			return
		}
		if len(n.Bindings) != len(variant.Payloads) {
			c.errf(n.P, "variant %s has %d payload(s), got %d binding(s)",
				n.VariantName, len(variant.Payloads), len(n.Bindings))
		}
		thenScope := newScope(s)
		n.BindingTypes = make([]ast.Type, len(n.Bindings))
		for k, name := range n.Bindings {
			var bt ast.Type
			if k < len(variant.Payloads) {
				bt = substituteType(variant.Payloads[k], sub)
			}
			n.BindingTypes[k] = bt
			thenScope.names[name] = bt
		}
		c.checkStmt(n.Then, thenScope)
		if n.Else != nil {
			c.checkStmt(n.Else, s)
		}
	case *ast.While:
		t := c.checkExpr(n.Cond, s)
		if t != nil && !ast.Equal(t, ast.BoolType{}) {
			c.errf(n.Cond.Pos(), "while condition must be boolean, got %s", t)
		}
		c.loopDepth++
		c.checkStmt(n.Body, s)
		c.loopDepth--
	case *ast.For:
		// Init runs in a new scope so a `for (var i = 0; ...)` doesn't
		// leak `i` to the surrounding block.
		inner := newScope(s)
		if n.Init != nil {
			c.checkStmt(n.Init, inner)
		}
		ct := c.checkExpr(n.Cond, inner)
		if ct != nil && !ast.Equal(ct, ast.BoolType{}) {
			c.errf(n.Cond.Pos(), "for condition must be boolean, got %s", ct)
		}
		c.loopDepth++
		c.checkStmt(n.Body, inner)
		if n.Step != nil {
			c.checkStmt(n.Step, inner)
		}
		c.loopDepth--
	case *ast.Break:
		// `break` is legal inside a `for`/`while` (exits the loop)
		// or inside a `switch` case (exits the switch).
		if c.loopDepth == 0 && c.switchDepth == 0 {
			c.errf(n.P, "break outside of a loop or switch")
		}
	case *ast.Continue:
		if c.loopDepth == 0 {
			c.errf(n.P, "continue outside of a loop")
		}
	case *ast.Return:
		want := c.current.ReturnType
		if n.Value == nil {
			if !ast.Equal(want, ast.VoidType{}) {
				c.errf(n.P, "return without value in function returning %s", want)
			}
			return
		}
		got := c.checkExpr(n.Value, s)
		c.settleNumeric(n.Value, want)
		if got != nil && !assignable(want, got) {
			c.errf(n.P, "return type mismatch: function returns %s but expression is %s", want, got)
		}
	case *ast.Var:
		if _, dup := s.names[n.Name]; dup {
			c.errf(n.P, "variable %q already declared in this scope", n.Name)
		}
		got := c.checkExpr(n.Init, s)
		if n.Type != nil {
			c.settleNumeric(n.Init, n.Type)
		}
		if n.Type == nil {
			if got == nil {
				return
			}
			n.Type = got
		} else if got != nil && !assignable(n.Type, got) {
			c.errf(n.P, "cannot assign %s to variable of type %s", got, n.Type)
		}
		s.names[n.Name] = n.Type
		c.info.VarTypes[n] = n.Type
		c.info.Locals[c.current] = append(c.info.Locals[c.current], n)
	case *ast.ExprStmt:
		c.checkExpr(n.Expr, s)
	case *ast.Switch:
		tagT := c.checkExpr(n.Tag, s)
		// Floats compare with NaN edge cases that switch's "exact
		// match" semantics aren't well-defined for. Reject them up
		// front rather than letting WASM's f32.eq surprise us.
		if tagT != nil && ast.Equal(tagT, ast.FloatType{}) {
			c.errf(n.Tag.Pos(), "switch on float values is not supported")
		}
		// `break` inside case bodies should leave the switch but not
		// abort an enclosing loop. `continue` falls straight through
		// to the enclosing real loop and is invalid otherwise — that's
		// why we bump switchDepth (a break-only counter), not loopDepth.
		c.switchDepth++
		for _, k := range n.Cases {
			for _, v := range k.Values {
				vt := c.checkExpr(v, s)
				if tagT != nil && vt != nil && !ast.Equal(vt, tagT) {
					c.errf(v.Pos(), "case value type %s, expected %s", vt, tagT)
				}
			}
			c.checkBlock(k.Body, s)
		}
		if n.Default != nil {
			c.checkBlock(n.Default, s)
		}
		c.switchDepth--
	case *ast.Match:
		c.checkMatch(n, s)
	case *ast.FuncDecl:
		c.checkLocalFunc(n, s)
	}
}

// checkMatch type-checks a `match` statement. The scrutinee must
// be an enum value; each arm's pattern variant must belong to
// that enum and supply the right number of binding names; the
// arm list must cover every variant of the enum (or end in a
// wildcard). Bindings are typed against the matching variant's
// payload list and bound in a fresh per-arm scope.
func (c *checker) checkMatch(n *ast.Match, s *scope) {
	tagT := c.checkExpr(n.Tag, s)
	if tagT == nil {
		return
	}
	et, ok := tagT.(ast.EnumType)
	if !ok {
		c.errf(n.Tag.Pos(), "match scrutinee must be an enum value, got %s", tagT)
		return
	}
	ed, ok := c.info.Enums[et.Name]
	if !ok {
		c.errf(n.Tag.Pos(), "unknown enum %q", et.Name)
		return
	}
	// For generic enums, build a substitution map from the
	// scrutinee's concrete type arguments. `match (o: Option[number])`
	// gives us T=number, so the arm `Some(v)` types `v` as
	// `number` rather than the unresolved `T`.
	sub := map[string]ast.Type{}
	if len(ed.TypeParams) == len(et.Args) {
		for i, p := range ed.TypeParams {
			sub[p] = et.Args[i]
		}
	}
	covered := map[string]bool{}
	sawWildcard := false
	for i, arm := range n.Arms {
		if arm.IsWildcard {
			if i != len(n.Arms)-1 {
				c.errf(arm.P, "wildcard `_` arm must be last in the match")
			}
			// Wildcard with a guard doesn't satisfy exhaustiveness
			// because the guard might be false at runtime — only
			// an unguarded `_` is the canonical "covers
			// everything" form.
			if arm.Guard == nil {
				sawWildcard = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errf(arm.Guard.Pos(), "match guard must be boolean, got %s", gt)
				}
			}
			c.checkBlock(arm.Body, s)
			continue
		}
		// Find the variant on this enum.
		varIdx := -1
		var variant *ast.EnumVariant
		for j := range ed.Variants {
			if ed.Variants[j].Name == arm.VariantName {
				varIdx = j
				variant = &ed.Variants[j]
				break
			}
		}
		if varIdx < 0 {
			c.errf(arm.P, "variant %q is not part of enum %s", arm.VariantName, ed.Name)
			c.checkBlock(arm.Body, s)
			continue
		}
		if covered[arm.VariantName] {
			c.errf(arm.P, "variant %q already covered earlier in this match", arm.VariantName)
		}
		// Guarded arms don't fully cover the variant: the guard
		// might be false at runtime, in which case the match
		// falls through. Leaving `covered[...]` clear means a
		// later unguarded arm for the same variant (or a
		// wildcard) is required for exhaustiveness — and is no
		// longer flagged as a duplicate.
		if arm.Guard == nil {
			covered[arm.VariantName] = true
		}
		if len(arm.Bindings) != len(variant.Payloads) {
			c.errf(arm.P, "variant %s has %d payload(s), got %d binding(s)",
				arm.VariantName, len(variant.Payloads), len(arm.Bindings))
		}
		// Bind names in a fresh scope so they don't leak into
		// sibling arms. Payload types get the type-parameter
		// substitution applied so `Some(v)` on `Option[number]`
		// types `v` as `number`, not the abstract `T`.
		armScope := newScope(s)
		arm.BindingTypes = make([]ast.Type, len(arm.Bindings))
		for k, name := range arm.Bindings {
			var bt ast.Type
			if k < len(variant.Payloads) {
				bt = substituteType(variant.Payloads[k], sub)
			}
			arm.BindingTypes[k] = bt
			armScope.names[name] = bt
		}
		// Guard runs in the bindings-in-scope frame so it can
		// reference the payload names. Required to be bool.
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errf(arm.Guard.Pos(), "match guard must be boolean, got %s", gt)
			}
		}
		c.checkBlock(arm.Body, armScope)
	}
	if !sawWildcard {
		for _, v := range ed.Variants {
			if !covered[v.Name] {
				c.errf(n.P, "match is not exhaustive — variant %s of enum %s is not covered (add an arm or use `_`)",
					v.Name, ed.Name)
			}
		}
	}
}

// checkLocalFunc type-checks a nested function and records its
// captured outer-scope variables. The local name is bound in the
// surrounding scope so subsequent calls (and recursion through the
// inner name) work; the body checks under a fresh root scope with
// its own params, plus a capture-sink that registers any outer-scope
// name the body reads.
func (c *checker) checkLocalFunc(fn *ast.FuncDecl, outer *scope) {
	// Bind the function's name in the outer scope so subsequent code
	// can call it.
	sig := &ast.FuncType{Result: fn.ReturnType}
	for _, p := range fn.Params {
		sig.Params = append(sig.Params, p.Type)
	}
	outer.names[fn.Name] = sig

	// Body scope: fresh root with the function's own params.
	root := newScope(nil)
	for _, p := range fn.Params {
		if _, dup := root.names[p.Name]; dup {
			c.errf(fn.P, "duplicate parameter %q", p.Name)
		}
		root.names[p.Name] = p.Type
	}

	captured := map[string]ast.Type{}
	var captureOrder []string

	prev := c.current
	prevSink := c.captureSink
	prevOuter := c.captureOuter
	prevLoop := c.loopDepth
	prevSwitch := c.switchDepth
	c.current = fn
	c.loopDepth = 0
	c.switchDepth = 0
	c.captureSink = func(name string, t ast.Type) {
		if _, ok := captured[name]; ok {
			return
		}
		// Recursive self-reference shouldn't capture: the inner
		// function's name is bound in the outer scope above so the
		// lookup falls through here, but we don't want to treat it
		// as a capture.
		if name == fn.Name {
			return
		}
		// Only allow capturing scalar types in this PR. References
		// (string/array/struct/function) would need indirection
		// through the env that we haven't designed yet.
		switch t.(type) {
		case ast.NumberType, ast.BoolType, ast.FloatType:
			captured[name] = t
			captureOrder = append(captureOrder, name)
		default:
			c.errf(fn.P, "captured variable %q has unsupported type %s (only number, boolean, float can be captured)", name, t)
		}
	}
	c.captureOuter = outer
	defer func() {
		c.current = prev
		c.captureSink = prevSink
		c.captureOuter = prevOuter
		c.loopDepth = prevLoop
		c.switchDepth = prevSwitch
	}()

	c.checkBlock(fn.Body, root)

	for _, name := range captureOrder {
		fn.Captures = append(fn.Captures, ast.Param{Name: name, Type: captured[name]})
	}
	// Track the local function's signature so call sites can look it
	// up by name. Codegen's hoisting pass will rename it later.
	c.info.FuncSigs[fn.Name] = sig
}

func (c *checker) checkExpr(e ast.Expr, s *scope) ast.Type {
	switch n := e.(type) {
	case *ast.NumberLit:
		// Integer literals are polymorphic: at the binary-op /
		// var-init / cast-inner / arg / return layer the checker
		// reconciles them against the surrounding expected type
		// via `c.settleNumeric`. If the literal already has a
		// resolved Width (set by a previous settling pass — e.g.
		// from a re-check during monomorphisation), report that.
		if n.Width != 0 {
			return ast.NumberType{Width: n.Width, Signed: !n.IsUnsigned}
		}
		return ast.NumberType{Polymorphic: true}
	case *ast.CastExpr:
		// Currently restricted to numeric → numeric. Bool and
		// string casts (via `as`) come back when the use case
		// arises; today they'd just hide a bug.
		inner := c.checkExpr(n.Inner, s)
		// `1 as u64`: settle the literal at the cast target's
		// width so the IR emits an i64.const, not an i32.const
		// that overflows. Without this, a literal like
		// `4611686018427387904 as u64` silently truncated to 0.
		c.settleNumeric(n.Inner, n.Target)
		inner = postSettleType(n.Inner, inner)
		n.InnerType = inner
		_, innerIsNum := inner.(ast.NumberType)
		_, innerIsFloat := inner.(ast.FloatType)
		_, targetIsNum := n.Target.(ast.NumberType)
		_, targetIsFloat := n.Target.(ast.FloatType)
		if (innerIsNum || innerIsFloat) && (targetIsNum || targetIsFloat) {
			return n.Target
		}
		c.errf(n.P, "cannot cast %s to %s; only numeric casts are supported", inner, n.Target)
		return n.Target
	case *ast.BoolLit:
		return ast.BoolType{}
	case *ast.StringLit:
		return ast.StringType{}
	case *ast.FloatLit:
		// Float literals are polymorphic, same shape as integer
		// literals. If a previous settling pass already locked in
		// a width, surface it; otherwise return a Polymorphic
		// placeholder so the surrounding context can decide.
		if n.Width != 0 {
			return ast.FloatType{Width: n.Width}
		}
		return ast.FloatType{Polymorphic: true}
	case *ast.Ident:
		if t, ok := s.lookup(n.Name); ok {
			return t
		}
		if sig, ok := c.info.FuncSigs[n.Name]; ok {
			return sig
		}
		// A bare name might be a payload-less enum variant.
		// (Variants with payloads are constructed via Call and
		// rejected here so the user gets a clearer error.)
		if vr, ok := c.variantOf[n.Name]; ok {
			if len(vr.payloads) > 0 {
				c.errf(n.P, "variant %s expects %d payload argument(s); call it as %s(...)",
					n.Name, len(vr.payloads), n.Name)
				return nil
			}
			return ast.EnumType{Name: vr.enumName}
		}
		// Inside a local function: a name not found in the local
		// scope might resolve in the enclosing function's scope.
		// Record it as a capture and return its outer type.
		if c.captureOuter != nil {
			if t, ok := c.captureOuter.lookup(n.Name); ok {
				if c.captureSink != nil {
					c.captureSink(n.Name, t)
				}
				return t
			}
		}
		c.errIdent(n, s, "undefined identifier %q", n.Name)
		return nil
	case *ast.ArrayLit:
		if len(n.Elems) == 0 {
			c.errf(n.P, "empty array literal needs a type annotation")
			return nil
		}
		elemT := c.checkExpr(n.Elems[0], s)
		for _, el := range n.Elems[1:] {
			t := c.checkExpr(el, s)
			if t != nil && elemT != nil && !ast.Equal(t, elemT) {
				c.errf(el.Pos(), "array element type %s, expected %s", t, elemT)
			}
		}
		return ast.ArrayType{Elem: elemT}
	case *ast.Index:
		at := c.checkExpr(n.Array, s)
		it := c.checkExpr(n.Idx, s)
		if it != nil && !ast.Equal(it, ast.NumberType{}) {
			c.errf(n.Idx.Pos(), "index must be number, got %s", it)
		}
		if arr, ok := at.(ast.ArrayType); ok {
			return arr.Elem
		}
		if sl, ok := at.(ast.SliceType); ok {
			n.IsSlice = true
			return sl.Elem
		}
		// `s[i]` on a string returns the byte at i as a number.
		if _, ok := at.(ast.StringType); ok {
			n.IsString = true
			return ast.NumberType{}
		}
		if at != nil {
			c.errf(n.P, "indexing non-array value of type %s", at)
		}
		return nil
	case *ast.SliceExpr:
		st := c.checkExpr(n.Source, s)
		if n.Low != nil {
			lt := c.checkExpr(n.Low, s)
			if lt != nil && !ast.Equal(lt, ast.NumberType{}) {
				c.errf(n.Low.Pos(), "slice low bound must be i32, got %s", lt)
			}
		}
		if n.High != nil {
			ht := c.checkExpr(n.High, s)
			if ht != nil && !ast.Equal(ht, ast.NumberType{}) {
				c.errf(n.High.Pos(), "slice high bound must be i32, got %s", ht)
			}
		}
		if arr, ok := st.(ast.ArrayType); ok {
			return ast.SliceType{Elem: arr.Elem}
		}
		if sl, ok := st.(ast.SliceType); ok {
			n.SourceIsSlice = true
			return sl
		}
		if st != nil {
			c.errf(n.P, "cannot slice value of type %s", st)
		}
		return nil
	case *ast.Call:
		// Variant constructor: `Some(x)` / `Square(2.0, 3.0)`.
		// Resolved purely by name so we can type the result as
		// the owning enum and check argument count + payload
		// types. Shadowing by a function or local is intentional —
		// the user can re-bind the name and the variant becomes
		// inaccessible until the shadow leaves scope.
		//
		// For generic enums (`Option[T]`, `Result[T, E]`), the
		// type arguments get inferred from the actual payload arg
		// types — `Some(42)` → `Option[number]`. Inference only
		// works when there's at least one type-determining
		// payload arg; payload-less variants on generic enums
		// (like `None`) yield an EnumType with empty Args, which
		// the assignment relaxation in `assignable` lets flow
		// into a concretely-typed slot at the var / return site.
		if id, ok := n.Callee.(*ast.Ident); ok {
			if vr, ok := c.variantOf[id.Name]; ok && !c.isUserFuncOrLocal(id.Name, s) {
				if len(n.Args) != len(vr.payloads) {
					c.errf(n.P, "variant %s expects %d argument(s), got %d",
						id.Name, len(vr.payloads), len(n.Args))
				}
				ed := c.info.Enums[vr.enumName]
				sub := map[string]ast.Type{}
				for i, a := range n.Args {
					at := c.checkExpr(a, s)
					if i >= len(vr.payloads) || at == nil {
						continue
					}
					if !c.unifyType(vr.payloads[i], at, sub) {
						c.errf(a.Pos(), "variant %s payload %d type %s, expected %s",
							id.Name, i, at, substituteType(vr.payloads[i], sub))
					}
				}
				// Record the substituted (concrete) payload types
				// so codegen knows whether each slot holds an
				// f32 vs i32 even when the variant declared its
				// payload as a type parameter. For non-generic
				// enums substituteType is a no-op.
				resolvedPayloads := make([]ast.Type, len(vr.payloads))
				for i := range vr.payloads {
					resolvedPayloads[i] = substituteType(vr.payloads[i], sub)
				}
				c.info.VariantCallPayloads[n] = resolvedPayloads
				if ed != nil && len(ed.TypeParams) > 0 {
					args := make([]ast.Type, len(ed.TypeParams))
					complete := true
					for i, p := range ed.TypeParams {
						if v, ok := sub[p]; ok {
							args[i] = v
						} else {
							complete = false
						}
					}
					if !complete {
						// Couldn't fill in every parameter from
						// the args alone (typical for a
						// payload-less variant). Leave Args nil
						// so `assignable` flows the type into
						// whatever the surrounding context
						// expects.
						return ast.EnumType{Name: vr.enumName}
					}
					return ast.EnumType{Name: vr.enumName, Args: args}
				}
				return ast.EnumType{Name: vr.enumName}
			}
		}
		// `len(x)` is a generic builtin: it accepts any string or
		// array and returns a number. We type-check it here rather
		// than in FuncSigs because no monomorphic FuncType expresses
		// the union.
		if id, ok := n.Callee.(*ast.Ident); ok && id.Name == "len" && !c.isUserFuncOrLocal(id.Name, s) {
			if len(n.Args) != 1 {
				c.errf(n.P, "len expects 1 argument, got %d", len(n.Args))
				return ast.NumberType{}
			}
			at := c.checkExpr(n.Args[0], s)
			switch at.(type) {
			case ast.StringType, ast.ArrayType, ast.SliceType:
				// fine
			default:
				if at != nil {
					c.errf(n.Args[0].Pos(), "len: expected string, array, or slice, got %s", at)
				}
			}
			return ast.NumberType{}
		}
		// Method call dispatch: `target.method(args)` where target is a
		// struct value and the struct has a method of that name. We
		// rewrite the Call node in place to `mangledName(target, args)`
		// so the rest of the pipeline (codegen, IR) only ever sees a
		// regular function call.
		if fa, ok := n.Callee.(*ast.FieldAccess); ok {
			tt := c.checkExpr(fa.Target, s)
			if st, ok := tt.(ast.StructType); ok {
				key := st.Name + "." + fa.Field
				if mangled, ok := c.info.Methods[key]; ok {
					n.Callee = &ast.Ident{P: fa.P, Name: mangled}
					n.Args = append([]ast.Expr{fa.Target}, n.Args...)
				}
			}
		}
		callee := c.checkExpr(n.Callee, s)
		ft, ok := callee.(*ast.FuncType)
		if !ok {
			if callee != nil {
				c.errf(n.P, "calling non-function value of type %s", callee)
			}
			return nil
		}
		if len(n.Args) != len(ft.Params) {
			c.errf(n.P, "function expects %d arguments, got %d", len(ft.Params), len(n.Args))
		}
		// If the callee resolves to a generic FuncDecl, infer its
		// type arguments from the actual argument types and stamp
		// them on the Call so the monomorphisation pass picks the
		// right clone. Inference only consults the args — explicit
		// type-arg syntax (`f[i32](42)`) is reserved.
		var sub map[string]ast.Type
		var genericFn *ast.FuncDecl
		if id, ok := n.Callee.(*ast.Ident); ok {
			if fn, isGen := c.info.GenericFuncs[id.Name]; isGen {
				genericFn = fn
				sub = make(map[string]ast.Type, len(fn.TypeParams))
			}
		}
		for i, a := range n.Args {
			at := c.checkExpr(a, s)
			if i < len(ft.Params) && at != nil {
				expected := ft.Params[i]
				// Polymorphic-literal settling: `f(1)` where f
				// expects i64 needs the literal to lock in i64
				// before assignable / unifyType run, otherwise
				// the i32-default would mismatch the expected
				// param type.
				c.settleNumeric(a, expected)
				at = postSettleType(a, at)
				if sub != nil {
					if !c.unifyType(expected, at, sub) {
						c.errf(a.Pos(), "argument %d: expected %s, got %s", i+1, expected, at)
					}
				} else if !assignable(expected, at) {
					c.errf(a.Pos(), "argument %d: expected %s, got %s", i+1, expected, at)
				}
			}
		}
		if genericFn != nil {
			// Substitute the inferred sub through the result so
			// callers see a concrete type, AND record TypeArgs in
			// declaration order for the monomorphiser.
			args := make([]ast.Type, len(genericFn.TypeParams))
			complete := true
			for i, tp := range genericFn.TypeParams {
				if v, ok := sub[tp]; ok {
					args[i] = v
				} else {
					c.errf(n.P, "could not infer type parameter %s for %s — explicit type args are not supported yet", tp, genericFn.Name)
					complete = false
				}
			}
			if complete {
				n.TypeArgs = args
				return substituteType(ft.Result, sub)
			}
			return nil
		}
		return ft.Result
	case *ast.Binary:
		lt := c.checkExpr(n.Left, s)
		rt := c.checkExpr(n.Right, s)
		switch n.Op {
		case "+":
			// Special case: string + string is concatenation.
			if _, lOk := lt.(ast.StringType); lOk {
				if _, rOk := rt.(ast.StringType); rOk {
					n.IsStringConcat = true
					return ast.StringType{}
				}
			}
			fallthrough
		case "-", "*", "/":
			if isFloat(lt) || isFloat(rt) {
				c.requireFloat(n.P, lt, n.Op)
				c.requireFloat(n.P, rt, n.Op)
				n.IsFloat = true
				common, ok := commonFloatWidth(lt, rt)
				if !ok {
					c.errf(n.P, "operator %q requires both operands to share a float type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
					return ast.FloatType{}
				}
				c.settleNumeric(n.Left, common)
				c.settleNumeric(n.Right, common)
				if !common.Polymorphic {
					n.FloatWidth = common.NormalWidth()
				}
				return common
			}
			c.requireInteger(n.P, lt, n.Op)
			c.requireInteger(n.P, rt, n.Op)
			common, ok := commonIntegerWidth(lt, rt)
			if !ok {
				c.errf(n.P, "operator %q requires both operands to share an integer type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
				return ast.NumberType{}
			}
			c.settleNumeric(n.Left, common)
			c.settleNumeric(n.Right, common)
			if !common.Polymorphic {
				n.IntWidth = common.NormalWidth()
				n.IsUnsigned = !common.IsSigned()
			}
			return common
		case "%", "&", "|", "^", "<<", ">>":
			c.requireInteger(n.P, lt, n.Op)
			c.requireInteger(n.P, rt, n.Op)
			common, ok := commonIntegerWidth(lt, rt)
			if !ok {
				c.errf(n.P, "operator %q requires both operands to share an integer type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
				return ast.NumberType{}
			}
			c.settleNumeric(n.Left, common)
			c.settleNumeric(n.Right, common)
			if !common.Polymorphic {
				n.IntWidth = common.NormalWidth()
				n.IsUnsigned = !common.IsSigned()
			}
			return common
		case "<", ">", "<=", ">=":
			if isFloat(lt) || isFloat(rt) {
				c.requireFloat(n.P, lt, n.Op)
				c.requireFloat(n.P, rt, n.Op)
				n.IsFloat = true
				if common, ok := commonFloatWidth(lt, rt); ok && !common.Polymorphic {
					c.settleNumeric(n.Left, common)
					c.settleNumeric(n.Right, common)
					n.FloatWidth = common.NormalWidth()
				}
				return ast.BoolType{}
			}
			c.requireInteger(n.P, lt, n.Op)
			c.requireInteger(n.P, rt, n.Op)
			common, ok := commonIntegerWidth(lt, rt)
			if !ok {
				c.errf(n.P, "operator %q requires both operands to share an integer type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
				return ast.BoolType{}
			}
			c.settleNumeric(n.Left, common)
			c.settleNumeric(n.Right, common)
			if !common.Polymorphic {
				n.IntWidth = common.NormalWidth()
				n.IsUnsigned = !common.IsSigned()
			}
			return ast.BoolType{}
		case "==", "!=":
			// Polymorphic-literal compare: settle the literal
			// side to the concrete side's type before the
			// equality check fires. `(x: i64) == 0` should not
			// error on width mismatch — the `0` is a polymorphic
			// literal that locks in i64 here. Same for floats.
			if common, common_ok := commonIntegerWidth(lt, rt); common_ok && !common.Polymorphic {
				c.settleNumeric(n.Left, common)
				c.settleNumeric(n.Right, common)
				lt = postSettleType(n.Left, lt)
				rt = postSettleType(n.Right, rt)
			} else if common, common_ok := commonFloatWidth(lt, rt); common_ok && !common.Polymorphic {
				c.settleNumeric(n.Left, common)
				c.settleNumeric(n.Right, common)
				lt = postSettleType(n.Left, lt)
				rt = postSettleType(n.Right, rt)
			}
			if lt != nil && rt != nil && !ast.Equal(lt, rt) {
				c.errf(n.P, "cannot compare %s and %s", lt, rt)
			}
			// String-vs-string equality compares contents; flag so
			// codegen lowers to a runtime call rather than i32.eq.
			if _, ok := lt.(ast.StringType); ok {
				if _, ok := rt.(ast.StringType); ok {
					n.IsStringCmp = true
				}
			}
			// Float-vs-float equality has to lower to f32.eq /
			// f32.ne — using i32.eq on f32 operands fails core-wasm
			// validation. Latent bug: never hit before because the
			// preview-1 test path observed floats via `--invoke
			// main` and never compared them in lang.
			if isFloat(lt) && isFloat(rt) {
				n.IsFloat = true
				if common, ok := commonFloatWidth(lt, rt); ok && !common.Polymorphic {
					c.settleNumeric(n.Left, common)
					c.settleNumeric(n.Right, common)
					n.FloatWidth = common.NormalWidth()
				}
			}
			// Track i64 equality so codegen knows to emit `i64.eq`
			// / `i64.ne` instead of `i32.eq`/`ne`. Settling
			// already happened above (before the equality check
			// fired) so just record the width here.
			if common, ok := commonIntegerWidth(lt, rt); ok && !common.Polymorphic {
				n.IntWidth = common.NormalWidth()
			}
			return ast.BoolType{}
		case "&&", "||":
			c.requireBool(n.P, lt, n.Op)
			c.requireBool(n.P, rt, n.Op)
			return ast.BoolType{}
		}
		c.errf(n.P, "unknown binary operator %q", n.Op)
		return nil
	case *ast.Unary:
		t := c.checkExpr(n.Operand, s)
		switch n.Op {
		case "-":
			if isFloat(t) {
				n.IsFloat = true
				return ast.FloatType{}
			}
			c.requireNumber(n.P, t, n.Op)
			return ast.NumberType{}
		case "!":
			c.requireBool(n.P, t, n.Op)
			return ast.BoolType{}
		}
		return nil
	case *ast.Assign:
		lt := c.checkExpr(n.Target, s)
		rt := c.checkExpr(n.Value, s)
		if lt != nil {
			c.settleNumeric(n.Value, lt)
			rt = postSettleType(n.Value, rt)
		}
		if lt != nil && rt != nil && !ast.Equal(lt, rt) && !assignable(lt, rt) {
			c.errf(n.P, "cannot assign %s to %s", rt, lt)
		}
		// Restrict assignment targets the same way `=` does for
		// arrays: only Ident, Index and FieldAccess are addressable.
		if _, ok := n.Target.(*ast.FieldAccess); !ok {
			if _, ok := n.Target.(*ast.Ident); !ok {
				if _, ok := n.Target.(*ast.Index); !ok {
					// Already errored elsewhere when the parser built
					// the Assign — nothing to add here.
				}
			}
		}
		return lt
	case *ast.Ternary:
		ct := c.checkExpr(n.Cond, s)
		if ct != nil && !ast.Equal(ct, ast.BoolType{}) {
			c.errf(n.Cond.Pos(), "ternary condition must be boolean, got %s", ct)
		}
		tt := c.checkExpr(n.Then, s)
		et := c.checkExpr(n.Else, s)
		if tt != nil && et != nil && !ast.Equal(tt, et) {
			c.errf(n.P, "ternary branches differ: %s vs %s", tt, et)
		}
		result := tt
		if result == nil {
			result = et
		}
		if isFloat(result) {
			n.IsFloat = true
		}
		return result
	case *ast.StructLit:
		sd, ok := c.info.Structs[n.TypeName]
		if !ok {
			c.errf(n.P, "unknown struct type %q", n.TypeName)
			return nil
		}
		// Each declared field must be initialised exactly once and
		// have the right type. Surplus / unknown fields are an error.
		seen := map[string]bool{}
		fieldT := map[string]ast.Type{}
		for _, f := range sd.Fields {
			fieldT[f.Name] = f.Type
		}
		// For generic structs we infer type-args from field
		// values via the same `unifyType` machinery used for
		// generic function calls. Empty for non-generic structs.
		var sub map[string]ast.Type
		if len(sd.TypeParams) > 0 {
			sub = make(map[string]ast.Type, len(sd.TypeParams))
		}
		for _, f := range n.Fields {
			expected, present := fieldT[f.Name]
			if !present {
				c.errf(n.P, "struct %s has no field %q", sd.Name, f.Name)
				continue
			}
			if seen[f.Name] {
				c.errf(n.P, "duplicate field %q in struct literal", f.Name)
			}
			seen[f.Name] = true
			vt := c.checkExpr(f.Value, s)
			if vt == nil {
				continue
			}
			c.settleNumeric(f.Value, expected)
			vt = postSettleType(f.Value, vt)
			if sub != nil {
				if !c.unifyType(expected, vt, sub) {
					c.errf(f.Value.Pos(), "field %q: expected %s, got %s", f.Name, expected, vt)
				}
			} else if !ast.Equal(vt, expected) {
				c.errf(f.Value.Pos(), "field %q: expected %s, got %s", f.Name, expected, vt)
			}
		}
		for _, f := range sd.Fields {
			if !seen[f.Name] {
				c.errf(n.P, "struct literal missing field %q", f.Name)
			}
		}
		if len(sd.TypeParams) > 0 {
			args := make([]ast.Type, len(sd.TypeParams))
			complete := true
			for i, tp := range sd.TypeParams {
				if v, ok := sub[tp]; ok {
					args[i] = v
				} else {
					c.errf(n.P, "could not infer type parameter %s for struct %s — explicit type args are not supported yet", tp, sd.Name)
					complete = false
				}
			}
			if complete {
				// Stamp on the StructLit so the monomorpher
				// can rewrite TypeName without re-running
				// inference.
				n.TypeArgs = args
				return ast.StructType{Name: sd.Name, Args: args}
			}
			return nil
		}
		return ast.StructType{Name: sd.Name}
	case *ast.TupleLit:
		elems := make([]ast.Type, len(n.Elems))
		for i, e := range n.Elems {
			t := c.checkExpr(e, s)
			if t == nil {
				return nil
			}
			elems[i] = t
		}
		return ast.TupleType{Elems: elems}
	case *ast.FieldAccess:
		tt := c.checkExpr(n.Target, s)
		// Tuple field access: `pair.0`, `pair.1`. The Field name
		// is the digit string from the parser; reject anything
		// that isn't a non-negative integer in range, but defer
		// to the struct path otherwise so `obj.fieldName` keeps
		// working.
		if tup, ok := tt.(ast.TupleType); ok {
			idx, err := strconv.Atoi(n.Field)
			if err != nil || idx < 0 {
				c.errf(n.P, "tuple field access requires a numeric index, got %q", n.Field)
				return nil
			}
			if idx >= len(tup.Elems) {
				c.errf(n.P, "tuple has %d elements; index %d is out of range", len(tup.Elems), idx)
				return nil
			}
			return tup.Elems[idx]
		}
		st, ok := tt.(ast.StructType)
		if !ok {
			if tt != nil {
				c.errf(n.P, "field access on non-struct value of type %s", tt)
			}
			return nil
		}
		sd := c.info.Structs[st.Name]
		if sd == nil {
			c.errf(n.P, "unknown struct type %q", st.Name)
			return nil
		}
		for _, f := range sd.Fields {
			if f.Name == n.Field {
				if len(sd.TypeParams) > 0 && len(st.Args) == len(sd.TypeParams) {
					// Generic struct field: substitute the
					// type-arg values into the field's
					// declared type so callers see the
					// concrete type (`Pair[i32, string].first`
					// → i32, not `A`).
					sub := make(map[string]ast.Type, len(sd.TypeParams))
					for i, tp := range sd.TypeParams {
						sub[tp] = st.Args[i]
					}
					return substituteType(f.Type, sub)
				}
				return f.Type
			}
		}
		c.errf(n.P, "struct %s has no field %q", st.Name, n.Field)
		return nil
	}
	return nil
}

func (c *checker) requireNumber(p ast.Position, t ast.Type, op string) {
	if t != nil && !ast.Equal(t, ast.NumberType{}) {
		c.errf(p, "operator %q requires i32, got %s", op, t)
	}
}

// requireInteger matches any integer type — i32, i64, eventually
// the unsigned widths. Used by arithmetic checks that allow either
// width as long as both sides agree.
func (c *checker) requireInteger(p ast.Position, t ast.Type, op string) {
	if t == nil {
		return
	}
	if _, ok := t.(ast.NumberType); !ok {
		c.errf(p, "operator %q requires an integer type, got %s", op, t)
	}
}

// settleNumeric stamps the resolved integer type onto every
// polymorphic-literal node it can reach in `e`. The hint must be
// an ast.NumberType describing the resolved width + signedness;
// non-integer hints are no-ops. This is invoked at every site
// where a known-concrete type meets an expression that may
// contain Width=0 NumberLits — variable initialisers, return
// statements, function arguments, struct fields, cast inners,
// assignments, and the binary-op merging path.
//
// The walker only descends through expressions that legitimately
// "carry through" the type: literals, unary +/-, and arithmetic
// / bitwise binary ops where the IntWidth isn't already set.
// Function calls, casts, struct-lit fields, etc. set their own
// types and shouldn't have hints leak into them.
func (c *checker) settleNumeric(e ast.Expr, hint ast.Type) {
	switch hn := hint.(type) {
	case ast.NumberType:
		if hn.Polymorphic {
			return
		}
		c.settleInt(e, hn)
	case ast.FloatType:
		if hn.Polymorphic {
			return
		}
		c.settleFloat(e, hn)
	}
}

func (c *checker) settleInt(e ast.Expr, hn ast.NumberType) {
	width := hn.NormalWidth()
	isUnsigned := !hn.IsSigned()
	switch x := e.(type) {
	case *ast.NumberLit:
		if x.Width == 0 {
			x.Width = width
			x.IsUnsigned = isUnsigned
			c.checkLiteralFits(x, hn)
		}
	case *ast.Unary:
		if x.Op == "-" || x.Op == "+" {
			c.settleInt(x.Operand, hn)
		}
	case *ast.Binary:
		switch x.Op {
		case "+", "-", "*", "/", "%", "&", "|", "^", "<<", ">>":
			if x.IntWidth == 0 {
				c.settleInt(x.Left, hn)
				c.settleInt(x.Right, hn)
				x.IntWidth = width
				x.IsUnsigned = isUnsigned
			}
		}
	}
}

func (c *checker) settleFloat(e ast.Expr, hf ast.FloatType) {
	width := hf.NormalWidth()
	switch x := e.(type) {
	case *ast.FloatLit:
		if x.Width == 0 {
			x.Width = width
		}
	case *ast.Unary:
		if x.Op == "-" || x.Op == "+" {
			c.settleFloat(x.Operand, hf)
		}
	case *ast.Binary:
		switch x.Op {
		case "+", "-", "*", "/":
			if x.FloatWidth == 0 {
				c.settleFloat(x.Left, hf)
				c.settleFloat(x.Right, hf)
				x.FloatWidth = width
			}
		}
	}
}

// postSettleType returns the post-settling type of `e`. After
// `c.settleNumeric` has stamped concrete widths onto polymorphic
// nodes, the original type returned by `checkExpr` is stale —
// for a NumberLit it was a Polymorphic placeholder, for a
// Binary it was Polymorphic from
// `commonIntegerWidth(poly, poly)`. This helper recomputes the
// type from whatever the settling pass stamped. Non-numeric
// expressions return `prior` unchanged.
func postSettleType(e ast.Expr, prior ast.Type) ast.Type {
	switch x := e.(type) {
	case *ast.NumberLit:
		if x.Width != 0 {
			return ast.NumberType{Width: x.Width, Signed: !x.IsUnsigned}
		}
	case *ast.FloatLit:
		if x.Width != 0 {
			return ast.FloatType{Width: x.Width}
		}
	case *ast.Binary:
		if x.IntWidth != 0 {
			return ast.NumberType{Width: x.IntWidth, Signed: !x.IsUnsigned}
		}
		if x.FloatWidth != 0 {
			return ast.FloatType{Width: x.FloatWidth}
		}
	case *ast.Unary:
		return postSettleType(x.Operand, prior)
	}
	return prior
}

// checkLiteralFits reports an error if the literal's value is
// outside the range representable by the resolved integer type.
// Run once the width has been decided. For unsigned types the
// value must be in [0, 2^width); for signed it must be in
// [-2^(width-1), 2^(width-1)).
func (c *checker) checkLiteralFits(lit *ast.NumberLit, t ast.NumberType) {
	w := t.NormalWidth()
	if t.IsSigned() {
		var min, max int64
		switch w {
		case 32:
			min, max = -1<<31, 1<<31-1
		case 64:
			return
		default:
			return
		}
		if lit.Value < min || lit.Value > max {
			c.errf(lit.P, "literal %d does not fit in %s", lit.Value, t)
		}
	} else {
		var max uint64
		switch w {
		case 32:
			max = 1<<32 - 1
		case 64:
			return
		default:
			return
		}
		if lit.Value < 0 || uint64(lit.Value) > max {
			c.errf(lit.P, "literal %d does not fit in %s", lit.Value, t)
		}
	}
}

// commonFloatWidth mirrors commonIntegerWidth for FloatType. A
// polymorphic side unifies with whichever concrete width the
// other side has; mixed concrete widths are an error.
func commonFloatWidth(lt, rt ast.Type) (ast.FloatType, bool) {
	ln, lOk := lt.(ast.FloatType)
	rn, rOk := rt.(ast.FloatType)
	if !lOk || !rOk {
		return ast.FloatType{}, false
	}
	if ln.Polymorphic && !rn.Polymorphic {
		return rn, true
	}
	if rn.Polymorphic && !ln.Polymorphic {
		return ln, true
	}
	if ln.NormalWidth() != rn.NormalWidth() {
		return ast.FloatType{}, false
	}
	return ln, true
}

// commonIntegerWidth returns the common NumberType when both sides
// are integers of the same width + signedness, plus a boolean
// indicating success. Mixed widths trigger a checker error from
// the caller — `as` is required for explicit conversion.
//
// Polymorphic-literal placeholders (NumberType{Polymorphic:true})
// unify with any concrete integer type: the more-specific side
// wins. `1 + (x: i64)` returns i64 here so the binary's IntWidth
// gets stamped to 64; the caller is responsible for settling the
// polymorphic literal's recorded width via c.settleNumeric.
// Two placeholders return a placeholder, which the caller
// eventually settles to i32 if no further hint arrives.
func commonIntegerWidth(lt, rt ast.Type) (ast.NumberType, bool) {
	ln, lOk := lt.(ast.NumberType)
	rn, rOk := rt.(ast.NumberType)
	if !lOk || !rOk {
		return ast.NumberType{}, false
	}
	if ln.Polymorphic && !rn.Polymorphic {
		return rn, true
	}
	if rn.Polymorphic && !ln.Polymorphic {
		return ln, true
	}
	if ln.NormalWidth() != rn.NormalWidth() || ln.IsSigned() != rn.IsSigned() {
		return ast.NumberType{}, false
	}
	return ln, true
}
func (c *checker) requireFloat(p ast.Position, t ast.Type, op string) {
	if t == nil {
		return
	}
	if _, ok := t.(ast.FloatType); !ok {
		c.errf(p, "operator %q requires float, got %s", op, t)
	}
}
func isFloat(t ast.Type) bool {
	_, ok := t.(ast.FloatType)
	return ok
}
func (c *checker) requireBool(p ast.Position, t ast.Type, op string) {
	if t != nil && !ast.Equal(t, ast.BoolType{}) {
		c.errf(p, "operator %q requires boolean, got %s", op, t)
	}
}
