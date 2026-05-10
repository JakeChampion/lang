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
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/prelude"
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

// isWideMapValueType is the checker-side mirror of the IR's
// wide-V Map detection — used by the bits of method dispatch
// that need to reject Map operations not yet covered by the
// boxing path (currently just `values()`).
func isWideMapValueType(t ast.Type) bool {
	if n, ok := t.(ast.NumberType); ok && n.Width == 64 {
		return true
	}
	if f, ok := t.(ast.FloatType); ok && f.Width == 64 {
		return true
	}
	return false
}

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
		// JsonValue — recursive AST representation for JSON
		// documents. Numbers carry their textual representation
		// (the JSON spec doesn't fix precision); callers that
		// want a numeric type call `s.parse_int()` /
		// `s.parse_float()` on the payload. Self-referential
		// variants (JArray → JsonValue[], JObject →
		// Map[string, JsonValue]) work because enum payloads
		// are heap-allocated, breaking the size cycle. Pairs
		// with the `json_encode(v)` builtin (and the future
		// `json_parse(s) -> Option[JsonValue]`).
		{
			Name: "JsonValue",
			Variants: []ast.EnumVariant{
				{Name: "JNull"},
				{Name: "JBool", Payloads: []ast.Type{ast.BoolType{}}},
				{Name: "JNumber", Payloads: []ast.Type{ast.StringType{}}},
				{Name: "JString", Payloads: []ast.Type{ast.StringType{}}},
				{Name: "JArray", Payloads: []ast.Type{
					ast.ArrayType{Elem: ast.EnumType{Name: "JsonValue"}},
				}},
				{Name: "JObject", Payloads: []ast.Type{
					ast.StructType{
						Name: "Map",
						Args: []ast.Type{
							ast.StringType{},
							ast.EnumType{Name: "JsonValue"},
						},
					},
				}},
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
		// Map[i32, i32] — first cut of the IndexMap-shaped Map
		// from PR 4 (docs/LANGUAGE-DIRECTION.md). Concrete-typed
		// (i32 keys, i32 values) for now; generic K / V comes in
		// a follow-up that replaces the runtime helpers + wires
		// monomorphisation. Linear-search internals; fixed
		// capacity at construction. The struct is opaque-by-
		// convention; user code constructs via `Map.new(cap)`
		// and reads through methods.
		// Map[K, V] — generic IndexMap-shaped associative
		// container. Linear search internals for now; the
		// IndexMap fingerprint metadata table + Wyhash
		// generalisation is the follow-up. K is restricted to
		// i32-sized scalars or `string`; V is restricted to
		// pointer-sized values (any 4-byte storage type — i32,
		// string, struct / enum / array / slice ptr). i64 / u64
		// / f64 keys + values are deferred. The runtime stores
		// a `keyKind` tag in the buffer header so the linear
		// scan can branch i32-eq vs strcmp without per-
		// instantiation monomorphisation of helper code.
		{
			Name:       "Map",
			TypeParams: []string{"K", "V"},
			Fields: []ast.Param{
				{Name: "data", Type: ast.NumberType{}},
			},
		},
		// MapIter[K, V] — non-allocating cursor-style
		// iterator over Map[K, V]. The struct holds a
		// pointer back to the map's kv buffer plus the
		// current entry index. `m.iter()` constructs a
		// fresh MapIter; iteration uses
		// `it.has_next()` / `it.key()` / `it.value()` /
		// `it.advance()` which all stay i32-shaped on the
		// wasm side (Key / Value are reinterpreted via the
		// type-system substitution path).
		{
			Name:       "MapIter",
			TypeParams: []string{"K", "V"},
			Fields: []ast.Param{
				{Name: "data", Type: ast.NumberType{}},
				{Name: "i", Type: ast.NumberType{}},
			},
		},
		// Url — return type of `url_parse(s)`. Holds the
		// component pieces of an absolute or relative URL.
		// `port = 0` means unspecified (parser defaults
		// follow-up). Empty strings indicate missing
		// sections rather than allocating Option types
		// per-field — keeps the struct flat and the wasm
		// emitter simple.
		{
			Name: "Url",
			Fields: []ast.Param{
				{Name: "scheme", Type: ast.StringType{}},
				{Name: "host", Type: ast.StringType{}},
				{Name: "port", Type: ast.NumberType{}},
				{Name: "path", Type: ast.StringType{}},
				{Name: "query", Type: ast.StringType{}},
				{Name: "fragment", Type: ast.StringType{}},
			},
		},
	}
}

// injectPrelude parses the embedded prelude source (from
// internal/prelude/prelude.lang) and appends its top-level
// declarations to `prog`. Idempotent — re-running on a program
// whose Funcs already contain a prelude marker is a no-op.
//
// The prelude lang code goes through the same parser /
// checker / IR / codegen pipeline as user code, so it
// participates in IR-level optimisations (peephole, dce,
// inlining) and works on every backend without backend-
// specific shims. See docs/LANGUAGE-DIRECTION.md "Stdlib
// implementation strategy" for the rationale.
//
// The prelude is re-parsed on each `Check` call rather than
// cached + cloned. The source is small (a couple hundred
// lines of lang code at most) and parsing it twice is cheap
// next to type-checking; caching would require deep-cloning
// the FuncDecl AST per-call to avoid mutation aliasing
// (receiver-hoisting rewrites the FuncDecl in place).
func injectPrelude(prog *ast.Program) error {
	// Idempotency: if any function in prog.Funcs is already
	// flagged IsPrelude, assume the prelude is injected and
	// skip. Lets `Check` be re-entrant on the same AST —
	// monomorph re-checks the program after rewriting,
	// which would otherwise duplicate the prelude.
	for _, f := range prog.Funcs {
		if f.IsPrelude {
			return nil
		}
	}
	pre, err := parser.Parse(prelude.Source)
	if err != nil {
		return fmt.Errorf("prelude: %w", err)
	}
	// Append funcs, structs, and enums separately. The
	// IsPrelude flag is only on FuncDecl today (struct /
	// enum decls aren't filtered by tests, since their
	// shape is part of the auto-injected stdlib).
	for _, fn := range pre.Funcs {
		fn.IsPrelude = true
		prog.Funcs = append(prog.Funcs, fn)
	}
	prog.Structs = append(prog.Structs, pre.Structs...)
	prog.Enums = append(prog.Enums, pre.Enums...)
	return nil
}

// Check type-checks the program. It returns an aggregated error if any
// problems were found.
func Check(prog *ast.Program) (*Info, error) {
	// Prepend the built-in Option / Result / IoError /
	// JsonValue enums so user code (and the lang prelude)
	// can reference them without an explicit declaration.
	// Each is injected individually if the user hasn't
	// already declared the same name — earlier the
	// "auto-inject only when prog.Enums[0].Name != Option"
	// heuristic skipped EVERY builtin if the user declared
	// their own Option, which broke the prelude's json_encode
	// (uses JsonValue).
	{
		userEnums := map[string]bool{}
		for _, ed := range prog.Enums {
			userEnums[ed.Name] = true
		}
		var inject []*ast.EnumDecl
		for _, ed := range builtinEnumDecls() {
			if !userEnums[ed.Name] {
				inject = append(inject, ed)
			}
		}
		if len(inject) > 0 {
			prog.Enums = append(inject, prog.Enums...)
		}
	}
	// Same shape for the auto-injected structs (Reader,
	// Writer, HttpRequest, HttpResponse, Map, MapIter, Url).
	{
		userStructs := map[string]bool{}
		for _, sd := range prog.Structs {
			userStructs[sd.Name] = true
		}
		var inject []*ast.StructDecl
		for _, sd := range builtinStructDecls() {
			if !userStructs[sd.Name] {
				inject = append(inject, sd)
			}
		}
		if len(inject) > 0 {
			prog.Structs = append(inject, prog.Structs...)
		}
	}
	// Inject the lang-source prelude (small stdlib helpers
	// expressed in lang itself; see internal/prelude). Runs
	// after enums/structs since prelude functions may reference
	// the auto-injected types.
	if err := injectPrelude(prog); err != nil {
		return nil, err
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
		if len(sd.TypeParams) > 0 && !isRuntimeGenericStruct(sd.Name) {
			// Track generic struct decls so the monomorph pass
			// knows which ones to clone, and post-monomorph
			// callers can tell "we used to be generic". The
			// auto-injected `Map[K, V]` is excluded — its
			// runtime is parameterised by an in-buffer
			// `keyKind` tag, so a single shared struct + helper
			// set handles every (K, V) instantiation. Cloning
			// it would split the methods across mangled names
			// the dispatch path doesn't know about.
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
	// `int_to_string(n)` migrated to the lang prelude
	// (internal/prelude/prelude.lang); its signature is
	// registered via the prelude's FuncDecl.
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

	// Map[K, V] — generic IndexMap-shaped associative
	// container per PR 4 of docs/LANGUAGE-DIRECTION.md. The
	// runtime stores a `keyKind` tag in the buffer header
	// alongside cap / len so the linear-search core can
	// dispatch i32.eq vs strcmp for the comparison without
	// per-instantiation monomorphisation.
	keyParam := ast.ParamType{Name: "K"}
	valueParam := ast.ParamType{Name: "V"}
	mapKV := ast.StructType{Name: "Map", Args: []ast.Type{keyParam, valueParam}}
	optionV := ast.EnumType{Name: "Option", Args: []ast.Type{valueParam}}
	// `map_new(cap)` returns a Map with no Args — the call
	// site's destination type (e.g. `var m: Map[i32, string]
	// = map_new(8)`) drives K and V via assignable's "empty-
	// Args generic" relaxation. The IR lowering reads
	// `n.TypeArgs` (stamped by the Var case from the
	// destination's Args) to inject the runtime keyKind tag.
	c.info.FuncSigs["map_new"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.StructType{Name: "Map"},
	}
	registerMapMethod := func(methodName string, params []ast.Type, result ast.Type) {
		mangled := "__method_Map_" + methodName
		c.info.Methods["Map."+methodName] = mangled
		fullParams := append([]ast.Type{mapKV}, params...)
		c.info.FuncSigs[mangled] = &ast.FuncType{Params: fullParams, Result: result}
	}
	registerMapMethod("len", nil, ast.NumberType{})
	registerMapMethod("has", []ast.Type{keyParam}, ast.BoolType{})
	registerMapMethod("get", []ast.Type{keyParam}, optionV)
	registerMapMethod("set", []ast.Type{keyParam, valueParam}, ast.VoidType{})
	registerMapMethod("keys", nil, ast.ArrayType{Elem: keyParam})
	registerMapMethod("values", nil, ast.ArrayType{Elem: valueParam})
	registerMapMethod("delete", []ast.Type{keyParam}, ast.BoolType{})
	registerMapMethod("clear", nil, ast.VoidType{})
	registerMapMethod("get_or", []ast.Type{keyParam, valueParam}, valueParam)
	mapIterKV := ast.StructType{Name: "MapIter", Args: []ast.Type{keyParam, valueParam}}
	registerMapMethod("iter", nil, mapIterKV)

	// Generic array methods. Today only `push` is registered.
	// Routes through the same `__method_<Type>_<name>` mangling
	// + receiver-TypeArgs substitution path that Map's methods
	// use, so `arr.push(v)` on `string[]` type-checks `v` as
	// string while on `JsonValue[]` it type-checks as JsonValue.
	// Codegen aliases the mangled name to a per-stride
	// underlying helper:
	//   - 4-byte stride → __array_append_string (lang prelude).
	//   - 8-byte int    → __array_append_i64    (wat helper).
	// The checker's method-dispatch path overrides the mangled
	// name when the receiver's Elem is 8-byte-int (i64 / u64) so
	// the substitution path picks up the right signature for arg
	// type-checking (`arr.push(v)` on i64[] checks `v` as i64).
	arrayElemParam := ast.ParamType{Name: "T"}
	c.info.Methods["Array.push"] = "__method_Array_push"
	c.info.FuncSigs["__method_Array_push"] = &ast.FuncType{
		Params: []ast.Type{
			ast.ArrayType{Elem: arrayElemParam},
			arrayElemParam,
		},
		Result: ast.ArrayType{Elem: arrayElemParam},
	}
	// Wide (8-byte) pushes: same shape as the 4-byte version
	// but each routes to its own per-stride lang-prelude
	// helper. Two mangled names so the codegen alias map can
	// dispatch: i64/u64 → __array_append_i64, f64 →
	// __array_append_f64. The substitution path picks up the
	// right monomorphic signature at the call site (`v` types
	// as i64 for i64[], as f64 for f64[], etc).
	c.info.FuncSigs["__method_Array_push_i64"] = &ast.FuncType{
		Params: []ast.Type{
			ast.ArrayType{Elem: arrayElemParam},
			arrayElemParam,
		},
		Result: ast.ArrayType{Elem: arrayElemParam},
	}
	c.info.FuncSigs["__method_Array_push_f64"] = &ast.FuncType{
		Params: []ast.Type{
			ast.ArrayType{Elem: arrayElemParam},
			arrayElemParam,
		},
		Result: ast.ArrayType{Elem: arrayElemParam},
	}

	// MapIter[K, V] — paired with Map's iter() above. The
	// receiver has K + V from the map's TypeArgs which
	// flow through the same dispatch-path substitution. The
	// runtime helpers stay i32-shaped; the type system
	// reinterprets Key / Value at the call site.
	registerMapIterMethod := func(methodName string, params []ast.Type, result ast.Type) {
		mangled := "__method_MapIter_" + methodName
		c.info.Methods["MapIter."+methodName] = mangled
		fullParams := append([]ast.Type{mapIterKV}, params...)
		c.info.FuncSigs[mangled] = &ast.FuncType{Params: fullParams, Result: result}
	}
	registerMapIterMethod("has_next", nil, ast.BoolType{})
	registerMapIterMethod("key", nil, keyParam)
	registerMapIterMethod("value", nil, valueParam)
	registerMapIterMethod("advance", nil, ast.VoidType{})

	// Built-in string methods. The receiver type is
	// StringType (not StructType), so we can't use
	// `registerMethod` directly — that helper hardcodes
	// `StructType{Name: structName}` as the first param.
	// Use the same `__method_string_<name>` mangling so the
	// dispatch path picks them up uniformly.
	registerStringMethod := func(methodName string, params []ast.Type, result ast.Type) {
		mangled := "__method_string_" + methodName
		c.info.Methods["string."+methodName] = mangled
		fullParams := append([]ast.Type{ast.StringType{}}, params...)
		c.info.FuncSigs[mangled] = &ast.FuncType{Params: fullParams, Result: result}
	}
	// `starts_with` / `ends_with` / `contains` / `index_of`
	// / `trim` / `to_lower` / `to_upper` / `bytes` / `split`
	// / `replace` migrated to the lang prelude
	// (internal/prelude/prelude.lang); their signatures are
	// registered via the prelude's FuncDecls.
	// `s.is_empty()` lives in the lang prelude
	// (internal/prelude/prelude.lang); the receiver-hoisting
	// machinery + builtin-receivers extension wires it
	// through automatically.
	// `s.repeat(n)` lives in the lang prelude
	// (internal/prelude/prelude.lang).
	// `s.as_bytes()` — non-copying companion to `s.bytes()`. Returns
	// a `[u8]` slice header whose data_ptr aliases the string's
	// payload and whose len is `len(s)`. Sharing the parent's
	// lifetime is fine under the bump allocator (the string's
	// storage lives until the arena tears down).
	registerStringMethod("as_bytes", nil, ast.SliceType{Elem: ast.NumberType{Width: 8, Signed: false}})
	// `s.parse_int()` lives in the lang prelude
	// (internal/prelude/prelude.lang). The receiver-hoisting
	// + dispatch wires it through the same way as any
	// `__method_string_*`.
	// `s.parse_float()` lives in the lang prelude.

	c.info.FuncSigs["string_from_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.StringType{},
	}

	// `base64_encode` / `base64_decode` migrated to the
	// lang prelude (internal/prelude/prelude.lang); their
	// signatures are registered via the prelude's FuncDecls.

	// `hex_encode` / `hex_decode` migrated to the lang
	// prelude (internal/prelude/prelude.lang); their
	// signatures are registered via the prelude's FuncDecls.

	// `url_parse(s)` lives in the lang prelude
	// (internal/prelude/prelude.lang).

	// `__array_append_string(arr, v)` migrated to the lang
	// prelude (internal/prelude/prelude.lang); its
	// signature is registered via the prelude's FuncDecl.

	// `__array_append_jsonvalue(arr, v)` — same shape as
	// `__array_append_string` but for `JsonValue[]`.
	// `json_parse` builds JArray payloads incrementally.
	// Generic per-element-stride append is the right
	// long-term shape; today both lang signatures lower to
	// the same wat helper (4-byte-pointer-stride is
	// type-erased at the wat layer).
	c.info.FuncSigs["__array_append_jsonvalue"] = &ast.FuncType{
		Params: []ast.Type{
			ast.ArrayType{Elem: ast.EnumType{Name: "JsonValue"}},
			ast.EnumType{Name: "JsonValue"},
		},
		Result: ast.ArrayType{Elem: ast.EnumType{Name: "JsonValue"}},
	}

	// `__memcpy(dst, src, n)` / `__memset(dst, b, n)` —
	// thin lang-callable wrappers around wasm's bulk-memory
	// `memory.copy` / `memory.fill`. The doc-roadmap calls
	// them out as the unlock for moving the json buffer
	// family + the Map runtime from hand-written wat into
	// the lang prelude (every growable-byte-buffer pattern
	// needs them). All three params are i32 byte counts /
	// pointers; the helpers return void. Backends without
	// bulk-memory (eg arm32 today) trip an "unsupported"
	// path during codegen — wat is for now the only consumer.
	c.info.FuncSigs["__memcpy"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["__memset"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.NumberType{}, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	// `__alloc_u8(n)` returns a fresh `u8[]` of length n,
	// zero-initialised. Pairs with `__memcpy` / `__memset` /
	// the `[u8] → i32` data-pointer cast so prelude code can
	// build a single-pass byte buffer.
	c.info.FuncSigs["__alloc_u8"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
	}
	// Raw-memory escape hatches for prelude code that
	// builds typed-pointer arrays (`__array_append_string`)
	// or runtime structures (the Map runtime migration).
	// `__alloc(n)` returns a raw n-byte block, no length
	// prefix; `__load_i32` / `__store_i32` peek and poke a
	// 4-byte word at any address. Out-of-bounds traps at
	// the wasm level — the prelude is expected to bounds-
	// check at the lang level.
	c.info.FuncSigs["__alloc"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["__load_i32"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["__store_i32"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	// `__load_i64` / `__store_i64` — wide-int peek/poke
	// counterparts. Lets the lang prelude build wide-element
	// data structures (today: `__array_append_i64`) using the
	// same compose-with-primitives pattern that the 4-byte
	// path uses, instead of duplicating the helper at the wat
	// layer. Same out-of-bounds-traps-on-wasm-level contract.
	c.info.FuncSigs["__load_i64"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["__store_i64"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.NumberType{Width: 64, Signed: true}},
		Result: ast.VoidType{},
	}
	// `__load_f64` / `__store_f64` — wide-float counterparts
	// for the same reason. Pairs with `__array_append_f64` in
	// the lang prelude.
	c.info.FuncSigs["__load_f64"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.FloatType{Width: 64},
	}
	c.info.FuncSigs["__store_f64"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.FloatType{Width: 64}},
		Result: ast.VoidType{},
	}

	// `url_encode(s)` / `url_decode(s)` live in the lang
	// prelude (internal/prelude/prelude.lang).

	// `query_parse(s)` lives in the lang prelude.

	// `json_encode(v)` lives in the lang prelude.
	// `json_parse` migrated to the lang prelude
	// (internal/prelude/prelude.lang); its signature is
	// registered via the prelude's FuncDecl.

	// Built-in numeric methods. The receiver type is `NumberType`
	// keyed by width + signedness; the dispatch path above maps
	// `i32` / `u32` / `i64` / `u64` value types to the
	// corresponding `__method_<typename>_<method>` mangled name.
	// `i32.to_string()` / `u32.to_string()` /
	// `i64.to_string()` / `u64.to_string()` migrated to the
	// lang prelude (internal/prelude/prelude.lang) — its
	// receiver-method declarations register the
	// `string.*_to_string` mangled names automatically via
	// the receiver-hoisting pass below.

	// `f32.to_string()` / `f64.to_string()` migrated to the
	// lang prelude (internal/prelude/prelude.lang).

	// First pass: gather all top-level signatures so functions can call
	// each other in any order. Methods are hoisted to mangled
	// top-level names (`__method_<Type>_<Name>`) with the receiver
	// prepended to the parameter list, so codegen never has to know
	// about methods.
	for _, fn := range prog.Funcs {
		if fn.Receiver != nil {
			var typeName string
			switch rt := fn.Receiver.Type.(type) {
			case ast.StructType:
				if _, ok := c.info.Structs[rt.Name]; !ok {
					c.errf(fn.P, "method receiver references unknown struct %q", rt.Name)
					continue
				}
				typeName = rt.Name
			case ast.EnumType:
				if _, ok := c.info.Enums[rt.Name]; !ok {
					c.errf(fn.P, "method receiver references unknown enum %q", rt.Name)
					continue
				}
				typeName = rt.Name
			case ast.StringType:
				typeName = "string"
			case ast.NumberType:
				// Same width/sign mapping the dispatch path uses
				// for numeric method calls — keeps receiver and
				// call-site naming in lockstep.
				switch {
				case rt.NormalWidth() == 64 && rt.IsSigned():
					typeName = "i64"
				case rt.NormalWidth() == 64 && !rt.IsSigned():
					typeName = "u64"
				case !rt.IsSigned():
					typeName = "u32"
				default:
					typeName = "i32"
				}
			case ast.FloatType:
				if rt.NormalWidth() == 64 {
					typeName = "f64"
				} else {
					typeName = "f32"
				}
			default:
				c.errf(fn.P, "method receiver type must be a struct, enum, or built-in type, got %s", fn.Receiver.Type)
				continue
			}
			methodKey := typeName + "." + fn.Name
			if _, dup := c.info.Methods[methodKey]; dup {
				c.errf(fn.P, "method %q on %s redeclared", fn.Name, typeName)
				continue
			}
			mangled := "__method_" + typeName + "_" + fn.Name
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
	// Polymorphic empty-array literal (`[]`) — its concrete
	// element type is filled in from `dst` by settleEmptyArray.
	if da, dok := dst.(ast.ArrayType); dok {
		if sa, sok := src.(ast.ArrayType); sok && sa.Elem == nil && da.Elem != nil {
			return true
		}
	}
	d, dok := dst.(ast.EnumType)
	s, sok := src.(ast.EnumType)
	if dok && sok && d.Name == s.Name && len(s.Args) == 0 && len(d.Args) > 0 {
		return true
	}
	// Generic enum with polymorphic-numeric type-args inferred
	// from a literal payload — `Some(1)` returns
	// `Option[NumberType{Polymorphic}]`, and the destination
	// `Option[i64]` flows in here after settleNumeric stamped
	// the literal's width. Walk pairwise: each src Arg must be
	// assignable to its dst Arg (recursive), so nested enums
	// like `Option[Option[i64]] = Some(Some(1))` also work.
	if dok && sok && d.Name == s.Name && len(d.Args) == len(s.Args) && len(d.Args) > 0 {
		for i := range d.Args {
			if !assignable(d.Args[i], s.Args[i]) {
				return false
			}
		}
		return true
	}
	// Same relaxation for generic struct values: a builtin like
	// `map_new(cap)` returns `StructType{Name: "Map"}` with no
	// Args; the destination context (e.g. `var m: Map[i32,
	// string] = map_new(8);`) names the concrete K + V. The
	// Var / argument-checking sites stamp the resolved args
	// back onto the source expression so the IR lowering can
	// see them.
	ds, dsok := dst.(ast.StructType)
	ss, ssok := src.(ast.StructType)
	if dsok && ssok && ds.Name == ss.Name && len(ss.Args) == 0 && len(ds.Args) > 0 {
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
	case *ast.Defer:
		// Just type-check the expression; its result is
		// discarded (defer is statement-shaped, not
		// expression-shaped). The IR builder is responsible
		// for replaying the expression at function exits.
		c.checkExpr(n.Expr, s)
	case *ast.Arena:
		// `arena { … }` introduces a new lexical scope same
		// as a plain block; the special semantics (cursor
		// snap on exit) are an IR-level concern. The body's
		// scope shadows but doesn't leak — same model as
		// any other block.
		c.checkBlock(n.Body, s)
	case *ast.Var:
		if _, dup := s.names[n.Name]; dup {
			c.errf(n.P, "variable %q already declared in this scope", n.Name)
		}
		got := c.checkExpr(n.Init, s)
		if n.Type != nil {
			c.settleNumeric(n.Init, n.Type)
			got = postSettleType(n.Init, got)
			// Generic-struct destination inference: a builtin
			// like `map_new(cap)` returns `Map` with no Args;
			// the destination's `Map[K, V]` Args propagate
			// back so the IR lowering can stamp the runtime
			// keyKind tag.
			c.stampStructTypeArgs(n.Init, n.Type)
		}
		if n.Type == nil {
			if got == nil {
				return
			}
			// Polymorphic empty array `[]` with no annotation
			// to settle from — surface the original missing-
			// annotation error here rather than silently
			// recording a nil-elem type.
			if at, ok := got.(ast.ArrayType); ok && at.Elem == nil {
				c.errf(n.P, "empty array literal needs a type annotation")
				return
			}
			n.Type = got
		} else if got != nil && !assignable(n.Type, got) {
			c.errf(n.P, "cannot assign %s to variable of type %s", got, n.Type)
		}
		s.names[n.Name] = n.Type
		c.info.VarTypes[n] = n.Type
		c.info.Locals[c.current] = append(c.info.Locals[c.current], n)
	case *ast.Destructure:
		// `let (a, b, …) = expr;` — Init must produce a
		// tuple of arity len(Names). Each name is registered
		// as a local in the enclosing scope, plus a hidden
		// temp local that holds the tuple pointer so the IR
		// can do one evaluation followed by per-name field
		// loads.
		got := c.checkExpr(n.Init, s)
		tup, ok := got.(ast.TupleType)
		if !ok {
			if got != nil {
				c.errf(n.P, "tuple destructure needs a tuple expression, got %s", got)
			}
			return
		}
		if len(tup.Elems) != len(n.Names) {
			c.errf(n.P, "tuple has %d elements, but %d names given", len(tup.Elems), len(n.Names))
			return
		}
		// Hidden temp holds the tuple pointer between the
		// init's evaluation and the per-name loads. Name is
		// uniqued by source position so multiple destructures
		// in the same function don't collide.
		tempName := fmt.Sprintf("__destruct_%d_%d", n.P.Line, n.P.Col)
		n.TempName = tempName
		tempVar := &ast.Var{P: n.P, Name: tempName, Type: tup}
		s.names[tempName] = tup
		c.info.VarTypes[tempVar] = tup
		c.info.Locals[c.current] = append(c.info.Locals[c.current], tempVar)
		for i, name := range n.Names {
			if _, dup := s.names[name]; dup {
				c.errf(n.P, "variable %q already declared in this scope", name)
				continue
			}
			elemT := tup.Elems[i]
			v := &ast.Var{P: n.P, Name: name, Type: elemT}
			s.names[name] = elemT
			c.info.VarTypes[v] = elemT
			c.info.Locals[c.current] = append(c.info.Locals[c.current], v)
		}
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

// checkMatchExpr validates an expression-position `match` and
// returns the unified arm type. Same scrutinee, payload-binding,
// guard, and exhaustiveness rules as checkMatch — the difference
// is each arm body is an Expr (not a Block), and every arm body
// must produce the same type so the construct itself has a
// single result type.
func (c *checker) checkMatchExpr(n *ast.MatchExpr, s *scope) ast.Type {
	tagT := c.checkExpr(n.Tag, s)
	if tagT == nil {
		return nil
	}
	et, ok := tagT.(ast.EnumType)
	if !ok {
		c.errf(n.Tag.Pos(), "match scrutinee must be an enum value, got %s", tagT)
		return nil
	}
	ed, ok := c.info.Enums[et.Name]
	if !ok {
		c.errf(n.Tag.Pos(), "unknown enum %q", et.Name)
		return nil
	}
	sub := map[string]ast.Type{}
	if len(ed.TypeParams) == len(et.Args) {
		for i, p := range ed.TypeParams {
			sub[p] = et.Args[i]
		}
	}
	covered := map[string]bool{}
	sawWildcard := false
	var result ast.Type
	unify := func(armT ast.Type, p ast.Position) {
		if armT == nil {
			return
		}
		if result == nil {
			result = armT
			return
		}
		if !ast.Equal(result, armT) {
			c.errf(p, "match-expression arms differ: %s vs %s", result, armT)
		}
	}
	for i, arm := range n.Arms {
		if arm.IsWildcard {
			if i != len(n.Arms)-1 {
				c.errf(arm.P, "wildcard `_` arm must be last in the match")
			}
			if arm.Guard == nil {
				sawWildcard = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errf(arm.Guard.Pos(), "match guard must be boolean, got %s", gt)
				}
			}
			unify(c.checkExpr(arm.Body, s), arm.Body.Pos())
			continue
		}
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
			unify(c.checkExpr(arm.Body, s), arm.Body.Pos())
			continue
		}
		if covered[arm.VariantName] {
			c.errf(arm.P, "variant %q already covered earlier in this match", arm.VariantName)
		}
		if arm.Guard == nil {
			covered[arm.VariantName] = true
		}
		if len(arm.Bindings) != len(variant.Payloads) {
			c.errf(arm.P, "variant %s has %d payload(s), got %d binding(s)",
				arm.VariantName, len(variant.Payloads), len(arm.Bindings))
		}
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
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errf(arm.Guard.Pos(), "match guard must be boolean, got %s", gt)
			}
		}
		unify(c.checkExpr(arm.Body, armScope), arm.Body.Pos())
	}
	if !sawWildcard {
		for _, v := range ed.Variants {
			if !covered[v.Name] {
				c.errf(n.P, "match-expression is not exhaustive — variant %s of enum %s is not covered (add an arm or use `_`)",
					v.Name, ed.Name)
			}
		}
	}
	if isFloat(result) {
		n.IsFloat = true
	}
	return result
}

// inferUseParam fills in `fn`'s first-parameter type by reading
// the source-call's signature. The parser left the slot nil
// when the user wrote `use IDENT <- EXPR;` (no `: TYPE`); this
// function looks up the call's callee in `c.info.FuncSigs`,
// finds the trailing function-typed parameter (the callback
// slot the callback is being passed into), and stamps the
// callback's first parameter from there. On failure (callee
// not a bare identifier we can resolve, or the receiving
// param isn't function-typed) it records an error pointing at
// the `use` site.
func (c *checker) inferUseParam(fn *ast.FuncDecl, outer *scope) {
	if len(fn.Params) == 0 || fn.Params[0].Type != nil {
		return
	}
	src := fn.UseInferSource
	if src == nil {
		return
	}
	id, ok := src.Callee.(*ast.Ident)
	if !ok {
		c.errf(fn.P, "use: cannot infer binding type for non-identifier source — add an explicit `: TYPE` annotation")
		return
	}
	sig, ok := c.info.FuncSigs[id.Name]
	if !ok {
		// Maybe it's a local function in the outer scope.
		if t, ok := outer.lookup(id.Name); ok {
			if ft, isFunc := t.(*ast.FuncType); isFunc {
				sig = ft
			}
		}
	}
	if sig == nil || len(sig.Params) == 0 {
		c.errf(fn.P, "use: callee %q has no signature; add an explicit `: TYPE` annotation", id.Name)
		return
	}
	last := sig.Params[len(sig.Params)-1]
	cbSig, isFunc := last.(*ast.FuncType)
	if !isFunc {
		c.errf(fn.P, "use: callee %q's last parameter isn't a function — add an explicit `: TYPE` annotation", id.Name)
		return
	}
	if len(cbSig.Params) == 0 {
		c.errf(fn.P, "use: callee %q's callback takes no arguments — there's nothing to bind", id.Name)
		return
	}
	fn.Params[0].Type = cbSig.Params[0]
}

// checkLocalFunc type-checks a nested function and records its
// captured outer-scope variables. The local name is bound in the
// surrounding scope so subsequent calls (and recursion through the
// inner name) work; the body checks under a fresh root scope with
// its own params, plus a capture-sink that registers any outer-scope
// name the body reads.
func (c *checker) checkLocalFunc(fn *ast.FuncDecl, outer *scope) {
	// `use IDENT <- EXPR;` synthesises a callback FuncDecl whose
	// first parameter has no source-level type annotation. Infer
	// it from the receiving call's signature: that call's last
	// parameter is itself a FuncType (the callback slot we're
	// being passed into), and its first parameter is the binding
	// type. Generic-callee inference is a follow-up — for now we
	// require the callee to resolve to a concrete signature.
	if fn.UseInferSource != nil {
		c.inferUseParam(fn, outer)
	}
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
		// Capture eligibility. Scalars (i32 / i64 / f32 / f64 /
		// boolean) live directly in the env block; pointer-
		// shaped types (string, T[], [T], structs, enums,
		// tuples, function values) store their 4-byte heap
		// reference in the same slot — the heap object itself
		// stays where the outer scope put it. Lifetime is
		// "captures must outlive the closure", same rule that
		// applies to slices, enforced socially via the bump
		// allocator's per-arena reset.
		//
		// Reject only the types that genuinely have no runtime
		// representation: VoidType (no value) and ParamType
		// (an unresolved generic placeholder — should never
		// surface here in practice but guard for safety).
		switch t.(type) {
		case ast.VoidType, ast.ParamType:
			c.errf(fn.P, "captured variable %q has unsupported type %s", name, t)
		default:
			captured[name] = t
			captureOrder = append(captureOrder, name)
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
		if n.IsFloat {
			return ast.FloatType{Width: n.FloatWidth}
		}
		if n.Width != 0 {
			return ast.NumberType{Width: n.Width, Signed: !n.IsUnsigned}
		}
		return ast.NumberType{Polymorphic: true}
	case *ast.CastExpr:
		// Numeric ↔ numeric is the common case. The one
		// exception: a `[u8]` slice or `u8[]` array can cast
		// to `i32` to recover its data-pointer for the
		// bulk-memory primitives (__memcpy / __memset). It's
		// an explicit low-level escape hatch — useful inside
		// prelude buffer-management helpers, marked by the
		// cast at the source level.
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
		// Any owned array, slice, string, or struct → i32 —
		// recover the data / wrapper pointer for the bulk-
		// memory primitives. All four lower to a single i32 at
		// runtime; the cast is the source-level escape hatch
		// the prelude uses to call __memcpy / __store_i32
		// against the underlying memory.
		if nt, ok := n.Target.(ast.NumberType); ok && nt.NormalWidth() == 32 {
			switch inner.(type) {
			case ast.ArrayType, ast.SliceType, ast.StringType, ast.StructType:
				return n.Target
			}
		}
		// Reverse direction: `i32 → T[]`, `i32 → string`,
		// and `i32 → struct` promote a raw pointer back to a
		// typed handle. The runtime layout is identical (lang
		// ABI for arrays/strings is "value = data pointer,
		// length prefix at base-4"; for structs, "value = base
		// pointer, fields at constant offsets") — only the
		// type-level view changes. Used by the prelude when a
		// builtin returns a freshly allocated raw block that
		// the caller wants to expose as a typed collection
		// (`__array_append_string`'s rebuild loop) or as a
		// wrapper struct (`map_new`'s Map handle).
		if nt, ok := inner.(ast.NumberType); ok && nt.NormalWidth() == 32 {
			switch n.Target.(type) {
			case ast.ArrayType, ast.StringType, ast.StructType:
				return n.Target
			}
		}
		c.errf(n.P, "cannot cast %s to %s; only numeric casts (and [u8]/u8[]/string ↔ i32 data-pointer hops, plus i32 → T[]) are supported", inner, n.Target)
		return n.Target
	case *ast.BoolLit:
		return ast.BoolType{}
	case *ast.StringLit:
		return ast.StringType{}
	case *ast.FString:
		// Build the desugared `+`-chain right here so method-call
		// dispatch on each `.to_string()` gets resolved via the
		// regular checker path. The IR then lowers the desugared
		// expression rather than the FString node itself; the
		// formatter still reads n.Parts to rebuild the f"..." form.
		// Empty f-strings desugar to a single empty string literal.
		if n.Desugared == nil {
			if len(n.Parts) == 0 {
				n.Desugared = &ast.StringLit{P: n.P, Value: ""}
			} else {
				var built ast.Expr
				for _, part := range n.Parts {
					var piece ast.Expr
					if part.Expr != nil {
						piece = &ast.Call{
							P: n.P,
							Callee: &ast.FieldAccess{
								P:      n.P,
								Target: part.Expr,
								Field:  "to_string",
							},
						}
					} else {
						piece = &ast.StringLit{P: n.P, Value: part.Lit}
					}
					if built == nil {
						built = piece
					} else {
						built = &ast.Binary{P: n.P, Op: "+", Left: built, Right: piece}
					}
				}
				n.Desugared = built
			}
		}
		// Recursively type-check the desugared chain. The Binary
		// nodes get their IsStringConcat flag set the same way an
		// ordinary `"a" + b.to_string()` would.
		_ = c.checkExpr(n.Desugared, s)
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
			// Polymorphic-empty marker, resolved by the
			// surrounding context (Var annotation, function
			// arg, return type) via settleEmptyArray below.
			// If nothing settles it, the var-assignment site
			// raises the missing-annotation error.
			return ast.ArrayType{Elem: nil}
		}
		elemT := c.checkExpr(n.Elems[0], s)
		for _, el := range n.Elems[1:] {
			t := c.checkExpr(el, s)
			if t != nil && elemT != nil && !ast.Equal(t, elemT) {
				c.errf(el.Pos(), "array element type %s, expected %s", t, elemT)
			}
		}
		n.ElemType = elemT
		return ast.ArrayType{Elem: elemT}
	case *ast.Index:
		at := c.checkExpr(n.Array, s)
		it := c.checkExpr(n.Idx, s)
		if it != nil && !ast.Equal(it, ast.NumberType{}) {
			c.errf(n.Idx.Pos(), "index must be an integer, got %s", it)
		}
		if arr, ok := at.(ast.ArrayType); ok {
			n.ElemType = arr.Elem
			return arr.Elem
		}
		if sl, ok := at.(ast.SliceType); ok {
			n.IsSlice = true
			n.ElemType = sl.Elem
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
			n.ElemType = arr.Elem
			return ast.SliceType{Elem: arr.Elem}
		}
		if sl, ok := st.(ast.SliceType); ok {
			n.SourceIsSlice = true
			n.ElemType = sl.Elem
			return sl
		}
		if _, ok := st.(ast.StringType); ok {
			n.IsString = true
			return ast.StringType{}
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
				// Pre-settle polymorphic numerics against the
				// declared payload type so a non-generic variant
				// like `enum Wide { W(i64, i32) }` accepts a bare
				// literal — `W(8589934592, 7)` settles its first
				// arg to i64 before checkExpr runs. Generic
				// payloads (ParamType) skip this pass and rely on
				// the destination annotation (Option[i64]) to flow
				// in via monomorph; pre-settle is a no-op for
				// non-numeric literal positions.
				for i, a := range n.Args {
					if i >= len(vr.payloads) {
						break
					}
					if _, isParam := vr.payloads[i].(ast.ParamType); !isParam {
						c.settleNumeric(a, vr.payloads[i])
					}
				}
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
		// struct or enum value with a method of that name. We rewrite
		// the Call node in place to `mangledName(target, args)` so the
		// rest of the pipeline (codegen, IR) only ever sees a regular
		// function call.
		if fa, ok := n.Callee.(*ast.FieldAccess); ok {
			tt := c.checkExpr(fa.Target, s)
			var typeName string
			switch t := tt.(type) {
			case ast.StructType:
				typeName = t.Name
			case ast.EnumType:
				typeName = t.Name
			case ast.StringType:
				_ = t
				typeName = "string"
			case ast.NumberType:
				// Method dispatch on integer types names by
				// width / signedness so a `t.to_string()` on
				// an i64 value can resolve to a different
				// (i64-aware) helper than the i32 variant.
				switch {
				case t.NormalWidth() == 64 && t.IsSigned():
					typeName = "i64"
				case t.NormalWidth() == 64 && !t.IsSigned():
					typeName = "u64"
				case !t.IsSigned():
					typeName = "u32"
				default:
					typeName = "i32"
				}
			case ast.FloatType:
				// f32 / f64 split same way.
				if t.NormalWidth() == 64 {
					typeName = "f64"
				} else {
					typeName = "f32"
				}
			case ast.ArrayType:
				// Generic array methods (today: `push`). Treated
				// as if Array were a one-type-param generic
				// struct so the receiver-TypeArgs substitution
				// path applies — `string[].push(v)` checks `v`
				// as string, `JsonValue[].push(v)` as JsonValue.
				_ = t
				typeName = "Array"
			}
			if typeName != "" {
				key := typeName + "." + fa.Field
				if mangled, ok := c.info.Methods[key]; ok {
					n.Callee = &ast.Ident{P: fa.P, Name: mangled}
					n.Args = append([]ast.Expr{fa.Target}, n.Args...)
					// Carry the receiver's TypeArgs (if any) so
					// the call-checking path below can substitute
					// ParamType-typed entries in the method's
					// registered signature against the
					// instantiation's concrete arguments. This
					// is what makes `(m: Map[string, i32]).set(k,
					// v)` type-check `k` as string and `v` as
					// i32 when the registered sig uses
					// ParamType("K") / ParamType("V").
					if st, ok := tt.(ast.StructType); ok && len(st.Args) > 0 {
						n.TypeArgs = st.Args
					}
					// Array's `Args` is just the single Elem
					// type — wrap it so the same substitution
					// path treats it as `[T]` with T = Elem.
					if at, ok := tt.(ast.ArrayType); ok {
						n.TypeArgs = []ast.Type{at.Elem}
						// Per-stride dispatch for `arr.push(v)`.
						// 4-byte (i32 / f32 / pointer) → existing
						//   __method_Array_push → __array_append_string
						// 8-byte int (i64 / u64) → __method_Array_push_i64
						//   → __array_append_i64 (lang prelude).
						// 8-byte float (f64) → __method_Array_push_f64
						//   → __array_append_f64 (lang prelude).
						// Other strides (sub-i32) aren't wired
						// up — error clearly at the call site.
						if mangled == "__method_Array_push" {
							sz := ast.ElemSizeBytes(at.Elem)
							if sz == 8 {
								if _, isFloat := at.Elem.(ast.FloatType); isFloat {
									mangled = "__method_Array_push_f64"
								} else {
									mangled = "__method_Array_push_i64"
								}
								n.Callee = &ast.Ident{P: fa.P, Name: mangled}
							} else if sz != 4 {
								c.errf(fa.P, "arr.push() on %s[] is not yet supported (only 4- and 8-byte-stride elements are wired up)", at.Elem)
							}
						}
					}
					// Wide-V Map: `m.values()` would return a
					// `V[]` whose entries are still cell-pointer
					// boxes (the wat helper sees i32-stride),
					// which would silently mis-index on the lang
					// side. Reject explicitly until the helper
					// learns to unbox-into-a-wide-stride array.
					if mangled == "__method_Map_values" {
						if st, ok := tt.(ast.StructType); ok && len(st.Args) >= 2 {
							v := st.Args[1]
							if isWideMapValueType(v) {
								c.errf(fa.P, "Map[K, %s].values() is not yet supported (wide V)", v)
							}
						}
					}
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
		// Method calls on a generic struct's instantiation: the
		// dispatch path stamped n.TypeArgs from the receiver's
		// concrete Args. Substitute those into the registered
		// method signature so per-instantiation argument types
		// (`Map[string, i32].set` taking string + i32 instead
		// of the registered `K` + `V`) flow through the regular
		// argument-checking + return-type path below.
		methodSubResult := ft.Result
		if id, ok := n.Callee.(*ast.Ident); ok && len(n.TypeArgs) > 0 &&
			strings.HasPrefix(id.Name, "__method_") {
			// Recover the type-param list from the owning struct
			// (or enum) decl whose name appears between
			// `__method_` and the trailing `_<MethodName>`.
			rest := id.Name[len("__method_"):]
			// Split at the FIRST underscore so multi-word
			// method names like `Map_has_next` resolve to
			// `Map` (not `Map_has`). Type names themselves
			// can't contain underscores by parser rule.
			if idx := strings.Index(rest, "_"); idx > 0 {
				typeName := rest[:idx]
				var typeParams []string
				if sd := c.info.Structs[typeName]; sd != nil {
					typeParams = sd.TypeParams
				} else if ed := c.info.Enums[typeName]; ed != nil {
					typeParams = ed.TypeParams
				} else if typeName == "Array" {
					// Array isn't an actual decl — it's the
					// builtin `T[]` type-constructor. Synthesise
					// the one-element type-param list so the
					// substitution path sees `T` and substitutes
					// against the receiver's Elem type.
					typeParams = []string{"T"}
				}
				if len(typeParams) == len(n.TypeArgs) {
					sub := make(map[string]ast.Type, len(typeParams))
					for i, tp := range typeParams {
						sub[tp] = n.TypeArgs[i]
					}
					substitutedParams := make([]ast.Type, len(ft.Params))
					for i, p := range ft.Params {
						substitutedParams[i] = substituteType(p, sub)
					}
					ft = &ast.FuncType{
						Params: substitutedParams,
						Result: substituteType(ft.Result, sub),
					}
					methodSubResult = ft.Result
				}
			}
		}
		_ = methodSubResult
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
		// If exactly one side is a concrete float and the other
		// is a polymorphic numeric literal, promote the literal
		// to that float type before requireFloat fires. Lets
		// `r * 2` and `r <= 0` work when r is f32/f64. The same
		// trick the polymorphic-int side does via
		// commonIntegerWidth, generalised to cross-class
		// (int-literal → float-context) promotion.
		if ft, ok := lt.(ast.FloatType); ok && !ft.Polymorphic {
			if rn, ok := rt.(ast.NumberType); ok && rn.Polymorphic {
				c.settleNumeric(n.Right, ft)
				rt = postSettleType(n.Right, rt)
			}
		}
		if ft, ok := rt.(ast.FloatType); ok && !ft.Polymorphic {
			if ln, ok := lt.(ast.NumberType); ok && ln.Polymorphic {
				c.settleNumeric(n.Left, ft)
				lt = postSettleType(n.Left, lt)
			}
		}
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
			if ft, ok := t.(ast.FloatType); ok {
				n.IsFloat = true
				// Propagate polymorphism — `-3.14` should still
				// be polymorphic so it can settle to f32 / f64.
				return ft
			}
			c.requireNumber(n.P, t, n.Op)
			// Propagate the operand's NumberType (including its
			// Polymorphic flag) so unary minus on a polymorphic
			// literal stays polymorphic; otherwise `var s: i8 =
			// -7` couldn't settle the literal.
			if nt, ok := t.(ast.NumberType); ok {
				return nt
			}
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
	case *ast.IfExpr:
		ct := c.checkExpr(n.Cond, s)
		if ct != nil && !ast.Equal(ct, ast.BoolType{}) {
			c.errf(n.Cond.Pos(), "if-expression condition must be boolean, got %s", ct)
		}
		tt := c.checkExpr(n.Then, s)
		et := c.checkExpr(n.Else, s)
		if tt != nil && et != nil && !ast.Equal(tt, et) {
			c.errf(n.P, "if-expression branches differ: %s vs %s", tt, et)
		}
		result := tt
		if result == nil {
			result = et
		}
		if isFloat(result) {
			n.IsFloat = true
		}
		return result
	case *ast.MatchExpr:
		return c.checkMatchExpr(n, s)
	case *ast.TryOp:
		// Postfix `?` covers two source enums:
		//   - Option[T]?    yields T; on None,   returns None
		//                                         (encl. fn returns Option[_])
		//   - Result[T,E]?  yields T; on Err(e), returns Err(e) unchanged
		//                                         (encl. fn returns Result[_, E])
		//
		// E must match exactly between source and enclosing
		// return — the Err is forwarded as-is, no From-conversion.
		inner := c.checkExpr(n.Inner, s)
		if inner == nil {
			return nil
		}
		if c.current == nil {
			c.errf(n.P, "`?` operator can only be used inside a function")
			return nil
		}
		ret := c.current.ReturnType
		retEnum, retOK := ret.(ast.EnumType)
		srcEnum, srcOK := inner.(ast.EnumType)
		if !srcOK {
			c.errf(n.P, "`?` operator requires an Option or Result value, got %s", inner)
			return nil
		}
		switch srcEnum.Name {
		case "Option":
			if len(srcEnum.Args) != 1 {
				c.errf(n.P, "malformed Option type %s", inner)
				return nil
			}
			if !retOK || retEnum.Name != "Option" || len(retEnum.Args) != 1 {
				c.errf(n.P, "`?` on Option requires the surrounding function to return Option[_], got %s", ret)
				return nil
			}
			n.Kind = ast.TryKindOption
			n.Type = srcEnum.Args[0]
			return n.Type
		case "Result":
			if len(srcEnum.Args) != 2 {
				c.errf(n.P, "malformed Result type %s", inner)
				return nil
			}
			if !retOK || retEnum.Name != "Result" || len(retEnum.Args) != 2 {
				c.errf(n.P, "`?` on Result requires the surrounding function to return Result[_, E], got %s", ret)
				return nil
			}
			if !ast.Equal(srcEnum.Args[1], retEnum.Args[1]) {
				c.errf(n.P, "`?` on Result[_, %s] but the surrounding function returns Result[_, %s]; the error types must match",
					srcEnum.Args[1], retEnum.Args[1])
				return nil
			}
			n.Kind = ast.TryKindResult
			n.Type = srcEnum.Args[0]
			return n.Type
		default:
			c.errf(n.P, "`?` operator requires an Option or Result value, got %s", inner)
			return nil
		}
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
	case *ast.MapLit:
		// Pick K and V from the first entry's key / value
		// types, then check the rest against those. Empty
		// literals fall back to `Map[i32, i32]` so that a
		// trailing `var m: Map[i32, i32] = Map {}` (or any
		// destination-typed empty map) keeps working without
		// the destination-driven inference machinery.
		var keyType ast.Type = ast.NumberType{}
		var valueType ast.Type = ast.NumberType{}
		if len(n.Entries) > 0 {
			kt := c.checkExpr(n.Entries[0].Key, s)
			vt := c.checkExpr(n.Entries[0].Value, s)
			kt = postSettleType(n.Entries[0].Key, kt)
			vt = postSettleType(n.Entries[0].Value, vt)
			if kt != nil {
				keyType = kt
			}
			if vt != nil {
				valueType = vt
			}
			// Reject keys we can't compare yet (i64 / float /
			// struct / enum / array / slice). String + i32-
			// sized scalar are allowed.
			switch keyType.(type) {
			case ast.NumberType, ast.StringType:
				// fine
			default:
				c.errf(n.Entries[0].Key.Pos(), "map key type %s is not yet supported (use i32 or string)", keyType)
			}
		}
		// Re-check entries with the inferred K / V as the
		// expected type so polymorphic numeric literals settle
		// to the right width and same-type-must-be-same is
		// enforced.
		for i, ent := range n.Entries {
			if i > 0 {
				c.checkExpr(ent.Key, s)
				c.checkExpr(ent.Value, s)
			}
			c.settleNumeric(ent.Key, keyType)
			c.settleNumeric(ent.Value, valueType)
			kt := postSettleType(ent.Key, keyType)
			vt := postSettleType(ent.Value, valueType)
			if kt != nil && !ast.Equal(kt, keyType) {
				c.errf(ent.Key.Pos(), "map key type %s, expected %s", kt, keyType)
			}
			if vt != nil && !ast.Equal(vt, valueType) {
				c.errf(ent.Value.Pos(), "map value type %s, expected %s", vt, valueType)
			}
		}
		n.KeyType = keyType
		n.ValueType = valueType
		return ast.StructType{Name: "Map", Args: []ast.Type{keyType, valueType}}
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
// isRuntimeGenericStruct names the auto-injected struct types
// whose generic args are resolved at the type-system layer but
// share a single concrete struct + helper set at the wasm
// runtime. The monomorpher skips these so the helper-method
// dispatch keeps working unchanged.
func isRuntimeGenericStruct(name string) bool {
	return name == "Map" || name == "MapIter"
}

// stampStructTypeArgs flows TypeArgs from a destination struct
// type into a source Call expression that returned the same
// struct without args. Only the auto-injected `map_new` builtin
// uses this today — its return type is `Map` (no Args), and the
// destination context (Var Type / Assign target) names the
// concrete K + V. The IR lowering reads `Call.TypeArgs` to bake
// in runtime tags (keyKind) at construction.
func (c *checker) stampStructTypeArgs(e ast.Expr, dst ast.Type) {
	dStruct, ok := dst.(ast.StructType)
	if !ok || len(dStruct.Args) == 0 {
		return
	}
	call, ok := e.(*ast.Call)
	if !ok || len(call.TypeArgs) > 0 {
		return
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return
	}
	if sig, ok := c.info.FuncSigs[id.Name]; ok {
		if rs, ok := sig.Result.(ast.StructType); ok && rs.Name == dStruct.Name && len(rs.Args) == 0 {
			call.TypeArgs = dStruct.Args
		}
	}
}

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
	case ast.ArrayType:
		// Array-literal element-type propagation: `var x: [u8] =
		// [1, 2, 3]` should settle each element to u8 so the IR
		// emits 1-byte stores. Stamp the AST node's ElemType too
		// so the IR's ArrayLit lowering picks the right stride.
		if al, ok := e.(*ast.ArrayLit); ok {
			al.ElemType = hn.Elem
			for _, el := range al.Elems {
				c.settleNumeric(el, hn.Elem)
			}
		}
	case ast.SliceType:
		if al, ok := e.(*ast.ArrayLit); ok {
			al.ElemType = hn.Elem
			for _, el := range al.Elems {
				c.settleNumeric(el, hn.Elem)
			}
		}
	case ast.EnumType:
		// Variant constructor with a destination annotation:
		// `var o: Option[i64] = Some(1);` — the literal `1`
		// otherwise defaults to i32 and the assignment fails.
		// Build the type-param substitution from the hint's
		// Args, look up the variant's declared payload types,
		// and settle each constructor arg against its
		// substituted payload type. This is the second-pass
		// counterpart to the variant-call's pre-settle (which
		// only fires when payloads are non-generic). Also
		// re-stamps `VariantCallPayloads` so the IR's
		// emitEnumNew picks the resolved (no-longer-polymorphic)
		// payload type for slot sizing + store-op selection.
		call, ok := e.(*ast.Call)
		if !ok {
			return
		}
		id, ok := call.Callee.(*ast.Ident)
		if !ok {
			return
		}
		vr, isVariant := c.variantOf[id.Name]
		if !isVariant {
			return
		}
		ed := c.info.Enums[vr.enumName]
		if ed == nil || len(ed.TypeParams) != len(hn.Args) {
			return
		}
		sub := map[string]ast.Type{}
		for i, tp := range ed.TypeParams {
			sub[tp] = hn.Args[i]
		}
		resolvedPayloads := make([]ast.Type, len(vr.payloads))
		for i := range vr.payloads {
			resolvedPayloads[i] = substituteType(vr.payloads[i], sub)
		}
		for i, a := range call.Args {
			if i >= len(resolvedPayloads) {
				break
			}
			if resolvedPayloads[i] != nil {
				c.settleNumeric(a, resolvedPayloads[i])
			}
		}
		c.info.VariantCallPayloads[call] = resolvedPayloads
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
			// Don't stomp a float-typed binary's resolved
			// FloatWidth with an int width — happens when an
			// int-cast surrounds a float multiply, e.g.
			// `(frac * mult) as i64`. settleInt is fed the
			// cast's int target type as the hint; we must
			// leave the inner float binary alone so the cast
			// lowers to a float→int trunc op.
			if x.FloatWidth != 0 {
				return
			}
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
	case *ast.NumberLit:
		// Polymorphic integer literal in float context: stamp
		// IsFloat + FloatWidth so checkExpr returns FloatType
		// and the IR's NumberLit lowering picks the f-const
		// path. Skip when the literal is already locked to an
		// integer type (typed-suffix `42i64` or a previous
		// settle pass).
		if x.Width == 0 && !x.IsFloat {
			x.IsFloat = true
			x.FloatWidth = width
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
		if x.IsFloat {
			return ast.FloatType{Width: x.FloatWidth}
		}
		if x.Width != 0 {
			return ast.NumberType{Width: x.Width, Signed: !x.IsUnsigned}
		}
	case *ast.FloatLit:
		if x.Width != 0 {
			return ast.FloatType{Width: x.Width}
		}
	case *ast.Binary:
		// Comparison ops (`==`, `!=`, `<`, `<=`, `>`, `>=`)
		// stamp IntWidth / FloatWidth on the Binary node so
		// codegen knows whether to emit `i32.eq` vs `i64.eq`
		// vs `f32.eq` etc. — but their RESULT type is bool,
		// not the operand width. Without this guard a
		// `var b: boolean = a == b;` would re-type the rhs as
		// the operand's NumberType and fail the assignment.
		switch x.Op {
		case "==", "!=", "<", "<=", ">", ">=":
			return ast.BoolType{}
		}
		if x.IntWidth != 0 {
			return ast.NumberType{Width: x.IntWidth, Signed: !x.IsUnsigned}
		}
		if x.FloatWidth != 0 {
			return ast.FloatType{Width: x.FloatWidth}
		}
	case *ast.Unary:
		return postSettleType(x.Operand, prior)
	case *ast.ArrayLit:
		if x.ElemType != nil {
			return ast.ArrayType{Elem: x.ElemType}
		}
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
		case 8:
			min, max = -1<<7, 1<<7-1
		case 16:
			min, max = -1<<15, 1<<15-1
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
		case 8:
			max = 1<<8 - 1
		case 16:
			max = 1<<16 - 1
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
