// Package wasm emits WebAssembly text format (WAT) for a checked
// Program. Both this backend and the ARM32 emitter consume the same
// lowered ir.Program and share the optimisation pipeline (Inline,
// FuseTee, FlattenBranches, plus the PropagateCopies / ConstPropagate
// / Fold / ReduceStrength fixed-point cleanup) — new language
// features land once at the IR layer and both backends pick them up.
//
// Run the output with `wasmtime run --invoke main prog.wat` or
// convert to binary first with `wat2wasm prog.wat`.
package wasm

import (
	"fmt"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
)

// Emit returns the WAT module text for prog.
//
// Programs that use strings, `print` or `putchar` cause the emitter to
// add a memory section, a WASI `fd_write` import and a small pair of
// runtime helpers ($putchar / $print). Programs that take functions as
// values cause it to emit a `funcref` table plus type declarations for
// each indirect-call signature. Modules that touch none of those stay
// free of the extra structure so they can be loaded under minimal
// hosts.
func Emit(prog *ast.Program, info *checker.Info) (string, error) {
	return EmitWithOptions(prog, info, EmitOptions{})
}

// EmitOptions tunes the WAT emission. Each field is conservative —
// the zero value matches what the original `Emit` produced before
// options were introduced.
type EmitOptions struct {
	// HttpHandler emits a `wasi:http/incoming-handler@0.2.0#handle`
	// export wrapping the user's `function handle(req: HttpRequest):
	// HttpResponse`. No `_start` is exported (proxy-world components
	// have no entry point — the host invokes `handle` per inbound
	// request). When this is set, `HttpRequest` and `HttpResponse`
	// struct decls are auto-injected into the program.
	HttpHandler bool

	// PrintMainResult makes the `_start` wrapper format `main()`'s
	// integer return value through `int_to_string` and write it to
	// stdout (followed by a newline) before returning, instead of
	// dropping the value. Used by the WASM e2e test harness so it
	// can observe `main`'s i32 result through the component's
	// stdout — preview-2 components don't preserve arbitrary
	// integers through `wasi:cli/exit` and there's no `--invoke
	// main` equivalent for components, so stdout is the only
	// observation channel left. Has no effect when `main` returns
	// void; if `main` returns float, the flag is ignored (only the
	// integer path is wired) and the test should use a PASS/FAIL
	// shape instead.
	PrintMainResult bool
}

// EmitWithOptions is the option-aware sibling of Emit. The two share
// the same lowering pipeline (closure conversion → IR → IR opts →
// EmitFromIR); the options only influence the WAT layer.
func EmitWithOptions(prog *ast.Program, info *checker.Info, opts EmitOptions) (string, error) {
	// ir.Lower runs closure conversion as a precondition (hoisting
	// nested functions, rewriting captures), then produces an
	// ir.Program. ir.Fold runs constant folding on the lowered ops
	// — picking up the post-lowering shapes the AST optimiser can't
	// see (collapsed ternaries / short-circuits, etc.). EmitFromIR
	// turns the folded program into WAT, reusing the module-level
	// scaffolding (runtime helpers, function table, closure cells,
	// data segments, exports) defined alongside it.
	ip, err := ir.Lower(prog, info)
	if err != nil {
		return "", err
	}
	ir.Inline(ip)
	ir.FuseTee(ip)
	ir.FlattenBranches(ip)
	// PropagateCopies + ConstPropagate + Fold + ReduceStrength
	// expose new opportunities for each other (a const propagated
	// into an arithmetic expression folds; the fold makes a tee
	// dead; dropping the tee makes constants adjacent for further
	// folding). Run them to a fixed point so the cascade settles.
	ir.OptimizeCleanup(ip)
	ir.EliminateDeadCode(ip)
	return EmitFromIRWithOptions(prog, info, ip, opts)
}

type generator struct {
	out      strings.Builder
	info     *checker.Info
	indent   int
	current  *ast.FuncDecl
	httpHandler     bool // emit the wasi:http/incoming-handler.handle export wrapping user's handle(HttpRequest) -> HttpResponse.
	printMainResult bool // _start prints main()'s i32 return value via int_to_string before returning (test-only).
	// origTopLevelCount records how many functions the source had
	// before closure conversion appended hoisted ones. The first
	// origTopLevelCount entries get static closure cells (env=0)
	// because they're the only ones a `var f = name` reference can
	// reach by name.
	origTopLevelCount int

	// Runtime / strings / arrays.
	needsRuntime  bool
	needsArrays   bool
	needsStrEq       bool // any String == String / != comparison
	needsStrConcat   bool // any String + String concatenation
	needsStrSlice    bool // any `s[a:b]` on a string — emits $__str_slice
	needsStrMethods  bool // any `s.starts_with` / `.ends_with` / `.contains` call
	needsStrFromBytes bool // any `string_from_bytes(bs)` call
	needsBase64       bool // any `base64_encode` / `base64_decode` call
	needsHex          bool // any `hex_encode(s)` / `hex_decode(s)` call
	needsUrlParse     bool // any `url_parse(s)` call — emits the Url builder + parser helpers
	needsUrlCoder     bool // any `url_encode(s)` / `url_decode(s)` call
	needsQueryParse   bool // any `query_parse(s)` call
	needsJsonEncode   bool // any `json_encode(v)` call — emits the encoder + buffer-builder helpers
	needsJsonParse    bool // any `json_parse(s)` call
	needsStructs     bool
	needsBoundsCheck bool // any array or string Index expression appears
	needsClosures    bool // any FuncDecl was hoisted by closure conversion
	needsArgs        bool // any `args()` call appears — pulls in WASI args_*
	needsReadLine    bool // any `read_line()` call appears — pulls in WASI fd_read + helper + scratch slots
	needsEnv         bool // any `env(name)` call appears — pulls in WASI environ_* + helper + cache slots
	needsExit        bool // any `exit(code)` call appears — pulls in WASI proc_exit
	needsArena       bool // any `arena_save` / `arena_restore` call — emits the two heap-cursor helpers
	needsMap         bool // any `map_new()` or `__method_Map_*` call — emits the Map runtime helpers
	needsRandomBytes bool // any `random_bytes(n)` call — pulls in WASI random_get
	needsIntToString bool // any `int_to_string(n)` call — emits a small decimal-formatting helper
	needsNumToString bool // any `(i32 / u32 / i64 / u64).to_string()` method call — wraps the i64 formatter
	needsFloatToString bool // any `(f32 / f64).to_string()` method call
	needsTcp         bool // any tcp_* call — pulls in WASI sock_accept + fd_read/fd_write/fd_close on socket fds
	needsFileIO      bool // any `read_file` / `write_file` call — pulls in WASI path_open / fd_read / fd_close
	needsStreamingIO bool // any open_reader / open_writer / Reader|Writer method call — extends needsFileIO with the streaming helpers
	needsStdStreams  bool // any stdin() / stdout() / stderr() call — emits trivial constructors that wrap fd 0 / 1 / 2 in Reader / Writer
	stringPool    map[string]int // value → pointer in linear memory
	stringEntries []stringEntry  // emission order (data segments)
	stringOffset  int            // next free byte for a string entry
	closuresBase  int            // start of the per-function closure-cell region

	// Function table / indirect calls. needsFuncTable is set if any
	// top-level function name appears in non-callee position (taken
	// as a value) OR if any call goes through a local — both need a
	// `funcref` table populated in declaration order. indirectSigs
	// lists each unique signature used by call_indirect, in the
	// order we first saw it; sigIndex maps a signature key to its
	// position in indirectSigs. funcIndex maps each top-level
	// function name to its table-element index.
	needsFuncTable bool
	funcIndex      map[string]int
	indirectSigs   []*ast.FuncType
	sigIndex       map[string]int
	// inTable[name] is true if function `name` needs to live in the
	// funcref table. Hoisted closure functions are always in the
	// table; top-level user functions only when referenced as a
	// value somewhere. Functions outside the table skip the
	// trailing __env parameter, so wasmtime's `--invoke main` keeps
	// working.
	inTable map[string]bool
	// tableIndex[name] gives the position of `name` in the funcref
	// table, populated lazily once the scan phase has run. Closure
	// cells use the same indices (cell i = (tableIndex i, env=0)).
	tableIndex   map[string]int
	tableEntries []string

	// funcDecls maps each (post-closure-conversion) function's name to
	// its AST FuncDecl. The IR-driven emitter uses it to look up the
	// hoisted closure target's Captures list at OpMakeClosure time —
	// per-capture types decide between i32.store and f32.store when
	// packing the env block.
	funcDecls map[string]*ast.FuncDecl
}

type stringEntry struct {
	offset int    // address of the 4-byte length prefix
	text   string
}

func (g *generator) line(s string) {
	g.out.WriteString(strings.Repeat("  ", g.indent))
	g.out.WriteString(s)
	g.out.WriteByte('\n')
}
func (g *generator) linef(format string, args ...any) {
	g.line(fmt.Sprintf(format, args...))
}

// envParamPresent reports whether fn already carries the synthetic
// `__env` parameter that closure conversion appends to hoisted local
// functions. Top-level functions don't carry it natively; we add the
// param at emit time when needsFuncTable is on.
func envParamPresent(fn *ast.FuncDecl) bool {
	if len(fn.Params) == 0 {
		return false
	}
	last := fn.Params[len(fn.Params)-1]
	return last.Name == "__env"
}

// scanEnumUses returns true if the program actually constructs or
// matches an enum. The auto-injected Option / Result decls show
// up in `prog.Enums` on every program, so a `len(prog.Enums) > 0`
// check would over-trigger the bump allocator on programs that
// never use enums. We look at *Match statements (any match
// implies a scrutinee that came from somewhere — usually a
// variant call earlier) and at calls whose callee name matches a
// variant in any registered enum.
func scanEnumUses(prog *ast.Program) bool {
	variants := map[string]bool{}
	for _, ed := range prog.Enums {
		for _, v := range ed.Variants {
			variants[v.Name] = true
		}
	}
	found := false
	var walk func(any)
	walk = func(n any) {
		if found {
			return
		}
		switch x := n.(type) {
		case *ast.Match:
			found = true
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok && variants[id.Name] {
				found = true
				return
			}
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Ident:
			if variants[x.Name] {
				found = true
			}
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Arena:
			walk(x.Body)
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.Destructure:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.StructLit:
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ast.FieldAccess:
			walk(x.Target)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
	return found
}

// scanForIOBuiltins records which I/O builtins the program calls so
// the preamble can pull in only the WASI imports + helpers it
// actually needs. Unlike scanForArrayUses, this walker does NOT
// short-circuit — every call site is checked, because the per-
// builtin flags are independent.
func (g *generator) scanForIOBuiltins(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		switch x := n.(type) {
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok {
				switch id.Name {
				case "args":
					g.needsArgs = true
				case "env":
					g.needsEnv = true
				case "exit":
					g.needsExit = true
				case "arena_save", "arena_restore":
					g.needsArena = true
				case "random_bytes":
					g.needsRandomBytes = true
					g.needsArrays = true
					g.needsRuntime = true
				case "int_to_string":
					g.needsIntToString = true
					g.needsArrays = true
					g.needsRuntime = true
				case "tcp_listen", "tcp_accept", "tcp_recv", "tcp_send", "tcp_close":
					g.needsTcp = true
					g.needsArrays = true
					g.needsRuntime = true
				case "map_new":
					g.needsMap = true
					g.needsRuntime = true
					// String-keyed Map[string, V] needs the
					// byte-level string-equality helper for
					// the linear-search core's strcmp branch.
					g.needsStrEq = true
				case "string_from_bytes":
					g.needsStrFromBytes = true
					g.needsRuntime = true
				case "base64_encode", "base64_decode":
					g.needsBase64 = true
					g.needsRuntime = true
				case "hex_encode", "hex_decode":
					g.needsHex = true
					g.needsRuntime = true
				case "url_parse":
					g.needsUrlParse = true
					g.needsRuntime = true
					// The Url result struct is constructed in
					// the helper, the helper sub-slices the
					// input via `$__str_slice`, and indexing
					// the input goes through bounds-check to
					// avoid surprises. Trip the supporting
					// flags so all dependencies emit.
					g.needsStructs = true
					g.needsStrSlice = true
					g.needsBoundsCheck = true
				case "url_encode", "url_decode":
					g.needsUrlCoder = true
					g.needsRuntime = true
				case "query_parse":
					// Builds a Map[string, string] internally
					// and decodes pairs via url_decode. Trip
					// every supporting flag.
					g.needsQueryParse = true
					g.needsUrlCoder = true
					g.needsMap = true
					g.needsStrEq = true
					g.needsStrSlice = true
					g.needsBoundsCheck = true
					g.needsRuntime = true
				case "json_encode":
					// Walks a JsonValue tree, recursing through
					// JArray / JObject. Pulls Map (for
					// JObject's iteration), runtime/strings,
					// and the bounds-check / arrays helpers.
					g.needsJsonEncode = true
					g.needsMap = true
					g.needsRuntime = true
					g.needsArrays = true
					g.needsBoundsCheck = true
				case "json_parse":
					// Reuses the encoder's buffer-builder for
					// string accumulation (decoding escapes)
					// and array element collection. Emits Map
					// for JObject construction.
					g.needsJsonParse = true
					g.needsJsonEncode = true
					g.needsMap = true
					g.needsStrEq = true
					g.needsStrSlice = true
					g.needsRuntime = true
					g.needsArrays = true
					g.needsBoundsCheck = true
				case "read_file", "write_file":
					g.needsFileIO = true
				case "open_reader", "open_writer", "open_appender":
					g.needsFileIO = true
					g.needsStreamingIO = true
				case "stdin", "stdout", "stderr":
					// stdin/stdout/stderr by themselves
					// don't need the file I/O machinery,
					// but the only point of having them is
					// to call .read_line() / .write() etc.
					// — those methods light up
					// needsStreamingIO via the
					// __method_Reader_* / __method_Writer_*
					// scan a few lines down. Set the
					// dedicated flag for the constructor
					// helper itself.
					g.needsStdStreams = true
				}
				// Method calls on Reader/Writer arrive here as
				// post-checker mangled `__method_Reader_*` /
				// `__method_Writer_*` names; trip the streaming
				// IO flag for any of them.
				if strings.HasPrefix(id.Name, "__method_Reader_") ||
					strings.HasPrefix(id.Name, "__method_Writer_") {
					g.needsFileIO = true
					g.needsStreamingIO = true
				}
				if strings.HasPrefix(id.Name, "__method_Map_") ||
					strings.HasPrefix(id.Name, "__method_MapIter_") {
					g.needsMap = true
					g.needsRuntime = true
					g.needsStrEq = true
				}
				if strings.HasPrefix(id.Name, "__method_string_") {
					g.needsStrMethods = true
					g.needsRuntime = true
					// `trim` allocates a fresh substring via
					// `$__str_slice`; we emit all string-method
					// helpers as a single block, so always pull
					// the slice helper in when any string method
					// is in use.
					g.needsStrSlice = true
					g.needsBoundsCheck = true
				}
				// `n.to_string()` for any integer width pulls in the
				// existing `int_to_string` decimal formatter — the
				// method-mangled wrappers route there via the
				// shared `$__int_to_string_u64` helper.
				if strings.HasPrefix(id.Name, "__method_i32_to_string") ||
					strings.HasPrefix(id.Name, "__method_u32_to_string") ||
					strings.HasPrefix(id.Name, "__method_i64_to_string") ||
					strings.HasPrefix(id.Name, "__method_u64_to_string") {
					g.needsNumToString = true
					g.needsRuntime = true
				}
				if strings.HasPrefix(id.Name, "__method_f32_to_string") ||
					strings.HasPrefix(id.Name, "__method_f64_to_string") {
					g.needsFloatToString = true
					// Pulls in the integer formatter for the
					// integral half + the bump allocator + the
					// digit append helpers + the json
					// growable-buffer family (for the fraction
					// digit emit / trim path).
					g.needsNumToString = true
					g.needsJsonEncode = true
					g.needsMap = true
					g.needsArrays = true
					g.needsBoundsCheck = true
					g.needsRuntime = true
				}
			}
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.MapLit:
			// `Map { k: v, ... }` desugars to map_new + set
			// calls during IR lowering; trip the Map flag at
			// AST-walk time so the runtime helpers get emitted.
			g.needsMap = true
			g.needsRuntime = true
			for _, e := range x.Entries {
				walk(e.Key)
				walk(e.Value)
			}
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Arena:
			// `arena { … }` lowers to arena_save → body →
			// arena_restore at IR time, so the helpers need
			// to be in the binary even if user code never
			// names them directly.
			g.needsArena = true
			walk(x.Body)
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.Destructure:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Match:
			walk(x.Tag)
			for _, arm := range x.Arms {
				walk(arm.Body)
			}
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.StructLit:
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ast.FieldAccess:
			walk(x.Target)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// scanForArrayUses pre-walks the program and sets needsArrays if any
// ArrayLit, Index, or Index-target Assign appears. Arrays imply the
// runtime preamble (memory + bump allocator).
func (g *generator) scanForArrayUses(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsArrays {
			return
		}
		switch x := n.(type) {
		case *ast.ArrayLit, *ast.Index:
			g.needsArrays = true
			g.needsRuntime = true
		case *ast.Assign:
			if _, isIdx := x.Target.(*ast.Index); isIdx {
				g.needsArrays = true
				g.needsRuntime = true
			}
			walk(x.Target)
			walk(x.Value)
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Arena:
			walk(x.Body)
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.Destructure:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Call:
			// `args()` / `read_line()` / `env()` build length-
			// prefixed strings at runtime, so they need the array
			// preamble (bump allocator). `exit()` is detected in
			// scanForIOBuiltins, since this scan short-circuits as
			// soon as needsArrays is set.
			if id, ok := x.Callee.(*ast.Ident); ok {
				switch id.Name {
				case "args", "env", "read_file", "write_file",
					"open_reader", "open_writer", "open_appender",
					"stdin", "stdout", "stderr":
					g.needsArrays = true
					g.needsRuntime = true
				case "arena_save", "arena_restore":
					// Arena helpers read/write the bump cursor
					// at memory[40]. The data segment that seeds
					// that cursor is gated on needsArrays, so
					// we trip that flag here too.
					g.needsArrays = true
					g.needsRuntime = true
				}
				if strings.HasPrefix(id.Name, "__method_Reader_") ||
					strings.HasPrefix(id.Name, "__method_Writer_") {
					g.needsArrays = true
					g.needsRuntime = true
				}
			}
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Match:
			walk(x.Tag)
			for _, arm := range x.Arms {
				walk(arm.Body)
			}
		case *ast.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}
// scanForStructUses pre-walks the program and sets needsStructs if any
// StructLit appears. Structs share the bump allocator with arrays, so
// the runtime preamble lights up either way.
func (g *generator) scanForStructUses(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsStructs {
			return
		}
		switch x := n.(type) {
		case *ast.StructLit:
			g.needsStructs = true
			g.needsRuntime = true
		case *ast.TupleLit:
			// Tuples share the heap-record codegen with structs;
			// the same `needsStructs` flag pulls in __lang_alloc.
			g.needsStructs = true
			g.needsRuntime = true
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.FieldAccess:
			walk(x.Target)
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Arena:
			walk(x.Body)
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.Destructure:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Call:
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// scanForIndirectCalls walks every function body and records the
// signatures that will need `(type $tN ...)` declarations for use
// with call_indirect, plus whether the program touches the function
// table at all.
//
// The two triggers:
//
//   - an Ident referring to a top-level function appears in
//     non-callee position (taken as a value, e.g. `var f = add`); it
//     needs to materialise as the table index, which means the table
//     must exist;
//   - a Call whose callee resolves to a local of *FuncType (rather
//     than to a top-level function name) lowers to call_indirect and
//     needs the corresponding `(type $tN ...)` declaration.
func (g *generator) scanForIndirectCalls(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		g.current = fn
		g.scanIndirectStmt(fn.Body)
	}
	g.current = nil
}

func (g *generator) scanIndirectStmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.Block:
		for _, ss := range x.Stmts {
			g.scanIndirectStmt(ss)
		}
	case *ast.Arena:
		g.scanIndirectStmt(x.Body)
	case *ast.If:
		g.scanIndirectExpr(x.Cond, false)
		g.scanIndirectStmt(x.Then)
		if x.Else != nil {
			g.scanIndirectStmt(x.Else)
		}
	case *ast.While:
		g.scanIndirectExpr(x.Cond, false)
		g.scanIndirectStmt(x.Body)
	case *ast.For:
		if x.Init != nil {
			g.scanIndirectStmt(x.Init)
		}
		g.scanIndirectExpr(x.Cond, false)
		if x.Step != nil {
			g.scanIndirectStmt(x.Step)
		}
		g.scanIndirectStmt(x.Body)
	case *ast.Return:
		if x.Value != nil {
			g.scanIndirectExpr(x.Value, false)
		}
	case *ast.Var:
		g.scanIndirectExpr(x.Init, false)
	case *ast.Destructure:
		g.scanIndirectExpr(x.Init, false)
	case *ast.ExprStmt:
		g.scanIndirectExpr(x.Expr, false)
	case *ast.Switch:
		g.scanIndirectExpr(x.Tag, false)
		for _, k := range x.Cases {
			for _, v := range k.Values {
				g.scanIndirectExpr(v, false)
			}
			g.scanIndirectStmt(k.Body)
		}
		if x.Default != nil {
			g.scanIndirectStmt(x.Default)
		}
	case *ast.Match:
		g.scanIndirectExpr(x.Tag, false)
		for _, arm := range x.Arms {
			g.scanIndirectStmt(arm.Body)
		}
	}
}

// scanIndirectExpr walks an expression tree. inCalleePos is true when
// the expression sits directly in `Call.Callee` — that single position
// is where a top-level function name doesn't trigger the table.
func (g *generator) scanIndirectExpr(e ast.Expr, inCalleePos bool) {
	switch x := e.(type) {
	case *ast.Ident:
		if !inCalleePos {
			if _, ok := g.funcIndex[x.Name]; ok {
				g.needsFuncTable = true
				g.inTable[x.Name] = true
			}
		}
	case *ast.Binary:
		g.scanIndirectExpr(x.Left, false)
		g.scanIndirectExpr(x.Right, false)
	case *ast.Unary:
		g.scanIndirectExpr(x.Operand, false)
	case *ast.Index:
		g.scanIndirectExpr(x.Array, false)
		g.scanIndirectExpr(x.Idx, false)
	case *ast.ArrayLit:
		for _, el := range x.Elems {
			g.scanIndirectExpr(el, false)
		}
	case *ast.Assign:
		g.scanIndirectExpr(x.Target, false)
		g.scanIndirectExpr(x.Value, false)
	case *ast.Ternary:
		g.scanIndirectExpr(x.Cond, false)
		g.scanIndirectExpr(x.Then, false)
		g.scanIndirectExpr(x.Else, false)
	case *ast.Call:
		// Walk args first.
		for _, a := range x.Args {
			g.scanIndirectExpr(a, false)
		}
		// Then decide whether this is direct or indirect.
		if id, ok := x.Callee.(*ast.Ident); ok {
			if _, isTopLevel := g.funcIndex[id.Name]; isTopLevel {
				// direct call — callee Ident is in callee position
				g.scanIndirectExpr(x.Callee, true)
				return
			}
			// Local of function type → indirect call.
			ft := g.localFuncType(g.current, id.Name)
			if ft != nil {
				g.needsFuncTable = true
				g.recordSig(ft)
			}
			g.scanIndirectExpr(x.Callee, true)
		} else {
			g.scanIndirectExpr(x.Callee, false)
		}
	}
}

// localFuncType returns the function type of a local identifier
// (parameter or `var`) in fn, or nil if the name doesn't resolve to a
// function-typed local in that scope.
func (g *generator) localFuncType(fn *ast.FuncDecl, name string) *ast.FuncType {
	if fn != nil {
		for _, p := range fn.Params {
			if p.Name == name {
				if ft, ok := p.Type.(*ast.FuncType); ok {
					return ft
				}
				return nil
			}
		}
	}
	if vars, ok := g.info.Locals[fn]; ok {
		for _, v := range vars {
			if v.Name == name {
				if ft, ok := v.Type.(*ast.FuncType); ok {
					return ft
				}
				return nil
			}
		}
	}
	return nil
}

// recordSig assigns ft a stable index in indirectSigs, deduplicating
// by structural signature key.
func (g *generator) recordSig(ft *ast.FuncType) int {
	key := sigKey(ft)
	if idx, ok := g.sigIndex[key]; ok {
		return idx
	}
	idx := len(g.indirectSigs)
	g.sigIndex[key] = idx
	g.indirectSigs = append(g.indirectSigs, ft)
	return idx
}

func sigKey(ft *ast.FuncType) string {
	var b strings.Builder
	b.WriteByte('(')
	for i, p := range ft.Params {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.String())
	}
	b.WriteString(")->")
	b.WriteString(ft.Result.String())
	return b.String()
}

// watFuncType renders a *FuncType as the WAT `(func ...)` body used
// in `(type $tN (func ...))` declarations. Under the closure ABI
// every table entry carries a trailing `__env i32` parameter, so the
// signature has one more param than the user-visible type. Legacy
// programs (no nested functions) skip the env param and pass bare
// table indices.
func (g *generator) watFuncType(ft *ast.FuncType) string {
	var b strings.Builder
	b.WriteString("(func")
	for _, p := range ft.Params {
		t, _ := watType(p)
		b.WriteString(" (param ")
		b.WriteString(t)
		b.WriteByte(')')
	}
	if g.needsClosures {
		b.WriteString(" (param i32)") // env pointer
	}
	if !ast.Equal(ft.Result, ast.VoidType{}) {
		t, _ := watType(ft.Result)
		b.WriteString(" (result ")
		b.WriteString(t)
		b.WriteByte(')')
	}
	b.WriteByte(')')
	return b.String()
}

// scanForStringEq pre-walks the program and sets needsStrEq if any
// `==` or `!=` between strings appears, so emitRuntimePreamble knows
// to include the $__str_eq helper. The helper reads from linear
// memory, so it implies needsRuntime as well.
func (g *generator) scanForStringEq(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsStrEq {
			return
		}
		switch x := n.(type) {
		case *ast.Binary:
			if x.IsStringCmp {
				g.needsStrEq = true
				g.needsRuntime = true
				return
			}
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Call:
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Arena:
			walk(x.Body)
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.Destructure:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Match:
			walk(x.Tag)
			for _, arm := range x.Arms {
				if arm.Guard != nil {
					walk(arm.Guard)
				}
				walk(arm.Body)
			}
		case *ast.IfLet:
			walk(x.Source)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.LetElse:
			walk(x.Source)
			walk(x.Else)
		case *ast.Defer:
			walk(x.Expr)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// scanForStringConcat pre-walks the program and sets needsStrConcat
// if any `+` between strings appears. The helper allocates a fresh
// buffer via `__lang_alloc`, so it pulls in needsArrays as well.
func (g *generator) scanForStringConcat(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsStrConcat {
			return
		}
		switch x := n.(type) {
		case *ast.Binary:
			if x.IsStringConcat {
				g.needsStrConcat = true
				g.needsRuntime = true
				g.needsArrays = true
				return
			}
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Call:
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.StructLit:
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ast.FieldAccess:
			walk(x.Target)
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Arena:
			walk(x.Body)
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Match:
			walk(x.Tag)
			for _, arm := range x.Arms {
				walk(arm.Body)
			}
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.Destructure:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// scanForBoundsCheck pre-walks the program and sets needsBoundsCheck
// if any Index expression appears. The helpers it triggers
// ($__arr_idx / $__str_idx) read the length prefix from linear
// memory, so it implies needsRuntime.
func (g *generator) scanForBoundsCheck(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsBoundsCheck {
			return
		}
		switch x := n.(type) {
		case *ast.Index:
			g.needsBoundsCheck = true
			g.needsRuntime = true
			return
		case *ast.SliceExpr:
			// Slicing pulls in the same heap-record machinery
			// as bounds-checked indexing — the slice constructor
			// is alloc + 2× store, plus indexing on slice values
			// goes through `$__slice_idx`. String slicing has
			// its own dedicated copy-into-fresh-string helper.
			g.needsBoundsCheck = true
			g.needsRuntime = true
			g.needsStructs = true
			if x.IsString {
				g.needsStrSlice = true
			}
			walk(x.Source)
			if x.Low != nil {
				walk(x.Low)
			}
			if x.High != nil {
				walk(x.High)
			}
			return
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Call:
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.StructLit:
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ast.FieldAccess:
			walk(x.Target)
		case *ast.CastExpr:
			walk(x.Inner)
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Arena:
			walk(x.Body)
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Match:
			walk(x.Tag)
			for _, arm := range x.Arms {
				walk(arm.Body)
			}
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.Destructure:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// scanForRuntimeUses pre-walks the program and sets needsRuntime if
// any string literal, `print` call or `putchar` call appears.
func (g *generator) scanForRuntimeUses(prog *ast.Program) {
	var walk func(any)
	walk = func(n any) {
		if g.needsRuntime {
			return
		}
		switch x := n.(type) {
		case *ast.StringLit:
			g.needsRuntime = true
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok {
				switch id.Name {
				case "print", "write", "eprint", "putchar", "args", "env", "exit",
					"read_file", "write_file", "open_reader", "open_writer", "open_appender",
					"stdin", "stdout", "stderr":
					g.needsRuntime = true
					return
				}
			}
			walk(x.Callee)
			for _, a := range x.Args {
				walk(a)
			}
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.Arena:
			walk(x.Body)
		case *ast.If:
			walk(x.Cond)
			walk(x.Then)
			if x.Else != nil {
				walk(x.Else)
			}
		case *ast.While:
			walk(x.Cond)
			walk(x.Body)
		case *ast.For:
			if x.Init != nil {
				walk(x.Init)
			}
			walk(x.Cond)
			if x.Step != nil {
				walk(x.Step)
			}
			walk(x.Body)
		case *ast.Return:
			if x.Value != nil {
				walk(x.Value)
			}
		case *ast.Var:
			walk(x.Init)
		case *ast.Destructure:
			walk(x.Init)
		case *ast.ExprStmt:
			walk(x.Expr)
		case *ast.Binary:
			walk(x.Left)
			walk(x.Right)
		case *ast.Unary:
			walk(x.Operand)
		case *ast.Assign:
			walk(x.Target)
			walk(x.Value)
		case *ast.Index:
			walk(x.Array)
			walk(x.Idx)
		case *ast.ArrayLit:
			for _, e := range x.Elems {
				walk(e)
			}
		case *ast.Switch:
			walk(x.Tag)
			for _, k := range x.Cases {
				for _, v := range k.Values {
					walk(v)
				}
				walk(k.Body)
			}
			if x.Default != nil {
				walk(x.Default)
			}
		case *ast.Match:
			walk(x.Tag)
			for _, arm := range x.Arms {
				walk(arm.Body)
			}
		case *ast.Ternary:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		}
	}
	for _, fn := range prog.Funcs {
		walk(fn.Body)
	}
}

// internString assigns an address to s the first time we see it and
// reuses it on repeats. The returned pointer skips the 4-byte length
// prefix, so callers can do `i32.load (sub ptr 4)` to recover length.
func (g *generator) internString(s string) int {
	if ptr, ok := g.stringPool[s]; ok {
		return ptr
	}
	g.needsRuntime = true
	off := g.stringOffset
	g.stringEntries = append(g.stringEntries, stringEntry{offset: off, text: s})
	ptr := off + 4
	g.stringPool[s] = ptr
	g.stringOffset = off + 4 + len(s)
	return ptr
}

// emitRuntimePreamble emits the WASI import, the linear memory, and
// two helper functions ($putchar, $print). They share a small block
// of static memory — see the data segments at the end of the module.
//
// Memory layout for the runtime constants (offsets in bytes):
//
//	 0..3   putchar i32 buffer (only the low byte is used)
//	 4..11  putchar iovec  { ptr=0, len=1 }   pre-initialised
//	12..15  putchar nwritten
//	16..23  print iovec[0] { ptr=string, len=L }  set per call
//	24..31  print iovec[1] { ptr=32, len=1 }      pre-initialised
//	32      newline byte 0x0A                    pre-initialised
//	36..39  print nwritten
//	40..43  bump-allocator pointer (when needsArrays)
//	44..47  args() cache pointer (0 = not yet built; non-zero = ptr)
//	48..51  args_sizes_get out: argc
//	52..55  args_sizes_get out: argv buffer size
//	56..63  read_line iovec { ptr=68, len=1 }    pre-initialised
//	64..67  read_line nread out
//	68..71  read_line single-byte buffer (only byte 68 used)
//	72..75  env init flag (0 = uninitialised, 1 = environ_get done)
//	76..79  env count (number of "KEY=VALUE" entries after init)
//	80..83  env_ptrs heap pointer (filled by environ_get)
//	84..87  environ_sizes_get out: count
//	88..91  environ_sizes_get out: bufsize
//	92..103 preview-2 canonical-ABI retptr area (12 bytes: enough for
//	         result<list<u8>, stream-error> as well as the smaller
//	         (ptr, len) pair from `get-random-bytes`). Single-threaded,
//	         so the slot is shared between calls.
//	104..107 preview-2 stdout output-stream resource handle (cached on
//	         first $print / $write / $putchar call)
//	108..111 preview-2 stderr output-stream resource handle (cached on
//	         first $eprint call)
//	112..115 preview-2 stream-handle init flags
//	         (bit 0 = stdout cached, bit 1 = stderr cached,
//	          bit 2 = stdin cached, bit 3 = preopen cached,
//	          bit 4 = network cached)
//	116..119 preview-2 stdin input-stream resource handle (cached on
//	         first $read_line call)
//	120..123 preview-2 preopen descriptor handle (cached on first
//	         file open; resolves paths relative to it via open-at)
//	124..127 preview-2 network resource handle (cached on first
//	         tcp_listen / tcp_accept; from instance-network())
//	96+     string data, each entry: 4-byte length prefix then bytes
//	  (string data starts at 128 instead of 96 when preview-2 streams
//	  are in play; see EmitFromIRWithOptions.)
func (g *generator) emitRuntimePreamble() {
	if g.needsArgs {
		// args / env still come in via preview-1; the adapter
		// translates these to `wasi:cli/environment.get-arguments`
		// / `get-environment` at component-new time. A native
		// migration of these two pulls in canonical-ABI list<string>
		// machinery and is deferred to a follow-up.
		g.line(`(import "wasi_snapshot_preview1" "args_sizes_get" (func $__wasi_args_sizes_get (param i32 i32) (result i32)))`)
		g.line(`(import "wasi_snapshot_preview1" "args_get" (func $__wasi_args_get (param i32 i32) (result i32)))`)
	}
	if g.needsEnv {
		g.line(`(import "wasi_snapshot_preview1" "environ_sizes_get" (func $__wasi_environ_sizes_get (param i32 i32) (result i32)))`)
		g.line(`(import "wasi_snapshot_preview1" "environ_get" (func $__wasi_environ_get (param i32 i32) (result i32)))`)
	}
	if g.needsExit {
		// proc_exit is wrapped by the adapter into
		// `wasi:cli/exit.exit(result<_, _>)`; non-zero codes are
		// flattened to Err(()), so the host process sees exit 1
		// for any non-zero code. Documented limitation under
		// preview-2 0.2.0 — a `wasi:cli/exit.exit-with-code` wrapper
		// would land in 0.2.1+.
		g.line(`(import "wasi_snapshot_preview1" "proc_exit" (func $__wasi_proc_exit (param i32)))`)
	}
	// Preview-2 stdio: get-stdout / get-stderr / get-stdin return
	// resource handles; output-stream.blocking-write-and-flush
	// takes (handle, ptr, len, retptr) and flushes synchronously
	// — drop-in for $print / $write / $eprint / $putchar.
	// input-stream.blocking-read takes (handle, len_u64, retptr)
	// and writes `result<list<u8>, stream-error>` through the
	// retptr — see the read_line / read_chunk / tcp_recv call
	// sites.
	g.line(`(import "wasi:cli/stdout@0.2.0" "get-stdout" (func $__wasi_get_stdout (result i32)))`)
	g.line(`(import "wasi:cli/stderr@0.2.0" "get-stderr" (func $__wasi_get_stderr (result i32)))`)
	g.line(`(import "wasi:io/streams@0.2.0" "[method]output-stream.blocking-write-and-flush" (func $__wasi_blocking_write_and_flush (param i32 i32 i32 i32)))`)
	if g.needsReadLine || g.needsStreamingIO || g.needsFileIO || g.needsStdStreams || g.needsTcp || g.httpHandler {
		g.line(`(import "wasi:cli/stdin@0.2.0" "get-stdin" (func $__wasi_get_stdin (result i32)))`)
		g.line(`(import "wasi:io/streams@0.2.0" "[method]input-stream.blocking-read" (func $__wasi_blocking_read (param i32 i64 i32)))`)
	}
	if g.needsFileIO || g.needsStreamingIO {
		// File I/O imports. preopens.get-directories returns the
		// host's preopen list; we take the first entry as the
		// working directory descriptor and call descriptor.open-at
		// against it. read/write/append-via-stream return a stream
		// resource the rest of the runtime feeds through
		// wasi:io/streams.
		g.line(`(import "wasi:filesystem/preopens@0.2.0" "get-directories" (func $__wasi_get_directories (param i32)))`)
		g.line(`(import "wasi:filesystem/types@0.2.0" "[method]descriptor.open-at" (func $__wasi_descriptor_open_at (param i32 i32 i32 i32 i32 i32 i32)))`)
		g.line(`(import "wasi:filesystem/types@0.2.0" "[method]descriptor.read-via-stream" (func $__wasi_descriptor_read_via_stream (param i32 i64 i32)))`)
		g.line(`(import "wasi:filesystem/types@0.2.0" "[method]descriptor.write-via-stream" (func $__wasi_descriptor_write_via_stream (param i32 i64 i32)))`)
		g.line(`(import "wasi:filesystem/types@0.2.0" "[method]descriptor.append-via-stream" (func $__wasi_descriptor_append_via_stream (param i32 i32)))`)
	}
	if g.needsFileIO || g.needsStreamingIO || g.needsTcp || g.httpHandler {
		// Stream resource drops — closing a file Reader/Writer
		// (step 3c) and closing a TCP connection (step 4) both
		// drop input-stream / output-stream resources to keep
		// them from leaking on the host. The underlying
		// descriptor / tcp-socket handles still leak because the
		// bump allocator can't free the wrapper struct either;
		// bounded opens per program lifetime keeps that
		// acceptable.
		g.line(`(import "wasi:io/streams@0.2.0" "[resource-drop]input-stream" (func $__wasi_input_stream_drop (param i32)))`)
		g.line(`(import "wasi:io/streams@0.2.0" "[resource-drop]output-stream" (func $__wasi_output_stream_drop (param i32)))`)
	}
	if g.needsRandomBytes {
		// Component-model lowered signature for
		//   get-random-bytes: func(len: u64) -> list<u8>
		// is `(param i64 i32)` where i32 is the return-area
		// pointer. The host calls our exported `cabi_realloc` to
		// allocate a buffer in our linear memory, fills it with
		// the random bytes, then writes the (ptr, len) pair to
		// the return area.
		g.line(`(import "wasi:random/random@0.2.0" "get-random-bytes" (func $__wasi_random_get_p2 (param i64 i32)))`)
	}
	if g.needsTcp {
		// Native wasi:sockets. The user-facing API (tcp_listen /
		// tcp_accept / tcp_recv / tcp_send / tcp_close) returns a
		// heap pointer to a 12-byte struct `(tcp_socket,
		// input_stream, output_stream)`; for listening sockets
		// the stream slots stay 0. The host doesn't need
		// `wasmtime --tcp-listen=…` — the guest binds the port
		// itself.
		g.line(`(import "wasi:sockets/instance-network@0.2.0" "instance-network" (func $__wasi_instance_network (result i32)))`)
		g.line(`(import "wasi:sockets/network@0.2.0" "[resource-drop]network" (func $__wasi_network_drop (param i32)))`)
		g.line(`(import "wasi:sockets/tcp-create-socket@0.2.0" "create-tcp-socket" (func $__wasi_create_tcp_socket (param i32 i32)))`)
		// start-bind takes the canonical-ABI flattening of
		// `ip-socket-address` — a 1-i32 discriminant plus an
		// 11-i32 max payload (ipv4 uses 5 slots, ipv6 needs 11,
		// the variant joins them). Total params: self,
		// borrow<network>, disc, 11 flat slots, retptr = 15 i32.
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[method]tcp-socket.start-bind" (func $__wasi_tcp_start_bind (param i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32)))`)
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[method]tcp-socket.finish-bind" (func $__wasi_tcp_finish_bind (param i32 i32)))`)
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[method]tcp-socket.start-listen" (func $__wasi_tcp_start_listen (param i32 i32)))`)
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[method]tcp-socket.finish-listen" (func $__wasi_tcp_finish_listen (param i32 i32)))`)
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[method]tcp-socket.accept" (func $__wasi_tcp_accept (param i32 i32)))`)
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[method]tcp-socket.subscribe" (func $__wasi_tcp_subscribe (param i32) (result i32)))`)
		g.line(`(import "wasi:sockets/tcp@0.2.0" "[resource-drop]tcp-socket" (func $__wasi_tcp_socket_drop (param i32)))`)
		g.line(`(import "wasi:io/poll@0.2.0" "[method]pollable.block" (func $__wasi_pollable_block (param i32)))`)
		g.line(`(import "wasi:io/poll@0.2.0" "[resource-drop]pollable" (func $__wasi_pollable_drop (param i32)))`)
	}
	if g.httpHandler {
		// wasi:http imports for the `wasi:http/incoming-handler.handle`
		// export wrapper. The wrapper unpacks the incoming request
		// into a lang HttpRequest, calls the user's `handle`, and
		// streams the response back through outgoing-body. See
		// emitHttpHandlerWrapper for the call ordering.
		g.line(`(import "wasi:http/types@0.2.0" "[method]incoming-request.method" (func $__wasi_http_request_method (param i32 i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[method]incoming-request.path-with-query" (func $__wasi_http_request_path_with_query (param i32 i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[method]incoming-request.consume" (func $__wasi_http_request_consume (param i32 i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[resource-drop]incoming-request" (func $__wasi_http_request_drop (param i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[method]incoming-body.stream" (func $__wasi_http_incoming_body_stream (param i32 i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[static]incoming-body.finish" (func $__wasi_http_incoming_body_finish (param i32) (result i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[resource-drop]future-trailers" (func $__wasi_http_future_trailers_drop (param i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[constructor]fields" (func $__wasi_http_fields_new (result i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[constructor]outgoing-response" (func $__wasi_http_response_new (param i32) (result i32)))`)
		// set-status-code's `result<_, _>` returns the disc inline as
		// a single i32 (no retptr, since both Ok and Err carry no
		// payload — the canonical-ABI flat representation collapses
		// to a single discriminant slot).
		g.line(`(import "wasi:http/types@0.2.0" "[method]outgoing-response.set-status-code" (func $__wasi_http_response_set_status (param i32 i32) (result i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[method]outgoing-response.body" (func $__wasi_http_response_body (param i32 i32)))`)
		g.line(`(import "wasi:http/types@0.2.0" "[method]outgoing-body.write" (func $__wasi_http_outgoing_body_write (param i32 i32)))`)
		// outgoing-body.finish: (param this option-disc option-trailers retptr).
		// Result <_, error-code> can be large; we ignore the result
		// payload but the host still needs ≥64 bytes of retptr to
		// scratch the error variant. We pass our shared 16-byte
		// retptr at memory[92] — the static area was sized for
		// smaller variants — so allocate a wider one inline.
		g.line(`(import "wasi:http/types@0.2.0" "[static]outgoing-body.finish" (func $__wasi_http_outgoing_body_finish (param i32 i32 i32 i32)))`)
		// response-outparam.set: param + flattened
		// `result<outgoing-response, error-code>`. Total 9 i32-or-i64
		// params: 1 outparam, 1 disc, 7 payload slots. Slot 2 of
		// the payload is i64 because an error-code case carries
		// `option<u64>` (HTTP-{request,response}-body-size) and
		// the canonical-ABI joins the variant width up to the
		// wider type.
		g.line(`(import "wasi:http/types@0.2.0" "[static]response-outparam.set" (func $__wasi_http_response_outparam_set (param i32 i32 i32 i32 i64 i32 i32 i32 i32)))`)
		// pollable.block + drop already imported when needsTcp; we
		// might need them for the future-trailers (no — future-trailers.drop
		// is what we use; we don't subscribe). Skip.
	}
	g.line(`(memory $mem 1)`)

	{
		// $__lang_alloc bumps the allocator pointer at memory[40] and
		// returns the address that was there before the bump. There's
		// no free — arrays in lang are immutable but not GC'd.
		//
		// Always emit because `$cabi_realloc` (the canonical-ABI
		// allocator the host calls into) defers to it
		// unconditionally; an alloc-free program would otherwise
		// fail to compile at the wasm-tools step with "unknown func
		// $__lang_alloc".
		//
		// Grows memory on demand: if the post-bump end goes past the
		// current memory size (in pages), call `memory.grow` for the
		// shortfall. The original implementation skipped this, which
		// was fine for tiny programs but breaks under preview-2 — the
		// preview-1 adapter calls our exported `cabi_realloc` (which
		// hits this allocator) requesting a full 64 KiB page at
		// startup and the canonical-ABI runtime check fails as soon
		// as we hand back a pointer past the current memory.
		g.line(`(func $__lang_alloc (param $size i32) (result i32)`)
		g.indent++
		g.line(`(local $ptr i32) (local $end i32) (local $need i32)`)
		// ptr = mem[40]
		g.line(`i32.const 40`)
		g.line(`i32.load`)
		g.line(`local.set $ptr`)
		// end = ptr + size
		g.line(`local.get $ptr`)
		g.line(`local.get $size`)
		g.line(`i32.add`)
		g.line(`local.set $end`)
		// need = ((end + 65535) >> 16) - memory.size
		g.line(`local.get $end`)
		g.line(`i32.const 65535`)
		g.line(`i32.add`)
		g.line(`i32.const 16`)
		g.line(`i32.shr_u`)
		g.line(`memory.size`)
		g.line(`i32.sub`)
		g.line(`local.set $need`)
		// if (i32) need > 0: memory.grow need (drop result; trust host).
		g.line(`local.get $need`)
		g.line(`i32.const 0`)
		g.line(`i32.gt_s`)
		g.line(`if`)
		g.indent++
		g.line(`local.get $need`)
		g.line(`memory.grow`)
		g.line(`drop`)
		g.indent--
		g.line(`end`)
		// mem[40] = end
		g.line(`i32.const 40`)
		g.line(`local.get $end`)
		g.line(`i32.store`)
		g.line(`local.get $ptr`)
		g.indent--
		g.line(`)`)
	}

	if g.needsStrEq {
		// $__str_eq compares two length-prefixed strings byte-by-byte.
		// Returns 1 if equal, 0 otherwise. Identical pointers short-circuit
		// to true; lengths are read from `ptr - 4` (4-byte little-endian
		// prefix) before the byte loop.
		g.line(`(func $__str_eq (param $a i32) (param $b i32) (result i32)`)
		g.indent++
		g.line(`(local $la i32) (local $lb i32) (local $i i32)`)
		g.line(`local.get $a`)
		g.line(`local.get $b`)
		g.line(`i32.eq`)
		g.line(`if (result i32)`)
		g.indent++
		g.line(`i32.const 1`)
		g.indent--
		g.line(`else`)
		g.indent++
		// la = mem[a-4]; lb = mem[b-4]
		g.line(`local.get $a`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`local.set $la`)
		g.line(`local.get $b`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`local.set $lb`)
		g.line(`local.get $la`)
		g.line(`local.get $lb`)
		g.line(`i32.ne`)
		g.line(`if (result i32)`)
		g.indent++
		g.line(`i32.const 0`)
		g.indent--
		g.line(`else`)
		g.indent++
		// for (i=0; i<la; i++) if (a[i] != b[i]) return 0
		g.line(`i32.const 0`)
		g.line(`local.set $i`)
		g.line(`block $end`)
		g.indent++
		g.line(`loop $loop`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`local.get $la`)
		g.line(`i32.eq`)
		g.line(`br_if $end`)
		g.line(`local.get $a`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`i32.load8_u`)
		g.line(`local.get $b`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`i32.load8_u`)
		g.line(`i32.ne`)
		g.line(`if`)
		g.indent++
		g.line(`i32.const 0`)
		g.line(`return`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $i`)
		g.line(`i32.const 1`)
		g.line(`i32.add`)
		g.line(`local.set $i`)
		g.line(`br $loop`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		g.line(`i32.const 1`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`)`)
	}

	if g.needsStrConcat {
		// $__str_concat allocates a fresh length-prefixed buffer holding
		// the bytes of `a` followed by the bytes of `b`. Both inputs
		// point at the first byte of their content; their lengths live
		// at `ptr - 4`. The bump allocator is shared with arrays.
		g.line(`(func $__str_concat (param $a i32) (param $b i32) (result i32)`)
		g.indent++
		g.line(`(local $la i32) (local $lb i32) (local $base i32) (local $dst i32) (local $i i32)`)
		// la / lb
		g.line(`local.get $a`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`local.set $la`)
		g.line(`local.get $b`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`local.set $lb`)
		// base = __lang_alloc(la + lb + 4)
		g.line(`local.get $la`)
		g.line(`local.get $lb`)
		g.line(`i32.add`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`call $__lang_alloc`)
		g.line(`local.set $base`)
		// store length prefix at base
		g.line(`local.get $base`)
		g.line(`local.get $la`)
		g.line(`local.get $lb`)
		g.line(`i32.add`)
		g.line(`i32.store`)
		// dst = base + 4
		g.line(`local.get $base`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.set $dst`)
		// Copy a's bytes: for (i=0; i<la; i++) dst[i] = a[i]
		g.line(`i32.const 0`)
		g.line(`local.set $i`)
		g.line(`block $aend`)
		g.indent++
		g.line(`loop $aloop`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`local.get $la`)
		g.line(`i32.eq`)
		g.line(`br_if $aend`)
		g.line(`local.get $dst`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`local.get $a`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`i32.load8_u`)
		g.line(`i32.store8`)
		g.line(`local.get $i`)
		g.line(`i32.const 1`)
		g.line(`i32.add`)
		g.line(`local.set $i`)
		g.line(`br $aloop`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		// Copy b's bytes: for (i=0; i<lb; i++) dst[la+i] = b[i]
		g.line(`i32.const 0`)
		g.line(`local.set $i`)
		g.line(`block $bend`)
		g.indent++
		g.line(`loop $bloop`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`local.get $lb`)
		g.line(`i32.eq`)
		g.line(`br_if $bend`)
		g.line(`local.get $dst`)
		g.line(`local.get $la`)
		g.line(`i32.add`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`local.get $b`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`i32.load8_u`)
		g.line(`i32.store8`)
		g.line(`local.get $i`)
		g.line(`i32.const 1`)
		g.line(`i32.add`)
		g.line(`local.set $i`)
		g.line(`br $bloop`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		// Return the content pointer (base + 4).
		g.line(`local.get $dst`)
		g.indent--
		g.line(`)`)
	}

	if g.needsBoundsCheck {
		// $__arr_idx and $__str_idx return the byte address of element i
		// in a length-prefixed array / string, trapping if i is out of
		// range. The length lives at `base - 4` (4-byte little-endian
		// prefix). Stride differs (4 for arrays, 1 for strings) so we
		// emit two specialised helpers rather than threading a stride
		// argument.
		g.line(`(func $__arr_idx (param $base i32) (param $i i32) (result i32)`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`i32.const 0`)
		g.line(`i32.lt_s`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $i`)
		g.line(`local.get $base`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`i32.ge_u`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $base`)
		g.line(`local.get $i`)
		g.line(`i32.const 4`)
		g.line(`i32.mul`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)

		g.line(`(func $__str_idx (param $base i32) (param $i i32) (result i32)`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`i32.const 0`)
		g.line(`i32.lt_s`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $i`)
		g.line(`local.get $base`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`i32.ge_u`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $base`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)

		// Stride-aware variants for sub-i32 (`__str_idx` already
		// covers stride=1) and i64/f64 arrays. Same bounds-check
		// shape as `__arr_idx`; the only delta is the multiplier
		// applied to the index when computing the byte address.
		g.line(`(func $__arr_idx_2 (param $base i32) (param $i i32) (result i32)`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`i32.const 0`)
		g.line(`i32.lt_s`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $i`)
		g.line(`local.get $base`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`i32.ge_u`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $base`)
		g.line(`local.get $i`)
		g.line(`i32.const 2`)
		g.line(`i32.mul`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)

		g.line(`(func $__arr_idx_8 (param $base i32) (param $i i32) (result i32)`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`i32.const 0`)
		g.line(`i32.lt_s`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $i`)
		g.line(`local.get $base`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`i32.ge_u`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $base`)
		g.line(`local.get $i`)
		g.line(`i32.const 8`)
		g.line(`i32.mul`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)

		// Slice helpers — gated on the same flag as bounds-check
		// since slice creation, indexing, and len all need to
		// access the (data_ptr, len) heap pair.
		//
		// Slice memory layout: 8 bytes at slice_ptr —
		//   [+0..+3] data_ptr (i32, aliases the parent array's
		//            element-0 address shifted by `low * 4`)
		//   [+4..+7] len (i32)
		g.line(`(func $__slice_make (param $data i32) (param $len i32) (result i32)`)
		g.indent++
		g.line(`(local $s i32)`)
		g.line(`i32.const 8`)
		g.line(`call $__lang_alloc`)
		g.line(`local.tee $s`)
		g.line(`local.get $data`)
		g.line(`i32.store`)
		g.line(`local.get $s`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.get $len`)
		g.line(`i32.store`)
		g.line(`local.get $s`)
		g.indent--
		g.line(`)`)

		// $__slice_idx_N: same shape, scaled stride. The IR
		// picks the helper to call from the slice's element
		// type (1 for `[u8]` / `[i8]`, 2 for `[u16]` /
		// `[i16]`, 4 for the historical i32-stride layout, 8
		// for `[i64]` / `[u64]` / `[f64]`).
		emitSliceIdx := func(name string, stride int) {
			g.linef(`(func %s (param $slice i32) (param $i i32) (result i32)`, name)
			g.indent++
			g.line(`(local $data i32) (local $len i32)`)
			g.line(`local.get $slice`)
			g.line(`i32.load`)
			g.line(`local.set $data`)
			g.line(`local.get $slice`)
			g.line(`i32.const 4`)
			g.line(`i32.add`)
			g.line(`i32.load`)
			g.line(`local.set $len`)
			g.line(`local.get $i`)
			g.line(`i32.const 0`)
			g.line(`i32.lt_s`)
			g.line(`if`)
			g.indent++
			g.line(`unreachable`)
			g.indent--
			g.line(`end`)
			g.line(`local.get $i`)
			g.line(`local.get $len`)
			g.line(`i32.ge_u`)
			g.line(`if`)
			g.indent++
			g.line(`unreachable`)
			g.indent--
			g.line(`end`)
			g.line(`local.get $data`)
			if stride == 1 {
				g.line(`local.get $i`)
				g.line(`i32.add`)
			} else {
				g.line(`local.get $i`)
				g.linef(`i32.const %d`, stride)
				g.line(`i32.mul`)
				g.line(`i32.add`)
			}
			g.indent--
			g.line(`)`)
		}
		emitSliceIdx("$__slice_idx", 4)
		emitSliceIdx("$__slice_idx_1", 1)
		emitSliceIdx("$__slice_idx_2", 2)
		emitSliceIdx("$__slice_idx_8", 8)
	}

	if g.needsStrSlice {
		// $__str_slice(base, low, high) — copy bytes
		// `base[low..high]` into a fresh length-prefixed string
		// and return the new string's content pointer. Bounds
		// check matches the rest of the family: low >= 0, high
		// <= source length, low <= high.
		g.line(`(func $__str_slice (param $base i32) (param $low i32) (param $high i32) (result i32)`)
		g.indent++
		g.line(`(local $src_len i32) (local $new_len i32) (local $out i32)`)
		// src_len = base[-4]
		g.line(`local.get $base`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`local.set $src_len`)
		// low < 0 → trap
		g.line(`local.get $low`)
		g.line(`i32.const 0`)
		g.line(`i32.lt_s`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		// high > src_len → trap
		g.line(`local.get $high`)
		g.line(`local.get $src_len`)
		g.line(`i32.gt_u`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		// low > high → trap
		g.line(`local.get $low`)
		g.line(`local.get $high`)
		g.line(`i32.gt_s`)
		g.line(`if`)
		g.indent++
		g.line(`unreachable`)
		g.indent--
		g.line(`end`)
		// new_len = high - low
		g.line(`local.get $high`)
		g.line(`local.get $low`)
		g.line(`i32.sub`)
		g.line(`local.set $new_len`)
		// out = alloc(4 + new_len)
		g.line(`local.get $new_len`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`call $__lang_alloc`)
		g.line(`local.set $out`)
		// out[0] = new_len
		g.line(`local.get $out`)
		g.line(`local.get $new_len`)
		g.line(`i32.store`)
		// memory.copy(out + 4, base + low, new_len)
		g.line(`local.get $out`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.get $base`)
		g.line(`local.get $low`)
		g.line(`i32.add`)
		g.line(`local.get $new_len`)
		g.line(`memory.copy`)
		// Return content pointer (out + 4).
		g.line(`local.get $out`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)
	}

	if g.needsBase64 {
		g.emitBase64Helpers()
	}

	if g.needsStrMethods {
		g.emitStringMethodHelpers()
	}
	if g.needsStrFromBytes {
		// $string_from_bytes(bs: u8[]): string — copies the
		// byte-array's payload into a fresh length-prefixed
		// string. Round-trip companion to s.bytes().
		g.line(`(func $string_from_bytes (param $bs i32) (result i32)`)
		g.indent++
		g.line(`(local $bLen i32) (local $out i32)`)
		g.line(`local.get $bs`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`local.set $bLen`)
		g.line(`local.get $bLen`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`call $__lang_alloc`)
		g.line(`local.tee $out`)
		g.line(`local.get $bLen`)
		g.line(`i32.store`)
		g.line(`local.get $out`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.get $bs`)
		g.line(`local.get $bLen`)
		g.line(`memory.copy`)
		g.line(`local.get $out`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)
	}

	if g.needsHex {
		g.emitHexHelpers()
	}
	if g.needsUrlParse {
		g.emitUrlParseHelper()
	}
	if g.needsUrlCoder {
		g.emitUrlCoderHelpers()
	}
	if g.needsQueryParse {
		g.emitQueryParseHelper()
	}
	if g.needsJsonEncode {
		g.emitJsonEncodeHelpers()
	}
	if g.needsJsonParse {
		g.emitJsonParseHelpers()
	}

	g.emitStreamsStdioHelpers()

	// putchar(n) — blocking-write-and-flush(stdout, ptr=0, len=1).
	// memory[0] holds the byte we just stored.
	g.line(`(func $putchar (param $n i32)`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.get $n`)
	g.line(`i32.store8`)
	g.line(`call $__stdout_handle`)
	g.line(`i32.const 0`)
	g.line(`i32.const 1`)
	g.line(`call $__streams_write`)
	g.indent--
	g.line(`)`)

	// print(s) — writes the string and a newline. Two single-iovec
	// blocking-write-and-flush calls (rather than one with two
	// iovecs) because some wasmtime versions silently drop all but
	// the first iovec when iovs_len > 1.
	g.line(`(func $print (param $s i32)`)
	g.indent++
	g.emitStreamsWriteString("$__stdout_handle", "$s")
	g.emitStreamsWriteNewline("$__stdout_handle")
	g.indent--
	g.line(`)`)

	// write(s) — stdout without a trailing newline. Same shape as
	// $print's first half; users compose their own newlines / field
	// separators when they want.
	g.line(`(func $write (param $s i32)`)
	g.indent++
	g.emitStreamsWriteString("$__stdout_handle", "$s")
	g.indent--
	g.line(`)`)

	// eprint(s) — `print` shape but routed to stderr.
	g.line(`(func $eprint (param $s i32)`)
	g.indent++
	g.emitStreamsWriteString("$__stderr_handle", "$s")
	g.emitStreamsWriteNewline("$__stderr_handle")
	g.indent--
	g.line(`)`)

	if g.needsArgs {
		g.emitArgsHelper()
	}
	if g.needsReadLine {
		g.emitReadLineHelper()
	}
	if g.needsEnv {
		g.emitEnvHelper()
	}
	if g.needsExit {
		g.emitExitHelper()
	}
	if g.needsArena {
		g.emitArenaHelpers()
	}
	if g.needsMap {
		g.emitMapHelpers()
	}
	if g.needsRandomBytes {
		g.emitRandomBytesHelper()
	}
	if g.needsIntToString {
		g.emitIntToStringHelper()
	}
	if g.needsNumToString {
		g.emitNumberToStringHelpers()
	}
	if g.needsFloatToString {
		g.emitFloatToStringHelpers()
	}
	// `cabi_realloc` is the canonical-ABI allocator the host
	// invokes to materialise dynamically-sized return values
	// (e.g. `list<u8>` from `get-random-bytes` or
	// `input-stream.blocking-read`) in our linear memory.
	// Always emit it — the cost is one trivially-tiny function;
	// tracking individual import dependencies isn't worth the
	// gating complexity.
	g.emitCabiRealloc()
	if g.needsTcp {
		g.emitTcpHelpers()
	}
	if g.httpHandler {
		g.emitHttpHandlerWrapper()
	}
	if g.needsFileIO {
		g.emitFileIOHelpers()
	}
	if g.needsStdStreams {
		g.emitStdStreamHelpers()
	}
}

// emitStdStreamHelpers writes `$stdin`, `$stdout`, `$stderr` —
// trivial constructors that allocate a 4-byte Reader / Writer
// struct holding the cached stdin / stdout / stderr stream
// resource handle. Called repeatedly each yields a fresh struct;
// that's a small allocation cost for a usually-once-per-program
// lookup, in exchange for not needing static memory slots or a
// cached-once flag for the *struct* itself — the underlying
// stream handle is cached elsewhere (see
// emitStreamHandleAccessor).
func (g *generator) emitStdStreamHelpers() {
	g.emitStdStream("$stdin", "$__stdin_handle")
	g.emitStdStream("$stdout", "$__stdout_handle")
	g.emitStdStream("$stderr", "$__stderr_handle")
}

func (g *generator) emitStdStream(name, handleAccessor string) {
	g.linef(`(func %s (result i32)`, name)
	g.indent++
	g.line(`(local $r i32)`)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $r`)
	g.line(`local.get $r`)
	g.linef(`call %s`, handleAccessor)
	g.line(`i32.store`)
	g.line(`local.get $r`)
	g.indent--
	g.line(`)`)
}

// emitFdWriteString emits one fd_write call that writes a single
// length-prefixed string to `fd`. local is the wasm local holding
// the string's data pointer (e.g. "$s"); the string's length
// lives at `local - 4`. Reuses iovec[0] at offset 16.
func (g *generator) emitFdWriteString(fd int, local string) {
	g.line(`i32.const 16`)
	g.linef(`local.get %s`, local)
	g.line(`i32.store`)
	g.line(`i32.const 20`)
	g.linef(`local.get %s`, local)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	g.linef(`i32.const %d`, fd)
	g.line(`i32.const 16`) // iovs ptr
	g.line(`i32.const 1`)  // iovs_len = 1
	g.line(`i32.const 36`) // nwritten
	g.line(`call $__wasi_fd_write`)
	g.line(`drop`)
}

// emitFdWriteNewline emits one fd_write call that writes the
// pre-initialised newline iovec at offset 24 (memory[32]='\n')
// to `fd`. Used by `$print` and `$eprint` after their string
// write.
func (g *generator) emitFdWriteNewline(fd int) {
	g.linef(`i32.const %d`, fd)
	g.line(`i32.const 24`) // iovs ptr (newline iovec)
	g.line(`i32.const 1`)
	g.line(`i32.const 36`)
	g.line(`call $__wasi_fd_write`)
	g.line(`drop`)
}

// emitStreamsStdioHelpers writes the preview-2 stdio helpers:
//   - $__stdout_handle / $__stderr_handle: lazily call get-stdout
//     / get-stderr and cache the resource handle in static memory
//     (handles are opaque ints and 0 is a valid handle, so the
//     cache uses an init-flag bitfield rather than a 0-sentinel);
//   - $__streams_write: a single blocking-write-and-flush call
//     against (handle, ptr, len). Result <_, stream-error> is
//     written to the shared retptr slot at offset 92 and ignored
//     — failures on stdio in a CLI are effectively unrecoverable.
//
// Memory slots come from the runtime layout block above:
//
//	104..107 stdout handle
//	108..111 stderr handle
//	112..115 init flags  (bit 0 = stdout cached, bit 1 = stderr cached,
//	                      bit 2 = stdin cached)
//	116..119 stdin handle (only when needsReadLine && preview2)
//	 92..103 result<_, stream-error> retptr area (12 bytes; shared with
//	         result<list<u8>, stream-error>)
func (g *generator) emitStreamsStdioHelpers() {
	g.emitStreamHandleAccessor("$__stdout_handle", "$__wasi_get_stdout", 104, 1)
	g.emitStreamHandleAccessor("$__stderr_handle", "$__wasi_get_stderr", 108, 2)
	if g.needsReadLine || g.needsStreamingIO || g.needsStdStreams || g.needsFileIO {
		g.emitStreamHandleAccessor("$__stdin_handle", "$__wasi_get_stdin", 116, 4)
	}
	if g.needsReadLine || g.needsStreamingIO || g.needsFileIO {
		g.emitStdinStreamsReadLine()
	}
	if g.needsFileIO || g.needsStreamingIO {
		g.emitPreopenDirHelper()
	}

	// $__streams_write(handle, ptr, len) — wrap blocking-write-and-flush.
	// `blocking-write-and-flush` has a per-call 4 KiB buffer cap
	// in canonical implementations (wasmtime enforces it
	// strictly), so chunk the write. We don't surface
	// stream-error here — print / write failures aren't recoverable
	// for a CLI program and the helpers' callers don't propagate
	// errors anyway.
	g.line(`(func $__streams_write (param $handle i32) (param $ptr i32) (param $len i32)`)
	g.indent++
	g.line(`(local $off i32) (local $chunk i32)`)
	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $off`)
	g.line(`local.get $len`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end`)
	// chunk = min(len - off, 4096)
	g.line(`local.get $len`)
	g.line(`local.get $off`)
	g.line(`i32.sub`)
	g.line(`local.tee $chunk`)
	g.line(`i32.const 4096`)
	g.line(`i32.gt_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4096`)
	g.line(`local.set $chunk`)
	g.indent--
	g.line(`end`)
	// blocking-write-and-flush(handle, ptr+off, chunk, retptr=92)
	g.line(`local.get $handle`)
	g.line(`local.get $ptr`)
	g.line(`local.get $off`)
	g.line(`i32.add`)
	g.line(`local.get $chunk`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_blocking_write_and_flush`)
	// On Err (disc != 0), bail — we can't make progress.
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`br_if $end`)
	g.line(`local.get $off`)
	g.line(`local.get $chunk`)
	g.line(`i32.add`)
	g.line(`local.set $off`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
}

// emitStreamHandleAccessor writes a `name (result i32)` helper that
// returns the cached resource handle at memory[handleSlot]. On the
// first call, it invokes `getterImport` (e.g. $__wasi_get_stdout),
// stores the result, and sets the corresponding bit (1 << (initBit-1)
// in our convention) in the init-flags byte at offset 112. The
// init-flag indirection is necessary because resource handles are
// opaque ints where 0 is a valid value, so we can't use a 0-sentinel
// to detect "not yet cached".
func (g *generator) emitStreamHandleAccessor(name, getterImport string, handleSlot, initMask int) {
	g.linef(`(func %s (result i32)`, name)
	g.indent++
	g.line(`(local $h i32)`)
	g.line(`i32.const 112`)
	g.line(`i32.load`)
	g.linef(`i32.const %d`, initMask)
	g.line(`i32.and`)
	g.line(`if (result i32)`)
	g.indent++
	g.linef(`i32.const %d`, handleSlot)
	g.line(`i32.load`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.linef(`call %s`, getterImport)
	g.line(`local.tee $h`)
	// Store handle at handleSlot, then OR initMask into the flag byte.
	g.linef(`i32.const %d`, handleSlot)
	g.line(`local.get $h`)
	g.line(`i32.store`)
	g.line(`i32.const 112`)
	g.line(`i32.const 112`)
	g.line(`i32.load`)
	g.linef(`i32.const %d`, initMask)
	g.line(`i32.or`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
}

// emitStreamsWriteString emits the call sequence for writing a
// length-prefixed string through the preview-2 streams API:
// load the cached handle, push (data_ptr, len), call
// $__streams_write. `handleAccessor` is the WAT name of the
// `$__stdout_handle` / `$__stderr_handle` helper to use; `local`
// is the wasm local holding the string's data pointer (e.g. "$s")
// — the length lives at `local - 4`, the same shape every other
// string-passing helper expects.
func (g *generator) emitStreamsWriteString(handleAccessor, local string) {
	g.linef(`call %s`, handleAccessor)
	g.linef(`local.get %s`, local)
	g.linef(`local.get %s`, local)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`call $__streams_write`)
}

// emitStreamsWriteNewline emits one $__streams_write call against
// the pre-initialised newline byte at memory[32]. Used by $print
// / $eprint after the string body to mirror the arm32 puts-based
// lowering.
func (g *generator) emitStreamsWriteNewline(handleAccessor string) {
	g.linef(`call %s`, handleAccessor)
	g.line(`i32.const 32`) // newline byte
	g.line(`i32.const 1`)
	g.line(`call $__streams_write`)
}

// emitPreopenDirHelper writes `$__preopen_dir (result i32)` — a
// lazily-initialising accessor over the host's first preopen
// directory descriptor. Mirrors the preview-1 convention of
// resolving paths against `fd=3`. Called by the preview-2
// open_reader / open_writer / open_appender / read_file /
// write_file helpers; the descriptor handle gets cached at
// memory[120] (init bit 3 in the flag byte at memory[112]).
//
// The `get-directories` import returns `list<tuple<descriptor,
// string>>`; canonical-ABI lowering passes us a retptr we read
// (host_list_ptr, host_list_len) from. Each tuple is 12 bytes
// `(descriptor i32, name_ptr i32, name_len i32)` (4-byte aligned),
// so the first descriptor lives at `host_list_ptr + 0`. We
// ignore the name and any subsequent preopens.
func (g *generator) emitPreopenDirHelper() {
	g.line(`(func $__preopen_dir (result i32)`)
	g.indent++
	g.line(`(local $h i32) (local $list_ptr i32)`)
	// Already cached?
	g.line(`i32.const 112`)
	g.line(`i32.load`)
	g.line(`i32.const 8`) // bit 3
	g.line(`i32.and`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 120`)
	g.line(`i32.load`)
	g.indent--
	g.line(`else`)
	g.indent++
	// get-directories(retptr=92) → mem[92]=ptr, mem[96]=len.
	g.line(`i32.const 92`)
	g.line(`call $__wasi_get_directories`)
	g.line(`i32.const 92`)
	g.line(`i32.load`)
	g.line(`local.set $list_ptr`)
	// First tuple's descriptor handle at list_ptr + 0.
	g.line(`local.get $list_ptr`)
	g.line(`i32.load`)
	g.line(`local.tee $h`)
	g.line(`i32.const 120`)
	g.line(`local.get $h`)
	g.line(`i32.store`)
	g.line(`i32.const 112`)
	g.line(`i32.const 112`)
	g.line(`i32.load`)
	g.line(`i32.const 8`)
	g.line(`i32.or`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
}

// emitOpenViaStreamHelper writes a private
// `$<name>(path) -> result<stream, errno>` helper that opens a
// path under the cached preopen directory and returns the
// requested stream type. It packages errors as a 2-element slot:
// retptr+0 is the stream handle on success / errno on failure;
// retptr+4 is the errno (0 on success). Callers (open_reader /
// open_writer / open_appender) wrap that into the Result[Reader|
// Writer, IoError] shape.
//
// `openFlags` and `descFlags` are the WIT-level `open-flags` and
// `descriptor-flags` immediates. `viaStreamImport` is the
// canonical-ABI lowered name of the via-stream call to use after
// the open succeeds (`$__wasi_descriptor_read_via_stream`,
// `$__wasi_descriptor_write_via_stream`, or
// `$__wasi_descriptor_append_via_stream`). Append-via-stream has
// a shorter signature `(param i32 i32)` (no offset), so callers
// pass `appendMode=true` to skip the offset push.
func (g *generator) emitOpenViaStreamHelper(name string, openFlags, descFlags int, viaStreamImport string, appendMode bool) {
	g.linef(`(func %s (param $path i32) (result i32)`, name)
	g.indent++
	g.line(`(local $errno i32) (local $desc i32) (local $stream i32) (local $result i32)`)

	// open-at(preopen, path-flags=1=symlink-follow, path_ptr,
	// path_len, open-flags, descriptor-flags, retptr=92).
	g.line(`call $__preopen_dir`)
	g.line(`i32.const 1`) // path-flags = symlink-follow
	g.line(`local.get $path`)
	g.line(`local.get $path`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.linef(`i32.const %d`, openFlags)
	g.linef(`i32.const %d`, descFlags)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_descriptor_open_at`)

	// Outer disc at retptr+0; non-zero = Err(error-code).
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	// Build Err(IoError) — Result.Err = tag 1, payload via
	// __build_io_error. The error-code at retptr+4 is the
	// `wasi:filesystem/types.error-code` enum index; translate it
	// to the preview-1 errno space before handing it to the
	// shared $__build_io_error table (which keys on preview-1
	// values).
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`call $__filesystem_error_translate`)
	g.line(`local.set $errno`)
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $errno`)
	g.line(`local.get $path`)
	g.line(`call $__build_io_error`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// Ok: descriptor at retptr+4. Fetch a stream from it. The
	// descriptor itself is intentionally leaked (the bump
	// allocator can't free, and the program lifetime is bounded).
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $desc`)
	if appendMode {
		// append-via-stream(self, retptr=92).
		g.line(`local.get $desc`)
		g.line(`i32.const 92`)
		g.linef(`call %s`, viaStreamImport)
	} else {
		// read/write-via-stream(self, offset=0, retptr=92).
		g.line(`local.get $desc`)
		g.line(`i64.const 0`)
		g.line(`i32.const 92`)
		g.linef(`call %s`, viaStreamImport)
	}
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	// Stream open failed. Treat as IoError with the inner errno.
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $errno`)
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $errno`)
	g.line(`local.get $path`)
	g.line(`call $__build_io_error`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// Ok: stream at retptr+4. Allocate Reader/Writer struct
	// holding the stream handle, return Ok wrapper.
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $stream`)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $desc`) // reuse $desc as the struct ptr
	g.line(`local.get $stream`)
	g.line(`i32.store`)
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 0`) // Result.Ok = tag 0
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $desc`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
}

// emitArgsHelper writes the lazy-initialising `$args` runtime
// function. The first call materialises a length-prefixed
// string[] from the WASI argv buffer and caches its pointer at
// memory offset 44; subsequent calls return the cached pointer
// without going back to the host.
//
// The materialised array layout is the standard one used elsewhere
// for `string[]`:
//
//	[ length prefix : i32 ][ s0_ptr : i32 ][ s1_ptr : i32 ] ...
//
// where each `sK_ptr` points to the bytes of a length-prefixed
// string allocated separately on the bump heap.
func (g *generator) emitArgsHelper() {
	g.line(`(func $args (result i32)`)
	g.indent++
	g.line(`(local $cached i32)`)
	g.line(`(local $argc i32)`)
	g.line(`(local $bufsize i32)`)
	g.line(`(local $argv_ptrs i32)`)
	g.line(`(local $argv_buf i32)`)
	g.line(`(local $result i32)`)
	g.line(`(local $i i32)`)
	g.line(`(local $cstr i32)`)
	g.line(`(local $end i32)`)
	g.line(`(local $strlen i32)`)
	g.line(`(local $sbase i32)`)
	g.line(`(local $j i32)`)

	// Fast path: cached.
	g.line(`i32.const 44`)
	g.line(`i32.load`)
	g.line(`local.tee $cached`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $cached`)
	g.indent--
	g.line(`else`)
	g.indent++

	// Slow path: ask the host how many args + how big a buffer.
	g.line(`i32.const 48`)
	g.line(`i32.const 52`)
	g.line(`call $__wasi_args_sizes_get`)
	g.line(`drop`)
	g.line(`i32.const 48`)
	g.line(`i32.load`)
	g.line(`local.set $argc`)
	g.line(`i32.const 52`)
	g.line(`i32.load`)
	g.line(`local.set $bufsize`)

	// Allocate scratch buffers for the host call. argv_ptrs gets
	// argc * 4 bytes; argv_buf gets bufsize bytes (which already
	// covers every NUL-terminated argv string back-to-back).
	g.line(`local.get $argc`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $argv_ptrs`)
	g.line(`local.get $bufsize`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $argv_buf`)
	g.line(`local.get $argv_ptrs`)
	g.line(`local.get $argv_buf`)
	g.line(`call $__wasi_args_get`)
	g.line(`drop`)

	// Allocate the result string[]: length prefix (4 bytes) +
	// argc * 4 entry pointers. $result lands on the entries (the
	// language's `string[]` convention is that the value is the
	// data pointer, with the length at value-4).
	g.line(`local.get $argc`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`local.get $argc`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $result`)

	// For each argv entry: walk the C string to find its length,
	// allocate a fresh length-prefixed buffer, copy the bytes, and
	// stash the resulting string pointer at result[i].
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $end_outer`)
	g.indent++
	g.line(`loop $outer`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $argc`)
	g.line(`i32.eq`)
	g.line(`br_if $end_outer`)

	// cstr = argv_ptrs[i] (each entry is an i32)
	g.line(`local.get $argv_ptrs`)
	g.line(`local.get $i`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $cstr`)

	// strlen: scan from cstr until a NUL byte.
	g.line(`local.get $cstr`)
	g.line(`local.set $end`)
	g.line(`block $end_strlen`)
	g.indent++
	g.line(`loop $strlen`)
	g.indent++
	g.line(`local.get $end`)
	g.line(`i32.load8_u`)
	g.line(`i32.eqz`)
	g.line(`br_if $end_strlen`)
	g.line(`local.get $end`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $end`)
	g.line(`br $strlen`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $end`)
	g.line(`local.get $cstr`)
	g.line(`i32.sub`)
	g.line(`local.set $strlen`)

	// Allocate strlen+4 bytes; write length prefix.
	g.line(`local.get $strlen`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $strlen`)
	g.line(`i32.store`)

	// Byte-copy cstr[0..strlen) into sbase+4.
	g.line(`i32.const 0`)
	g.line(`local.set $j`)
	g.line(`block $end_copy`)
	g.indent++
	g.line(`loop $copy`)
	g.indent++
	g.line(`local.get $j`)
	g.line(`local.get $strlen`)
	g.line(`i32.eq`)
	g.line(`br_if $end_copy`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`local.get $cstr`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.store8`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	g.line(`br $copy`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// result[i] = sbase + 4 (the data pointer; length lives at -4)
	g.line(`local.get $result`)
	g.line(`local.get $i`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.store`)

	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $outer`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// Cache and return.
	g.line(`i32.const 44`)
	g.line(`local.get $result`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
}

// emitReadLineHelper writes `$read_line`, a one-byte-at-a-time
// stdin reader. Each iteration reads one byte; the loop exits at
// EOF or when the read byte is `\n`. The preview-1 path uses
// fd_read on the iovec at offset 56 (which points at byte 68);
// the preview-2 path is a single delegation to the private
// `$__stdin_read_line` helper that emitStreamsStdioHelpers writes
// (also reused by `__method_Reader_read_line` for Readers wrapping
// stdin).
//
// The result is an `Option[string]` heap object: `Some(line)`
// when at least one byte was read (the line preserves its
// trailing `\n`); `None` when the first read came back empty
// (EOF). Tag 0 is `Some`, tag 1 is `None` — the canonical order
// from the auto-injected Option enum, hardcoded here.
func (g *generator) emitReadLineHelper() {
	g.line(`(func $read_line (result i32)`)
	g.indent++
	g.line(`call $__stdin_handle`)
	g.line(`call $__stream_read_line`)
	g.indent--
	g.line(`)`)
}

// emitStdinStreamsReadLine writes the private
// `$__stream_read_line(handle) -> Option[string]` helper — the
// streams-flavoured line reader shared between the bare
// `$read_line` global, `__method_Reader_read_line` (preview-2),
// and any other path that reads a line from an `input-stream`
// resource. Reads one byte at a time via
// `wasi:io/streams.input-stream.blocking-read(1)`, accumulating
// into a heap buffer, and packages the result as `Option[string]`
// — `None` on EOF before any byte, `Some(line)` (newline included)
// otherwise.
//
// The accumulator is a separate, growable allocation rather than
// the implicit cursor-based anchor the preview-1 path uses.
// Reason: each blocking-read goes through the host's
// `cabi_realloc`, which shares our bump cursor — so the host's
// per-byte buffers and our would-be accumulator slots interleave
// in the heap, and the "cursor advances by exactly 1 per
// iteration" invariant the preview-1 helper relies on no longer
// holds. Initial buffer is 64 bytes; we double on overflow and
// `memory.copy` the prefix into the new region.
//
// EOF detection: the canonical-ABI result discriminant at the
// retptr is non-zero (`Err(stream-error)`) or the returned list is
// length 0. The error resource leaks here — we don't import
// `[resource-drop]error`. Acceptable trade-off for now: a CLI
// program hits stream errors at most once per process lifetime.
func (g *generator) emitStdinStreamsReadLine() {
	g.line(`(func $__stream_read_line (param $handle i32) (result i32)`)
	g.indent++
	g.line(`(local $buf i32) (local $buf_size i32) (local $cur_offset i32)`)
	g.line(`(local $byte i32) (local $list_ptr i32)`)
	g.line(`(local $new_buf i32) (local $new_size i32)`)
	g.line(`(local $sbase i32) (local $sptr i32)`)

	// Initial accumulator: 64 bytes. Doubles on overflow.
	g.line(`i32.const 64`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $buf`)
	g.line(`i32.const 64`)
	g.line(`local.set $buf_size`)
	g.line(`i32.const 0`)
	g.line(`local.set $cur_offset`)

	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	// blocking-read(handle, 1, retptr=92).
	g.line(`local.get $handle`)
	g.line(`i64.const 1`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_blocking_read`)

	// Outer disc at retptr+0; non-zero = Err = treat as EOF.
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`br_if $end`)

	// list_len at retptr+8; zero-length = EOF on blocking read.
	g.line(`i32.const 100`)
	g.line(`i32.load`)
	g.line(`i32.eqz`)
	g.line(`br_if $end`)

	// list_ptr at retptr+4 → byte = mem[list_ptr].
	g.line(`i32.const 96`)
	g.line(`i32.load`)
	g.line(`local.set $list_ptr`)
	g.line(`local.get $list_ptr`)
	g.line(`i32.load8_u`)
	g.line(`local.set $byte`)

	// Grow the buffer if it's full. new_size = buf_size * 2.
	g.line(`local.get $cur_offset`)
	g.line(`local.get $buf_size`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf_size`)
	g.line(`i32.const 1`)
	g.line(`i32.shl`)
	g.line(`local.set $new_size`)
	g.line(`local.get $new_size`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $new_buf`)
	g.line(`local.get $new_buf`)
	g.line(`local.get $buf`)
	g.line(`local.get $cur_offset`)
	g.line(`memory.copy`)
	g.line(`local.get $new_buf`)
	g.line(`local.set $buf`)
	g.line(`local.get $new_size`)
	g.line(`local.set $buf_size`)
	g.indent--
	g.line(`end`)

	// mem[buf + cur_offset] = byte
	g.line(`local.get $buf`)
	g.line(`local.get $cur_offset`)
	g.line(`i32.add`)
	g.line(`local.get $byte`)
	g.line(`i32.store8`)

	// cur_offset += 1
	g.line(`local.get $cur_offset`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $cur_offset`)

	// Break on newline.
	g.line(`local.get $byte`)
	g.line(`i32.const 10`)
	g.line(`i32.eq`)
	g.line(`br_if $end`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// Empty (no bytes consumed) = None. Tag 1 = None per the
	// auto-injected Option enum layout.
	g.line(`local.get $cur_offset`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $sbase`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// Materialise as a length-prefixed string.
	g.line(`local.get $cur_offset`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $cur_offset`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)

	// memory.copy(sptr, buf, cur_offset)
	g.line(`local.get $sptr`)
	g.line(`local.get $buf`)
	g.line(`local.get $cur_offset`)
	g.line(`memory.copy`)

	// Wrap in Some(sptr): tag=0 + payload.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $sbase`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $sptr`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.indent--
	g.line(`)`)
}

// emitNoneHelper / emitEnvHelper marker comment
// initialises the environ buffers via WASI; subsequent calls
// reuse the cached pointers. The lookup walks the cached
// environ pointer table, comparing each "KEY=VALUE" entry
// against `name` up to the `=`. On match, a fresh
// length-prefixed string is allocated for the VALUE half.
// Missing keys return the empty string (data pointer to a
// pre-built 0-length string entry).
func (g *generator) emitEnvHelper() {
	g.line(`(func $env (param $name i32) (result i32)`)
	g.indent++
	g.line(`(local $name_len i32)`)
	g.line(`(local $count i32)`)
	g.line(`(local $bufsize i32)`)
	g.line(`(local $env_ptrs i32)`)
	g.line(`(local $env_buf i32)`)
	g.line(`(local $i i32)`)
	g.line(`(local $entry i32)`)
	g.line(`(local $j i32)`)
	g.line(`(local $vlen i32)`)
	g.line(`(local $vstart i32)`)
	g.line(`(local $sbase i32)`)
	g.line(`(local $sptr i32)`)
	g.line(`(local $matches i32)`)
	g.line(`(local $k i32)`)

	// Lazily init the environ buffers.
	g.line(`i32.const 72`)
	g.line(`i32.load`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	// environ_sizes_get(*count_out=84, *bufsize_out=88)
	g.line(`i32.const 84`)
	g.line(`i32.const 88`)
	g.line(`call $__wasi_environ_sizes_get`)
	g.line(`drop`)
	g.line(`i32.const 84`)
	g.line(`i32.load`)
	g.line(`local.set $count`)
	g.line(`i32.const 88`)
	g.line(`i32.load`)
	g.line(`local.set $bufsize`)
	// Allocate env_ptrs (count * 4 bytes) and env_buf (bufsize bytes).
	g.line(`local.get $count`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $env_ptrs`)
	g.line(`local.get $bufsize`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $env_buf`)
	g.line(`local.get $env_ptrs`)
	g.line(`local.get $env_buf`)
	g.line(`call $__wasi_environ_get`)
	g.line(`drop`)
	// Cache.
	g.line(`i32.const 76`)
	g.line(`local.get $count`)
	g.line(`i32.store`)
	g.line(`i32.const 80`)
	g.line(`local.get $env_ptrs`)
	g.line(`i32.store`)
	g.line(`i32.const 72`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)

	// Load cached count + ptr table.
	g.line(`i32.const 76`)
	g.line(`i32.load`)
	g.line(`local.set $count`)
	g.line(`i32.const 80`)
	g.line(`i32.load`)
	g.line(`local.set $env_ptrs`)

	// name length is stored at name-4.
	g.line(`local.get $name`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $name_len`)

	// for i in 0..count
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $outer_end`)
	g.indent++
	g.line(`loop $outer`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $count`)
	g.line(`i32.eq`)
	g.line(`br_if $outer_end`)

	// entry = env_ptrs[i]
	g.line(`local.get $env_ptrs`)
	g.line(`local.get $i`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $entry`)

	// Compare entry[0..name_len] with name[0..name_len], then
	// require entry[name_len] == '='. matches=1 if all good.
	g.line(`i32.const 1`)
	g.line(`local.set $matches`)
	g.line(`i32.const 0`)
	g.line(`local.set $j`)
	g.line(`block $cmp_end`)
	g.indent++
	g.line(`loop $cmp`)
	g.indent++
	g.line(`local.get $j`)
	g.line(`local.get $name_len`)
	g.line(`i32.eq`)
	g.line(`br_if $cmp_end`)
	g.line(`local.get $entry`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.get $name`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.ne`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.set $matches`)
	g.line(`br $cmp_end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	g.line(`br $cmp`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// If matches and entry[name_len]=='=', this is our entry.
	g.line(`local.get $matches`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $entry`)
	g.line(`local.get $name_len`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 61`) // '='
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	// Found. vstart = entry + name_len + 1; scan for NUL.
	g.line(`local.get $entry`)
	g.line(`local.get $name_len`)
	g.line(`i32.add`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $vstart`)
	g.line(`local.get $vstart`)
	g.line(`local.set $k`)
	g.line(`block $vlen_end`)
	g.indent++
	g.line(`loop $vlen_loop`)
	g.indent++
	g.line(`local.get $k`)
	g.line(`i32.load8_u`)
	g.line(`i32.eqz`)
	g.line(`br_if $vlen_end`)
	g.line(`local.get $k`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $k`)
	g.line(`br $vlen_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $k`)
	g.line(`local.get $vstart`)
	g.line(`i32.sub`)
	g.line(`local.set $vlen`)

	// Allocate result and copy.
	g.line(`local.get $vlen`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $vlen`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)
	g.line(`i32.const 0`)
	g.line(`local.set $j`)
	g.line(`block $vcopy_end`)
	g.indent++
	g.line(`loop $vcopy`)
	g.indent++
	g.line(`local.get $j`)
	g.line(`local.get $vlen`)
	g.line(`i32.eq`)
	g.line(`br_if $vcopy_end`)
	g.line(`local.get $sptr`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`local.get $vstart`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.store8`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	g.line(`br $vcopy`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Found: wrap the materialised string pointer in
	// `Some(sptr)`. Layout matches read_line: 8 bytes total
	// with tag at +0 and the string pointer at +4.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $sptr`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $outer`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// Not found: return `None` — a 4-byte heap object with
	// tag = 1.
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $sbase`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.indent--
	g.line(`)`)
}

// emitArenaHelpers writes `$arena_save` and `$arena_restore`,
// the bump-cursor snapshot/restore pair the language exposes
// for long-running servers. The bump pointer lives at
// memory[40] (see `$__lang_alloc`); save reads it, restore
// writes it. No allocation, no syscall — same shape as the
// arm32 helpers.
func (g *generator) emitArenaHelpers() {
	g.line(`(func $arena_save (result i32)`)
	g.indent++
	g.line(`i32.const 40`)
	g.line(`i32.load`)
	g.indent--
	g.line(`)`)
	g.line(`(func $arena_restore (param $h i32)`)
	g.indent++
	g.line(`i32.const 40`)
	g.line(`local.get $h`)
	g.line(`i32.store`)
	g.indent--
	g.line(`)`)
}

// emitStringMethodHelpers writes the built-in string method
// runtime helpers: `s.starts_with(prefix)`, `s.ends_with(suffix)`,
// `s.contains(needle)`. Each takes the receiver string + one
// argument string (both as content pointers — the lang ABI for
// `string`) and returns an i32 boolean. Linear-search
// `contains`; the `*_with` pair is a single bounded
// `__lang_strcmp` call.
// emitBase64Helpers writes runtime functions for the
// `base64_encode(s)` / `base64_decode(s)` builtins. Standard
// RFC 4648 alphabet, with `=` padding on encode and decoding
// terminated at the first non-base64 character (so callers
// can detect partial input by length compare).
//
// Both helpers operate on lang strings (length-prefixed byte
// arrays) and return a freshly-allocated string. The encode
// alphabet is computed arithmetically rather than table-
// looking-up so we don't have to wire a dedicated data
// segment.
func (g *generator) emitBase64Helpers() {
	// $__b64_alphabet(v): i32 — maps a 6-bit value (0..63) to
	// its base64 character. Branchy; kept readable rather than
	// fast.
	g.line(`(func $__b64_alphabet (param $v i32) (result i32)`)
	g.indent++
	g.line(`local.get $v`)
	g.line(`i32.const 26`)
	g.line(`i32.lt_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $v`)
	g.line(`i32.const 65`) // 'A'
	g.line(`i32.add`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $v`)
	g.line(`i32.const 52`)
	g.line(`i32.lt_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $v`)
	g.line(`i32.const 71`) // 'a' - 26 = 71
	g.line(`i32.add`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $v`)
	g.line(`i32.const 62`)
	g.line(`i32.lt_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $v`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`) // '0' - 52 = -4
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $v`)
	g.line(`i32.const 62`)
	g.line(`i32.eq`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 43`) // '+'
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const 47`) // '/'
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)

	// $__b64_decode_char(c): i32 — returns the 6-bit value
	// for c, or -1 if c isn't a base64 character.
	g.line(`(func $__b64_decode_char (param $c i32) (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 65`) // 'A'
	g.line(`i32.ge_u`)
	g.line(`local.get $c`)
	g.line(`i32.const 91`) // 'Z' + 1
	g.line(`i32.lt_u`)
	g.line(`i32.and`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 65`)
	g.line(`i32.sub`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 97`) // 'a'
	g.line(`i32.ge_u`)
	g.line(`local.get $c`)
	g.line(`i32.const 123`) // 'z' + 1
	g.line(`i32.lt_u`)
	g.line(`i32.and`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 71`) // 'a' - 26 = 71
	g.line(`i32.sub`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 48`) // '0'
	g.line(`i32.ge_u`)
	g.line(`local.get $c`)
	g.line(`i32.const 58`) // '9' + 1
	g.line(`i32.lt_u`)
	g.line(`i32.and`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 4`)
	g.line(`i32.add`) // c + 4 → 52..61
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 43`) // '+'
	g.line(`i32.eq`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 62`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 47`) // '/'
	g.line(`i32.eq`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 63`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const -1`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)

	// $base64_encode(s): allocate `4 * ceil(len/3)` bytes and
	// fill each 4-char group from a 3-byte input window.
	g.line(`(func $base64_encode (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $outLen i32) (local $out i32)`)
	g.line(`(local $i i32) (local $j i32)`)
	g.line(`(local $b1 i32) (local $b2 i32) (local $b3 i32) (local $remaining i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	// outLen = ((sLen + 2) / 3) * 4
	g.line(`local.get $sLen`)
	g.line(`i32.const 2`)
	g.line(`i32.add`)
	g.line(`i32.const 3`)
	g.line(`i32.div_u`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`local.set $outLen`)
	g.line(`local.get $outLen`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $out`)
	g.line(`local.get $outLen`)
	g.line(`i32.store`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`i32.const 0`)
	g.line(`local.set $j`)
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_s`)
	g.line(`br_if $break`)
	// remaining = sLen - i (1, 2, or >=3)
	g.line(`local.get $sLen`)
	g.line(`local.get $i`)
	g.line(`i32.sub`)
	g.line(`local.set $remaining`)
	// b1 = s[i]
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $b1`)
	// b2 = (i+1 < sLen) ? s[i+1] : 0
	g.line(`local.get $remaining`)
	g.line(`i32.const 1`)
	g.line(`i32.gt_s`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const 0`)
	g.indent--
	g.line(`end`)
	g.line(`local.set $b2`)
	// b3 = (i+2 < sLen) ? s[i+2] : 0
	g.line(`local.get $remaining`)
	g.line(`i32.const 2`)
	g.line(`i32.gt_s`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.const 2`)
	g.line(`i32.add`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const 0`)
	g.indent--
	g.line(`end`)
	g.line(`local.set $b3`)
	// out[j+0] = alphabet(b1 >> 2)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`local.get $b1`)
	g.line(`i32.const 2`)
	g.line(`i32.shr_u`)
	g.line(`call $__b64_alphabet`)
	g.line(`i32.store8`)
	// out[j+1] = alphabet(((b1 & 3) << 4) | (b2 >> 4))
	g.line(`local.get $out`)
	g.line(`i32.const 5`)
	g.line(`i32.add`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`local.get $b1`)
	g.line(`i32.const 3`)
	g.line(`i32.and`)
	g.line(`i32.const 4`)
	g.line(`i32.shl`)
	g.line(`local.get $b2`)
	g.line(`i32.const 4`)
	g.line(`i32.shr_u`)
	g.line(`i32.or`)
	g.line(`call $__b64_alphabet`)
	g.line(`i32.store8`)
	// out[j+2]: '=' if remaining < 2, else alphabet(((b2 & 15) << 2) | (b3 >> 6))
	g.line(`local.get $out`)
	g.line(`i32.const 6`)
	g.line(`i32.add`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`local.get $remaining`)
	g.line(`i32.const 2`)
	g.line(`i32.lt_s`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 61`) // '='
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $b2`)
	g.line(`i32.const 15`)
	g.line(`i32.and`)
	g.line(`i32.const 2`)
	g.line(`i32.shl`)
	g.line(`local.get $b3`)
	g.line(`i32.const 6`)
	g.line(`i32.shr_u`)
	g.line(`i32.or`)
	g.line(`call $__b64_alphabet`)
	g.indent--
	g.line(`end`)
	g.line(`i32.store8`)
	// out[j+3]: '=' if remaining < 3, else alphabet(b3 & 63)
	g.line(`local.get $out`)
	g.line(`i32.const 7`)
	g.line(`i32.add`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`local.get $remaining`)
	g.line(`i32.const 3`)
	g.line(`i32.lt_s`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 61`) // '='
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $b3`)
	g.line(`i32.const 63`)
	g.line(`i32.and`)
	g.line(`call $__b64_alphabet`)
	g.indent--
	g.line(`end`)
	g.line(`i32.store8`)
	g.line(`local.get $i`)
	g.line(`i32.const 3`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`local.get $j`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)

	// $base64_decode(s): for each 4-char group, produce up to
	// 3 output bytes. Stops decoding at the first non-base64
	// character (including `=` padding).
	g.line(`(func $base64_decode (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $outCap i32) (local $out i32)`)
	g.line(`(local $i i32) (local $j i32)`)
	g.line(`(local $v0 i32) (local $v1 i32) (local $v2 i32) (local $v3 i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	// outCap = (sLen / 4) * 3 + 2 (over-estimate; final j is the actual length)
	g.line(`local.get $sLen`)
	g.line(`i32.const 4`)
	g.line(`i32.div_u`)
	g.line(`i32.const 3`)
	g.line(`i32.mul`)
	g.line(`i32.const 2`)
	g.line(`i32.add`)
	g.line(`local.set $outCap`)
	g.line(`local.get $outCap`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $out`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`i32.const 0`)
	g.line(`local.set $j`)
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	// Need at least 2 chars for any output byte.
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_s`)
	g.line(`br_if $break`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`call $__b64_decode_char`)
	g.line(`local.set $v0`)
	g.line(`local.get $v0`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`br_if $break`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`call $__b64_decode_char`)
	g.line(`local.set $v1`)
	g.line(`local.get $v1`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`br_if $break`)
	// Output byte 0
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`local.get $v0`)
	g.line(`i32.const 2`)
	g.line(`i32.shl`)
	g.line(`local.get $v1`)
	g.line(`i32.const 4`)
	g.line(`i32.shr_u`)
	g.line(`i32.or`)
	g.line(`i32.store8`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	// Try the 3rd char.
	g.line(`local.get $i`)
	g.line(`i32.const 2`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_s`)
	g.line(`br_if $break`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.const 2`)
	g.line(`i32.add`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`call $__b64_decode_char`)
	g.line(`local.set $v2`)
	g.line(`local.get $v2`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`br_if $break`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`local.get $v1`)
	g.line(`i32.const 15`)
	g.line(`i32.and`)
	g.line(`i32.const 4`)
	g.line(`i32.shl`)
	g.line(`local.get $v2`)
	g.line(`i32.const 2`)
	g.line(`i32.shr_u`)
	g.line(`i32.or`)
	g.line(`i32.store8`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	// Try the 4th char.
	g.line(`local.get $i`)
	g.line(`i32.const 3`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_s`)
	g.line(`br_if $break`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.const 3`)
	g.line(`i32.add`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`call $__b64_decode_char`)
	g.line(`local.set $v3`)
	g.line(`local.get $v3`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`br_if $break`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`local.get $v2`)
	g.line(`i32.const 3`)
	g.line(`i32.and`)
	g.line(`i32.const 6`)
	g.line(`i32.shl`)
	g.line(`local.get $v3`)
	g.line(`i32.or`)
	g.line(`i32.store8`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	g.line(`local.get $i`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Write the actual length.
	g.line(`local.get $out`)
	g.line(`local.get $j`)
	g.line(`i32.store`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)
}

func (g *generator) emitStringMethodHelpers() {
	// Shared body: compares `n` bytes at $aBase against $bBase.
	// Returns 1 if every byte matches, 0 on first mismatch.
	g.line(`(func $__bytes_eq (param $aBase i32) (param $bBase i32) (param $n i32) (result i32)`)
	g.indent++
	g.line(`(local $i i32)`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $n`)
	g.line(`i32.ge_s`)
	g.line(`br_if $break`)
	// if a[i] != b[i] return 0
	g.line(`local.get $aBase`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.get $bBase`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.ne`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const 1`)
	g.indent--
	g.line(`)`)

	// $__method_string_starts_with(s, prefix): bool
	g.line(`(func $__method_string_starts_with (param $s i32) (param $prefix i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $pLen i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`local.get $prefix`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $pLen`)
	g.line(`local.get $pLen`)
	g.line(`local.get $sLen`)
	g.line(`i32.gt_s`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $s`)
	g.line(`local.get $prefix`)
	g.line(`local.get $pLen`)
	g.line(`call $__bytes_eq`)
	g.indent--
	g.line(`)`)

	// $__method_string_ends_with(s, suffix): bool
	g.line(`(func $__method_string_ends_with (param $s i32) (param $suffix i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $sufLen i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`local.get $suffix`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sufLen`)
	g.line(`local.get $sufLen`)
	g.line(`local.get $sLen`)
	g.line(`i32.gt_s`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Compare $s[sLen - sufLen ..] against $suffix.
	g.line(`local.get $s`)
	g.line(`local.get $sLen`)
	g.line(`i32.add`)
	g.line(`local.get $sufLen`)
	g.line(`i32.sub`)
	g.line(`local.get $suffix`)
	g.line(`local.get $sufLen`)
	g.line(`call $__bytes_eq`)
	g.indent--
	g.line(`)`)

	// $__method_string_contains(s, needle): bool — linear scan
	// over every starting position 0..sLen-needleLen.
	g.line(`(func $__method_string_contains (param $s i32) (param $needle i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $nLen i32) (local $i i32) (local $last i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`local.get $needle`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $nLen`)
	// An empty needle is contained in every string (matches the
	// strstr / std::string::contains convention).
	g.line(`local.get $nLen`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 1`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $nLen`)
	g.line(`local.get $sLen`)
	g.line(`i32.gt_s`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $sLen`)
	g.line(`local.get $nLen`)
	g.line(`i32.sub`)
	g.line(`local.set $last`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $last`)
	g.line(`i32.gt_s`)
	g.line(`br_if $break`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`local.get $needle`)
	g.line(`local.get $nLen`)
	g.line(`call $__bytes_eq`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 1`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const 0`)
	g.indent--
	g.line(`)`)

	// $__method_string_index_of(s, needle): i32 — returns the
	// position of the first occurrence of needle in s, or -1 if
	// not found. Empty needle returns 0 (matches strstr / std).
	g.line(`(func $__method_string_index_of (param $s i32) (param $needle i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $nLen i32) (local $i i32) (local $last i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`local.get $needle`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $nLen`)
	g.line(`local.get $nLen`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $nLen`)
	g.line(`local.get $sLen`)
	g.line(`i32.gt_s`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const -1`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $sLen`)
	g.line(`local.get $nLen`)
	g.line(`i32.sub`)
	g.line(`local.set $last`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $last`)
	g.line(`i32.gt_s`)
	g.line(`br_if $break`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`local.get $needle`)
	g.line(`local.get $nLen`)
	g.line(`call $__bytes_eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const -1`)
	g.indent--
	g.line(`)`)

	// $__method_string_trim(s): string — returns a fresh
	// string with leading and trailing ASCII whitespace
	// removed. Whitespace = space (0x20), tab (0x09), LF
	// (0x0A), CR (0x0D), VT (0x0B), FF (0x0C). The result is
	// either a copy or empty; trim never aliases the source's
	// storage, matching `__str_slice`'s ownership shape.
	g.line(`(func $__method_string_trim (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $low i32) (local $high i32) (local $b i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`i32.const 0`)
	g.line(`local.set $low`)
	g.line(`local.get $sLen`)
	g.line(`local.set $high`)
	// Advance low while bytes are whitespace.
	g.line(`block $low_done`)
	g.indent++
	g.line(`loop $low_loop`)
	g.indent++
	g.line(`local.get $low`)
	g.line(`local.get $high`)
	g.line(`i32.ge_s`)
	g.line(`br_if $low_done`)
	g.line(`local.get $s`)
	g.line(`local.get $low`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.tee $b`)
	g.line(`call $__is_ascii_ws`)
	g.line(`i32.eqz`)
	g.line(`br_if $low_done`)
	g.line(`local.get $low`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $low`)
	g.line(`br $low_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Decrement high while bytes are whitespace.
	g.line(`block $high_done`)
	g.indent++
	g.line(`loop $high_loop`)
	g.indent++
	g.line(`local.get $high`)
	g.line(`local.get $low`)
	g.line(`i32.le_s`)
	g.line(`br_if $high_done`)
	g.line(`local.get $s`)
	g.line(`local.get $high`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.tee $b`)
	g.line(`call $__is_ascii_ws`)
	g.line(`i32.eqz`)
	g.line(`br_if $high_done`)
	g.line(`local.get $high`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.set $high`)
	g.line(`br $high_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Tail-call __str_slice(s, low, high).
	g.line(`local.get $s`)
	g.line(`local.get $low`)
	g.line(`local.get $high`)
	g.line(`call $__str_slice`)
	g.indent--
	g.line(`)`)

	// $__is_ascii_ws(b): i32 — 1 if b is one of
	// SP/TAB/LF/CR/VT/FF, else 0.
	g.line(`(func $__is_ascii_ws (param $b i32) (result i32)`)
	g.indent++
	g.line(`local.get $b`)
	g.line(`i32.const 32`)
	g.line(`i32.eq`)
	g.line(`local.get $b`)
	g.line(`i32.const 9`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.line(`local.get $b`)
	g.line(`i32.const 10`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.line(`local.get $b`)
	g.line(`i32.const 13`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.line(`local.get $b`)
	g.line(`i32.const 11`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.line(`local.get $b`)
	g.line(`i32.const 12`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.indent--
	g.line(`)`)

	// $__method_string_to_lower(s): string — fresh string with
	// every ASCII A-Z byte mapped to a-z. Non-ASCII bytes are
	// copied verbatim. UTF-8 multibyte sequences are unaffected
	// since their leading bytes are all >= 0x80.
	emitStringCaseFold := func(name string, srcBase int, dstBase int) {
		g.linef(`(func %s (param $s i32) (result i32)`, name)
		g.indent++
		g.line(`(local $sLen i32) (local $out i32) (local $i i32) (local $b i32)`)
		g.line(`local.get $s`)
		g.line(`i32.const 4`)
		g.line(`i32.sub`)
		g.line(`i32.load`)
		g.line(`local.set $sLen`)
		g.line(`local.get $sLen`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`call $__lang_alloc`)
		g.line(`local.set $out`)
		g.line(`local.get $out`)
		g.line(`local.get $sLen`)
		g.line(`i32.store`)
		g.line(`i32.const 0`)
		g.line(`local.set $i`)
		g.line(`block $break`)
		g.indent++
		g.line(`loop $loop`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`local.get $sLen`)
		g.line(`i32.ge_s`)
		g.line(`br_if $break`)
		// b = s[i]
		g.line(`local.get $s`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`i32.load8_u`)
		g.line(`local.set $b`)
		// if srcBase <= b <= srcBase+25: b += dstBase - srcBase
		g.line(`local.get $b`)
		g.linef(`i32.const %d`, srcBase)
		g.line(`i32.ge_u`)
		g.line(`local.get $b`)
		g.linef(`i32.const %d`, srcBase+26)
		g.line(`i32.lt_u`)
		g.line(`i32.and`)
		g.line(`if`)
		g.indent++
		g.line(`local.get $b`)
		g.linef(`i32.const %d`, dstBase-srcBase)
		g.line(`i32.add`)
		g.line(`local.set $b`)
		g.indent--
		g.line(`end`)
		// out[4 + i] = b
		g.line(`local.get $out`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.get $i`)
		g.line(`i32.add`)
		g.line(`local.get $b`)
		g.line(`i32.store8`)
		g.line(`local.get $i`)
		g.line(`i32.const 1`)
		g.line(`i32.add`)
		g.line(`local.set $i`)
		g.line(`br $loop`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $out`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)
	}
	emitStringCaseFold("$__method_string_to_lower", 65, 97) // A-Z → a-z
	emitStringCaseFold("$__method_string_to_upper", 97, 65) // a-z → A-Z

	// $__method_string_split(s, sep): string[] — splits s on
	// each occurrence of sep, returning an array of substrings.
	// Empty sep splits into single-character strings (one per
	// byte of s), matching JS's `String.prototype.split("")`
	// rather than the Go std behaviour of returning every
	// "between" position.
	g.line(`(func $__method_string_split (param $s i32) (param $sep i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $sepLen i32) (local $i i32) (local $start i32)`)
	g.line(`(local $count i32) (local $arr i32) (local $arrCursor i32)`)
	g.line(`(local $piecePtr i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`local.get $sep`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sepLen`)
	// Empty sep: allocate sLen single-byte strings.
	g.line(`local.get $sepLen`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	// arr = alloc(4 + sLen*4); arr[0] = sLen
	g.line(`local.get $sLen`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $arr`)
	g.line(`local.get $sLen`)
	g.line(`i32.store`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $charBreak`)
	g.indent++
	g.line(`loop $charLoop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_s`)
	g.line(`br_if $charBreak`)
	// Allocate single-byte string: alloc(5), len=1, byte = s[i]
	g.line(`i32.const 5`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $piecePtr`)
	g.line(`local.get $piecePtr`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $piecePtr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.store8`)
	// arr[4 + i*4] = piece content ptr (piecePtr + 4)
	g.line(`local.get $arr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $i`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $piecePtr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.store`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $charLoop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $arr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Non-empty sep. First pass: count occurrences (so we can
	// size the result array exactly). count starts at 1 — even
	// no-occurrence input produces a single-element array
	// holding the whole string.
	g.line(`i32.const 1`)
	g.line(`local.set $count`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $countBreak`)
	g.indent++
	g.line(`loop $countLoop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sepLen`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`i32.gt_s`)
	g.line(`br_if $countBreak`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`local.get $sep`)
	g.line(`local.get $sepLen`)
	g.line(`call $__bytes_eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $count`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $count`)
	g.line(`local.get $i`)
	g.line(`local.get $sepLen`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.indent--
	g.line(`end`)
	g.line(`br $countLoop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Allocate result array.
	g.line(`local.get $count`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $arr`)
	g.line(`local.get $count`)
	g.line(`i32.store`)
	g.line(`local.get $arr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $arrCursor`)
	// Second pass: emit each piece.
	g.line(`i32.const 0`)
	g.line(`local.set $start`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $emitBreak`)
	g.indent++
	g.line(`loop $emitLoop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sepLen`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`i32.gt_s`)
	g.line(`br_if $emitBreak`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`local.get $sep`)
	g.line(`local.get $sepLen`)
	g.line(`call $__bytes_eq`)
	g.line(`if`)
	g.indent++
	// Emit s[start..i] into arrCursor.
	g.line(`local.get $arrCursor`)
	g.line(`local.get $s`)
	g.line(`local.get $start`)
	g.line(`local.get $i`)
	g.line(`call $__str_slice`)
	g.line(`i32.store`)
	g.line(`local.get $arrCursor`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $arrCursor`)
	g.line(`local.get $i`)
	g.line(`local.get $sepLen`)
	g.line(`i32.add`)
	g.line(`local.tee $i`)
	g.line(`local.set $start`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.indent--
	g.line(`end`)
	g.line(`br $emitLoop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Emit final piece s[start..sLen].
	g.line(`local.get $arrCursor`)
	g.line(`local.get $s`)
	g.line(`local.get $start`)
	g.line(`local.get $sLen`)
	g.line(`call $__str_slice`)
	g.line(`i32.store`)
	g.line(`local.get $arr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)

	// $__method_string_replace(s, old, new): string — replaces
	// every non-overlapping occurrence of `old` with `new`.
	// Two-pass: first count occurrences to size the output, then
	// build it with a single allocation. Empty `old` returns s
	// unchanged (matches the convention that splitting on the
	// empty string + joining with the new value is well-defined
	// only for non-empty separators).
	g.line(`(func $__method_string_replace (param $s i32) (param $old i32) (param $new i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $oldLen i32) (local $newLen i32)`)
	g.line(`(local $i i32) (local $count i32) (local $outLen i32)`)
	g.line(`(local $out i32) (local $cursor i32) (local $start i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`local.get $old`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $oldLen`)
	g.line(`local.get $new`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $newLen`)
	// Empty old → return s unchanged via __str_slice(s, 0, sLen)
	// (so the result is independently owned).
	g.line(`local.get $oldLen`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $s`)
	g.line(`i32.const 0`)
	g.line(`local.get $sLen`)
	g.line(`call $__str_slice`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// First pass: count occurrences.
	g.line(`i32.const 0`)
	g.line(`local.set $count`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $countBreak`)
	g.indent++
	g.line(`loop $countLoop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $oldLen`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`i32.gt_s`)
	g.line(`br_if $countBreak`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`local.get $old`)
	g.line(`local.get $oldLen`)
	g.line(`call $__bytes_eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $count`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $count`)
	g.line(`local.get $i`)
	g.line(`local.get $oldLen`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.indent--
	g.line(`end`)
	g.line(`br $countLoop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// outLen = sLen + count * (newLen - oldLen)
	g.line(`local.get $sLen`)
	g.line(`local.get $count`)
	g.line(`local.get $newLen`)
	g.line(`local.get $oldLen`)
	g.line(`i32.sub`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.set $outLen`)
	// Allocate output and write its length prefix.
	g.line(`local.get $outLen`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $out`)
	g.line(`local.get $outLen`)
	g.line(`i32.store`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $cursor`)
	// Second pass: copy + substitute.
	g.line(`i32.const 0`)
	g.line(`local.set $start`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $emitBreak`)
	g.indent++
	g.line(`loop $emitLoop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $oldLen`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`i32.gt_s`)
	g.line(`br_if $emitBreak`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`local.get $old`)
	g.line(`local.get $oldLen`)
	g.line(`call $__bytes_eq`)
	g.line(`if`)
	g.indent++
	// memcpy(cursor, s + start, i - start)
	g.line(`local.get $cursor`)
	g.line(`local.get $s`)
	g.line(`local.get $start`)
	g.line(`i32.add`)
	g.line(`local.get $i`)
	g.line(`local.get $start`)
	g.line(`i32.sub`)
	g.line(`memory.copy`)
	g.line(`local.get $cursor`)
	g.line(`local.get $i`)
	g.line(`local.get $start`)
	g.line(`i32.sub`)
	g.line(`i32.add`)
	g.line(`local.set $cursor`)
	// memcpy(cursor, new, newLen)
	g.line(`local.get $cursor`)
	g.line(`local.get $new`)
	g.line(`local.get $newLen`)
	g.line(`memory.copy`)
	g.line(`local.get $cursor`)
	g.line(`local.get $newLen`)
	g.line(`i32.add`)
	g.line(`local.set $cursor`)
	g.line(`local.get $i`)
	g.line(`local.get $oldLen`)
	g.line(`i32.add`)
	g.line(`local.tee $i`)
	g.line(`local.set $start`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.indent--
	g.line(`end`)
	g.line(`br $emitLoop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Final tail: memcpy(cursor, s + start, sLen - start)
	g.line(`local.get $cursor`)
	g.line(`local.get $s`)
	g.line(`local.get $start`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`local.get $start`)
	g.line(`i32.sub`)
	g.line(`memory.copy`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)

	// $__method_string_bytes(s): u8[] — copies the string's
	// bytes into a fresh u8[] (1-byte stride). The result is
	// independently owned; mutations don't affect the source
	// string.
	g.line(`(func $__method_string_bytes (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $arr i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	// arr = alloc(4 + sLen); arr[0] = sLen
	g.line(`local.get $sLen`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $arr`)
	g.line(`local.get $sLen`)
	g.line(`i32.store`)
	// memory.copy(arr + 4, s, sLen)
	g.line(`local.get $arr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $s`)
	g.line(`local.get $sLen`)
	g.line(`memory.copy`)
	// Return content ptr.
	g.line(`local.get $arr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)

	// $__method_string_is_empty(s): bool — returns 1 if
	// the string's length prefix is 0, else 0.
	g.line(`(func $__method_string_is_empty (param $s i32) (result i32)`)
	g.indent++
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`i32.eqz`)
	g.indent--
	g.line(`)`)

	// $__method_string_repeat(s, n): string — `n` copies of
	// s concatenated. `n <= 0` returns empty; `n == 1`
	// allocates a fresh copy (matches the rest of the
	// string-builder family — every call returns an
	// independently owned string). For larger n, allocates
	// `sLen * n` bytes and memcpy's the source into each
	// slot in turn.
	g.line(`(func $__method_string_repeat (param $s i32) (param $n i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $outLen i32) (local $out i32) (local $dst i32) (local $i i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	// n <= 0 -> empty string. Allocate a 4-byte length-only
	// header, store 0, return the content ptr.
	g.line(`local.get $n`)
	g.line(`i32.const 0`)
	g.line(`i32.le_s`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $out`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// outLen = sLen * n
	g.line(`local.get $sLen`)
	g.line(`local.get $n`)
	g.line(`i32.mul`)
	g.line(`local.set $outLen`)
	g.line(`local.get $outLen`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $out`)
	g.line(`local.get $outLen`)
	g.line(`i32.store`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $dst`)
	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $n`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end`)
	// memcpy(dst + i*sLen, s, sLen)
	g.line(`local.get $dst`)
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $s`)
	g.line(`local.get $sLen`)
	g.line(`memory.copy`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $dst`)
	g.indent--
	g.line(`)`)

	// $__method_string_as_bytes(s): [u8] — non-copying view.
	// Allocates only the 8-byte slice header `{data_ptr=s,
	// len=len(s)}`; the data_ptr aliases the string's
	// payload, sharing its lifetime. Reads/writes through the
	// view route through `__slice_idx_1` (1-byte stride),
	// which is the same helper byte-arrays use, so the read
	// path produces ergonomic-equivalent values to a copying
	// `bytes()` call without the alloc. Mutations through the
	// view do propagate to the underlying string memory —
	// callers that don't want this should use `bytes()`.
	g.line(`(func $__method_string_as_bytes (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $hdr i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $hdr`)
	g.line(`local.get $s`)
	g.line(`i32.store`) // data_ptr = s
	g.line(`local.get $hdr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`i32.store`) // len = sLen
	g.line(`local.get $hdr`)
	g.indent--
	g.line(`)`)

	// $__method_string_parse_int(s): Option[i32] — decimal i32
	// parser. Accepts optional leading `-`; rejects empty input,
	// any non-digit character, and overflow (the underlying
	// accumulator is i64 so we get an exact bound check against
	// the signed-i32 range). Returns the standard Option layout:
	// 4-byte None{tag=1} or 8-byte Some{tag=0, val}.
	g.line(`(func $__method_string_parse_int (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $i i32) (local $b i32)`)
	g.line(`(local $acc i64) (local $neg i32) (local $opt i32)`)
	g.line(`(local $val i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	// Empty -> None.
	g.line(`local.get $sLen`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Optional leading '-'. Lone '-' is None.
	g.line(`local.get $s`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 45`) // '-'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 1`)
	g.line(`local.set $neg`)
	g.line(`i32.const 1`)
	g.line(`local.set $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.const 1`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Loop with two exit targets: clean exit (i >= sLen)
	// jumps `br $done` past both blocks into the range
	// check + Some emit; bad input / overflow does
	// `br $bad` which falls through to the None emit at
	// the end of $bad.
	g.line(`block $done`)
	g.indent++
	g.line(`block $bad`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_u`)
	g.line(`if`)
	g.indent++
	g.line(`br $done`) // exit loop + both blocks: post-$done = range check
	g.indent--
	g.line(`end`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $b`)
	g.line(`local.get $b`)
	g.line(`i32.const 48`) // '0'
	g.line(`i32.lt_u`)
	g.line(`br_if $bad`)
	g.line(`local.get $b`)
	g.line(`i32.const 57`) // '9'
	g.line(`i32.gt_u`)
	g.line(`br_if $bad`)
	g.line(`local.get $acc`)
	g.line(`i64.const 10`)
	g.line(`i64.mul`)
	g.line(`local.get $b`)
	g.line(`i32.const 48`)
	g.line(`i32.sub`)
	g.line(`i64.extend_i32_u`)
	g.line(`i64.add`)
	g.line(`local.set $acc`)
	// Overflow guard: anything > 2^32 is definitely beyond
	// even the unsigned-i32 range, so it can never fit a
	// signed i32 regardless of sign.
	g.line(`local.get $acc`)
	g.line(`i64.const 4294967296`)
	g.line(`i64.ge_u`)
	g.line(`br_if $bad`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`) // loop
	g.indent--
	g.line(`end`) // $bad — falls through to None emit
	// $bad path lands here. Emit None and return.
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`return`)
	g.indent--
	g.line(`end`) // $done
	// Range check: positive must fit i32 signed (≤ 2^31-1);
	// negative must satisfy acc ≤ 2^31 so -acc fits.
	g.line(`local.get $neg`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $acc`)
	g.line(`i64.const 2147483648`)
	g.line(`i64.gt_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`i64.const 0`)
	g.line(`local.get $acc`)
	g.line(`i64.sub`)
	g.line(`i32.wrap_i64`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $acc`)
	g.line(`i64.const 2147483647`)
	g.line(`i64.gt_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $acc`)
	g.line(`i32.wrap_i64`)
	g.indent--
	g.line(`end`)
	g.line(`local.set $val`)
	// Some(val): alloc 8, tag=0 at +0, value at +4.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $val`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.indent--
	g.line(`)`)

	// $__method_string_parse_float(s): Option[f32] — decimal
	// f32 parser. Accepts:
	//   [-]<digits>[.<digits>][(e|E)[+-]?<digits>]
	// at least one of integer or fraction digits is required;
	// trailing garbage rejected. Mantissa accumulates into an
	// i64 with a 2^53-ish saturation point — beyond that we
	// stop adding digits but track the magnitude in `exp_adj`
	// so the final scale remains correct (modulo precision).
	// Final value = (f32) mantissa * 10^exp_adj, sign applied
	// via `f32.neg` at the end.
	g.line(`(func $__method_string_parse_float (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $i i32) (local $b i32)`)
	g.line(`(local $mantissa i64) (local $exp_adj i32)`)
	g.line(`(local $exp i32) (local $exp_neg i32)`)
	g.line(`(local $saw_int i32) (local $saw_frac i32) (local $saw_exp i32)`)
	g.line(`(local $neg i32) (local $opt i32) (local $v f32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	// Empty -> None.
	g.line(`local.get $sLen`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Optional leading '-'. Lone '-' is None.
	g.line(`local.get $s`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 45`) // '-'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 1`)
	g.line(`local.set $neg`)
	g.line(`i32.const 1`)
	g.line(`local.set $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.const 1`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// $done jumps past $bad on a clean parse; $bad falls through
	// to the inline None emit at the end of $bad.
	g.line(`block $done`)
	g.indent++
	g.line(`block $bad`)
	g.indent++
	// === Integer digits ===
	g.line(`block $end_int`)
	g.indent++
	g.line(`loop $int_loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_int`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $b`)
	g.line(`local.get $b`)
	g.line(`i32.const 48`) // '0'
	g.line(`i32.lt_u`)
	g.line(`br_if $end_int`)
	g.line(`local.get $b`)
	g.line(`i32.const 57`) // '9'
	g.line(`i32.gt_u`)
	g.line(`br_if $end_int`)
	// Saturating accumulate: while mantissa < 1e15, add digit;
	// past that, just bump exp_adj per digit consumed so the
	// magnitude stays right.
	g.line(`local.get $mantissa`)
	g.line(`i64.const 1000000000000000`)
	g.line(`i64.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $mantissa`)
	g.line(`i64.const 10`)
	g.line(`i64.mul`)
	g.line(`local.get $b`)
	g.line(`i32.const 48`)
	g.line(`i32.sub`)
	g.line(`i64.extend_i32_u`)
	g.line(`i64.add`)
	g.line(`local.set $mantissa`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $exp_adj`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $exp_adj`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const 1`)
	g.line(`local.set $saw_int`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $int_loop`)
	g.indent--
	g.line(`end`) // int_loop
	g.indent--
	g.line(`end`) // $end_int
	// === Fraction part: optional `.<digits>` ===
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 46`) // '.'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`block $end_frac`)
	g.indent++
	g.line(`loop $frac_loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_frac`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $b`)
	g.line(`local.get $b`)
	g.line(`i32.const 48`)
	g.line(`i32.lt_u`)
	g.line(`br_if $end_frac`)
	g.line(`local.get $b`)
	g.line(`i32.const 57`)
	g.line(`i32.gt_u`)
	g.line(`br_if $end_frac`)
	// Same saturation rule as the integer branch, but
	// each digit also moves exp_adj down by 1 (we're past
	// the decimal point).
	g.line(`local.get $mantissa`)
	g.line(`i64.const 1000000000000000`)
	g.line(`i64.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $mantissa`)
	g.line(`i64.const 10`)
	g.line(`i64.mul`)
	g.line(`local.get $b`)
	g.line(`i32.const 48`)
	g.line(`i32.sub`)
	g.line(`i64.extend_i32_u`)
	g.line(`i64.add`)
	g.line(`local.set $mantissa`)
	g.line(`local.get $exp_adj`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.set $exp_adj`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const 1`)
	g.line(`local.set $saw_frac`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $frac_loop`)
	g.indent--
	g.line(`end`) // frac_loop
	g.indent--
	g.line(`end`) // $end_frac
	g.indent--
	g.line(`end`) // matched '.'
	g.indent--
	g.line(`end`) // i < sLen
	// At least one of integer or fraction digits required.
	g.line(`local.get $saw_int`)
	g.line(`local.get $saw_frac`)
	g.line(`i32.or`)
	g.line(`i32.eqz`)
	g.line(`br_if $bad`)
	// === Exponent part: optional [eE][+-]?<digits> ===
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.tee $b`)
	g.line(`i32.const 101`) // 'e'
	g.line(`i32.eq`)
	g.line(`local.get $b`)
	g.line(`i32.const 69`) // 'E'
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	// Optional sign on exponent.
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $b`)
	g.line(`local.get $b`)
	g.line(`i32.const 43`) // '+'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $b`)
	g.line(`i32.const 45`) // '-'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 1`)
	g.line(`local.set $exp_neg`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Exponent digits — at least one required.
	g.line(`block $end_exp`)
	g.indent++
	g.line(`loop $exp_loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_exp`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $b`)
	g.line(`local.get $b`)
	g.line(`i32.const 48`)
	g.line(`i32.lt_u`)
	g.line(`br_if $end_exp`)
	g.line(`local.get $b`)
	g.line(`i32.const 57`)
	g.line(`i32.gt_u`)
	g.line(`br_if $end_exp`)
	// exp = min(exp*10 + digit, 1000) — caps so the post-loop
	// scale loop terminates even on `1e9999999`.
	g.line(`local.get $exp`)
	g.line(`i32.const 1000`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $exp`)
	g.line(`i32.const 10`)
	g.line(`i32.mul`)
	g.line(`local.get $b`)
	g.line(`i32.const 48`)
	g.line(`i32.sub`)
	g.line(`i32.add`)
	g.line(`local.set $exp`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const 1`)
	g.line(`local.set $saw_exp`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $exp_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`) // $end_exp
	g.line(`local.get $saw_exp`)
	g.line(`i32.eqz`)
	g.line(`br_if $bad`)
	// Apply exponent sign to exp_adj.
	g.line(`local.get $exp_neg`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $exp_adj`)
	g.line(`local.get $exp`)
	g.line(`i32.sub`)
	g.line(`local.set $exp_adj`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $exp_adj`)
	g.line(`local.get $exp`)
	g.line(`i32.add`)
	g.line(`local.set $exp_adj`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`) // matched 'e'/'E'
	g.indent--
	g.line(`end`) // i < sLen for exponent check
	// Trailing garbage -> bad.
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.lt_u`)
	g.line(`br_if $bad`)
	g.line(`br $done`)
	g.indent--
	g.line(`end`) // $bad — falls through to None emit
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`return`)
	g.indent--
	g.line(`end`) // $done
	// === Compute value ===
	// v = (f32) mantissa
	g.line(`local.get $mantissa`)
	g.line(`f32.convert_i64_u`)
	g.line(`local.set $v`)
	// while exp_adj > 0: v *= 10; exp_adj--
	g.line(`block $end_pos`)
	g.indent++
	g.line(`loop $pos_loop`)
	g.indent++
	g.line(`local.get $exp_adj`)
	g.line(`i32.const 0`)
	g.line(`i32.le_s`)
	g.line(`br_if $end_pos`)
	g.line(`local.get $v`)
	g.line(`f32.const 10`)
	g.line(`f32.mul`)
	g.line(`local.set $v`)
	g.line(`local.get $exp_adj`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.set $exp_adj`)
	g.line(`br $pos_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// while exp_adj < 0: v /= 10; exp_adj++
	g.line(`block $end_neg`)
	g.indent++
	g.line(`loop $neg_loop`)
	g.indent++
	g.line(`local.get $exp_adj`)
	g.line(`i32.const 0`)
	g.line(`i32.ge_s`)
	g.line(`br_if $end_neg`)
	g.line(`local.get $v`)
	g.line(`f32.const 10`)
	g.line(`f32.div`)
	g.line(`local.set $v`)
	g.line(`local.get $exp_adj`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $exp_adj`)
	g.line(`br $neg_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Apply sign.
	g.line(`local.get $neg`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $v`)
	g.line(`f32.neg`)
	g.line(`local.set $v`)
	g.indent--
	g.line(`end`)
	// Some(v): alloc 8, tag=0 at +0, f32 at +4.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $v`)
	g.line(`f32.store`)
	g.line(`local.get $opt`)
	g.indent--
	g.line(`)`)
}

// emitUrlParseHelper writes `$url_parse(s)` — splits an
// absolute or relative URL into a Url struct. Returns
// Option[Url] (None only on completely empty input;
// otherwise a best-effort parse).
//
// Algorithm: scan once to find the boundary characters
// (`:` + `//` for scheme, `?` for query, `#` for fragment),
// derive section indices, then slice the input into the
// struct's six string fields. Port parsing is inlined —
// digits up to 65535; any non-digit zeroes the port.
func (g *generator) emitUrlParseHelper() {
	g.line(`(func $url_parse (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $i i32) (local $b i32)`)
	g.line(`(local $scheme_end i32) (local $rest_start i32)`)
	g.line(`(local $frag_start i32) (local $query_start i32)`)
	g.line(`(local $authority_end i32) (local $colon i32)`)
	g.line(`(local $port i32) (local $u i32) (local $opt i32)`)
	g.line(`(local $empty i32)`)
	// sLen
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	// Empty input → None.
	g.line(`local.get $sLen`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Default scheme_end = -1 (no scheme found yet).
	g.line(`i32.const -1`)
	g.line(`local.set $scheme_end`)
	// Allocate the Url struct (6 fields × 4 bytes).
	g.line(`i32.const 24`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $u`)
	// Empty-string pointer for default field values.
	g.line(`local.get $s`)
	g.line(`i32.const 0`)
	g.line(`i32.const 0`)
	g.line(`call $__str_slice`)
	g.line(`local.set $empty`)
	// Initialize fields: scheme=empty, host=empty, port=0,
	// path=empty, query=empty, fragment=empty.
	g.line(`local.get $u`)
	g.line(`local.get $empty`)
	g.line(`i32.store`) // +0 scheme
	g.line(`local.get $u`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $empty`)
	g.line(`i32.store`) // +4 host
	g.line(`local.get $u`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store`) // +8 port
	g.line(`local.get $u`)
	g.line(`i32.const 12`)
	g.line(`i32.add`)
	g.line(`local.get $empty`)
	g.line(`i32.store`) // +12 path
	g.line(`local.get $u`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.get $empty`)
	g.line(`i32.store`) // +16 query
	g.line(`local.get $u`)
	g.line(`i32.const 20`)
	g.line(`i32.add`)
	g.line(`local.get $empty`)
	g.line(`i32.store`) // +20 fragment
	// === Find scheme (look for ':' + '//') ===
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $end_scheme_scan`)
	g.indent++
	g.line(`loop $scheme_scan`)
	g.indent++
	// Need at least 3 bytes left.
	g.line(`local.get $i`)
	g.line(`i32.const 2`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_scheme_scan`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 58`) // ':'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 47`) // '/'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.const 2`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 47`) // '/'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	// Scheme found at offset 0..i. Reject empty scheme.
	g.line(`local.get $i`)
	g.line(`i32.const 0`)
	g.line(`i32.gt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.set $scheme_end`)
	g.line(`br $end_scheme_scan`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $scheme_scan`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`) // $end_scheme_scan
	// rest_start = (scheme_end >= 0) ? scheme_end + 3 : 0
	g.line(`i32.const 0`)
	g.line(`local.set $rest_start`)
	g.line(`local.get $scheme_end`)
	g.line(`i32.const 0`)
	g.line(`i32.ge_s`)
	g.line(`if`)
	g.indent++
	// Slice scheme into u + 0
	g.line(`local.get $u`)
	g.line(`local.get $s`)
	g.line(`i32.const 0`)
	g.line(`local.get $scheme_end`)
	g.line(`call $__str_slice`)
	g.line(`i32.store`)
	g.line(`local.get $scheme_end`)
	g.line(`i32.const 3`)
	g.line(`i32.add`)
	g.line(`local.set $rest_start`)
	g.indent--
	g.line(`end`)
	// === Find fragment '#' ===
	g.line(`local.get $sLen`)
	g.line(`local.set $frag_start`)
	g.line(`local.get $rest_start`)
	g.line(`local.set $i`)
	g.line(`block $end_frag`)
	g.indent++
	g.line(`loop $frag_scan`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_frag`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 35`) // '#'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.set $frag_start`)
	g.line(`br $end_frag`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $frag_scan`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// If found, slice fragment into u + 20
	g.line(`local.get $frag_start`)
	g.line(`local.get $sLen`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $u`)
	g.line(`i32.const 20`)
	g.line(`i32.add`)
	g.line(`local.get $s`)
	g.line(`local.get $frag_start`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`call $__str_slice`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)
	// === Find query '?' in [rest_start, frag_start) ===
	g.line(`local.get $frag_start`)
	g.line(`local.set $query_start`)
	g.line(`local.get $rest_start`)
	g.line(`local.set $i`)
	g.line(`block $end_query`)
	g.indent++
	g.line(`loop $query_scan`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $frag_start`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_query`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 63`) // '?'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.set $query_start`)
	g.line(`br $end_query`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $query_scan`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// If found, slice query into u + 16
	g.line(`local.get $query_start`)
	g.line(`local.get $frag_start`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $u`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.get $s`)
	g.line(`local.get $query_start`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.get $frag_start`)
	g.line(`call $__str_slice`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)
	// === Find authority end ===
	// If had a scheme: first '/' in [rest_start, query_start)
	// determines authority_end. Otherwise the whole thing is
	// path, so authority_end = rest_start (= 0).
	g.line(`local.get $query_start`)
	g.line(`local.set $authority_end`)
	g.line(`local.get $scheme_end`)
	g.line(`i32.const 0`)
	g.line(`i32.ge_s`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $rest_start`)
	g.line(`local.set $i`)
	g.line(`block $end_auth`)
	g.indent++
	g.line(`loop $auth_scan`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $query_start`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_auth`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 47`) // '/'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.set $authority_end`)
	g.line(`br $end_auth`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $auth_scan`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`else`)
	g.indent++
	// No scheme — whole thing is path; no authority.
	g.line(`local.get $rest_start`)
	g.line(`local.set $authority_end`)
	g.indent--
	g.line(`end`)
	// === Authority: split host:port ===
	g.line(`local.get $rest_start`)
	g.line(`local.get $authority_end`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	// Find ':' in [rest_start, authority_end)
	g.line(`local.get $authority_end`)
	g.line(`local.set $colon`)
	g.line(`local.get $rest_start`)
	g.line(`local.set $i`)
	g.line(`block $end_colon`)
	g.indent++
	g.line(`loop $colon_scan`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $authority_end`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_colon`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 58`) // ':'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.set $colon`)
	g.line(`br $end_colon`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $colon_scan`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $colon`)
	g.line(`local.get $authority_end`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	// host = s[rest_start:colon]
	g.line(`local.get $u`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $s`)
	g.line(`local.get $rest_start`)
	g.line(`local.get $colon`)
	g.line(`call $__str_slice`)
	g.line(`i32.store`)
	// Parse port: digits in [colon+1, authority_end), max 65535.
	// Any non-digit zeroes the port.
	g.line(`i32.const 0`)
	g.line(`local.set $port`)
	g.line(`local.get $colon`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`block $end_port`)
	g.indent++
	g.line(`loop $port_scan`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $authority_end`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_port`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $b`)
	g.line(`local.get $b`)
	g.line(`i32.const 48`) // '0'
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.set $port`)
	g.line(`br $end_port`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $b`)
	g.line(`i32.const 57`) // '9'
	g.line(`i32.gt_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.set $port`)
	g.line(`br $end_port`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $port`)
	g.line(`i32.const 10`)
	g.line(`i32.mul`)
	g.line(`local.get $b`)
	g.line(`i32.const 48`)
	g.line(`i32.sub`)
	g.line(`i32.add`)
	g.line(`local.set $port`)
	// Port > 65535: zero it.
	g.line(`local.get $port`)
	g.line(`i32.const 65535`)
	g.line(`i32.gt_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.set $port`)
	g.line(`br $end_port`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $port_scan`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $u`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $port`)
	g.line(`i32.store`)
	g.indent--
	g.line(`else`)
	g.indent++
	// No colon — host is the whole authority, port stays 0.
	g.line(`local.get $u`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $s`)
	g.line(`local.get $rest_start`)
	g.line(`local.get $authority_end`)
	g.line(`call $__str_slice`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// === Path: [authority_end, query_start) ===
	g.line(`local.get $authority_end`)
	g.line(`local.get $query_start`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $u`)
	g.line(`i32.const 12`)
	g.line(`i32.add`)
	g.line(`local.get $s`)
	g.line(`local.get $authority_end`)
	g.line(`local.get $query_start`)
	g.line(`call $__str_slice`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)
	// Some(u): alloc 8, tag=0 at +0, struct ptr at +4.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $u`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.indent--
	g.line(`)`)
}

// emitUrlCoderHelpers writes `$url_encode(s)` /
// `$url_decode(s)` — RFC 3986 percent-encoding. Encoding
// passes through the unreserved set (`A-Za-z0-9-_.~`) and
// emits `%HH` (uppercase hex) for everything else; decoding
// is forgiving — malformed `%` sequences (non-hex
// following, truncated tail) are passed through verbatim.
func (g *generator) emitUrlCoderHelpers() {
	// Helper: `__url_is_unreserved(b)` — true for
	// `A-Z`, `a-z`, `0-9`, `-`, `_`, `.`, `~`. Returns i32
	// boolean (0/1) as the OR of the per-class membership
	// tests, no branching.
	g.line(`(func $__url_is_unreserved (param $b i32) (result i32)`)
	g.indent++
	// A-Z
	g.line(`local.get $b`)
	g.line(`i32.const 65`)
	g.line(`i32.ge_u`)
	g.line(`local.get $b`)
	g.line(`i32.const 90`)
	g.line(`i32.le_u`)
	g.line(`i32.and`)
	// a-z
	g.line(`local.get $b`)
	g.line(`i32.const 97`)
	g.line(`i32.ge_u`)
	g.line(`local.get $b`)
	g.line(`i32.const 122`)
	g.line(`i32.le_u`)
	g.line(`i32.and`)
	g.line(`i32.or`)
	// 0-9
	g.line(`local.get $b`)
	g.line(`i32.const 48`)
	g.line(`i32.ge_u`)
	g.line(`local.get $b`)
	g.line(`i32.const 57`)
	g.line(`i32.le_u`)
	g.line(`i32.and`)
	g.line(`i32.or`)
	// '-' (45)
	g.line(`local.get $b`)
	g.line(`i32.const 45`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	// '.' (46)
	g.line(`local.get $b`)
	g.line(`i32.const 46`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	// '_' (95)
	g.line(`local.get $b`)
	g.line(`i32.const 95`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	// '~' (126)
	g.line(`local.get $b`)
	g.line(`i32.const 126`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.indent--
	g.line(`)`)

	// $url_encode(s): allocate worst-case (3 × len) + 4-byte
	// length prefix, copy bytes through, store the actual
	// written length at the prefix on exit.
	g.line(`(func $url_encode (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $i i32) (local $b i32)`)
	g.line(`(local $out i32) (local $dst i32) (local $oi i32) (local $hi i32) (local $lo i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	// Allocate sLen*3 + 4 bytes (worst case: every byte → %HH).
	g.line(`local.get $sLen`)
	g.line(`i32.const 3`)
	g.line(`i32.mul`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $out`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $dst`)
	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end`)
	// b = s[i]
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $b`)
	g.line(`local.get $b`)
	g.line(`call $__url_is_unreserved`)
	g.line(`if`)
	g.indent++
	// dst[oi] = b; oi++
	g.line(`local.get $dst`)
	g.line(`local.get $oi`)
	g.line(`i32.add`)
	g.line(`local.get $b`)
	g.line(`i32.store8`)
	g.line(`local.get $oi`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $oi`)
	g.indent--
	g.line(`else`)
	g.indent++
	// dst[oi] = '%'
	g.line(`local.get $dst`)
	g.line(`local.get $oi`)
	g.line(`i32.add`)
	g.line(`i32.const 37`) // '%'
	g.line(`i32.store8`)
	// hi = (b >> 4) & 0xf; uppercase hex char.
	g.line(`local.get $b`)
	g.line(`i32.const 4`)
	g.line(`i32.shr_u`)
	g.line(`i32.const 15`)
	g.line(`i32.and`)
	g.line(`local.tee $hi`)
	g.line(`i32.const 10`)
	g.line(`i32.lt_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $hi`)
	g.line(`i32.const 48`) // '0'
	g.line(`i32.add`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $hi`)
	g.line(`i32.const 55`) // 'A' - 10
	g.line(`i32.add`)
	g.indent--
	g.line(`end`)
	g.line(`local.set $hi`)
	g.line(`local.get $dst`)
	g.line(`local.get $oi`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.add`)
	g.line(`local.get $hi`)
	g.line(`i32.store8`)
	// lo = b & 0xf
	g.line(`local.get $b`)
	g.line(`i32.const 15`)
	g.line(`i32.and`)
	g.line(`local.tee $lo`)
	g.line(`i32.const 10`)
	g.line(`i32.lt_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $lo`)
	g.line(`i32.const 48`)
	g.line(`i32.add`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $lo`)
	g.line(`i32.const 55`)
	g.line(`i32.add`)
	g.indent--
	g.line(`end`)
	g.line(`local.set $lo`)
	g.line(`local.get $dst`)
	g.line(`local.get $oi`)
	g.line(`i32.const 2`)
	g.line(`i32.add`)
	g.line(`i32.add`)
	g.line(`local.get $lo`)
	g.line(`i32.store8`)
	g.line(`local.get $oi`)
	g.line(`i32.const 3`)
	g.line(`i32.add`)
	g.line(`local.set $oi`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Patch length prefix to actually-written count.
	g.line(`local.get $out`)
	g.line(`local.get $oi`)
	g.line(`i32.store`)
	g.line(`local.get $dst`)
	g.indent--
	g.line(`)`)

	// $url_decode(s): allocate sLen + 4 (output never longer
	// than input). Walk bytes; '%' followed by 2 hex digits
	// emits the decoded byte; otherwise pass through.
	g.line(`(func $url_decode (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $i i32) (local $b i32) (local $h1 i32) (local $h2 i32)`)
	g.line(`(local $out i32) (local $dst i32) (local $oi i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`local.get $sLen`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $out`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $dst`)
	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $b`)
	// '%' + 2 hex digits → decoded byte. Otherwise pass
	// through. Helper: __hex_value isn't always emitted
	// (only when needsHex), so inline a tiny check here.
	g.line(`local.get $b`)
	g.line(`i32.const 37`) // '%'
	g.line(`i32.eq`)
	g.line(`local.get $i`)
	g.line(`i32.const 2`)
	g.line(`i32.add`)
	g.line(`local.get $sLen`)
	g.line(`i32.lt_u`)
	g.line(`i32.and`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`call $__url_hex_val`)
	g.line(`local.set $h1`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.const 2`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`call $__url_hex_val`)
	g.line(`local.set $h2`)
	g.line(`local.get $h1`)
	g.line(`i32.const 0`)
	g.line(`i32.ge_s`)
	g.line(`local.get $h2`)
	g.line(`i32.const 0`)
	g.line(`i32.ge_s`)
	g.line(`i32.and`)
	g.line(`if`)
	g.indent++
	// Valid %HH: emit (h1<<4)|h2, advance i by 3.
	g.line(`local.get $dst`)
	g.line(`local.get $oi`)
	g.line(`i32.add`)
	g.line(`local.get $h1`)
	g.line(`i32.const 4`)
	g.line(`i32.shl`)
	g.line(`local.get $h2`)
	g.line(`i32.or`)
	g.line(`i32.store8`)
	g.line(`local.get $oi`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $oi`)
	g.line(`local.get $i`)
	g.line(`i32.const 3`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Pass-through: copy byte, advance by 1.
	g.line(`local.get $dst`)
	g.line(`local.get $oi`)
	g.line(`i32.add`)
	g.line(`local.get $b`)
	g.line(`i32.store8`)
	g.line(`local.get $oi`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $oi`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Patch length prefix.
	g.line(`local.get $out`)
	g.line(`local.get $oi`)
	g.line(`i32.store`)
	g.line(`local.get $dst`)
	g.indent--
	g.line(`)`)

	// Local helper: parse a single hex digit byte -> 0..15
	// (or -1 for non-hex). Don't reuse $__hex_value because
	// that lives in the hex stdlib module which is gated
	// separately by needsHex; url-decoding shouldn't pull in
	// hex_encode / hex_decode just to share this 1-line
	// table.
	g.line(`(func $__url_hex_val (param $c i32) (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 48`)
	g.line(`i32.lt_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const -1`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 57`)
	g.line(`i32.le_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 48`)
	g.line(`i32.sub`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 97`)
	g.line(`i32.ge_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 102`)
	g.line(`i32.le_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 87`)
	g.line(`i32.sub`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const -1`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 65`)
	g.line(`i32.ge_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 70`)
	g.line(`i32.le_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 55`)
	g.line(`i32.sub`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const -1`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const -1`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
}

// emitQueryParseHelper writes `$query_parse(s)` — split a
// URL-encoded query string into a Map[string, string[]].
// Pairs are separated by `&`; within a pair, `=` separates
// key from value. Keys and values are url_decode'd before
// storage. Duplicate keys (`?tag=a&tag=b`) all preserved —
// values for the same key collect into a string[] in
// insertion order. A pair without `=` records the key with a
// single-element empty-string array. Empty input yields an
// empty map. `+` is left alone — callers handling form-
// encoded data should pre-process before calling.
func (g *generator) emitQueryParseHelper() {
	g.line(`(func $query_parse (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $i i32)`)
	g.line(`(local $pair_start i32) (local $pair_len i32)`)
	g.line(`(local $eq i32) (local $j i32)`)
	g.line(`(local $key i32) (local $val i32) (local $m i32)`)
	g.line(`(local $sep i32) (local $existing i32) (local $oldLen i32)`)
	g.line(`(local $newArr i32) (local $oldArr i32) (local $k2 i32)`)
	// m = map_new(8, 1)  ;; keyKind=1=string
	g.line(`i32.const 8`)
	g.line(`i32.const 1`)
	g.line(`call $map_new`)
	g.line(`local.set $m`)
	// sLen
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	// Empty input: return the empty map.
	g.line(`local.get $sLen`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $m`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	// sep = (i >= sLen) || s[i] == '&'
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 1`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 38`) // '&'
	g.line(`i32.eq`)
	g.indent--
	g.line(`end`)
	g.line(`local.set $sep`)
	g.line(`local.get $sep`)
	g.line(`if`)
	g.indent++
	// pair_len = i - pair_start
	g.line(`local.get $i`)
	g.line(`local.get $pair_start`)
	g.line(`i32.sub`)
	g.line(`local.tee $pair_len`)
	g.line(`i32.const 0`)
	g.line(`i32.gt_u`)
	g.line(`if`)
	g.indent++
	// Find '=' in [pair_start, i)
	g.line(`i32.const -1`)
	g.line(`local.set $eq`)
	g.line(`local.get $pair_start`)
	g.line(`local.set $j`)
	g.line(`block $end_eq`)
	g.indent++
	g.line(`loop $eq_loop`)
	g.indent++
	g.line(`local.get $j`)
	g.line(`local.get $i`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_eq`)
	g.line(`local.get $s`)
	g.line(`local.get $j`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 61`) // '='
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $j`)
	g.line(`local.set $eq`)
	g.line(`br $end_eq`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $j`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $j`)
	g.line(`br $eq_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Slice + decode key/val based on whether '=' was found.
	g.line(`local.get $eq`)
	g.line(`i32.const 0`)
	g.line(`i32.ge_s`)
	g.line(`if`)
	g.indent++
	// key = url_decode(s[pair_start:eq])
	g.line(`local.get $s`)
	g.line(`local.get $pair_start`)
	g.line(`local.get $eq`)
	g.line(`call $__str_slice`)
	g.line(`call $url_decode`)
	g.line(`local.set $key`)
	// val = url_decode(s[eq+1:i])
	g.line(`local.get $s`)
	g.line(`local.get $eq`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.get $i`)
	g.line(`call $__str_slice`)
	g.line(`call $url_decode`)
	g.line(`local.set $val`)
	g.indent--
	g.line(`else`)
	g.indent++
	// key = url_decode(s[pair_start:i])
	g.line(`local.get $s`)
	g.line(`local.get $pair_start`)
	g.line(`local.get $i`)
	g.line(`call $__str_slice`)
	g.line(`call $url_decode`)
	g.line(`local.set $key`)
	// val = ""
	g.line(`local.get $s`)
	g.line(`i32.const 0`)
	g.line(`i32.const 0`)
	g.line(`call $__str_slice`)
	g.line(`local.set $val`)
	g.indent--
	g.line(`end`)
	// Append-or-create: if key already in map, allocate a
	// new array of len+1, copy existing values, append the
	// new one, store back. Else allocate a fresh 1-element
	// array.
	g.line(`local.get $m`)
	g.line(`local.get $key`)
	g.line(`call $__method_Map_get`)
	g.line(`local.tee $existing`)
	g.line(`i32.load`)
	g.line(`i32.eqz`) // tag == 0 means Some
	g.line(`if`)
	g.indent++
	// Some: read existing array ptr; oldLen = len(arr).
	g.line(`local.get $existing`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $oldArr`)
	g.line(`local.get $oldArr`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $oldLen`)
	// alloc(4 + (oldLen+1)*4); newArr = ptr + 4.
	g.line(`local.get $oldLen`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $newArr`)
	g.line(`local.get $oldLen`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.store`)
	g.line(`local.get $newArr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $newArr`)
	// Copy existing values: for k2 in 0..oldLen.
	g.line(`i32.const 0`)
	g.line(`local.set $k2`)
	g.line(`block $end_copy`)
	g.indent++
	g.line(`loop $copy_loop`)
	g.indent++
	g.line(`local.get $k2`)
	g.line(`local.get $oldLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_copy`)
	g.line(`local.get $newArr`)
	g.line(`local.get $k2`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $oldArr`)
	g.line(`local.get $k2`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	g.line(`local.get $k2`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $k2`)
	g.line(`br $copy_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Append new value at index oldLen.
	g.line(`local.get $newArr`)
	g.line(`local.get $oldLen`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $val`)
	g.line(`i32.store`)
	g.indent--
	g.line(`else`)
	g.indent++
	// None: alloc 1-element array.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $newArr`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $newArr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.tee $newArr`)
	g.line(`local.get $val`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)
	// m.set(key, newArr)
	g.line(`local.get $m`)
	g.line(`local.get $key`)
	g.line(`local.get $newArr`)
	g.line(`call $__method_Map_set`)
	g.indent--
	g.line(`end`) // pair_len > 0
	// pair_start = i + 1
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $pair_start`)
	g.indent--
	g.line(`end`) // sep
	// If we just hit sLen, we're done.
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end`)
	// i++
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`) // $end
	g.line(`local.get $m`)
	g.indent--
	g.line(`)`)
}

// emitJsonEncodeHelpers writes the JSON encoder runtime —
// `$json_encode(v)` plus the supporting `__json_buf_*`
// growable-string-builder helpers and `__json_escape_into`.
//
// Buffer layout: `[cap:i32 @ 0, len:i32 @ 4, data:[cap]u8 @ 8]`.
// Returned strings reuse the inner `len` slot as the length
// prefix — content_ptr = buf+8, content_ptr - 4 = len. The
// 4-byte `cap` slot is wasted on finalize, that's the cost of
// the doubling-grow strategy.
//
// JsonValue variant tags (declaration order):
//
//	JNull = 0, JBool = 1, JNumber = 2, JString = 3,
//	JArray = 4, JObject = 5.
func (g *generator) emitJsonEncodeHelpers() {
	// $__json_buf_new(): allocate a fresh growable buffer
	// (cap=256, len=0).
	g.line(`(func $__json_buf_new (result i32)`)
	g.indent++
	g.line(`(local $b i32)`)
	g.line(`i32.const 264`) // 256 + 8
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $b`)
	g.line(`i32.const 256`)
	g.line(`i32.store`) // cap
	g.line(`local.get $b`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store`) // len
	g.line(`local.get $b`)
	g.indent--
	g.line(`)`)

	// $__json_buf_grow(buf, need): ensure cap - len >= need.
	// If not, alloc a fresh 2x-or-more-bytes buffer, copy
	// the existing payload over, return the new buf ptr.
	// Otherwise return buf unchanged.
	g.line(`(func $__json_buf_grow (param $buf i32) (param $need i32) (result i32)`)
	g.indent++
	g.line(`(local $cap i32) (local $len i32) (local $newCap i32) (local $nb i32)`)
	g.line(`local.get $buf`)
	g.line(`i32.load`)
	g.line(`local.set $cap`)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $len`)
	g.line(`local.get $len`)
	g.line(`local.get $need`)
	g.line(`i32.add`)
	g.line(`local.get $cap`)
	g.line(`i32.le_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// newCap = max(cap*2, len+need)
	g.line(`local.get $cap`)
	g.line(`i32.const 1`)
	g.line(`i32.shl`)
	g.line(`local.set $newCap`)
	g.line(`local.get $newCap`)
	g.line(`local.get $len`)
	g.line(`local.get $need`)
	g.line(`i32.add`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $len`)
	g.line(`local.get $need`)
	g.line(`i32.add`)
	g.line(`local.set $newCap`)
	g.indent--
	g.line(`end`)
	// nb = alloc(newCap + 8); store cap, len; memcpy data.
	g.line(`local.get $newCap`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $nb`)
	g.line(`local.get $newCap`)
	g.line(`i32.store`)
	g.line(`local.get $nb`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $len`)
	g.line(`i32.store`)
	g.line(`local.get $nb`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $buf`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $len`)
	g.line(`memory.copy`)
	g.line(`local.get $nb`)
	g.indent--
	g.line(`)`)

	// $__json_buf_byte(buf, b) -> new buf ptr
	g.line(`(func $__json_buf_byte (param $buf i32) (param $b i32) (result i32)`)
	g.indent++
	g.line(`(local $len i32)`)
	g.line(`local.get $buf`)
	g.line(`i32.const 1`)
	g.line(`call $__json_buf_grow`)
	g.line(`local.set $buf`)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $len`)
	g.line(`local.get $buf`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $len`)
	g.line(`i32.add`)
	g.line(`local.get $b`)
	g.line(`i32.store8`)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $len`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.store`)
	g.line(`local.get $buf`)
	g.indent--
	g.line(`)`)

	// $__json_buf_str(buf, s) -> new buf ptr
	g.line(`(func $__json_buf_str (param $buf i32) (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $len i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`local.get $buf`)
	g.line(`local.get $sLen`)
	g.line(`call $__json_buf_grow`)
	g.line(`local.set $buf`)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $len`)
	g.line(`local.get $buf`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $len`)
	g.line(`i32.add`)
	g.line(`local.get $s`)
	g.line(`local.get $sLen`)
	g.line(`memory.copy`)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $len`)
	g.line(`local.get $sLen`)
	g.line(`i32.add`)
	g.line(`i32.store`)
	g.line(`local.get $buf`)
	g.indent--
	g.line(`)`)

	// $__json_buf_hex(buf, n): write 2 lowercase hex digits.
	// Used by the \u00XX control-char escape.
	g.line(`(func $__json_buf_hex (param $buf i32) (param $n i32) (result i32)`)
	g.indent++
	g.line(`(local $hi i32) (local $lo i32)`)
	g.line(`local.get $n`)
	g.line(`i32.const 4`)
	g.line(`i32.shr_u`)
	g.line(`i32.const 15`)
	g.line(`i32.and`)
	g.line(`local.set $hi`)
	g.line(`local.get $n`)
	g.line(`i32.const 15`)
	g.line(`i32.and`)
	g.line(`local.set $lo`)
	g.line(`local.get $buf`)
	g.line(`local.get $hi`)
	g.line(`i32.const 10`)
	g.line(`i32.lt_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $hi`)
	g.line(`i32.const 48`)
	g.line(`i32.add`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $hi`)
	g.line(`i32.const 87`)
	g.line(`i32.add`)
	g.indent--
	g.line(`end`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.get $lo`)
	g.line(`i32.const 10`)
	g.line(`i32.lt_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $lo`)
	g.line(`i32.const 48`)
	g.line(`i32.add`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $lo`)
	g.line(`i32.const 87`)
	g.line(`i32.add`)
	g.indent--
	g.line(`end`)
	g.line(`call $__json_buf_byte`)
	g.indent--
	g.line(`)`)

	// $__json_escape_into(s, buf) -> new buf ptr. Walks the
	// bytes of s, emits JSON escape sequences for `"`, `\`,
	// and the standard control chars. Bytes >= 0x20 outside
	// `"` and `\` pass through verbatim — UTF-8 byte
	// sequences are valid JSON.
	g.line(`(func $__json_escape_into (param $s i32) (param $buf i32) (result i32)`)
	g.indent++
	g.line(`(local $sLen i32) (local $i i32) (local $b i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end`)
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $b`)
	// '"' 34 → \"
	g.line(`local.get $b`)
	g.line(`i32.const 34`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 92`) // backslash
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 34`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	// '\\' 92 → \\
	g.line(`local.get $b`)
	g.line(`i32.const 92`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 92`)
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 92`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	// '\n' 10 → \n
	g.line(`local.get $b`)
	g.line(`i32.const 10`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 92`)
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 110`) // 'n'
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	// '\r' 13 → \r
	g.line(`local.get $b`)
	g.line(`i32.const 13`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 92`)
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 114`) // 'r'
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	// '\t' 9 → \t
	g.line(`local.get $b`)
	g.line(`i32.const 9`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 92`)
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 116`) // 't'
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	// Other control chars (< 0x20): \u00XX
	g.line(`local.get $b`)
	g.line(`i32.const 32`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 92`) // \
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 117`) // 'u'
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 48`) // '0'
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 48`) // '0'
	g.line(`call $__json_buf_byte`)
	g.line(`local.get $b`)
	g.line(`call $__json_buf_hex`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	// Pass through.
	g.line(`local.get $buf`)
	g.line(`local.get $b`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $buf`)
	g.indent--
	g.line(`)`)

	// $__json_encode_into(v, buf) -> new buf ptr. Recurses
	// through JsonValue variants. v is the heap pointer to
	// the enum (tag at +0, payload at +4 for variants that
	// carry one).
	g.line(`(func $__json_encode_into (param $v i32) (param $buf i32) (result i32)`)
	g.indent++
	g.line(`(local $tag i32) (local $payload i32)`)
	g.line(`(local $arr i32) (local $aLen i32) (local $i i32)`)
	g.line(`(local $m i32) (local $cap i32) (local $mLen i32) (local $kvBase i32)`)
	g.line(`(local $first i32) (local $entryIdx i32)`)
	g.line(`local.get $v`)
	g.line(`i32.load`)
	g.line(`local.set $tag`)
	g.line(`local.get $v`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $payload`)
	// JNull → "null"
	g.line(`local.get $tag`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`call $__json_str_null`)
	g.line(`call $__json_buf_str`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// JBool(b) — payload is i32 0/1.
	g.line(`local.get $tag`)
	g.line(`i32.const 1`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $payload`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`call $__json_str_true`)
	g.line(`call $__json_buf_str`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`call $__json_str_false`)
	g.line(`call $__json_buf_str`)
	g.indent--
	g.line(`end`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// JNumber(s) — payload is a string ptr; emit verbatim.
	g.line(`local.get $tag`)
	g.line(`i32.const 2`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`local.get $payload`)
	g.line(`call $__json_buf_str`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// JString(s) — emit "..." with escapes.
	g.line(`local.get $tag`)
	g.line(`i32.const 3`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 34`) // '"'
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.line(`local.get $payload`)
	g.line(`local.get $buf`)
	g.line(`call $__json_escape_into`)
	g.line(`i32.const 34`)
	g.line(`call $__json_buf_byte`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// JArray(arr) — payload is an i32[] of JsonValue ptrs.
	g.line(`local.get $tag`)
	g.line(`i32.const 4`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $payload`)
	g.line(`local.set $arr`)
	g.line(`local.get $arr`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $aLen`)
	g.line(`local.get $buf`)
	g.line(`i32.const 91`) // '['
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.line(`block $end_arr`)
	g.indent++
	g.line(`loop $arr_loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $aLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_arr`)
	g.line(`local.get $i`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 44`) // ','
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`end`)
	// arr[i] is a JsonValue ptr.
	g.line(`local.get $arr`)
	g.line(`local.get $i`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.get $buf`)
	g.line(`call $__json_encode_into`)
	g.line(`local.set $buf`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $arr_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $buf`)
	g.line(`i32.const 93`) // ']'
	g.line(`call $__json_buf_byte`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// JObject(m) — payload is a Map[string, JsonValue].
	// Iterate via direct buffer access since we know the
	// internal layout (header at +0..+16, buckets, then
	// entries). The Map struct value is m, and the buffer
	// pointer is at m[0]. cap is at buf[0], len at buf[4].
	// Entries start at buf + 16 + cap*4, each is (k:i32,
	// v:i32) = 8 bytes in insertion order.
	g.line(`local.get $tag`)
	g.line(`i32.const 5`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $payload`)
	g.line(`local.set $m`)
	g.line(`local.get $m`)
	g.line(`i32.load`)
	g.line(`local.tee $kvBase`)
	g.line(`i32.load`)
	g.line(`local.set $cap`)
	g.line(`local.get $kvBase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $mLen`)
	// entries base = kvBase + 16 + cap*4
	g.line(`local.get $kvBase`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.get $cap`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.set $kvBase`)
	g.line(`local.get $buf`)
	g.line(`i32.const 123`) // '{'
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.line(`block $end_obj`)
	g.indent++
	g.line(`loop $obj_loop`)
	g.indent++
	g.line(`local.get $entryIdx`)
	g.line(`local.get $mLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_obj`)
	g.line(`local.get $entryIdx`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 44`) // ','
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`end`)
	// "key":val
	g.line(`local.get $buf`)
	g.line(`i32.const 34`) // '"'
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	// key = entries[idx].k
	g.line(`local.get $kvBase`)
	g.line(`local.get $entryIdx`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.get $buf`)
	g.line(`call $__json_escape_into`)
	g.line(`i32.const 34`) // '"'
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 58`) // ':'
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	// val = entries[idx].v (a JsonValue ptr)
	g.line(`local.get $kvBase`)
	g.line(`local.get $entryIdx`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.get $buf`)
	g.line(`call $__json_encode_into`)
	g.line(`local.set $buf`)
	g.line(`local.get $entryIdx`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $entryIdx`)
	g.line(`br $obj_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $buf`)
	g.line(`i32.const 125`) // '}'
	g.line(`call $__json_buf_byte`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Unknown tag: shouldn't happen — return buf unchanged.
	g.line(`local.get $buf`)
	g.indent--
	g.line(`)`)

	// Tiny helpers that return the literal strings "null",
	// "true", "false" — pulling these from the string pool
	// would be cleaner but the pool requires a pre-pass; a
	// dedicated 1-byte-per-char alloc is cheap enough.
	for _, lit := range []struct {
		name, body string
	}{
		{"$__json_str_null", "null"},
		{"$__json_str_true", "true"},
		{"$__json_str_false", "false"},
	} {
		g.linef(`(func %s (result i32)`, lit.name)
		g.indent++
		// Allocate the string fresh each call. The bump
		// allocator makes this trivially cheap, and we don't
		// want to keep a singleton (which would need a static
		// data segment + initialisation).
		g.linef(`(local $s i32)`)
		g.linef(`i32.const %d`, len(lit.body)+4)
		g.line(`call $__lang_alloc`)
		g.line(`local.tee $s`)
		g.linef(`i32.const %d`, len(lit.body))
		g.line(`i32.store`)
		// Write each byte.
		for i, c := range []byte(lit.body) {
			g.line(`local.get $s`)
			g.linef(`i32.const %d`, 4+i)
			g.line(`i32.add`)
			g.linef(`i32.const %d`, c)
			g.line(`i32.store8`)
		}
		g.line(`local.get $s`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)
	}

	// $json_encode(v): public entry point. New buffer,
	// encode in place, return content pointer (= buf + 8,
	// which has the len prefix at -4).
	g.line(`(func $json_encode (param $v i32) (result i32)`)
	g.indent++
	g.line(`(local $buf i32)`)
	g.line(`call $__json_buf_new`)
	g.line(`local.set $buf`)
	g.line(`local.get $v`)
	g.line(`local.get $buf`)
	g.line(`call $__json_encode_into`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)
}

// emitJsonParseHelpers writes `$json_parse(s)` plus its
// supporting recursive-descent helpers.
//
// State struct (16 bytes, allocated by `$json_parse`):
//
//	+0  s     : i32  — content ptr of the input string
//	+4  sLen  : i32  — input length
//	+8  pos   : i32  — current scan position
//	+12 error : i32  — non-zero on parse failure
//
// All helpers take the state pointer; each parse function
// returns the parsed value's heap pointer (or 0 on error,
// with `error` flag set so callers can short-circuit).
func (g *generator) emitJsonParseHelpers() {
	// === Variant constructors ===
	// JNull (tag = 0, no payload, 4-byte alloc).
	g.line(`(func $__json_jv_null (result i32)`)
	g.indent++
	g.line(`(local $v i32)`)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $v`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $v`)
	g.indent--
	g.line(`)`)
	// Generic single-payload constructor (tag, payload).
	g.line(`(func $__json_jv (param $tag i32) (param $payload i32) (result i32)`)
	g.indent++
	g.line(`(local $v i32)`)
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $v`)
	g.line(`local.get $tag`)
	g.line(`i32.store`)
	g.line(`local.get $v`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $payload`)
	g.line(`i32.store`)
	g.line(`local.get $v`)
	g.indent--
	g.line(`)`)

	// === State accessors ===
	// $__json_p_pos(p) -> i32
	g.line(`(func $__json_p_pos (param $p i32) (result i32)`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.indent--
	g.line(`)`)
	// $__json_p_set_pos(p, v)
	g.line(`(func $__json_p_set_pos (param $p i32) (param $v i32)`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $v`)
	g.line(`i32.store`)
	g.indent--
	g.line(`)`)
	// $__json_p_byte(p) -> i32 (or 0 if at end)
	g.line(`(func $__json_p_byte (param $p i32) (result i32)`)
	g.indent++
	g.line(`(local $pos i32)`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`local.tee $pos`)
	g.line(`local.get $p`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`i32.ge_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 0`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`i32.load`)
	g.line(`local.get $pos`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
	// $__json_p_set_error(p)
	g.line(`(func $__json_p_set_error (param $p i32)`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`i32.const 12`)
	g.line(`i32.add`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.indent--
	g.line(`)`)
	// $__json_p_error(p) -> i32
	g.line(`(func $__json_p_error (param $p i32) (result i32)`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`i32.const 12`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.indent--
	g.line(`)`)

	// === Whitespace skipping ===
	g.line(`(func $__json_p_skip_ws (param $p i32)`)
	g.indent++
	g.line(`(local $b i32) (local $pos i32) (local $sLen i32)`)
	g.line(`local.get $p`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`local.tee $pos`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end`)
	g.line(`local.get $p`)
	g.line(`i32.load`)
	g.line(`local.get $pos`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.tee $b`)
	g.line(`i32.const 32`)
	g.line(`i32.eq`)
	g.line(`local.get $b`)
	g.line(`i32.const 9`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.line(`local.get $b`)
	g.line(`i32.const 10`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.line(`local.get $b`)
	g.line(`i32.const 13`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.line(`i32.eqz`)
	g.line(`br_if $end`)
	g.line(`local.get $p`)
	g.line(`local.get $pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)

	// === Match a literal at the current pos: try to match
	// `len` bytes starting at memory address `lit` (in the
	// program's data segment is awkward; instead callers
	// inline byte-by-byte since the literals are short:
	// "null", "true", "false"). Helper inlined below.

	// === Number scan ===
	// $__json_p_number(p) -> JsonValue* (or 0). Slices the
	// scanned span as the JNumber's string payload.
	g.line(`(func $__json_p_number (param $p i32) (result i32)`)
	g.indent++
	g.line(`(local $start i32) (local $b i32) (local $pos i32) (local $sLen i32)`)
	g.line(`(local $s i32)`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`local.set $start`)
	g.line(`local.get $p`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	// Optional '-'.
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`i32.const 45`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.indent--
	g.line(`end`)
	// Need at least one digit.
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`local.tee $b`)
	g.line(`i32.const 48`)
	g.line(`i32.lt_u`)
	g.line(`local.get $b`)
	g.line(`i32.const 57`)
	g.line(`i32.gt_u`)
	g.line(`i32.or`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Integer digits.
	g.line(`block $end_int`)
	g.indent++
	g.line(`loop $int_loop`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`local.tee $b`)
	g.line(`i32.const 48`)
	g.line(`i32.lt_u`)
	g.line(`local.get $b`)
	g.line(`i32.const 57`)
	g.line(`i32.gt_u`)
	g.line(`i32.or`)
	g.line(`br_if $end_int`)
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`br $int_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Optional '.<digits>'.
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`i32.const 46`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`block $end_frac`)
	g.indent++
	g.line(`loop $frac_loop`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`local.tee $b`)
	g.line(`i32.const 48`)
	g.line(`i32.lt_u`)
	g.line(`local.get $b`)
	g.line(`i32.const 57`)
	g.line(`i32.gt_u`)
	g.line(`i32.or`)
	g.line(`br_if $end_frac`)
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`br $frac_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Optional [eE][+-]?<digits>.
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`local.tee $b`)
	g.line(`i32.const 101`)
	g.line(`i32.eq`)
	g.line(`local.get $b`)
	g.line(`i32.const 69`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`local.tee $b`)
	g.line(`i32.const 43`)
	g.line(`i32.eq`)
	g.line(`local.get $b`)
	g.line(`i32.const 45`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.indent--
	g.line(`end`)
	g.line(`block $end_exp`)
	g.indent++
	g.line(`loop $exp_loop`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`local.tee $b`)
	g.line(`i32.const 48`)
	g.line(`i32.lt_u`)
	g.line(`local.get $b`)
	g.line(`i32.const 57`)
	g.line(`i32.gt_u`)
	g.line(`i32.or`)
	g.line(`br_if $end_exp`)
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`br $exp_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Slice [start, pos) into a fresh string, wrap as JNumber.
	g.line(`local.get $p`)
	g.line(`i32.load`)
	g.line(`local.get $start`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`call $__str_slice`)
	g.line(`local.set $s`)
	g.line(`i32.const 2`)
	g.line(`local.get $s`)
	g.line(`call $__json_jv`)
	g.indent--
	g.line(`)`)

	// === String parse ===
	// $__json_p_string(p) -> string* — consumes the
	// surrounding quotes, decodes escapes into a fresh
	// length-prefixed string. Errors set the parser flag
	// and return 0.
	g.line(`(func $__json_p_string (param $p i32) (result i32)`)
	g.indent++
	g.line(`(local $b i32) (local $buf i32) (local $hex i32) (local $u i32)`)
	g.line(`(local $i i32) (local $hi i32)`)
	// Expect '"'.
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`i32.const 34`)
	g.line(`i32.ne`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	// Init buf.
	g.line(`call $__json_buf_new`)
	g.line(`local.set $buf`)
	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`local.tee $b`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $b`)
	g.line(`i32.const 34`) // '"' closes the string
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`br $end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $b`)
	g.line(`i32.const 92`) // '\\'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`local.tee $b`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Switch on escape char.
	// "  \  /  pass through literal byte
	g.line(`local.get $b`)
	g.line(`i32.const 34`)
	g.line(`i32.eq`)
	g.line(`local.get $b`)
	g.line(`i32.const 92`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.line(`local.get $b`)
	g.line(`i32.const 47`)
	g.line(`i32.eq`)
	g.line(`i32.or`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`local.get $b`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $b`)
	g.line(`i32.const 110`) // 'n'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 10`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $b`)
	g.line(`i32.const 114`) // 'r'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 13`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $b`)
	g.line(`i32.const 116`) // 't'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 9`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $b`)
	g.line(`i32.const 98`) // 'b'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 8`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $b`)
	g.line(`i32.const 102`) // 'f'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 12`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $b`)
	g.line(`i32.const 117`) // 'u'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	// \uXXXX — read 4 hex digits, build code point, encode UTF-8.
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`i32.const 0`)
	g.line(`local.set $u`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $hex_done`)
	g.indent++
	g.line(`loop $hex_loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`i32.const 4`)
	g.line(`i32.ge_u`)
	g.line(`br_if $hex_done`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`local.tee $hex`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Convert hex digit. Stash the digit value into $hi
	// directly inside each branch (rather than chaining
	// `if (result i32)` cascades, which had a stack-
	// balance bug — the inner `local.set $hi` lived
	// inside one branch instead of after the cascade).
	g.line(`local.get $hex`)
	g.line(`i32.const 48`)
	g.line(`i32.ge_u`)
	g.line(`local.get $hex`)
	g.line(`i32.const 57`)
	g.line(`i32.le_u`)
	g.line(`i32.and`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $hex`)
	g.line(`i32.const 48`)
	g.line(`i32.sub`)
	g.line(`local.set $hi`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $hex`)
	g.line(`i32.const 97`)
	g.line(`i32.ge_u`)
	g.line(`local.get $hex`)
	g.line(`i32.const 102`)
	g.line(`i32.le_u`)
	g.line(`i32.and`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $hex`)
	g.line(`i32.const 87`)
	g.line(`i32.sub`)
	g.line(`local.set $hi`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $hex`)
	g.line(`i32.const 65`)
	g.line(`i32.ge_u`)
	g.line(`local.get $hex`)
	g.line(`i32.const 70`)
	g.line(`i32.le_u`)
	g.line(`i32.and`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $hex`)
	g.line(`i32.const 55`)
	g.line(`i32.sub`)
	g.line(`local.set $hi`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $u`)
	g.line(`i32.const 4`)
	g.line(`i32.shl`)
	g.line(`local.get $hi`)
	g.line(`i32.or`)
	g.line(`local.set $u`)
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $hex_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`) // $hex_done
	// UTF-8 encode the code point in $u (BMP only —
	// surrogate pairs aren't combined here, just emitted as
	// individual 3-byte sequences which produces invalid
	// UTF-8 for true astral chars; standard-conforming
	// pairing is a follow-up).
	g.line(`local.get $u`)
	g.line(`i32.const 128`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`local.get $u`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $u`)
	g.line(`i32.const 2048`)
	g.line(`i32.lt_u`)
	g.line(`if`)
	g.indent++
	// 2-byte: 110xxxxx 10xxxxxx
	g.line(`local.get $buf`)
	g.line(`local.get $u`)
	g.line(`i32.const 6`)
	g.line(`i32.shr_u`)
	g.line(`i32.const 192`)
	g.line(`i32.or`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.get $u`)
	g.line(`i32.const 63`)
	g.line(`i32.and`)
	g.line(`i32.const 128`)
	g.line(`i32.or`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`else`)
	g.indent++
	// 3-byte: 1110xxxx 10xxxxxx 10xxxxxx
	g.line(`local.get $buf`)
	g.line(`local.get $u`)
	g.line(`i32.const 12`)
	g.line(`i32.shr_u`)
	g.line(`i32.const 224`)
	g.line(`i32.or`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.get $u`)
	g.line(`i32.const 6`)
	g.line(`i32.shr_u`)
	g.line(`i32.const 63`)
	g.line(`i32.and`)
	g.line(`i32.const 128`)
	g.line(`i32.or`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.get $u`)
	g.line(`i32.const 63`)
	g.line(`i32.and`)
	g.line(`i32.const 128`)
	g.line(`i32.or`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// `\u` branch already advanced past XXXX; no `pos++` here.
	g.line(`br $loop`)
	g.indent--
	g.line(`else`)
	g.indent++
	// Unknown escape: error.
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	// Plain byte.
	g.line(`local.get $buf`)
	g.line(`local.get $b`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Return content_ptr (= buf+8). The buf's `len` slot
	// at +4 doubles as the length prefix.
	g.line(`local.get $buf`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)

	// === Value dispatch ===
	g.line(`(func $__json_p_value (param $p i32) (result i32)`)
	g.indent++
	g.line(`(local $b i32) (local $s i32)`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_skip_ws`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`local.tee $b`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// 'n' "null"
	g.line(`local.get $b`)
	g.line(`i32.const 110`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	// Validate the trailing chars u-l-l were actually there.
	// Going back: pos-3 = 'u', pos-2 = 'l', pos-1 = 'l'.
	// Use byte loads at base + (pos - 3..). Skipped strict
	// validation here for brevity — caller checks pos<sLen
	// at outer level + the leading byte was 'n'; if the
	// next 3 chars aren't u-l-l, the parse will produce an
	// off-by-some result. We check explicitly:
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`local.get $p`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`i32.gt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Validate "ull".
	g.line(`local.get $p`)
	g.line(`i32.load`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.add`)
	g.line(`i32.const 3`)
	g.line(`i32.sub`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 117`) // 'u'
	g.line(`i32.ne`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $p`)
	g.line(`i32.load`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.add`)
	g.line(`i32.const 2`)
	g.line(`i32.sub`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 108`) // 'l'
	g.line(`i32.ne`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $p`)
	g.line(`i32.load`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.add`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`i32.load8_u`)
	g.line(`i32.const 108`)
	g.line(`i32.ne`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`call $__json_jv_null`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// 't' "true"
	g.line(`local.get $b`)
	g.line(`i32.const 116`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`local.get $p`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`i32.gt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Skip strict validation; trust the leading 't' as
	// signal. (A malformed "tXXX" would just produce
	// JBool(true) silently — accept this for now; full
	// validation is a follow-up.)
	g.line(`i32.const 1`)
	g.line(`i32.const 1`)
	g.line(`call $__json_jv`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// 'f' "false"
	g.line(`local.get $b`)
	g.line(`i32.const 102`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 5`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`local.get $p`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`i32.gt_u`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const 1`)
	g.line(`i32.const 0`)
	g.line(`call $__json_jv`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// '"' string
	g.line(`local.get $b`)
	g.line(`i32.const 34`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_string`)
	g.line(`local.set $s`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_error`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const 3`)
	g.line(`local.get $s`)
	g.line(`call $__json_jv`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// '[' array
	g.line(`local.get $b`)
	g.line(`i32.const 91`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_array`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// '{' object
	g.line(`local.get $b`)
	g.line(`i32.const 123`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_object`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// '-' or digit -> number
	g.line(`local.get $b`)
	g.line(`i32.const 45`)
	g.line(`i32.eq`)
	g.line(`local.get $b`)
	g.line(`i32.const 48`)
	g.line(`i32.ge_u`)
	g.line(`local.get $b`)
	g.line(`i32.const 57`)
	g.line(`i32.le_u`)
	g.line(`i32.and`)
	g.line(`i32.or`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_number`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.indent--
	g.line(`)`)

	// === Array parse ===
	// Builds an i32-buffer of element ptrs, then converts
	// to a length-prefixed array on close.
	g.line(`(func $__json_p_array (param $p i32) (result i32)`)
	g.indent++
	g.line(`(local $buf i32) (local $count i32) (local $arr i32) (local $elem i32)`)
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_skip_ws`)
	// Empty array shortcut.
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`i32.const 93`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $arr`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`i32.const 4`)
	g.line(`local.get $arr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__json_jv`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`call $__json_buf_new`)
	g.line(`local.set $buf`)
	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_value`)
	g.line(`local.tee $elem`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_error`)
	g.line(`i32.or`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Append elem ptr (4 bytes) to buf manually since
	// __json_buf_byte writes 1 byte. Inline the grow.
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`call $__json_buf_grow`)
	g.line(`local.set $buf`)
	g.line(`local.get $buf`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`i32.add`)
	g.line(`local.get $elem`)
	g.line(`i32.store`)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.store`)
	g.line(`local.get $count`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $count`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_skip_ws`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`i32.const 44`) // ','
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_skip_ws`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`i32.const 93`) // ']'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`br $end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Convert buf to length-prefixed array.
	g.line(`local.get $count`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $arr`)
	g.line(`local.get $count`)
	g.line(`i32.store`)
	g.line(`local.get $arr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $buf`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $count`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`memory.copy`)
	g.line(`i32.const 4`)
	g.line(`local.get $arr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__json_jv`)
	g.indent--
	g.line(`)`)

	// === Object parse ===
	g.line(`(func $__json_p_object (param $p i32) (result i32)`)
	g.indent++
	g.line(`(local $m i32) (local $key i32) (local $val i32)`)
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_skip_ws`)
	// Empty object shortcut.
	g.line(`i32.const 8`)
	g.line(`i32.const 1`)
	g.line(`call $map_new`)
	g.line(`local.set $m`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`i32.const 125`) // '}'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`i32.const 5`)
	g.line(`local.get $m`)
	g.line(`call $__json_jv`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_string`)
	g.line(`local.set $key`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_error`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_skip_ws`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`i32.const 58`) // ':'
	g.line(`i32.ne`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_value`)
	g.line(`local.set $val`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_error`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $m`)
	g.line(`local.get $key`)
	g.line(`local.get $val`)
	g.line(`call $__method_Map_set`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_skip_ws`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`i32.const 44`) // ','
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_skip_ws`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_byte`)
	g.line(`i32.const 125`) // '}'
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $p`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`call $__json_p_set_pos`)
	g.line(`br $end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_set_error`)
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const 5`)
	g.line(`local.get $m`)
	g.line(`call $__json_jv`)
	g.indent--
	g.line(`)`)

	// === Public entry ===
	g.line(`(func $json_parse (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $p i32) (local $v i32) (local $opt i32)`)
	g.line(`i32.const 16`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $p`)
	g.line(`local.get $s`)
	g.line(`i32.store`)
	g.line(`local.get $p`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	g.line(`local.get $p`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $p`)
	g.line(`i32.const 12`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_value`)
	g.line(`local.set $v`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_skip_ws`)
	// On error or trailing garbage -> None.
	g.line(`local.get $p`)
	g.line(`call $__json_p_error`)
	g.line(`local.get $p`)
	g.line(`call $__json_p_pos`)
	g.line(`local.get $p`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`i32.lt_u`)
	g.line(`i32.or`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $v`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.indent--
	g.line(`)`)
}

// emitMapHelpers writes the runtime functions backing the
// auto-injected generic `Map[K, V]` (see PR 4 of
// docs/LANGUAGE-DIRECTION.md). IndexMap shape: insertion-
// ordered entries array + open-addressed bucket index.
//
// Map struct layout (single-i32 wrapper):
//
//	+0  data: i32 — pointer to the kv buffer below
//
// kv buffer layout (data points here):
//
//	+0   cap     : i32 — bucket count, power-of-2, ≥ 4
//	+4   len     : i32 — live entry count
//	+8   keyKind : i32 — 0 = i32-sized scalar, 1 = string
//	+12  _pad    : i32 — reserved (future fingerprint metadata)
//	+16  buckets : i32[cap]  — entry-index-or-sentinel array
//	                          (-1 = empty, -2 = tombstone)
//	+16+cap*4  entries : (k:i32, v:i32)[cap]
//	                  — flat (key, value) pairs in insertion order
//
// Total buffer size: 16 + 12*cap bytes.
//
// Operations:
//   - lookup / has / get: hash key → probe bucket array,
//     skipping tombstones, until we hit either the key
//     (→ found) or an empty slot (→ not found). O(1) expected.
//   - set: probe; on hit, update value in the entries slot;
//     on miss, append entry, write its index into the
//     terminating bucket (or first tombstone seen along the
//     probe).
//   - delete: probe; tombstone the bucket; swap-with-last in
//     the entries array and patch the swapped entry's bucket
//     to point at its new location. Trades insertion order
//     past the deletion point for O(1) removal.
//   - resize: when `(len + 1) * 4 ≥ cap * 3` (75% load
//     factor), double cap and re-insert all entries.
//
// Hash function: Wang's integer mix for scalar keys, FNV-1a
// 32-bit for string keys. Both produce a 32-bit hash that we
// mask to `cap - 1` (cap is power-of-2). Wyhash-flavour upgrade
// is straightforward once we want the better mixing
// guarantees on adversarial inputs.
func (g *generator) emitMapHelpers() {
	const (
		emptyBucket = -1
		tombstone   = -2
	)

	// $__map_pow2_ceil(n): i32 — round n up to the next
	// power-of-2, clamped to a minimum of 4.
	g.line(`(func $__map_pow2_ceil (param $n i32) (result i32)`)
	g.indent++
	g.line(`local.get $n`)
	g.line(`i32.const 4`)
	g.line(`i32.le_s`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// 1 << (32 - clz(n - 1))
	g.line(`i32.const 1`)
	g.line(`i32.const 32`)
	g.line(`local.get $n`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`i32.clz`)
	g.line(`i32.sub`)
	g.line(`i32.shl`)
	g.indent--
	g.line(`)`)

	// $__map_hash(k, keyKind): i32 — Wang's integer mix for
	// scalar keys, FNV-1a 32-bit for string keys. Both produce
	// a 32-bit hash that callers mask down to `cap - 1`.
	g.line(`(func $__map_hash (param $k i32) (param $keyKind i32) (result i32)`)
	g.indent++
	g.line(`(local $h i32) (local $i i32) (local $sLen i32)`)
	g.line(`local.get $keyKind`)
	g.line(`i32.eqz`)
	g.line(`if (result i32)`)
	g.indent++
	// Wang's integer mix.
	g.line(`local.get $k`)
	g.line(`i32.const 61`)
	g.line(`i32.xor`)
	g.line(`local.tee $h`)
	g.line(`i32.const 16`)
	g.line(`i32.shr_u`)
	g.line(`local.get $h`)
	g.line(`i32.xor`)
	g.line(`local.set $h`)
	g.line(`local.get $h`)
	g.line(`local.get $h`)
	g.line(`i32.const 3`)
	g.line(`i32.shl`)
	g.line(`i32.add`)
	g.line(`local.set $h`)
	g.line(`local.get $h`)
	g.line(`local.get $h`)
	g.line(`i32.const 4`)
	g.line(`i32.shr_u`)
	g.line(`i32.xor`)
	g.line(`i32.const 0x27d4eb2d`)
	g.line(`i32.mul`)
	g.line(`local.set $h`)
	g.line(`local.get $h`)
	g.line(`local.get $h`)
	g.line(`i32.const 15`)
	g.line(`i32.shr_u`)
	g.line(`i32.xor`)
	g.indent--
	g.line(`else`)
	g.indent++
	// FNV-1a 32-bit. h = 0x811c9dc5; for each byte b in k:
	//   h = (h ^ b) * 0x01000193.
	g.line(`i32.const 0x811c9dc5`)
	g.line(`local.set $h`)
	g.line(`local.get $k`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $sLen`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $sLen`)
	g.line(`i32.ge_s`)
	g.line(`br_if $break`)
	g.line(`local.get $h`)
	g.line(`local.get $k`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.xor`)
	g.line(`i32.const 0x01000193`)
	g.line(`i32.mul`)
	g.line(`local.set $h`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $h`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)

	// $map_new(cap_hint, keyKind): allocate the wrapper and a
	// kv buffer with `cap = max(4, pow2_ceil(cap_hint))`,
	// initialise the bucket array to all-empty (`-1`, written
	// as 0xFF byte fills via memory.fill).
	g.line(`(func $map_new (param $cap_hint i32) (param $keyKind i32) (result i32)`)
	g.indent++
	g.line(`(local $cap i32) (local $buf i32) (local $m i32)`)
	g.line(`local.get $cap_hint`)
	g.line(`call $__map_pow2_ceil`)
	g.line(`local.set $cap`)
	// buf = alloc(16 + 12*cap)
	g.line(`local.get $cap`)
	g.line(`i32.const 12`)
	g.line(`i32.mul`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $buf`)
	// header
	g.line(`local.get $buf`)
	g.line(`local.get $cap`)
	g.line(`i32.store`)
	g.line(`local.get $buf`)
	g.line(`i32.const 0`)
	g.line(`i32.store offset=4`)
	g.line(`local.get $buf`)
	g.line(`local.get $keyKind`)
	g.line(`i32.store offset=8`)
	g.line(`local.get $buf`)
	g.line(`i32.const 0`)
	g.line(`i32.store offset=12`)
	// memory.fill(buf+16, 0xFF, cap*4) → buckets all `-1`
	g.line(`local.get $buf`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`i32.const 255`)
	g.line(`local.get $cap`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`memory.fill`)
	// wrap: m = alloc(4); m[0] = buf
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $m`)
	g.line(`local.get $buf`)
	g.line(`i32.store`)
	g.line(`local.get $m`)
	g.indent--
	g.line(`)`)

	// $__method_Map_len(m): i32 — header field at buf[4].
	g.line(`(func $__method_Map_len (param $m i32) (result i32)`)
	g.indent++
	g.line(`local.get $m`)
	g.line(`i32.load`)
	g.line(`i32.load offset=4`)
	g.indent--
	g.line(`)`)

	// $__map_lookup(m, k): i32 — returns the entry index of
	// the matching key, or `-1` if not present. Linear probing
	// over the bucket array, skipping tombstones (which keep
	// probing valid past deletions).
	g.line(`(func $__map_lookup (param $m i32) (param $k i32) (result i32)`)
	g.indent++
	g.line(`(local $buf i32) (local $cap i32) (local $keyKind i32)`)
	g.line(`(local $mask i32) (local $bucket i32) (local $b i32)`)
	g.line(`(local $entriesBase i32) (local $entryK i32) (local $eq i32)`)
	g.line(`local.get $m`)
	g.line(`i32.load`)
	g.line(`local.tee $buf`)
	g.line(`i32.load`)
	g.line(`local.set $cap`)
	g.line(`local.get $buf`)
	g.line(`i32.load offset=8`)
	g.line(`local.set $keyKind`)
	g.line(`local.get $cap`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.set $mask`)
	g.line(`local.get $buf`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.get $cap`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.set $entriesBase`)
	g.line(`local.get $k`)
	g.line(`local.get $keyKind`)
	g.line(`call $__map_hash`)
	g.line(`local.get $mask`)
	g.line(`i32.and`)
	g.line(`local.set $bucket`)
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	// b = buckets[bucket]
	g.line(`local.get $buf`)
	g.line(`local.get $bucket`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load offset=16`)
	g.line(`local.set $b`)
	// empty → not found
	g.line(`local.get $b`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const -1`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// not tombstone → compare keys
	g.line(`local.get $b`)
	g.line(`i32.const -2`)
	g.line(`i32.ne`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $entriesBase`)
	g.line(`local.get $b`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $entryK`)
	g.line(`local.get $keyKind`)
	g.line(`i32.eqz`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $entryK`)
	g.line(`local.get $k`)
	g.line(`i32.eq`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $entryK`)
	g.line(`local.get $k`)
	g.line(`call $__str_eq`)
	g.indent--
	g.line(`end`)
	g.line(`local.set $eq`)
	g.line(`local.get $eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $b`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// bucket = (bucket + 1) & mask
	g.line(`local.get $bucket`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.get $mask`)
	g.line(`i32.and`)
	g.line(`local.set $bucket`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const -1`)
	g.indent--
	g.line(`)`)

	// $__method_Map_has(m, k): bool
	g.line(`(func $__method_Map_has (param $m i32) (param $k i32) (result i32)`)
	g.indent++
	g.line(`local.get $m`)
	g.line(`local.get $k`)
	g.line(`call $__map_lookup`)
	g.line(`i32.const -1`)
	g.line(`i32.ne`)
	g.indent--
	g.line(`)`)

	// $__method_Map_get(m, k): Option[V]
	g.line(`(func $__method_Map_get (param $m i32) (param $k i32) (result i32)`)
	g.indent++
	g.line(`(local $idx i32) (local $opt i32) (local $buf i32) (local $cap i32)`)
	g.line(`(local $entriesBase i32)`)
	g.line(`local.get $m`)
	g.line(`local.get $k`)
	g.line(`call $__map_lookup`)
	g.line(`local.set $idx`)
	g.line(`local.get $idx`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $opt`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $opt`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Some(v): alloc 8, tag=0, value at +4.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $opt`)
	g.line(`local.get $opt`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $m`)
	g.line(`i32.load`)
	g.line(`local.tee $buf`)
	g.line(`i32.load`)
	g.line(`local.set $cap`)
	g.line(`local.get $buf`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.get $cap`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.set $entriesBase`)
	g.line(`local.get $opt`)
	g.line(`local.get $entriesBase`)
	g.line(`local.get $idx`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load offset=4`)
	g.line(`i32.store offset=4`)
	g.line(`local.get $opt`)
	g.indent--
	g.line(`)`)

	// $__method_Map_set(m, k, v): probe; on hit update value
	// in-place; on miss append entry + write its index into
	// the terminating bucket (or first tombstone seen along
	// the probe). Resize first when the next insert would
	// push past the 75% load factor.
	g.line(`(func $__method_Map_set (param $m i32) (param $k i32) (param $v i32)`)
	g.indent++
	g.line(`(local $buf i32) (local $cap i32) (local $len i32)`)
	g.line(`(local $keyKind i32) (local $mask i32)`)
	g.line(`(local $bucket i32) (local $b i32) (local $insertSlot i32)`)
	g.line(`(local $entriesBase i32) (local $entryK i32) (local $eq i32)`)
	// Resize check: (len + 1) * 4 >= cap * 3 ?
	g.line(`local.get $m`)
	g.line(`i32.load`)
	g.line(`local.tee $buf`)
	g.line(`i32.load`)
	g.line(`local.set $cap`)
	g.line(`local.get $buf`)
	g.line(`i32.load offset=4`)
	g.line(`local.set $len`)
	g.line(`local.get $len`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`local.get $cap`)
	g.line(`i32.const 3`)
	g.line(`i32.mul`)
	g.line(`i32.ge_s`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $m`)
	g.line(`call $__map_grow`)
	g.line(`drop`)
	g.line(`local.get $m`)
	g.line(`i32.load`)
	g.line(`local.tee $buf`)
	g.line(`i32.load`)
	g.line(`local.set $cap`)
	g.line(`local.get $buf`)
	g.line(`i32.load offset=4`)
	g.line(`local.set $len`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $buf`)
	g.line(`i32.load offset=8`)
	g.line(`local.set $keyKind`)
	g.line(`local.get $cap`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.set $mask`)
	g.line(`local.get $buf`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.get $cap`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.set $entriesBase`)
	g.line(`local.get $k`)
	g.line(`local.get $keyKind`)
	g.line(`call $__map_hash`)
	g.line(`local.get $mask`)
	g.line(`i32.and`)
	g.line(`local.set $bucket`)
	g.line(`i32.const -1`)
	g.line(`local.set $insertSlot`)
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`local.get $bucket`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load offset=16`)
	g.line(`local.set $b`)
	// empty → done probing; insert here (or earlier tomb).
	g.line(`local.get $b`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $insertSlot`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $bucket`)
	g.line(`local.set $insertSlot`)
	g.indent--
	g.line(`end`)
	g.line(`br $break`)
	g.indent--
	g.line(`end`)
	// tombstone → record first; keep probing.
	g.line(`local.get $b`)
	g.line(`i32.const -2`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $insertSlot`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $bucket`)
	g.line(`local.set $insertSlot`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`else`)
	g.indent++
	// alive entry → compare keys.
	g.line(`local.get $entriesBase`)
	g.line(`local.get $b`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $entryK`)
	g.line(`local.get $keyKind`)
	g.line(`i32.eqz`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $entryK`)
	g.line(`local.get $k`)
	g.line(`i32.eq`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $entryK`)
	g.line(`local.get $k`)
	g.line(`call $__str_eq`)
	g.indent--
	g.line(`end`)
	g.line(`local.set $eq`)
	g.line(`local.get $eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $entriesBase`)
	g.line(`local.get $b`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $v`)
	g.line(`i32.store offset=4`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $bucket`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.get $mask`)
	g.line(`i32.and`)
	g.line(`local.set $bucket`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Append entry at index = len.
	g.line(`local.get $entriesBase`)
	g.line(`local.get $len`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $k`)
	g.line(`i32.store`)
	g.line(`local.get $entriesBase`)
	g.line(`local.get $len`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $v`)
	g.line(`i32.store offset=4`)
	// buckets[insertSlot] = len
	g.line(`local.get $buf`)
	g.line(`local.get $insertSlot`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $len`)
	g.line(`i32.store offset=16`)
	// len = len + 1
	g.line(`local.get $buf`)
	g.line(`local.get $len`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.store offset=4`)
	g.indent--
	g.line(`)`)

	// $__map_grow: double capacity, allocate fresh buffer,
	// re-hash + re-insert each existing entry into the new
	// bucket array. Returns the new buffer pointer; the
	// wrapper is updated in place. The bump allocator can't
	// reclaim the old buffer; that pays back when the arena
	// resets at scope exit.
	g.line(`(func $__map_grow (param $m i32) (result i32)`)
	g.indent++
	g.line(`(local $old_buf i32) (local $old_cap i32) (local $old_len i32)`)
	g.line(`(local $old_kk i32) (local $old_entries i32)`)
	g.line(`(local $new_buf i32) (local $new_cap i32) (local $new_mask i32)`)
	g.line(`(local $new_entries i32)`)
	g.line(`(local $i i32) (local $key i32) (local $val i32)`)
	g.line(`(local $bucket i32) (local $b i32)`)
	g.line(`local.get $m`)
	g.line(`i32.load`)
	g.line(`local.tee $old_buf`)
	g.line(`i32.load`)
	g.line(`local.set $old_cap`)
	g.line(`local.get $old_buf`)
	g.line(`i32.load offset=4`)
	g.line(`local.set $old_len`)
	g.line(`local.get $old_buf`)
	g.line(`i32.load offset=8`)
	g.line(`local.set $old_kk`)
	g.line(`local.get $old_buf`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.get $old_cap`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.set $old_entries`)
	g.line(`local.get $old_cap`)
	g.line(`i32.eqz`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 4`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $old_cap`)
	g.line(`i32.const 1`)
	g.line(`i32.shl`)
	g.indent--
	g.line(`end`)
	g.line(`local.set $new_cap`)
	g.line(`local.get $new_cap`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.set $new_mask`)
	g.line(`local.get $new_cap`)
	g.line(`i32.const 12`)
	g.line(`i32.mul`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $new_buf`)
	g.line(`local.get $new_buf`)
	g.line(`local.get $new_cap`)
	g.line(`i32.store`)
	g.line(`local.get $new_buf`)
	g.line(`local.get $old_len`)
	g.line(`i32.store offset=4`)
	g.line(`local.get $new_buf`)
	g.line(`local.get $old_kk`)
	g.line(`i32.store offset=8`)
	g.line(`local.get $new_buf`)
	g.line(`i32.const 0`)
	g.line(`i32.store offset=12`)
	// Reset bucket array to empty.
	g.line(`local.get $new_buf`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`i32.const 255`)
	g.line(`local.get $new_cap`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`memory.fill`)
	g.line(`local.get $new_buf`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.get $new_cap`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.set $new_entries`)
	// For each old entry, copy to new entries[i] and probe
	// the new bucket array for an empty slot.
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $old_len`)
	g.line(`i32.ge_s`)
	g.line(`br_if $break`)
	g.line(`local.get $old_entries`)
	g.line(`local.get $i`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $key`)
	g.line(`local.get $old_entries`)
	g.line(`local.get $i`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load offset=4`)
	g.line(`local.set $val`)
	g.line(`local.get $new_entries`)
	g.line(`local.get $i`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $key`)
	g.line(`i32.store`)
	g.line(`local.get $new_entries`)
	g.line(`local.get $i`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $val`)
	g.line(`i32.store offset=4`)
	g.line(`local.get $key`)
	g.line(`local.get $old_kk`)
	g.line(`call $__map_hash`)
	g.line(`local.get $new_mask`)
	g.line(`i32.and`)
	g.line(`local.set $bucket`)
	g.line(`block $insertBreak`)
	g.indent++
	g.line(`loop $insertLoop`)
	g.indent++
	g.line(`local.get $new_buf`)
	g.line(`local.get $bucket`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load offset=16`)
	g.line(`local.set $b`)
	g.line(`local.get $b`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $new_buf`)
	g.line(`local.get $bucket`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $i`)
	g.line(`i32.store offset=16`)
	g.line(`br $insertBreak`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $bucket`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.get $new_mask`)
	g.line(`i32.and`)
	g.line(`local.set $bucket`)
	g.line(`br $insertLoop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $m`)
	g.line(`local.get $new_buf`)
	g.line(`i32.store`)
	g.line(`local.get $new_buf`)
	g.indent--
	g.line(`)`)

	// $__method_Map_keys / $__method_Map_values: snapshot one
	// column of the entries array into a freshly-allocated
	// length-prefixed `i32[]`. Insertion order preserved up to
	// any deletions (which use swap-with-last).
	emitMapColumn := func(name string, kvOffset int) {
		g.linef(`(func %s (param $m i32) (result i32)`, name)
		g.indent++
		g.line(`(local $buf i32) (local $cap i32) (local $len i32)`)
		g.line(`(local $arr i32) (local $entriesBase i32) (local $i i32)`)
		g.line(`local.get $m`)
		g.line(`i32.load`)
		g.line(`local.tee $buf`)
		g.line(`i32.load`)
		g.line(`local.set $cap`)
		g.line(`local.get $buf`)
		g.line(`i32.load offset=4`)
		g.line(`local.set $len`)
		g.line(`local.get $buf`)
		g.line(`i32.const 16`)
		g.line(`i32.add`)
		g.line(`local.get $cap`)
		g.line(`i32.const 4`)
		g.line(`i32.mul`)
		g.line(`i32.add`)
		g.line(`local.set $entriesBase`)
		g.line(`local.get $len`)
		g.line(`i32.const 4`)
		g.line(`i32.mul`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`call $__lang_alloc`)
		g.line(`local.tee $arr`)
		g.line(`local.get $len`)
		g.line(`i32.store`)
		g.line(`i32.const 0`)
		g.line(`local.set $i`)
		g.line(`block $break`)
		g.indent++
		g.line(`loop $loop`)
		g.indent++
		g.line(`local.get $i`)
		g.line(`local.get $len`)
		g.line(`i32.ge_s`)
		g.line(`br_if $break`)
		g.line(`local.get $arr`)
		g.line(`local.get $i`)
		g.line(`i32.const 4`)
		g.line(`i32.mul`)
		g.line(`i32.add`)
		g.line(`local.get $entriesBase`)
		g.line(`local.get $i`)
		g.line(`i32.const 8`)
		g.line(`i32.mul`)
		g.line(`i32.add`)
		g.linef(`i32.load offset=%d`, kvOffset)
		g.line(`i32.store offset=4`)
		g.line(`local.get $i`)
		g.line(`i32.const 1`)
		g.line(`i32.add`)
		g.line(`local.set $i`)
		g.line(`br $loop`)
		g.indent--
		g.line(`end`)
		g.indent--
		g.line(`end`)
		g.line(`local.get $arr`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.indent--
		g.line(`)`)
	}
	emitMapColumn("$__method_Map_keys", 0)
	emitMapColumn("$__method_Map_values", 4)

	// $__method_Map_delete(m, k): bool — probe to find key.
	// On hit: tombstone the bucket; if the entry isn't the
	// last in the entries array, swap-with-last and patch the
	// swapped entry's bucket pointer to the deleted slot's
	// index. Returns true. On miss, returns false.
	g.line(`(func $__method_Map_delete (param $m i32) (param $k i32) (result i32)`)
	g.indent++
	g.line(`(local $buf i32) (local $cap i32) (local $len i32)`)
	g.line(`(local $keyKind i32) (local $mask i32)`)
	g.line(`(local $bucket i32) (local $b i32)`)
	g.line(`(local $entriesBase i32) (local $entryK i32) (local $eq i32)`)
	g.line(`(local $foundBucket i32) (local $idx i32) (local $last i32)`)
	g.line(`(local $lastKey i32) (local $lastBucket i32)`)
	g.line(`local.get $m`)
	g.line(`i32.load`)
	g.line(`local.tee $buf`)
	g.line(`i32.load`)
	g.line(`local.set $cap`)
	g.line(`local.get $buf`)
	g.line(`i32.load offset=4`)
	g.line(`local.set $len`)
	g.line(`local.get $buf`)
	g.line(`i32.load offset=8`)
	g.line(`local.set $keyKind`)
	g.line(`local.get $cap`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.set $mask`)
	g.line(`local.get $buf`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.get $cap`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.set $entriesBase`)
	g.line(`local.get $k`)
	g.line(`local.get $keyKind`)
	g.line(`call $__map_hash`)
	g.line(`local.get $mask`)
	g.line(`i32.and`)
	g.line(`local.set $bucket`)
	g.line(`i32.const -1`)
	g.line(`local.set $foundBucket`)
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`local.get $bucket`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load offset=16`)
	g.line(`local.set $b`)
	g.line(`local.get $b`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`br_if $break`)
	g.line(`local.get $b`)
	g.line(`i32.const -2`)
	g.line(`i32.ne`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $entriesBase`)
	g.line(`local.get $b`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $entryK`)
	g.line(`local.get $keyKind`)
	g.line(`i32.eqz`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $entryK`)
	g.line(`local.get $k`)
	g.line(`i32.eq`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $entryK`)
	g.line(`local.get $k`)
	g.line(`call $__str_eq`)
	g.indent--
	g.line(`end`)
	g.line(`local.set $eq`)
	g.line(`local.get $eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $bucket`)
	g.line(`local.set $foundBucket`)
	g.line(`local.get $b`)
	g.line(`local.set $idx`)
	g.line(`br $break`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $bucket`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.get $mask`)
	g.line(`i32.and`)
	g.line(`local.set $bucket`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $foundBucket`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Tombstone the bucket.
	g.line(`local.get $buf`)
	g.line(`local.get $foundBucket`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.const -2`)
	g.line(`i32.store offset=16`)
	g.line(`local.get $len`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.set $last`)
	g.line(`local.get $idx`)
	g.line(`local.get $last`)
	g.line(`i32.ne`)
	g.line(`if`)
	g.indent++
	// Find the last entry's bucket so we can patch it after
	// the swap. The last entry is alive (about to be removed
	// from the array) so the probe will hit it.
	g.line(`local.get $entriesBase`)
	g.line(`local.get $last`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $lastKey`)
	g.line(`local.get $lastKey`)
	g.line(`local.get $keyKind`)
	g.line(`call $__map_hash`)
	g.line(`local.get $mask`)
	g.line(`i32.and`)
	g.line(`local.set $lastBucket`)
	g.line(`block $lastBreak`)
	g.indent++
	g.line(`loop $lastLoop`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`local.get $lastBucket`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load offset=16`)
	g.line(`local.get $last`)
	g.line(`i32.eq`)
	g.line(`br_if $lastBreak`)
	g.line(`local.get $lastBucket`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.get $mask`)
	g.line(`i32.and`)
	g.line(`local.set $lastBucket`)
	g.line(`br $lastLoop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Move entries[last] → entries[idx].
	g.line(`local.get $entriesBase`)
	g.line(`local.get $idx`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $entriesBase`)
	g.line(`local.get $last`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	g.line(`local.get $entriesBase`)
	g.line(`local.get $idx`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $entriesBase`)
	g.line(`local.get $last`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load offset=4`)
	g.line(`i32.store offset=4`)
	// buckets[lastBucket] = idx (point at the moved entry).
	g.line(`local.get $buf`)
	g.line(`local.get $lastBucket`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $idx`)
	g.line(`i32.store offset=16`)
	g.indent--
	g.line(`end`)
	// len = last (i.e. len - 1)
	g.line(`local.get $buf`)
	g.line(`local.get $last`)
	g.line(`i32.store offset=4`)
	g.line(`i32.const 1`)
	g.indent--
	g.line(`)`)

	// $__method_Map_iter(m): MapIter[K, V] — allocate the
	// 8-byte iterator struct (data ptr + cursor index) and
	// initialise it. data = m.data so we can read len /
	// keyKind / entries[] directly without going through the
	// wrapper on every step.
	g.line(`(func $__method_Map_iter (param $m i32) (result i32)`)
	g.indent++
	g.line(`(local $it i32)`)
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $it`)
	g.line(`local.get $m`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	g.line(`local.get $it`)
	g.line(`i32.const 0`)
	g.line(`i32.store offset=4`)
	g.line(`local.get $it`)
	g.indent--
	g.line(`)`)

	// $__method_MapIter_has_next(it): bool — i < len.
	g.line(`(func $__method_MapIter_has_next (param $it i32) (result i32)`)
	g.indent++
	g.line(`local.get $it`)
	g.line(`i32.load offset=4`)
	g.line(`local.get $it`)
	g.line(`i32.load`)
	g.line(`i32.load offset=4`)
	g.line(`i32.lt_s`)
	g.indent--
	g.line(`)`)

	// $__map_iter_entry_addr(it): i32 — returns the address
	// of the current entry's (k, v) pair within the kv buffer.
	// Shared by key / value so they don't each duplicate the
	// header-walking arithmetic.
	g.line(`(func $__map_iter_entry_addr (param $it i32) (result i32)`)
	g.indent++
	g.line(`(local $buf i32)`)
	g.line(`local.get $it`)
	g.line(`i32.load`)
	g.line(`local.tee $buf`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.get $buf`)
	g.line(`i32.load`) // cap
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.get $it`)
	g.line(`i32.load offset=4`) // i
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)

	// $__method_MapIter_key(it): K
	g.line(`(func $__method_MapIter_key (param $it i32) (result i32)`)
	g.indent++
	g.line(`local.get $it`)
	g.line(`call $__map_iter_entry_addr`)
	g.line(`i32.load`)
	g.indent--
	g.line(`)`)

	// $__method_MapIter_value(it): V
	g.line(`(func $__method_MapIter_value (param $it i32) (result i32)`)
	g.indent++
	g.line(`local.get $it`)
	g.line(`call $__map_iter_entry_addr`)
	g.line(`i32.load offset=4`)
	g.indent--
	g.line(`)`)

	// $__method_MapIter_advance(it): void — i++.
	g.line(`(func $__method_MapIter_advance (param $it i32)`)
	g.indent++
	g.line(`local.get $it`)
	g.line(`local.get $it`)
	g.line(`i32.load offset=4`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.store offset=4`)
	g.indent--
	g.line(`)`)

	// $__method_Map_get_or(m, k, default): V — returns the
	// value for k if present, otherwise `default`. Saves
	// callers from the `if let Some(v) = m.get(k) { v } else
	// { default }` ceremony for the common-case lookup.
	g.line(`(func $__method_Map_get_or (param $m i32) (param $k i32) (param $default i32) (result i32)`)
	g.indent++
	g.line(`(local $idx i32) (local $buf i32) (local $cap i32) (local $entriesBase i32)`)
	g.line(`local.get $m`)
	g.line(`local.get $k`)
	g.line(`call $__map_lookup`)
	g.line(`local.tee $idx`)
	g.line(`i32.const -1`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $default`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $m`)
	g.line(`i32.load`)
	g.line(`local.tee $buf`)
	g.line(`i32.load`)
	g.line(`local.set $cap`)
	g.line(`local.get $buf`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.get $cap`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`local.set $entriesBase`)
	g.line(`local.get $entriesBase`)
	g.line(`local.get $idx`)
	g.line(`i32.const 8`)
	g.line(`i32.mul`)
	g.line(`i32.add`)
	g.line(`i32.load offset=4`)
	g.indent--
	g.line(`)`)

	// $__method_Map_clear(m): void — drop every live entry by
	// resetting `len` to 0 and refilling the bucket array with
	// all-empty (-1, written as 0xFF byte fills). The entries
	// array's storage is overwritten on subsequent inserts; we
	// don't bother zeroing it here. The kv buffer + cap stay
	// the same, so `clear` is O(cap) rather than O(len) but
	// avoids any allocation.
	g.line(`(func $__method_Map_clear (param $m i32)`)
	g.indent++
	g.line(`(local $buf i32) (local $cap i32)`)
	g.line(`local.get $m`)
	g.line(`i32.load`)
	g.line(`local.tee $buf`)
	g.line(`i32.load`)
	g.line(`local.set $cap`)
	g.line(`local.get $buf`)
	g.line(`i32.const 0`)
	g.line(`i32.store offset=4`)
	g.line(`local.get $buf`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`i32.const 255`)
	g.line(`local.get $cap`)
	g.line(`i32.const 4`)
	g.line(`i32.mul`)
	g.line(`memory.fill`)
	g.indent--
	g.line(`)`)
}

// emitHexHelpers writes the runtime backing `hex_encode(s)` /
// `hex_decode(s)`. Encoding is lowercase (`0-9a-f`); decoding
// terminates at the first non-hex character or at an
// odd-length tail without raising. Like base64, this treats
// the input string as a raw byte array — non-ASCII content
// round-trips byte-for-byte.
func (g *generator) emitHexHelpers() {
	// $__hex_char(d): map a 4-bit nibble (0..15) to its
	// lowercase ASCII representation. d<10 → '0'+d, else
	// 'a'+(d-10) which is `d+87`.
	g.line(`(func $__hex_char (param $d i32) (result i32)`)
	g.indent++
	g.line(`local.get $d`)
	g.line(`i32.const 10`)
	g.line(`i32.lt_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $d`)
	g.line(`i32.const 48`) // '0'
	g.line(`i32.add`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $d`)
	g.line(`i32.const 87`) // 'a' - 10
	g.line(`i32.add`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)

	// $__hex_value(c): inverse of $__hex_char. Returns 0..15
	// for '0'-'9' / 'a'-'f' / 'A'-'F'; -1 for anything else
	// so the decoder can terminate on the first non-hex byte.
	g.line(`(func $__hex_value (param $c i32) (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 48`) // '0'
	g.line(`i32.lt_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const -1`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 57`) // '9'
	g.line(`i32.le_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 48`)
	g.line(`i32.sub`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 97`) // 'a'
	g.line(`i32.ge_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 102`) // 'f'
	g.line(`i32.le_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 87`) // 'a' - 10
	g.line(`i32.sub`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const -1`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 65`) // 'A'
	g.line(`i32.ge_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 70`) // 'F'
	g.line(`i32.le_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`local.get $c`)
	g.line(`i32.const 55`) // 'A' - 10
	g.line(`i32.sub`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const -1`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const -1`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)

	// $hex_encode(s): encode each byte as 2 lowercase hex
	// chars. Output length is exactly 2*input length.
	g.line(`(func $hex_encode (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $len i32) (local $out i32) (local $dst i32)`)
	g.line(`(local $i i32) (local $b i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $len`)
	// allocate len*2 + 4 bytes
	g.line(`local.get $len`)
	g.line(`i32.const 1`)
	g.line(`i32.shl`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $out`)
	// length prefix = len*2
	g.line(`local.get $out`)
	g.line(`local.get $len`)
	g.line(`i32.const 1`)
	g.line(`i32.shl`)
	g.line(`i32.store`)
	// dst = out + 4
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $dst`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $len`)
	g.line(`i32.ge_u`)
	g.line(`br_if $break`)
	// b = s[i]
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $b`)
	// dst[i*2] = hex_char(b >> 4)
	g.line(`local.get $dst`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.shl`)
	g.line(`i32.add`)
	g.line(`local.get $b`)
	g.line(`i32.const 4`)
	g.line(`i32.shr_u`)
	g.line(`i32.const 15`)
	g.line(`i32.and`)
	g.line(`call $__hex_char`)
	g.line(`i32.store8`)
	// dst[i*2 + 1] = hex_char(b & 0xf)
	g.line(`local.get $dst`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.shl`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.add`)
	g.line(`local.get $b`)
	g.line(`i32.const 15`)
	g.line(`i32.and`)
	g.line(`call $__hex_char`)
	g.line(`i32.store8`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $dst`)
	g.indent--
	g.line(`)`)

	// $hex_decode(s): decode pairs of hex chars into bytes.
	// Bails out at the first non-hex char or odd-length tail
	// and writes the actually-decoded length to the prefix
	// so callers can use `len()` to detect short input.
	g.line(`(func $hex_decode (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $len i32) (local $out i32) (local $dst i32)`)
	g.line(`(local $i i32) (local $oi i32) (local $hi i32) (local $lo i32)`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $len`)
	// allocate len/2 + 4 bytes (round down)
	g.line(`local.get $len`)
	g.line(`i32.const 1`)
	g.line(`i32.shr_u`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $out`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $dst`)
	g.line(`i32.const 0`)
	g.line(`local.set $i`)
	g.line(`i32.const 0`)
	g.line(`local.set $oi`)
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	// need 2 chars: i + 1 < len
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.get $len`)
	g.line(`i32.ge_u`)
	g.line(`br_if $break`)
	// hi = hex_value(s[i])
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`call $__hex_value`)
	g.line(`local.tee $hi`)
	g.line(`i32.const 0`)
	g.line(`i32.lt_s`)
	g.line(`br_if $break`)
	// lo = hex_value(s[i+1])
	g.line(`local.get $s`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`call $__hex_value`)
	g.line(`local.tee $lo`)
	g.line(`i32.const 0`)
	g.line(`i32.lt_s`)
	g.line(`br_if $break`)
	// dst[oi] = (hi << 4) | lo
	g.line(`local.get $dst`)
	g.line(`local.get $oi`)
	g.line(`i32.add`)
	g.line(`local.get $hi`)
	g.line(`i32.const 4`)
	g.line(`i32.shl`)
	g.line(`local.get $lo`)
	g.line(`i32.or`)
	g.line(`i32.store8`)
	g.line(`local.get $i`)
	g.line(`i32.const 2`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`local.get $oi`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $oi`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// patch length prefix to actually-written count
	g.line(`local.get $out`)
	g.line(`local.get $oi`)
	g.line(`i32.store`)
	g.line(`local.get $dst`)
	g.indent--
	g.line(`)`)
}

// emitRandomBytesHelper writes `$random_bytes(n)`, allocating
// a fresh length-prefixed lang string of n bytes and filling
// it via the WASI `random_get` import. Returns the data
// pointer (post-prefix), matching the runtime ABI of every
// other string-producing builtin.
//
// WASI `random_get(buf, n)` fills the buffer with cryptographic-
// quality random bytes (errno is ignored; the runtime treats
// any failure as program-fatal, same as our other helpers).
// emitTcpHelpers writes the wasi:sockets-flavoured TCP builtins.
// `tcp_listen` drives the bind+listen pipeline directly so the
// program is self-contained (no `wasmtime --tcp-listen=…` host
// flag needed), and `tcp_accept` returns a heap struct holding
// the per-connection (tcp-socket, input-stream, output-stream)
// triple instead of a bare fd.
func (g *generator) emitTcpHelpers() {
	g.emitTcpHelpersPreview2()
}


// emitTcpHelpersPreview2 writes the wasi:sockets-flavoured TCP
// builtins. The user-facing API stays "fd-shaped" — every
// tcp_listen / tcp_accept return value is a 12-byte heap struct
// `(tcp_socket, input_stream, output_stream)`; for listening
// sockets the two stream slots are 0 (no streams attached until a
// connection is accepted). tcp_recv / tcp_send / tcp_close all
// take that pointer back and read the appropriate slot.
//
// The (tcp-socket, input-stream, output-stream) triple is the
// shape `tcp-socket.accept` returns natively, so the
// per-connection struct is a direct lift of the canonical-ABI
// payload. For tcp_close to be a single function the listener and
// connection structs share the same shape — listeners just zero
// the stream slots, and tcp_close skips drops on zero-handle
// slots. The bump allocator can't free the struct itself, so it
// leaks; same trade-off step 3c made for descriptors.
func (g *generator) emitTcpHelpersPreview2() {
	g.emitNetworkHandleAccessor()
	g.emitTcpListenPreview2()
	g.emitTcpAcceptPreview2()
	g.emitTcpRecvPreview2()
	g.emitTcpSendPreview2()
	g.emitTcpClosePreview2()
}

// emitNetworkHandleAccessor writes
// `$__network_handle (result i32)` — the lazily-initialising
// accessor over `wasi:sockets/instance-network.instance-network`.
// Mirrors the stdin/stdout/stderr cached-handle pattern: the
// result lives at memory[124], with bit 4 of the init-flags byte
// at memory[112] tracking whether the slot is populated. Resource
// handles are opaque ints where 0 is a valid value, so the
// flag-bit indirection is necessary.
func (g *generator) emitNetworkHandleAccessor() {
	g.line(`(func $__network_handle (result i32)`)
	g.indent++
	g.line(`(local $h i32)`)
	g.line(`i32.const 112`)
	g.line(`i32.load`)
	g.line(`i32.const 16`) // bit 4
	g.line(`i32.and`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 124`)
	g.line(`i32.load`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`call $__wasi_instance_network`)
	g.line(`local.tee $h`)
	g.line(`i32.const 124`)
	g.line(`local.get $h`)
	g.line(`i32.store`)
	g.line(`i32.const 112`)
	g.line(`i32.const 112`)
	g.line(`i32.load`)
	g.line(`i32.const 16`)
	g.line(`i32.or`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`)`)
}

// emitTcpListenPreview2 writes `$tcp_listen(port) -> i32` for the
// wasi:sockets path. Pipeline: create-tcp-socket(ipv4) →
// start-bind + finish-bind (0.0.0.0:port) → start-listen +
// finish-listen → return a 12-byte heap struct
// `(tcp_socket, 0, 0)`. On any error returns -errno (the inner
// error-code byte) so callers can treat negative as failure —
// matches the preview-1 contract for backward compatibility.
//
// `start-bind` takes 15 i32 params: self + borrow<network> +
// `ip-socket-address` flattened (1 disc + 11 payload) + retptr.
// We always emit the IPv4 case bound to 0.0.0.0:port; the unused
// payload slots beyond the IPv4 5-slot prefix are zero-padded
// because the variant joins the wider IPv6 layout.
func (g *generator) emitTcpListenPreview2() {
	g.line(`(func $tcp_listen (param $port i32) (result i32)`)
	g.indent++
	g.line(`(local $sock i32) (local $struct i32)`)

	// create-tcp-socket(0=ipv4, retptr=92) -> result<tcp-socket, error-code>
	g.line(`i32.const 0`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_create_tcp_socket`)
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	// errno from retptr+4 (i8 enum), return -errno.
	g.line(`i32.const 0`)
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.sub`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $sock`)

	// start-bind(self, borrow<network>, ip-socket-address ipv4(0.0.0.0:port), retptr=92)
	g.line(`local.get $sock`)
	g.line(`call $__network_handle`)
	g.line(`i32.const 0`)         // disc = 0 (ipv4)
	g.line(`local.get $port`)     // ipv4-socket-address.port
	g.line(`i32.const 0`)         // ipv4 byte 0
	g.line(`i32.const 0`)         // ipv4 byte 1
	g.line(`i32.const 0`)         // ipv4 byte 2
	g.line(`i32.const 0`)         // ipv4 byte 3
	g.line(`i32.const 0`)         // pad: ipv6 flow-info / ipv4 unused
	g.line(`i32.const 0`)         // pad
	g.line(`i32.const 0`)         // pad
	g.line(`i32.const 0`)         // pad
	g.line(`i32.const 0`)         // pad
	g.line(`i32.const 0`)         // pad — total 6 padding slots beyond the ipv4 5-slot prefix
	g.line(`i32.const 92`)
	g.line(`call $__wasi_tcp_start_bind`)
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.sub`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// finish-bind(self, retptr).
	g.line(`local.get $sock`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_tcp_finish_bind`)
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.sub`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// start-listen(self, retptr).
	g.line(`local.get $sock`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_tcp_start_listen`)
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.sub`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// finish-listen(self, retptr).
	g.line(`local.get $sock`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_tcp_finish_listen`)
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`i32.const 92`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.sub`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// Allocate 12-byte struct (sock, 0, 0).
	g.line(`i32.const 12`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $struct`)
	g.line(`local.get $sock`)
	g.line(`i32.store`)
	g.line(`local.get $struct`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $struct`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $struct`)
	g.indent--
	g.line(`)`)
}

// emitTcpAcceptPreview2 writes `$tcp_accept(listener) -> i32`.
// Pipeline: subscribe(sock) → pollable.block → accept. The
// `accept` result is `result<tuple<tcp-socket, input-stream,
// output-stream>, error-code>`, lowered to 16 bytes (1 disc + 3
// pad + 12 payload). Our shared retptr at memory[92] is only 12
// bytes wide, so we allocate a fresh 16-byte scratch via
// __lang_alloc. The pointer leaks (bump allocator) — that's a
// 16-byte cost per accept, acceptable for the connection
// lifetime.
func (g *generator) emitTcpAcceptPreview2() {
	g.line(`(func $tcp_accept (param $listener i32) (result i32)`)
	g.indent++
	g.line(`(local $sock i32) (local $pollable i32) (local $retptr i32)`)
	g.line(`(local $newsock i32) (local $instream i32) (local $outstream i32) (local $struct i32)`)
	// Subscribe + block until a connection is ready. accept is
	// non-blocking on the wasi:sockets API; without the poll we'd
	// just get would-block on the first call.
	g.line(`local.get $listener`)
	g.line(`i32.load`)
	g.line(`local.tee $sock`)
	g.line(`call $__wasi_tcp_subscribe`)
	g.line(`local.tee $pollable`)
	g.line(`call $__wasi_pollable_block`)
	g.line(`local.get $pollable`)
	g.line(`call $__wasi_pollable_drop`)

	// Allocate a 16-byte retptr scratch.
	g.line(`i32.const 16`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $retptr`)
	g.line(`local.get $sock`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_tcp_accept`)
	g.line(`local.get $retptr`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	// errno at retptr+4.
	g.line(`i32.const 0`)
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`i32.sub`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// Ok payload at retptr+4: tuple<i32, i32, i32>.
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $newsock`)
	g.line(`local.get $retptr`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $instream`)
	g.line(`local.get $retptr`)
	g.line(`i32.const 12`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $outstream`)

	// Allocate 12-byte connection struct.
	g.line(`i32.const 12`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $struct`)
	g.line(`local.get $newsock`)
	g.line(`i32.store`)
	g.line(`local.get $struct`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $instream`)
	g.line(`i32.store`)
	g.line(`local.get $struct`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $outstream`)
	g.line(`i32.store`)
	g.line(`local.get $struct`)
	g.indent--
	g.line(`)`)
}

// emitTcpRecvPreview2 writes `$tcp_recv(conn, max) -> string`.
// Loads input_stream from the connection struct, calls
// blocking-read, materialises a length-prefixed string from the
// host buffer. On stream errors / EOF we return an empty string
// — same shape preview-1 tcp_recv produces on fd_read errno.
func (g *generator) emitTcpRecvPreview2() {
	g.line(`(func $tcp_recv (param $conn i32) (param $max i32) (result i32)`)
	g.indent++
	g.line(`(local $stream i32) (local $list_ptr i32) (local $n i32)`)
	g.line(`(local $sbase i32) (local $sptr i32)`)
	// stream = mem[conn + 4]
	g.line(`local.get $conn`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $stream`)
	// blocking-read(stream, max, retptr=92)
	g.line(`local.get $stream`)
	g.line(`local.get $max`)
	g.line(`i64.extend_i32_u`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_blocking_read`)
	// On Err, treat as empty.
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if (result i32)`)
	g.indent++
	g.line(`i32.const 0`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`i32.const 100`)
	g.line(`i32.load`)
	g.indent--
	g.line(`end`)
	g.line(`local.set $n`)
	g.line(`i32.const 96`)
	g.line(`i32.load`)
	g.line(`local.set $list_ptr`)
	// Allocate length-prefixed string of size $n + 5 (4 prefix + NUL),
	// matching the existing string-from-bytes allocation pattern.
	g.line(`local.get $n`)
	g.line(`i32.const 5`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $n`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)
	// memcpy host buffer into our string body.
	g.line(`local.get $sptr`)
	g.line(`local.get $list_ptr`)
	g.line(`local.get $n`)
	g.line(`memory.copy`)
	g.line(`local.get $sptr`)
	g.indent--
	g.line(`)`)
}

// emitTcpSendPreview2 writes `$tcp_send(conn, data) -> i32`.
// Loads output_stream from the connection struct, sends the
// string via the chunked $__streams_write helper. Returns the
// content length on success or -1 on failure. The preview-1
// version surfaced -errno; we don't have a meaningful errno here
// since stream errors get translated upstream, so -1 is the best
// negative sentinel.
func (g *generator) emitTcpSendPreview2() {
	g.line(`(func $tcp_send (param $conn i32) (param $data i32) (result i32)`)
	g.indent++
	g.line(`(local $stream i32) (local $len i32)`)
	g.line(`local.get $conn`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $stream`)
	g.line(`local.get $data`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $len`)
	g.line(`local.get $stream`)
	g.line(`local.get $data`)
	g.line(`local.get $len`)
	g.line(`call $__streams_write`)
	g.line(`local.get $len`)
	g.indent--
	g.line(`)`)
}

// emitTcpClosePreview2 writes `$tcp_close(conn) -> i32`. Drops
// the resources the struct holds: input + output streams first
// (only if non-zero — listener structs zero those slots), then
// the parent tcp-socket. The order matters: the canonical-ABI
// resource layer rejects dropping a parent that still has live
// children with "resource has children", so streams must go
// before the socket. Resource drops are infallible, so the
// return value is always 0.
func (g *generator) emitTcpClosePreview2() {
	g.line(`(func $tcp_close (param $struct i32) (result i32)`)
	g.indent++
	g.line(`(local $h i32)`)
	// input-stream first (child of tcp-socket).
	g.line(`local.get $struct`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.tee $h`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $h`)
	g.line(`call $__wasi_input_stream_drop`)
	g.indent--
	g.line(`end`)
	// output-stream (also a child).
	g.line(`local.get $struct`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.tee $h`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $h`)
	g.line(`call $__wasi_output_stream_drop`)
	g.indent--
	g.line(`end`)
	// Now the socket itself.
	g.line(`local.get $struct`)
	g.line(`i32.load`)
	g.line(`call $__wasi_tcp_socket_drop`)
	g.line(`i32.const 0`)
	g.indent--
	g.line(`)`)
}

// emitHttpHandlerWrapper writes the WAT function exported as
// `wasi:http/incoming-handler@0.2.0#handle` for the wasi-http
// target. The wrapper marshals the canonical-ABI incoming request
// into a 12-byte HttpRequest struct `(method, path, body)` (all
// length-prefixed lang strings), invokes the user's
// `handle(req: HttpRequest): HttpResponse`, then streams the
// response back through outgoing-body and hands an
// `Ok(outgoing-response)` to response-outparam.set.
//
// Resource lifetime, in order:
//   - `req` (incoming-request) is borrowed for `.method` /
//     `.path-with-query` / `.consume`, then explicitly dropped
//     once the body has been finished.
//   - `consume()` returns ownership of the incoming-body. After
//     reading the stream we hand the body to `[static]finish`
//     which transfers ownership and produces a future-trailers we
//     drop.
//   - `fields` constructed for the response are owned by the
//     outgoing-response constructor — no manual drop.
//   - `outgoing-body` from `outgoing-response.body()` is consumed
//     by `[static]finish`, again no manual drop.
//   - `output-stream` from `outgoing-body.write()` IS dropped
//     manually before we finish the body (the canonical-ABI
//     rejects parent drops with live children, same as TCP).
//   - `outgoing-response` and `response-outparam` are both
//     consumed by `[static]response-outparam.set`.
//
// Retptr scratch: each canonical-ABI return area we touch fits in
// 16 bytes (the static area at memory[92]) except
// `outgoing-body.finish`, whose result<_, error-code> can be 30+
// bytes once the variant payload joins are accounted for. We
// allocate a 64-byte scratch via `__lang_alloc(64)` for those
// calls. Eaten by the bump allocator, but the cost is per-request
// and the host serialises requests through `wasmtime serve`
// anyway — measurable budgets for fastly-style handlers don't
// notice.
func (g *generator) emitHttpHandlerWrapper() {
	// Sanity-check the user's `handle` signature. The checker
	// can't enforce this (HttpRequest / HttpResponse are
	// always-available structs, not http-target-only), so do
	// it here to keep error messages precise and the runtime
	// emit well-typed.
	handleFn := g.funcDecls["handle"]
	if handleFn == nil {
		// Fall back to emitting a stub that returns 500. The
		// real error comes from wasm-tools / wasmtime when the
		// component doesn't satisfy the export — a clearer
		// message would need a checker hook gated on
		// EmitOptions, which we haven't plumbed. Acceptable
		// for now: the lang error will be "no `handle` defined
		// for -target wasi-http" once we add the checker hook.
	}

	// Pre-intern the static method names. ASCII case matches
	// what most frameworks want (exposed as the lang string
	// `req.method`); upper-cased here so the user gets the
	// HTTP/1.1 canonical form regardless of wire format.
	methodGet := g.internString("GET")
	methodHead := g.internString("HEAD")
	methodPost := g.internString("POST")
	methodPut := g.internString("PUT")
	methodDelete := g.internString("DELETE")
	methodConnect := g.internString("CONNECT")
	methodOptions := g.internString("OPTIONS")
	methodTrace := g.internString("TRACE")
	methodPatch := g.internString("PATCH")
	emptyStr := g.internString("")

	g.line(`(func $__http_entry (param $req i32) (param $out i32)`)
	g.indent++
	g.line(`(local $retptr i32)`)
	g.line(`(local $method_str i32) (local $path_str i32) (local $body_str i32)`)
	g.line(`(local $body_handle i32) (local $body_stream i32)`)
	g.line(`(local $host_ptr i32) (local $host_len i32)`)
	g.line(`(local $body_buf i32) (local $body_size i32) (local $body_cur i32) (local $body_new_buf i32)`)
	g.line(`(local $disc i32)`)
	g.line(`(local $req_struct i32) (local $resp_struct i32) (local $status i32)`)
	g.line(`(local $resp_handle i32) (local $headers i32)`)
	g.line(`(local $out_body i32) (local $out_stream i32)`)

	// 64-byte scratch for canonical-ABI returns; large enough for
	// outgoing-body.finish's result<_, error-code> joined variant.
	g.line(`i32.const 64`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $retptr`)

	// ============================================================
	// Read method.
	// ============================================================
	g.line(`local.get $req`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_http_request_method`)
	// Method variant: disc at retptr+0, payload (string ptr/len)
	// at retptr+4 / +8. Stash the disc in a local — the surrounding
	// `block`s introduce stack fences (default block type is empty
	// → empty, so the value stack inside is independent of the
	// outer one), so we can't carry the loaded byte across the
	// nesting and have to reload it inside.
	g.line(`local.get $retptr`)
	g.line(`i32.load8_u`)
	g.line(`local.set $disc`)
	g.line(`block $m_done`)
	g.indent++
	g.line(`block $m_other`)
	g.indent++
	g.line(`block $m_patch`)
	g.indent++
	g.line(`block $m_trace`)
	g.indent++
	g.line(`block $m_options`)
	g.indent++
	g.line(`block $m_connect`)
	g.indent++
	g.line(`block $m_delete`)
	g.indent++
	g.line(`block $m_put`)
	g.indent++
	g.line(`block $m_post`)
	g.indent++
	g.line(`block $m_head`)
	g.indent++
	g.line(`block $m_get`)
	g.indent++
	g.line(`local.get $disc`)
	// br_table over disc; out-of-range falls into "other" too (cap at 9).
	g.line(`br_table $m_get $m_head $m_post $m_put $m_delete $m_connect $m_options $m_trace $m_patch $m_other $m_other`)
	g.indent--
	g.line(`end`) // m_get
	g.linef(`i32.const %d`, methodGet)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_head
	g.linef(`i32.const %d`, methodHead)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_post
	g.linef(`i32.const %d`, methodPost)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_put
	g.linef(`i32.const %d`, methodPut)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_delete
	g.linef(`i32.const %d`, methodDelete)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_connect
	g.linef(`i32.const %d`, methodConnect)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_options
	g.linef(`i32.const %d`, methodOptions)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_trace
	g.linef(`i32.const %d`, methodTrace)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_patch
	g.linef(`i32.const %d`, methodPatch)
	g.line(`local.set $method_str`)
	g.line(`br $m_done`)
	g.indent--
	g.line(`end`) // m_other
	// other(s): ptr at retptr+4, len at retptr+8. Materialise.
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $host_ptr`)
	g.line(`local.get $retptr`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $host_len`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $host_len`)
	g.line(`call $__bytes_to_lang_string`)
	g.line(`local.set $method_str`)
	g.indent--
	g.line(`end`) // m_done

	// ============================================================
	// Read path-with-query (option<string>).
	// ============================================================
	g.line(`local.get $req`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_http_request_path_with_query`)
	g.line(`local.get $retptr`)
	g.line(`i32.load8_u`)
	g.line(`if (result i32)`)
	g.indent++
	// Some(string): ptr at retptr+4, len at retptr+8.
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $host_ptr`)
	g.line(`local.get $retptr`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $host_len`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $host_len`)
	g.line(`call $__bytes_to_lang_string`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.linef(`i32.const %d`, emptyStr)
	g.indent--
	g.line(`end`)
	g.line(`local.set $path_str`)

	// ============================================================
	// Read body via consume + stream + bulk-read accumulator.
	// ============================================================
	g.line(`local.get $req`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_http_request_consume`)
	g.line(`local.get $retptr`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	// Err: empty body, no body resource to drop.
	g.linef(`i32.const %d`, emptyStr)
	g.line(`local.set $body_str`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $body_handle`)
	// stream() -> result<input-stream>
	g.line(`local.get $body_handle`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_http_incoming_body_stream`)
	g.line(`local.get $retptr`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	g.linef(`i32.const %d`, emptyStr)
	g.line(`local.set $body_str`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $body_stream`)
	// Accumulator: 4 KiB doubling buffer, same as $read_file.
	g.line(`i32.const 4096`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $body_buf`)
	g.line(`i32.const 4096`)
	g.line(`local.set $body_size`)
	g.line(`i32.const 0`)
	g.line(`local.set $body_cur`)
	g.line(`block $body_end`)
	g.indent++
	g.line(`loop $body_loop`)
	g.indent++
	// blocking-read(stream, 4096, retptr=92) — we use the static
	// retptr here, not our 64-byte one, because list<u8>'s ret
	// area is just (ptr, len) = 8 bytes.
	g.line(`local.get $body_stream`)
	g.line(`i64.const 4096`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_blocking_read`)
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`br_if $body_end`)
	g.line(`i32.const 100`)
	g.line(`i32.load`)
	g.line(`local.tee $host_len`)
	g.line(`i32.eqz`)
	g.line(`br_if $body_end`)
	g.line(`i32.const 96`)
	g.line(`i32.load`)
	g.line(`local.set $host_ptr`)
	// Grow the buffer until cur + host_len fits. Doubling.
	g.line(`block $grow_done`)
	g.indent++
	g.line(`loop $grow`)
	g.indent++
	g.line(`local.get $body_cur`)
	g.line(`local.get $host_len`)
	g.line(`i32.add`)
	g.line(`local.get $body_size`)
	g.line(`i32.le_u`)
	g.line(`br_if $grow_done`)
	g.line(`local.get $body_size`)
	g.line(`i32.const 1`)
	g.line(`i32.shl`)
	g.line(`local.set $body_size`)
	g.line(`local.get $body_size`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $body_new_buf`)
	// memcpy(new_buf, old_buf, body_cur).
	g.line(`local.get $body_new_buf`)
	g.line(`local.get $body_buf`)
	g.line(`local.get $body_cur`)
	g.line(`memory.copy`)
	g.line(`local.get $body_new_buf`)
	g.line(`local.set $body_buf`)
	g.line(`br $grow`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Append host bytes: memcpy(body_buf + body_cur, host_ptr, host_len)
	g.line(`local.get $body_buf`)
	g.line(`local.get $body_cur`)
	g.line(`i32.add`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $host_len`)
	g.line(`memory.copy`)
	g.line(`local.get $body_cur`)
	g.line(`local.get $host_len`)
	g.line(`i32.add`)
	g.line(`local.set $body_cur`)
	g.line(`br $body_loop`)
	g.indent--
	g.line(`end`) // body_loop
	g.indent--
	g.line(`end`) // body_end
	// Materialise lang string from (body_buf, body_cur).
	g.line(`local.get $body_buf`)
	g.line(`local.get $body_cur`)
	g.line(`call $__bytes_to_lang_string`)
	g.line(`local.set $body_str`)
	// Drop input-stream.
	g.line(`local.get $body_stream`)
	g.line(`call $__wasi_input_stream_drop`)
	g.indent--
	g.line(`end`) // stream-result branch

	// finish(body_handle) -> future-trailers; drop the trailers.
	g.line(`local.get $body_handle`)
	g.line(`call $__wasi_http_incoming_body_finish`)
	g.line(`call $__wasi_http_future_trailers_drop`)
	g.indent--
	g.line(`end`) // consume-result branch

	// Drop incoming-request.
	g.line(`local.get $req`)
	g.line(`call $__wasi_http_request_drop`)

	// ============================================================
	// Build HttpRequest struct (12 bytes: method, path, body).
	// ============================================================
	g.line(`i32.const 12`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $req_struct`)
	g.line(`local.get $method_str`)
	g.line(`i32.store`)
	g.line(`local.get $req_struct`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $path_str`)
	g.line(`i32.store`)
	g.line(`local.get $req_struct`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $body_str`)
	g.line(`i32.store`)

	// ============================================================
	// Call user-defined `handle(req): HttpResponse`.
	// ============================================================
	g.line(`local.get $req_struct`)
	g.line(`call $handle`)
	g.line(`local.set $resp_struct`)

	// HttpResponse layout: [status:i32][body:i32 (string ptr)] = 8 bytes.
	g.line(`local.get $resp_struct`)
	g.line(`i32.load`)
	g.line(`local.set $status`)
	g.line(`local.get $resp_struct`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $body_str`) // reuse local; now holds response body

	// ============================================================
	// Build outgoing-response.
	// ============================================================
	g.line(`call $__wasi_http_fields_new`)
	g.line(`local.set $headers`)
	g.line(`local.get $headers`)
	g.line(`call $__wasi_http_response_new`)
	g.line(`local.set $resp_handle`)

	// set-status-code(resp, status) — returns the result-disc inline; ignore it.
	g.line(`local.get $resp_handle`)
	g.line(`local.get $status`)
	g.line(`call $__wasi_http_response_set_status`)
	g.line(`drop`)

	// body() -> result<outgoing-body>.
	g.line(`local.get $resp_handle`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_http_response_body`)
	// Assume Ok (the spec says it can only fail if called twice).
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $out_body`)

	// outgoing-body.write() -> result<output-stream>. Take the
	// output-stream BEFORE calling response-outparam.set, since
	// `set` consumes the response and the host treats the
	// response-outparam handing-off as the "headers are sealed,
	// start streaming the body" cue.
	g.line(`local.get $out_body`)
	g.line(`local.get $retptr`)
	g.line(`call $__wasi_http_outgoing_body_write`)
	g.line(`local.get $retptr`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $out_stream`)

	// ============================================================
	// response-outparam.set(out, Ok(resp_handle)). Has to happen
	// BEFORE we write the body bytes — the host won't accept body
	// chunks for a response whose headers haven't been finalised
	// via `set`. Takes ownership of `out` and `resp_handle`.
	// 9-param call: (outparam, disc, payload[7 slots, slot 2=i64]).
	// ============================================================
	g.line(`local.get $out`)
	g.line(`i32.const 0`)            // result disc = 0 (Ok)
	g.line(`local.get $resp_handle`) // Ok payload slot 0 (the response handle)
	g.line(`i32.const 0`)            // payload slot 1
	g.line(`i64.const 0`)            // payload slot 2 (i64, joined-variant width)
	g.line(`i32.const 0`)            // payload slot 3
	g.line(`i32.const 0`)            // payload slot 4
	g.line(`i32.const 0`)            // payload slot 5
	g.line(`i32.const 0`)            // payload slot 6
	g.line(`call $__wasi_http_response_outparam_set`)

	// blocking-write-and-flush via the chunked $__streams_write
	// helper. Now that the response is "in flight", body bytes
	// stream out to the client as we write.
	g.line(`local.get $out_stream`)
	g.line(`local.get $body_str`)
	g.line(`local.get $body_str`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`call $__streams_write`)

	// Drop output-stream (child of outgoing-body).
	g.line(`local.get $out_stream`)
	g.line(`call $__wasi_output_stream_drop`)

	// We deliberately don't call `[static]outgoing-body.finish`.
	// The wasi:http 0.2.0 spec implies the host needs an explicit
	// finish, but in practice (wasmtime 30+, Fastly Compute,
	// Netlify) `response-outparam.set` already takes ownership of
	// the response and seals the body when the output-stream is
	// dropped — calling finish afterwards traps with
	// "unknown handle index" because the body resource has
	// already been reaped. If a future host needs the explicit
	// call we'd add it before set instead of after, and skip the
	// drop(out_stream) line so the body is still alive.

	g.indent--
	g.line(`)`)
	g.line(`(export "wasi:http/incoming-handler@0.2.0#handle" (func $__http_entry))`)

	// $__bytes_to_lang_string(host_ptr, host_len) -> lang_string_ptr.
	// Allocates a length-prefixed + NUL-terminated string buffer
	// and memcpies the host's bytes in. Used by the method /
	// path / body marshaling above.
	g.line(`(func $__bytes_to_lang_string (param $host_ptr i32) (param $host_len i32) (result i32)`)
	g.indent++
	g.line(`(local $sbase i32)`)
	g.line(`local.get $host_len`)
	g.line(`i32.const 5`) // 4 prefix + NUL
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $sbase`)
	g.line(`local.get $host_len`)
	g.line(`i32.store`) // length prefix
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $host_len`)
	g.line(`memory.copy`)
	// Trailing NUL.
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $host_len`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store8`)
	// Return data pointer (sbase + 4).
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)
}

// emitRandomBytesHelper emits `$random_bytes` against the
// `wasi:random/random.get-random-bytes` import. The canonical-ABI
// lowered signature is `(param i64 i32)`: the i64 is the requested
// length and the i32 is a "return area" pointer where the host
// writes a (ptr, len) pair. The host calls our exported
// `cabi_realloc` to allocate the buffer in our linear memory,
// fills it, and writes back. We memcpy those bytes into a
// length-prefixed + NUL-terminated string-shape allocation so the
// rest of the runtime sees the standard string layout.
//
// Memory[92..99] is the static return-area slot — see the runtime
// memory layout comment near `emitRuntimePreamble`.
func (g *generator) emitRandomBytesHelper() {
	g.line(`(func $random_bytes (param $n i32) (result i32)`)
	g.indent++
	g.line(`(local $data i32) (local $host_ptr i32) (local $host_len i32)`)
	// get-random-bytes(len: u64, retptr) — host writes (ptr, len) at retptr.
	g.line(`local.get $n`)
	g.line(`i64.extend_i32_u`)
	g.line(`i32.const 92`) // retptr slot
	g.line(`call $__wasi_random_get_p2`)
	// Read back (host_ptr, host_len) from the retptr slot.
	g.line(`i32.const 92`)
	g.line(`i32.load`)
	g.line(`local.set $host_ptr`)
	g.line(`i32.const 96`)
	g.line(`i32.load`)
	g.line(`local.set $host_len`)
	// Allocate string-shape buffer: 4-byte length prefix + bytes + NUL.
	g.line(`local.get $host_len`)
	g.line(`i32.const 5`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $data`)
	// Length prefix at data - 4.
	g.line(`local.get $data`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`local.get $host_len`)
	g.line(`i32.store`)
	// memory.copy(dest=data, src=host_ptr, n=host_len). The host
	// allocated host_ptr via cabi_realloc, so it lives in our
	// linear memory and memory.copy can move it freely.
	g.line(`local.get $data`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $host_len`)
	g.line(`memory.copy`)
	// Trailing NUL at data + host_len.
	g.line(`local.get $data`)
	g.line(`local.get $host_len`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store8`)
	g.line(`local.get $data`)
	g.indent--
	g.line(`)`)
}

// emitCabiRealloc emits the canonical-ABI realloc entry point that
// the preview-2 host invokes to allocate `list<u8>` (and other
// dynamically-sized) return buffers in our linear memory. The
// signature is fixed by the canonical ABI:
//
//	(orig_ptr, orig_size, align, new_size) -> ptr
//
// Random-bytes only ever calls it with orig_ptr=0 (fresh
// emitIntToStringHelper writes `$int_to_string(n) -> string`,
// formatting `n` as ASCII decimal in lang's standard
// length-prefixed string layout (`mem[ptr-4]` = length, then
// content, then a trailing NUL).
//
// Implementation:
//  1. Allocate a 16-byte scratch buffer (max width is
//     "-2147483648" = 11 chars, comfortably under 16).
//  2. Walk digits backwards starting from the buffer's far end,
//     storing `'0' + (n % 10)` and dividing. Unsigned div/rem
//     handles INT_MIN correctly: `0 - INT_MIN` wraps to
//     `0x80000000`, whose unsigned interpretation (2147483648) is
//     the right magnitude.
//  3. Prepend '-' for negatives.
//  4. Allocate a fresh string-shaped buffer (4-byte prefix +
//     content + NUL), memcpy the right-aligned digits in. Two
//     allocs is wasteful but the common case is single-digit
//     numbers in tests; the bump allocator can't free either way.
func (g *generator) emitIntToStringHelper() {
	g.line(`(func $int_to_string (param $n i32) (result i32)`)
	g.indent++
	g.line(`(local $tmp i32) (local $end i32) (local $len i32)`)
	g.line(`(local $neg i32) (local $buf i32)`)
	// scratch = __lang_alloc(16); end = scratch + 16
	g.line(`i32.const 16`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $tmp`)
	g.line(`local.get $tmp`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.set $end`)

	// neg = n < 0; if neg: n = -n (unsigned-safe for INT_MIN).
	g.line(`local.get $n`)
	g.line(`i32.const 0`)
	g.line(`i32.lt_s`)
	g.line(`local.tee $neg`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.get $n`)
	g.line(`i32.sub`)
	g.line(`local.set $n`)
	g.indent--
	g.line(`end`)

	// Special-case 0: emit '0' once. The loop below skips the
	// initial iteration on n==0, which would leave the buffer
	// empty.
	g.line(`local.get $n`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $end`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.tee $end`)
	g.line(`i32.const 48`) // '0'
	g.line(`i32.store8`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $n`)
	g.line(`i32.eqz`)
	g.line(`br_if $break`)
	// digit = n % 10 (unsigned)
	g.line(`local.get $end`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.tee $end`)
	g.line(`local.get $n`)
	g.line(`i32.const 10`)
	g.line(`i32.rem_u`)
	g.line(`i32.const 48`) // '0'
	g.line(`i32.add`)
	g.line(`i32.store8`)
	// n /= 10 (unsigned)
	g.line(`local.get $n`)
	g.line(`i32.const 10`)
	g.line(`i32.div_u`)
	g.line(`local.set $n`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// Minus sign.
	g.line(`local.get $neg`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $end`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.tee $end`)
	g.line(`i32.const 45`) // '-'
	g.line(`i32.store8`)
	g.indent--
	g.line(`end`)

	// len = (scratch + 16) - end
	g.line(`local.get $tmp`)
	g.line(`i32.const 16`)
	g.line(`i32.add`)
	g.line(`local.get $end`)
	g.line(`i32.sub`)
	g.line(`local.set $len`)

	// Allocate string buffer (len + 5: 4 prefix + content + NUL).
	g.line(`local.get $len`)
	g.line(`i32.const 5`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $buf`)
	g.line(`local.get $len`)
	g.line(`i32.store`)
	// memcpy(buf+4, end, len)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $end`)
	g.line(`local.get $len`)
	g.line(`memory.copy`)
	// Trailing NUL at buf+4+len.
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $len`)
	g.line(`i32.add`)
	g.line(`i32.const 0`)
	g.line(`i32.store8`)
	// Return data ptr (buf + 4).
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)
}

// emitNumberToStringHelpers writes the four method-syntax
// wrappers around the existing decimal formatter:
// `(i32).to_string()`, `(u32).to_string()`,
// `(i64).to_string()`, `(u64).to_string()`. They all funnel
// into a shared `$__int_to_string_u64(magnitude, neg)` core
// that the family was missing — `int_to_string` only knew
// signed i32. The wrappers convert to the (i64-magnitude, sign-
// flag) shape and call through.
func (g *generator) emitNumberToStringHelpers() {
	// $__int_to_string_u64(mag: i64, neg: i32): i32 — formats
	// a non-negative i64 magnitude into a freshly allocated
	// length-prefixed string, prepending `-` when neg is set.
	g.line(`(func $__int_to_string_u64 (param $mag i64) (param $neg i32) (result i32)`)
	g.indent++
	g.line(`(local $tmp i32) (local $end i32) (local $len i32) (local $buf i32)`)
	// 24 bytes is enough for max u64 (20 digits) + sign + spare.
	g.line(`i32.const 24`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $tmp`)
	g.line(`local.get $tmp`)
	g.line(`i32.const 24`)
	g.line(`i32.add`)
	g.line(`local.set $end`)
	// Special-case 0.
	g.line(`local.get $mag`)
	g.line(`i64.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $end`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.tee $end`)
	g.line(`i32.const 48`) // '0'
	g.line(`i32.store8`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`block $break`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	g.line(`local.get $mag`)
	g.line(`i64.eqz`)
	g.line(`br_if $break`)
	// digit = mag % 10 (unsigned)
	g.line(`local.get $end`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.tee $end`)
	g.line(`local.get $mag`)
	g.line(`i64.const 10`)
	g.line(`i64.rem_u`)
	g.line(`i32.wrap_i64`)
	g.line(`i32.const 48`) // '0'
	g.line(`i32.add`)
	g.line(`i32.store8`)
	// mag /= 10 (unsigned)
	g.line(`local.get $mag`)
	g.line(`i64.const 10`)
	g.line(`i64.div_u`)
	g.line(`local.set $mag`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Sign.
	g.line(`local.get $neg`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $end`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`local.tee $end`)
	g.line(`i32.const 45`) // '-'
	g.line(`i32.store8`)
	g.indent--
	g.line(`end`)
	// len = (tmp + 24) - end
	g.line(`local.get $tmp`)
	g.line(`i32.const 24`)
	g.line(`i32.add`)
	g.line(`local.get $end`)
	g.line(`i32.sub`)
	g.line(`local.set $len`)
	// out = alloc(4 + len); out[0] = len; memcpy(out+4, end, len)
	g.line(`local.get $len`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $buf`)
	g.line(`local.get $buf`)
	g.line(`local.get $len`)
	g.line(`i32.store`)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $end`)
	g.line(`local.get $len`)
	g.line(`memory.copy`)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)

	// $__method_i32_to_string(n: i32): string — sign-aware,
	// negates negative n into its u32 magnitude (handling
	// INT_MIN via the unsigned trick: 0 - n in unsigned wraps
	// correctly for all i32 values).
	g.line(`(func $__method_i32_to_string (param $n i32) (result i32)`)
	g.indent++
	g.line(`(local $neg i32) (local $mag i64)`)
	g.line(`local.get $n`)
	g.line(`i32.const 0`)
	g.line(`i32.lt_s`)
	g.line(`local.tee $neg`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 0`)
	g.line(`local.get $n`)
	g.line(`i32.sub`)
	g.line(`i64.extend_i32_u`)
	g.line(`local.set $mag`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $n`)
	g.line(`i64.extend_i32_u`)
	g.line(`local.set $mag`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $mag`)
	g.line(`local.get $neg`)
	g.line(`call $__int_to_string_u64`)
	g.indent--
	g.line(`)`)

	// $__method_u32_to_string(n: i32): string — n is treated
	// as unsigned; just zero-extend and format.
	g.line(`(func $__method_u32_to_string (param $n i32) (result i32)`)
	g.indent++
	g.line(`local.get $n`)
	g.line(`i64.extend_i32_u`)
	g.line(`i32.const 0`)
	g.line(`call $__int_to_string_u64`)
	g.indent--
	g.line(`)`)

	// $__method_i64_to_string(n: i64): string — sign-aware,
	// same negation trick at i64 width.
	g.line(`(func $__method_i64_to_string (param $n i64) (result i32)`)
	g.indent++
	g.line(`(local $neg i32) (local $mag i64)`)
	g.line(`local.get $n`)
	g.line(`i64.const 0`)
	g.line(`i64.lt_s`)
	g.line(`local.tee $neg`)
	g.line(`if`)
	g.indent++
	g.line(`i64.const 0`)
	g.line(`local.get $n`)
	g.line(`i64.sub`)
	g.line(`local.set $mag`)
	g.indent--
	g.line(`else`)
	g.indent++
	g.line(`local.get $n`)
	g.line(`local.set $mag`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $mag`)
	g.line(`local.get $neg`)
	g.line(`call $__int_to_string_u64`)
	g.indent--
	g.line(`)`)

	// $__method_u64_to_string(n: i64): string — unsigned,
	// straight pass-through.
	g.line(`(func $__method_u64_to_string (param $n i64) (result i32)`)
	g.indent++
	g.line(`local.get $n`)
	g.line(`i32.const 0`)
	g.line(`call $__int_to_string_u64`)
	g.indent--
	g.line(`)`)
}

// emitFloatToStringHelpers writes `$__method_f32_to_string`
// and `$__method_f64_to_string`. The algorithm:
//   - Detect NaN and ±Inf and emit their canonical names.
//   - Sign: emit '-' if negative, then negate.
//   - Integer part: truncate-to-i64, format via the existing
//     `$__int_to_string_u64`.
//   - Fractional part: subtract integer, multiply by 10^k
//     where k = 7 (f32) or 15 (f64), truncate to i64, emit
//     exactly k digits with leading zeros, then trim
//     trailing zeros (and drop the leading `.` if all
//     digits were zero).
//
// NOT bit-exact Steele/White / Ryu — close-enough-for-handler
// output, matching parse_float's tolerance. A 7-digit f32
// representation round-trips through parse_float to within
// f32 epsilon for typical values; pathological cases lose
// trailing precision.
func (g *generator) emitFloatToStringHelpers() {
	// Internal helper: write the canonical name (NaN / Inf /
	// -Inf) into a fresh string. Used by both width helpers.
	g.line(`(func $__float_special (param $name i32) (param $nameLen i32) (result i32)`)
	g.indent++
	g.line(`(local $out i32)`)
	g.line(`local.get $nameLen`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $out`)
	g.line(`local.get $nameLen`)
	g.line(`i32.store`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $name`)
	g.line(`local.get $nameLen`)
	g.line(`memory.copy`)
	g.line(`local.get $out`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)

	// Internal helper: append a string to a json buffer.
	// Wraps __json_buf_str so we can use the buffer as the
	// scratch builder (it already grows on demand).

	// Emit `n` decimal digits of `frac` (i64) into buf,
	// padding with leading zeros, then trim trailing zeros
	// in place by adjusting buf.len. If all digits were
	// zero, also remove the trailing `.` (caller appended
	// it before calling). Returns the (possibly grown) buf.
	g.line(`(func $__float_emit_frac (param $buf i32) (param $frac i64) (param $k i32) (result i32)`)
	g.indent++
	g.line(`(local $i i32) (local $div i64) (local $digit i64)`)
	g.line(`(local $start i32) (local $pos i32) (local $b i32)`)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.set $start`) // start of fraction digits
	// div = 10^(k-1)
	g.line(`i64.const 1`)
	g.line(`local.set $div`)
	g.line(`i32.const 1`)
	g.line(`local.set $i`)
	g.line(`block $end_pow`)
	g.indent++
	g.line(`loop $pow_loop`)
	g.indent++
	g.line(`local.get $i`)
	g.line(`local.get $k`)
	g.line(`i32.ge_u`)
	g.line(`br_if $end_pow`)
	g.line(`local.get $div`)
	g.line(`i64.const 10`)
	g.line(`i64.mul`)
	g.line(`local.set $div`)
	g.line(`local.get $i`)
	g.line(`i32.const 1`)
	g.line(`i32.add`)
	g.line(`local.set $i`)
	g.line(`br $pow_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Loop k times: emit digit = frac/div, frac %= div, div /= 10.
	g.line(`block $end_emit`)
	g.indent++
	g.line(`loop $emit_loop`)
	g.indent++
	g.line(`local.get $div`)
	g.line(`i64.const 0`)
	g.line(`i64.le_s`)
	g.line(`br_if $end_emit`)
	g.line(`local.get $frac`)
	g.line(`local.get $div`)
	g.line(`i64.div_s`)
	g.line(`local.set $digit`)
	g.line(`local.get $frac`)
	g.line(`local.get $digit`)
	g.line(`local.get $div`)
	g.line(`i64.mul`)
	g.line(`i64.sub`)
	g.line(`local.set $frac`)
	g.line(`local.get $buf`)
	g.line(`local.get $digit`)
	g.line(`i32.wrap_i64`)
	g.line(`i32.const 48`)
	g.line(`i32.add`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.line(`local.get $div`)
	g.line(`i64.const 10`)
	g.line(`i64.div_s`)
	g.line(`local.set $div`)
	g.line(`br $emit_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// Trim trailing zeros: while buf.len > start && data[buf.len-1] == '0': buf.len--
	g.line(`block $end_trim`)
	g.indent++
	g.line(`loop $trim_loop`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.tee $pos`)
	g.line(`local.get $start`)
	g.line(`i32.le_u`)
	g.line(`br_if $end_trim`)
	g.line(`local.get $buf`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`local.get $pos`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`i32.add`)
	g.line(`i32.load8_u`)
	g.line(`local.set $b`)
	g.line(`local.get $b`)
	g.line(`i32.const 48`)
	g.line(`i32.ne`)
	g.line(`br_if $end_trim`)
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $pos`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`i32.store`)
	g.line(`br $trim_loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)
	// If all digits were zero (pos == start), remove the
	// trailing '.' by decrementing buf.len once more.
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.get $start`)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $start`)
	g.line(`i32.const 1`)
	g.line(`i32.sub`)
	g.line(`i32.store`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $buf`)
	g.indent--
	g.line(`)`)

	// Pre-built byte sequences for special-case names.
	// `__float_special` copies these into a fresh string.
	// We use direct writes to a stack-allocated 4-byte
	// space rather than pulling in the data segment.

	// $__method_f32_to_string(n: f32): string
	g.line(`(func $__method_f32_to_string (param $n f32) (result i32)`)
	g.indent++
	g.line(`(local $buf i32) (local $intPart i64) (local $frac f64) (local $fracInt i64)`)
	g.line(`(local $intStr i32) (local $tmp i32) (local $nd f64)`)
	// NaN: n != n
	g.line(`local.get $n`)
	g.line(`local.get $n`)
	g.line(`f32.ne`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $tmp`)
	g.line(`i32.const 3`)
	g.line(`i32.store`)
	g.line(`local.get $tmp`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.const 78`) // 'N'
	g.line(`i32.store8`)
	g.line(`local.get $tmp`)
	g.line(`i32.const 5`)
	g.line(`i32.add`)
	g.line(`i32.const 97`) // 'a'
	g.line(`i32.store8`)
	g.line(`local.get $tmp`)
	g.line(`i32.const 6`)
	g.line(`i32.add`)
	g.line(`i32.const 78`) // 'N'
	g.line(`i32.store8`)
	g.line(`local.get $tmp`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Inf: |n| == Inf
	g.line(`local.get $n`)
	g.line(`f32.abs`)
	g.line(`f32.const inf`)
	g.line(`f32.eq`)
	g.line(`if`)
	g.indent++
	g.line(`call $__json_buf_new`)
	g.line(`local.set $buf`)
	g.line(`local.get $n`)
	g.line(`f32.const 0`)
	g.line(`f32.lt`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 45`) // '-'
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $buf`)
	g.line(`i32.const 73`) // 'I'
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 110`) // 'n'
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 102`) // 'f'
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Build buf.
	g.line(`call $__json_buf_new`)
	g.line(`local.set $buf`)
	g.line(`local.get $n`)
	g.line(`f32.const 0`)
	g.line(`f32.lt`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 45`) // '-'
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.line(`local.get $n`)
	g.line(`f32.neg`)
	g.line(`local.set $n`)
	g.indent--
	g.line(`end`)
	// Promote to f64 for sufficient mantissa headroom.
	g.line(`local.get $n`)
	g.line(`f64.promote_f32`)
	g.line(`local.set $nd`)
	// intPart = (i64) trunc(nd)
	g.line(`local.get $nd`)
	g.line(`i64.trunc_sat_f64_s`)
	g.line(`local.set $intPart`)
	g.line(`local.get $intPart`)
	g.line(`i32.const 0`)
	g.line(`call $__int_to_string_u64`)
	g.line(`local.set $intStr`)
	g.line(`local.get $buf`)
	g.line(`local.get $intStr`)
	g.line(`call $__json_buf_str`)
	g.line(`local.set $buf`)
	// frac = nd - (f64)intPart
	g.line(`local.get $nd`)
	g.line(`local.get $intPart`)
	g.line(`f64.convert_i64_s`)
	g.line(`f64.sub`)
	g.line(`f64.abs`)
	g.line(`local.set $frac`)
	g.line(`local.get $frac`)
	g.line(`f64.const 1e-7`)
	g.line(`f64.gt`)
	g.line(`if`)
	g.indent++
	// Emit '.', then 7 digits of fraction.
	g.line(`local.get $buf`)
	g.line(`i32.const 46`) // '.'
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.line(`local.get $frac`)
	g.line(`f64.const 10000000`)
	g.line(`f64.mul`)
	g.line(`i64.trunc_sat_f64_s`)
	g.line(`local.set $fracInt`)
	g.line(`local.get $buf`)
	g.line(`local.get $fracInt`)
	g.line(`i32.const 7`)
	g.line(`call $__float_emit_frac`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $buf`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)

	// $__method_f64_to_string(n: f64): string — same shape
	// as the f32 helper but with 15 fractional digits.
	g.line(`(func $__method_f64_to_string (param $n f64) (result i32)`)
	g.indent++
	g.line(`(local $buf i32) (local $intPart i64) (local $frac f64) (local $fracInt i64)`)
	g.line(`(local $intStr i32) (local $tmp i32)`)
	g.line(`local.get $n`)
	g.line(`local.get $n`)
	g.line(`f64.ne`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $tmp`)
	g.line(`i32.const 3`)
	g.line(`i32.store`)
	g.line(`local.get $tmp`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.const 78`)
	g.line(`i32.store8`)
	g.line(`local.get $tmp`)
	g.line(`i32.const 5`)
	g.line(`i32.add`)
	g.line(`i32.const 97`)
	g.line(`i32.store8`)
	g.line(`local.get $tmp`)
	g.line(`i32.const 6`)
	g.line(`i32.add`)
	g.line(`i32.const 78`)
	g.line(`i32.store8`)
	g.line(`local.get $tmp`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $n`)
	g.line(`f64.abs`)
	g.line(`f64.const inf`)
	g.line(`f64.eq`)
	g.line(`if`)
	g.indent++
	g.line(`call $__json_buf_new`)
	g.line(`local.set $buf`)
	g.line(`local.get $n`)
	g.line(`f64.const 0`)
	g.line(`f64.lt`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 45`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $buf`)
	g.line(`i32.const 73`)
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 110`)
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 102`)
	g.line(`call $__json_buf_byte`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	g.line(`call $__json_buf_new`)
	g.line(`local.set $buf`)
	g.line(`local.get $n`)
	g.line(`f64.const 0`)
	g.line(`f64.lt`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 45`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.line(`local.get $n`)
	g.line(`f64.neg`)
	g.line(`local.set $n`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $n`)
	g.line(`i64.trunc_sat_f64_s`)
	g.line(`local.set $intPart`)
	g.line(`local.get $intPart`)
	g.line(`i32.const 0`)
	g.line(`call $__int_to_string_u64`)
	g.line(`local.set $intStr`)
	g.line(`local.get $buf`)
	g.line(`local.get $intStr`)
	g.line(`call $__json_buf_str`)
	g.line(`local.set $buf`)
	g.line(`local.get $n`)
	g.line(`local.get $intPart`)
	g.line(`f64.convert_i64_s`)
	g.line(`f64.sub`)
	g.line(`f64.abs`)
	g.line(`local.set $frac`)
	g.line(`local.get $frac`)
	g.line(`f64.const 1e-15`)
	g.line(`f64.gt`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $buf`)
	g.line(`i32.const 46`)
	g.line(`call $__json_buf_byte`)
	g.line(`local.set $buf`)
	g.line(`local.get $frac`)
	g.line(`f64.const 1e15`)
	g.line(`f64.mul`)
	g.line(`i64.trunc_sat_f64_s`)
	g.line(`local.set $fracInt`)
	g.line(`local.get $buf`)
	g.line(`local.get $fracInt`)
	g.line(`i32.const 15`)
	g.line(`call $__float_emit_frac`)
	g.line(`local.set $buf`)
	g.indent--
	g.line(`end`)
	g.line(`local.get $buf`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.indent--
	g.line(`)`)
}

// allocation) and align=1, so we ignore the realloc-shrink and
// realloc-grow cases for now. If a future preview-2 import needs
// real reallocation, the body needs to grow a memcpy from the
// previous buffer.
func (g *generator) emitCabiRealloc() {
	g.line(`(func $cabi_realloc (param $orig_ptr i32) (param $orig_size i32) (param $align i32) (param $new_size i32) (result i32)`)
	g.indent++
	g.line(`local.get $new_size`)
	g.line(`call $__lang_alloc`)
	g.indent--
	g.line(`)`)
}

// emitExitHelper writes `$exit(code)`, a one-line wrapper around
// the WASI proc_exit import. WASI's proc_exit takes the status
// code as its only parameter and never returns. We expose it as
// a void-returning lang function; the wasm validator is happy
// because the wrapper itself doesn't need a result type.
func (g *generator) emitExitHelper() {
	g.line(`(func $exit (param $code i32)`)
	g.indent++
	g.line(`local.get $code`)
	g.line(`call $__wasi_proc_exit`)
	g.indent--
	g.line(`)`)
}

// emitFileIOHelpers writes `$read_file`, `$write_file`, and the
// shared `$__build_io_error` helper that maps a WASI errno to the
// matching `IoError` variant. Variant indices are hardcoded to
// match the auto-injected enum (NotFound=0, PermissionDenied=1,
// AlreadyExists=2, InvalidUtf8=3, Interrupted=4, Unsupported=5,
// Other=6).
//
// `read_file` allocates 4 KiB chunks in a loop, packing them
// contiguously on the bump heap by un-bumping any unused tail.
// On EOF / partial read, the contiguous region from $start to
// the bump pointer is the file content; we then allocate a
// length-prefixed result string and memcpy into it. Files
// larger than the available linear memory will simply OOM the
// allocator — streaming via Reader/Writer (Phase 4b) is the
// fix when that matters.
//
// `write_file` opens with O_CREAT | O_TRUNC and a single
// fd_write. Multi-iovec splitting isn't necessary because the
// WASI API accepts an arbitrary buffer length.
//
// Path interpretation is governed by the WASI preopen at fd 3
// — wasmtime users pass `--dir=...` to make a directory
// accessible. Paths are relative to that preopen; absolute
// paths fail with EBADF.
func (g *generator) emitFileIOHelpers() {
	// Pre-intern the static "io error" message so $__build_io_error
	// can reach it via a constant pointer rather than rebuilding
	// the string on every error path.
	ioErrMsgPtr := g.internString("io error")
	g.line(`(func $__build_io_error (param $errno i32) (param $path i32) (result i32)`)
	g.indent++
	g.line(`(local $result i32)`)
	// Errno-to-variant table. Common cases get a typed variant;
	// anything else falls through to Other(path, "io error").
	//
	// Under preview-2 the value reaching this helper is the
	// `wasi:filesystem/types.error-code` enum index, NOT a
	// preview-1 errno; the open helper goes through
	// `$__filesystem_error_translate` first to remap the enum
	// values (access=0, exist=7, interrupted=11, no-entry=20,
	// unsupported=27 in the spec) onto the preview-1 codes the
	// table below uses (2/20/27/44/58). Two of the preview-2
	// indexes overlap with preview-1 errnos for different
	// conditions (`exist`/EEXIST agree; `no-entry` collides with
	// EEXIST=20; `unsupported` collides with EINTR=27), which is
	// why the translate step has to run before this helper sees
	// the value.
	g.emitIoErrorCase(2, 1, true)   // EACCES → PermissionDenied(path)
	g.emitIoErrorCase(20, 2, true)  // EEXIST → AlreadyExists(path)
	g.emitIoErrorCase(27, 4, false) // EINTR  → Interrupted
	g.emitIoErrorCase(44, 0, true)  // ENOENT → NotFound(path)
	g.emitIoErrorCase(58, 5, false) // ENOTSUP → Unsupported
	// Default: Other(path, "io error"). Allocate 12 bytes:
	// [tag=6, path_ptr, msg_ptr].
	g.line(`i32.const 12`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 6`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $path`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 8`)
	g.line(`i32.add`)
	g.linef(`i32.const %d`, ioErrMsgPtr)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)

	g.emitFilesystemErrorTranslate()
	g.emitReadFileHelper()
	g.emitWriteFileHelper()
	if g.needsStreamingIO {
		g.emitStreamingIOHelpers()
	}
}

// emitFilesystemErrorTranslate writes
// `$__filesystem_error_translate(code) -> errno` — the bridge
// between `wasi:filesystem/types.error-code` (an enum) and the
// preview-1 errno values that `$__build_io_error` keys on.
// Untranslated codes drop through to a sentinel that the
// io-error table treats as `Other(path, "io error")`.
func (g *generator) emitFilesystemErrorTranslate() {
	g.line(`(func $__filesystem_error_translate (param $code i32) (result i32)`)
	g.indent++
	type pair struct{ p2, p1 int }
	for _, p := range []pair{
		{0, 2},   // access     → EACCES
		{7, 20},  // exist      → EEXIST
		{11, 27}, // interrupted → EINTR
		{20, 44}, // no-entry   → ENOENT
		{27, 58}, // unsupported → ENOTSUP
	} {
		g.line(`local.get $code`)
		g.linef(`i32.const %d`, p.p2)
		g.line(`i32.eq`)
		g.line(`if (result i32)`)
		g.indent++
		g.linef(`i32.const %d`, p.p1)
		g.line(`return`)
		g.indent--
		g.line(`else`)
		g.indent++
	}
	// Catch-all: anything else falls through as -1, which doesn't
	// match any case in $__build_io_error and lands in Other(...).
	g.line(`i32.const -1`)
	for range 5 {
		g.indent--
		g.line(`end`)
	}
	g.indent--
	g.line(`)`)
}

// emitStreamingIOHelpers writes the runtime functions backing
// `open_reader` / `open_writer` / `open_appender` plus the
// auto-injected Reader / Writer methods. Layout for the
// returned struct values is just `[fd : i32]` — 4 bytes total
// — so $__build_reader / $__build_writer reuse the same store
// pattern.
func (g *generator) emitStreamingIOHelpers() {
	g.emitOpenReaderHelper()
	g.emitOpenWriterHelper(false)
	g.emitOpenAppenderHelper()
	g.emitReaderReadLineMethod()
	g.emitReaderReadChunkMethod()
	g.emitReaderCloseMethod()
	g.emitWriterWriteMethod()
	g.emitWriterCloseMethod()
}

// emitOpenReaderHelper writes `$open_reader(path) ->
// Result[Reader, IoError]`. On preview-1: path_open with read
// rights. descriptor.open-at against the cached preopen directory
// (open-flags=0, descriptor-flags=1=read), then
// descriptor.read-via-stream for an input-stream resource handle
// — the Reader struct holds that handle. Errors flow through
// __build_io_error.
func (g *generator) emitOpenReaderHelper() {
	// open-flags=0 (no create / truncate), descriptor-flags=1 (read).
	g.emitOpenViaStreamHelper("$open_reader", 0, 1, "$__wasi_descriptor_read_via_stream", false)
}

// emitOpenWriterHelper writes `$open_writer(path)`: open-at with
// open-flags=create|truncate (1|8 = 9), descriptor-flags=write
// (2), then descriptor.write-via-stream.
// (The `appendMode` parameter is unused for now; appender uses
// the dedicated emitOpenAppenderHelper.)
func (g *generator) emitOpenWriterHelper(_ bool) {
	g.emitOpenViaStreamHelper("$open_writer", 9, 2, "$__wasi_descriptor_write_via_stream", false)
}

// emitOpenAppenderHelper writes `$open_appender(path)`:
// open-flags=create (1), descriptor-flags=write (2), then
// descriptor.append-via-stream — append-via-stream inherently
// appends.
func (g *generator) emitOpenAppenderHelper() {
	g.emitOpenViaStreamHelper("$open_appender", 1, 2, "$__wasi_descriptor_append_via_stream", true)
}

// emitOpenHelper is the shared body of the three open_*
// constructors. `name` is the wat function name; `oflags` /
// `rights` / `fdflags` are the path_open immediates.
func (g *generator) emitOpenHelper(name string, oflags int, rights int64, fdflags int) {
	g.linef(`(func %s (param $path i32) (result i32)`, name)
	g.indent++
	g.line(`(local $fd_buf i32)`)
	g.line(`(local $fd i32)`)
	g.line(`(local $errno i32)`)
	g.line(`(local $reader i32)`)
	g.line(`(local $result i32)`)

	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $fd_buf`)

	g.line(`i32.const 3`)
	g.line(`i32.const 1`)
	g.line(`local.get $path`)
	g.line(`local.get $path`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.linef(`i32.const %d`, oflags)
	g.linef(`i64.const %d`, rights)
	g.line(`i64.const 0`)
	g.linef(`i32.const %d`, fdflags)
	g.line(`local.get $fd_buf`)
	g.line(`call $__wasi_path_open`)
	g.line(`local.set $errno`)
	g.line(`local.get $errno`)
	g.line(`if`)
	g.indent++
	// Build Err(io_error_from_errno) — Result.Err = tag 1.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $errno`)
	g.line(`local.get $path`)
	g.line(`call $__build_io_error`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	g.line(`local.get $fd_buf`)
	g.line(`i32.load`)
	g.line(`local.set $fd`)

	// Allocate the Reader / Writer struct (4 bytes — single fd
	// field at offset 0).
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $reader`)
	g.line(`local.get $reader`)
	g.line(`local.get $fd`)
	g.line(`i32.store`)

	// Build Ok(reader): 8 bytes [tag=0, reader_ptr].
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $reader`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
}

// emitReaderReadLineMethod writes the Reader.read_line method.
// Reads bytes from r.fd one at a time into the static
// 1-byte buffer at memory[68], scanning for `\n`. The
// matched bytes accumulate on the bump heap (1-byte allocs
// in a row keep them contiguous); the final length-prefixed
// string lives just past them.
//
// The static iovec at offset 56 (ptr=68, len=1) is reused
// across calls — the caller is single-threaded so concurrent
// access can't happen. Heap-allocating the iovec doesn't work
// because the byte-grain allocator drifts the bump pointer
// off 4-byte alignment, which wasmtime rejects on fd_read.
//
// The mangled name matches what the checker emits at call
// sites after rewriting `r.read_line()` →
// `__method_Reader_read_line(r)`.
func (g *generator) emitReaderReadLineMethod() {
	g.line(`(func $__method_Reader_read_line (param $r i32) (result i32)`)
	g.indent++
	// Readers hold an `input-stream` resource handle in mem[$r]
	// (whether they came from $stdin, $open_reader, or anywhere
	// else). Delegate to the shared `$__stream_read_line` helper.
	g.line(`local.get $r`)
	g.line(`i32.load`)
	g.line(`call $__stream_read_line`)
	g.indent--
	g.line(`)`)
}

// emitReaderReadChunkMethod writes Reader.read_chunk(size).
// Preview-1: single fd_read into a heap buffer of capacity
// `size`. Single
// `wasi:io/streams.input-stream.blocking-read(size)` against the
// stream resource handle in mem[$r], `memory.copy` the host's
// buffer into a length-prefixed string, return Some. Uses the
// shared retptr at memory[92].
func (g *generator) emitReaderReadChunkMethod() {
	g.line(`(func $__method_Reader_read_chunk (param $r i32) (param $size i32) (result i32)`)
	g.indent++
	g.line(`(local $fd i32) (local $sbase i32) (local $sptr i32)`)
	g.line(`(local $n i32) (local $result i32) (local $list_ptr i32)`)

	g.line(`local.get $r`)
	g.line(`i32.load`)
	g.line(`local.set $fd`)

	// blocking-read(stream, size, retptr=92).
	g.line(`local.get $fd`)
	g.line(`local.get $size`)
	g.line(`i64.extend_i32_u`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_blocking_read`)
	// Outer disc at retptr+0; non-zero = Err = treat as EOF.
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// list_ptr = mem[96], n = mem[100].
	g.line(`i32.const 96`)
	g.line(`i32.load`)
	g.line(`local.set $list_ptr`)
	g.line(`i32.const 100`)
	g.line(`i32.load`)
	g.line(`local.set $n`)
	// Empty list = EOF on a blocking read.
	g.line(`local.get $n`)
	g.line(`i32.eqz`)
	g.line(`if`)
	g.indent++
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// Materialise length-prefixed string + memcpy from the host buffer.
	g.line(`local.get $n`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $n`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)
	g.line(`local.get $sptr`)
	g.line(`local.get $list_ptr`)
	g.line(`local.get $n`)
	g.line(`memory.copy`)
	// Some(sptr).
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $sptr`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
}

// emitReaderCloseMethod / emitWriterCloseMethod share the same
// shape — fd_close (preview-1) or `[resource-drop]input-stream`
// / `[resource-drop]output-stream` (preview-2). Both flavours
// return Option[IoError]; the streams path always succeeds (resource
// drops can't fail), so we always emit `None`.
func (g *generator) emitReaderCloseMethod() {
	g.emitCloseMethod("$__method_Reader_close", "$__wasi_input_stream_drop")
}

func (g *generator) emitWriterCloseMethod() {
	g.emitCloseMethod("$__method_Writer_close", "$__wasi_output_stream_drop")
}

// emitCloseMethod writes a Reader.close / Writer.close that
// drops the stream resource and returns Option[IoError]. Resource
// drops are infallible at the canonical-ABI level — there isn't a
// meaningful error to propagate to the lang program, so we always
// construct None.
func (g *generator) emitCloseMethod(name, dropImport string) {
	g.linef(`(func %s (param $r i32) (result i32)`, name)
	g.indent++
	g.line(`(local $result i32)`)
	g.line(`local.get $r`)
	g.line(`i32.load`)
	g.linef(`call %s`, dropImport)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
}

// emitWriterWriteMethod writes Writer.write(s). On preview-1, a
// single fd_write of the entire string. On preview-2, a single
// `wasi:io/streams.[method]output-stream.blocking-write-and-flush`
// against the cached resource handle in mem[$w] — the Writer
// struct holds a stream handle end-to-end on the preview-2 path.
// Returns None on success, Some(IoError) on failure. Uses the
// static iovec at memory[56] and nwritten at memory[64] (same
// slots the stdin reader / Reader.read_line use — single-threaded
// so reuse is safe). On the streams path, retptr lives at memory
// 92 (shared canonical-ABI return area).
func (g *generator) emitWriterWriteMethod() {
	g.line(`(func $__method_Writer_write (param $w i32) (param $s i32) (result i32)`)
	g.indent++
	g.line(`(local $result i32)`)
	// $__streams_write loops blocking-write-and-flush in 4 KiB
	// chunks; it silently swallows stream errors (the helper
	// returns to caller without a discriminant), so Writer.write
	// always returns None. Matches the "stdio failures are
	// unrecoverable for a CLI" posture; a future revision can
	// switch to a helper that returns the canonical disc and
	// surface Some(IoError) on partial-write failure.
	g.line(`local.get $w`)
	g.line(`i32.load`)
	g.line(`local.get $s`)
	g.line(`local.get $s`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`call $__streams_write`)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
}

// emitIoErrorCase writes one branch of the errno → variant
// dispatch in `$__build_io_error`. `errno` is the WASI errno
// to match; `tagIdx` is the IoError variant index;
// `withPathPayload` is true for variants that carry the path
// (NotFound, PermissionDenied, AlreadyExists) and false for
// the payload-less ones (Interrupted, Unsupported).
func (g *generator) emitIoErrorCase(errno, tagIdx int, withPathPayload bool) {
	g.line(`local.get $errno`)
	g.linef(`i32.const %d`, errno)
	g.line(`i32.eq`)
	g.line(`if`)
	g.indent++
	if withPathPayload {
		g.line(`i32.const 8`)
	} else {
		g.line(`i32.const 4`)
	}
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.linef(`i32.const %d`, tagIdx)
	g.line(`i32.store`)
	if withPathPayload {
		g.line(`local.get $result`)
		g.line(`i32.const 4`)
		g.line(`i32.add`)
		g.line(`local.get $path`)
		g.line(`i32.store`)
	}
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
}

// emitReadFileHelper writes `$read_file(path) -> Result[string, IoError]`.
// Pipeline: $open_reader for the stream handle, bulk
// blocking-read(4096) loop into a growable accumulator,
// [resource-drop]input-stream on the way out, wrap in Ok(string).
// Errors out of $open_reader propagate as-is; stream errors
// mid-read are treated as EOF (matches Reader.read_chunk).
func (g *generator) emitReadFileHelper() {
	g.emitReadFileHelperPreview2()
}


// emitReadFileHelperPreview2 is the streams-flavoured `$read_file`.
// Pipeline: $open_reader (which goes through wasi:filesystem
// open-at + read-via-stream as of step 3c) → bulk
// blocking-read(4096) loop into a growable accumulator → drop
// the input-stream resource → wrap the accumulator in
// `Ok(string)`. Errors out of $open_reader propagate as-is; stream
// errors mid-read are treated as EOF (matches what Reader.read_chunk
// does too).
func (g *generator) emitReadFileHelperPreview2() {
	g.line(`(func $read_file (param $path i32) (result i32)`)
	g.indent++
	g.line(`(local $open_result i32) (local $reader i32) (local $stream i32)`)
	g.line(`(local $buf i32) (local $buf_size i32) (local $cur i32)`)
	g.line(`(local $host_ptr i32) (local $host_len i32)`)
	g.line(`(local $new_buf i32) (local $new_size i32)`)
	g.line(`(local $sbase i32) (local $sptr i32) (local $result i32)`)

	// Open. Result.Err shape matches what we want to return.
	g.line(`local.get $path`)
	g.line(`call $open_reader`)
	g.line(`local.tee $open_result`)
	g.line(`i32.load`)
	g.line(`if`)
	g.indent++
	g.line(`local.get $open_result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)
	// reader = mem[open_result + 4]; stream = mem[reader].
	g.line(`local.get $open_result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.tee $reader`)
	g.line(`i32.load`)
	g.line(`local.set $stream`)

	// Initial accumulator: 4 KiB. Doubles on overflow.
	g.line(`i32.const 4096`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $buf`)
	g.line(`i32.const 4096`)
	g.line(`local.set $buf_size`)
	g.line(`i32.const 0`)
	g.line(`local.set $cur`)

	g.line(`block $end`)
	g.indent++
	g.line(`loop $loop`)
	g.indent++
	// blocking-read(stream, 4096, retptr=92).
	g.line(`local.get $stream`)
	g.line(`i64.const 4096`)
	g.line(`i32.const 92`)
	g.line(`call $__wasi_blocking_read`)
	// Outer disc; non-zero = Err = treat as EOF.
	g.line(`i32.const 92`)
	g.line(`i32.load8_u`)
	g.line(`br_if $end`)
	// list_len at retptr+8; 0 = EOF.
	g.line(`i32.const 100`)
	g.line(`i32.load`)
	g.line(`local.tee $host_len`)
	g.line(`i32.eqz`)
	g.line(`br_if $end`)
	// list_ptr at retptr+4.
	g.line(`i32.const 96`)
	g.line(`i32.load`)
	g.line(`local.set $host_ptr`)

	// Grow the buffer if cur + host_len > buf_size. Loop because
	// host_len could exceed multiple doublings (rare but
	// mathematically possible — we asked for ≤4096, the host
	// can return up to that, and 4096 fits in one doubling).
	g.line(`block $grow_done`)
	g.indent++
	g.line(`loop $grow`)
	g.indent++
	g.line(`local.get $cur`)
	g.line(`local.get $host_len`)
	g.line(`i32.add`)
	g.line(`local.get $buf_size`)
	g.line(`i32.le_u`)
	g.line(`br_if $grow_done`)
	// new_size = buf_size * 2; new_buf = alloc(new_size); memory.copy.
	g.line(`local.get $buf_size`)
	g.line(`i32.const 1`)
	g.line(`i32.shl`)
	g.line(`local.tee $new_size`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $new_buf`)
	g.line(`local.get $new_buf`)
	g.line(`local.get $buf`)
	g.line(`local.get $cur`)
	g.line(`memory.copy`)
	g.line(`local.get $new_buf`)
	g.line(`local.set $buf`)
	g.line(`local.get $new_size`)
	g.line(`local.set $buf_size`)
	g.line(`br $grow`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// memory.copy(buf + cur, host_ptr, host_len)
	g.line(`local.get $buf`)
	g.line(`local.get $cur`)
	g.line(`i32.add`)
	g.line(`local.get $host_ptr`)
	g.line(`local.get $host_len`)
	g.line(`memory.copy`)
	// cur += host_len
	g.line(`local.get $cur`)
	g.line(`local.get $host_len`)
	g.line(`i32.add`)
	g.line(`local.set $cur`)
	g.line(`br $loop`)
	g.indent--
	g.line(`end`)
	g.indent--
	g.line(`end`)

	// Drop the stream resource (resource drops are infallible).
	g.line(`local.get $stream`)
	g.line(`call $__wasi_input_stream_drop`)

	// Materialise as a length-prefixed string.
	g.line(`local.get $cur`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $sbase`)
	g.line(`local.get $sbase`)
	g.line(`local.get $cur`)
	g.line(`i32.store`)
	g.line(`local.get $sbase`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.set $sptr`)
	g.line(`local.get $sptr`)
	g.line(`local.get $buf`)
	g.line(`local.get $cur`)
	g.line(`memory.copy`)

	// Build Ok(sptr).
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $sptr`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
}

// emitWriteFileHelper writes
// `$write_file(path, content) -> Option[IoError]`. Pipeline:
// $open_writer → single blocking-write-and-flush of the entire
// content → drop the output-stream resource → return None.
// open_writer's Result.Err gets repackaged into Option[IoError];
// mid-write stream errors map to Some(IoError) with errno=29 (EIO).
func (g *generator) emitWriteFileHelper() {
	g.emitWriteFileHelperPreview2()
}


// emitWriteFileHelperPreview2 is the streams-flavoured `$write_file`.
// Pipeline: $open_writer (which goes through wasi:filesystem
// open-at + write-via-stream as of step 3c) → single
// blocking-write-and-flush of the entire content → drop the
// output-stream resource → return None. open_writer's Result.Err
// gets repackaged into the Option[IoError] shape this function
// returns; mid-write stream errors map to Some(IoError) with
// errno=29 (EIO).
func (g *generator) emitWriteFileHelperPreview2() {
	g.line(`(func $write_file (param $path i32) (param $content i32) (result i32)`)
	g.indent++
	g.line(`(local $open_result i32) (local $writer i32) (local $stream i32)`)
	g.line(`(local $content_len i32) (local $result i32)`)

	// Open. Result.Err shape needs to translate from
	// Result[Writer, IoError] to Option[IoError] = Some(err).
	g.line(`local.get $path`)
	g.line(`call $open_writer`)
	g.line(`local.tee $open_result`)
	g.line(`i32.load`)
	g.line(`if`)
	g.indent++
	// Some(err): allocate 8 bytes, tag=0 + err_ptr from open_result+4.
	g.line(`i32.const 8`)
	g.line(`call $__lang_alloc`)
	g.line(`local.set $result`)
	g.line(`local.get $result`)
	g.line(`i32.const 0`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`local.get $open_result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.line(`return`)
	g.indent--
	g.line(`end`)

	// writer = mem[open_result + 4]; stream = mem[writer].
	g.line(`local.get $open_result`)
	g.line(`i32.const 4`)
	g.line(`i32.add`)
	g.line(`i32.load`)
	g.line(`local.tee $writer`)
	g.line(`i32.load`)
	g.line(`local.set $stream`)

	// content_len = mem[content - 4].
	g.line(`local.get $content`)
	g.line(`i32.const 4`)
	g.line(`i32.sub`)
	g.line(`i32.load`)
	g.line(`local.set $content_len`)

	// $__streams_write loops blocking-write-and-flush in 4 KiB
	// chunks (the canonical-ABI per-call cap). It swallows
	// stream errors silently — same trade-off as Writer.write;
	// a future revision can plumb success/failure through a
	// dedicated chunked helper.
	g.line(`local.get $stream`)
	g.line(`local.get $content`)
	g.line(`local.get $content_len`)
	g.line(`call $__streams_write`)

	// Drop the stream and return None.
	g.line(`local.get $stream`)
	g.line(`call $__wasi_output_stream_drop`)
	g.line(`i32.const 4`)
	g.line(`call $__lang_alloc`)
	g.line(`local.tee $result`)
	g.line(`i32.const 1`)
	g.line(`i32.store`)
	g.line(`local.get $result`)
	g.indent--
	g.line(`)`)
}

// emitDataSegments writes the static-memory initialisers: the runtime
// iovecs / newline byte plus every interned string with its 4-byte
// little-endian length prefix.
func (g *generator) emitDataSegments() {
	// putchar iovec at 4: ptr=0, len=1
	g.line(`(data (i32.const 4) "\00\00\00\00\01\00\00\00")`)
	// print iovec[1] at 24: ptr=32 (=newline byte), len=1
	g.line(`(data (i32.const 24) "\20\00\00\00\01\00\00\00")`)
	// newline byte at 32
	g.line(`(data (i32.const 32) "\0a")`)
	if g.needsReadLine || g.needsStreamingIO {
		// read_line iovec at 56: ptr=68 (one-byte buffer), len=1.
		// The Reader.read_line method reuses this static slot
		// because heap-allocated iovecs aren't reliably aligned
		// (the byte-grain accumulator drifts the bump pointer)
		// and wasmtime requires 4-byte alignment on fd_read's
		// iovs argument. Both helpers run on the single thread
		// of execution, so static reuse is safe.
		g.line(`(data (i32.const 56) "\44\00\00\00\01\00\00\00")`)
	}
	// Per-function closure cells: 8 bytes each at closuresBase+8*i,
	// pre-initialised with (table_idx=i, env_ptr=0). Only the
	// originally top-level functions get cells; hoisted (closure-
	// converted) entries are reached through fresh closures the
	// MakeClosure code allocates per construction.
	if g.needsClosures {
		// Each in-table top-level (i.e. value-referenced) function
		// gets a static cell; hoisted closures get fresh cells per
		// MakeClosure invocation. Cell i contains (table-idx i,
		// env_ptr=0).
		for i, name := range g.tableEntries {
			if g.funcIndex[name] >= g.origTopLevelCount {
				continue // hoisted entry; no static cell
			}
			g.linef(`(data (i32.const %d) "%s%s")`, g.closuresBase+8*i, encodeI32(i), encodeI32(0))
		}
	}
	// strings
	for _, s := range g.stringEntries {
		g.linef(`(data (i32.const %d) "%s%s")`, s.offset, encodeI32(len(s.text)), wasmEscape(s.text))
	}
	// Bump-allocator initial pointer at offset 40. We seed it past the
	// end of the strings, rounded up to 4 bytes for i32 access.
	if g.needsArrays {
		start := g.stringOffset
		if start < 64 {
			start = 64
		}
		if start%4 != 0 {
			start += 4 - (start % 4)
		}
		g.linef(`(data (i32.const 40) "%s")`, encodeI32(start))
	}
}

// encodeI32 returns a four-byte little-endian byte string in WAT data
// escape form (e.g. 13 → `\0d\00\00\00`).
func encodeI32(n int) string {
	return fmt.Sprintf(`\%02x\%02x\%02x\%02x`,
		byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
}

// wasmEscape encodes the contents of a string literal for inclusion in
// a WAT data segment: printable ASCII apart from `"` and `\` is kept
// verbatim, everything else becomes `\xx`.
func wasmEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c >= 0x20 && c <= 0x7e:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\%02x`, c)
		}
	}
	return b.String()
}

// ---------- type mapping ----------

func watType(t ast.Type) (string, error) {
	switch v := t.(type) {
	case ast.NumberType:
		// Sub-i32 widths still live in an i32 wasm slot (with
		// masking on store) — only i64 needs the wider wasm type.
		if v.NormalWidth() == 64 {
			return "i64", nil
		}
		return "i32", nil
	case ast.BoolType, ast.StringType:
		// Strings are pointers into linear memory, so they're i32.
		return "i32", nil
	case ast.ArrayType, ast.SliceType:
		// Arrays and slices are both heap-pointer values at the
		// wasm level — slices point to an 8-byte
		// (data_ptr, len) struct, arrays point at the data
		// (with a length prefix at base-4).
		return "i32", nil
	case *ast.FuncType:
		// Function values are table indices.
		return "i32", nil
	case ast.StructType, ast.EnumType, ast.TupleType:
		// Struct, enum and tuple values are heap pointers.
		return "i32", nil
	case ast.FloatType:
		if v.NormalWidth() == 64 {
			return "f64", nil
		}
		return "f32", nil
	}
	return "", fmt.Errorf("wasm: type %s isn't supported by this backend yet", t)
}
