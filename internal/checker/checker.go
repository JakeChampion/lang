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
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/prelude"
	"github.com/jakechampion/lang/internal/stdlib"
)

type Error struct {
	Pos  ast.Position
	Span int    // optional: token length for `^~~~~` underline; 0 = caret only
	Note string // optional: "did you mean foo?" hint
	Msg  string
	Path string // source file path; filled by errf from c.current.SourceModule
}

func (e *Error) Error() string          { return fmt.Sprintf("type error at %s: %s", e.Pos, e.Msg) }
func (e *Error) Position() ast.Position { return e.Pos }
func (e *Error) Length() int            { return e.Span }
func (e *Error) Hint() string           { return e.Note }
func (e *Error) File() string           { return e.Path }
func (e *Error) setFile(p string)       { e.Path = p }

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
	// MethodSources records the canonical source-module path each
	// registered method's body came from. Mangled-name keys mirror
	// the values in Methods (e.g. `__method_i32_abs` →
	// `stdlib://std/i32.lang`). Entries with an empty value are
	// universally visible — that's the case for synthetic methods
	// the checker registers itself (Reader / Writer / Map /
	// MapIter, the inline-IR `Array.push`, and the built-in string
	// methods) plus anything sourced from the auto-injected magic
	// prelude.
	//
	// The dispatch path filters method resolutions against the
	// call site's enclosing module + the program's `ModuleImports`
	// closure so that methods declared in a module are only callable
	// from files whose import closure reaches that module
	// (`docs/PRELUDE-TO-MODULES.md`). Empty entries on either side
	// skip the filter (transitional accommodation for prelude-
	// injected decls and single-file programs).
	MethodSources map[string]string
	// ModuleImports mirrors `ast.Program.ModuleImports` — the per-
	// module transitive import closure modload computes during
	// loading. Copied onto Info at the start of Check so the
	// dispatch path doesn't need a back-reference to the Program.
	ModuleImports map[string]map[string]bool
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
	// Generics is the kind-agnostic view of GenericFuncs + GenericStructs.
	// Lets passes that just need "is this name a generic decl?" or
	// "iterate every generic decl" (notably the monomorphisation
	// pass's drop-loop) consult a single map instead of running
	// parallel paths over the two type-specific maps. Always kept
	// in sync with GenericFuncs / GenericStructs by the checker's
	// registration code.
	Generics map[string]ast.GenericDecl
}

// builtinEnumDecls returns the synthetic enum declarations the
// checker injects into every program: `Option[T]` and
// `Result[T, E]`. Variant order matters — runtime helpers
// (`$read_line`, `$env`) hardcode the tag indices, so `Some` is
// 0, `None` is 1, `Ok` is 0, `Err` is 1; and the IR's pair-form
// lowering hard-codes the same `OpMakeSomeI32 → tag=0` /
// `OpMakeOkI32 → tag=0` mapping. Letting a user redeclare
// `Option` with the variants swapped silently miscompiles —
// `Check` rejects any user `enum Option { … }` /
// IsReservedName reports whether name is the name of an auto-
// injected built-in enum or struct. User code can't redeclare
// these — the checker rejects shadow attempts at the source
// position of the user decl (see `shadowedEnums` /
// `shadowedStructs` in `Check`).
//
// Single source of truth for the reserved-name set: external
// callers (langsmith's generator, IDE rename refactors,
// documentation tooling) consult this rather than maintaining
// their own copy.
func IsReservedName(name string) bool {
	for _, ed := range builtinEnumDecls() {
		if ed.Name == name {
			return true
		}
	}
	for _, sd := range builtinStructDecls() {
		if sd.Name == name {
			return true
		}
	}
	return false
}

// `enum Result { … }` (and the other reserved names below)
// before reaching variant registration. The decls live in the
// AST, not just the checker info, so the formatter /
// interpreter / IR layers see them just like user-written
// enums.
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
		// ProcessResult — the return shape of `exec(cmd, args,
		// stdin)`. The interp's Go-side implementation populates
		// stdout / stderr / exit_code; the wasm + native backends
		// don't lower exec today, so the type registration is
		// here-only for the test-runner migration path that runs
		// the migrated suites under `lang -interp`. Spawn failures
		// (executable missing, permission denied) surface as
		// `exit_code = 127` (POSIX `command not found` convention)
		// with the OS error message in `stderr`, so callers can
		// treat "didn't run" identically to "ran and failed" for
		// the common case while still distinguishing via the
		// sentinel when they care.
		{
			Name: "ProcessResult",
			Fields: []ast.Param{
				{Name: "stdout", Type: ast.StringType{}},
				{Name: "stderr", Type: ast.StringType{}},
				{Name: "exit_code", Type: ast.NumberType{}},
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
	// Opt-out: a program that `import "core/no_prelude";`s
	// signals it doesn't want the auto-prelude. The module
	// itself is empty — the import statement is the signal.
	// Used for explicit-imports test programs and (eventually,
	// per docs/PRELUDE-TO-MODULES.md phase 5) for every program
	// once the auto-prelude is retired.
	//
	// modload's `combine` doesn't copy entry-module imports into
	// the combined Program (it flattens decls only), so we read
	// the signal off the LoadedStdlibPaths set instead. Single-
	// file checker callers that bypass modload (and therefore
	// leave LoadedStdlibPaths nil) can't opt out today — but
	// they also don't import stdlib paths, so the auto-prelude
	// is the right behaviour for them.
	if prog.LoadedStdlibPaths["stdlib://core/no_prelude.lang"] {
		return nil
	}
	pre, err := parser.Parse(prelude.Source)
	if err != nil {
		return fmt.Errorf("prelude: %w", err)
	}
	// Phase 3 of docs/PRELUDE-TO-MODULES.md: as methods migrate
	// out of the magic prelude into `std/…` modules, the prelude
	// file `import "std/foo";`s them back in so existing programs
	// keep working unchanged. Auto-prelude's imports are routed
	// through `modload.LoadStdlibFlat`, which loads each path
	// (and every stdlib module it transitively imports) and runs
	// modload's rewriter in flat-namespace mode — qualified call
	// sites inside stdlib bodies (`int.foo()` →) rewrite to bare
	// calls (`foo()`), so a stdlib module can use `import
	// "core/int";` plus `int.foo(...)` to call across-module
	// without breaking the auto-prelude path's "all decls bare-
	// named" invariant.
	//
	// Top-level dedup: paths already loaded by the user's entry
	// program through `modload.Load` are filtered out here. The
	// user's modload-loaded copy has mangled names; loading the
	// same module via auto-prelude again would create a second
	// bare-named copy and the checker's receiver-method
	// redeclaration check would fire on every method imported
	// by both paths.
	wantPaths := make([]string, 0, len(pre.Imports))
	for _, imp := range pre.Imports {
		if !stdlib.IsStdlibPath(imp.Path) {
			continue
		}
		key := imp.Path
		if !strings.HasSuffix(key, ".lang") {
			key += ".lang"
		}
		canonical := "stdlib://" + key
		if prog.LoadedStdlibPaths[canonical] {
			continue
		}
		wantPaths = append(wantPaths, imp.Path)
	}
	if len(wantPaths) > 0 {
		// LoadStdlibFlatSkipping recursively loads stdlib paths
		// the user didn't explicitly import. Transitive imports
		// inside stdlib may reach back to a path the entry program
		// already loaded through modload's mangling path (e.g.
		// std/string now imports std/array, so an entry program
		// importing `std/array` would surface duplicate
		// `__method_Array_*` decls without the skip). Passing the
		// already-loaded paths suppresses their flat-namespace
		// contribution.
		stdProg, err := modload.LoadStdlibFlatSkipping(wantPaths, prog.LoadedStdlibPaths)
		if err != nil {
			return fmt.Errorf("prelude: %w", err)
		}
		for _, fn := range stdProg.Funcs {
			fn.IsPrelude = true
			prog.Funcs = append(prog.Funcs, fn)
		}
		prog.Structs = append(prog.Structs, stdProg.Structs...)
		prog.Enums = append(prog.Enums, stdProg.Enums...)
		prog.Unions = append(prog.Unions, stdProg.Unions...)
		prog.Consts = append(prog.Consts, stdProg.Consts...)
		if prog.LoadedStdlibPaths == nil {
			prog.LoadedStdlibPaths = map[string]bool{}
		}
		for p := range stdProg.LoadedStdlibPaths {
			prog.LoadedStdlibPaths[p] = true
		}
	}
	// The prelude file itself has no decls today (all its content
	// moved into std/* + core/* modules), but the prelude path is
	// kept open in case a future change needs to add a helper
	// that doesn't sit cleanly in a single module.
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
	//
	// Builtin names are RESERVED: if the user declared one
	// with the same name, we drop the builtin (so the user's
	// decl is the only one in the program — avoids a noisy
	// "redeclared" pile-on) and record the offending decl so
	// `Check` can flag it once the error sink exists.
	// Allowing the shadow silently would miscompile, since
	// runtime helpers + IR pair-form lowering hard-code the
	// builtin variant order.
	// `Check` is re-entered by the monomorph pass on the
	// rewritten program — at that point `prog.Enums` /
	// `prog.Structs` already contain the builtin decls that
	// the first pass prepended. We distinguish those from
	// real user decls by position: parser-produced decls have
	// non-zero `P` (the lexer numbers from line 1), so the
	// zero-positioned entries are the previously-injected
	// builtins and skipping re-injection is the right move
	// (not a shadow).
	var shadowedEnums []*ast.EnumDecl
	{
		userEnums := map[string]*ast.EnumDecl{}
		for _, ed := range prog.Enums {
			userEnums[ed.Name] = ed
		}
		var inject []*ast.EnumDecl
		for _, ed := range builtinEnumDecls() {
			if existing, dup := userEnums[ed.Name]; dup {
				if existing.P == (ast.Position{}) {
					continue
				}
				shadowedEnums = append(shadowedEnums, existing)
				continue
			}
			inject = append(inject, ed)
		}
		if len(inject) > 0 {
			prog.Enums = append(inject, prog.Enums...)
		}
	}
	// Same shape for the auto-injected structs (Reader,
	// Writer, HttpRequest, HttpResponse, Map, MapIter, Url) —
	// same shadow-is-an-error policy, same monomorph-re-entry
	// handling.
	var shadowedStructs []*ast.StructDecl
	{
		userStructs := map[string]*ast.StructDecl{}
		for _, sd := range prog.Structs {
			userStructs[sd.Name] = sd
		}
		var inject []*ast.StructDecl
		for _, sd := range builtinStructDecls() {
			if existing, dup := userStructs[sd.Name]; dup {
				if existing.P == (ast.Position{}) {
					continue
				}
				shadowedStructs = append(shadowedStructs, existing)
				continue
			}
			inject = append(inject, sd)
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
			MethodSources:       map[string]string{},
			ModuleImports:       prog.ModuleImports,
			VariantCallPayloads: map[*ast.Call][]ast.Type{},
			GenericFuncs:        map[string]*ast.FuncDecl{},
			GenericStructs:      map[string]*ast.StructDecl{},
			Generics:            map[string]ast.GenericDecl{},
		},
		variantOf: map[string][]variantRef{},
	}

	// Surface shadow-attempts on reserved built-in type names
	// recorded above. We report on the user's decl position so
	// the IDE squiggle lands on the offending source, not on
	// some synthetic builtin we never actually exposed.
	for _, ed := range shadowedEnums {
		c.errf(ed.P, "enum %q is a reserved built-in name and cannot be redeclared", ed.Name)
	}
	for _, sd := range shadowedStructs {
		c.errf(sd.P, "struct %q is a reserved built-in name and cannot be redeclared", sd.Name)
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
			c.info.Generics[sd.Name] = sd
		}
	}

	// Desugar `type X = A | B | C;` unions into synthesised
	// EnumDecls before the enum registration loop runs — that
	// way the rest of the pipeline only ever sees enums. Each
	// member must resolve to a non-generic StructDecl registered
	// above. Errors at this stage (unknown member, generic
	// member, duplicate union name) surface as type errors with
	// the source position of the union or member; the
	// synthesised enum is dropped on error so callers don't see
	// a phantom registration. After desugaring, prog.Unions is
	// nil so subsequent passes can't read it.
	for _, ud := range prog.Unions {
		if _, dup := c.info.Structs[ud.Name]; dup {
			c.errf(ud.P, "union %q collides with a struct of the same name", ud.Name)
			continue
		}
		if _, dup := c.info.Enums[ud.Name]; dup {
			c.errf(ud.P, "union %q collides with an enum of the same name", ud.Name)
			continue
		}
		variants := make([]ast.EnumVariant, 0, len(ud.Members))
		seen := map[string]bool{}
		ok := true
		for _, member := range ud.Members {
			if seen[member] {
				c.errf(ud.P, "duplicate member %q in union %s", member, ud.Name)
				ok = false
				continue
			}
			seen[member] = true
			sd, isStruct := c.info.Structs[member]
			if !isStruct {
				c.errf(ud.P, "union %s member %q does not name a struct in scope", ud.Name, member)
				ok = false
				continue
			}
			if len(sd.TypeParams) > 0 {
				c.errf(ud.P, "union %s member %q is generic; generic struct members in unions are not supported yet", ud.Name, member)
				ok = false
				continue
			}
			variants = append(variants, ast.EnumVariant{
				P:        ud.P,
				Name:     member,
				Payloads: []ast.Type{ast.StructType{Name: member}},
			})
		}
		if !ok {
			continue
		}
		prog.Enums = append(prog.Enums, &ast.EnumDecl{
			P:            ud.P,
			Name:         ud.Name,
			Variants:     variants,
			Public:       ud.Public,
			SourceModule: ud.SourceModule,
		})
	}
	prog.Unions = nil

	// Register every enum declaration. Variant names are recorded
	// in variantOf so an unqualified `Some(x)` or `Red` can be
	// rewritten into a typed *EnumLit during expression checking.
	// Two enums declaring the same variant name (e.g. `Color.Red`
	// and `Status.Red`) coexist — the bare `Red` becomes ambiguous
	// and must be qualified at the use site (`Color.Red`); the
	// resolution helpers below produce the disambiguation error.
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
			c.variantOf[v.Name] = append(c.variantOf[v.Name], variantRef{
				enumName: ed.Name,
				index:    i,
				payloads: v.Payloads,
			})
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
	// f32_bits(x: f32): i32 — reinterprets a 32-bit float as its
	// IEEE-754 bit pattern. f32_from_bits is the inverse. The pair
	// is needed by float formatting routines (extracting sign /
	// exponent / mantissa fields) and by lossless encoding of f32
	// values into byte buffers (JSON / wire formats). No value
	// conversion happens — the 32 bits on the operand stack carry
	// through unchanged; the IR lowers both calls to a no-op once
	// the type checker is happy.
	c.info.FuncSigs["f32_bits"] = &ast.FuncType{
		Params: []ast.Type{ast.FloatType{Width: 32}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	c.info.FuncSigs["f32_from_bits"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.FloatType{Width: 32},
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
	// On Linux/arm64 the helper opens the socket itself.
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
	// temp_dir(prefix): Result[string, IoError] — create a
	// fresh empty directory and return its absolute path.
	// `prefix` is appended to a random suffix so concurrent
	// runs don't clash. Caller invokes `remove_dir_all`
	// (or registers a cleanup hook on a TestRunner) to scrub
	// when finished; system tmpfs scrub-on-reboot is the
	// fallback safety net.
	c.info.FuncSigs["temp_dir"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StringType{},
			ast.EnumType{Name: "IoError"},
		}},
	}
	// read_dir(path): Result[string[], IoError] — list the
	// non-recursive children of `path`. Entries are base names
	// (no leading directory), unsorted. Use `std/sort` on the
	// result if a deterministic order matters.
	c.info.FuncSigs["read_dir"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.ArrayType{Elem: ast.StringType{}},
			ast.EnumType{Name: "IoError"},
		}},
	}
	// remove_file(path): Option[IoError] — unlink the file.
	// `None` on success, `Some(err)` on failure (mirrors
	// `write_file`). Removing a non-existent file is an
	// error — callers that don't care about that distinction
	// should ignore the return value.
	c.info.FuncSigs["remove_file"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Option", Args: []ast.Type{
			ast.EnumType{Name: "IoError"},
		}},
	}
	// remove_dir_all(path): Option[IoError] — recursively
	// remove `path` (mirrors POSIX `rm -rf`). Used by tests
	// to scrub `temp_dir` output. Same `Option[IoError]`
	// return shape as `remove_file` / `write_file`. Removing
	// a non-existent directory is silently ignored — matches
	// Go's `os.RemoveAll` semantics.
	c.info.FuncSigs["remove_dir_all"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Option", Args: []ast.Type{
			ast.EnumType{Name: "IoError"},
		}},
	}
	// subprocess(cmd, args, stdin): ProcessResult — spawn `cmd`
	// with `args` (NOT including argv[0]), feed `stdin` to its
	// standard input, capture stdout + stderr into the
	// returned struct, and surface its exit code. Always
	// returns a ProcessResult so the test-runner migration
	// can write straight-line "expected exit / stdout" diffs;
	// spawn failures surface as `exit_code = 127` with the
	// OS error message in `stderr` (POSIX convention). The
	// interp owns the only implementation today; native /
	// wasm backends would surface their own "subprocess not
	// supported on this target" error from codegen.
	//
	// Named `subprocess` rather than the obvious `exec` to
	// stay clear of `examples/self_host/vm.lang`'s long-
	// standing `pub function exec(ops: Op[]): Value` and any
	// user code that wraps an interpreter.
	procResult := ast.StructType{Name: "ProcessResult"}
	c.info.FuncSigs["subprocess"] = &ast.FuncType{
		Params: []ast.Type{
			ast.StringType{},
			ast.ArrayType{Elem: ast.StringType{}},
			ast.StringType{},
		},
		Result: procResult,
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

	// `arr.push(v)` is the one Array method that DOESN'T have a
	// prelude function declaration — the IR intercepts the
	// rewritten `__method_Array_push(arr, v)` call and emits the
	// alloc + memcpy + width-correct tail store inline (see
	// `emitArrayPush` in `internal/ir/ir.go`). One codepath covers
	// every stride class — no per-stride mangled names, no
	// per-stride prelude functions. Because there's no source-
	// level decl, the auto-discovery loop below can't see push:
	// we register it manually here along with its generic
	// signature so dispatch + type-checking work.
	arrayElemParam := ast.ParamType{Name: "T"}
	c.info.Methods["Array.push"] = "__method_Array_push"
	c.info.FuncSigs["__method_Array_push"] = &ast.FuncType{
		Params: []ast.Type{
			ast.ArrayType{Elem: arrayElemParam},
			arrayElemParam,
		},
		Result: ast.ArrayType{Elem: arrayElemParam},
	}

	// Auto-discover the remaining Array methods from the
	// `__method_Array_<name>` naming convention. Every prelude
	// function (and, post-migration, every `std/array` module
	// function) that follows the convention registers itself for
	// the `arr.<name>(…)` dispatch path without a hand-written
	// line per method. The receiver-element constraint stays
	// inside the prelude function signature (e.g.
	// `function __method_Array_join(arr: string[], …)`); the
	// checker's type unification surfaces a clean
	// "cannot match i32[] to string[]" diagnostic when callers
	// mismatch.
	//
	// FuncSigs for these functions get populated by the normal
	// FuncDecl processing in `Check` — we only need to wire the
	// Methods map here.
	for _, fn := range prog.Funcs {
		if !strings.HasPrefix(fn.Name, "__method_Array_") {
			continue
		}
		suffix := fn.Name[len("__method_Array_"):]
		if suffix == "" || suffix == "push" {
			continue
		}
		c.info.Methods["Array."+suffix] = fn.Name
		c.info.MethodSources[fn.Name] = fn.SourceModule
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


	// `__memcpy(dst, src, n)` / `__memset(dst, b, n)` —
	// thin lang-callable wrappers around wasm's bulk-memory
	// `memory.copy` / `memory.fill`. The doc-roadmap calls
	// them out as the unlock for moving the json buffer
	// family + the Map runtime from hand-written wat into
	// the lang prelude (every growable-byte-buffer pattern
	// needs them). All three params are i32 byte counts /
	// pointers; the helpers return void. arm64 inlines them
	// via plain loads/stores; wat uses memory.copy.
	usizeT := ast.NumberType{Width: ast.WidthPtr, Signed: false, Spelling: "usize"}
	c.info.FuncSigs["__memcpy"] = &ast.FuncType{
		Params: []ast.Type{usizeT, usizeT, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["__memset"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}, ast.NumberType{}},
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
	// `__alloc(n)` returns a fresh n-byte block on the bump heap.
	// Returns `usize` so the full address survives on arm64-darwin
	// where the heap lives above 4 GiB.
	c.info.FuncSigs["__alloc"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: usizeT,
	}
	c.info.FuncSigs["__load_i32"] = &ast.FuncType{
		Params: []ast.Type{usizeT},
		Result: ast.NumberType{},
	}
	c.info.FuncSigs["__store_i32"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	// `__load_ptr` / `__store_ptr` — pointer-width memory pokes.
	// Address AND value are usize so the full 8-byte pointer
	// shape survives on natives. On wasm32 both collapse to i32
	// because WidthPtr resolves to 4 there.
	c.info.FuncSigs["__load_ptr"] = &ast.FuncType{
		Params: []ast.Type{usizeT},
		Result: usizeT,
	}
	c.info.FuncSigs["__store_ptr"] = &ast.FuncType{
		Params: []ast.Type{usizeT, usizeT},
		Result: ast.VoidType{},
	}
	// `__ptr_width()` — returns the target's pointer width in
	// bytes: 4 on wasm32, 8 on arm64. The Map runtime uses it
	// to size per-entry key/value slots so heap pointers (string
	// keys/values) round-trip through 8-byte slots on arm64 (and
	// stay 4-byte on wasm32, no regression in bucket-buffer
	// size).
	c.info.FuncSigs["__ptr_width"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
	}
	// `__load_i64` / `__store_i64` — 8-byte memory pokes. Used
	// by the Map runtime's wide-scalar-boxed key path
	// (keyKind=2): on wasm32 an i64 / u64 / f64 key doesn't
	// fit a `usize` slot, so the IR boxes it into a heap cell
	// and the runtime dereferences via these shims to hash and
	// compare the underlying 8-byte value. Address stays usize
	// (4 on wasm32, 8 on natives); the loaded / stored value
	// is i64.
	c.info.FuncSigs["__load_i64"] = &ast.FuncType{
		Params: []ast.Type{usizeT},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["__store_i64"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{Width: 64, Signed: true}},
		Result: ast.VoidType{},
	}
	// Other wide / sub-i32 wat shims (`__load_f64` / `__store_f64`,
	// `__load_u8` / `__store_u8`, `__load_u16` / `__store_u16`)
	// were removed when `arr.push(v)` moved to inline IR
	// lowering — the IR emits the typed wasm store ops
	// directly, no callable wat shim needed. Reintroduce here
	// next to `__store_i32` if a future lang-prelude helper
	// needs them.

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
			c.info.MethodSources[mangled] = fn.SourceModule
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
			c.info.Generics[fn.Name] = fn
		}
	}

	// Auto-main from handle: when the user defines
	// `function handle(req: HttpRequest): HttpResponse` but
	// no `main()`, synthesise a minimal main that calls
	// `tcp_serve(port, handle)` after reading PORT from the
	// environment (default 8080). The same source then
	// compiles for arm64 (CLI-mode native server), wasm
	// CLI-mode (`--invoke main`), and wasi-http (the
	// existing handle()-export wiring is unaffected by main
	// existing alongside it).
	//
	// Skipped silently when both handle() and main() are
	// user-defined — don't surprise users who want their
	// own main alongside the wasi-http handler.
	if hasHandleDecl(prog) && !hasMainDecl(prog) {
		prog.Funcs = append(prog.Funcs, synthesiseHandleMain(prog))
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

// methodVisibleHere reports whether `mangled` is callable from
// the current call-site context. The rule (per
// docs/PRELUDE-TO-MODULES.md) is: a method declared in module M
// is callable from a file F only if M ∈ closure(F).
//
// Empty source modules on either side skip the check —
// transitional accommodation for prelude-injected decls,
// checker-synthetic methods (Reader / Writer / Map / MapIter /
// the inline-IR `Array.push`), and single-file programs that
// bypass modload. Both sides go away once Phase 5 removes the
// magic prelude and every method lives in a module.
//
// Same-module always passes (a module can always call its own
// methods regardless of import graph). Cross-module visibility
// consults the program-wide import-closure map modload built
// during `combine`.
//
// Stdlib-to-stdlib shortcut: a method declared in any stdlib
// module is universally callable from any other stdlib module
// regardless of import closure. The stdlib's method graph has
// natural cycles (std/string's bodies call (i32) byte methods
// from std/i32; std/i32's bodies call (string) methods from
// std/string) that modload's cycle-detector would otherwise
// reject, and the auto-prelude path already side-steps the
// gate by clearing `SourceModule` on every loaded fn. The
// shortcut keeps no-prelude semantically identical to auto-
// prelude for stdlib internals — only USER → stdlib visibility
// still requires an explicit import.
func (c *checker) methodVisibleHere(mangled string) bool {
	methodSrc := c.info.MethodSources[mangled]
	if methodSrc == "" {
		return true
	}
	if c.current == nil || c.current.SourceModule == "" {
		return true
	}
	if c.current.SourceModule == methodSrc {
		return true
	}
	if strings.HasPrefix(c.current.SourceModule, "stdlib://") &&
		strings.HasPrefix(methodSrc, "stdlib://") {
		return true
	}
	if c.info.ModuleImports == nil {
		return true
	}
	return c.info.ModuleImports[c.current.SourceModule][methodSrc]
}

type checker struct {
	info        *Info
	errors      []error
	current     *ast.FuncDecl
	loopDepth   int
	switchDepth int

	// variantOf maps a variant's bare name (`Some`, `Err`, `Red`) to
	// every enum that declares it. Built during the enum
	// registration pass. Most names have exactly one entry — the
	// IDE / IR pretend the map is `[string]variantRef`. When two
	// enums declare the same variant (e.g. `Color.Red` and
	// `Status.Red` coexist), the unqualified reference is
	// ambiguous and the user must qualify with `Color.Red`. The
	// resolution helpers below pick the right entry from the slice.
	variantOf map[string][]variantRef

	// Closure-capture plumbing. While checking a local function body,
	// captureSink records each outer-scope name read by the body as
	// a capture; captureOuter is the scope of the immediately
	// enclosing function so we can look those names up. Both are nil
	// outside a local function.
	captureSink  func(name string, t ast.Type)
	captureOuter *scope
	// captureChain stacks (sink, scope) entries for every enclosing
	// local function — outermost first. A name resolved several
	// levels up must be captured by every intermediate function so
	// each closure's env block can forward it down to the deepest
	// reader. Without this, three-level nesting (level3 captures
	// from makeChain; level1's body references it) would error with
	// `undefined identifier` because the lookup only walked the
	// immediately-enclosing scope.
	captureChain []captureEntry

	// mutualRecSiblings is the set of local FuncDecl names that
	// form a mutual-recursion cycle in the current block (set by
	// checkBlock's pre-pass after a Tarjan SCC walk). Only these
	// names skip the capture path — non-cycle forward references
	// still capture normally. closureconv consumes the same
	// detection (via the AST walk it re-runs) to drive the
	// null-env direct-call rewrite.
	mutualRecSiblings map[string]bool
}

type captureEntry struct {
	sink  func(name string, t ast.Type)
	scope *scope
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
		c.resolveTypesInBlock(fn.Body, params)
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

func (c *checker) resolveTypesInBlock(b *ast.Block, params map[string]bool) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		switch x := st.(type) {
		case *ast.Block:
			c.resolveTypesInBlock(x, params)
		case *ast.If:
			c.resolveTypesInBlock(asBlock(x.Then), params)
			c.resolveTypesInBlock(asBlock(x.Else), params)
		case *ast.IfLet:
			c.resolveTypesInBlock(asBlock(x.Then), params)
			c.resolveTypesInBlock(asBlock(x.Else), params)
		case *ast.LetElse:
			c.resolveTypesInBlock(x.Else, params)
		case *ast.While:
			c.resolveTypesInBlock(asBlock(x.Body), params)
		case *ast.For:
			c.resolveTypesInBlock(asBlock(x.Body), params)
		case *ast.Var:
			c.resolveType(&x.Type, params)
		case *ast.Switch:
			for _, k := range x.Cases {
				c.resolveTypesInBlock(k.Body, params)
			}
			c.resolveTypesInBlock(x.Default, params)
		case *ast.Match:
			for _, arm := range x.Arms {
				c.resolveTypesInBlock(arm.Body, params)
			}
		case *ast.FuncDecl:
			// Nested function declarations (closures, hoisted
			// inner functions). Their own type-params would
			// shadow the outer scope's; the typical inner
			// function has no type-params and just sees the
			// outer params, so passing the surrounding `params`
			// set is the conservative right answer here.
			for i := range x.Params {
				c.resolveType(&x.Params[i].Type, params)
			}
			c.resolveType(&x.ReturnType, params)
			c.resolveTypesInBlock(x.Body, params)
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
	case ast.SliceType:
		elem := t.Elem
		c.resolveType(&elem, params)
		*slot = ast.SliceType{Elem: elem}
	case ast.TupleType:
		// Recurse into each element. Without this, a
		// generic function's `(T, T)` return type kept its
		// elements as the parser-built `StructType{Name:"T"}`
		// (never converted to `ParamType`), while
		// `checkExpr((x, x))` returned `TupleType` over the
		// param's already-resolved `ParamType`. ast.Equal then
		// compared `StructType` vs `ParamType` and returned
		// false — the user saw "function returns (T, T) but
		// expression is (T, T)" with identical-looking sides.
		elems := make([]ast.Type, len(t.Elems))
		copy(elems, t.Elems)
		for i := range elems {
			c.resolveType(&elems[i], params)
		}
		*slot = ast.TupleType{Elems: elems}
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

// resolveVariant looks up a variant reference by name and (optional)
// enum qualifier. Returns the matching variantRef and ok=true. If
// `enumName` is non-empty, only the entry on that specific enum
// matches — used for `Color.Red`-style qualified references. If
// `enumName` is empty (the bare `Red` / `Some(x)` form), there must
// be exactly one candidate; multiple candidates means the call site
// has to disambiguate. multi reports whether the bare-name lookup
// hit more than one entry — the caller uses it to produce a
// "qualify with `<E>.<v>`" hint.
func (c *checker) resolveVariant(name, enumName string) (variantRef, bool, bool) {
	cands := c.variantOf[name]
	if enumName != "" {
		for _, vr := range cands {
			if vr.enumName == enumName {
				return vr, true, len(cands) > 1
			}
		}
		return variantRef{}, false, false
	}
	if len(cands) == 1 {
		return cands[0], true, false
	}
	return variantRef{}, false, len(cands) > 1
}

// variantEnumList returns a human-readable list of enum names that
// declare `name`. Used in the "ambiguous variant" diagnostic so the
// user sees every candidate, not just the first one.
func (c *checker) variantEnumList(name string) string {
	cands := c.variantOf[name]
	if len(cands) == 0 {
		return ""
	}
	if len(cands) == 1 {
		return cands[0].enumName
	}
	var b strings.Builder
	for i, vr := range cands {
		if i > 0 {
			if i == len(cands)-1 {
				b.WriteString(" and ")
			} else {
				b.WriteString(", ")
			}
		}
		b.WriteString(vr.enumName)
	}
	return b.String()
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
// unifyIfArms returns a single type representing both arms of
// an if-expression, or nil if the two arm types are
// incompatible. Used to allow `if (cond) { Some(x) } else
// { None }` to type-check as `Option[i32]` — the EnumLit
// machinery produces `EnumType{Name: "Option"}` (no Args) for
// the bare `None` because there's no payload to infer T from,
// and we want the specified arm's type args to flow up rather
// than failing the strict equality check the IfExpr handler
// was using before.
//
// Rules:
//   - If either side is nil (downstream error), return the
//     other.
//   - If `ast.Equal`, return either.
//   - If one is `EnumType{Name: X, Args: nil}` and the other
//     is `EnumType{Name: X, Args: [...]}`, return the
//     specified one (mirrors the `assignable` rule at the
//     same enum.no-args boundary).
//   - Otherwise nil (caller errors).
func unifyIfArms(a, b ast.Type) ast.Type {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if ast.Equal(a, b) {
		return a
	}
	// Polymorphic numeric (an unsettled NumberLit) is
	// compatible with any concrete numeric / float type —
	// return the concrete side and let the surrounding
	// settle pass stamp the literal's width. Without this,
	// `var n: i64 = if cond { a * b } else { 0 };` fails
	// the unify check because `0` is polymorphic while
	// `a * b` is i64.
	if an, aok := a.(ast.NumberType); aok && an.Polymorphic {
		switch b.(type) {
		case ast.NumberType, ast.FloatType:
			return b
		}
	}
	if bn, bok := b.(ast.NumberType); bok && bn.Polymorphic {
		switch a.(type) {
		case ast.NumberType, ast.FloatType:
			return a
		}
	}
	// Polymorphic float (an unsettled FloatLit) is
	// compatible with any concrete FloatType — let the
	// settle pass stamp the literal's width. Without this,
	// `var f: f64 = if cond { n.v } else { 0.0 };` rejects
	// because `0.0` defaults to f32 polymorphic and `n.v`
	// is concrete f64.
	if af, aok := a.(ast.FloatType); aok && af.Polymorphic {
		if _, ok := b.(ast.FloatType); ok {
			return b
		}
	}
	if bf, bok := b.(ast.FloatType); bok && bf.Polymorphic {
		if _, ok := a.(ast.FloatType); ok {
			return a
		}
	}
	ae, aok := a.(ast.EnumType)
	be, bok := b.(ast.EnumType)
	if aok && bok && ae.Name == be.Name {
		if len(ae.Args) == 0 && len(be.Args) > 0 {
			return be
		}
		if len(be.Args) == 0 && len(ae.Args) > 0 {
			return ae
		}
	}
	// Tuple types: unify element-wise. Lets polymorphic /
	// concrete widening flow through tuple-typed if-arms
	// like `if cond { (1234567890123, 3.14) } else { (0 as
	// i64, 0.0) }` — both arms become `(i64, f64)` once the
	// polymorphic-numeric / polymorphic-float rules above
	// resolve each element pair.
	if at, aok := a.(ast.TupleType); aok {
		if bt, bok := b.(ast.TupleType); bok && len(at.Elems) == len(bt.Elems) {
			out := make([]ast.Type, len(at.Elems))
			for i := range at.Elems {
				u := unifyIfArms(at.Elems[i], bt.Elems[i])
				if u == nil {
					return nil
				}
				out[i] = u
			}
			return ast.TupleType{Elems: out}
		}
	}
	// Empty-array / empty-slice literal vs typed array of the
	// same shape: `[]` (Elem=nil) unifies with any concrete
	// `T[]`. Lets a mixed array literal like
	// `[[1, 2], [], [3]]` type-check — the empty inner array
	// inherits the outer's element type from the first
	// non-empty sibling.
	if aa, aok := a.(ast.ArrayType); aok {
		if ba, bok := b.(ast.ArrayType); bok {
			if aa.Elem == nil {
				return ba
			}
			if ba.Elem == nil {
				return aa
			}
		}
	}
	if as_, aok := a.(ast.SliceType); aok {
		if bs, bok := b.(ast.SliceType); bok {
			if as_.Elem == nil {
				return bs
			}
			if bs.Elem == nil {
				return as_
			}
		}
	}
	return nil
}

// maybeWrapForUnion is the implicit-wrap sugar for union types
// declared via `type X = A | B | C;`. When `dst` is a union enum
// and `srcType` is one of its struct members, we rewrite
// `*holder` from the bare struct expression `Add { l:1, r:2 }`
// into the equivalent variant call `Add(Add { l:1, r:2 })` and
// re-type-check it so the rest of the pipeline (variant call
// registration, IR lowering of EnumLit, codegen) handles it
// uniformly with the explicit form.
//
// Returns the (possibly rewritten) type; callers should bind
// the return value before consulting `assignable`. No-op when
// dst isn't a union, src isn't a struct, or the union has no
// matching variant — leaves the holder untouched.
//
// Pinned to the union-desugar shape: variant name == struct
// name AND the variant has exactly one positional payload of
// the matching struct type. Hand-written enums whose variants
// happen to satisfy this shape will also auto-wrap; this is
// intentional and matches the natural reading of the variant.
func (c *checker) maybeWrapForUnion(dst ast.Type, holder *ast.Expr, srcType ast.Type, s *scope) ast.Type {
	if holder == nil || *holder == nil {
		return srcType
	}
	du, dok := dst.(ast.EnumType)
	if !dok {
		return srcType
	}
	ss, sok := srcType.(ast.StructType)
	if !sok {
		return srcType
	}
	ed, edOk := c.info.Enums[du.Name]
	if !edOk {
		return srcType
	}
	matched := false
	for _, v := range ed.Variants {
		if v.Name != ss.Name || len(v.Payloads) != 1 {
			continue
		}
		ps, ok := v.Payloads[0].(ast.StructType)
		if !ok || ps.Name != ss.Name {
			continue
		}
		matched = true
		break
	}
	if !matched {
		return srcType
	}
	src := *holder
	wrapped := &ast.Call{
		P:      src.Pos(),
		Callee: &ast.Ident{P: src.Pos(), Name: ss.Name},
		Args:   []ast.Expr{src},
	}
	*holder = wrapped
	return c.checkExpr(wrapped, s)
}

func assignable(dst, src ast.Type) bool {
	if ast.Equal(dst, src) {
		return true
	}
	// Pointer-shaped values ↔ usize. The prelude's raw-pointer
	// helpers (__load_ptr / __store_ptr / __alloc) declare their
	// pointer params + result as usize so the full 8-byte address
	// survives on arm64-darwin. User-code pointer values (string,
	// Map, T[], [T], struct) flow into these helpers without an
	// explicit `as` cast — runtime representation is the same
	// pointer, only the type-level view changes. Mirrors the
	// CastExpr machinery that already allows the explicit `as
	// usize` hop.
	if dn, ok := dst.(ast.NumberType); ok && dn.IsPointerWidth() {
		switch src.(type) {
		case ast.ArrayType, ast.SliceType, ast.StringType, ast.StructType:
			return true
		}
	}
	if sn, ok := src.(ast.NumberType); ok && sn.IsPointerWidth() {
		switch dst.(type) {
		case ast.ArrayType, ast.SliceType, ast.StringType, ast.StructType:
			return true
		}
	}
	// usize ↔ i32 / i64 at assignment boundaries. The prelude's
	// internal helpers return usize for pointer-shaped values;
	// existing user code passing the same value to an i32-typed
	// param (or storing in an i32 var) needs to type-check
	// without an explicit `as i32` everywhere. The narrowing
	// case (usize → i32) is the existing behavior; the bug fix
	// kicks in at the LOAD/STORE level where usize values stay
	// wide internally. Bidirectional for the wide case too.
	if dn, ok := dst.(ast.NumberType); ok && dn.IsPointerWidth() {
		if _, sok := src.(ast.NumberType); sok {
			return true
		}
	}
	if sn, ok := src.(ast.NumberType); ok && sn.IsPointerWidth() {
		if _, dok := dst.(ast.NumberType); dok {
			return true
		}
	}
	// Option[usize] / Option[V] cross-assign for the codegen
	// alias boundary. `__method_Map_get(Map[K, V]): Option[V]`
	// (user-facing) routes to `__map_get_impl(m: usize):
	// Option[usize]` (prelude). The user-code Option[V] flows
	// through the prelude's Option[usize] return without an
	// explicit cast — same pointer, different type-level view.
	if de, dok := dst.(ast.EnumType); dok {
		if se, sok := src.(ast.EnumType); sok && de.Name == se.Name && len(de.Args) == len(se.Args) {
			allOk := true
			for i := range de.Args {
				if !assignable(de.Args[i], se.Args[i]) {
					allOk = false
					break
				}
			}
			if allOk {
				return true
			}
		}
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
	c.errors = append(c.errors, &Error{Pos: pos, Msg: fmt.Sprintf(format, args...), Path: c.currentFile()})
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
		Path: c.currentFile(),
	}
	if suggestion != "" {
		e.Note = fmt.Sprintf("did you mean %q?", suggestion)
	}
	c.errors = append(c.errors, e)
}

// currentFile returns the SourceModule path of the FuncDecl the
// checker is currently inside, or "" when no decl is active (the
// top-level pre-checking phase that registers structs / enums).
// LSP workspace mode uses this for per-file diagnostic routing.
func (c *checker) currentFile() string {
	if c.current == nil {
		return ""
	}
	return c.current.SourceModule
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
	// Pre-pass: detect mutual-recursion SCCs among the sibling
	// local FuncDecls and bind the SCC members' names +
	// signatures in the block's scope up front. Pure forward
	// references (caller declared before callee, no callee→caller
	// back-edge) DON'T pre-bind — they'll hit the regular
	// source-order rule and fail with `undefined identifier`
	// just like before this change. This keeps the runtime
	// env-init-order semantics intact for non-cycle siblings
	// (whose `var <name> = MakeClosure{...}` IS initialised in
	// source order — a caller declared after the callee can
	// still resolve a normal capture).
	prevMutualRec := c.mutualRecSiblings
	var localFns []*ast.FuncDecl
	for _, st := range b.Stmts {
		fn, ok := st.(*ast.FuncDecl)
		if !ok || !fn.IsLocal {
			continue
		}
		localFns = append(localFns, fn)
	}
	if len(localFns) > 1 {
		c.mutualRecSiblings = detectMutualRecSCCs(localFns)
		for _, fn := range localFns {
			if c.mutualRecSiblings[fn.Name] {
				sig := &ast.FuncType{Result: fn.ReturnType}
				for _, p := range fn.Params {
					sig.Params = append(sig.Params, p.Type)
				}
				s.names[fn.Name] = sig
			}
		}
	} else {
		c.mutualRecSiblings = nil
	}
	for _, st := range b.Stmts {
		c.checkStmt(st, s)
	}
	c.mutualRecSiblings = prevMutualRec
}

// detectMutualRecSCCs computes the names that participate in a
// mutual-recursion SCC among `localFns`. A name is in an SCC of
// size ≥ 2 iff there's a cycle of references through other
// sibling names that comes back to it. Uses Tarjan's algorithm
// to keep the work O(V + E) over the sibling-reference graph.
//
// Self-cycles (a single function referencing itself) are
// excluded — recursive self-calls have a separate rewrite path
// (#567) and don't need the env-cycle workaround.
func detectMutualRecSCCs(fns []*ast.FuncDecl) map[string]bool {
	siblings := map[string]*ast.FuncDecl{}
	for _, fn := range fns {
		siblings[fn.Name] = fn
	}
	// Build adjacency: fn name → set of sibling names referenced
	// in the body.
	adj := map[string][]string{}
	for _, fn := range fns {
		seen := map[string]bool{}
		walkBodyForNames(fn.Body, fn.Name, siblings, seen)
		out := make([]string, 0, len(seen))
		for name := range seen {
			out = append(out, name)
		}
		adj[fn.Name] = out
	}
	// Tarjan's SCC algorithm.
	index := 0
	indices := map[string]int{}
	lowlinks := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	out := map[string]bool{}
	var strongconnect func(name string)
	strongconnect = func(name string) {
		indices[name] = index
		lowlinks[name] = index
		index++
		stack = append(stack, name)
		onStack[name] = true
		for _, succ := range adj[name] {
			if _, ok := indices[succ]; !ok {
				strongconnect(succ)
				if lowlinks[succ] < lowlinks[name] {
					lowlinks[name] = lowlinks[succ]
				}
			} else if onStack[succ] {
				if indices[succ] < lowlinks[name] {
					lowlinks[name] = indices[succ]
				}
			}
		}
		if lowlinks[name] == indices[name] {
			// Pop the SCC off the stack.
			var scc []string
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[top] = false
				scc = append(scc, top)
				if top == name {
					break
				}
			}
			if len(scc) >= 2 {
				for _, n := range scc {
					out[n] = true
				}
			}
		}
	}
	for _, fn := range fns {
		if _, ok := indices[fn.Name]; !ok {
			strongconnect(fn.Name)
		}
	}
	return out
}

// walkBodyForNames walks an AST body and records which sibling
// names (in `siblings`) it references. Skips the function's own
// name — self-references go through the recursive-self-call
// path, not the mutual-rec SCC machinery.
func walkBodyForNames(b *ast.Block, selfName string, siblings map[string]*ast.FuncDecl, seen map[string]bool) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		walkStmtForNames(st, selfName, siblings, seen)
	}
}

func walkStmtForNames(s ast.Stmt, selfName string, siblings map[string]*ast.FuncDecl, seen map[string]bool) {
	switch n := s.(type) {
	case *ast.Block:
		walkBodyForNames(n, selfName, siblings, seen)
	case *ast.If:
		walkExprForNames(n.Cond, selfName, siblings, seen)
		walkStmtForNames(n.Then, selfName, siblings, seen)
		walkStmtForNames(n.Else, selfName, siblings, seen)
	case *ast.IfLet:
		walkExprForNames(n.Source, selfName, siblings, seen)
		walkStmtForNames(n.Then, selfName, siblings, seen)
		walkStmtForNames(n.Else, selfName, siblings, seen)
	case *ast.LetElse:
		walkExprForNames(n.Source, selfName, siblings, seen)
		walkBodyForNames(n.Else, selfName, siblings, seen)
	case *ast.While:
		walkExprForNames(n.Cond, selfName, siblings, seen)
		walkStmtForNames(n.Body, selfName, siblings, seen)
	case *ast.For:
		walkStmtForNames(n.Init, selfName, siblings, seen)
		walkExprForNames(n.Cond, selfName, siblings, seen)
		walkStmtForNames(n.Step, selfName, siblings, seen)
		walkStmtForNames(n.Body, selfName, siblings, seen)
	case *ast.Return:
		walkExprForNames(n.Value, selfName, siblings, seen)
	case *ast.Var:
		walkExprForNames(n.Init, selfName, siblings, seen)
	case *ast.Destructure:
		walkExprForNames(n.Init, selfName, siblings, seen)
	case *ast.ExprStmt:
		walkExprForNames(n.Expr, selfName, siblings, seen)
	case *ast.Switch:
		walkExprForNames(n.Tag, selfName, siblings, seen)
		for _, k := range n.Cases {
			walkBodyForNames(k.Body, selfName, siblings, seen)
		}
		walkBodyForNames(n.Default, selfName, siblings, seen)
	case *ast.Match:
		walkExprForNames(n.Tag, selfName, siblings, seen)
		for _, arm := range n.Arms {
			if arm.Literal != nil {
				walkExprForNames(arm.Literal, selfName, siblings, seen)
			}
			walkExprForNames(arm.Guard, selfName, siblings, seen)
			walkBodyForNames(arm.Body, selfName, siblings, seen)
		}
	case *ast.Defer:
		walkExprForNames(n.Expr, selfName, siblings, seen)
	case *ast.Arena:
		walkBodyForNames(n.Body, selfName, siblings, seen)
	}
}

func walkExprForNames(e ast.Expr, selfName string, siblings map[string]*ast.FuncDecl, seen map[string]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Ident:
		if n.Name != selfName {
			if _, ok := siblings[n.Name]; ok {
				seen[n.Name] = true
			}
		}
	case *ast.Binary:
		walkExprForNames(n.Left, selfName, siblings, seen)
		walkExprForNames(n.Right, selfName, siblings, seen)
	case *ast.Unary:
		walkExprForNames(n.Operand, selfName, siblings, seen)
	case *ast.CastExpr:
		walkExprForNames(n.Inner, selfName, siblings, seen)
	case *ast.SliceExpr:
		walkExprForNames(n.Source, selfName, siblings, seen)
		walkExprForNames(n.Low, selfName, siblings, seen)
		walkExprForNames(n.High, selfName, siblings, seen)
	case *ast.Call:
		walkExprForNames(n.Callee, selfName, siblings, seen)
		for _, a := range n.Args {
			walkExprForNames(a, selfName, siblings, seen)
		}
	case *ast.Index:
		walkExprForNames(n.Array, selfName, siblings, seen)
		walkExprForNames(n.Idx, selfName, siblings, seen)
	case *ast.ArrayLit:
		for _, el := range n.Elems {
			walkExprForNames(el, selfName, siblings, seen)
		}
	case *ast.Assign:
		walkExprForNames(n.Target, selfName, siblings, seen)
		walkExprForNames(n.Value, selfName, siblings, seen)
	case *ast.IfExpr:
		walkExprForNames(n.Cond, selfName, siblings, seen)
		walkExprForNames(n.Then, selfName, siblings, seen)
		walkExprForNames(n.Else, selfName, siblings, seen)
	case *ast.TryOp:
		walkExprForNames(n.Inner, selfName, siblings, seen)
	case *ast.MatchExpr:
		walkExprForNames(n.Tag, selfName, siblings, seen)
		for _, arm := range n.Arms {
			if arm.Literal != nil {
				walkExprForNames(arm.Literal, selfName, siblings, seen)
			}
			walkExprForNames(arm.Guard, selfName, siblings, seen)
			walkExprForNames(arm.Body, selfName, siblings, seen)
		}
	case *ast.StructLit:
		for _, f := range n.Fields {
			walkExprForNames(f.Value, selfName, siblings, seen)
		}
	case *ast.FieldAccess:
		walkExprForNames(n.Target, selfName, siblings, seen)
	case *ast.FString:
		for _, p := range n.Parts {
			walkExprForNames(p.Expr, selfName, siblings, seen)
		}
		walkExprForNames(n.Desugared, selfName, siblings, seen)
	case *ast.TupleLit:
		for _, el := range n.Elems {
			walkExprForNames(el, selfName, siblings, seen)
		}
	case *ast.MapLit:
		for _, ent := range n.Entries {
			walkExprForNames(ent.Key, selfName, siblings, seen)
			walkExprForNames(ent.Value, selfName, siblings, seen)
		}
	case *ast.Lambda:
		walkBodyForNames(n.Body, selfName, siblings, seen)
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
		// Refresh `got` from the post-settle AST — the
		// `Var` path does the same via `postSettleType`,
		// and a tuple / numeric-literal return would
		// otherwise compare the pre-settle width against
		// the function's declared return type.
		got = postSettleType(n.Value, got)
		got = c.maybeWrapForUnion(want, &n.Value, got, s)
		// Same generic-call destination refinement as the
		// Var case — `return f(...)` from a function returning
		// Result[i32, i32] needs f's TypeArgs to be fully
		// concrete before monomorph runs.
		c.refineCallTypeArgsFromDest(n.Value, want)
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
			// Generic-call destination inference: when the
			// init is a generic call whose TypeArgs got
			// partially inferred (e.g. variant constructor
			// args only pin one of Result[T, E]'s two type
			// params), refine using the destination type. See
			// refineCallTypeArgsFromDest for the full story.
			c.refineCallTypeArgsFromDest(n.Init, n.Type)
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
		} else if got != nil {
			got = c.maybeWrapForUnion(n.Type, &n.Init, got, s)
			if !assignable(n.Type, got) {
				c.errf(n.P, "cannot assign %s to variable of type %s", got, n.Type)
			}
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
		// Non-enum scrutinee: every arm must be a literal pattern
		// or the wildcard. Dispatch via the literal-pattern shape.
		// Conventional types are number / string / bool — anything
		// else is an error (struct / array / etc. don't have a
		// reasonable equality match yet).
		c.checkLiteralMatch(n, tagT, s)
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
		// Validate the optional qualifier on the variant pattern.
		// Two forms:
		//   1. Module qualifier (`mod.TokA`): modload rewrote
		//      it to the canonical module path so an equality
		//      comparison against the enum's SourceModule is
		//      enough.
		//   2. Enum qualifier (`Color.Red`): names the scrutinee
		//      enum directly. modload leaves these intact (it
		//      detects the qualifier as a known enum name and
		//      suppresses the "unknown module" error).
		if arm.VariantModule != "" {
			if _, qualIsEnum := c.info.Enums[arm.VariantModule]; qualIsEnum {
				if arm.VariantModule != ed.Name {
					c.errf(arm.P, "variant pattern qualifier %q does not match scrutinee enum %s",
						arm.VariantModule, ed.Name)
				}
			} else if ed.SourceModule != "" && arm.VariantModule != ed.SourceModule {
				c.errf(arm.P, "variant pattern qualifier names module %q, but enum %s lives in module %q",
					arm.VariantModule, ed.Name, ed.SourceModule)
			}
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

// checkLiteralMatch handles `match (n) { 0 => …, _ => … }` where
// the scrutinee is a number / string / bool. Every arm must
// carry a literal pattern or be a wildcard; literals must
// type-check against the scrutinee's type. Exhaustiveness:
// a trailing unguarded `_` is required (we don't enumerate
// integer / string domains, and bool exhaustiveness via the
// two-literal form is intentionally NOT special-cased — the
// `_` arm covers it more uniformly).
func (c *checker) checkLiteralMatch(n *ast.Match, tagT ast.Type, s *scope) {
	sawWildcard := false
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
			c.checkBlock(arm.Body, s)
			continue
		}
		if arm.Literal == nil {
			c.errf(arm.P, "match on non-enum value `%s` only accepts literal patterns or `_`", tagT)
			c.checkBlock(arm.Body, s)
			continue
		}
		litT := c.checkExpr(arm.Literal, s)
		if litT != nil {
			c.settleNumeric(arm.Literal, tagT)
			litT = postSettleType(arm.Literal, litT)
			if !assignable(litT, tagT) {
				c.errf(arm.P, "literal pattern of type %s does not match scrutinee type %s", litT, tagT)
			}
		}
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard, s)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errf(arm.Guard.Pos(), "match guard must be boolean, got %s", gt)
			}
		}
		c.checkBlock(arm.Body, s)
	}
	if !sawWildcard {
		c.errf(n.P, "match on non-enum value is not exhaustive — add an unguarded `_` arm")
	}
}

// checkLiteralMatchExpr is the expression-form counterpart of
// checkLiteralMatch. Each arm body is an Expr and the unified
// arm type is returned as the match-expression's result.
func (c *checker) checkLiteralMatchExpr(n *ast.MatchExpr, tagT ast.Type, s *scope) ast.Type {
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
		if assignable(armT, result) {
			return
		}
		if assignable(result, armT) {
			result = armT
			return
		}
		c.errf(p, "match arms have incompatible types: %s vs %s", result, armT)
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
			unify(c.checkExpr(arm.Body, s), arm.P)
			continue
		}
		if arm.Literal == nil {
			c.errf(arm.P, "match on non-enum value `%s` only accepts literal patterns or `_`", tagT)
			continue
		}
		litT := c.checkExpr(arm.Literal, s)
		if litT != nil {
			c.settleNumeric(arm.Literal, tagT)
			litT = postSettleType(arm.Literal, litT)
			if !assignable(litT, tagT) {
				c.errf(arm.P, "literal pattern of type %s does not match scrutinee type %s", litT, tagT)
			}
		}
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard, s)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errf(arm.Guard.Pos(), "match guard must be boolean, got %s", gt)
			}
		}
		unify(c.checkExpr(arm.Body, s), arm.P)
	}
	if !sawWildcard {
		c.errf(n.P, "match on non-enum value is not exhaustive — add an unguarded `_` arm")
	}
	return result
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
		// Non-enum scrutinee: arms are literal patterns + a
		// wildcard. Delegate to the literal-pattern branch.
		return c.checkLiteralMatchExpr(n, tagT, s)
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
		// Reuse the same widening rules as IfExpr (covers
		// polymorphic-numeric vs concrete-numeric and the
		// no-payload-vs-with-payload enum match). Falling
		// through to ast.Equal first preserves existing
		// behaviour for already-aligned pairs.
		if unified := unifyIfArms(result, armT); unified != nil {
			result = unified
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
		// Same qualifier validation as the stmt-form arm loop above.
		if arm.VariantModule != "" && ed.SourceModule != "" && arm.VariantModule != ed.SourceModule {
			c.errf(arm.P, "variant pattern qualifier names module %q, but enum %s lives in module %q",
				arm.VariantModule, ed.Name, ed.SourceModule)
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
// function looks up the call's callee, finds the trailing
// function-typed parameter (the callback slot the callback is
// being passed into), and stamps the callback's first parameter
// from there.
//
// For a generic callee (e.g. `each[T](items: T[], cb: (T) => U)`)
// the callback's first param references a type parameter `T`.
// We unify each non-callback arg's checked type against the
// corresponding sig param to build a substitution map, then
// apply the map to the callback's first param. The args get
// type-checked here AND again when the surrounding call is
// visited; `checkExpr` is idempotent for the shapes that reach
// this path (literals settle once, identifiers look up the same
// scope each time).
//
// On failure (callee not a bare identifier we can resolve, the
// receiving param isn't function-typed, or generic inference
// couldn't pin every type parameter the callback's first param
// references) records an error pointing at the `use` site.
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
	bindType := cbSig.Params[0]

	// Generic callee: solve type parameters from the args the
	// user already wrote. The callback arg is the LAST sig
	// param (skipped — we're inferring its shape).
	if _, isGen := c.info.GenericFuncs[id.Name]; isGen {
		sub := map[string]ast.Type{}
		for i, arg := range src.Args {
			if i >= len(sig.Params)-1 {
				break
			}
			argType := c.checkExpr(arg, outer)
			if argType == nil {
				continue
			}
			c.unifyType(sig.Params[i], argType, sub)
		}
		resolved := substituteType(bindType, sub)
		if containsParamType(resolved) {
			c.errf(fn.P, "use: could not infer binding type for %q from its arguments — add an explicit `: TYPE` annotation", id.Name)
			return
		}
		bindType = resolved
	}

	fn.Params[0].Type = bindType
}

// containsParamType reports whether t (or any of its component
// types) is a still-unresolved generic ParamType. Used to flag
// failed `use`-callback inference: a substitution that leaves a
// ParamType behind means some type-parameter wasn't pinned by
// the args.
func containsParamType(t ast.Type) bool {
	switch x := t.(type) {
	case ast.ParamType:
		return true
	case ast.ArrayType:
		return containsParamType(x.Elem)
	case ast.SliceType:
		return containsParamType(x.Elem)
	case ast.TupleType:
		for _, e := range x.Elems {
			if containsParamType(e) {
				return true
			}
		}
		return false
	case ast.EnumType:
		for _, a := range x.Args {
			if containsParamType(a) {
				return true
			}
		}
		return false
	case ast.StructType:
		for _, a := range x.Args {
			if containsParamType(a) {
				return true
			}
		}
		return false
	case *ast.FuncType:
		for _, p := range x.Params {
			if containsParamType(p) {
				return true
			}
		}
		return containsParamType(x.Result)
	}
	return false
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
	// Snapshot the mutual-recursion sibling set at this
	// declaration point. Nested blocks inside `fn.Body`
	// overwrite c.mutualRecSiblings as their own pre-pass runs,
	// but capture analysis needs the OUTER block's set to
	// recognise (and skip) cycle members referenced inside this
	// body. Non-cycle forward references aren't in this set and
	// capture normally.
	mySiblings := c.mutualRecSiblings
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
		// Mutual-recursion sibling: handled by closureconv's
		// null-env direct-call rewrite, not via env capture.
		// Capturing here would create a cycle (each closure's env
		// referencing the other's pair pointer, which exists only
		// after BOTH pairs are built). Skipping means each SCC
		// member hoists to a zero-capture closure and the body's
		// sibling calls bypass the env entirely. ONLY names in a
		// detected SCC are skipped — plain forward refs (caller
		// references callee, callee doesn't reference back) still
		// capture normally.
		if mySiblings[name] {
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
	// Push (sink, scope) for the deeper-lookup chain. The order
	// matters: outermost-first so the lookup walks from
	// immediately-enclosing inward, capturing transitively.
	c.captureChain = append(c.captureChain, captureEntry{sink: c.captureSink, scope: outer})
	defer func() {
		c.current = prev
		c.captureSink = prevSink
		c.captureOuter = prevOuter
		c.captureChain = c.captureChain[:len(c.captureChain)-1]
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
		// Any owned array, slice, string, or struct → i32 / usize —
		// recover the data / wrapper pointer for the bulk-
		// memory primitives. All four lower to a single pointer
		// at runtime; the cast is the source-level escape hatch
		// the prelude uses to call __memcpy / __store_ptr against
		// the underlying memory. i32 stays the historical hop
		// (truncates to 32 bits on natives — fine until heap
		// > 4 GiB); usize is the target-aware shape that
		// preserves the full 8-byte address on arm64-darwin.
		if nt, ok := n.Target.(ast.NumberType); ok && (nt.NormalWidth() == 32 || nt.IsPointerWidth()) {
			switch inner.(type) {
			case ast.ArrayType, ast.SliceType, ast.StringType, ast.StructType:
				return n.Target
			}
		}
		// Reverse direction: `i32 / usize → T[]`, `→ string`,
		// and `→ struct` promote a raw pointer back to a typed
		// handle. The runtime layout is identical (lang ABI for
		// arrays/strings is "value = data pointer, length prefix
		// at base-4"; for structs, "value = base pointer, fields
		// at constant offsets") — only the type-level view
		// changes. Used by the prelude when a builtin returns a
		// freshly allocated raw block that the caller wants to
		// expose as a typed collection (`__array_append_string`'s
		// rebuild loop) or as a wrapper struct (`map_new`'s
		// Map handle).
		if nt, ok := inner.(ast.NumberType); ok && (nt.NormalWidth() == 32 || nt.IsPointerWidth()) {
			switch n.Target.(type) {
			case ast.ArrayType, ast.StringType, ast.StructType:
				return n.Target
			}
		}
		// Type ascription. The numeric-only `as` form above is a
		// runtime conversion (truncate / sign-extend / float-round).
		// This branch is the opposite: a zero-cost annotation that
		// lets the user pin a type inline where the inference can't
		// reach. The classic case is a payload-less variant —
		// `None as Option[i32]`, `[] as i32[]` — but the rule
		// generalises to anything the existing `var x: T = e`
		// destination-flow already accepts. Run the same flow
		// (settle polymorphic numerics, stamp struct args, refine
		// generic-call type args, union-wrap), then accept when the
		// result is `assignable` to the target. IR / interp see a
		// CastExpr whose inner has already evaluated; nothing more
		// to lower.
		c.stampStructTypeArgs(n.Inner, n.Target)
		c.refineCallTypeArgsFromDest(n.Inner, n.Target)
		inner = c.maybeWrapForUnion(n.Target, &n.Inner, inner, s)
		n.InnerType = inner
		if assignable(n.Target, inner) {
			return n.Target
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
		// Inside a local function: a name not found in the local
		// scope might resolve in the enclosing function's scope.
		// Record it as a capture and return its outer type. This
		// check fires before the FuncSigs lookup below — local
		// FuncDecls register in FuncSigs too, and FuncSigs alone
		// would short-circuit the capture path for a sibling
		// local function name (`function outer() { return inner; }`
		// where both inner and outer are local to the same outer
		// function). The outer scope is the authoritative source
		// for whether a sibling needs capturing.
		//
		// Walk the captureChain outermost-first. The deepest level
		// (current local function) is the LAST entry; the first
		// entry to hit is the immediately-enclosing function. When
		// a name resolves three levels up, every intermediate
		// function captures it so the env blocks chain the
		// reference down to the deepest reader.
		if len(c.captureChain) > 0 {
			for i := len(c.captureChain) - 1; i >= 0; i-- {
				ent := c.captureChain[i]
				if ent.scope == nil {
					continue
				}
				if t, ok := ent.scope.lookup(n.Name); ok {
					// Record the capture in this entry's sink AND
					// in every deeper sink (so each intermediate
					// closure forwards the slot through).
					for j := i; j < len(c.captureChain); j++ {
						if c.captureChain[j].sink != nil {
							c.captureChain[j].sink(n.Name, t)
						}
					}
					return t
				}
			}
		}
		if sig, ok := c.info.FuncSigs[n.Name]; ok {
			return sig
		}
		// A bare name might be a payload-less enum variant.
		// (Variants with payloads are constructed via Call and
		// rejected here so the user gets a clearer error.) The
		// optional `n.EnumName` qualifier (set by the FieldAccess
		// rewrite for `Color.Red`) restricts the lookup to one
		// specific enum.
		if vr, ok, multi := c.resolveVariant(n.Name, n.EnumName); ok {
			if len(vr.payloads) > 0 {
				c.errf(n.P, "variant %s expects %d payload argument(s); call it as %s(...)",
					n.Name, len(vr.payloads), n.Name)
				return nil
			}
			n.EnumName = vr.enumName
			return ast.EnumType{Name: vr.enumName}
		} else if multi {
			c.errf(n.P, "variant %q is declared in multiple enums (%s) — qualify the reference, e.g. `%s.%s`",
				n.Name, c.variantEnumList(n.Name), c.variantOf[n.Name][0].enumName, n.Name)
			return nil
		} else if n.EnumName != "" {
			c.errf(n.P, "enum %s has no variant %q", n.EnumName, n.Name)
			return nil
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
			if t == nil || elemT == nil {
				continue
			}
			if ast.Equal(t, elemT) {
				continue
			}
			// Allow polymorphic-vs-concrete and the
			// argless-enum-vs-with-args shapes that
			// `unifyIfArms` already handles for if-expression
			// arms. Picks the concrete side so a later
			// `settleNumeric` walk against the destination
			// element type still has a fixed point to land
			// on. Mirrors the cohort of fixes that make
			// numeric / enum widening work across the
			// language's other "two-arm" positions.
			if unified := unifyIfArms(elemT, t); unified != nil {
				elemT = unified
				continue
			}
			c.errf(el.Pos(), "array element type %s, expected %s", t, elemT)
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
		// A `Color.Red(payload)` qualified-variant call parses as
		// Call{Callee: FieldAccess{Target: Ident{Color}, Field:
		// "Red"}}. Detect that shape, look up the variant on the
		// named enum, and rewrite Callee to a plain Ident with
		// EnumName stamped so the rest of this branch (and the IR's
		// `lookupVariant`) handle it uniformly with the unqualified
		// form. Only fires when the target is a known enum name —
		// every other FieldAccess (struct field, method call) flows
		// down the usual path.
		if fa, ok := n.Callee.(*ast.FieldAccess); ok {
			if tid, ok := fa.Target.(*ast.Ident); ok {
				if _, isEnum := c.info.Enums[tid.Name]; isEnum {
					n.Callee = &ast.Ident{P: fa.P, Name: fa.Field, EnumName: tid.Name}
				}
			}
		}
		if id, ok := n.Callee.(*ast.Ident); ok {
			vr, vrOk, vrMulti := c.resolveVariant(id.Name, id.EnumName)
			isVar := vrOk && !c.isUserFuncOrLocal(id.Name, s)
			// Bare-name reference to a variant that lives in two
			// or more enums — the call site has to qualify.
			// Report once, then fall through (vrOk=false) so the
			// usual function-call path runs and the caller still
			// gets a follow-up "undefined identifier" diag if the
			// name resolves to nothing else.
			if !isVar && vrMulti && id.EnumName == "" && !c.isUserFuncOrLocal(id.Name, s) {
				c.errf(n.P, "variant %q is declared in multiple enums (%s) — qualify the reference, e.g. `%s.%s(...)`",
					id.Name, c.variantEnumList(id.Name), c.variantOf[id.Name][0].enumName, id.Name)
				return nil
			}
			if isVar {
				if len(n.Args) != len(vr.payloads) {
					c.errf(n.P, "variant %s expects %d argument(s), got %d",
						id.Name, len(vr.payloads), len(n.Args))
				}
				id.EnumName = vr.enumName
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
				// Tag this Call as a variant constructor so later
				// passes that need to gate on the variant-vs-fn
				// distinction (postSettleType) can do so without
				// the previous case-sensitivity heuristic.
				n.IsVariantCall = true
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
				if mangled, ok := c.info.Methods[key]; ok && c.methodVisibleHere(mangled) {
					// Preserve the source-level call site so the LSP
					// can resolve hover / goto-def on `area` in
					// `p.area()` after we rewrite the AST to a
					// mangled flat call.
					n.Method = &ast.MethodCallSite{
						Field:    fa.Field,
						FieldPos: fa.FieldPos,
						Receiver: tt,
					}
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
					// `arr.push(v)`'s lowering happens inline at
					// the IR layer (emitArrayPush) — no per-
					// stride dispatch required here.
					if at, ok := tt.(ast.ArrayType); ok {
						n.TypeArgs = []ast.Type{at.Elem}
					}
					// Wide-V Map: `m.values()` is intercepted by
					// the IR (emitMapValues) which dispatches by
					// V stride — narrow V routes to the existing
					// `__map_values_impl` lang prelude function,
					// wide V (i64 / u64 / f64) follows each entry's
					// cell pointer + `__memcpy`s the 8 payload
					// bytes into a real wide-stride result. Both
					// share the same mangled name; the IR sees
					// the receiver's V via `b.exprType(args[0])`.
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
		// If the callee resolves to a generic FuncDecl, build its
		// type-arg substitution. Two source shapes:
		// - Explicit: `f[i32](42)` — the parser stamps `n.TypeArgs`
		//   directly. We seed `sub` from those args, paired with
		//   the FuncDecl's TypeParams declaration order.
		// - Inferred: `f(42)` — `sub` starts empty and gets filled
		//   in by walking args against the function's declared
		//   params via unifyType below.
		var sub map[string]ast.Type
		var genericFn *ast.FuncDecl
		if id, ok := n.Callee.(*ast.Ident); ok {
			if fn, isGen := c.info.GenericFuncs[id.Name]; isGen {
				genericFn = fn
				sub = make(map[string]ast.Type, len(fn.TypeParams))
				if len(n.TypeArgs) > 0 {
					if len(n.TypeArgs) != len(fn.TypeParams) {
						c.errf(n.P, "%s expects %d type argument(s), got %d",
							fn.Name, len(fn.TypeParams), len(n.TypeArgs))
					}
					for i, tp := range fn.TypeParams {
						if i < len(n.TypeArgs) {
							sub[tp] = n.TypeArgs[i]
						}
					}
				}
			}
		}
		for i := range n.Args {
			at := c.checkExpr(n.Args[i], s)
			if i < len(ft.Params) && at != nil {
				expected := ft.Params[i]
				// Polymorphic-literal settling: `f(1)` where f
				// expects i64 needs the literal to lock in i64
				// before assignable / unifyType run, otherwise
				// the i32-default would mismatch the expected
				// param type.
				c.settleNumeric(n.Args[i], expected)
				at = postSettleType(n.Args[i], at)
				at = c.maybeWrapForUnion(expected, &n.Args[i], at, s)
				// If the arg is itself a generic call with
				// partially-inferred TypeArgs (e.g. `pick(c,
				// Ok(1), Err(2))` returning Result without
				// inner args), refine its TypeArgs from the
				// enclosing call's param type — same shape as
				// the Var / Return destination refinement.
				// Only fires when this call isn't itself
				// generic (i.e. `sub` is nil); generic-into-
				// generic plumbing would need bidirectional
				// substitution we don't have yet.
				if sub == nil {
					c.refineCallTypeArgsFromDest(n.Args[i], expected)
				}
				if sub != nil {
					if !c.unifyType(expected, at, sub) {
						c.errf(n.Args[i].Pos(), "argument %d: expected %s, got %s", i+1, expected, at)
					}
				} else if !assignable(expected, at) {
					c.errf(n.Args[i].Pos(), "argument %d: expected %s, got %s", i+1, expected, at)
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
			// Auto-widen the narrower operand when the two
			// resolved sides differ in width (same signedness
			// already enforced by commonIntegerWidth). This
			// keeps pointer-arithmetic-style code in the
			// prelude — `buf64 + 16` where `buf64` is i64 and
			// `16` is the default i32 NumberLit — type-correct
			// without an explicit `as i64`.
			if ln, ok := lt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Left, ln, common)
			}
			if rn, ok := rt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Right, rn, common)
			}
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
			if ln, ok := lt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Left, ln, common)
			}
			if rn, ok := rt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Right, rn, common)
			}
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
			if ln, ok := lt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Left, ln, common)
			}
			if rn, ok := rt.(ast.NumberType); ok {
				c.widenIntOperand(&n.Right, rn, common)
			}
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
			rt = c.maybeWrapForUnion(lt, &n.Value, rt, s)
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
	case *ast.Lambda:
		// Anonymous function expression — mirror of checkLocalFunc
		// but inlined here so the Lambda gets its captures filled
		// in place. Body scope is fresh-with-params; capture
		// analysis runs against the captureChain so the lambda's
		// reads of outer-scope names flow into its `Captures`
		// list. `c.current` swaps to a synthetic FuncDecl so
		// `return` statements inside the lambda body type-check
		// against the LAMBDA's return type, not the enclosing
		// function's. The Lambda's type is the FuncType built
		// from its declared params + return type.
		//
		// Resolve the lambda's declared param + return types
		// against the enclosing function's type parameters. The
		// `resolveTypesInBlock` pre-pass walks statement-level
		// types but doesn't descend into expression-position
		// Lambda nodes, so `T` in `function (x: T): T { ... }`
		// would otherwise stay as `StructType{T}` and fail to
		// match the outer's `ParamType{T}` at the return-type
		// check. Pulling the enclosing FuncDecl's TypeParams set
		// off `c.current` is sufficient — nested local fns
		// inherit their host's params via this same channel.
		var lambdaParams map[string]bool
		if c.current != nil && len(c.current.TypeParams) > 0 {
			lambdaParams = make(map[string]bool, len(c.current.TypeParams))
			for _, tp := range c.current.TypeParams {
				lambdaParams[tp] = true
			}
			for i := range n.Params {
				c.resolveType(&n.Params[i].Type, lambdaParams)
			}
			c.resolveType(&n.ReturnType, lambdaParams)
		}
		root := newScope(nil)
		for _, p := range n.Params {
			if _, dup := root.names[p.Name]; dup {
				c.errf(n.P, "duplicate parameter %q", p.Name)
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
		c.current = &ast.FuncDecl{
			P:          n.P,
			Params:     n.Params,
			ReturnType: n.ReturnType,
			Body:       n.Body,
		}
		c.loopDepth = 0
		c.switchDepth = 0
		c.captureSink = func(name string, t ast.Type) {
			if _, ok := captured[name]; ok {
				return
			}
			switch t.(type) {
			case ast.VoidType, ast.ParamType:
				c.errf(n.P, "captured variable %q has unsupported type %s", name, t)
			default:
				captured[name] = t
				captureOrder = append(captureOrder, name)
			}
		}
		c.captureOuter = s
		c.captureChain = append(c.captureChain, captureEntry{sink: c.captureSink, scope: s})
		c.checkBlock(n.Body, root)
		c.captureChain = c.captureChain[:len(c.captureChain)-1]
		c.captureSink = prevSink
		c.captureOuter = prevOuter
		c.loopDepth = prevLoop
		c.switchDepth = prevSwitch
		c.current = prev
		for _, name := range captureOrder {
			n.Captures = append(n.Captures, ast.Param{Name: name, Type: captured[name]})
		}
		ft := &ast.FuncType{Result: n.ReturnType}
		for _, p := range n.Params {
			ft.Params = append(ft.Params, p.Type)
		}
		return ft
	case *ast.IfExpr:
		ct := c.checkExpr(n.Cond, s)
		if ct != nil && !ast.Equal(ct, ast.BoolType{}) {
			c.errf(n.Cond.Pos(), "if-expression condition must be boolean, got %s", ct)
		}
		tt := c.checkExpr(n.Then, s)
		et := c.checkExpr(n.Else, s)
		result := unifyIfArms(tt, et)
		if tt != nil && et != nil && result == nil {
			c.errf(n.P, "if-expression branches differ: %s vs %s", tt, et)
			result = tt
		}
		if result == nil {
			result = tt
		}
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
				// Allow the polymorphic / argless-enum vs
				// concrete widening rules from `unifyIfArms`
				// — e.g. `struct Node { next: Option[Node] }`
				// initialised with `next: None` (which checks
				// to `Option` with empty Args). Same shape as
				// the array-element widening from #541.
				if unifyIfArms(expected, vt) == nil {
					c.errf(f.Value.Pos(), "field %q: expected %s, got %s", f.Name, expected, vt)
				}
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
		// Qualified payload-less variant: `Color.Red`. Detect
		// before recursing into Target — the Target Ident names an
		// enum type, not a value, so the usual `checkExpr` path
		// would (correctly) reject it as undefined. The qualified-
		// variant-call shape (`Color.Red(payload)`) is handled in
		// the *ast.Call branch.
		if tid, ok := n.Target.(*ast.Ident); ok {
			if _, isEnum := c.info.Enums[tid.Name]; isEnum {
				if vr, ok, _ := c.resolveVariant(n.Field, tid.Name); ok {
					if len(vr.payloads) > 0 {
						c.errf(n.P, "variant %s.%s expects %d payload argument(s); call it as %s.%s(...)",
							tid.Name, n.Field, len(vr.payloads), tid.Name, n.Field)
						return nil
					}
					return ast.EnumType{Name: vr.enumName}
				}
				c.errf(n.P, "enum %s has no variant %q", tid.Name, n.Field)
				return nil
			}
		}
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

// refineCallTypeArgsFromDest pushes the destination type's
// concrete args back into a generic call's TypeArgs when the
// args-driven inference produced an under-specified entry.
//
// Background: variant constructors only fix the type
// parameter(s) they have payloads for. `Ok(1)` sets
// `Result.T → i32` from the payload, but leaves `E` unresolved
// — the variant-call path returns `EnumType{Name:"Result"}`
// (no Args). Pass that through `pick[T](c, a, b): T` and the
// call's TypeArgs gets stamped as `[Result{no args}]`.
// Monomorph mangles to `pick__Result`, the cloned param /
// return types lack the inner Args, and the re-check rejects
// with "Result has 2 type parameter(s), 0 supplied".
//
// The destination annotation (`var r: Result[i32, i32]`,
// `return ...` against a typed fn return) carries the full
// type. We walk the generic fn's declared return type against
// the destination pairwise; wherever the declared return
// position is a `ParamType{Name: T}`, the destination's
// matching position becomes a refined `sub[T]` entry.
// Re-stamping `TypeArgs` from the refined sub completes the
// inference so monomorph sees `[Result[i32, i32]]` and clones
// with the right shape.
func (c *checker) refineCallTypeArgsFromDest(e ast.Expr, dst ast.Type) {
	if e == nil || dst == nil {
		return
	}
	switch x := e.(type) {
	case *ast.Call:
		c.refineSingleCallTypeArgs(x, dst)
	case *ast.IfExpr:
		// Both arms produce dst — recurse into each.
		c.refineCallTypeArgsFromDest(x.Then, dst)
		c.refineCallTypeArgsFromDest(x.Else, dst)
	case *ast.MatchExpr:
		for _, arm := range x.Arms {
			if arm != nil {
				c.refineCallTypeArgsFromDest(arm.Body, dst)
			}
		}
	case *ast.TryOp:
		// `expr?` — the inner expression is Option[dst] or
		// Result[dst, E]; not the same shape as dst itself.
		// Skip; the inner call's TypeArgs (if any) would need
		// the wider Option / Result type, which only the
		// enclosing function's return slot has.
	}
}

// refineSingleCallTypeArgs is the leaf case of
// refineCallTypeArgsFromDest — picks up a Call directly.
func (c *checker) refineSingleCallTypeArgs(call *ast.Call, dst ast.Type) {
	if len(call.TypeArgs) == 0 {
		return
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return
	}
	fn, isGen := c.info.GenericFuncs[id.Name]
	if !isGen || len(call.TypeArgs) != len(fn.TypeParams) {
		return
	}
	sub := make(map[string]ast.Type, len(fn.TypeParams))
	for i, tp := range fn.TypeParams {
		sub[tp] = call.TypeArgs[i]
	}
	refineParamSubFromDest(fn.ReturnType, dst, sub)
	for i, tp := range fn.TypeParams {
		if v, ok := sub[tp]; ok {
			call.TypeArgs[i] = v
		}
	}
}

// refineParamSubFromDest walks `src` (a generic-returning
// function's declared return type — may contain ParamType
// placeholders) against `dst` (the destination's fully
// concrete type) and refines entries in `sub` that are
// under-specified relative to the destination's same-shape
// position.
//
// Conservative: only replaces a sub entry when the new value
// strictly improves on the existing one (the existing entry
// is nil, or is an EnumType/StructType with strictly fewer
// Args than the destination provides). Never widens or
// overrides a fully-resolved entry.
func refineParamSubFromDest(src, dst ast.Type, sub map[string]ast.Type) {
	if src == nil || dst == nil {
		return
	}
	switch s := src.(type) {
	case ast.ParamType:
		existing, has := sub[s.Name]
		if !has {
			sub[s.Name] = dst
			return
		}
		if betterRefinement(existing, dst) {
			sub[s.Name] = dst
		}
	case ast.EnumType:
		dEnum, dok := dst.(ast.EnumType)
		if !dok || dEnum.Name != s.Name {
			return
		}
		// Walk pairwise. Either side may have fewer args than
		// the other (the src side can have ParamType
		// placeholders, the dst side has concrete types).
		n := len(s.Args)
		if len(dEnum.Args) < n {
			n = len(dEnum.Args)
		}
		for i := 0; i < n; i++ {
			refineParamSubFromDest(s.Args[i], dEnum.Args[i], sub)
		}
	case ast.StructType:
		dStruct, dok := dst.(ast.StructType)
		if !dok || dStruct.Name != s.Name {
			return
		}
		n := len(s.Args)
		if len(dStruct.Args) < n {
			n = len(dStruct.Args)
		}
		for i := 0; i < n; i++ {
			refineParamSubFromDest(s.Args[i], dStruct.Args[i], sub)
		}
	case ast.ArrayType:
		if dArr, ok := dst.(ast.ArrayType); ok {
			refineParamSubFromDest(s.Elem, dArr.Elem, sub)
		}
	case ast.SliceType:
		if dSlice, ok := dst.(ast.SliceType); ok {
			refineParamSubFromDest(s.Elem, dSlice.Elem, sub)
		}
	case ast.TupleType:
		dTup, dok := dst.(ast.TupleType)
		if !dok || len(dTup.Elems) != len(s.Elems) {
			return
		}
		for i := range s.Elems {
			refineParamSubFromDest(s.Elems[i], dTup.Elems[i], sub)
		}
	case *ast.FuncType:
		dFn, dok := dst.(*ast.FuncType)
		if !dok || len(dFn.Params) != len(s.Params) {
			return
		}
		for i := range s.Params {
			refineParamSubFromDest(s.Params[i], dFn.Params[i], sub)
		}
		refineParamSubFromDest(s.Result, dFn.Result, sub)
	}
}

// betterRefinement reports whether `candidate` is a strictly
// more-specific version of `existing` — used to decide whether
// the destination-driven refinement should replace what
// args-driven inference produced.
//
// Today only handles the variant-constructor case: an
// EnumType / StructType with fewer Args is improvable when a
// candidate of the same name has more (typically the full
// arity from the destination annotation).
func betterRefinement(existing, candidate ast.Type) bool {
	if existing == nil {
		return candidate != nil
	}
	switch e := existing.(type) {
	case ast.EnumType:
		c, ok := candidate.(ast.EnumType)
		if !ok || c.Name != e.Name {
			return false
		}
		return len(e.Args) < len(c.Args)
	case ast.StructType:
		c, ok := candidate.(ast.StructType)
		if !ok || c.Name != e.Name {
			return false
		}
		return len(e.Args) < len(c.Args)
	}
	return false
}

func (c *checker) settleNumeric(e ast.Expr, hint ast.Type) {
	// TryOp: `Some(EXPR)?` / `Ok(EXPR)?` — the destination's
	// hint applies to the inner expression's payload, not
	// to the TryOp itself. Wrap the hint in the appropriate
	// enum (Option / Result) so the inner variant-call gets
	// its payload settled. Without this, `var v: f64 =
	// Some(3.14)?;` left 3.14 at f32 default and wasm
	// rejected the f64 destination load.
	if to, ok := e.(*ast.TryOp); ok {
		switch to.Kind {
		case ast.TryKindOption:
			c.settleNumeric(to.Inner, ast.EnumType{Name: "Option", Args: []ast.Type{hint}})
		case ast.TryKindResult:
			// Reconstruct Result[T, E] using the encl. fn's
			// error type when known — settling Inner with
			// just the T half is the part that matters for
			// the polymorphic payload.
			args := []ast.Type{hint}
			if c.current != nil {
				if re, ok := c.current.ReturnType.(ast.EnumType); ok && re.Name == "Result" && len(re.Args) == 2 {
					args = append(args, re.Args[1])
				}
			}
			c.settleNumeric(to.Inner, ast.EnumType{Name: "Result", Args: args})
		}
		// Stamp `to.Type` so postSettleType / IR sees the
		// resolved payload width — `to.Type` was set by the
		// original checkExpr from the source's pre-settle
		// `srcEnum.Args[0]`.
		to.Type = hint
	}
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
		// IfExpr / MatchExpr forms: recurse into each arm so
		// `var arr: i64[] = if cond { [...] } else { [...] }`
		// reaches each branch's array literal.
		if al, ok := e.(*ast.ArrayLit); ok {
			al.ElemType = hn.Elem
			for _, el := range al.Elems {
				c.settleNumeric(el, hn.Elem)
			}
		} else if ie, ok := e.(*ast.IfExpr); ok {
			if ie.Then != nil {
				c.settleNumeric(ie.Then, hint)
			}
			if ie.Else != nil {
				c.settleNumeric(ie.Else, hint)
			}
		} else if me, ok := e.(*ast.MatchExpr); ok {
			for _, arm := range me.Arms {
				if arm == nil {
					continue
				}
				c.settleNumeric(arm.Body, hint)
			}
		}
	case ast.SliceType:
		if al, ok := e.(*ast.ArrayLit); ok {
			al.ElemType = hn.Elem
			for _, el := range al.Elems {
				c.settleNumeric(el, hn.Elem)
			}
		} else if ie, ok := e.(*ast.IfExpr); ok {
			if ie.Then != nil {
				c.settleNumeric(ie.Then, hint)
			}
			if ie.Else != nil {
				c.settleNumeric(ie.Else, hint)
			}
		} else if me, ok := e.(*ast.MatchExpr); ok {
			for _, arm := range me.Arms {
				if arm == nil {
					continue
				}
				c.settleNumeric(arm.Body, hint)
			}
		}
	case ast.TupleType:
		// Tuple-literal element-type propagation. Without
		// this, `var p: (string, i64) = ("hi", 100)` rejects
		// the i32-defaulted literal against the i64 slot —
		// each element is checked in isolation by checkExpr.
		// Walk in lockstep so element `i` settles to
		// `hn.Elems[i]`.
		switch tl := e.(type) {
		case *ast.TupleLit:
			if len(tl.Elems) == len(hn.Elems) {
				for i, el := range tl.Elems {
					c.settleNumeric(el, hn.Elems[i])
				}
			}
		case *ast.IfExpr:
			// `return if cond { tup1 } else { tup2 }` — feed
			// the destination tuple type into both arms so
			// each arm's TupleLit settles its elements.
			c.settleNumeric(tl.Then, hint)
			if tl.Else != nil {
				c.settleNumeric(tl.Else, hint)
			}
		case *ast.MatchExpr:
			// `return match (e) { A => tup }` — each arm
			// body is an expression that must produce the
			// destination tuple type.
			for _, arm := range tl.Arms {
				if arm == nil {
					continue
				}
				c.settleNumeric(arm.Body, hint)
			}
		}
	case ast.StructType:
		// Map literal with a destination annotation. The
		// `Map[K, V]` struct's TypeArgs are (key-type,
		// value-type); a `MapLit` with bare-numeric values
		// (`Map { "a": 1234567890123 }`) needs the V to flow
		// into each entry so polymorphic literals settle to
		// the destination's slot width. Without this,
		// `var m: Map[string, i64] = Map { "a": 1234567890123 };`
		// keeps its inferred `Map[string, i32]` shape and
		// the assignable check rejects.
		if ml, ok := e.(*ast.MapLit); ok && hn.Name == "Map" && len(hn.Args) == 2 {
			for _, ent := range ml.Entries {
				c.settleNumeric(ent.Key, hn.Args[0])
				c.settleNumeric(ent.Value, hn.Args[1])
			}
		} else if sl, ok := e.(*ast.StructLit); ok && len(hn.Args) > 0 {
			// Generic struct literal with a destination
			// annotation: `var b: Box[i64] = Box { v: 100 }`.
			// Build the type-param substitution from the hint's
			// Args, look up each field's declared type, and
			// settle each field value against the substituted
			// type so polymorphic literals widen. Also stamp
			// the literal's TypeArgs so postSettleType returns
			// the resolved StructType shape to the assignable
			// check.
			if sd, ok := c.info.Structs[sl.TypeName]; ok && len(sd.TypeParams) == len(hn.Args) {
				sub := map[string]ast.Type{}
				for i, tp := range sd.TypeParams {
					sub[tp] = hn.Args[i]
				}
				fieldT := map[string]ast.Type{}
				for _, f := range sd.Fields {
					fieldT[f.Name] = substituteType(f.Type, sub)
				}
				for _, f := range sl.Fields {
					if expected, present := fieldT[f.Name]; present && expected != nil {
						c.settleNumeric(f.Value, expected)
					}
				}
				sl.TypeArgs = append([]ast.Type{}, hn.Args...)
			}
		} else if ie, ok := e.(*ast.IfExpr); ok {
			// `var m: Map[K, V] = if cond { Map {...} } else
			// { Map {...} }` — fan out the destination Map type
			// into both arms.
			if ie.Then != nil {
				c.settleNumeric(ie.Then, hint)
			}
			if ie.Else != nil {
				c.settleNumeric(ie.Else, hint)
			}
		} else if me, ok := e.(*ast.MatchExpr); ok {
			for _, arm := range me.Arms {
				if arm == nil {
					continue
				}
				c.settleNumeric(arm.Body, hint)
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
		// IfExpr / MatchExpr forms: recurse into each arm
		// body so `return match (e) { A => Some(...) }`
		// against an `Option[i64]` destination reaches the
		// inner variant constructor.
		if ie, ok := e.(*ast.IfExpr); ok {
			if ie.Then != nil {
				c.settleNumeric(ie.Then, hint)
			}
			if ie.Else != nil {
				c.settleNumeric(ie.Else, hint)
			}
			return
		}
		if me, ok := e.(*ast.MatchExpr); ok {
			for _, arm := range me.Arms {
				if arm == nil {
					continue
				}
				c.settleNumeric(arm.Body, hint)
			}
			return
		}
		call, ok := e.(*ast.Call)
		if !ok {
			return
		}
		id, ok := call.Callee.(*ast.Ident)
		if !ok {
			return
		}
		vr, isVariant, _ := c.resolveVariant(id.Name, id.EnumName)
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
	case *ast.IfExpr:
		// `var n: i64 = if cond { 1 } else { 2 }` — settle
		// both arm bodies against the destination width.
		// Without this, the literals stayed i32 and the i64
		// load read garbage high bits.
		if x.Then != nil {
			c.settleInt(x.Then, hn)
		}
		if x.Else != nil {
			c.settleInt(x.Else, hn)
		}
	case *ast.MatchExpr:
		// Same fan-out for `var n: i64 = match (e) { A => 1,
		// B => 2 }` — every arm body is an expression that
		// must reach the destination width.
		for _, arm := range x.Arms {
			if arm == nil {
				continue
			}
			c.settleInt(arm.Body, hn)
		}
	case *ast.Call:
		// Generic-function call whose result type substitutes
		// to the hint's T. The initial check ran with sub[T]
		// = Polymorphic (every NumberLit arg returned a
		// polymorphic NumberType, leaving T un-pinned), and
		// the result type substituted to the same polymorphic
		// shape. Now that the destination commits to a
		// concrete width, walk the args that map to the
		// generic parameter and settle them — both pins the
		// literal widths and lets TypeArgs/monomorph pick the
		// right clone.
		//
		// Gate on the existing TypeArgs entry being polymorphic
		// or zero-width: a call whose args already pinned T to a
		// concrete width (`id(7i64)` → T=i64) should NOT have
		// its TypeArgs overridden by an enclosing-context hint
		// like a CastExpr's target. Without this gate,
		// `(id(7i64) as i32)` flipped id's T to i32, monomorph
		// produced id__i32 with i32-typed params, and the
		// re-check rejected the i64 literal arg.
		if id, ok := x.Callee.(*ast.Ident); ok {
			if fn, isGen := c.info.GenericFuncs[id.Name]; isGen {
				for i, p := range fn.Params {
					if i >= len(x.Args) {
						break
					}
					if pt, ok := p.Type.(ast.ParamType); ok {
						_ = pt
						c.settleInt(x.Args[i], hn)
					}
				}
				// Re-stamp TypeArgs only when the existing entry
				// is polymorphic — meaning args-driven inference
				// didn't fix T at a concrete width. A concrete
				// entry (e.g. from a typed-literal arg) wins
				// over the surrounding-context hint.
				if len(fn.TypeParams) == 1 && len(x.TypeArgs) == 1 &&
					isPolymorphicNumeric(x.TypeArgs[0]) {
					x.TypeArgs[0] = hn
				}
			}
		}
	}
}

// isPolymorphicNumeric reports whether t is an unsettled
// numeric / float type — i.e. came from a bare literal whose
// width hasn't been pinned yet. settleInt's generic-call case
// uses this to decide whether the destination's width hint
// should override the existing TypeArgs entry.
func isPolymorphicNumeric(t ast.Type) bool {
	if n, ok := t.(ast.NumberType); ok {
		return n.Polymorphic || n.Width == 0
	}
	if f, ok := t.(ast.FloatType); ok {
		return f.Polymorphic
	}
	return false
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
	case *ast.IfExpr:
		// `var f: f64 = if cond { 3.14 } else { 0.0 }` —
		// fan the float hint into both arms.
		if x.Then != nil {
			c.settleFloat(x.Then, hf)
		}
		if x.Else != nil {
			c.settleFloat(x.Else, hf)
		}
	case *ast.MatchExpr:
		for _, arm := range x.Arms {
			if arm == nil {
				continue
			}
			c.settleFloat(arm.Body, hf)
		}
	case *ast.Call:
		// Generic-function call returning T against a float
		// destination. Mirror the settleInt Call case so
		// `var x: f64 = pick(true, 3.14, 0.0);` settles the
		// arg widths and re-stamps TypeArgs for monomorph.
		// Without this, the arg literals stayed at the f32
		// / Polymorphic default and the f64 destination
		// load returned garbage (observed: `0` for a
		// `pick(true, 3.14, 0.0)` call).
		if id, ok := x.Callee.(*ast.Ident); ok {
			if fn, isGen := c.info.GenericFuncs[id.Name]; isGen {
				for i, p := range fn.Params {
					if i >= len(x.Args) {
						break
					}
					if _, ok := p.Type.(ast.ParamType); ok {
						c.settleFloat(x.Args[i], hf)
					}
				}
				if len(fn.TypeParams) == 1 && len(x.TypeArgs) == 1 {
					x.TypeArgs[0] = hf
				}
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
// isVariantCall reports whether a *ast.Call resolved to a variant
// constructor (`Some(x)`, `Ok(v)`) rather than a regular function
// call. Reads the IsVariantCall flag the checker stamps on every
// variant-resolved call.
//
// The gate exists to keep `postSettleType`'s Call branch from
// firing on non-variant calls that happen to return Option[T] /
// Result[T, E]. For example, `f(p: boolean[]): Option[i32]` called
// as `f([true])` used to be refreshed as `Option[boolean[]]`
// because the gate didn't distinguish variant constructors from
// regular calls. The previous case-sensitivity heuristic (callee
// Ident starts with an upper-case letter) matched Lang's naming
// convention but wasn't a guarantee; the explicit flag is.
func isVariantCall(c *ast.Call) bool { return c.IsVariantCall }

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
	case *ast.TupleLit:
		// After settleNumeric has propagated the destination's
		// element types into the literal, recompute the tuple
		// shape so the `assignable` check sees the resolved
		// widths. Without this, a `var p: (string, i64) =
		// ("hi", 1)` keeps its pre-settle `(string, i32)`
		// type and the assignment rejects.
		if tt, ok := prior.(ast.TupleType); ok && len(tt.Elems) == len(x.Elems) {
			out := make([]ast.Type, len(x.Elems))
			for i, el := range x.Elems {
				out[i] = postSettleType(el, tt.Elems[i])
			}
			return ast.TupleType{Elems: out}
		}
	case *ast.IfExpr:
		// Recurse through `if cond { … } else { … }` — after
		// settleNumeric walked both arms with the destination
		// type, the unified shape lives in the arm bodies.
		// First arm whose post-settle type differs from `prior`
		// wins; otherwise both arms agreed with `prior`.
		if x.Then != nil {
			if t := postSettleType(x.Then, prior); t != nil {
				return t
			}
		}
		if x.Else != nil {
			if t := postSettleType(x.Else, prior); t != nil {
				return t
			}
		}
	case *ast.MatchExpr:
		// `return match (e) { Variant => tupleLit, … }` —
		// recurse into each arm body and let it refresh the
		// type. Same shape as IfExpr.
		for _, arm := range x.Arms {
			if arm == nil {
				continue
			}
			if t := postSettleType(arm.Body, prior); t != nil {
				return t
			}
		}
	case *ast.Call:
		// Variant constructor calls (`Some(tupleLit)`,
		// `Ok(...)`) — after settleNumeric stamped widths
		// onto each constructor arg, the prior EnumType's
		// Args still reflect the pre-settle widths (the
		// type was unified before the settle pass touched
		// the literals). Recompute Args[0] from the
		// (now-resolved) first arg, when:
		//
		//   - prior is an EnumType with a single Arg, and
		//   - the call has at least one arg
		//
		// Multi-arg generic variants (multi-type-param
		// generics like Result[T, E]) need both args
		// refreshed — walk both when prior carries the
		// right shape. Without this refresh,
		// `Some((1234567890123, 42))` keeps its pre-settle
		// `Option[(i32, i32)]` type and a `var o:
		// Option[(i64, i32)] = ...` assignment rejects.
		//
		// Gated on the call being an actual variant
		// constructor (not just any function call that
		// happens to return Option[T] / Result[T, E]). The
		// gate uses `*ast.Call.Args` pairwise against the
		// prior's Args — for a non-variant call like
		// `f([true]): Option[i32]`, the arg types
		// (boolean[]) don't match the prior's Args (i32),
		// so the assignable-check that follows would have
		// the wrong refreshed type. The check below catches
		// this by requiring the call's first-arg-position
		// resolved type to be assignable to the prior's
		// matching Arg — variant constructors satisfy this
		// trivially (their args ARE the payload values).
		if et, ok := prior.(ast.EnumType); ok && len(et.Args) > 0 && len(x.Args) >= len(et.Args) && isVariantCall(x) {
			newArgs := make([]ast.Type, len(et.Args))
			for i := range et.Args {
				newArgs[i] = postSettleType(x.Args[i], et.Args[i])
			}
			return ast.EnumType{Name: et.Name, Args: newArgs}
		}
	case *ast.MapLit:
		// After settleNumeric stamped widths onto each entry
		// key / value, the prior StructType's TypeArgs may
		// still point at the pre-settle K / V. Recompute
		// from the (now-resolved) first entry — the entry
		// re-check below the MapLit case in `checkExpr`
		// already enforces same-type-across-entries.
		// Also refresh the MapLit's own KeyType / ValueType
		// stamps so the IR's MapLit lowering sees the
		// resolved widths (it reads them to pick the
		// runtime keyKind / valKind tags and the boxing
		// path for wide V).
		if st, ok := prior.(ast.StructType); ok && st.Name == "Map" && len(st.Args) == 2 && len(x.Entries) > 0 {
			ent := x.Entries[0]
			newK := postSettleType(ent.Key, st.Args[0])
			newV := postSettleType(ent.Value, st.Args[1])
			x.KeyType = newK
			x.ValueType = newV
			return ast.StructType{Name: "Map", Args: []ast.Type{newK, newV}}
		}
	case *ast.StructLit:
		// Generic struct literal whose TypeArgs got committed
		// to a concrete shape by settleNumeric. The settle
		// path stamped `x.TypeArgs` directly; reading those
		// back here lets the `assignable` check see
		// `Box[i64]` instead of the pre-settle `Box[i32]`.
		if len(x.TypeArgs) > 0 {
			return ast.StructType{Name: x.TypeName, Args: append([]ast.Type{}, x.TypeArgs...)}
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
// widenIntOperand wraps `*slot` in an implicit CastExpr when the
// resolved operand type's width doesn't match `target`'s. Lang
// requires explicit `as` casts between integer widths in user
// code, but the checker auto-inserts them for binop operands so
// `i64 + i32` (mixed-width pointer arithmetic in the prelude,
// for example) doesn't require sprinkling `as i64` everywhere.
// Signedness must already match — see commonIntegerWidth.
func (c *checker) widenIntOperand(slot *ast.Expr, srcT, targetT ast.NumberType) {
	if srcT.Polymorphic || targetT.Polymorphic {
		return
	}
	if srcT.NormalWidth() == targetT.NormalWidth() {
		return
	}
	// usize ↔ concrete-int needs a cast too (NormalWidth = -1
	// is never equal to 32 / 64, so the same-width fast-path
	// above already falls through). Don't bail when one side
	// is pointer-width — that's exactly the case where the
	// implicit cast matters.
	// Skip if the operand is already a typed numeric literal
	// of the right width — settleNumeric handled it.
	if nl, ok := (*slot).(*ast.NumberLit); ok && nl.Width == targetT.NormalWidth() {
		return
	}
	pos := (*slot).Pos()
	*slot = &ast.CastExpr{
		P:         pos,
		Inner:     *slot,
		Target:    targetT,
		InnerType: srcT,
	}
}

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
	// Pointer-width arithmetic: `usize + i32` (or `i32 + usize`)
	// auto-widens to usize so prelude pointer math stays
	// readable. usize is unsigned and i32 is signed, so the
	// signedness check below would otherwise reject. The
	// 2's-complement representation makes the result identical
	// to what the prelude computed before via explicit
	// `as i64 / as usize` casts.
	if ln.IsPointerWidth() || rn.IsPointerWidth() {
		if ln.IsPointerWidth() {
			return ln, true
		}
		return rn, true
	}
	if ln.IsSigned() != rn.IsSigned() {
		// Mixed signedness needs an explicit cast — the
		// reinterpretation isn't free of edge cases (e.g. a
		// negative i32 + a small u32 isn't well-defined
		// without picking a result domain), and getting it
		// wrong silently is worse than asking the user.
		return ast.NumberType{}, false
	}
	if ln.NormalWidth() == rn.NormalWidth() {
		return ln, true
	}
	// Different widths, same signedness: auto-widen the
	// narrower side to the wider type. Caller is expected to
	// insert an implicit cast on whichever operand is
	// narrower so the IR sees a homogeneous-width binop.
	if ln.NormalWidth() > rn.NormalWidth() {
		return ln, true
	}
	return rn, true
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

// hasHandleDecl reports whether the program defines a top-level
// `function handle(req: HttpRequest): HttpResponse` — the
// signature shape every wasi-http program targets. The check is
// purely structural: any top-level FuncDecl named `handle`
// counts. Mismatched signatures will surface as type errors at
// the synthesised main() check (or the wasi-http wrapper's
// signature check on that target).
func hasHandleDecl(prog *ast.Program) bool {
	for _, fn := range prog.Funcs {
		if fn.Name == "handle" {
			return true
		}
	}
	return false
}

// hasMainDecl mirrors hasHandleDecl for `main`. Used to gate
// auto-main synthesis: if the user defined main themselves we
// keep their version verbatim.
func hasMainDecl(prog *ast.Program) bool {
	for _, fn := range prog.Funcs {
		if fn.Name == "main" {
			return true
		}
	}
	return false
}

// synthesiseHandleMain builds:
//
//	function main(): i32 {
//	    return tcp_serve(__port_from_env("PORT", 8080), handle);
//	}
//
// — the canonical entry point for handler-shaped programs on
// CLI / arm64 targets. The wasi-http target has its own
// `wasi:http/incoming-handler.handle` export wrapper that
// invokes the user's `handle` directly; main existing
// alongside it costs nothing (wasi-http's _start is an empty
// stub anyway).
//
// Under the auto-prelude, both `tcp_serve` and `__port_from_env`
// live at their bare names (LoadStdlibFlat doesn't mangle).
// Under no-prelude with `import "std/tcp";` they get the
// modload `tcp__` prefix instead. We probe `prog.Funcs` for
// whichever name exists and stamp the Ident accordingly so the
// synthesised main resolves cleanly through both load paths.
func synthesiseHandleMain(prog *ast.Program) *ast.FuncDecl {
	pos := ast.Position{}
	resolve := func(bare, mangled string) string {
		for _, fn := range prog.Funcs {
			if fn.Name == bare {
				return bare
			}
		}
		for _, fn := range prog.Funcs {
			if fn.Name == mangled {
				return mangled
			}
		}
		// Neither variant present — the bare name still gives
		// the cleanest "undefined identifier" diagnostic when
		// the program forgot to import std/tcp.
		return bare
	}
	portCall := &ast.Call{
		P:      pos,
		Callee: &ast.Ident{P: pos, Name: resolve("__port_from_env", "tcp____port_from_env")},
		Args: []ast.Expr{
			&ast.StringLit{P: pos, Value: "PORT"},
			&ast.NumberLit{P: pos, Value: 8080},
		},
	}
	tcpServeCall := &ast.Call{
		P:      pos,
		Callee: &ast.Ident{P: pos, Name: resolve("tcp_serve", "tcp__tcp_serve")},
		Args: []ast.Expr{
			portCall,
			&ast.Ident{P: pos, Name: "handle"},
		},
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Return{P: pos, Value: tcpServeCall},
	}}
	return &ast.FuncDecl{
		P:                        pos,
		Name:                     "main",
		Params:                   nil,
		ReturnType:               ast.NumberType{Width: 32, Signed: true},
		Body:                     body,
		IsSynthesisedHandlerMain: true,
	}
}

func (c *checker) requireBool(p ast.Position, t ast.Type, op string) {
	if t != nil && !ast.Equal(t, ast.BoolType{}) {
		c.errf(p, "operator %q requires boolean, got %s", op, t)
	}
}
