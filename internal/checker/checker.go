// Package checker performs name-resolution and type-checking on a Program.
//
// Each function is checked against an environment chain that starts at the
// top-level (functions) and is extended for parameters and `var`
// declarations. Errors are accumulated rather than fatally aborting on the
// first one, so a single run reports as much as possible.
package checker

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/defaultargs"
	"github.com/jakechampion/lang/internal/diag"
)

type Error struct {
	Pos     ast.Position
	Span    int    // optional: token length for `^~~~~` underline; 0 = caret only
	Note    string // optional: free-text hint rendered as `note:`
	Msg     string
	Path    string // source file path; filled by errf from c.current.SourceModule
	ErrCode string // optional: stable error code (E001…), surfaces in the header + `lang explain` output
	// Fix is an optional machine-applicable suggestion rendered as a
	// `help:` line (diag.Suggestion — span + replacement + title). Only
	// attached when applying the replacement is guaranteed to re-parse.
	Fix *diag.Suggestion
}

func (e *Error) Error() string          { return fmt.Sprintf("type error at %s: %s", e.Pos, e.Msg) }
func (e *Error) Position() ast.Position { return e.Pos }
func (e *Error) Length() int            { return e.Span }
func (e *Error) Hint() string           { return e.Note }
func (e *Error) File() string           { return e.Path }
func (e *Error) setFile(p string)       { e.Path = p }
func (e *Error) Code() string           { return e.ErrCode }

func (e *Error) Suggestion() *diag.Suggestion { return e.Fix }

// Info captures everything codegen needs that the checker discovered:
// the inferred type of every var without an annotation, and a per-function
// list of locals (so codegen can lay out a frame).
type Info struct {
	VarTypes map[*ast.Var]ast.Type
	// BoxedCells names the locals that closureconv.BoxMutatedScalarCaptures
	// rewrote into 1-element array cells for by-reference scalar capture. Such a
	// cell is a SHARED MUTABLE reference (the whole point — a closure and the
	// outer scope observe each other's writes), so an `cell[0] = v` store must
	// NOT go through copy-on-write (which would fork the cell when its rc > 1
	// because a closure also holds it, breaking the sharing). The IR's index-
	// assign CoW gate skips names in this set and stores in place. Names are
	// unique post-shadowrename, so one program-wide set is unambiguous. Empty /
	// nil for any program with no mutated scalar captures.
	BoxedCells map[string]bool
	Locals     map[*ast.FuncDecl][]*ast.Var
	FuncSigs   map[string]*ast.FuncType
	// OwnFuncs maps a function name to its per-parameter `own` (owned /
	// consuming) flags, for functions that have at least one owned parameter.
	// The IR uses it to lower ownership transfer: a callee reclaims its `own`
	// params, and a caller moves an owned argument into them instead of
	// dropping it. Empty when no function uses `own`.
	OwnFuncs map[string][]bool
	// Structs maps a struct name to its declaration (which carries the
	// ordered field list — codegen looks up field offsets here).
	Structs map[string]*ast.StructDecl
	// Enums maps an enum name to its declaration. The variant list +
	// payload types live there; codegen looks up the runtime tag
	// (the variant's index in the variant slice) and the payload
	// shapes via this map.
	Enums map[string]*ast.EnumDecl
	// Resources maps a `resource Name;` declaration's name to its decl —
	// nominal WIT resource-handle types (P5 — docs/WIT-BRING-YOUR-OWN.md).
	// resolveType reclassifies a bare resource-name reference to an owned
	// ast.HandleType, and validateResourceHandles checks that every `own R` /
	// `borrow R` names a registered resource. Empty for programs with no
	// `resource` declarations.
	Resources map[string]*ast.ResourceDecl
	// Methods maps `<StructName>.<MethodName>` to the mangled
	// top-level function name the receiver-rewriting pass introduces
	// (`__method_<StructName>_<MethodName>`). Call-site rewriting
	// uses this map to turn `p.area()` into `__method_Point_area(p)`.
	Methods map[string]string
	// MethodSources records the canonical source-module path each
	// registered method's body came from. Mangled-name keys mirror
	// the values in Methods (e.g. `__method_i32_abs` →
	// `stdlib://std/i32.fern`). Entries with an empty value are
	// universally visible — that's the case for synthetic methods
	// the checker registers itself (Reader / Writer / Map /
	// MapIter, the inline-IR `Array.push`, and the built-in string
	// methods).
	//
	// The dispatch path filters method resolutions against the
	// call site's enclosing module + the program's `ModuleImports`
	// closure so that methods declared in a module are only callable
	// from files whose import closure reaches that module
	// (`docs/PRELUDE-TO-MODULES.md`). Empty entries on either side
	// skip the filter (accommodation for checker-synthesised decls
	// and single-file programs).
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
	// Traits maps a trait name to its declaration. Populated at the
	// start of Check from prog.Traits. Used by the conformance
	// check and (Phase 2) by generic bound resolution. See
	// docs/TRAITS.md.
	Traits map[string]*ast.TraitDecl
	// Impls records which concrete types implement which trait:
	// Impls[traitName][typeName] is true when a validated
	// `impl Trait for Type` exists. Phase 2's bound checking asks
	// "does i32 implement Display?" against this.
	Impls map[string]map[string]bool
	// ImplTraitArgs records the type arguments a generic-trait impl bound
	// (`impl From[i32] for Celsius` → ImplTraitArgs["From"]["Celsius"] =
	// [i32]). Lets a generic-trait BOUND (`T: From[i32]`) check the args
	// match, not just that some `impl From[_] for T` exists. Empty for a
	// non-generic trait. See docs/TRAITS.md.
	ImplTraitArgs map[string]map[string][]ast.Type
	// ImplForPattern records a PARAMETRIC impl-of-a-generic-trait's `for`
	// type pattern (`impl[T] Iterator[T] for ArrayIter[T]` →
	// ImplForPattern["Iterator"]["ArrayIter"] = ArrayIter[T], with T as a
	// ParamType). A bound check unifies it against a concrete type
	// (`ArrayIter[i32]`) to recover the impl's param binding (T=i32) and
	// resolve the generic ImplTraitArgs ([T]) to concrete ([i32]). Empty for
	// concrete impls. See docs/TRAITS.md.
	ImplForPattern map[string]map[string]ast.Type
	// AssocBindings records each impl's associated-type bindings:
	// AssocBindings[typeName][assocName] = concrete type. Built during the
	// conformance pass. resolveProj uses it to resolve a concrete-base
	// `Foo::Item` projection to its bound type. See docs/ASSOCIATED-TYPES.md.
	AssocBindings map[string]map[string]ast.Type
	// DynCoercions records every concrete→`dyn Trait` boxing site,
	// keyed by the holder expression the checker saw flow into a `dyn`
	// slot (var init, assignment, argument, return, array element,
	// struct field — all route through maybeWrapForUnion). Compiled-
	// backend IR lowering reads this to box the concrete value into the
	// `{data, vtable}` fat pointer with the named concrete type's vtable
	// (docs/DYN-TRAITS.md §4.2.1). The interpreter ignores it (it
	// dispatches by the receiver's runtime type). Empty for programs
	// with no `dyn` coercions.
	//
	// Keyed by the *unrewritten* holder expression pointer, which flows
	// unchanged to the IR for non-generic code. (A coercion inside a
	// generic body is cloned to a fresh pointer by monomorph and so is
	// not found — out of scope for the first compiled `dyn` slice.)
	DynCoercions map[ast.Expr]DynCoercion
}

// DynCoercion identifies one concrete→`dyn …` boxing site: the trait(s)
// being coerced to and the concrete type flowing in. The IR uses the
// (trait, concrete) pair to select the vtable to pair with the boxed
// value. See checker.Info.DynCoercions and docs/DYN-TRAITS.md.
//
// Trait is the PRIMARY (single) trait — `Traits[0]` — kept for the
// single-trait compiled codegen path (the only one that lowers today;
// multi-trait is compiled-rejected). Traits holds the WHOLE set, so
// set-aware consumers (tree-shaking, which must root the impl methods of
// EVERY trait in the set) iterate it rather than reading only Trait.
type DynCoercion struct {
	Trait    string
	Traits   []string
	Concrete string
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
// callers (fernsmith's generator, IDE rename refactors,
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
		// `fern -target wasi-http` mode (step 5 of
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
				// `headers` lands at the END of the layout so the
				// pre-headers byte offsets the wasi-http wrapper
				// hardcodes (method@+0/+4, path@+8/+12,
				// body@+16/+20) stay stable. HeaderMap is a 4-byte
				// pointer slot on wasm32, total HttpRequest size
				// 28 bytes. Inbound population: http_parse_request
				// on the tcp_serve path parses the header block
				// into the map; the wasi-http wrapper inlines an
				// empty HeaderMap (canonical-ABI fields-resource
				// integration is the next follow-up PR).
				{Name: "headers", Type: ast.StructType{Name: "HeaderMap"}},
			},
		},
		{
			Name: "HttpResponse",
			Fields: []ast.Param{
				{Name: "status", Type: ast.NumberType{}},
				{Name: "body", Type: ast.StringType{}},
				// `headers` lands at the END so the wasi-http
				// wrapper's hardcoded reads (status@+0,
				// body@+8/+12) stay stable. http_serialize_response
				// walks this map to emit the wire header block;
				// Content-Length and Connection are auto-emitted
				// after user headers (and de-duped against names
				// the user already set so a manual
				// `resp.headers.set("Connection", ...)` wins).
				{Name: "headers", Type: ast.StructType{Name: "HeaderMap"}},
			},
		},
		// Platform — the capability bag threaded as the second
		// parameter of every handler (docs/PLATFORM-RESEARCH.md
		// Rec §1). Phase 1 carries a single `version: i32`
		// placeholder so the struct has a well-defined ABI
		// without zero-sized-struct edge cases; future phases
		// will add capability fields (fetch / kv / secrets /
		// log / now). The auto-`main`-from-`handle` synthesis
		// and the wasi-http wrapper construct one per request
		// and pass it through; handler code receives it as
		// `plat: Platform` and (for now) ignores the fields.
		{
			Name: "Platform",
			Fields: []ast.Param{
				{Name: "version", Type: ast.NumberType{}},
			},
		},
		// HeaderMap — case-insensitive, multi-valued, insertion-
		// ordered header bag (docs/STDLIB-DESIGN-RESEARCH.md
		// Rec §2). Storage is two parallel arrays — `names`
		// holds the case-folded (lowercase) header name; `values`
		// holds the raw value in insertion order. Lookups are
		// linear scans through `names`, which is fine for the
		// typical <20-header request and matches the spirit of
		// hyper's HeaderMap without the indexing overhead.
		// Methods (`get`, `get_all`, `set`, `append`, `len`)
		// live in `internal/stdlib/std/headers.fern`; this PR
		// lands the type + module only and defers the
		// `headers: HeaderMap` integration into HttpRequest /
		// HttpResponse to a follow-up so the wasi-http
		// canonical-ABI fields-resource wiring can be its own
		// reviewable unit.
		{
			Name: "HeaderMap",
			Fields: []ast.Param{
				{Name: "names", Type: ast.ArrayType{Elem: ast.StringType{}}},
				{Name: "values", Type: ast.ArrayType{Elem: ast.StringType{}}},
			},
		},
		// Stream — byte-stream value (docs/STDLIB-DESIGN-RESEARCH.md
		// Rec §1 Phase 2). The eventual home for
		// `HttpRequest.body` (today: `string`; tomorrow:
		// `Stream`). Phase 1 ships an in-memory buffer-backed
		// Stream with `data: u8[]` + `pos: i32` cursor. Lazy /
		// chunked reads land in Phase 2 once the underlying
		// runtime grows a reader-shaped iteration protocol.
		// `bytes` is treated as `u8[]` throughout — no separate
		// type alias yet; doc-comments call out the bytes ≡
		// u8[] equivalence.
		{
			Name: "Stream",
			Fields: []ast.Param{
				{Name: "data", Type: ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
				{Name: "pos", Type: ast.NumberType{}},
			},
		},
		// BytesWriter — in-memory buffered writer
		// (docs/STDLIB-DESIGN-RESEARCH.md Rec §5). The "Memory-
		// Writer" the doc lists alongside Reader / Writer
		// interfaces — collects writes into a u8[] buffer that
		// callers `.into_string()` or `.into_bytes()` at the
		// end. Phase 1 doesn't introduce nominal interface
		// types; concrete BytesWriter shares the same
		// `.write_string()` / `.write_bytes()` method shape
		// as Writer (the fd-backed version) so callers can
		// swap one for the other.
		{
			Name: "BytesWriter",
			Fields: []ast.Param{
				{Name: "data", Type: ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
			},
		},
		// MockCall + MockPlatform — test-ergonomics helpers for
		// Tier-C Rec §11 (docs/PLATFORM-RESEARCH.md §6). Today's
		// Platform is a one-field placeholder; once Phase 2 adds
		// capability fields (log / fetch / kv / now), MockPlatform
		// grows matching methods that intercept calls. Phase 1
		// ships the call-recording infrastructure: tests
		// instantiate MockPlatform, perform some flow that
		// records `MockCall { name, args }` entries, then
		// inspect `(m).calls()` to assert effect ordering /
		// payload shape.
		{
			Name: "MockCall",
			Fields: []ast.Param{
				{Name: "name", Type: ast.StringType{}},
				{Name: "args", Type: ast.StringType{}},
			},
		},
		{
			Name: "MockPlatform",
			Fields: []ast.Param{
				{Name: "calls", Type: ast.ArrayType{Elem: ast.StructType{Name: "MockCall"}}},
			},
		},
		// Date/time types (docs/STDLIB-DESIGN-RESEARCH.md
		// Rec §4 — the jiff/NodaTime six-type shape).
		// Phase 1: type registrations + a stub std/time
		// module. Subsequent PRs land Instant.now() (needs
		// clock_gettime per-target), Hinnant date arithmetic,
		// RFC 3339 parser/formatter, IANA zone lookup, and
		// the Span/Duration calendar-vs-absolute split.
		//
		// Each type is meaning-distinct:
		//   Instant   — a point in physical time (UTC, ns).
		//   Date      — civil (year, month, day); no zone.
		//   Time      — civil wall-clock (h, m, s, ns); no zone.
		//   DateTime  — pair of (Date, Time); no zone.
		//   Zoned     — Instant + TimeZone (fully qualified).
		//   Span      — calendar-flavoured interval (years,
		//               months, days, …).
		//   Duration  — absolute interval (sec + nsec).
		//   TimeZone  — IANA name + offset cache.
		//
		// Conversions stay explicit per the doc: Date→Instant
		// requires a TimeZone, Zoned→Date discards the zone,
		// nothing coerces implicitly.
		{
			Name: "Instant",
			Fields: []ast.Param{
				{Name: "sec", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "nsec", Type: ast.NumberType{}},
			},
		},
		{
			Name: "Date",
			Fields: []ast.Param{
				{Name: "year", Type: ast.NumberType{}},
				{Name: "month", Type: ast.NumberType{}},
				{Name: "day", Type: ast.NumberType{}},
			},
		},
		{
			Name: "Time",
			Fields: []ast.Param{
				{Name: "hour", Type: ast.NumberType{}},
				{Name: "minute", Type: ast.NumberType{}},
				{Name: "second", Type: ast.NumberType{}},
				{Name: "nsec", Type: ast.NumberType{}},
			},
		},
		{
			Name: "DateTime",
			Fields: []ast.Param{
				{Name: "date", Type: ast.StructType{Name: "Date"}},
				{Name: "time", Type: ast.StructType{Name: "Time"}},
			},
		},
		{
			Name: "TimeZone",
			Fields: []ast.Param{
				{Name: "name", Type: ast.StringType{}},
				// `offset_seconds` is the cached offset from
				// UTC for the period containing `Zoned.instant`.
				// Future PRs populate it from tzdb; for now
				// zero-init carries the UTC convention.
				{Name: "offset_seconds", Type: ast.NumberType{}},
			},
		},
		{
			Name: "Zoned",
			Fields: []ast.Param{
				{Name: "instant", Type: ast.StructType{Name: "Instant"}},
				{Name: "zone", Type: ast.StructType{Name: "TimeZone"}},
			},
		},
		{
			Name: "Span",
			Fields: []ast.Param{
				{Name: "years", Type: ast.NumberType{}},
				{Name: "months", Type: ast.NumberType{}},
				{Name: "weeks", Type: ast.NumberType{}},
				{Name: "days", Type: ast.NumberType{}},
				{Name: "hours", Type: ast.NumberType{}},
				{Name: "minutes", Type: ast.NumberType{}},
				{Name: "seconds", Type: ast.NumberType{}},
				{Name: "nanos", Type: ast.NumberType{}},
			},
		},
		{
			Name: "Duration",
			Fields: []ast.Param{
				{Name: "sec", Type: ast.NumberType{Width: 64, Signed: true}},
				{Name: "nsec", Type: ast.NumberType{}},
			},
		},
		// ProcessResult — the return shape of `exec(cmd, args,
		// stdin)`. The interp's Go-side implementation populates
		// stdout / stderr / exit_code; the wasm + native backends
		// don't lower exec today, so the type registration is
		// here-only for the test-runner migration path that runs
		// the migrated suites under `fern -interp`. Spawn failures
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
		// FileStat — `stat(path)` shape. Carries the minimum
		// surface needed by test fixtures: the kind (file vs
		// directory vs other) and the byte size. Mtime is a
		// follow-up: lang has no Time type yet, and the
		// migration's tests don't need it.
		{
			Name: "FileStat",
			Fields: []ast.Param{
				{Name: "is_file", Type: ast.BoolType{}},
				{Name: "is_dir", Type: ast.BoolType{}},
				{Name: "size", Type: ast.NumberType{Width: 64, Signed: true}},
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
		// Cell[T] — a single-slot mutable heap box, the sanctioned
		// mutable-state primitive for the immutable-data world
		// (docs/CELL-TYPE-PLAN.md). `T` is restricted to cycle-free
		// types (E057) so a cell can never reconstruct a reference
		// cycle — v1 is scalars only (string and the rest wait on the
		// owning-slot RC integration). The field is an opaque scalar
		// slot; cell_new / get / set are IR-lowered to a one-element
		// heap box (alloc + load/store at offset 0) so Perceus RCs the
		// box itself with no per-slot RC.
		{
			Name:       "Cell",
			TypeParams: []string{"T"},
			Fields: []ast.Param{
				{Name: "value", Type: ast.NumberType{}},
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

// Check type-checks the program. It returns an aggregated error if any
// problems were found.
func Check(prog *ast.Program) (*Info, error) {
	return CheckContext(context.Background(), prog)
}

// CheckContext is the context-aware sibling of Check —
// checks the context between each top-level function-body
// pass so the LSP can cancel a long type-check mid-flight
// when a new edit invalidates the in-progress result.
// See docs/IDE-COMPILATION-RESEARCH.md Rec §1.
//
// On cancel, returns (nil, ctx.Err()) — same convention as
// ParseContext. The body-check loop is by far the dominant
// cost in Check; the preceding builtin registration
// + first-pass collection runs are O(decl-count) walks with
// no recursive descent, so a single up-front ctx check
// suffices for them.
func CheckContext(ctx context.Context, prog *ast.Program) (*Info, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return checkImpl(ctx, prog)
}

func checkImpl(ctx context.Context, prog *ast.Program) (*Info, error) {
	// Prepend the built-in Option / Result / IoError /
	// JsonValue enums so user code (and the stdlib)
	// can reference them without an explicit declaration.
	// Each is injected individually if the user hasn't
	// already declared the same name — earlier the
	// "auto-inject only when prog.Enums[0].Name != Option"
	// heuristic skipped EVERY builtin if the user declared
	// their own Option, which broke the stdlib's json_encode
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
	// Writer, HttpRequest, HttpResponse, Platform, HeaderMap,
	// Stream, BytesWriter, MockCall, MockPlatform, Instant /
	// Date / Time / DateTime / TimeZone / Zoned / Span /
	// Duration, Map, MapIter, Url) — same shadow-is-an-error
	// policy, same monomorph-re-entry handling.
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
	c := &checker{
		info: &Info{
			VarTypes:            map[*ast.Var]ast.Type{},
			Locals:              map[*ast.FuncDecl][]*ast.Var{},
			FuncSigs:            map[string]*ast.FuncType{},
			Structs:             map[string]*ast.StructDecl{},
			Enums:               map[string]*ast.EnumDecl{},
			Resources:           map[string]*ast.ResourceDecl{},
			Methods:             map[string]string{},
			MethodSources:       map[string]string{},
			ModuleImports:       prog.ModuleImports,
			VariantCallPayloads: map[*ast.Call][]ast.Type{},
			GenericFuncs:        map[string]*ast.FuncDecl{},
			GenericStructs:      map[string]*ast.StructDecl{},
			Generics:            map[string]ast.GenericDecl{},
			Traits:              map[string]*ast.TraitDecl{},
			Impls:               map[string]map[string]bool{},
			ImplTraitArgs:       map[string]map[string][]ast.Type{},
			ImplForPattern:      map[string]map[string]ast.Type{},
			AssocBindings:       map[string]map[string]ast.Type{},
		},
		variantOf: map[string][]variantRef{},
	}
	// Map operations need core/map linked. If the program came through
	// modload (LoadedStdlibPaths populated) but didn't pull core/map
	// into its closure, flag Map use as a clean type error instead of
	// a codegen link failure. Programs constructed without modload
	// (bare parser.Parse — single-file probes) can't import anything,
	// so they're exempt.
	if prog.LoadedStdlibPaths != nil && !prog.LoadedStdlibPaths["stdlib://core/map.fern"] {
		c.requireMapImport = true
	}
	// Resolve named arguments + fill default parameter values before any
	// type-checking, so the rest of the checker (and every later pass) sees a
	// complete positional call. Resolution diagnostics surface as checker
	// errors. Idempotent — safe across LSP re-checks.
	for _, fe := range defaultargs.Fill(prog) {
		c.errfCode(fe.Pos, fe.Code, "%s", fe.Msg)
	}

	// Surface shadow-attempts on reserved built-in type names
	// recorded above. We report on the user's decl position so
	// the IDE squiggle lands on the offending source, not on
	// some synthetic builtin we never actually exposed.
	for _, ed := range shadowedEnums {
		c.errfCode(ed.P, "E010", "enum %q is a reserved built-in name and cannot be redeclared", ed.Name)
	}
	for _, sd := range shadowedStructs {
		c.errfCode(sd.P, "E010", "struct %q is a reserved built-in name and cannot be redeclared", sd.Name)
	}

	// Register every struct declaration up front so that types
	// referenced by name (`function f(p: Point)`) resolve when we
	// check function signatures below.
	for _, sd := range prog.Structs {
		if _, dup := c.info.Structs[sd.Name]; dup {
			c.errfCode(sd.P, "E006", "struct %q redeclared", sd.Name)
			continue
		}
		seen := map[string]bool{}
		for _, f := range sd.Fields {
			if seen[f.Name] {
				c.errfCode(sd.P, "E007", "duplicate field %q in struct %s", f.Name, sd.Name)
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
			c.errfCode(ud.P, "E016", "union %q collides with a struct of the same name", ud.Name)
			continue
		}
		if _, dup := c.info.Enums[ud.Name]; dup {
			c.errfCode(ud.P, "E016", "union %q collides with an enum of the same name", ud.Name)
			continue
		}
		variants := make([]ast.EnumVariant, 0, len(ud.Members))
		seen := map[string]bool{}
		ok := true
		for _, member := range ud.Members {
			if seen[member] {
				c.errfCode(ud.P, "E016", "duplicate member %q in union %s", member, ud.Name)
				ok = false
				continue
			}
			seen[member] = true
			sd, isStruct := c.info.Structs[member]
			if !isStruct {
				c.errfCode(ud.P, "E016", "union %s member %q does not name a struct in scope", ud.Name, member)
				ok = false
				continue
			}
			if len(sd.TypeParams) > 0 {
				c.errfCode(ud.P, "E016", "union %s member %q is generic; generic struct members in unions are not supported yet", ud.Name, member)
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
			c.errfCode(ed.P, "E006", "enum %q redeclared", ed.Name)
			continue
		}
		c.info.Enums[ed.Name] = ed
		seen := map[string]bool{}
		for i := range ed.Variants {
			v := &ed.Variants[i]
			if seen[v.Name] {
				c.errfCode(v.P, "E017", "duplicate variant %q in enum %s", v.Name, ed.Name)
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

	// Register every `resource Name;` declaration (P5 WIT resource-handle
	// types — docs/WIT-BRING-YOUR-OWN.md) before signatures resolve, so that
	// `own Name` / `borrow Name` references and bare resource-name references
	// resolve. A resource name shares the nominal namespace with structs and
	// enums, so a collision is a redeclaration error.
	for _, rd := range prog.Resources {
		if _, dup := c.info.Resources[rd.Name]; dup {
			c.errfCode(rd.P, "E006", "resource %q redeclared", rd.Name)
			continue
		}
		if _, isStruct := c.info.Structs[rd.Name]; isStruct {
			c.errfCode(rd.P, "E006", "resource %q conflicts with a struct of the same name", rd.Name)
			continue
		}
		if _, isEnum := c.info.Enums[rd.Name]; isEnum {
			c.errfCode(rd.P, "E006", "resource %q conflicts with an enum of the same name", rd.Name)
			continue
		}
		c.info.Resources[rd.Name] = rd
	}

	// Bind receiver type-variables for generic-receiver methods. A
	// method like `function (b: Box[T]) get(): T` introduces T as an
	// implicit type parameter, inferred from the receiver at the call
	// site — unless the name resolves to a concrete struct / enum /
	// built-in. This mirrors how `@derive` methods on a generic type
	// get their type params bound (bindDeriveTypeParams); once
	// fn.TypeParams carries T, resolveTypeNames below rewrites T to a
	// ParamType across the receiver / params / return / body, and the
	// ordinary generic-method inference + monomorph path takes over.
	// Runs before resolveTypeNames so the rewrite sees the params; the
	// struct / enum sets are already populated above.
	for _, fn := range prog.Funcs {
		if fn.Receiver == nil {
			continue
		}
		var vars []string
		seen := map[string]bool{}
		for _, tp := range fn.TypeParams {
			seen[tp] = true
		}
		c.collectFreeTypeVars(fn.Receiver.Type, &vars, seen)
		// Receiver type-vars go FIRST: the call site seeds them
		// positionally from the receiver's type args (a method like
		// `(b: Box[T]) map[U](...)` gets T from the receiver and infers
		// U from the arguments), so T must precede any post-name `[U]`.
		fn.TypeParams = append(vars, fn.TypeParams...)
	}

	// Now that the enum set is known, walk every type position in
	// the program and rewrite StructType{Name: X} → EnumType when
	// X resolves to an enum. The parser doesn't know which named
	// types are structs vs. enums; we lazily disambiguate here.
	c.resolveTypeNames(prog)

	// With every type slot resolved (parameters → ParamType, enums →
	// EnumType, resources → HandleType), validate that each remaining
	// nominal reference names a type that actually exists — otherwise an
	// unknown type was silently accepted as an opaque nominal and only
	// surfaced as a confusing downstream cascade (or not at all). See E064.
	c.validateKnownTypes(prog)

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
	// strbuf_reset() / strbuf_append(s) / strbuf_take() — global
	// mutable string-builder primitive. There's a single 64 MiB BSS
	// scratch buffer; reset zeroes its length, append memcpys bytes
	// past the current tail, take allocates a fresh string of the
	// accumulated bytes and resets. Built for the asm self-host
	// backend's emit_module — `s = s.out + text` per write
	// allocates O(N²) bytes through the bump heap (~60 GB to compile
	// asm.fern through itself). With strbuf the same loop is O(N).
	c.info.FuncSigs["strbuf_reset"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["strbuf_append"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["strbuf_take"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.StringType{},
	}
	// __rc_inc / __rc_dec / __rc_get — direct access to the
	// refcount machinery for debugging and Phase 1 testing.
	// They bypass the normal alias-tracking that Phase 1c/d
	// will introduce, so they're not meant for everyday code.
	// __rc_inc returns the input pointer so the call can be
	// spliced into an expression chain. __rc_get reads the rc
	// word at `[arr - 8]`. See docs/RC-PERCEUS-PLAN.md.
	u8ArrType := ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}
	c.info.FuncSigs["__rc_inc"] = &ast.FuncType{
		Params: []ast.Type{u8ArrType},
		Result: u8ArrType,
	}
	c.info.FuncSigs["__rc_dec"] = &ast.FuncType{
		Params: []ast.Type{u8ArrType},
		Result: ast.VoidType{},
	}
	c.info.FuncSigs["__rc_get"] = &ast.FuncType{
		Params: []ast.Type{u8ArrType},
		Result: ast.NumberType{},
	}
	// __rc_underflow_count(): i32 — Phase 3 step 1 detector. Reads
	// the count of rc over-releases (decrements of an already-<=0
	// rc) the runtime has observed. WASM-only probe; see the IR
	// lowering. Used by tests to assert a program is drift-free.
	c.info.FuncSigs["__rc_underflow_count"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
	}
	// __heap_bump_bytes(): i32 — Phase 6 measurement probe. Returns the
	// bump allocator's high-water mark (cursor − region base) in bytes; 0
	// before the first alloc. The cursor only advances on a fresh bump,
	// never on a freelist reuse, so a reclaiming loop keeps it flat while
	// a leaking loop grows it — the metric for asserting reclaim/boundedness
	// and for profiling hot allocations. See the IR lowering / per-backend
	// runtime reader.
	c.info.FuncSigs["__heap_bump_bytes"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
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
	// Float math primitives. These are checker builtins because
	// the interp / native / wasm backends all have access to
	// hardware-precise implementations (Go's `math` package
	// for the interp; wasm's f64.{sqrt,floor,…} ops for the
	// wasm backend; libm-style sequences on arm64 / x86 for
	// the native backends). Wrapping each in Lang code (eg
	// Newton iteration for sqrt) would be slower AND less
	// precise, which is bad both for performance and for
	// property-based tests that compare against an external
	// reference.
	//
	// The user-facing surface is the receiver methods in
	// `std/float` (`(x: f64).sqrt()`, `(x: f64).floor()`, …)
	// which dispatch to these primitives. Native / wasm
	// codegen support follows the same path as `f32_bits` —
	// for now interp-only is the right scope (the test-runner
	// migration uses these via `fern -interp`).
	f64ToF64Builtin := &ast.FuncType{
		Params: []ast.Type{ast.FloatType{Width: 64}},
		Result: ast.FloatType{Width: 64},
	}
	c.info.FuncSigs["__sqrt_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__floor_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__ceil_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__round_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__trunc_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__abs_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__log_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__exp_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__sin_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__cos_f64"] = f64ToF64Builtin
	c.info.FuncSigs["__pow_f64"] = &ast.FuncType{
		Params: []ast.Type{
			ast.FloatType{Width: 64},
			ast.FloatType{Width: 64},
		},
		Result: ast.FloatType{Width: 64},
	}
	// f64_bits / f64_from_bits — same idea for 64-bit floats.
	// The interp owns the only implementation today; the native
	// + wasm backends route f64 / i64 through their own
	// reinterpret instructions and don't need a builtin helper.
	// Exposing the signature lets pure-Lang float-formatting
	// code (std/float's `__float_to_string`, future hex-float
	// emitters) extract the IEEE-754 fields without a runtime
	// trip.
	c.info.FuncSigs["f64_bits"] = &ast.FuncType{
		Params: []ast.Type{ast.FloatType{Width: 64}},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	c.info.FuncSigs["f64_from_bits"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}},
		Result: ast.FloatType{Width: 64},
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
	// random_i32(): i32 — returns a single cryptographic-quality
	// random i32. Backed by the kernel CSPRNG (or
	// `wasi:random/random::get-random-u64` on preview-2 WASM,
	// truncated to i32). Use when you need a single small random
	// value without the heap-allocation overhead of random_bytes.
	c.info.FuncSigs["random_i32"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
	}
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
	// tcp_connect(host_be, port): number — outbound client connect (the
	// upstream-fetch half of the edge-handler use case). host_be is the
	// IPv4 in network byte order packed into an i32
	// (a | b<<8 | c<<16 | d<<24); returns the connected fd, or -errno.
	// x86-64 first; arm64 follows.
	c.info.FuncSigs["tcp_connect"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	// tcp_pollable(conn): number — a wasi:io/poll pollable handle for a
	// connection's readiness (tcp-socket.subscribe), so std/async
	// can multiplex N connections via wasm_poll for overlapped outbound
	// fan-out. wasm-only (Preview-2 pollables); the native reactor polls
	// the connection fd directly.
	c.info.FuncSigs["tcp_pollable"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	// poll(fds, timeout_ms): number — the std/task reactor's readiness
	// multiplexer (docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1). Waits up
	// to `timeout_ms` (negative = block indefinitely, 0 = non-blocking)
	// for any fd in `fds` to become readable; returns the index of the
	// first ready fd, or -1 on timeout. x86-64 first; arm64 (ppoll) +
	// wasm (wasi:io/poll) follow.
	c.info.FuncSigs["poll"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{}}, ast.NumberType{}},
		Result: ast.NumberType{},
	}
	// timer_fd(ms): number — a CLOCK_MONOTONIC timerfd that becomes
	// readable once after `ms` milliseconds; returns its fd (poll it via
	// `poll` / std/reactor). Backs reactor timeouts + deterministic
	// readiness tests (docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1c).
	// x86-64 + arm64 (Linux timerfd); wasm/Darwin follow.
	c.info.FuncSigs["timer_fd"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
	}
	// wasm_timer_pollable(duration_ns: i64): i32 — the wasm reactor's
	// timer primitive. Returns a wasi:io/poll pollable handle that
	// becomes ready after `duration_ns` nanoseconds (via
	// wasi:clocks/monotonic-clock.subscribe-duration). The pollable
	// analog of the native timer_fd; wasm-only (Preview-2). See
	// docs/WASM-REACTOR-PLAN.md.
	c.info.FuncSigs["wasm_timer_pollable"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	// wasm_block(pollable: i32): i32 — synchronously block until the
	// pollable handle is ready, then return 0. Wraps
	// wasi:io/poll.pollable.block; wasm-only (Preview-2).
	c.info.FuncSigs["wasm_block"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	// wasm_poll(pollables: i32[]): i32 — the wasm reactor's readiness
	// multiplexer. Blocks until at least one pollable in the array is
	// ready, then returns its array index (or -1 if none). Wraps
	// wasi:io/poll.poll(list<pollable>) -> list<u32>; wasm-only
	// (Preview-2). The pollable analog of the native poll(fds).
	c.info.FuncSigs["wasm_poll"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 32, Signed: true}}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	// wasm_pollable_drop(pollable: i32): i32 — drop a consumed pollable
	// handle (returns 0), so the reactor frees a fired timer pollable
	// instead of leaking it until exit. Wraps
	// wasi:io/poll.[resource-drop]pollable; wasm-only (Preview-2).
	c.info.FuncSigs["wasm_pollable_drop"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 32, Signed: true}},
		Result: ast.NumberType{Width: 32, Signed: true},
	}
	// udp_send(host, port, data): number — one-shot fire-and-forget UDP
	// datagram. host is an IPv4 literal ("a.b.c.d"); binds an ephemeral
	// local port, connects to host:port, sends data, and tears the
	// socket down. Returns the bytes accepted by the host, or a negative
	// errno on failure. (Send-only / IPv4-literal v1 — for telemetry /
	// syslog to a local agent.)
	c.info.FuncSigs["udp_send"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}, ast.NumberType{}, ast.StringType{}},
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
	// now_unix_ms(): i64 — wall-clock milliseconds since the
	// Unix epoch (1970-01-01 00:00:00 UTC). Wraps Go's
	// `time.Now().UnixMilli()`. Subject to NTP adjustments
	// and intentional clock changes — DON'T use for benchmark
	// timing; use `monotonic_ns()` for that. The use cases
	// for this primitive are "stamp a log line with the
	// current time" and "compute the date for a fixture file
	// name".
	c.info.FuncSigs["now_unix_ms"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	// monotonic_ns(): i64 — nanoseconds since some fixed
	// reference, monotonically non-decreasing. Wraps Go's
	// `time.Now().UnixNano()` (which carries a monotonic
	// reading on every platform Go supports). Use for
	// benchmark timing — the elapsed time between two
	// `monotonic_ns()` calls is the right answer regardless
	// of wall-clock NTP jumps.
	c.info.FuncSigs["monotonic_ns"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	// now_ns(): i64 — wall-clock nanoseconds since the Unix
	// epoch. Same time source as `now_unix_ms` but at
	// nanosecond resolution. Wraps WASI's wall-clock now
	// (preview-1 `clock_time_get(realtime)`, preview-2
	// `wasi:clocks/wall-clock::now`) on wasm.
	c.info.FuncSigs["now_ns"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{Width: 64, Signed: true},
	}
	// read_line(): Option[string] — read one line from stdin
	// (including the trailing '\n' if present), returning
	// Some(line) or None at end-of-file before any byte. The
	// ergonomic stdin reader for CLI tools — the bare counterpart
	// to `stdin().read_line()`. Implemented on every backend
	// (native reads byte-by-byte into a scratch buffer; wasm wraps
	// preview-1 `fd_read` / preview-2 `wasi:cli/stdin::get-stdin` +
	// `wasi:io/streams::blocking-read`).
	c.info.FuncSigs["read_line"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.EnumType{Name: "Option", Args: []ast.Type{ast.StringType{}}},
	}
	// sleep_ms(ms): void — best-effort sleep for the given
	// duration. Useful in tests that want to wait for a
	// timer / background process to make progress. Not
	// designed for hard real-time guarantees — Go's runtime
	// scheduler may delay wakeup under load.
	c.info.FuncSigs["sleep_ms"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{Width: 64, Signed: true}},
		Result: ast.VoidType{},
	}
	// proc_fork(): i32 — fork the process (docs/CRASH-ONLY-SERVE.md
	// D2'). Returns 0 in the child, the child's pid in the parent,
	// or a negative errno on failure. Capability-gated (`proc`,
	// native targets only — E066 elsewhere). The interpreter cannot
	// bare-fork (Go's runtime is threaded) and returns -38 (ENOSYS);
	// callers like `tcp_serve_supervised` treat that as "supervision
	// unavailable" and degrade to single-process serving.
	c.info.FuncSigs["proc_fork"] = &ast.FuncType{
		Params: []ast.Type{},
		Result: ast.NumberType{},
	}
	// proc_waitpid(pid): i32 — block until the child `pid` exits and
	// return its exit code 0..255; for a signal death, 128+signal
	// (shell convention). Negative errno on failure (interp: -10 /
	// ECHILD — no forked children can exist there). Same `proc`
	// capability gate as proc_fork.
	c.info.FuncSigs["proc_waitpid"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.NumberType{},
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
	// stat(path): Result[FileStat, IoError] — pull file
	// metadata. `is_file` / `is_dir` distinguish the kind;
	// `size` carries byte size for regular files (and the
	// directory entry size on POSIX for `is_dir == true`,
	// which is platform-defined but useful as a non-zero
	// signal that the directory exists). Mtime is omitted
	// pending a Time type in the language.
	c.info.FuncSigs["stat"] = &ast.FuncType{
		Params: []ast.Type{ast.StringType{}},
		Result: ast.EnumType{Name: "Result", Args: []ast.Type{
			ast.StructType{Name: "FileStat"},
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
	// stay clear of `examples/self_host/vm.fern`'s long-
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
	registerMapMethod("set", []ast.Type{keyParam, valueParam}, mapKV)
	registerMapMethod("keys", nil, ast.ArrayType{Elem: keyParam})
	registerMapMethod("values", nil, ast.ArrayType{Elem: valueParam})
	deleteResult := ast.TupleType{Elems: []ast.Type{mapKV, ast.BoolType{}}}
	registerMapMethod("delete", []ast.Type{keyParam}, deleteResult)
	registerMapMethod("clear", nil, mapKV)
	registerMapMethod("get_or", []ast.Type{keyParam, valueParam}, valueParam)
	mapIterKV := ast.StructType{Name: "MapIter", Args: []ast.Type{keyParam, valueParam}}
	registerMapMethod("iter", nil, mapIterKV)
	// Value-returning aliases (docs/PURE-COLLECTION-API-PLAN.md §3a) —
	// the immutable-looking vocabulary the pure-collection-API work is
	// migrating onto. Each alias resolves to the SAME mangled lowering
	// as its mutable-looking sibling, so dispatch (checker.go:6392,
	// rewriting the call to the mangled ident) and the IR keyed on that
	// name are reused wholesale — purely additive, zero IR change, no
	// breakage. The mutable-looking names (`set`/`delete`/`clear`) stay
	// for now; a later slice marks them deprecated and eventually
	// removes them once call sites have migrated.
	c.info.Methods["Map.insert"] = "__method_Map_set"     // m.insert(k, v) — value-returning set
	c.info.Methods["Map.without"] = "__method_Map_delete" // m.without(k) — value-returning delete
	c.info.Methods["Map.cleared"] = "__method_Map_clear"  // m.cleared() — value-returning clear

	// `arr.push(v)` is the one Array method that DOESN'T have a
	// stdlib function declaration — the IR intercepts the
	// rewritten `__method_Array_push(arr, v)` call and emits the
	// alloc + memcpy + width-correct tail store inline (see
	// `emitArrayPush` in `internal/ir/ir.go`). One codepath covers
	// every stride class — no per-stride mangled names, no
	// per-stride stdlib functions. Because there's no source-
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
	// `arr.set(i, v)` — Phase 2b's value-returning sister to
	// `arr[i] = v`. The IR intercepts the rewritten
	// `__method_Array_set(arr, i, v)` call and emits the
	// `__fern_arr_cow_inplace` shape inline (see emitArraySet).
	// Useful when callers want explicit value semantics in
	// shapes the `arr[i] = v` desugar doesn't yet cover
	// (parameter targets, slice writes, expression-position
	// chaining like `m = m.set(k, v).set(k2, v2)`).
	c.info.Methods["Array.set"] = "__method_Array_set"
	c.info.FuncSigs["__method_Array_set"] = &ast.FuncType{
		Params: []ast.Type{
			ast.ArrayType{Elem: arrayElemParam},
			ast.NumberType{},
			arrayElemParam,
		},
		Result: ast.ArrayType{Elem: arrayElemParam},
	}
	// Value-returning aliases (docs/PURE-COLLECTION-API-PLAN.md §3a),
	// resolving to the same mangled lowerings as `push` / `set`. See
	// the Map alias note above — purely additive, no IR change.
	c.info.Methods["Array.append"] = "__method_Array_push" // arr.append(x) — value-returning push
	c.info.Methods["Array.with"] = "__method_Array_set"    // arr.with(i, v) — value-returning element set
	// Remove the mutable-looking spellings (docs/PURE-COLLECTION-API-PLAN.md
	// §3a, "hard removal" step). The value-returning aliases above
	// (insert / without / cleared / with) are now the ONLY public names;
	// the mangled lowerings (__method_Map_set / _delete / _clear /
	// __method_Array_set) stay — the aliases resolve to them — so this
	// only deletes the user-facing names, with zero IR change. `arr[i] = v`
	// still lowers through __method_Array_set via the desugar; only the
	// `arr.set(i, v)` *method* spelling is withdrawn (use `arr.with`);
	// `arr.push(x)` is withdrawn (use `arr.append`).
	delete(c.info.Methods, "Map.set")
	delete(c.info.Methods, "Map.delete")
	delete(c.info.Methods, "Map.clear")
	delete(c.info.Methods, "Array.set")
	delete(c.info.Methods, "Array.push")
	// `arr.len()` — like push, the IR intercepts the rewritten
	// `__method_Array_len(arr)` call and inlines the [ptr - 4]
	// length-prefix load. One generic signature covers every
	// element type.
	c.info.Methods["Array.len"] = "__method_Array_len"
	c.info.FuncSigs["__method_Array_len"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: arrayElemParam}},
		Result: ast.NumberType{},
	}
	// `sl.len()` — slice length. The IR inlines a load of the
	// 4-byte length field at `slice + 4` (after the data
	// pointer). Generic over element type like Array.len.
	sliceElemParam := ast.ParamType{Name: "T"}
	c.info.Methods["slice.len"] = "__method_slice_len"
	c.info.FuncSigs["__method_slice_len"] = &ast.FuncType{
		Params: []ast.Type{ast.SliceType{Elem: sliceElemParam}},
		Result: ast.NumberType{},
	}

	// Cell[T] — single-slot mutable box (docs/CELL-TYPE-PLAN.md).
	// `cell_new(v)` returns a Cell with no Args (like map_new, the
	// destination type drives T); `get` reads the slot, `set` writes
	// it in place and returns void (so `c.set(v);` is a normal
	// statement, not an E055 discard). All three are IR-intercepted.
	cellParam := ast.ParamType{Name: "T"}
	cellT := ast.StructType{Name: "Cell", Args: []ast.Type{cellParam}}
	c.info.FuncSigs["cell_new"] = &ast.FuncType{
		Params: []ast.Type{cellParam},
		Result: ast.StructType{Name: "Cell"},
	}
	c.info.Methods["Cell.get"] = "__method_Cell_get"
	c.info.FuncSigs["__method_Cell_get"] = &ast.FuncType{
		Params: []ast.Type{cellT},
		Result: cellParam,
	}
	c.info.Methods["Cell.set"] = "__method_Cell_set"
	c.info.FuncSigs["__method_Cell_set"] = &ast.FuncType{
		Params: []ast.Type{cellT, cellParam},
		Result: ast.VoidType{},
	}

	// Auto-discover the remaining Array methods from the
	// `__method_Array_<name>` naming convention. Every stdlib
	// function (and, post-migration, every `std/array` module
	// function) that follows the convention registers itself for
	// the `arr.<name>(…)` dispatch path without a hand-written
	// line per method. The receiver-element constraint stays
	// inside the stdlib function signature (e.g.
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
	// `s.as_bytes()` — non-copying companion to `s.bytes()`. Returns
	// a `[u8]` slice header whose data_ptr aliases the string's
	// payload and whose len is `len(s)`. Sharing the parent's
	// lifetime is fine under the bump allocator (the string's
	// storage lives until the arena tears down).
	registerStringMethod("as_bytes", nil, ast.SliceType{Elem: ast.NumberType{Width: 8, Signed: false}})
	// `s.len()` — IR intercepts the rewritten
	// `__method_string_len(s)` call and emits OpStrLen so a
	// future SSO pass can change the encoding in one place.
	registerStringMethod("len", nil, ast.NumberType{})

	c.info.FuncSigs["string_from_bytes"] = &ast.FuncType{
		Params: []ast.Type{ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}}},
		Result: ast.StringType{},
	}

	// `__memcpy(dst, src, n)` / `__memset(dst, b, n)` —
	// thin lang-callable wrappers around wasm's bulk-memory
	// `memory.copy` / `memory.fill`. The doc-roadmap calls
	// them out as the unlock for moving the json buffer
	// family + the Map runtime from hand-written wat into
	// the stdlib (every growable-byte-buffer pattern
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
	// the `[u8] → i32` data-pointer cast so stdlib code can
	// build a single-pass byte buffer.
	c.info.FuncSigs["__alloc_u8"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: ast.ArrayType{Elem: ast.NumberType{Width: 8, Signed: false}},
	}
	// Raw-memory escape hatches for stdlib code that
	// builds typed-pointer arrays (`__array_append_string`)
	// or runtime structures (the Map runtime migration).
	// `__alloc(n)` returns a raw n-byte block, no length
	// prefix; `__load_i32` / `__store_i32` peek and poke a
	// 4-byte word at any address. Out-of-bounds traps at
	// the wasm level — the stdlib is expected to bounds-
	// check at the lang level.
	// `__alloc(n)` returns a fresh n-byte block on the bump heap.
	// Returns `usize` so the full address survives on arm64-darwin
	// where the heap lives above 4 GiB.
	c.info.FuncSigs["__alloc"] = &ast.FuncType{
		Params: []ast.Type{ast.NumberType{}},
		Result: usizeT,
	}
	// `__fern_rc_inc(ptr)` bumps the rc word at [ptr-8] and
	// returns ptr (a no-op on null / low / static-sentinel
	// pointers). Exposed to the Map runtime so __map_get*/values
	// can retain a pointer-shaped value before handing it out —
	// the read side of map-value reclamation.
	c.info.FuncSigs["__fern_rc_inc"] = &ast.FuncType{
		Params: []ast.Type{usizeT},
		Result: usizeT,
	}
	// `__fern_arr_dec(data, stride)` / `__fern_drop_arr_ptr(data,
	// stride)` dec an array's rc and, on the last reference, free
	// its buffer (drop_arr_ptr also recurses one level to dec
	// pointer-shaped elements first). Exposed to the Map runtime
	// so __map_drop_values can free array-typed values — the
	// write/read incs (inc-on-set / inc-on-get) balance these.
	c.info.FuncSigs["__fern_arr_dec"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}},
		Result: usizeT,
	}
	c.info.FuncSigs["__fern_drop_arr_ptr"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}},
		Result: usizeT,
	}
	// `__free(ptr, size)` returns the `size`-byte block at `ptr` to
	// the allocator's freelist (Phase 3 step 4). A no-op unless the
	// freelist is enabled (ast.RcFreeEnabled). `size` must be the
	// same value passed to the matching `__alloc`.
	c.info.FuncSigs["__free"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}},
		Result: ast.VoidType{},
	}
	// `__alloc_reuse(token, tokenSize, size)` is the Phase 5 drop-reuse
	// (FBIP) primitive: when `token != 0` and its size class
	// (`(tokenSize+15)&-16`) equals `size`'s class, it returns `token`
	// in place — the dropped block's storage is reused for the new
	// allocation, skipping the free + alloc round trip. Otherwise it
	// returns the dropped block to the freelist (when non-null) and
	// bump/freelist-allocates `size` bytes via `__alloc`. A `0` token,
	// or a class mismatch, therefore degrades to a plain allocation, so
	// a mispaired reuse is slow-not-wrong (never an overflow, never a
	// leak). Slice 5a exposes it as a shim so the reuse-vs-alloc branch
	// is testable before the pairing analysis (5b+) emits it. `size`
	// must be the value that would be passed to the matching `__alloc`;
	// `tokenSize` the size the dropped block was allocated with.
	c.info.FuncSigs["__alloc_reuse"] = &ast.FuncType{
		Params: []ast.Type{usizeT, ast.NumberType{}, ast.NumberType{}},
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
	// `__c_call0..4(fn, args...)` — call a C-ABI function pointer `fn` with
	// up to four integer/pointer arguments, returning its result. The
	// arguments and result are usize (a raw machine word). This is the FFI
	// primitive for talking to C: a JNIEnv method (loaded from the env's
	// function table) or an NDK callback is just a function pointer invoked
	// with the System V / AAPCS64 integer-arg convention. The codegen emits
	// a tiny shim that re-shuffles Fern's arg registers into the C ABI and
	// tail-calls fn.
	for n, params := range map[string]int{"__c_call0": 1, "__c_call1": 2, "__c_call2": 3, "__c_call3": 4, "__c_call4": 5} {
		ps := make([]ast.Type, params)
		for i := range ps {
			ps[i] = usizeT
		}
		c.info.FuncSigs[n] = &ast.FuncType{Params: ps, Result: usizeT}
		// FP-return variants `__c_call<n>_f32` / `_f64`: same integer-arg
		// shim, but the result rides in an FP register (xmm0 / d0). These let
		// std/jni read float/double JNIEnv methods (Get{Float,Double}Field,
		// CallFloatMethod, …). FP *arguments* are not modelled — only the
		// return crosses the FP boundary.
		ps32 := make([]ast.Type, params)
		ps64 := make([]ast.Type, params)
		for i := range ps32 {
			ps32[i] = usizeT
			ps64[i] = usizeT
		}
		c.info.FuncSigs[n+"_f32"] = &ast.FuncType{Params: ps32, Result: ast.FloatType{Width: 32}}
		c.info.FuncSigs[n+"_f64"] = &ast.FuncType{Params: ps64, Result: ast.FloatType{Width: 64}}
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
	// next to `__store_i32` if a future stdlib helper
	// needs them.

	// Built-in numeric methods. The receiver type is `NumberType`
	// keyed by width + signedness; the dispatch path above maps
	// `i32` / `u32` / `i64` / `u64` value types to the
	// corresponding `__method_<typename>_<method>` mangled name.

	// Register trait declarations so the conformance check (after the
	// receiver-hoist loop below) and Phase 2 bound resolution can look
	// them up. Duplicate trait names are an error. See docs/TRAITS.md.
	for _, td := range prog.Traits {
		if _, dup := c.info.Traits[td.Name]; dup {
			c.errfCode(td.P, "E006", "trait %q redeclared", td.Name)
			continue
		}
		seenMethod := map[string]bool{}
		for _, m := range td.Methods {
			if seenMethod[m.Name] {
				c.errfCode(m.P, "E006", "trait method %q redeclared in trait %q", m.Name, td.Name)
				continue
			}
			seenMethod[m.Name] = true
		}
		c.info.Traits[td.Name] = td
	}

	// `@derive(Trait)` on a struct: synthesise a field-wise impl per
	// derived trait (appending receiver-method FuncDecls + an ImplDecl)
	// before the receiver-hoist and conformance passes pick them up.
	c.synthesizeDerives(prog)

	// A trait method written with a `{ … }` body is a default: any impl
	// that omits it inherits a copy (with `Self` substituted to the impl
	// type). Synthesise those receiver-method FuncDecls now — after
	// derives, before the receiver-hoist — so they flow through the
	// existing hoist + conformance + dispatch paths unchanged. See
	// docs/TRAITS.md.
	c.synthesizeTraitDefaults(prog)

	// Colorless stream result: an `@import async function f(): stream[T]` is
	// delivered incrementally over the wire but, under the colorless model,
	// yields the fully-collected `T[]` at the call site (docs/STREAM-TYPE-SURFACE.md).
	// Rewrite the effective return type to `T[]` here — before FuncSigs and every
	// other fn.ReturnType reader — and stash the element type so codegen knows to
	// use the stream-collect ABI rather than the single-block list lowering.
	for _, fn := range prog.Funcs {
		if fn.ImportIface != "" && fn.Async {
			if st, ok := fn.ReturnType.(ast.StreamType); ok && st.Elem != nil {
				fn.StreamResultElem = st.Elem
				fn.ReturnType = ast.ArrayType{Elem: st.Elem}
			}
			// Colorless stream PARAMETER (the mirror of the result transform): an
			// `@import async function f(s: stream[T])` accepts an eager `T[]` at the
			// call site (the wrapper creates a stream and write-streams the array's
			// elements over the wire). Rewrite each `stream[T]` param to `T[]` and
			// stash the element type so codegen uses the stream-produce ABI.
			for i := range fn.Params {
				if st, ok := fn.Params[i].Type.(ast.StreamType); ok && st.Elem != nil {
					if fn.StreamParamElems == nil {
						fn.StreamParamElems = map[int]ast.Type{}
					}
					fn.StreamParamElems[i] = st.Elem
					fn.Params[i].Type = ast.ArrayType{Elem: st.Elem}
				}
			}
		}
	}

	// Lazy stream iteration (L2): the parser leaves `for x in f(args)` as an
	// ast.ForEach when `f` is a u8 async stream import (every other iterand was
	// lowered to the array `.len()`+index loop at parse time). Lower those
	// surviving ForEach nodes HERE — after modload has mangled cross-module call
	// sites, so the synthesised `f$open` tracks the (possibly mangled) import
	// name — into a per-element read loop (ast.DesugarForEachStream), and register
	// the codegen-helper signatures the loop calls. The helpers (`f$open`,
	// `__stream_next_u8`, `__stream_drop`) are emitted by wasmbin
	// (internal/codegen/wasmbin/extern.go). See docs/STREAM-TYPE-SURFACE.md.
	streamElem := map[string]ast.Type{}
	elemKinds := map[string]ast.Type{} // kind → element type, for the __stream_elem_<kind> sigs
	for _, fn := range prog.Funcs {
		if fn.ImportIface == "" || fn.StreamResultElem == nil {
			continue
		}
		if kind := ast.StreamElemKind(fn.StreamResultElem); kind != "" {
			streamElem[fn.Name] = fn.StreamResultElem
			elemKinds[kind] = fn.StreamResultElem
			// Per-import open helper: the import's scalar params → an i32 cursor
			// pointer (the awaited stream-result lower wrapped in a read cursor).
			params := make([]ast.Type, len(fn.Params))
			for i, p := range fn.Params {
				params[i] = p.Type
			}
			c.info.FuncSigs[fn.Name+"$open"] = &ast.FuncType{Params: params, Result: ast.NumberType{Width: 32, Signed: true}}
		}
	}
	if len(streamElem) > 0 {
		i32 := ast.NumberType{Width: 32, Signed: true}
		// __stream_next(cursor) -> i32 (1 = element read into the cursor, 0 = EOF);
		// __stream_drop(cursor) -> () ; both element-type-agnostic.
		c.info.FuncSigs["__stream_next"] = &ast.FuncType{Params: []ast.Type{i32}, Result: i32}
		c.info.FuncSigs["__stream_drop"] = &ast.FuncType{Params: []ast.Type{i32}, Result: ast.VoidType{}}
		// __stream_elem_<kind>(cursor) -> T : reads the buffered element as its
		// scalar type, one per distinct element kind actually used.
		for kind, elem := range elemKinds {
			c.info.FuncSigs["__stream_elem_"+kind] = &ast.FuncType{Params: []ast.Type{i32}, Result: elem}
		}
		lowerStreamForEachProgram(prog, streamElem)
	}

	// First pass: gather all top-level signatures so functions can call
	// each other in any order. Methods are hoisted to mangled
	// top-level names (`__method_<Type>_<Name>`) with the receiver
	// prepended to the parameter list, so codegen never has to know
	// about methods.
	for _, fn := range prog.Funcs {
		if fn.AssocType != "" {
			// Associated function (`impl … for T { function f(): Self }`):
			// no receiver, called as `T.f(args)`. Hoist to a flat
			// `__assoc_<T>_<f>` and register under the `T.f` key so the
			// Call-case resolves `T.f(args)` (a FieldAccess on a *type*
			// name) to it with no receiver argument. Stamp MethodRecv /
			// MethodSimpleName so the monomorph re-check re-registers it
			// from the `else if fn.MethodRecv != ""` branch below after
			// Name has been rewritten. Then fall through to the common
			// FuncSig / GenericFuncs registration.
			typeName := fn.AssocType
			methodKey := typeName + "." + fn.Name
			if _, dup := c.info.Methods[methodKey]; dup {
				c.errfCode(fn.P, "E006", "associated function %q on %s redeclared", fn.Name, typeName)
				continue
			}
			fn.MethodRecv = typeName
			fn.MethodSimpleName = fn.Name
			fn.Name = "__assoc_" + typeName + "_" + fn.Name
			fn.AssocType = ""
			c.info.Methods[methodKey] = fn.Name
			c.info.MethodSources[fn.Name] = fn.SourceModule
		} else if fn.Receiver != nil {
			var typeName string
			switch rt := fn.Receiver.Type.(type) {
			case ast.StructType:
				if _, ok := c.info.Structs[rt.Name]; !ok {
					c.errfCode(fn.P, "E021", "method receiver references unknown struct %q", rt.Name)
					continue
				}
				typeName = rt.Name
			case ast.EnumType:
				if _, ok := c.info.Enums[rt.Name]; !ok {
					c.errfCode(fn.P, "E021", "method receiver references unknown enum %q", rt.Name)
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
			case ast.BoolType:
				typeName = "boolean"
			case ast.ArrayType:
				// Element-polymorphic method on an owned array,
				// `function (xs: T[]) first(): T`. Registered under the
				// same "Array" namespace the built-in array methods +
				// the call-site dispatch use; `collectFreeTypeVars`
				// already bound the element type-var, so it's a generic
				// method inferred from the receiver's element type.
				// The element MUST be a type variable: the "Array"
				// namespace can't distinguish element types, so a
				// concrete `(xs: i32[]) ...` would wrongly apply to every
				// array — reject it.
				if _, ok := rt.Elem.(ast.ParamType); !ok {
					c.errfCode(fn.P, "E021", "array-receiver method must be element-polymorphic (e.g. `(xs: T[])`); a concrete element type like %s is not supported", fn.Receiver.Type)
					continue
				}
				typeName = "Array"
			case ast.SliceType:
				// Same for a slice view, `function (xs: [T]) head(): T`,
				// under the "slice" namespace.
				if _, ok := rt.Elem.(ast.ParamType); !ok {
					c.errfCode(fn.P, "E021", "slice-receiver method must be element-polymorphic (e.g. `(xs: [T])`); a concrete element type like %s is not supported", fn.Receiver.Type)
					continue
				}
				typeName = "slice"
			default:
				c.errfCode(fn.P, "E021", "method receiver type must be a struct, enum, array, slice, or built-in type, got %s", fn.Receiver.Type)
				continue
			}
			methodKey := typeName + "." + fn.Name
			if _, dup := c.info.Methods[methodKey]; dup {
				c.errfCode(fn.P, "E006", "method %q on %s redeclared", fn.Name, typeName)
				continue
			}
			mangled := "__method_" + typeName + "_" + fn.Name
			// Stamp the method identity so a later Check pass can
			// re-register it (Receiver is about to be cleared).
			simpleName := fn.Name
			// Rewrite the FuncDecl so codegen sees a regular
			// top-level function with the receiver as its first
			// parameter.
			fn.Name = mangled
			fn.MethodRecv = typeName
			fn.MethodSimpleName = simpleName
			// Consuming methods are supported: the receiver â including an `own`
			// one, `(own self: T)` â is hoisted to Params[0], so the method lowers
			// like a free function with an `own` first parameter. The method-call
			// ownership transfer (move-on-call) and the E051 call-site guard both
			// handle method calls, so this is sound.
			fn.Params = append([]ast.Param{*fn.Receiver}, fn.Params...)
			fn.Receiver = nil
			c.info.Methods[methodKey] = mangled
			c.info.MethodSources[mangled] = fn.SourceModule
		} else if fn.MethodRecv != "" {
			// Already-hoisted method whose Receiver was consumed by a
			// previous Check pass — this is what the monomorph
			// re-check sees (Check rebuilds Info from scratch, but the
			// FuncDecl no longer carries a Receiver). Re-register it
			// from the stamped identity (robust against mangled type
			// names like `shapes__Square` that the name alone can't be
			// split on). Idempotent: only fills a missing key.
			// See docs/TRAITS.md §4.
			key := fn.MethodRecv + "." + fn.MethodSimpleName
			if _, exists := c.info.Methods[key]; !exists {
				c.info.Methods[key] = fn.Name
				c.info.MethodSources[fn.Name] = fn.SourceModule
			}
		}
		if _, dup := c.info.FuncSigs[fn.Name]; dup {
			c.errfCode(fn.P, "E006", "function %q redeclared", fn.Name)
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

	// Trait conformance + coherence. By now every impl method has been
	// hoisted into c.info.Methods under `__method_<Type>_<name>`, so an
	// impl satisfies its trait iff every trait method is registered for
	// the `for` type with a matching signature. We also enforce the
	// orphan rule (the impl's module must declare the trait or the
	// type) and reject duplicate `(Trait, Type)` impls. See docs/TRAITS.md.
	//
	// Per-(hoisted method) `own` flags, read straight off the FuncDecl params
	// since c.ownFuncs / info.OwnFuncs aren't built until after this pass. The
	// receiver-hoist above already merged each method's receiver into Params[0],
	// so index 0 is `self`.
	methodOwns := map[string][]bool{}
	for _, fn := range prog.Funcs {
		flags := make([]bool, len(fn.Params))
		any := false
		for i, p := range fn.Params {
			flags[i] = p.Own
			any = any || p.Own
		}
		if any {
			methodOwns[fn.Name] = flags
		}
	}
	for _, impl := range prog.Impls {
		// Inherent impl (`impl Type { … }`, #2700): no trait, so there is
		// nothing to check for conformance/coherence here. Its methods and
		// associated functions were already desugared into ordinary
		// FuncDecls (hoisted as `__method_*` / `__assoc_*`) by the parser,
		// so they register and check through the normal paths.
		if impl.Trait == "" {
			continue
		}
		typeName, ok := methodTypeName(impl.Type)
		if !ok {
			c.errfCode(impl.TypePos, "E021", "`impl … for %s`: type must be a struct, enum, or built-in type", impl.Type)
			continue
		}
		td, ok := c.info.Traits[impl.Trait]
		if !ok {
			c.errfCode(impl.TraitPos, "E021", "unknown trait %q in impl", demangle(impl.Trait))
			continue
		}
		// Generic-trait arity: `impl From[i32] for T` must supply exactly
		// one type argument per the trait's type parameters. See docs/TRAITS.md.
		if len(impl.TraitArgs) != len(td.TypeParams) {
			c.errfCode(impl.TraitPos, "E021", "trait %s takes %d type argument(s), %d supplied",
				demangle(impl.Trait), len(td.TypeParams), len(impl.TraitArgs))
			continue
		}
		// Orphan rule: legal only if the trait or the type is declared
		// in the impl's own module. Single-file programs leave every
		// SourceModule empty, so the rule is vacuous there.
		if impl.SourceModule != "" {
			traitLocal := td.SourceModule == impl.SourceModule
			typeLocal := false
			if sd, ok := c.info.Structs[typeName]; ok && sd.SourceModule == impl.SourceModule {
				typeLocal = true
			}
			if ed, ok := c.info.Enums[typeName]; ok && ed.SourceModule == impl.SourceModule {
				typeLocal = true
			}
			if !traitLocal && !typeLocal {
				c.errfCode(impl.P, "E021",
					"orphan impl: `impl %s for %s` must be declared in the module that defines the trait or the type",
					demangle(impl.Trait), demangle(typeName))
				continue
			}
		}
		// At most one impl per (Trait, Type) across the whole program.
		if c.info.Impls[impl.Trait] == nil {
			c.info.Impls[impl.Trait] = map[string]bool{}
		}
		if c.info.Impls[impl.Trait][typeName] {
			c.errfCode(impl.P, "E006", "duplicate impl: %s is already implemented for %s", demangle(impl.Trait), demangle(typeName))
			continue
		}
		// The impl must provide exactly the trait's methods — no more.
		traitMethods := map[string]bool{}
		for _, m := range td.Methods {
			traitMethods[m.Name] = true
		}
		for _, mn := range impl.MethodNames {
			if !traitMethods[mn] {
				c.errfCode(impl.P, "E021", "method %q is not a member of trait %s", mn, demangle(impl.Trait))
			}
		}
		// Impl-type-param names (`impl[T] … for Box[T]`) canonicalise to
		// ParamType so the want/got structural compare below doesn't split a
		// StructType("T") from a ParamType("T"). Empty for concrete impls.
		paramSub := map[string]ast.Type{}
		for _, tp := range impl.TypeParams {
			paramSub[tp] = ast.ParamType{Name: tp}
		}
		// Every trait method must be present with a matching signature.
		conforms := true
		for _, m := range td.Methods {
			// Associated trait methods (no `self`) hoist to `__assoc_…`;
			// ordinary methods to `__method_…`. The expected signature is
			// built from m.Params directly either way — an assoc method has
			// no leading `self`, and its hoisted form has no receiver, so
			// they align without a prepended receiver slot.
			prefix := "__method_"
			if m.Assoc {
				prefix = "__assoc_"
			}
			mangled := prefix + typeName + "_" + m.Name
			sig, ok := c.info.FuncSigs[mangled]
			if !ok {
				c.errfCode(impl.P, "E021", "%s does not implement %s: missing method %q", demangle(typeName), demangle(impl.Trait), m.Name)
				conforms = false
				continue
			}
			// Expected signature: the trait method with Self -> the
			// concrete type. m.Params[0] is `self: Self`, which lines
			// up with the hoisted method's prepended receiver.
			// Resolve associated-type projections on both sides via the
			// impl's own bindings, so `Self::Item` (expected, after
			// Self→impl-type) and the impl method's `Self::Item` both
			// collapse to the bound type before the structural compare.
			// Generic trait: bind the trait's type parameters to this
			// impl's TraitArgs (`impl From[i32] for …` → T=i32) before the
			// Self substitution, so `(self: Self): T` becomes `(IntBox): i32`
			// for the structural compare. See docs/TRAITS.md.
			traitSub := map[string]ast.Type{}
			if len(td.TypeParams) == len(impl.TraitArgs) {
				for i, tp := range td.TypeParams {
					traitSub[tp] = impl.TraitArgs[i]
				}
			}
			subTrait := func(t ast.Type) ast.Type {
				if len(traitSub) > 0 {
					t = substByName(t, traitSub)
				}
				return c.resolveProjWith(c.normalizeEnumKinds(ast.SubstSelf(t, impl.Type)), impl.AssocTypeBindings)
			}
			want := make([]ast.Type, len(m.Params))
			for i, p := range m.Params {
				want[i] = subTrait(p.Type)
			}
			wantRet := subTrait(m.Result)
			// Normalize the impl side to the same nominal kinds. The parser
			// defaults every bare type name to StructType (no symbol table), so a
			// trait signature naming an enum — `Self` on an enum impl, or a
			// literal `E` return — arrives as StructType(E) while the impl
			// method's real signature carries EnumType(E); without reconciling the
			// kinds, sigMatches saw a spurious mismatch (the E021 "expected
			// (E)=>E, got (E)=>E" that blocked every enum-returning trait method).
			gotSig := &ast.FuncType{Params: make([]ast.Type, len(sig.Params)), Result: c.resolveProjWith(c.normalizeEnumKinds(sig.Result), impl.AssocTypeBindings)}
			for i, pt := range sig.Params {
				gotSig.Params[i] = c.resolveProjWith(c.normalizeEnumKinds(pt), impl.AssocTypeBindings)
			}
			// A parametric impl of a GENERIC trait (`impl[T] Iterator[T] for
			// ArrayIter[T]`) binds the trait's type parameter to the impl's own
			// type parameter. The hoisted `got` signature has those references
			// resolved to ParamType (resolveTypeNames walks prog.Impls), but the
			// `want` side substituted the raw TraitArgs / impl.Type, where the
			// param is still an unresolved StructType. Both print as "T" yet
			// ast.Equal separates ParamType from StructType, so the compare
			// spuriously failed. Canonicalise both sides first. (Concrete impls
			// have an empty paramSub and skip this.)
			if len(paramSub) > 0 {
				for i := range want {
					want[i] = substByName(want[i], paramSub)
				}
				wantRet = substByName(wantRet, paramSub)
				for i := range gotSig.Params {
					gotSig.Params[i] = substByName(gotSig.Params[i], paramSub)
				}
				gotSig.Result = substByName(gotSig.Result, paramSub)
			}
			if !sigMatches(gotSig, want, wantRet) {
				c.errfCode(impl.P, "E021",
					"%s.%s has the wrong signature for trait %s: expected %s, got %s",
					demangle(typeName), m.Name, demangle(impl.Trait),
					(&ast.FuncType{Params: want, Result: wantRet}).String(), gotSig.String())
				conforms = false
			}
			// Ownership (`own`) is part of the trait contract. A generic call
			// `x.m()` through a `T: Trait` bound transfers (or borrows) each
			// argument based on the TRAIT's declared own-ness — so an impl whose
			// ownership disagrees would make that call double-free (impl consumes
			// where the trait borrows) or leak / move-after-use (the trait
			// consumes where the impl borrows). Require them to match
			// position-by-position; the impl's flags live in OwnFuncs (absent ==
			// all-borrowed).
			implOwns := methodOwns[mangled]
			for i, p := range m.Params {
				implOwn := i < len(implOwns) && implOwns[i]
				if p.Own != implOwn {
					verb := "must take `own` on"
					if !p.Own {
						verb = "must not take `own` on"
					}
					c.errfCode(impl.P, "E021",
						"%s.%s %s parameter %q to match trait %s",
						demangle(typeName), m.Name, verb, p.Name, demangle(impl.Trait))
					conforms = false
					break
				}
			}
		}
		// Associated types: the impl must bind exactly the trait's
		// associated types (no missing, no extras). Record the bindings so
		// resolveProj can resolve `Foo::Item` projections. See
		// docs/ASSOCIATED-TYPES.md.
		if td2, ok := c.info.Traits[impl.Trait]; ok {
			declared := map[string]bool{}
			for _, at := range td2.AssocTypes {
				declared[at] = true
			}
			for name := range impl.AssocTypeBindings {
				if !declared[name] {
					c.errfCode(impl.P, "E021", "impl of %s for %s binds associated type %q which the trait does not declare",
						demangle(impl.Trait), demangle(typeName), name)
					conforms = false
				}
			}
			for _, at := range td2.AssocTypes {
				if _, ok := impl.AssocTypeBindings[at]; !ok {
					c.errfCode(impl.P, "E021", "impl of %s for %s must bind associated type %q (`type %s = …;`)",
						demangle(impl.Trait), demangle(typeName), at, at)
					conforms = false
				}
			}
			if conforms && len(impl.AssocTypeBindings) > 0 {
				if c.info.AssocBindings[typeName] == nil {
					c.info.AssocBindings[typeName] = map[string]ast.Type{}
				}
				for name, bt := range impl.AssocTypeBindings {
					c.info.AssocBindings[typeName][name] = bt
				}
			}
		}
		if conforms {
			c.info.Impls[impl.Trait][typeName] = true
			if len(impl.TraitArgs) > 0 {
				if c.info.ImplTraitArgs[impl.Trait] == nil {
					c.info.ImplTraitArgs[impl.Trait] = map[string][]ast.Type{}
				}
				c.info.ImplTraitArgs[impl.Trait][typeName] = impl.TraitArgs
				// A parametric impl of this generic trait
				// (`impl[T] Iterator[T] for ArrayIter[T]`) leaves its trait
				// args generic. Record the `for` pattern (params canonicalised
				// to ParamType) so a bound check can recover T=i32 from a
				// concrete `ArrayIter[i32]` and resolve [T] to [i32].
				if len(paramSub) > 0 {
					if c.info.ImplForPattern[impl.Trait] == nil {
						c.info.ImplForPattern[impl.Trait] = map[string]ast.Type{}
					}
					c.info.ImplForPattern[impl.Trait][typeName] = substByName(impl.Type, paramSub)
				}
			}
		}
	}

	// Supertrait references must name real traits, and the supertrait
	// graph must be acyclic (a cyclic graph would still terminate via the
	// `seen` guard in collectTraitSupers, but it's a user error). See
	// docs/TRAITS.md.
	for _, td := range prog.Traits {
		for _, sup := range td.Supertraits {
			if _, ok := c.info.Traits[sup]; !ok {
				c.errfCode(td.P, "E021", "unknown supertrait %q in trait %s", demangle(sup), demangle(td.Name))
			}
		}
		if c.traitInItsOwnSupers(td.Name) {
			c.errfCode(td.P, "E021", "cyclic supertrait: %s is (transitively) its own supertrait", demangle(td.Name))
		}
	}

	// Supertrait satisfaction: an `impl Trait for T` requires T to also
	// implement every (transitive) supertrait of Trait. Run after the loop
	// above has registered all conforming impls, so impl order within the
	// program doesn't matter. See docs/TRAITS.md.
	for _, impl := range prog.Impls {
		typeName, ok := methodTypeName(impl.Type)
		if !ok {
			continue
		}
		if !c.info.Impls[impl.Trait][typeName] {
			continue // impl didn't conform; the missing-method error already fired
		}
		td, ok := c.info.Traits[impl.Trait]
		if !ok {
			continue
		}
		for _, sup := range c.expandTraits(td.Supertraits) {
			if !c.info.Impls[sup][typeName] {
				c.errfCode(impl.P, "E021",
					"impl %s for %s also requires `impl %s for %s` (supertrait of %s)",
					demangle(impl.Trait), demangle(typeName), demangle(sup), demangle(typeName), demangle(impl.Trait))
			}
		}
	}

	// Resolve concrete-base associated-type projections (`Foo::Item` →
	// the impl's binding) across signatures + bodies, now that the
	// conformance pass has recorded every impl's bindings. See
	// docs/ASSOCIATED-TYPES.md.
	c.resolveProjections(prog)

	// Validate that every trait named in a function's type-parameter
	// bounds actually exists. Catches typos / unknown traits before
	// the deferred-dispatch path silently fails to resolve. See
	// docs/TRAITS.md.
	for _, fn := range prog.Funcs {
		// Normalise generic-trait bound args so a type-parameter reference
		// (`I: Iterator[T]`) is a ParamType, not a same-named StructType —
		// the parser can't tell them apart at parse time. This lets
		// unifyType / substituteType and the trait-method-signature
		// instantiation treat `T` uniformly, so bound-driven inference
		// (#2691) and `t.0`-style element typing inside the body agree.
		if len(fn.TypeParams) > 0 && fn.BoundArgs != nil {
			tpSet := make(map[string]bool, len(fn.TypeParams))
			for _, tp := range fn.TypeParams {
				tpSet[tp] = true
			}
			for _, argLists := range fn.BoundArgs {
				for i := range argLists {
					for k := range argLists[i] {
						argLists[i][k] = normalizeParamRefs(argLists[i][k], tpSet)
					}
				}
			}
		}
		for tp, traits := range fn.Bounds {
			for i, traitName := range traits {
				td, ok := c.info.Traits[traitName]
				if !ok {
					c.errfCode(fn.P, "E021", "unknown trait %q in bound on type parameter %s", demangle(traitName), tp)
					continue
				}
				// A generic-trait bound must supply the right number of
				// type arguments (`T: From[i32]` for `trait From[T]`); a
				// non-generic trait must supply none. See docs/TRAITS.md.
				var nargs int
				if ba := fn.BoundArgs[tp]; i < len(ba) {
					nargs = len(ba[i])
				}
				if nargs != len(td.TypeParams) {
					c.errfCode(fn.P, "E021", "trait %s takes %d type argument(s), %d supplied in bound on type parameter %s",
						demangle(traitName), len(td.TypeParams), nargs, tp)
				}
			}
		}
	}

	// Auto-main from handle: when the user defines
	// `function handle(req: HttpRequest, plat: Platform):
	// HttpResponse` but no `main()`, synthesise a minimal main
	// that calls `tcp_serve(port, handle)` after reading PORT
	// from the environment (default 8080). tcp_serve constructs
	// a Platform per request and threads it through. The same
	// source then
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

	// Validate `dyn Trait` type usage (trait exists + object-safe) now
	// that Traits + Impls are populated. The sole reporter of these
	// type-level errors — the per-call dispatch path assumes validity.
	// See docs/DYN-TRAITS.md.
	c.validateDynTraitTypes(prog)
	c.validateResourceHandles(prog)
	c.validateExports(prog)

	// Second pass: check bodies. Per-function cancellation
	// checkpoint — the LSP can cancel a long type-check
	// mid-flight when a new edit invalidates the in-progress
	// result (docs/IDE-COMPILATION-RESEARCH.md Rec §1).
	// Record per-function `own` parameter flags for the call-site ownership
	// guard (E051), before any body is checked.
	c.ownFuncs = map[string][]bool{}
	for _, fn := range prog.Funcs {
		hasOwn := false
		flags := make([]bool, len(fn.Params))
		for i, p := range fn.Params {
			flags[i] = p.Own
			hasOwn = hasOwn || p.Own
		}
		if hasOwn {
			c.ownFuncs[fn.Name] = flags
		}
	}
	c.info.OwnFuncs = c.ownFuncs // expose to the IR for ownership-transfer lowering

	for _, fn := range prog.Funcs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		c.checkFunction(fn)
	}

	c.checkFipFunctions(prog)

	if len(c.errors) > 0 {
		return c.info, diag.Errors(c.errors)
	}

	// Composite-type `==` / `!=` was type-checked into a structural
	// `eq` method call stashed on `Binary.EqCall`. Replace each such
	// Binary in place with that call (`!a.eq(b)` for `!=`) so every
	// later pass — monomorph instantiation, treeshake liveness,
	// codegen, interp — sees an ordinary method call rather than a
	// hidden side channel. Runs on success only; it also runs inside
	// monomorph's re-check, so cloned generic bodies desugar too.
	ast.RewriteProgramExprs(prog, func(e ast.Expr) ast.Expr {
		// Error-converting `?` → its block-expr desugar (#3234).
		if t, ok := e.(*ast.TryOp); ok && t.Lowered != nil {
			return t.Lowered
		}
		// Composite unary minus: `-v` → `v.neg()`.
		if u, ok := e.(*ast.Unary); ok {
			if u.NegCall != nil {
				return u.NegCall
			}
			return e
		}
		b, ok := e.(*ast.Binary)
		if !ok {
			return e
		}
		if b.EqCall != nil {
			if b.EqNegate {
				return &ast.Unary{P: b.P, Op: "!", Operand: b.EqCall}
			}
			return b.EqCall
		}
		// Composite ordering: `a <op> b` → `a.cmp(b) <op> 0`. The
		// resulting comparison is a plain signed-i32 Binary (cmp
		// returns -1/0/1); stamp IntWidth so codegen picks i32.lt_s
		// etc. (this node is created post-check, so the checker's
		// width stamping never ran on it).
		if b.CmpCall != nil {
			return &ast.Binary{
				P:        b.P,
				Op:       b.Op,
				Left:     b.CmpCall,
				Right:    &ast.NumberLit{P: b.P, Value: 0, Width: 32},
				IntWidth: 32,
			}
		}
		// Composite arithmetic: `a <op> b` → `a.<add|sub|mul|div>(b)`.
		if b.ArithCall != nil {
			return b.ArithCall
		}
		// Default a leftover-polymorphic integer op to i32. An integer
		// `+`/`-`/`*`/… whose operands never got pinned to a concrete
		// width (an unannotated `var x = 2147483647; var y = x + 1`)
		// stays `IntWidth == 0`: the inference branch skips it
		// (`!common.Polymorphic`) and no `settleInt` hint ever reached
		// it. The compiled backends still compute it in a 32-bit
		// register (i32 is the default int), so it wraps; the AST
		// interpreter is width-driven and would otherwise keep the full
		// 64-bit value (#3581). Stamp the default i32 width here, AFTER
		// all settling, so a genuine i64 context (which `settleInt`
		// already set to 64) is untouched and only true defaults land at
		// 32. Float ops carry their width on FloatWidth and evaluate
		// through the interp's separate Float value, so the stamp is
		// inert for them; string concatenation is flagged separately.
		if b.IntWidth == 0 && b.FloatWidth == 0 && !b.IsStringConcat {
			switch b.Op {
			case "+", "-", "*", "/", "%", "&", "|", "^", "<<", ">>":
				b.IntWidth = 32
			}
		}
		return e
	})

	return c.info, nil
}

// methodVisibleHere reports whether `mangled` is callable from
// the current call-site context. The rule (per
// docs/PRELUDE-TO-MODULES.md) is: a method declared in module M
// is callable from a file F only if M ∈ closure(F).
//
// Empty source modules on either side skip the check —
// accommodation for checker-synthesised methods (Reader /
// Writer / Map / MapIter / the inline-IR `Array.push`) and
// single-file programs that bypass modload.
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
// reject; modload clears `SourceModule` on every stdlib-loaded
// fn, so the shortcut lets stdlib internals call each other
// freely — only USER → stdlib visibility still requires an
// explicit import.
// methodTypeName maps a type to the canonical name used in the
// `__method_<Type>_<name>` mangling and the Info.Methods keys. It
// mirrors the receiver-hoist switch so impl-conformance lookups land
// on the same key the hoist registered. Returns false for types that
// can't carry methods.
func methodTypeName(t ast.Type) (string, bool) {
	switch rt := t.(type) {
	case ast.StructType:
		return rt.Name, true
	case ast.EnumType:
		return rt.Name, true
	case ast.StringType:
		return "string", true
	case ast.StrType:
		// `str` (#4813) shares the `string` method surface: every string
		// receiver method (builtin len/as_bytes and the std/string family)
		// dispatches on a view too -- methods borrow their receiver.
		return "string", true
	case ast.NumberType:
		switch {
		case rt.NormalWidth() == 64 && rt.IsSigned():
			return "i64", true
		case rt.NormalWidth() == 64 && !rt.IsSigned():
			return "u64", true
		case !rt.IsSigned():
			return "u32", true
		default:
			return "i32", true
		}
	case ast.FloatType:
		if rt.NormalWidth() == 64 {
			return "f64", true
		}
		return "f32", true
	case ast.BoolType:
		return "boolean", true
	default:
		return "", false
	}
}

// setElemHintFor stamps c.elemHint with the element type of `dst` when
// `e` is directly an array literal and `dst` is an array type — the
// only shape the ArrayLit case consumes the hint for. Keeping the guard
// tight means the hint never leaks into an array literal nested inside
// some other expression at the same site. See docs/DYN-TRAITS.md.
func (c *checker) setElemHintFor(e ast.Expr, dst ast.Type) {
	c.elemHint = nil
	if _, ok := e.(*ast.ArrayLit); !ok {
		return
	}
	if at, ok := dst.(ast.ArrayType); ok {
		c.elemHint = at.Elem
	}
}

// forEachDynTrait invokes fn for every `dyn Trait` nested anywhere in
// t (directly, or inside an array / slice / tuple / func / generic
// argument). Drives the dyn-type validation pass.
func forEachDynTrait(t ast.Type, fn func(string)) {
	switch x := t.(type) {
	case ast.DynTraitType:
		// Multi-trait-aware: validate EVERY trait in the set so
		// `dyn Bogus + Eq` reports both the unknown and the
		// non-object-safe trait.
		for _, tr := range x.Traits {
			fn(tr)
		}
	case ast.ArrayType:
		forEachDynTrait(x.Elem, fn)
	case ast.SliceType:
		forEachDynTrait(x.Elem, fn)
	case ast.TupleType:
		for _, e := range x.Elems {
			forEachDynTrait(e, fn)
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			forEachDynTrait(p, fn)
		}
		forEachDynTrait(x.Result, fn)
	case ast.StructType:
		for _, a := range x.Args {
			forEachDynTrait(a, fn)
		}
	case ast.EnumType:
		for _, a := range x.Args {
			forEachDynTrait(a, fn)
		}
	}
}

// forEachDynTraitObj invokes fn on every DynTraitType nested in t (itself,
// or inside an array / slice / tuple / func / generic argument). Unlike
// forEachDynTrait it surfaces the whole object so callers can inspect the
// per-trait generic arguments (e.g. arity validation of `dyn Container[i32]`).
func forEachDynTraitObj(t ast.Type, fn func(ast.DynTraitType)) {
	switch x := t.(type) {
	case ast.DynTraitType:
		fn(x)
		for _, args := range x.Args {
			for _, a := range args {
				forEachDynTraitObj(a, fn)
			}
		}
	case ast.ArrayType:
		forEachDynTraitObj(x.Elem, fn)
	case ast.SliceType:
		forEachDynTraitObj(x.Elem, fn)
	case ast.TupleType:
		for _, e := range x.Elems {
			forEachDynTraitObj(e, fn)
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			forEachDynTraitObj(p, fn)
		}
		forEachDynTraitObj(x.Result, fn)
	case ast.StructType:
		for _, a := range x.Args {
			forEachDynTraitObj(a, fn)
		}
	case ast.EnumType:
		for _, a := range x.Args {
			forEachDynTraitObj(a, fn)
		}
	}
}

// forEachHandle invokes fn for every resource HandleType (`own R` /
// `borrow R`) nested anywhere in t. Drives the resource-handle validation
// pass (P5 — docs/WIT-BRING-YOUR-OWN.md).
func forEachHandle(t ast.Type, fn func(ast.HandleType)) {
	switch x := t.(type) {
	case ast.HandleType:
		fn(x)
	case ast.ArrayType:
		forEachHandle(x.Elem, fn)
	case ast.SliceType:
		forEachHandle(x.Elem, fn)
	case ast.TupleType:
		for _, e := range x.Elems {
			forEachHandle(e, fn)
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			forEachHandle(p, fn)
		}
		forEachHandle(x.Result, fn)
	case ast.StructType:
		for _, a := range x.Args {
			forEachHandle(a, fn)
		}
	case ast.EnumType:
		for _, a := range x.Args {
			forEachHandle(a, fn)
		}
	}
}

// validateResourceHandles is the sole reporter of resource-handle type-level
// errors: every `own R` / `borrow R` must name a declared `resource`. It walks
// every function signature (params + return) and local var annotation, once,
// reporting each unknown resource a single time. Mirrors validateDynTraitTypes
// (P5 — docs/WIT-BRING-YOUR-OWN.md).
func (c *checker) validateResourceHandles(prog *ast.Program) {
	reported := map[string]bool{}
	visit := func(t ast.Type, pos ast.Position) {
		forEachHandle(t, func(h ast.HandleType) {
			if reported[h.Resource] {
				return
			}
			if _, ok := c.info.Resources[h.Resource]; !ok {
				reported[h.Resource] = true
				c.errfCode(pos, "E021", "unknown resource %q in handle type %q", demangle(h.Resource), h.String())
			}
		})
	}
	for _, fn := range prog.Funcs {
		for _, p := range fn.Params {
			visit(p.Type, fn.P)
		}
		visit(fn.ReturnType, fn.P)
		if fn.Body != nil {
			c.walkVarTypes(fn.Body, visit)
		}
	}
}

// validateExports checks `@export` functions (P6 — bind a Fern function to a
// WIT world export, docs/WIT-BRING-YOUR-OWN.md). A world export is lifted with
// a single concrete canonical ABI, so it cannot be generic; and the export
// surface is for top-level functions, not methods. The body is type-checked
// like any function elsewhere.
func (c *checker) validateExports(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		if fn.ExportIface == "" {
			continue
		}
		if len(fn.TypeParams) > 0 {
			c.errfCode(fn.P, "E054", "@export function %q cannot be generic (a world export has a single concrete ABI)", fn.Name)
		}
		if fn.Receiver != nil || fn.MethodRecv != "" {
			c.errfCode(fn.P, "E054", "@export cannot be applied to a method (%q); use a top-level function", fn.Name)
		}
	}
}

// validateDynTraitTypes is the sole reporter of `dyn Trait` type-level
// errors: a trait named in a `dyn` type must exist and be object-safe.
// It walks every function signature (params + return) and local var
// annotation. Running once over type positions catches a `dyn Bogus`
// or `dyn Eq` even when no method is ever called on the value; the
// per-call dispatch path then assumes validity. One report per trait
// keeps the output tidy. See docs/DYN-TRAITS.md §3.
func (c *checker) validateDynTraitTypes(prog *ast.Program) {
	reported := map[string]bool{}
	visit := func(t ast.Type, pos ast.Position) {
		forEachDynTrait(t, func(trait string) {
			if reported[trait] {
				return
			}
			if _, ok := c.info.Traits[trait]; !ok {
				reported[trait] = true
				c.errfCode(pos, "E021", "unknown trait %q in `dyn` type", demangle(trait))
			}
		})
		// Per-object checks (object-safety + generic-arg arity), which need
		// the dyn type's pinned arguments / associated types. A generic
		// trait must PIN its type parameters (`dyn Container[i32]`, not bare
		// `dyn Container`); a trait with associated types must pin every one
		// (`dyn Producer[Item = i32]`) to be object-safe — the concrete type
		// is erased, so an unpinned `T` / `Self::Item` can't be resolved at
		// the call site. Reported once per trait.
		forEachDynTraitObj(t, func(dt ast.DynTraitType) {
			for i, trait := range dt.Traits {
				td, ok := c.info.Traits[trait]
				if !ok {
					continue
				}
				pinned := map[string]bool{}
				for _, b := range dt.AssocFor(i) {
					pinned[b.Name] = true
				}
				if safe, reason := c.objectSafe(trait, pinned); !safe {
					if !reported[trait] {
						reported[trait] = true
						c.errfCode(pos, "E021", "trait %s is not object-safe: %s, so it cannot be used as `dyn %s`",
							demangle(trait), reason, demangle(trait))
					}
					continue
				}
				if len(td.TypeParams) > 0 {
					if got := len(dt.ArgsFor(i)); got != len(td.TypeParams) {
						key := trait + "!arity"
						if reported[key] {
							continue
						}
						reported[key] = true
						c.errfCode(pos, "E021", "generic trait %s used as `dyn` must pin its type parameter(s): expected %d argument(s) (e.g. `dyn %s[...]`), got %d",
							demangle(trait), len(td.TypeParams), demangle(trait), got)
					}
				}
			}
		})
	}
	for _, fn := range prog.Funcs {
		for _, p := range fn.Params {
			visit(p.Type, fn.P)
		}
		visit(fn.ReturnType, fn.P)
		if fn.Body != nil {
			c.walkVarTypes(fn.Body, visit)
		}
	}
}

// walkVarTypes invokes visit on every local `var` declaration's
// annotated type within a block (recursing into nested blocks).
func (c *checker) walkVarTypes(b *ast.Block, visit func(ast.Type, ast.Position)) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		switch x := st.(type) {
		case *ast.Var:
			visit(x.Type, x.P)
		case *ast.Block:
			c.walkVarTypes(x, visit)
		case *ast.If:
			c.walkVarTypes(asBlock(x.Then), visit)
			c.walkVarTypes(asBlock(x.Else), visit)
		case *ast.While:
			c.walkVarTypes(asBlock(x.Body), visit)
		case *ast.Loop:
			c.walkVarTypes(asBlock(x.Body), visit)
		case *ast.For:
			c.walkVarTypes(asBlock(x.Body), visit)
		case *ast.Match:
			for _, arm := range x.Arms {
				c.walkVarTypes(arm.Body, visit)
			}
		}
	}
}

// mentionsSelf reports whether `Self` appears anywhere in a type —
// directly or nested inside an array / slice / tuple / func / generic
// argument. Drives the object-safety check.
func mentionsSelf(t ast.Type) bool {
	switch x := t.(type) {
	case ast.SelfType:
		return true
	case ast.ArrayType:
		return mentionsSelf(x.Elem)
	case ast.SliceType:
		return mentionsSelf(x.Elem)
	case ast.TupleType:
		for _, e := range x.Elems {
			if mentionsSelf(e) {
				return true
			}
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			if mentionsSelf(p) {
				return true
			}
		}
		return mentionsSelf(x.Result)
	case ast.StructType:
		for _, a := range x.Args {
			if mentionsSelf(a) {
				return true
			}
		}
	case ast.EnumType:
		for _, a := range x.Args {
			if mentionsSelf(a) {
				return true
			}
		}
	}
	return false
}

// objectSafe reports whether a trait's methods can be dispatched
// through a `dyn Trait` object. After the receiver `self: Self`, no
// method may mention Self in another parameter or in its result — the
// concrete type is erased behind the object, so the compiler can
// neither supply nor name a second value of it. Returns the offending
// reason for the diagnostic. See docs/DYN-TRAITS.md §3.
func (c *checker) objectSafe(traitName string, pinned map[string]bool) (bool, string) {
	td, ok := c.info.Traits[traitName]
	if !ok {
		return false, ""
	}
	// A trait with associated types is object-safe ONLY when the `dyn` type
	// PINS every one (`dyn Producer[Item = i32]`): the concrete type is
	// erased, so an unpinned associated type — and any `Self::Item`
	// projection in a method signature — can't be resolved at the call
	// site. See docs/ASSOCIATED-TYPES.md.
	for _, at := range td.AssocTypes {
		if !pinned[at] {
			return false, fmt.Sprintf("associated type %q is not pinned — write `dyn %s[%s = ...]`", at, demangle(traitName), at)
		}
	}
	for _, m := range td.Methods {
		for i := 1; i < len(m.Params); i++ { // params[0] is self: Self
			if mentionsSelf(m.Params[i].Type) {
				return false, fmt.Sprintf("method %q takes Self as a non-receiver parameter", m.Name)
			}
		}
		if mentionsSelf(m.Result) {
			return false, fmt.Sprintf("method %q returns Self", m.Name)
		}
	}
	return true, ""
}

// checkDynMethodCall type-checks a method call on a `dyn Trait`
// receiver against the trait's signature and marks the call for runtime
// dispatch (Call.DynTrait), leaving the callee a FieldAccess. Returns
// the call's result type (nil on a hard error). See docs/DYN-TRAITS.md.
func (c *checker) checkDynMethodCall(n *ast.Call, fa *ast.FieldAccess, dt ast.DynTraitType, s *scope) ast.Type {
	// Method resolution searches the UNION of the traits' method sets.
	// Each trait must be known + object-safe (validateDynTraitTypes
	// reports those errors once over type positions; bail silently here
	// so per-call sites don't repeat them).
	for i, tr := range dt.Traits {
		if _, ok := c.info.Traits[tr]; !ok {
			return nil
		}
		pinned := map[string]bool{}
		for _, b := range dt.AssocFor(i) {
			pinned[b.Name] = true
		}
		if safe, _ := c.objectSafe(tr, pinned); !safe {
			return nil
		}
	}
	// Find the method across all traits in the set. Exactly one trait
	// declaring it → use it. Two+ → an ambiguity error (disambiguation
	// syntax is a follow-up; docs/DYN-TRAITS.md §N). None → no-method.
	var tm *ast.TraitMethod
	var ownerTrait string
	var collidingTraits []string
	for _, tr := range dt.Traits {
		td := c.info.Traits[tr]
		for i := range td.Methods {
			if td.Methods[i].Name == fa.Field {
				if tm != nil && ownerTrait != tr {
					collidingTraits = append(collidingTraits, tr)
				} else {
					tm = &td.Methods[i]
					ownerTrait = tr
				}
				break
			}
		}
	}
	if len(collidingTraits) > 0 {
		all := append([]string{ownerTrait}, collidingTraits...)
		for i := range all {
			all[i] = demangle(all[i])
		}
		c.errfCode(fa.FieldPos, "E062", "ambiguous method %q on `%s`: declared by traits %s",
			fa.Field, dt.String(), strings.Join(all, ", "))
		return nil
	}
	if tm == nil {
		c.errfCode(fa.FieldPos, "E021", "no method %q on `%s`", fa.Field, dt.String())
		return nil
	}
	// Pin the owner trait's generic type parameters to the dyn object's
	// arguments before reading the signature: `dyn Container[i32]` makes
	// `get(): T` read as `get(): i32`. Self is still substituted to the
	// dyn type itself (below) so the receiver keeps its object type.
	if td := c.info.Traits[ownerTrait]; td != nil && len(td.TypeParams) > 0 {
		var args []ast.Type
		for i, tr := range dt.Traits {
			if tr == ownerTrait {
				args = dt.ArgsFor(i)
				break
			}
		}
		if len(args) == len(td.TypeParams) {
			sub := make(map[string]ast.Type, len(args))
			for i, tp := range td.TypeParams {
				sub[tp] = args[i]
			}
			pinned := substTraitMethodTypeParams(*tm, sub)
			tm = &pinned
		}
	}
	// Resolve `Self::Item` projections in the signature to the dyn object's
	// pinned associated types: `dyn Producer[Item = i32]` makes
	// `get(): Self::Item` read as `get(): i32`. The bindings come from the
	// owner trait's AssocFor; resolveProjWith rewrites every ProjType.
	if td := c.info.Traits[ownerTrait]; td != nil && len(td.AssocTypes) > 0 {
		var binds []ast.AssocBinding
		for i, tr := range dt.Traits {
			if tr == ownerTrait {
				binds = dt.AssocFor(i)
				break
			}
		}
		if len(binds) > 0 {
			bindings := make(map[string]ast.Type, len(binds))
			for _, b := range binds {
				bindings[b.Name] = b.Type
			}
			resolved := *tm
			resolved.Params = make([]ast.Param, len(tm.Params))
			for i, p := range tm.Params {
				p.Type = c.resolveProjWith(p.Type, bindings)
				resolved.Params[i] = p
			}
			resolved.Result = c.resolveProjWith(tm.Result, bindings)
			tm = &resolved
		}
	}
	// Trait method signatures are stored as written: a `self` receiver is
	// present in Params only when the author spelled it (`function area(self:
	// Self): i32`); the common `function area(): i32;` form has none. Strip a
	// leading self when present so the remainder are the call arguments —
	// indexing `Params[1:]` unconditionally panicked on the no-self form (and
	// silently dropped the first real argument when a method had params but no
	// explicit self).
	wantParams := tm.Params
	if len(wantParams) > 0 {
		if _, isSelf := wantParams[0].Type.(ast.SelfType); isSelf || wantParams[0].Name == "self" {
			wantParams = wantParams[1:]
		}
	}
	if len(n.Args) != len(wantParams) {
		c.errfCode(n.P, "E004", "method %q expects %d argument(s), got %d", fa.Field, len(wantParams), len(n.Args))
		return ast.SubstSelf(tm.Result, dt)
	}
	for i, arg := range n.Args {
		at := c.checkExpr(arg, s)
		want := ast.SubstSelf(wantParams[i].Type, dt)
		if at != nil && !c.argAssignable(want, at, wantParams[i].Own) {
			c.errfCode(arg.Pos(), "E038", "argument %d to %q: expected %s, got %s", i+1, fa.Field, want, at)
		}
	}
	n.Method = &ast.MethodCallSite{Field: fa.Field, FieldPos: fa.FieldPos, Receiver: dt}
	// DynTrait records the trait that OWNS the resolved method (not the
	// whole set) — that's what the IR vtable lookup and the interp's
	// error messages key on. Runtime dispatch is still by the receiver's
	// concrete type, so for the single-trait case this is unchanged.
	n.DynTrait = ownerTrait
	return ast.SubstSelf(tm.Result, dt)
}

// normalizeEnumKinds rewrites a type so every nominal reference to an enum is an
// ast.EnumType, recursively. The parser defaults any bare type name to
// ast.StructType (it has no symbol table), so a name that is actually an enum
// arrives as StructType — which then mismatches the same enum spelled EnumType
// elsewhere. Applied to both sides of trait-conformance comparison so the kinds
// line up; idempotent on already-correct types and a no-op when the name is a
// real struct / generic / builtin.
func (c *checker) normalizeEnumKinds(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.StructType:
		args := make([]ast.Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = c.normalizeEnumKinds(a)
		}
		if _, isEnum := c.info.Enums[x.Name]; isEnum {
			return ast.EnumType{Name: x.Name, Args: args}
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		args := make([]ast.Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = c.normalizeEnumKinds(a)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.ArrayType:
		return ast.ArrayType{Elem: c.normalizeEnumKinds(x.Elem)}
	case ast.SliceType:
		return ast.SliceType{Elem: c.normalizeEnumKinds(x.Elem)}
	case ast.TupleType:
		els := make([]ast.Type, len(x.Elems))
		for i, e := range x.Elems {
			els[i] = c.normalizeEnumKinds(e)
		}
		return ast.TupleType{Elems: els}
	}
	return t
}

// sigMatches reports whether the registered method signature sig has
// exactly the parameter types want and result type wantRet.
func sigMatches(sig *ast.FuncType, want []ast.Type, wantRet ast.Type) bool {
	if len(sig.Params) != len(want) {
		return false
	}
	for i := range want {
		if !ast.Equal(sig.Params[i], want[i]) {
			return false
		}
	}
	return ast.Equal(sig.Result, wantRet)
}

// demangle turns the first module-mangling `__` in a name back into a
// `.` for user-facing diagnostics, so a cross-module trait/type/func
// reads as `mod.Name` (how the user wrote it) rather than the internal
// `mod__Name`. A no-op for single-file / same-module names. See
// docs/TRAITS.md (Phase 3).
// deriveKind classifies a (possibly module-mangled) derived-trait name
// by its simple name: "Eq", "Display", "Ord", "Hash", "Json", "Default",
// or "Debug". Returns "" for any other trait — only these are derivable.
func deriveKind(name string) string {
	simple := name
	if i := strings.LastIndex(simple, "__"); i >= 0 {
		simple = simple[i+2:]
	}
	switch simple {
	case "Eq", "Display", "Ord", "Hash", "Json", "Default", "Debug":
		return simple
	}
	return ""
}

// typeImplsEqAndHash reports whether the named struct/enum implements
// BOTH Eq and Hash — the requirement for using it as a Map key, whose
// derived `hash` / `eq` methods drive the type-erased map runtime's
// keyed bucket choice + key comparison (#2671). Trait names in
// c.info.Impls may be module-mangled (`cmp__Hash`), so classify by
// simple name via deriveKind, which already strips the prefix.
func (c *checker) typeImplsEqAndHash(typeName string) bool {
	hasEq, hasHash := false, false
	for trait, types := range c.info.Impls {
		if !types[typeName] {
			continue
		}
		switch deriveKind(trait) {
		case "Eq":
			hasEq = true
		case "Hash":
			hasHash = true
		}
	}
	return hasEq && hasHash
}

// mapKeyTypeError returns an E045 message describing why `k` cannot be
// a Map key, or "" if it is a usable key. Usable keys are i32-sized
// scalars, strings, and struct/enum types that implement both Eq and
// Hash (#2671). A struct/enum that lacks the derives gets a message
// pointing at the fix; other composite types (tuple / array / slice /
// float) keep the historical "not yet supported" wording. A type
// parameter or polymorphic literal passes — it is resolved later (per
// monomorph instantiation) and re-checked then.
func (c *checker) mapKeyTypeError(k ast.Type) string {
	switch kt := k.(type) {
	case ast.NumberType, ast.StringType, ast.ParamType:
		return ""
	case ast.StructType:
		if c.typeImplsEqAndHash(kt.Name) {
			return ""
		}
		return fmt.Sprintf("map key type %s is not supported — a struct used as a key must derive Eq and Hash (`@derive(Eq, Hash)`)", k)
	case ast.EnumType:
		if c.typeImplsEqAndHash(kt.Name) {
			return ""
		}
		return fmt.Sprintf("map key type %s is not supported — an enum used as a key must derive Eq and Hash (`@derive(Eq, Hash)`)", k)
	}
	return fmt.Sprintf("map key type %s is not yet supported (use i32, string, or a struct/enum with `@derive(Eq, Hash)`)", k)
}

// synthesizeDerives expands every struct's `@derive(Trait, …)` into a
// field-wise `impl Trait for Struct`: the generated method bodies call
// the corresponding trait method on each field (`self.f.eq(other.f)`,
// `self.f.to_string()`, `self.f.cmp(other.f)`), so derivation composes
// — a field type only needs to itself implement the trait. The
// synthesised receiver-methods are appended to prog.Funcs and an
// ImplDecl to prog.Impls, ahead of the receiver-hoist + conformance
// passes. See docs/TRAITS.md.
func (c *checker) synthesizeDerives(prog *ast.Program) {
	for _, sd := range prog.Structs {
		if len(sd.Derives) == 0 {
			continue
		}
		derives := sd.Derives
		// Idempotent: clear so a later Check pass (the monomorph
		// re-check rebuilds + re-checks the program) doesn't
		// synthesise the impls a second time.
		sd.Derives = nil
		// Generic struct (`@derive(...) struct Box[T]`): synthesise a
		// PARAMETRIC impl `impl[T: Trait] Trait for Box[T]`. The
		// receiver carries `Box[T]` (ParamType args), each method is
		// generic over the struct's type params, and each param is
		// bound by the trait being derived — so the field-wise body
		// (`self.v.to_string()`, where `self.v: T`) type-checks via the
		// bound and monomorphises per instantiation. See docs/TRAITS.md.
		recvType, implTypeParams := deriveRecvStruct(sd)
		for _, dn := range derives {
			td, ok := c.info.Traits[dn]
			if !ok {
				c.errfCode(sd.P, "E021", "@derive(%s): unknown trait", demangle(dn))
				continue
			}
			_ = td
			kind := deriveKind(dn)
			if kind == "" {
				c.errfCode(sd.P, "E021", "cannot @derive(%s): only Eq, Display, Debug, Ord, Hash, Json, and Default are derivable", demangle(dn))
				continue
			}
			var method *ast.FuncDecl
			switch kind {
			case "Eq":
				method = synthEq(sd, recvType)
			case "Display":
				method = synthDisplay(sd, recvType)
			case "Debug":
				method = synthDebug(sd, recvType)
			case "Ord":
				method = synthOrd(sd, recvType)
			case "Hash":
				method = synthHash(sd, recvType)
			case "Json":
				method = synthJson(sd, recvType)
				// Also synthesise the deserialise companion `from_json(s):
				// Result[Self, string]` — but ONLY when the real std/json is
				// imported (its `json__json_parse` is in scope) and every field
				// is supported (i32 / string / boolean). A user's own inline
				// `trait Json` (no std/json) gets serialise-only, as does a
				// struct with array / nested / wider fields — to_json still
				// derives in both cases. See synthFromJson / #2695.
				// Only synthesise from_json when the real std/json is imported
				// (its `json__json_parse` was merged into prog.Funcs by modload).
				// FuncSigs isn't populated yet at derive-synth time, so scan the
				// merged function list.
				haveStdJson := false
				for _, pf := range prog.Funcs {
					if pf.Name == "json__json_parse" {
						haveStdJson = true
						break
					}
				}
				fj, fjOK := synthFromJson(sd, recvType)
				if haveStdJson && fjOK {
					fj.SourceModule = sd.SourceModule
					bindDeriveTypeParams(fj, implTypeParams, dn)
					prog.Funcs = append(prog.Funcs, fj)
				}
			case "Default":
				m, badField, badType := synthDefault(sd, recvType)
				if m == nil {
					c.errfCode(sd.P, "E021", "cannot @derive(Default) for %s: field %q has type %s, which has no default; implement Default by hand", demangle(sd.Name), badField, badType)
					continue
				}
				method = m
			}
			method.SourceModule = sd.SourceModule
			bindDeriveTypeParams(method, implTypeParams, dn)
			prog.Funcs = append(prog.Funcs, method)
			prog.Impls = append(prog.Impls, &ast.ImplDecl{
				P: sd.P, Trait: dn, TraitPos: sd.P, Type: recvType, TypePos: sd.P,
				MethodNames: []string{method.Name}, SourceModule: sd.SourceModule,
				TypeParams: implTypeParams,
			})
		}
	}
	for _, ed := range prog.Enums {
		if len(ed.Derives) == 0 {
			continue
		}
		derives := ed.Derives
		ed.Derives = nil
		// Generic enum (`@derive(...) enum Option[T]`): same parametric-
		// impl synthesis as generic structs above — `impl[T: Trait]
		// Trait for Option[T]`, with the variant-wise body comparing /
		// rendering payloads via the bound. See docs/TRAITS.md.
		recvType, implTypeParams := deriveRecvEnum(ed)
		for _, dn := range derives {
			if _, ok := c.info.Traits[dn]; !ok {
				c.errfCode(ed.P, "E021", "@derive(%s): unknown trait", demangle(dn))
				continue
			}
			var method *ast.FuncDecl
			switch deriveKind(dn) {
			case "Eq":
				method = synthEnumEq(ed, recvType)
			case "Display":
				method = synthEnumDisplay(ed, recvType)
			case "Debug":
				method = synthEnumDebug(ed, recvType)
			case "Ord":
				method = synthEnumOrd(ed, recvType)
			case "Hash":
				method = synthEnumHash(ed, recvType)
			case "Json":
				method = synthEnumJson(ed, recvType)
			case "Default":
				m, badType := synthEnumDefault(ed, recvType)
				if m == nil {
					c.errfCode(ed.P, "E021", "cannot @derive(Default) for enum %s: first variant has a payload of type %s, which has no default; implement Default by hand", demangle(ed.Name), badType)
					continue
				}
				method = m
			default:
				c.errfCode(ed.P, "E021", "cannot @derive(%s): only Eq, Display, Debug, Ord, Hash, Json, and Default are derivable for enums", demangle(dn))
				continue
			}
			method.SourceModule = ed.SourceModule
			bindDeriveTypeParams(method, implTypeParams, dn)
			prog.Funcs = append(prog.Funcs, method)
			prog.Impls = append(prog.Impls, &ast.ImplDecl{
				P: ed.P, Trait: dn, TraitPos: ed.P, Type: recvType, TypePos: ed.P,
				MethodNames: []string{method.Name}, SourceModule: ed.SourceModule,
				TypeParams: implTypeParams,
			})
		}
	}
}

// synthesizeTraitDefaults materialises a trait's default methods for
// every impl that omits them. A trait method written with a `{ … }`
// body is a default: an impl that doesn't provide its own copy inherits
// one here. We deep-clone the default body (so each impl gets an
// isolated copy the checker can rewrite in place), substitute `Self` to
// the impl type across the signature, build a receiver-method (or
// associated-function) FuncDecl, append it to prog.Funcs, and record it
// in impl.MethodNames so the conformance pass sees the method as
// present. Idempotent across the monomorph re-check: the synthesised
// FuncDecl is a plain method (no trait default attached) and
// MethodNames already lists it, so a second pass adds nothing. See
// docs/TRAITS.md.
func (c *checker) synthesizeTraitDefaults(prog *ast.Program) {
	for _, impl := range prog.Impls {
		td, ok := c.info.Traits[impl.Trait]
		if !ok {
			continue // unknown trait — the conformance pass reports it
		}
		provided := map[string]bool{}
		for _, mn := range impl.MethodNames {
			provided[mn] = true
		}
		for _, m := range td.Methods {
			if m.Body == nil || provided[m.Name] {
				continue
			}
			method := &ast.FuncDecl{
				Name:         m.Name,
				ReturnType:   ast.SubstSelf(m.Result, impl.Type),
				Body:         ast.CloneBlock(m.Body),
				SourceModule: impl.SourceModule,
			}
			if m.Assoc {
				// Associated default (no `self`): hoists to
				// `__assoc_<Type>_<name>`. Use methodTypeName so the hoist
				// key matches what the conformance pass looks up.
				method.Params = substSelfParams(m.Params, impl.Type)
				if tn, ok := methodTypeName(impl.Type); ok {
					method.AssocType = tn
				} else {
					method.AssocType = impl.Type.String()
				}
			} else {
				// Ordinary default: m.Params[0] is `self: Self` → the
				// receiver the hoist prepends as Params[0].
				recv := m.Params[0]
				method.Receiver = &ast.Param{Name: recv.Name, Type: impl.Type, Own: recv.Own}
				method.Params = substSelfParams(m.Params[1:], impl.Type)
			}
			// A parametric impl (`impl[T: Bound] Trait for Box[T]`) makes
			// every method generic over the impl's type params — a default
			// body may reference T — so carry the params + bounds exactly as
			// the parser does for written methods.
			if len(impl.TypeParams) > 0 {
				method.TypeParams = append([]string(nil), impl.TypeParams...)
				method.Bounds = impl.Bounds
			}
			prog.Funcs = append(prog.Funcs, method)
			impl.MethodNames = append(impl.MethodNames, m.Name)
		}
	}
}

// substSelfParams copies params with `Self` substituted to self in each
// type, preserving names + `own` flags. Used when materialising a trait
// default method for a concrete impl.
func substSelfParams(params []ast.Param, self ast.Type) []ast.Param {
	if len(params) == 0 {
		return nil
	}
	out := make([]ast.Param, len(params))
	for i, p := range params {
		out[i] = p
		out[i].Type = ast.SubstSelf(p.Type, self)
	}
	return out
}

// deriveRecvStruct builds the receiver type + impl type-parameter list
// for a (possibly generic) struct's derived impl. For a plain struct
// it's `Struct` with no params; for `struct Box[T]` it's `Box[T]`
// (ParamType args) with type params `[T]`, driving a parametric impl.
func deriveRecvStruct(sd *ast.StructDecl) (ast.StructType, []string) {
	if len(sd.TypeParams) == 0 {
		return ast.StructType{Name: sd.Name}, nil
	}
	args := make([]ast.Type, len(sd.TypeParams))
	for i, tp := range sd.TypeParams {
		args[i] = ast.ParamType{Name: tp}
	}
	return ast.StructType{Name: sd.Name, Args: args}, sd.TypeParams
}

// deriveRecvEnum mirrors deriveRecvStruct for enums.
func deriveRecvEnum(ed *ast.EnumDecl) (ast.EnumType, []string) {
	if len(ed.TypeParams) == 0 {
		return ast.EnumType{Name: ed.Name}, nil
	}
	args := make([]ast.Type, len(ed.TypeParams))
	for i, tp := range ed.TypeParams {
		args[i] = ast.ParamType{Name: tp}
	}
	return ast.EnumType{Name: ed.Name, Args: args}, ed.TypeParams
}

// collectFreeTypeVars walks a method-receiver type and collects the
// names that are NOT known structs / enums — i.e. the implicit type
// variables a generic-receiver method binds (the `T` in `Box[T]`).
// Built-in scalar types are NumberType / FloatType / BoolType /
// StringType, not named StructType, so they're excluded naturally; a
// name that happens to match a real struct / enum is treated as a
// concrete instantiation, not a variable. Dedupes via `seen`.
func (c *checker) collectFreeTypeVars(t ast.Type, out *[]string, seen map[string]bool) {
	named := func(name string, args []ast.Type) {
		_, isStruct := c.info.Structs[name]
		_, isEnum := c.info.Enums[name]
		if !isStruct && !isEnum {
			if !seen[name] {
				seen[name] = true
				*out = append(*out, name)
			}
			return
		}
		for i := range args {
			c.collectFreeTypeVars(args[i], out, seen)
		}
	}
	switch x := t.(type) {
	case ast.StructType:
		named(x.Name, x.Args)
	case ast.EnumType:
		named(x.Name, x.Args)
	case ast.ArrayType:
		c.collectFreeTypeVars(x.Elem, out, seen)
	case ast.SliceType:
		c.collectFreeTypeVars(x.Elem, out, seen)
	case ast.TupleType:
		for i := range x.Elems {
			c.collectFreeTypeVars(x.Elems[i], out, seen)
		}
	}
}

// bindDeriveTypeParams turns a synthesised derive method into a generic
// method when the underlying type is generic: every type parameter is
// bound by the trait being derived (`@derive(Display)` on `Box[T]`
// yields `[T: Display]`), so the field-wise body type-checks through
// the bound and the method monomorphises per instantiation. A no-op
// for non-generic types. See docs/TRAITS.md.
func bindDeriveTypeParams(method *ast.FuncDecl, typeParams []string, trait string) {
	if len(typeParams) == 0 {
		return
	}
	method.TypeParams = typeParams
	method.Bounds = make(map[string][]string, len(typeParams))
	for _, tp := range typeParams {
		method.Bounds[tp] = []string{trait}
	}
}

// synthEnumEq builds a variant-wise `eq`: match self, and for each
// variant match other — same variant compares payloads field-wise,
// any other variant is unequal.
func synthEnumEq(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	arms := make([]*ast.MatchArm, 0, len(ed.Variants))
	for _, v := range ed.Variants {
		sBind := make([]string, len(v.Payloads))
		oBind := make([]string, len(v.Payloads))
		for i := range v.Payloads {
			sBind[i] = fmt.Sprintf("__s%d", i)
			oBind[i] = fmt.Sprintf("__o%d", i)
		}
		var eqExpr ast.Expr = &ast.BoolLit{Value: true}
		for i := range v.Payloads {
			cmp := methodCall(&ast.Ident{Name: sBind[i]}, "eq", &ast.Ident{Name: oBind[i]})
			if i == 0 {
				eqExpr = cmp
			} else {
				eqExpr = &ast.Binary{Op: "&&", Left: eqExpr, Right: cmp}
			}
		}
		inner := &ast.Match{Tag: &ast.Ident{Name: "other"}, Arms: []*ast.MatchArm{
			{VariantName: v.Name, Bindings: oBind, Body: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: eqExpr}}}},
			{IsWildcard: true, Body: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: &ast.BoolLit{Value: false}}}}},
		}}
		arms = append(arms, &ast.MatchArm{VariantName: v.Name, Bindings: sBind, Body: &ast.Block{Stmts: []ast.Stmt{inner}}})
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{Tag: &ast.Ident{Name: "self"}, Arms: arms},
		&ast.Return{Value: &ast.BoolLit{Value: false}},
	}}
	return &ast.FuncDecl{
		Name: "eq", Receiver: &ast.Param{Name: "self", Type: recv},
		Params: []ast.Param{{Name: "other", Type: recv}}, ReturnType: ast.BoolType{}, Body: body,
	}
}

// synthEnumDisplay builds `to_string` rendering `Variant(payload, …)`.
func synthEnumDisplay(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	arms := make([]*ast.MatchArm, 0, len(ed.Variants))
	for _, v := range ed.Variants {
		bind := make([]string, len(v.Payloads))
		for i := range v.Payloads {
			bind[i] = fmt.Sprintf("__p%d", i)
		}
		var expr ast.Expr = &ast.StringLit{Value: v.Name}
		if len(v.FieldNames) > 0 {
			// Named-field variant renders `Rect { w: …, h: … }`.
			add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
			add(&ast.StringLit{Value: " { "})
			for i, fn := range v.FieldNames {
				if i > 0 {
					add(&ast.StringLit{Value: ", "})
				}
				add(&ast.StringLit{Value: fn + ": "})
				add(methodCall(&ast.Ident{Name: bind[i]}, "to_string"))
			}
			add(&ast.StringLit{Value: " }"})
		} else if len(v.Payloads) > 0 {
			add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
			add(&ast.StringLit{Value: "("})
			for i := range v.Payloads {
				if i > 0 {
					add(&ast.StringLit{Value: ", "})
				}
				add(methodCall(&ast.Ident{Name: bind[i]}, "to_string"))
			}
			add(&ast.StringLit{Value: ")"})
		}
		arms = append(arms, &ast.MatchArm{VariantName: v.Name, Bindings: bind, Body: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}}})
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{Tag: &ast.Ident{Name: "self"}, Arms: arms},
		&ast.Return{Value: &ast.StringLit{Value: ""}},
	}}
	return &ast.FuncDecl{
		Name: "to_string", Receiver: &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.StringType{}, Body: body,
	}
}

// synthEnumDebug builds a variant-wise `to_debug` rendering `Variant` /
// `Variant(<debug payload>, …)`, the Debug sibling of synthEnumDisplay —
// each payload is rendered through its own `Debug` (so a string payload is
// quoted). Identical shape to the derived Display otherwise.
func synthEnumDebug(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	arms := make([]*ast.MatchArm, 0, len(ed.Variants))
	for _, v := range ed.Variants {
		bind := make([]string, len(v.Payloads))
		for i := range v.Payloads {
			bind[i] = fmt.Sprintf("__p%d", i)
		}
		var expr ast.Expr = &ast.StringLit{Value: v.Name}
		if len(v.FieldNames) > 0 {
			add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
			add(&ast.StringLit{Value: " { "})
			for i, fn := range v.FieldNames {
				if i > 0 {
					add(&ast.StringLit{Value: ", "})
				}
				add(&ast.StringLit{Value: fn + ": "})
				add(methodCall(&ast.Ident{Name: bind[i]}, "to_debug"))
			}
			add(&ast.StringLit{Value: " }"})
		} else if len(v.Payloads) > 0 {
			add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
			add(&ast.StringLit{Value: "("})
			for i := range v.Payloads {
				if i > 0 {
					add(&ast.StringLit{Value: ", "})
				}
				add(methodCall(&ast.Ident{Name: bind[i]}, "to_debug"))
			}
			add(&ast.StringLit{Value: ")"})
		}
		arms = append(arms, &ast.MatchArm{VariantName: v.Name, Bindings: bind, Body: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}}})
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{Tag: &ast.Ident{Name: "self"}, Arms: arms},
		&ast.Return{Value: &ast.StringLit{Value: ""}},
	}}
	return &ast.FuncDecl{
		Name: "to_debug", Receiver: &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.StringType{}, Body: body,
	}
}

// synthEnumOrd builds a variant-wise `cmp`: a variant declared earlier
// sorts before one declared later (by tag); within the same variant,
// payloads are compared lexicographically. `match self`, then for each
// self-variant `match other` with one arm per other-variant returning
// -1 / +1 by tag order, or the payload comparison when they match.
func synthEnumOrd(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	negOne := func() ast.Expr {
		return &ast.Binary{Op: "-", Left: &ast.NumberLit{Value: 0}, Right: &ast.NumberLit{Value: 1}}
	}
	retBlock := func(e ast.Expr) *ast.Block {
		return &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: e}}}
	}
	arms := make([]*ast.MatchArm, 0, len(ed.Variants))
	for i, vi := range ed.Variants {
		sBind := make([]string, len(vi.Payloads))
		for k := range vi.Payloads {
			sBind[k] = fmt.Sprintf("__s%d", k)
		}
		innerArms := make([]*ast.MatchArm, 0, len(ed.Variants))
		for j, vj := range ed.Variants {
			oBind := make([]string, len(vj.Payloads))
			for k := range vj.Payloads {
				oBind[k] = fmt.Sprintf("__o%d", k)
			}
			var body *ast.Block
			if j < i {
				body = retBlock(&ast.NumberLit{Value: 1}) // self's variant is later → greater
			} else if j > i {
				body = retBlock(negOne())
			} else {
				// Same variant: lexicographic payload compare. Both
				// sBind (outer arm) and oBind (this arm) are in scope.
				var stmts []ast.Stmt
				for k := range vi.Payloads {
					cn := fmt.Sprintf("__c%d", k)
					stmts = append(stmts, &ast.Var{
						Name: cn, Type: ast.NumberType{},
						Init: methodCall(&ast.Ident{Name: sBind[k]}, "cmp", &ast.Ident{Name: oBind[k]}),
					})
					stmts = append(stmts, &ast.If{
						Cond: &ast.Binary{Op: "!=", Left: &ast.Ident{Name: cn}, Right: &ast.NumberLit{Value: 0}},
						Then: retBlock(&ast.Ident{Name: cn}),
					})
				}
				stmts = append(stmts, &ast.Return{Value: &ast.NumberLit{Value: 0}})
				body = &ast.Block{Stmts: stmts}
			}
			innerArms = append(innerArms, &ast.MatchArm{VariantName: vj.Name, Bindings: oBind, Body: body})
		}
		inner := &ast.Match{Tag: &ast.Ident{Name: "other"}, Arms: innerArms}
		arms = append(arms, &ast.MatchArm{VariantName: vi.Name, Bindings: sBind, Body: &ast.Block{Stmts: []ast.Stmt{inner}}})
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{Tag: &ast.Ident{Name: "self"}, Arms: arms},
		&ast.Return{Value: &ast.NumberLit{Value: 0}},
	}}
	return &ast.FuncDecl{
		Name: "cmp", Receiver: &ast.Param{Name: "self", Type: recv},
		Params: []ast.Param{{Name: "other", Type: recv}}, ReturnType: ast.NumberType{}, Body: body,
	}
}

// selfField builds `self.<name>`; otherField builds `other.<name>`.
func selfField(name string) ast.Expr {
	return &ast.FieldAccess{Target: &ast.Ident{Name: "self"}, Field: name}
}
func otherField(name string) ast.Expr {
	return &ast.FieldAccess{Target: &ast.Ident{Name: "other"}, Field: name}
}

// methodCall builds `recv.<m>(args…)`.
func methodCall(recv ast.Expr, m string, args ...ast.Expr) ast.Expr {
	return &ast.Call{Callee: &ast.FieldAccess{Target: recv, Field: m}, Args: args}
}

// synthEq builds `function eq(self, other) { return f1.eq && f2.eq && … ; }`.
func synthEq(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	var expr ast.Expr = &ast.BoolLit{Value: true}
	for i, f := range sd.Fields {
		cmp := methodCall(selfField(f.Name), "eq", otherField(f.Name))
		if i == 0 {
			expr = cmp
		} else {
			expr = &ast.Binary{Op: "&&", Left: expr, Right: cmp}
		}
	}
	return &ast.FuncDecl{
		Name:       "eq",
		Receiver:   &ast.Param{Name: "self", Type: recv},
		Params:     []ast.Param{{Name: "other", Type: recv}},
		ReturnType: ast.BoolType{},
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}},
	}
}

// synthDisplay builds a `to_string` that renders `Name { f: …, … }`.
func synthDisplay(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	var expr ast.Expr = &ast.StringLit{Value: demangle(sd.Name) + " {"}
	add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
	for i, f := range sd.Fields {
		sep := " "
		if i > 0 {
			sep = ", "
		}
		add(&ast.StringLit{Value: sep + f.Name + ": "})
		add(methodCall(selfField(f.Name), "to_string"))
	}
	if len(sd.Fields) == 0 {
		add(&ast.StringLit{Value: "}"})
	} else {
		add(&ast.StringLit{Value: " }"})
	}
	return &ast.FuncDecl{
		Name:       "to_string",
		Receiver:   &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.StringType{},
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}},
	}
}

// synthDebug builds a `to_debug` that renders the structural `Name { f: …,
// … }` form, like synthDisplay, but composes through each field's `Debug`
// (`self.f.to_debug()`) rather than `Display`. The practical difference is
// that a string field renders QUOTED (`name: "hi"` vs Display's `name: hi`)
// via `impl Debug for string` in core/cmp. The generated method delegates
// to the primitive Debug impls / nested derived Debug methods, so it
// composes exactly as the derived Display does.
func synthDebug(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	var expr ast.Expr = &ast.StringLit{Value: demangle(sd.Name) + " {"}
	add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
	for i, f := range sd.Fields {
		sep := " "
		if i > 0 {
			sep = ", "
		}
		add(&ast.StringLit{Value: sep + f.Name + ": "})
		add(methodCall(selfField(f.Name), "to_debug"))
	}
	if len(sd.Fields) == 0 {
		add(&ast.StringLit{Value: "}"})
	} else {
		add(&ast.StringLit{Value: " }"})
	}
	return &ast.FuncDecl{
		Name:       "to_debug",
		Receiver:   &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.StringType{},
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}},
	}
}

// synthOrd builds a lexicographic `cmp`: compare each field in turn,
// returning the first non-zero result, else 0.
func synthOrd(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	var stmts []ast.Stmt
	for i, f := range sd.Fields {
		vn := fmt.Sprintf("__c%d", i)
		// var __ci: i32 = self.f.cmp(other.f);
		stmts = append(stmts, &ast.Var{
			Name: vn, Type: ast.NumberType{},
			Init: methodCall(selfField(f.Name), "cmp", otherField(f.Name)),
		})
		// if (__ci != 0) { return __ci; }
		stmts = append(stmts, &ast.If{
			Cond: &ast.Binary{Op: "!=", Left: &ast.Ident{Name: vn}, Right: &ast.NumberLit{Value: 0}},
			Then: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: &ast.Ident{Name: vn}}}},
		})
	}
	stmts = append(stmts, &ast.Return{Value: &ast.NumberLit{Value: 0}})
	return &ast.FuncDecl{
		Name:       "cmp",
		Receiver:   &ast.Param{Name: "self", Type: recv},
		Params:     []ast.Param{{Name: "other", Type: recv}},
		ReturnType: ast.NumberType{},
		Body:       &ast.Block{Stmts: stmts},
	}
}

// hashSeed is the field-wise hash combiner's starting value (a small
// odd prime); hashMul is the per-field multiplier — the textbook
// `h = h * 31 + field.hash()` fold. Distinct fields/variants of equal
// content stay distinguished because the multiply rotates earlier
// contributions into higher bits before each new field is mixed in.
const (
	hashSeed = 17
	hashMul  = 31
)

// hashFold appends `h = h * 31 + <e>.hash();` to stmts, where `e` is a
// field/payload accessor expression. The combiner is shared by the
// struct and enum synthesizers.
func hashFold(stmts []ast.Stmt, e ast.Expr) []ast.Stmt {
	return append(stmts, &ast.ExprStmt{Expr: &ast.Assign{
		Target: &ast.Ident{Name: "__h"},
		Value: &ast.Binary{
			Op:    "+",
			Left:  &ast.Binary{Op: "*", Left: &ast.Ident{Name: "__h"}, Right: &ast.NumberLit{Value: hashMul}},
			Right: methodCall(e, "hash"),
		},
	}})
}

// synthHash builds a field-wise `hash`: seed an accumulator, fold each
// field's `.hash()` through `h = h * 31 + f.hash()`, and return it. The
// seed-only result for a field-less struct is a fine constant hash. Pairs
// with the derived `Eq` so `a == b ⇒ a.hash() == b.hash()`.
func synthHash(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	stmts := []ast.Stmt{
		&ast.Var{Name: "__h", Type: ast.NumberType{}, Init: &ast.NumberLit{Value: hashSeed}},
	}
	for _, f := range sd.Fields {
		stmts = hashFold(stmts, selfField(f.Name))
	}
	stmts = append(stmts, &ast.Return{Value: &ast.Ident{Name: "__h"}})
	return &ast.FuncDecl{
		Name:       "hash",
		Receiver:   &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.NumberType{},
		Body:       &ast.Block{Stmts: stmts},
	}
}

// synthEnumHash builds a variant-wise `hash`: match self, seed the
// accumulator with the variant's tag (its declaration index) so distinct
// variants with identical payloads hash differently, then fold each
// payload's `.hash()` through the same combiner.
func synthEnumHash(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	arms := make([]*ast.MatchArm, 0, len(ed.Variants))
	for i, v := range ed.Variants {
		bind := make([]string, len(v.Payloads))
		for k := range v.Payloads {
			bind[k] = fmt.Sprintf("__p%d", k)
		}
		// Seed with the tag so payload-less variants (and same-shaped
		// payloads across variants) stay distinct.
		stmts := []ast.Stmt{
			&ast.Var{Name: "__h", Type: ast.NumberType{}, Init: &ast.NumberLit{Value: int64(hashSeed + i)}},
		}
		for k := range v.Payloads {
			stmts = hashFold(stmts, &ast.Ident{Name: bind[k]})
		}
		stmts = append(stmts, &ast.Return{Value: &ast.Ident{Name: "__h"}})
		arms = append(arms, &ast.MatchArm{VariantName: v.Name, Bindings: bind, Body: &ast.Block{Stmts: stmts}})
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{Tag: &ast.Ident{Name: "self"}, Arms: arms},
		&ast.Return{Value: &ast.NumberLit{Value: 0}},
	}}
	return &ast.FuncDecl{
		Name: "hash", Receiver: &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.NumberType{}, Body: body,
	}
}

// defaultDeriveExpr returns the default value for a field/payload of type
// t, used by `@derive(Default)`. Scalars get their zero literal; a nominal
// type (struct / enum / bound type-param) delegates to *its* `default()`
// associated function so derivation composes. Reports false for a type
// with no obvious default (array, map, tuple, slice, function) — the
// caller turns that into a "implement Default by hand" diagnostic.
func defaultDeriveExpr(t ast.Type) (ast.Expr, bool) {
	switch ft := t.(type) {
	case ast.NumberType:
		return &ast.NumberLit{}, true
	case ast.FloatType:
		return &ast.FloatLit{}, true
	case ast.BoolType:
		return &ast.BoolLit{Value: false}, true
	case ast.StringType:
		return &ast.StringLit{Value: ""}, true
	case ast.StructType:
		return defaultAssocCall(ft.Name), true
	case ast.EnumType:
		return defaultAssocCall(ft.Name), true
	case ast.ParamType:
		return defaultAssocCall(ft.Name), true
	}
	return nil, false
}

// defaultAssocCall builds `Type.default()` — the associated-function call
// the derived Default delegates to for a nominal field type.
func defaultAssocCall(typeName string) ast.Expr {
	return &ast.Call{Callee: &ast.FieldAccess{Target: &ast.Ident{Name: typeName}, Field: "default"}}
}

// synthDefault builds a struct's derived `default()` associated function:
// `function default(): Self { return Name { f0: <zero>, f1: <zero>, … }; }`.
// Returns (nil, fieldName, fieldType) if a field has no derivable default.
func synthDefault(sd *ast.StructDecl, recv ast.StructType) (*ast.FuncDecl, string, ast.Type) {
	fields := make([]ast.FieldInit, 0, len(sd.Fields))
	for _, f := range sd.Fields {
		dv, ok := defaultDeriveExpr(f.Type)
		if !ok {
			return nil, f.Name, f.Type
		}
		fields = append(fields, ast.FieldInit{Name: f.Name, Value: dv})
	}
	lit := &ast.StructLit{TypeName: recv.Name, Fields: fields, TypeArgs: recv.Args}
	return &ast.FuncDecl{
		Name:       "default",
		AssocType:  recv.Name,
		ReturnType: recv,
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: lit}}},
	}, "", nil
}

// synthEnumDefault builds an enum's derived `default()` associated
// function: the FIRST variant, with each payload defaulted. Returns
// (nil, payloadType) if a first-variant payload has no derivable default.
func synthEnumDefault(ed *ast.EnumDecl, recv ast.EnumType) (*ast.FuncDecl, ast.Type) {
	if len(ed.Variants) == 0 {
		return nil, nil
	}
	v0 := ed.Variants[0]
	var value ast.Expr
	if len(v0.Payloads) == 0 {
		value = &ast.Ident{Name: v0.Name, EnumName: recv.Name}
	} else {
		args := make([]ast.Expr, len(v0.Payloads))
		for i, pt := range v0.Payloads {
			dv, ok := defaultDeriveExpr(pt)
			if !ok {
				return nil, pt
			}
			args[i] = dv
		}
		value = &ast.Call{Callee: &ast.Ident{Name: v0.Name, EnumName: recv.Name}, Args: args}
	}
	return &ast.FuncDecl{
		Name:       "default",
		AssocType:  recv.Name,
		ReturnType: recv,
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: value}}},
	}, nil
}

// synthJson builds a field-wise `to_json` rendering a struct as a JSON
// object: `{"f1":<f1.to_json()>,"f2":<f2.to_json()>}`. Each field
// composes through its own `Json` impl, so a type serialises as soon as
// its fields do. Field names are identifiers, so they need no escaping
// (string *values* are escaped by `impl Json for string`). Returns the
// canonical JSON text directly — `json_encode` is unnecessary.
func synthJson(sd *ast.StructDecl, recv ast.StructType) *ast.FuncDecl {
	var expr ast.Expr = &ast.StringLit{Value: "{"}
	add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
	for i, f := range sd.Fields {
		sep := ""
		if i > 0 {
			sep = ","
		}
		add(&ast.StringLit{Value: sep + "\"" + f.Name + "\":"})
		add(methodCall(selfField(f.Name), "to_json"))
	}
	add(&ast.StringLit{Value: "}"})
	return &ast.FuncDecl{
		Name:       "to_json",
		Receiver:   &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.StringType{},
		Body:       &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}},
	}
}

// synthFromJson builds the deserialise companion to synthJson: a receiver-less
// associated `from_json(s: string): Result[Self, string]` that parses the JSON
// text and extracts each field by name, returning `Err(...)` on invalid JSON or
// a missing/wrong-typed field. v1 supports flat structs whose fields are i32 /
// string / boolean (the types with a `json.json_get_*` accessor); a field of any
// other type makes this return ok=false so the caller synthesises `to_json`
// only (serialise still works; from_json over nested / array / Option / wider
// numeric fields is a documented follow-up — see #2695). The body nests one
// `match` per field over the field's accessor `Option`, so a missing field
// short-circuits to `Err("missing field: <name>")` without a `?` operator. The
// `json.*` calls resolve through the `@derive(json.Json)` site's own
// `import "std/json"` (basename alias `json`).
func synthFromJson(sd *ast.StructDecl, recv ast.StructType) (*ast.FuncDecl, bool) {
	type fld struct{ name, accessor, bind string }
	flds := make([]fld, 0, len(sd.Fields))
	for _, f := range sd.Fields {
		acc := ""
		switch ft := f.Type.(type) {
		case ast.NumberType:
			// 64-bit-wide integer fields (i64 / u64) extract through the
			// i64 accessor; everything narrower through json_get_i32.
			if ft.Width == 64 {
				acc = "json_get_i64"
			} else {
				acc = "json_get_i32"
			}
		case ast.BoolType:
			acc = "json_get_bool"
		case ast.StringType:
			acc = "json_get_string"
		default:
			return nil, false
		}
		flds = append(flds, fld{name: f.Name, accessor: acc, bind: "__fj_" + f.Name})
	}
	// The synthesis runs AFTER modload has rewritten module-qualified calls
	// (`json.json_parse` → the flat `json__json_parse`), so emit the already-
	// mangled basename-prefixed name directly — a post-modload `json.func`
	// FieldAccess would leave `json` an undefined identifier.
	jcall := func(fn string, args ...ast.Expr) ast.Expr {
		return &ast.Call{Callee: &ast.Ident{Name: "json__" + fn}, Args: args}
	}
	variant := func(name string, arg ast.Expr) ast.Expr {
		return &ast.Call{Callee: &ast.Ident{Name: name}, Args: []ast.Expr{arg}}
	}
	// Innermost: return Ok(Recv { f: __fj_f, … }).
	fis := make([]ast.FieldInit, len(flds))
	for i, fl := range flds {
		fis[i] = ast.FieldInit{Name: fl.name, Value: &ast.Ident{Name: fl.bind}}
	}
	lit := &ast.StructLit{TypeName: recv.Name, Fields: fis, TypeArgs: recv.Args}
	body := &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: variant("Ok", lit)}}}
	// Wrap each field's accessor match, inner-to-outer.
	for i := len(flds) - 1; i >= 0; i-- {
		fl := flds[i]
		missing := &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: variant("Err",
			&ast.StringLit{Value: "missing field: " + fl.name})}}}
		m := &ast.Match{
			Tag: jcall(fl.accessor, &ast.Ident{Name: "__fj_jv"}, &ast.StringLit{Value: fl.name}),
			Arms: []*ast.MatchArm{
				{VariantName: "Some", Bindings: []string{fl.bind}, Body: body},
				{VariantName: "None", Body: missing},
			},
		}
		body = &ast.Block{Stmts: []ast.Stmt{m}}
	}
	// Outermost: parse the string, then run the field chain.
	parseFail := &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: variant("Err",
		&ast.StringLit{Value: "invalid JSON"})}}}
	outer := &ast.Match{
		Tag: jcall("json_parse", &ast.Ident{Name: "s"}),
		Arms: []*ast.MatchArm{
			{VariantName: "Some", Bindings: []string{"__fj_jv"}, Body: body},
			{VariantName: "None", Body: parseFail},
		},
	}
	resultType := ast.EnumType{Name: "Result", Args: []ast.Type{recv, ast.StringType{}}}
	return &ast.FuncDecl{
		Name:       "from_json",
		AssocType:  recv.Name,
		Params:     []ast.Param{{Name: "s", Type: ast.StringType{}}},
		ReturnType: resultType,
		Body:       &ast.Block{Stmts: []ast.Stmt{outer}},
	}, true
}

// tagged convention: a unit variant renders as the JSON string of its
// name (`"Empty"`); a payload variant renders as a single-key object
// (`{"Circle":<p0.to_json()>}`), with multiple payloads collected into a
// JSON array (`{"Rect":[<p0>,<p1>]}`). Mirrors the Go synthEnumDisplay
// shape; composes through each payload's `Json` impl.
func synthEnumJson(ed *ast.EnumDecl, recv ast.EnumType) *ast.FuncDecl {
	arms := make([]*ast.MatchArm, 0, len(ed.Variants))
	for _, v := range ed.Variants {
		bind := make([]string, len(v.Payloads))
		for k := range v.Payloads {
			bind[k] = fmt.Sprintf("__p%d", k)
		}
		var expr ast.Expr
		add := func(e ast.Expr) { expr = &ast.Binary{Op: "+", Left: expr, Right: e} }
		if len(v.FieldNames) > 0 {
			// Named-field variant encodes as a nested object:
			// `{"Rect":{"w":<p0>,"h":<p1>}}`.
			expr = &ast.StringLit{Value: "{\"" + v.Name + "\":{"}
			for k, fn := range v.FieldNames {
				if k > 0 {
					add(&ast.StringLit{Value: ","})
				}
				add(&ast.StringLit{Value: "\"" + fn + "\":"})
				add(methodCall(&ast.Ident{Name: bind[k]}, "to_json"))
			}
			add(&ast.StringLit{Value: "}}"})
		} else {
			switch len(v.Payloads) {
			case 0:
				expr = &ast.StringLit{Value: "\"" + v.Name + "\""}
			case 1:
				expr = &ast.StringLit{Value: "{\"" + v.Name + "\":"}
				add(methodCall(&ast.Ident{Name: bind[0]}, "to_json"))
				add(&ast.StringLit{Value: "}"})
			default:
				expr = &ast.StringLit{Value: "{\"" + v.Name + "\":["}
				for k := range v.Payloads {
					if k > 0 {
						add(&ast.StringLit{Value: ","})
					}
					add(methodCall(&ast.Ident{Name: bind[k]}, "to_json"))
				}
				add(&ast.StringLit{Value: "]}"})
			}
		}
		arms = append(arms, &ast.MatchArm{VariantName: v.Name, Bindings: bind, Body: &ast.Block{Stmts: []ast.Stmt{&ast.Return{Value: expr}}}})
	}
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.Match{Tag: &ast.Ident{Name: "self"}, Arms: arms},
		&ast.Return{Value: &ast.StringLit{Value: ""}},
	}}
	return &ast.FuncDecl{
		Name: "to_json", Receiver: &ast.Param{Name: "self", Type: recv},
		ReturnType: ast.StringType{}, Body: body,
	}
}

func demangle(s string) string {
	return strings.Replace(s, "__", ".", 1)
}

// checkOpaqueAccess rejects reaching into an `opaque` struct's fields
// (read or construction) from outside the module that declared it. The
// type name + methods stay usable cross-module; only the representation
// is private. `what` describes the offending access for the diagnostic.
// See docs/TRAITS.md.
func (c *checker) checkOpaqueAccess(sd *ast.StructDecl, pos ast.Position, what string) {
	if sd == nil || !sd.Opaque || sd.SourceModule == "" {
		return
	}
	cur := ""
	if c.current != nil {
		cur = c.current.SourceModule
	}
	if cur != sd.SourceModule {
		c.errfCode(pos, "E021", "cannot %s opaque type %s outside the module that defines it", what, demangle(sd.Name))
	}
}

// containsString reports whether `xs` contains `s`.
func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// resolveTraitMethodForParam looks up method `field` among the traits
// bound on type parameter `paramName` in the function currently being
// checked. Returns the matching trait-method signature and the trait
// name. Used by the deferred-dispatch path for trait-bounded generics.
// See docs/TRAITS.md.
func (c *checker) resolveTraitMethodForParam(paramName, field string) (ast.TraitMethod, string, bool) {
	if c.current == nil {
		return ast.TraitMethod{}, "", false
	}
	// Expand the bound traits with their supertraits: a `T: Ord` bound
	// also exposes the methods of Ord's supertraits (e.g. Eq). See
	// docs/TRAITS.md.
	// A generic-trait bound (`T: From[i32]`) carries type args parallel to
	// the direct bounds; map each direct trait to its args so the method
	// signature can be specialised (`from(v: T)` → `from(v: i32)`).
	direct := c.current.Bounds[paramName]
	argsFor := map[string][]ast.Type{}
	if ba := c.current.BoundArgs[paramName]; len(ba) == len(direct) {
		for i, tn := range direct {
			if len(ba[i]) > 0 {
				argsFor[tn] = ba[i]
			}
		}
	}
	for _, traitName := range c.expandTraits(direct) {
		td, ok := c.info.Traits[traitName]
		if !ok {
			continue
		}
		for _, m := range td.Methods {
			if m.Name == field {
				if args := argsFor[traitName]; len(args) > 0 && len(td.TypeParams) == len(args) {
					sub := make(map[string]ast.Type, len(args))
					for i, tp := range td.TypeParams {
						sub[tp] = args[i]
					}
					m = substTraitMethodTypeParams(m, sub)
				}
				return m, traitName, true
			}
		}
	}
	return ast.TraitMethod{}, "", false
}

// substTraitMethodTypeParams returns a copy of trait method `m` with the
// trait's type parameters substituted (via substByName) in its parameter
// and result types — used to specialise a generic-trait bound's method to
// the bound's type arguments. See docs/TRAITS.md.
func substTraitMethodTypeParams(m ast.TraitMethod, sub map[string]ast.Type) ast.TraitMethod {
	out := m
	out.Params = make([]ast.Param, len(m.Params))
	for i, p := range m.Params {
		p.Type = substByName(p.Type, sub)
		out.Params[i] = p
	}
	out.Result = substByName(m.Result, sub)
	return out
}

// collectTraitSupers appends `name` and all its transitive supertraits to
// *acc (deduped via seen, which also breaks cycles), following
// TraitDecl.Supertraits.
func (c *checker) collectTraitSupers(name string, seen map[string]bool, acc *[]string) {
	if seen[name] {
		return
	}
	seen[name] = true
	*acc = append(*acc, name)
	if td, ok := c.info.Traits[name]; ok {
		for _, sup := range td.Supertraits {
			c.collectTraitSupers(sup, seen, acc)
		}
	}
}

// traitInItsOwnSupers reports whether `name` is reachable from its own
// supertraits — i.e. the supertrait graph has a cycle through `name`
// (`trait A: A`, or `A: B` with `B: A`).
func (c *checker) traitInItsOwnSupers(name string) bool {
	seen := map[string]bool{}
	var stack []string
	if td, ok := c.info.Traits[name]; ok {
		stack = append(stack, td.Supertraits...)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == name {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		if td, ok := c.info.Traits[cur]; ok {
			stack = append(stack, td.Supertraits...)
		}
	}
	return false
}

// expandTraits returns `traits` plus all their transitive supertraits,
// deduplicated. Each trait is followed by its supertraits, so the order
// is deterministic. See docs/TRAITS.md.
func (c *checker) expandTraits(traits []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range traits {
		c.collectTraitSupers(t, seen, &out)
	}
	return out
}

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

// methodImplementsTrait reports whether calling `methodName` on receiver
// type `typeName` resolves to a *trait-impl* method — i.e. some trait
// declares a method of that name and `typeName` implements that trait.
// Trait-impl methods are part of the public trait contract (coherent +
// orphan-checked), so unlike a module's *inherent* methods (which
// methodVisibleHere gates by the import graph) they are callable wherever
// the receiver type flows. This is what lets a bounded generic method
// defined in one module — e.g. std/json's `(xs: T[]) to_json[T: Json]()`
// — dispatch `xs[i].to_json()` to a *user* type's derived impl after the
// monomorphiser substitutes T and re-checks the clone from the defining
// module's context (where the user module isn't imported).
func (c *checker) methodImplementsTrait(typeName, methodName string) bool {
	for traitName, td := range c.info.Traits {
		if !c.info.Impls[traitName][typeName] {
			continue
		}
		for _, m := range td.Methods {
			if m.Name == methodName {
				return true
			}
		}
	}
	return false
}

// displayDispatchTypeName maps a concrete value type to the receiver name
// `to_string` (and other trait methods) dispatch under — mirroring the
// scalar/struct/enum mapping in the method-call path. Returns "" for types
// that can't carry a `to_string` (void, tuples, raw arrays, …).
func displayDispatchTypeName(t ast.Type) string {
	switch x := t.(type) {
	case ast.StructType:
		return x.Name
	case ast.EnumType:
		return x.Name
	case ast.StringType:
		return "string"
	case ast.StrType:
		return "string"
	case ast.BoolType:
		return "boolean"
	case ast.NumberType:
		switch {
		case x.NormalWidth() == 64 && x.IsSigned():
			return "i64"
		case x.NormalWidth() == 64 && !x.IsSigned():
			return "u64"
		case !x.IsSigned():
			return "u32"
		default:
			return "i32"
		}
	case ast.FloatType:
		if x.NormalWidth() == 64 {
			return "f64"
		}
		return "f32"
	}
	return ""
}

// typeImplementsDisplay reports whether a value of type t can be rendered via
// the Display spine — i.e. `t.to_string(): string` resolves in the current
// context. Drives the auto-`.to_string()` rewrite for `print` / `write` /
// `eprint` (issue #2696): a bounded type parameter (`T: Display`), a
// `dyn Display`-style trait object, or a concrete struct/enum/scalar with a
// visible (or trait-impl) `to_string` method all qualify.
func (c *checker) typeImplementsDisplay(t ast.Type) bool {
	switch x := t.(type) {
	case ast.ParamType:
		_, _, found := c.resolveTraitMethodForParam(x.Name, "to_string")
		return found
	case ast.DynTraitType:
		// Display via any trait in the set — `to_string` may be declared
		// by any of the traits the object spans.
		for _, tr := range x.Traits {
			td, ok := c.info.Traits[tr]
			if !ok {
				continue
			}
			for _, m := range td.Methods {
				if m.Name == "to_string" {
					return true
				}
			}
		}
		return false
	}
	tn := displayDispatchTypeName(t)
	if tn == "" {
		return false
	}
	mangled, ok := c.info.Methods[tn+".to_string"]
	if !ok {
		return false
	}
	return c.methodVisibleHere(mangled) || c.methodImplementsTrait(tn, "to_string")
}

type checker struct {
	info      *Info
	errors    []error
	current   *ast.FuncDecl
	loopDepth int
	// tryConvN uniquifies the temp-var name in the error-converting `?`
	// desugar (TryOp.Lowered). See #3234.
	tryConvN int
	// inferReturns, when non-nil, accumulates the type of every
	// `return EXPR` statement checked in the body of the CURRENT
	// function — set up by checkFunction only for an unannotated
	// function (current.ReturnUnannotated) so its return type can be
	// inferred from the unified return-expression types. A nil entry
	// records a bare `return;`. It is pointer-saved/restored around
	// each checkFunction so nested function/lambda checks (which set
	// their own current) don't cross-contaminate. See inferReturnType.
	inferReturns *[]ast.Type
	// loopLabels is the stack of in-scope loop labels (innermost last),
	// pushed while checking a labeled `while`/`for`/`loop` body so a
	// `break label` / `continue label` can be validated against it.
	loopLabels []string

	// ownFuncs maps a function name to its per-parameter `own` flags (only
	// recorded for functions that have at least one owned parameter). Built
	// before body checking so the call-site ownership guard (checkOwnedParams /
	// E051) can require that arguments passed to an `own` parameter are owned
	// values the caller can transfer.
	ownFuncs map[string][]bool

	// elemHint carries the expected element type for an array literal
	// being checked at a coercion site (var init / return / argument).
	// It is set ONLY immediately around a checkExpr call whose argument
	// is directly an `*ast.ArrayLit`, and the ArrayLit case consumes it
	// at once — so it never leaks into unrelated literals. Today it is
	// used to let `[Circle{}, Rect{}]` coerce its (differently-typed)
	// elements to a `dyn Trait[]` destination. See docs/DYN-TRAITS.md.
	elemHint ast.Type

	// expectedType is the destination/result type a generic call's
	// return-position type parameters can be inferred from when the
	// arguments don't pin them. Set around `checkExpr` of a `var x:
	// T = call(...)` initializer and a `return call(...)` value;
	// the generic-call completion seeds its substitution from this
	// (return type ↔ expectedType) before reporting "could not
	// infer". A call clears it before checking its own arguments so
	// it never leaks into nested calls. Enables return-position
	// inference like `var s: Set[i32] = set_new();` (#2668).
	expectedType ast.Type

	// requireMapImport is set when the program was loaded through
	// modload (LoadedStdlibPaths != nil) but `core/map` isn't in the
	// import closure. Map operations (`map_new`, a `Map { … }`
	// literal) lower to core/map's runtime helpers (map_new_impl /
	// __map_*_impl), so without the import the build links against
	// undefined symbols — a failure the checker should catch up front
	// rather than leave for codegen. mapErrReported keeps it to one
	// diagnostic per program. Single-file callers (no modload, nil
	// LoadedStdlibPaths) are exempt — they have no import mechanism.
	requireMapImport bool
	mapErrReported   bool

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
			c.resolveType(&fn.Receiver.Type, params, orPos(fn.Receiver.NamePos, fn.P))
		}
		for i := range fn.Params {
			c.resolveType(&fn.Params[i].Type, params, orPos(fn.Params[i].NamePos, fn.P))
		}
		c.resolveType(&fn.ReturnType, params, fn.P)
		if fn.Body != nil {
			c.resolveTypesInBlock(fn.Body, params)
		}
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
			c.resolveType(&sd.Fields[i].Type, params, orPos(sd.Fields[i].NamePos, sd.P))
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
				c.resolveType(&ed.Variants[i].Payloads[j], params, orPos(ed.Variants[i].P, ed.P))
			}
		}
	}
	// Parametric impls: resolve the `for` type's own type-parameter
	// references (`Box[T]`) to ParamType so the conformance check's
	// SubstSelf-built expected signatures line up with the generic
	// hoisted methods (whose receiver `Box[T]` was already resolved
	// via the method's TypeParams). See docs/TRAITS.md.
	for _, impl := range prog.Impls {
		if len(impl.TypeParams) == 0 {
			continue
		}
		params := make(map[string]bool, len(impl.TypeParams))
		for _, n := range impl.TypeParams {
			params[n] = true
		}
		c.resolveType(&impl.Type, params, impl.P)
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
		case *ast.Loop:
			c.resolveTypesInBlock(asBlock(x.Body), params)
		case *ast.For:
			c.resolveTypesInBlock(asBlock(x.Body), params)
		case *ast.Var:
			c.resolveType(&x.Type, params, x.P)
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
				c.resolveType(&x.Params[i].Type, params, orPos(x.Params[i].NamePos, x.P))
			}
			c.resolveType(&x.ReturnType, params, x.P)
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
	case *ast.Loop:
		// `loop { … }` is unconditional by construction, so treat it
		// as diverging — same conservative "ignore breaks" stance as
		// stmtExits below: a `loop` containing a `break` could in
		// principle fall through to here, but requiring every escape
		// to be spelled as an explicit trailing return/break/continue
		// is what this analysis already asks of every other construct.
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

// funcBodyExits reports whether every path through a function body either
// returns or never falls off the end (an infinite loop). It is the
// missing-return analysis behind E052 and is deliberately CONSERVATIVE:
// it only returns false when the body can demonstrably fall through, so
// it never rejects a valid function. (It therefore accepts some functions
// that could in principle fall through — e.g. an infinite loop with a
// break — rather than risk a false positive.) See
// docs/ADVERSARIAL-REVIEW-2026-06.md (F4).
func funcBodyExits(b *ast.Block) bool {
	if b == nil || len(b.Stmts) == 0 {
		return false
	}
	return stmtExits(b.Stmts[len(b.Stmts)-1])
}

func stmtExits(s ast.Stmt) bool {
	switch x := s.(type) {
	case *ast.Return:
		return true
	case *ast.Block:
		return funcBodyExits(x)
	case *ast.If:
		// A one-armed if can fall through; both arms must exit.
		return x.Else != nil && stmtExits(x.Then) && stmtExits(x.Else)
	case *ast.IfLet:
		return x.Else != nil && stmtExits(x.Then) && stmtExits(x.Else)
	case *ast.Match:
		// Exhaustiveness is checked separately; here every arm must exit
		// for the match to guarantee the function exits.
		if len(x.Arms) == 0 {
			return false
		}
		for _, arm := range x.Arms {
			if !funcBodyExits(arm.Body) {
				return false
			}
		}
		return true
	case *ast.While:
		// `while (true) { … }` never falls through. Conservatively treat
		// any literal-true loop as divergent (ignoring breaks): a loop
		// that can actually break and needs a following value will still
		// have a trailing return, which the surrounding block catches.
		if lit, ok := x.Cond.(*ast.BoolLit); ok && lit.Value {
			return true
		}
		return false
	case *ast.Loop:
		// `loop { … }` is unconditional by construction — same
		// conservative treatment as literal-true While above, without
		// needing to pattern-match a BoolLit condition.
		return true
	}
	return false
}

// isVoidReturn reports whether a function's declared return type is void
// (or unspecified), in which case it may fall off the end legitimately.
func isVoidReturn(t ast.Type) bool {
	if t == nil {
		return true
	}
	_, ok := t.(ast.VoidType)
	return ok
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
// orPos returns p unless it is unset (synthetic nodes leave positions
// zero), in which case it falls back to `fallback`.
func orPos(p, fallback ast.Position) ast.Position {
	if p.Line == 0 {
		return fallback
	}
	return p
}

// isCellElemType reports whether T is permitted as Cell[T] (E057). The
// element must be cycle-free: a cell over a value that can transitively
// hold another cell could reconstruct a reference cycle, which the
// immutable-data model forbids so Perceus RC needs no cycle collector.
// Scalars (i32/i64/f64/bool) hold no pointer at all; `string` is a heap
// buffer of bytes that references no other Fern value, so a Cell[string]
// can never close a cycle either, and its owning slot participates in
// the string rc arc (cell_new / get / set / drop retain+release the
// buffer — docs/CELL-TYPE-PLAN.md, docs/RC-STRINGS-PLAN.md). Composite /
// reference types (struct / enum / array / tuple / closure / another
// Cell) stay rejected: those CAN form cycles. An unresolved generic
// param is allowed through here (there's no v1 generic-Cell use;
// monomorph-time checking is a follow-up) so generic signatures still
// resolve.
func isCellElemType(t ast.Type) bool {
	switch t.(type) {
	case ast.NumberType, ast.FloatType, ast.BoolType, ast.StringType, ast.ParamType:
		return true
	}
	return false
}

// resolveType canonicalises a parsed type annotation in place. `pos` is
// the closest source anchor for the annotation (the declaring name's
// position — field / param / var / decl); annotation-position
// diagnostics (E057) report there. Type nodes carry no positions of
// their own, so a zero `pos` (synthetic decls) falls back to the
// offending decl's own position at the report site.
func (c *checker) resolveType(slot *ast.Type, params map[string]bool, pos ast.Position) {
	if slot == nil || *slot == nil {
		return
	}
	switch t := (*slot).(type) {
	case ast.ProjType:
		// Resolve the base so `T::Item`'s base StructType{T} becomes
		// ParamType{T} and `Self::Item` stays SelfType. The projection
		// itself is resolved to its binding later (resolveProjections),
		// once impl conformance has recorded the bindings.
		base := t.Base
		c.resolveType(&base, params, pos)
		*slot = ast.ProjType{Base: base, Name: t.Name}
		return
	case ast.StructType:
		if params[t.Name] {
			*slot = ast.ParamType{Name: t.Name}
			return
		}
		if _, isEnum := c.info.Enums[t.Name]; isEnum {
			*slot = ast.EnumType{Name: t.Name}
			return
		}
		// A bare resource name (`Pollable`) in type position is an owned
		// handle — reclassify so body-checking sees a HandleType (P5).
		// `own Pollable` / `borrow Pollable` already arrive as HandleType
		// from the parser. Resource names can't take type arguments, so a
		// `R[...]` form falls through to the struct/enum arity path below.
		if _, isResource := c.info.Resources[t.Name]; isResource && len(t.Args) == 0 {
			*slot = ast.HandleType{Resource: t.Name}
			return
		}
		// Already a StructType — recurse into Args (populated
		// when the type came back through resolveType for a
		// generic struct's instantiation).
		if len(t.Args) > 0 {
			args := make([]ast.Type, len(t.Args))
			copy(args, t.Args)
			for i := range args {
				c.resolveType(&args[i], params, pos)
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
			c.resolveType(&args[i], params, pos)
		}
		if sd, ok := c.info.Structs[t.Name]; ok {
			if len(sd.TypeParams) != len(args) {
				c.errfCode(sd.P, "E019", "struct %s has %d type parameter(s), %d supplied",
					t.Name, len(sd.TypeParams), len(args))
			}
			// E057: a Cell[T] is only sound for a cycle-free T — a cell
			// over a reference type could reconstruct a reference cycle,
			// which is exactly what the immutable-data model forbids so
			// Perceus RC needs no cycle collector (docs/CELL-TYPE-PLAN.md
			// §1-2). v1 allows scalars only; string and richer cycle-free
			// types wait on the owning-slot RC integration.
			if t.Name == "Cell" && len(args) == 1 && !isCellElemType(args[0]) {
				// Anchor at the annotation's use site. Cell's decl is
				// synthesized (sd.P is 0:0, which diag.Format renders
				// without the error[E057] prefix), so the fallback only
				// fires for annotations with no source anchor of their
				// own (synthetic decls).
				at := pos
				if at.Line == 0 {
					at = sd.P
				}
				c.errfCode(at, "E057",
					"Cell[%s] is not allowed: a cell's element type must be a scalar (i32/i64/f64/bool) or string; a composite/reference type could form a cycle, which immutable data structures forbid",
					args[0])
			}
			*slot = ast.StructType{Name: t.Name, Args: args}
			return
		}
		if ed, ok := c.info.Enums[t.Name]; ok {
			if len(ed.TypeParams) != len(args) {
				c.errfCode(ed.P, "E019", "enum %s has %d type parameter(s), %d supplied",
					t.Name, len(ed.TypeParams), len(args))
			}
		}
		*slot = ast.EnumType{Name: t.Name, Args: args}
	case ast.ArrayType:
		elem := t.Elem
		c.resolveType(&elem, params, pos)
		*slot = ast.ArrayType{Elem: elem}
	case ast.SliceType:
		elem := t.Elem
		c.resolveType(&elem, params, pos)
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
			c.resolveType(&elems[i], params, pos)
		}
		*slot = ast.TupleType{Elems: elems}
	case *ast.FuncType:
		for i := range t.Params {
			c.resolveType(&t.Params[i], params, pos)
		}
		c.resolveType(&t.Result, params, pos)
	}
}

// validateKnownTypes reports any nominal type reference (E064) that names
// no declared type — `var x: Wibble`, `function f(a: Wibble): Wibble`,
// `struct S { f: Wibble }`. It runs after resolveTypeNames, so type
// parameters are already ParamType, enums are EnumType, and resources are
// HandleType; a leftover StructType/EnumType is therefore either a declared
// struct/enum or genuinely undefined. The per-decl type-parameter scope
// mirrors resolveTypeNames exactly so an in-scope `T` is never flagged. A
// function body uses a single type-param scope throughout (Fern has no
// nested generic scopes), so every `var` annotation inside it — at any
// block depth — is validated against the function's own parameters.
func (c *checker) validateKnownTypes(prog *ast.Program) {
	for _, fn := range prog.Funcs {
		params := typeParamSet(fn.TypeParams)
		if fn.Receiver != nil {
			c.checkTypeKnown(fn.Receiver.Type, params, fn.P)
		}
		for i := range fn.Params {
			c.checkTypeKnown(fn.Params[i].Type, params, paramPos(fn.Params[i], fn.P))
		}
		c.checkTypeKnown(fn.ReturnType, params, fn.P)
		if fn.Body != nil {
			ast.Walk(fn.Body, func(n ast.Node) bool {
				if v, ok := n.(*ast.Var); ok && v.Type != nil {
					c.checkTypeKnown(v.Type, params, v.P)
				}
				return true
			})
		}
	}
	for _, sd := range prog.Structs {
		params := typeParamSet(sd.TypeParams)
		for i := range sd.Fields {
			c.checkTypeKnown(sd.Fields[i].Type, params, paramPos(sd.Fields[i], sd.P))
		}
	}
	for _, ed := range prog.Enums {
		params := typeParamSet(ed.TypeParams)
		for i := range ed.Variants {
			for j := range ed.Variants[i].Payloads {
				c.checkTypeKnown(ed.Variants[i].Payloads[j], params, ed.Variants[i].P)
			}
		}
	}
	for _, impl := range prog.Impls {
		params := typeParamSet(impl.TypeParams)
		c.checkTypeKnown(impl.Type, params, impl.P)
	}
}

// checkTypeKnown walks a resolved type tree and reports E064 for each
// nominal (struct / enum) name that isn't a declared type or an in-scope
// type parameter. Composite types recurse; every other form (ParamType,
// the scalar/string/void built-ins, Self, dyn-trait, handle, projection)
// is intrinsically valid and skipped.
func (c *checker) checkTypeKnown(t ast.Type, params map[string]bool, pos ast.Position) {
	switch x := t.(type) {
	case ast.StructType:
		if !c.knownTypeName(x.Name, params) {
			c.errfCode(pos, "E064", "unknown type %q%s", x.Name, unknownTypeHint(x.Name))
			return
		}
		for _, a := range x.Args {
			c.checkTypeKnown(a, params, pos)
		}
	case ast.EnumType:
		if !c.knownTypeName(x.Name, params) {
			c.errfCode(pos, "E064", "unknown type %q%s", x.Name, unknownTypeHint(x.Name))
			return
		}
		for _, a := range x.Args {
			c.checkTypeKnown(a, params, pos)
		}
	case ast.ArrayType:
		c.checkTypeKnown(x.Elem, params, pos)
	case ast.SliceType:
		c.checkTypeKnown(x.Elem, params, pos)
	case ast.TupleType:
		for _, e := range x.Elems {
			c.checkTypeKnown(e, params, pos)
		}
	case *ast.FuncType:
		for _, p := range x.Params {
			c.checkTypeKnown(p, params, pos)
		}
		c.checkTypeKnown(x.Result, params, pos)
	}
}

// knownTypeName reports whether `name` is an in-scope type parameter or
// names any declared struct / enum / trait / resource (built-in generics
// like Map and Cell are registered structs; Option / Result are enums).
// It deliberately accepts a name known in *any* of these roles — the goal
// is to flag only genuinely-undefined names, not to police misuse of a
// known name (that's other rules' job), keeping false positives out.
func (c *checker) knownTypeName(name string, params map[string]bool) bool {
	if params[name] {
		return true
	}
	if _, ok := c.info.Structs[name]; ok {
		return true
	}
	if _, ok := c.info.Enums[name]; ok {
		return true
	}
	if _, ok := c.info.Traits[name]; ok {
		return true
	}
	if _, ok := c.info.Resources[name]; ok {
		return true
	}
	return false
}

// unknownTypeHint suggests the right spelling for a handful of common
// cross-language slips, appended to the E064 message.
func unknownTypeHint(name string) string {
	switch name {
	case "bool":
		return " (did you mean `boolean`?)"
	case "int", "long":
		return " (did you mean `i32`?)"
	case "uint", "u8":
		return " (did you mean `u32`?)"
	case "float", "double":
		return " (did you mean `f64`?)"
	case "str", "String":
		return " (did you mean `string`?)"
	}
	return ""
}

// typeParamSet builds a lookup set from a decl's type-parameter names, or
// nil when there are none.
func typeParamSet(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// paramPos prefers a Param's name position (so an E064 points at the
// offending parameter / field) and falls back to the decl position for a
// synthetic Param that carries no source location.
func paramPos(p ast.Param, fallback ast.Position) ast.Position {
	if p.NamePos.Line > 0 {
		return p.NamePos
	}
	return fallback
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

// monomorphCloneEnumName extracts, from a destination type, the name of
// a monomorphized enum clone (#3693) it pins — or "". Used to
// disambiguate a bare variant reference shared by clones of one generic
// enum (E__i32.A vs E__string.A). The Monomorphized gate keeps this a
// clone-only relaxation: user-written enums that share a variant name
// still require qualification (the E036 rule is unchanged for them).
func (c *checker) monomorphCloneEnumName(dest ast.Type) string {
	et, ok := dest.(ast.EnumType)
	if !ok {
		return ""
	}
	if ed, ok := c.info.Enums[et.Name]; ok && ed.Monomorphized {
		return et.Name
	}
	return ""
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
// arithOpMethod maps a binary arithmetic / bitwise operator to the
// conventionally-named method that overloads it on a composite type,
// mirroring `==`→eq / `<`→cmp. See compositeOpOverload / #2706.
var arithOpMethod = map[string]string{
	"+": "add", "-": "sub", "*": "mul", "/": "div", "%": "rem",
	"&": "bitand", "|": "bitor", "^": "bitxor", "<<": "shl", ">>": "shr",
}

// compositeOpOverload handles operator overloading for a binary operator
// whose operands are the same composite (struct / enum) type: it desugars
// `a <op> b` to the type's conventionally-named method (`arithOpMethod`),
// stashing the checked call on n.ArithCall (swapped in by the post-check
// rewrite) and returning its result type. handled=false means the operands
// aren't a matching composite pair, so the caller falls through to the
// numeric path. A composite operand without the method is a clear E009.
// tryConvertErrToDyn builds the desugar for an error-converting `?`: a
// `Result[T, E]` propagated through a function returning `Result[_, dyn
// Trait]` (E implements Trait) lowers to a block-expr that maps the error
// to `dyn Trait` then applies an ordinary exact-match `?`:
//
//	{ var __t: Result[T, dyn Trait] = match (inner) {
//	    Ok(__ok)  => Ok(__ok),
//	    Err(__e)  => Err(__e as dyn Trait),
//	  }; __t? }
//
// It type-checks the desugar so every later pass sees fully-typed nodes
// (the cast's DynCoercion recorded, the inner `?` stamped). Returns
// (nil, false) when the target error isn't a `dyn Trait` E implements.
// See #3234.
func (c *checker) tryConvertErrToDyn(n *ast.TryOp, srcEnum, retEnum ast.EnumType, s *scope) (ast.Expr, bool) {
	dt, ok := retEnum.Args[1].(ast.DynTraitType)
	if !ok {
		return nil, false
	}
	tn, ok := methodTypeName(srcEnum.Args[1])
	if !ok {
		return nil, false
	}
	// E must implement EVERY trait in the dyn-error set — `dyn A + B` ⇐ E iff
	// E impls A AND B (the same impl-all gate the multi-trait coercion uses).
	// A single-trait `dyn Error` is the 1-element case.
	if !c.implementsAllDynTraits(dt, tn) {
		return nil, false
	}
	c.tryConvN++
	tmp := fmt.Sprintf("__try_dyn_%d", c.tryConvN)
	okBind := fmt.Sprintf("__try_ok_%d", c.tryConvN)
	errBind := fmt.Sprintf("__try_err_%d", c.tryConvN)
	p := n.P
	resultDyn := ast.EnumType{Name: "Result", Args: []ast.Type{srcEnum.Args[0], dt}}
	mapMatch := &ast.MatchExpr{P: p, Tag: n.Inner, Arms: []*ast.MatchExprArm{
		{P: p, VariantName: "Ok", Bindings: []string{okBind},
			Body: &ast.Call{P: p, Callee: &ast.Ident{P: p, Name: "Ok"}, Args: []ast.Expr{&ast.Ident{P: p, Name: okBind}}}},
		{P: p, VariantName: "Err", Bindings: []string{errBind},
			Body: &ast.Call{P: p, Callee: &ast.Ident{P: p, Name: "Err"}, Args: []ast.Expr{
				&ast.CastExpr{P: p, Inner: &ast.Ident{P: p, Name: errBind}, Target: dt}}}},
	}}
	block := &ast.BlockExpr{P: p, Stmts: []ast.Stmt{
		&ast.Var{P: p, Name: tmp, Type: resultDyn, Init: mapMatch},
	}, Tail: &ast.TryOp{P: p, Inner: &ast.Ident{P: p, Name: tmp}}}
	if t := c.checkExpr(block, s); t == nil {
		return nil, false
	}
	return block, true
}

// tryConvertErrViaFrom builds the desugar for a `From`-based error-converting
// `?`: when the function's error type `E2` has an associated `from(E1): E2`
// (i.e. `impl From[E1] for E2`), a `Result[_, E1]` propagated through it maps
// `Err(e)` to `Err(E2.from(e))`. Structural by the `from` constructor's
// signature (so it's module-agnostic). Returns (nil, false) when E2 isn't a
// struct/enum with a matching `from`. See #2674.
func (c *checker) tryConvertErrViaFrom(n *ast.TryOp, srcEnum, retEnum ast.EnumType, s *scope) (ast.Expr, bool) {
	srcErr := srcEnum.Args[1]
	fnErr := retEnum.Args[1]
	tn, ok := methodTypeName(fnErr)
	if !ok {
		return nil, false
	}
	switch fnErr.(type) {
	case ast.StructType, ast.EnumType:
	default:
		return nil, false
	}
	// E2 must have an associated `from(E1): E2`.
	sig, ok := c.info.FuncSigs["__assoc_"+tn+"_from"]
	if !ok || len(sig.Params) != 1 || !ast.Equal(sig.Params[0], srcErr) || !ast.Equal(sig.Result, fnErr) {
		return nil, false
	}
	c.tryConvN++
	tmp := fmt.Sprintf("__try_from_%d", c.tryConvN)
	okBind := fmt.Sprintf("__try_ok_%d", c.tryConvN)
	errBind := fmt.Sprintf("__try_err_%d", c.tryConvN)
	p := n.P
	resultConv := ast.EnumType{Name: "Result", Args: []ast.Type{srcEnum.Args[0], fnErr}}
	// `E2.from(__e)` — an associated-function call (FieldAccess on the type name).
	fromCall := &ast.Call{P: p,
		Callee: &ast.FieldAccess{P: p, Target: &ast.Ident{P: p, Name: tn}, Field: "from"},
		Args:   []ast.Expr{&ast.Ident{P: p, Name: errBind}}}
	mapMatch := &ast.MatchExpr{P: p, Tag: n.Inner, Arms: []*ast.MatchExprArm{
		{P: p, VariantName: "Ok", Bindings: []string{okBind},
			Body: &ast.Call{P: p, Callee: &ast.Ident{P: p, Name: "Ok"}, Args: []ast.Expr{&ast.Ident{P: p, Name: okBind}}}},
		{P: p, VariantName: "Err", Bindings: []string{errBind},
			Body: &ast.Call{P: p, Callee: &ast.Ident{P: p, Name: "Err"}, Args: []ast.Expr{fromCall}}},
	}}
	block := &ast.BlockExpr{P: p, Stmts: []ast.Stmt{
		&ast.Var{P: p, Name: tmp, Type: resultConv, Init: mapMatch},
	}, Tail: &ast.TryOp{P: p, Inner: &ast.Ident{P: p, Name: tmp}}}
	if t := c.checkExpr(block, s); t == nil {
		return nil, false
	}
	return block, true
}

func (c *checker) compositeOpOverload(n *ast.Binary, lt, rt ast.Type, s *scope) (ast.Type, bool) {
	if lt == nil || !ast.Equal(lt, rt) {
		return nil, false
	}
	opMethod := arithOpMethod[n.Op]
	// Operator overloading over a trait-bounded TYPE PARAMETER: `a <op> b`
	// where `a`/`b` have type `T` and `T`'s bound provides the op's trait
	// method (`+`→`Add.add`, `*`→`Mul.mul`, …) desugars to `a.add(b)` —
	// resolved through the same deferred trait-bound dispatch as `a.cmp(b)`
	// for `T: Ord`. This is the #2706 payoff: generic numeric code
	// (`function sum[T: Num](xs: T[]): T { var acc = …; for x in xs { acc = acc + x } }`)
	// reads with operators instead of explicit `.add` calls. A type param
	// WITHOUT the matching arithmetic bound falls through (handled=false) to
	// the numeric path, which reports the usual E009.
	if pt, ok := lt.(ast.ParamType); ok {
		if opMethod == "" {
			return nil, false
		}
		if _, _, found := c.resolveTraitMethodForParam(pt.Name, opMethod); !found {
			return nil, false
		}
		call := &ast.Call{Callee: &ast.FieldAccess{Target: n.Left, Field: opMethod}, Args: []ast.Expr{n.Right}}
		rtt := c.checkExpr(call, s)
		n.ArithCall = call
		return rtt, true
	}
	switch lt.(type) {
	case ast.StructType, ast.EnumType:
	default:
		return nil, false
	}
	tn, _ := methodTypeName(lt)
	if mangled, ok := c.info.Methods[tn+"."+opMethod]; ok && c.methodVisibleHere(mangled) {
		call := &ast.Call{Callee: &ast.FieldAccess{Target: n.Left, Field: opMethod}, Args: []ast.Expr{n.Right}}
		rtt := c.checkExpr(call, s)
		n.ArithCall = call
		return rtt, true
	}
	c.errfCode(n.P, "E009", "operator %q is not defined for %s — implement `function (self: %s) %s(other: %s): %s` to overload it", n.Op, lt, tn, opMethod, tn, tn)
	return lt, true
}

// resolveProj resolves an associated-type projection whose base is a
// concrete type (`Foo::Item`) to the type the impl binds it to, recursing
// into composite types. A projection with an abstract base (`Self::Item`,
// `T::Item`) is left intact (its base is still resolved) — those resolve
// once the base becomes concrete (impl conformance / monomorph re-check).
// Requires c.info.AssocBindings, so it's only meaningful after the
// conformance pass. See docs/ASSOCIATED-TYPES.md.
func (c *checker) resolveProj(t ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.ProjType:
		base := c.resolveProj(x.Base)
		if tn, ok := methodTypeName(base); ok {
			if m, ok := c.info.AssocBindings[tn]; ok {
				if bound, ok := m[x.Name]; ok {
					return c.resolveProj(bound)
				}
			}
		}
		return ast.ProjType{Base: base, Name: x.Name}
	case ast.ArrayType:
		return ast.ArrayType{Elem: c.resolveProj(x.Elem)}
	case ast.SliceType:
		return ast.SliceType{Elem: c.resolveProj(x.Elem)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = c.resolveProj(x.Elems[i])
		}
		return out
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = c.resolveProj(x.Args[i])
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = c.resolveProj(x.Args[i])
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case *ast.FuncType:
		out := &ast.FuncType{Result: c.resolveProj(x.Result)}
		for _, p := range x.Params {
			out.Params = append(out.Params, c.resolveProj(p))
		}
		return out
	}
	return t
}

// resolveProjWith resolves associated-type projections using an explicit
// per-impl bindings map (assoc name → type), resolving any ProjType whose
// Name is bound regardless of base. Used in conformance comparison, where
// every projection refers to the impl's own associated types (the trait
// signature's `Self::Item` after Self→impl-type, and the impl method's
// own `Self::Item`). See docs/ASSOCIATED-TYPES.md.
func (c *checker) resolveProjWith(t ast.Type, bindings map[string]ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.ProjType:
		if b, ok := bindings[x.Name]; ok {
			return c.resolveProjWith(b, bindings)
		}
		return ast.ProjType{Base: c.resolveProjWith(x.Base, bindings), Name: x.Name}
	case ast.ArrayType:
		return ast.ArrayType{Elem: c.resolveProjWith(x.Elem, bindings)}
	case ast.SliceType:
		return ast.SliceType{Elem: c.resolveProjWith(x.Elem, bindings)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = c.resolveProjWith(x.Elems[i], bindings)
		}
		return out
	case ast.StructType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = c.resolveProjWith(x.Args[i], bindings)
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = c.resolveProjWith(x.Args[i], bindings)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case *ast.FuncType:
		out := &ast.FuncType{Result: c.resolveProjWith(x.Result, bindings)}
		for _, p := range x.Params {
			out.Params = append(out.Params, c.resolveProjWith(p, bindings))
		}
		return out
	}
	return t
}

// resolveProjections rewrites every concrete-base associated-type
// projection in the program's function signatures + bodies to its bound
// type, now that the conformance pass has filled c.info.AssocBindings.
// Runs each Check (incl. the monomorph re-check, which is what resolves a
// `T::Item` that monomorph substituted to a concrete `Foo::Item`).
func (c *checker) resolveProjections(prog *ast.Program) {
	if len(c.info.AssocBindings) == 0 {
		return
	}
	for name, sig := range c.info.FuncSigs {
		c.info.FuncSigs[name] = c.resolveProj(sig).(*ast.FuncType)
	}
	for _, fn := range prog.Funcs {
		fn.ReturnType = c.resolveProj(fn.ReturnType)
		for i := range fn.Params {
			fn.Params[i].Type = c.resolveProj(fn.Params[i].Type)
		}
		if fn.Receiver != nil {
			fn.Receiver.Type = c.resolveProj(fn.Receiver.Type)
		}
		if fn.Body != nil {
			c.resolveProjInBlock(fn.Body)
		}
	}
}

// resolveProjInBlock walks a body applying resolveProj to every type
// annotation slot (var decls, match binding types, lambda params/return,
// cast targets) so projections written inside bodies (a generic's
// `var x: T::Item`, concrete after monomorph) resolve too.
func (c *checker) resolveProjInBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		c.resolveProjInStmt(st)
	}
}

func (c *checker) resolveProjInStmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.Var:
		if x.Type != nil {
			x.Type = c.resolveProj(x.Type)
		}
		c.resolveProjInExpr(x.Init)
	case *ast.ExprStmt:
		c.resolveProjInExpr(x.Expr)
	case *ast.Return:
		c.resolveProjInExpr(x.Value)
	case *ast.Block:
		c.resolveProjInBlock(x)
	case *ast.If:
		c.resolveProjInExpr(x.Cond)
		c.resolveProjInStmt(x.Then)
		if x.Else != nil {
			c.resolveProjInStmt(x.Else)
		}
	case *ast.While:
		c.resolveProjInExpr(x.Cond)
		c.resolveProjInStmt(x.Body)
	case *ast.Loop:
		c.resolveProjInStmt(x.Body)
	case *ast.For:
		if x.Init != nil {
			c.resolveProjInStmt(x.Init)
		}
		c.resolveProjInExpr(x.Cond)
		if x.Step != nil {
			c.resolveProjInStmt(x.Step)
		}
		c.resolveProjInStmt(x.Body)
	case *ast.Match:
		c.resolveProjInExpr(x.Tag)
		for _, arm := range x.Arms {
			for i := range arm.BindingTypes {
				if arm.BindingTypes[i] != nil {
					arm.BindingTypes[i] = c.resolveProj(arm.BindingTypes[i])
				}
			}
			c.resolveProjInBlock(arm.Body)
		}
	}
}

func (c *checker) resolveProjInExpr(e ast.Expr) {
	switch x := e.(type) {
	case nil:
		return
	case *ast.CastExpr:
		x.Target = c.resolveProj(x.Target)
		c.resolveProjInExpr(x.Inner)
	case *ast.Assign:
		c.resolveProjInExpr(x.Target)
		c.resolveProjInExpr(x.Value)
	case *ast.Lambda:
		for i := range x.Params {
			x.Params[i].Type = c.resolveProj(x.Params[i].Type)
		}
		x.ReturnType = c.resolveProj(x.ReturnType)
		c.resolveProjInBlock(x.Body)
	case *ast.Binary:
		c.resolveProjInExpr(x.Left)
		c.resolveProjInExpr(x.Right)
	case *ast.Unary:
		c.resolveProjInExpr(x.Operand)
	case *ast.Call:
		c.resolveProjInExpr(x.Callee)
		for _, a := range x.Args {
			c.resolveProjInExpr(a)
		}
	case *ast.Index:
		c.resolveProjInExpr(x.Array)
		c.resolveProjInExpr(x.Idx)
	case *ast.FieldAccess:
		c.resolveProjInExpr(x.Target)
	}
}

// substByName substitutes a type whose bare name (StructType / EnumType
// with no args, or ParamType) is a key in `sub` — used to bind a generic
// trait's type parameters to an impl's TraitArgs during conformance. A
// trait method signature spells a trait param `T` as an unresolved
// `StructType{Name:"T"}` (trait methods aren't resolved against the
// trait's params), so plain ParamType substitution wouldn't catch it.
func substByName(t ast.Type, sub map[string]ast.Type) ast.Type {
	switch x := t.(type) {
	case ast.StructType:
		if len(x.Args) == 0 {
			if v, ok := sub[x.Name]; ok {
				return v
			}
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substByName(x.Args[i], sub)
		}
		return ast.StructType{Name: x.Name, Args: args}
	case ast.EnumType:
		if len(x.Args) == 0 {
			if v, ok := sub[x.Name]; ok {
				return v
			}
			return x
		}
		args := make([]ast.Type, len(x.Args))
		for i := range x.Args {
			args[i] = substByName(x.Args[i], sub)
		}
		return ast.EnumType{Name: x.Name, Args: args}
	case ast.ParamType:
		if v, ok := sub[x.Name]; ok {
			return v
		}
		return x
	case ast.ArrayType:
		return ast.ArrayType{Elem: substByName(x.Elem, sub)}
	case ast.SliceType:
		return ast.SliceType{Elem: substByName(x.Elem, sub)}
	case ast.TupleType:
		out := ast.TupleType{Elems: make([]ast.Type, len(x.Elems))}
		for i := range x.Elems {
			out.Elems[i] = substByName(x.Elems[i], sub)
		}
		return out
	case *ast.FuncType:
		out := &ast.FuncType{Result: substByName(x.Result, sub)}
		for _, p := range x.Params {
			out.Params = append(out.Params, substByName(p, sub))
		}
		return out
	case ast.ProjType:
		return ast.ProjType{Base: substByName(x.Base, sub), Name: x.Name}
	}
	return t
}

// implTraitArgsFor returns the type arguments the concrete type `ct` (with
// base name `tn`) supplies to `traitName`. For a concrete impl this is just
// the stored ImplTraitArgs. For a PARAMETRIC impl of a generic trait
// (`impl[T] Iterator[T] for ArrayIter[T]`) the stored args are generic ([T]):
// unify the recorded `for` pattern (ArrayIter[T]) against `ct` (ArrayIter[i32])
// to recover the binding (T=i32) and substitute, yielding the concrete args
// ([i32]). See docs/TRAITS.md.
func (c *checker) implTraitArgsFor(traitName, tn string, ct ast.Type) []ast.Type {
	implArgs := c.info.ImplTraitArgs[traitName][tn]
	if len(implArgs) == 0 {
		return implArgs
	}
	pat, ok := c.info.ImplForPattern[traitName][tn]
	if !ok {
		return implArgs
	}
	psub := map[string]ast.Type{}
	if !c.unifyType(pat, ct, psub) || len(psub) == 0 {
		return implArgs
	}
	out := make([]ast.Type, len(implArgs))
	for k := range implArgs {
		out[k] = substByName(implArgs[k], psub)
	}
	return out
}

// typeArgsEqual reports whether two type-argument lists are element-wise
// structurally equal (used to match a generic-trait bound's args against
// an impl's TraitArgs). See docs/TRAITS.md.
func typeArgsEqual(a, b []ast.Type) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !ast.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// normalizeParamRefs rewrites a parsed bound type-argument so any leaf whose
// name is a type parameter becomes a ParamType rather than a same-named
// zero-arg StructType. The parser emits the `T` in `Iterator[T]` as a nullary
// StructType — it can't distinguish a type-parameter reference from a nullary
// named type at parse time — so normalising to ParamType lets unifyType /
// substituteType, bindBoundParam, and substBoundArg treat `T` uniformly for
// bound-driven inference (#2691). Recurses through generic type arguments;
// non-param types pass through unchanged.
func normalizeParamRefs(t ast.Type, tpSet map[string]bool) ast.Type {
	switch b := t.(type) {
	case ast.StructType:
		if len(b.Args) == 0 {
			if tpSet[b.Name] {
				return ast.ParamType{Name: b.Name}
			}
			return t
		}
		na := make([]ast.Type, len(b.Args))
		for i := range b.Args {
			na[i] = normalizeParamRefs(b.Args[i], tpSet)
		}
		b.Args = na
		return b
	}
	return t
}

// bindBoundParam matches a generic-trait bound's type argument `boundArg`
// (which may reference the enclosing function's type parameters by name)
// against the concrete `implArg` the bound resolved to, recording any
// newly-resolved type parameter in `sub`. Returns true when it adds a
// binding. Bound args are parsed as a named type (StructType) or ParamType,
// so a leaf whose name is in `tpSet` is a type-parameter reference, not a
// concrete struct — that is the lever that pins `T` in `count[T, I:
// Iterator[T]]` from the `Iterator[i32] for RangeIter` impl. Nested generic
// bounds (`I: Iterator[Box[T]]`) recurse positionally. See #2691.
func bindBoundParam(boundArg, implArg ast.Type, tpSet map[string]bool, sub map[string]ast.Type) bool {
	bind := func(name string) bool {
		if tpSet[name] {
			if _, done := sub[name]; !done {
				sub[name] = implArg
				return true
			}
		}
		return false
	}
	switch b := boundArg.(type) {
	case ast.ParamType:
		return bind(b.Name)
	case ast.StructType:
		if len(b.Args) == 0 {
			return bind(b.Name)
		}
		if it, ok := implArg.(ast.StructType); ok && len(it.Args) == len(b.Args) {
			changed := false
			for i := range b.Args {
				if bindBoundParam(b.Args[i], it.Args[i], tpSet, sub) {
					changed = true
				}
			}
			return changed
		}
	}
	return false
}

// substBoundArg resolves any type-parameter references inside a generic-trait
// bound's type argument against the inferred substitution `sub`, so a bound
// written `Iterator[T]` can be compared (#2691, E021) against the concrete
// `Iterator[i32]` an impl provides once `T` is pinned. Type-param leaves are
// parsed as bare-name StructType / ParamType; non-param types pass through.
func substBoundArg(t ast.Type, tpSet map[string]bool, sub map[string]ast.Type) ast.Type {
	switch b := t.(type) {
	case ast.ParamType:
		if tpSet[b.Name] {
			if v, ok := sub[b.Name]; ok {
				return v
			}
		}
	case ast.StructType:
		if len(b.Args) == 0 {
			if tpSet[b.Name] {
				if v, ok := sub[b.Name]; ok {
					return v
				}
			}
			return t
		}
		na := make([]ast.Type, len(b.Args))
		for i := range b.Args {
			na[i] = substBoundArg(b.Args[i], tpSet, sub)
		}
		b.Args = na
		return b
	}
	return t
}

// traitArgsStr renders a trait's type-argument list as `[A, B]` (empty
// string for none) for diagnostics like `From[i32]`.
func traitArgsStr(args []ast.Type) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = demangle(a.String())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

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
	case ast.ProjType:
		// Substitute inside the base (`T::Item` → `IntBox::Item` when
		// T→IntBox); the concrete-base projection is resolved to its
		// binding by resolveProj. See docs/ASSOCIATED-TYPES.md.
		return ast.ProjType{Base: substituteType(x.Base, sub), Name: x.Name}
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
	// `never` (bottom) unifies to the other arm: an `if`/`match` branch
	// that always exits early contributes no value, so the construct's
	// result type comes from the branch(es) that do yield one. Two
	// never arms unify to never (the whole construct diverges). (#4522)
	if _, ok := a.(ast.NeverType); ok {
		return b
	}
	if _, ok := b.(ast.NeverType); ok {
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
	// dyn-trait coercion recording. Every concrete→`dyn Trait` boxing
	// site routes through here (this is the implicit-coercion chokepoint
	// the assignable() callers funnel through), so recording the
	// (trait, concrete) pair against the holder expression here covers
	// var init / assignment / argument / return / array-element /
	// struct-field uniformly. Recording-only — unlike the union case
	// below it does not rewrite the holder; the IR boxes it later. The
	// gate mirrors assignable()'s dyn branch (concrete impls the trait,
	// src is not already dyn). See docs/DYN-TRAITS.md §4.2.1.
	if dt, ok := dst.(ast.DynTraitType); ok {
		if _, isDyn := srcType.(ast.DynTraitType); !isDyn {
			// Coercion gate: the concrete must implement EVERY trait in
			// the set (`dyn A + B` ⇐ C iff C impls A AND B). Record the
			// whole set so tree-shaking roots all the impl methods.
			if tn, ok := methodTypeName(srcType); ok && c.implementsAllDynTraits(dt, tn) {
				if c.info.DynCoercions == nil {
					c.info.DynCoercions = map[ast.Expr]DynCoercion{}
				}
				c.info.DynCoercions[*holder] = DynCoercion{
					Trait:    dt.Trait0(),
					Traits:   dt.Traits,
					Concrete: tn,
				}
			}
		}
		return srcType
	}
	// Tuple literal against a tuple destination: recurse per element so
	// a union-member element widens (wraps) inside a multi-return —
	// `return (PatVariant { … }, p);` from a function declared
	// `(Pattern, Par)`. The element wrap rewrites the TupleLit slot in
	// place (same holder mechanics as every other site); the returned
	// type carries the widened element types so the caller's
	// assignable() check sees the post-wrap tuple.
	if dtup, dok := dst.(ast.TupleType); dok {
		tl, isLit := (*holder).(*ast.TupleLit)
		stup, isTup := srcType.(ast.TupleType)
		if isLit && isTup && len(tl.Elems) == len(dtup.Elems) && len(stup.Elems) == len(dtup.Elems) {
			out := make([]ast.Type, len(stup.Elems))
			copy(out, stup.Elems)
			for i := range tl.Elems {
				out[i] = c.maybeWrapForUnion(dtup.Elems[i], &tl.Elems[i], stup.Elems[i], s)
			}
			return ast.TupleType{Elems: out}
		}
		return srcType
	}
	du, dok := dst.(ast.EnumType)
	if !dok {
		return srcType
	}
	// Enum-payload dyn coercion (#3961): a variant-constructor call whose
	// result enum is being coerced to the SAME enum with a `dyn Trait`
	// payload — `Err(NotFound{…})` into `Result[_, dyn Error]` — must box
	// the payload into the `[data, vtable]` fat pointer. The enum-level
	// coercion below is otherwise a no-op (assignable permits the payload-
	// covariant widen), so the concrete payload would be stored straight
	// into the dyn slot and a later match-arm `e.message()` would dispatch
	// through a garbage vtable (segfault on the compiled backends). Inject
	// the explicit `payload as dyn Trait` cast the `?`-desugar already emits
	// (tryConvertErrViaDyn), per payload position whose declared slot
	// resolves to a `dyn Trait` under dst's type args, then re-check.
	if call, ok := (*holder).(*ast.Call); ok {
		if id, idOk := call.Callee.(*ast.Ident); idOk {
			if dts, found := c.variantDynPayloadTypes(du, id.Name); found && len(dts) == len(call.Args) {
				// Source payload types, so a payload that's ALREADY `dyn` is
				// left alone: that's a no-op coercion, and casting an already-
				// boxed `dyn` value would re-box it and over-release at drop
				// (the `enum Box { Wrap(dyn Shape) }` + `Box.Wrap(dc)` case
				// with `dc: dyn Shape`). Only a CONCRETE src payload widening
				// into a `dyn` dst slot needs the boxing cast.
				var srcPayloads []ast.Type
				if se, seOk := srcType.(ast.EnumType); seOk {
					srcPayloads, _ = c.variantDynPayloadTypes(se, id.Name)
				}
				changed := false
				for i := range call.Args {
					dt, isDyn := dts[i].(ast.DynTraitType)
					if !isDyn {
						continue
					}
					if i < len(srcPayloads) {
						if _, srcDyn := srcPayloads[i].(ast.DynTraitType); srcDyn {
							continue
						}
					}
					if _, already := call.Args[i].(*ast.CastExpr); already {
						continue
					}
					call.Args[i] = &ast.CastExpr{P: call.Args[i].Pos(), Inner: call.Args[i], Target: dt}
					changed = true
				}
				if changed {
					return c.checkExpr(*holder, s)
				}
			}
		}
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

// variantDynPayloadTypes resolves the payload types of `du`'s variant
// `variantName`, substituting the enum's type parameters with `du.Args` so a
// generic payload `E` becomes the concrete instantiation (e.g. `Result[_, dyn
// Error]`'s `Err` payload resolves to `dyn Error`). Returns (payloads, true)
// when the enum has such a variant, else (nil, false). Used by maybeWrapForUnion
// to spot a `dyn Trait` payload slot that needs the concrete arg boxed (#3961);
// a bare `ParamType` payload is substituted positionally, a concrete payload is
// returned as-is (a composite like `Box[E]` is never a `dyn Trait`, so leaving
// it unsubstituted is sound for this use).
func (c *checker) variantDynPayloadTypes(du ast.EnumType, variantName string) ([]ast.Type, bool) {
	ed, ok := c.info.Enums[du.Name]
	if !ok {
		return nil, false
	}
	for _, v := range ed.Variants {
		if v.Name != variantName {
			continue
		}
		out := make([]ast.Type, len(v.Payloads))
		for i, p := range v.Payloads {
			out[i] = p
			if pt, isParam := p.(ast.ParamType); isParam {
				for idx, tp := range ed.TypeParams {
					if tp == pt.Name && idx < len(du.Args) {
						out[i] = du.Args[idx]
						break
					}
				}
			}
		}
		return out, true
	}
	return nil, false
}

// inStdlibContext reports whether the checker is currently inside a
// stdlib/stdlib function body, where the low-level usize escape-hatch
// conversions in assignable are permitted. User code (c.current nil or a
// non-stdlib module) must use an explicit `as` cast instead. See
// docs/ADVERSARIAL-REVIEW-2026-06.md (F2).
func (c *checker) inStdlibContext() bool {
	return c.current != nil && strings.HasPrefix(c.current.SourceModule, "stdlib://")
}

// implementsAllDynTraits reports whether the concrete type named `tn`
// implements EVERY trait in the `dyn` set — the impl-all coercion gate
// for `dyn A + B` (a concrete coerces in iff it impls A AND B). The
// single-trait case is just the 1-element loop.
func (c *checker) implementsAllDynTraits(dt ast.DynTraitType, tn string) bool {
	for i, tr := range dt.Traits {
		if !c.info.Impls[tr][tn] {
			return false
		}
		// For a generic trait the impl must match the pinned arguments:
		// `BoxI: Container[i32]` coerces to `dyn Container[i32]` but not
		// to `dyn Container[string]`. ImplTraitArgs records what the
		// impl bound; an empty dyn-arg list (non-generic trait) skips it.
		if want := dt.ArgsFor(i); len(want) > 0 {
			if !typeArgsEqual(c.info.ImplTraitArgs[tr][tn], want) {
				return false
			}
		}
		// Pinned associated types must match the impl's binding too:
		// `IntBox: Producer<Item=i32>` coerces to `dyn Producer[Item = i32]`
		// but not `dyn Producer[Item = string]`. Info.AssocBindings records
		// what the impl bound (keyed by concrete type then assoc name).
		for _, b := range dt.AssocFor(i) {
			got, ok := c.info.AssocBindings[tn][b.Name]
			if !ok || !ast.Equal(got, b.Type) {
				return false
			}
		}
	}
	return true
}

// missingDynTraits returns a human-readable description of which trait(s)
// in the `dyn` set the concrete type `src` fails to implement — used to
// name the offending trait(s) in coercion-failure diagnostics. Returns
// the single trait spelling for a 1-element gap (so the single-trait
// message reads exactly as before).
func (c *checker) missingDynTraits(dt ast.DynTraitType, src ast.Type) string {
	tn, ok := methodTypeName(src)
	if !ok {
		return demangle(dt.Trait0())
	}
	var missing []string
	for _, tr := range dt.Traits {
		if !c.info.Impls[tr][tn] {
			missing = append(missing, demangle(tr))
		}
	}
	if len(missing) == 0 {
		// Shouldn't happen on the failure path, but fall back to the set.
		return demangle(dt.Trait0())
	}
	return strings.Join(missing, " + ")
}

// argAssignable is assignable plus the `str` borrow rule (#4813): a `str`
// view may flow into a `string` PARAMETER -- params are borrowed by default
// (OwnedByDefault), so the callee never frees its argument and lending a
// view is safe. Owning sinks stay on the strict assignable. `own` params
// are not yet distinguished here (FuncSigs carry types only); that
// tightening rides the A2 escape slice (#4814).
func (c *checker) argAssignable(want, got ast.Type, own bool) bool {
	if c.assignable(want, got) {
		return true
	}
	if own {
		// An `own` (consuming) parameter takes OWNERSHIP of its argument —
		// the callee frees it. A `str` view must never be freed by its
		// holder, so the borrow carve-out below does not apply: lending a
		// view to a consumer is exactly the #4294 corruption shape.
		// Materialise with .to_owned() instead.
		return false
	}
	_, gotStr := got.(ast.StrType)
	_, wantString := want.(ast.StringType)
	return gotStr && wantString
}

func (c *checker) assignable(dst, src ast.Type) bool {
	if ast.Equal(dst, src) {
		return true
	}
	// `never` (bottom) is assignable to any type: it is the type of an
	// expression that never yields a value (a value-position block whose
	// statements always exit early), so `var x: T = { …; return … };`
	// type-checks for every T (#4522).
	if _, ok := src.(ast.NeverType); ok {
		return true
	}
	// `str` (#4813): the borrowed-string view. An owned `string` freely
	// borrows INTO a `str` destination; a `str` never silently promotes to
	// an owned `string` -- an owning sink (var init, struct field, array
	// element, return) must materialise a fresh copy via .to_owned().
	// str==str is Equal above. Argument positions get a borrow carve-out
	// (argAssignable) since params are borrowed by default; tightening for
	// `own`-annotated params rides the A2 escape slice (#4814).
	if _, ok := dst.(ast.StrType); ok {
		_, srcIsString := src.(ast.StringType)
		return srcIsString
	}
	if _, ok := src.(ast.StrType); ok {
		return false
	}
	// Trait-object coercion (boxing): a concrete value whose type
	// implements `Trait` coerces to `dyn Trait`. This is the single
	// gate for every `dyn` boxing site (var init, assignment, argument,
	// array element, return) since they all route through assignable.
	// `dyn Trait` is not assignable back to a concrete type (no
	// downcast) and two different `dyn` types do not inter-assign. See
	// docs/DYN-TRAITS.md §5.
	if dt, ok := dst.(ast.DynTraitType); ok {
		if _, isDyn := src.(ast.DynTraitType); isDyn {
			return false // distinct dyn types: only Equal (handled above) assigns
		}
		if tn, ok := methodTypeName(src); ok {
			// Impl-ALL: a concrete coerces to `dyn A + B` iff it impls
			// every trait in the set.
			return c.implementsAllDynTraits(dt, tn)
		}
		return false
	}
	// WIT resource handles (P5 — docs/WIT-BRING-YOUR-OWN.md): an owned handle
	// `own R` coerces to a borrow `borrow R` of the same resource (you may
	// lend what you own). The reverse (a borrow flowing where an owned handle
	// is required) and any handle ↔ non-handle conversion stay rejected — a
	// plain i32 can't masquerade as a handle. Same-handle equality is already
	// covered by ast.Equal above.
	if dh, ok := dst.(ast.HandleType); ok {
		sh, isHandle := src.(ast.HandleType)
		return isHandle && dh.Borrowed && !sh.Borrowed && dh.Resource == sh.Resource
	}
	if _, ok := src.(ast.HandleType); ok {
		// A handle never flows into a non-handle destination.
		return false
	}
	// Pointer-shaped values ↔ usize, and usize ↔ i32 / i64. These
	// implicit conversions are a low-level escape hatch the stdlib's
	// raw-pointer helpers (__load_ptr / __store_ptr / __alloc, the Map
	// runtime) need: they declare pointer params + result as usize so the
	// full 8-byte address survives on arm64-darwin, and flow user-shaped
	// pointer / integer values through without an `as` cast. Exposing this
	// implicitly to USER code, though, turns usize into a wormhole that
	// launders i64→i32 narrowing and even string→struct reinterpretation
	// past the type system. So gate it to stdlib context; user code must
	// use an explicit `as` cast (the CastExpr machinery already allows the
	// usize hop). See docs/ADVERSARIAL-REVIEW-2026-06.md (F2).
	if c.inStdlibContext() {
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
	}
	// Option[usize] / Option[V] cross-assign for the codegen
	// alias boundary. `__method_Map_get(Map[K, V]): Option[V]`
	// (user-facing) routes to `__map_get_impl(m: usize):
	// Option[usize]` (stdlib). The user-code Option[V] flows
	// through the stdlib's Option[usize] return without an
	// explicit cast — same pointer, different type-level view.
	if de, dok := dst.(ast.EnumType); dok {
		if se, sok := src.(ast.EnumType); sok && de.Name == se.Name && len(de.Args) == len(se.Args) {
			allOk := true
			for i := range de.Args {
				if !c.assignable(de.Args[i], se.Args[i]) {
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
			if !c.assignable(d.Args[i], s.Args[i]) {
				return false
			}
		}
		return true
	}
	// Tuples assign element-wise. Without this, the only path to
	// a tuple assignment is the top-level `ast.Equal`, which
	// rejects a tuple whose elements are individually assignable
	// but not equal — e.g. `(None, s)` typed `(Option, Stream)`
	// returned from a function declared `(Option[i32], Stream)`.
	// The bare-return case relies on the 0-arg enum relaxation
	// below; recursing here lets that same relaxation reach a
	// tuple element. Cursor-idiom readers (docs/CURSOR-IDIOM.md)
	// that return `(Option[T], Stream)` with a bare `None` arm
	// depend on this.
	if dt, dok := dst.(ast.TupleType); dok {
		if st, sok := src.(ast.TupleType); sok && len(dt.Elems) == len(st.Elems) {
			for i := range dt.Elems {
				if !c.assignable(dt.Elems[i], st.Elems[i]) {
					return false
				}
			}
			return true
		}
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

// errfCode is the code-stamping sibling of errf — assigns a
// stable error code (docs/DIAGNOSTIC-UX-RESEARCH.md Rec §4)
// to the emission. Codes line up with the per-code catalogue
// under `internal/diag/explanations/`; surfacing them in the
// header lets users search for the error + look up the
// long-form explanation via `lang explain CODE`.
//
// Phase 1: codes stamped on a handful of common error sites
// (undefined identifier, type mismatches, missing struct
// fields, wrong-arity calls). Future PRs expand coverage —
// each stamping is mechanical, just touches the errf call.
func (c *checker) errfCode(pos ast.Position, code, format string, args ...any) {
	c.errors = append(c.errors, &Error{Pos: pos, Msg: fmt.Sprintf(format, args...), Path: c.currentFile(), ErrCode: code})
}

// pureCollectionMutators maps each value-returning collection mutator's
// mangled lowering to its source-level spelling. These are the operations
// that return a (possibly fresh) collection rather than mutating in place;
// discarding their result is the aliasing footgun E055 closes.
var pureCollectionMutators = map[string]string{
	"__method_Map_set":    "insert",
	"__method_Map_delete": "without",
	"__method_Map_clear":  "cleared",
	"__method_Array_set":  "with",
	"__method_Array_push": "append",
}

// checkUnusedCollectionResult implements E055: a bare statement whose whole
// expression is a value-returning collection mutator (`m.insert(k, v);`,
// `arr.append(x);`, …) silently discards the new collection. Under CoW that's
// correct only while the receiver is uniquely held — the moment an alias
// exists the write is lost (docs/PURE-COLLECTION-API-PLAN.md §1). Require the
// result be threaded back (`m = m.insert(k, v)`) or explicitly dropped
// (`var _ = m.insert(k, v)`, a declaration rather than a bare call).
//
// Only source-level method calls fire (Method != nil), so the `arr[i] = v`
// desugar and other synthesised `__method_*` calls are exempt; and only the
// outermost call of a statement is a bare ExprStmt, so a chained
// `m.insert(a,b).insert(c,d);` reports once.
func (c *checker) checkUnusedCollectionResult(e ast.Expr) {
	call, ok := e.(*ast.Call)
	if !ok || call.Method == nil {
		return
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return
	}
	if _, isMutator := pureCollectionMutators[id.Name]; !isMutator {
		return
	}
	c.errfCode(call.Method.FieldPos, "E055",
		"result of `.%s(...)` is unused; assign it back (e.g. `x = x.%s(...)`) — collection operations return a new value, they do not mutate in place (use `var _ = …` to discard intentionally)",
		call.Method.Field, call.Method.Field)
}

// fipNonAllocMethods is the whitelist of builtin methods a `fip` function may
// call: provably non-allocating, scalar-returning reads. Extend as more are
// proven heap-neutral.
var fipNonAllocMethods = map[string]bool{"len": true}

// checkFipFunctions verifies every `fip function` performs no heap allocation —
// a Koka-style fully-in-place CHECKED guarantee, as a SOUND, conservative
// subset (E053). It is verify-don't-enable: the in-place lowering (reuse, COW's
// unique-in-place branch, TRMC) already happens; `fip` only asserts and checks
// the result. Default-deny: any construct not proven heap-neutral is rejected.
//
// Allowed: scalars, arithmetic / comparison / logical ops, field & index READS,
// control flow, (re)binding locals, in-place index/field WRITES to an `own`
// array parameter (the COW unique-in-place branch — no copy), calls to other
// `fip` functions and the whitelisted non-allocating builtins (`len`).
// Rejected: array / tuple / struct / payload-carrying-enum literals, string
// concatenation / interpolation, writes to a non-`own` heap value (a copy), and
// any call the checker can't prove allocation-free.
//
// `fbip function` (fully in place with borrowing, plan E2') runs the SAME walk
// with one relaxation: constructor expressions — struct / tuple literals and
// payload-carrying enum variants — are allowed, because the IR layer verifies
// each such site is reuse-PAIRED (or covered by the graded allowance) and
// rejects the rest with E068 at lowering time. The same relaxation applies to
// a graded `fip(n)` / `fbip(n)` (FipAllowance > 0): the checker does NOT count
// n — the IR owns the count — it only stops rejecting the constructor shape.
// Everything else stays rejected for both: array literals (no array reuse
// pairing exists), string concat / interpolation, CoW-copy writes, and unproven
// calls. Call rule: `fip` may only call `fip` (the stronger claim); `fbip` may
// call `fip` or `fbip`.
func (c *checker) checkFipFunctions(prog *ast.Program) {
	fip := map[string]bool{}
	fbip := map[string]bool{}
	for _, fn := range prog.Funcs {
		if fn.Fip {
			fip[fn.Name] = true
		}
		if fn.Fbip {
			fbip[fn.Name] = true
		}
	}
	if len(fip)+len(fbip) == 0 {
		return
	}
	for _, fn := range prog.Funcs {
		if (!fn.Fip && !fn.Fbip) || fn.Body == nil {
			continue
		}
		kw := "fip"
		if fn.Fbip {
			kw = "fbip"
		}
		// Constructor expressions are allowed whenever the IR-level E068
		// verification owns the allocation budget: every `fbip` (each site
		// must be reuse-paired or within the allowance) and any graded
		// `fip(n)` (n > 0).
		ctorOK := fn.Fbip || fn.FipAllowance > 0
		own := map[string]bool{}
		for _, p := range fn.Params {
			if p.Own {
				own[p.Name] = true
			}
		}
		ast.Walk(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.ArrayLit:
				c.errfCode(x.Pos(), "E053", "`%s` function %q may not allocate (array literal)", kw, fn.Name)
			case *ast.TupleLit:
				if !ctorOK {
					c.errfCode(x.Pos(), "E053", "`%s` function %q may not allocate (tuple literal)", kw, fn.Name)
				}
			case *ast.StructLit:
				if !ctorOK {
					c.errfCode(x.Pos(), "E053", "`%s` function %q may not allocate (struct literal %q)", kw, fn.Name, x.TypeName)
				}
			case *ast.FString:
				c.errfCode(x.Pos(), "E053", "`%s` function %q may not allocate (string interpolation)", kw, fn.Name)
			case *ast.Binary:
				if x.IsStringConcat {
					c.errfCode(x.Pos(), "E053", "`%s` function %q may not allocate (string concatenation)", kw, fn.Name)
				}
			case *ast.Call:
				if x.IsVariantCall {
					if len(x.Args) > 0 && !ctorOK {
						c.errfCode(x.Pos(), "E053", "`%s` function %q may not allocate (enum variant construction)", kw, fn.Name)
					}
					return true
				}
				if x.Method != nil {
					// `recv.with(i, v)` on an `own` array is the in-place
					// element set (no size change → the COW unique-in-place
					// branch, no allocation). It is the method-call form of
					// the `arr[i] = v` write already allowed below, so it
					// carries the same `own`-root uniqueness assumption. This
					// is what lets the value-returning collection API
					// (`arr = arr.with(i, v)`, post-E056) stay fip — e.g. the
					// in-place insertion sorts.
					if x.Method.Field == "with" && len(x.Args) > 0 && own[fipRootIdent(x.Args[0])] {
						return true
					}
					if !fipNonAllocMethods[x.Method.Field] {
						c.errfCode(x.Pos(), "E053", "`%s` function %q may not call method %q (not proven allocation-free)", kw, fn.Name, x.Method.Field)
					}
					return true
				}
				if id, ok := x.Callee.(*ast.Ident); ok {
					// `fip` is the stronger claim: it may only lean on other
					// `fip` callees. `fbip` may also call `fbip` (the callee's
					// own construction sites are E068-verified in turn).
					if fn.Fbip {
						if !fip[id.Name] && !fbip[id.Name] {
							c.errfCode(x.Pos(), "E053", "`fbip` function %q may only call `fip` or `fbip` functions, not %q", fn.Name, id.Name)
						}
					} else if !fip[id.Name] {
						c.errfCode(x.Pos(), "E053", "`fip` function %q may only call other `fip` functions, not %q", fn.Name, id.Name)
					}
					return true
				}
				c.errfCode(x.Pos(), "E053", "`%s` function %q may not make an indirect call (not proven allocation-free)", kw, fn.Name)
			case *ast.Assign:
				if fipWriteAllocates(x.Target, own) {
					c.errfCode(x.Pos(), "E053", "`%s` function %q may not write to a non-`own` heap value (triggers a copy-on-write)", kw, fn.Name)
				}
			}
			return true
		})
	}
}

// fipWriteAllocates reports whether an assignment target can trigger a heap
// allocation. (Re)binding a local slot allocates nothing; an index/field write
// is in-place only when its root is an `own` parameter (the COW unique branch) —
// otherwise it copies.
func fipWriteAllocates(target ast.Expr, own map[string]bool) bool {
	switch t := target.(type) {
	case *ast.Ident:
		return false
	case *ast.Index:
		return !own[fipRootIdent(t.Array)]
	case *ast.FieldAccess:
		return !own[fipRootIdent(t.Target)]
	}
	return true
}

// fipRootIdent unwraps nested index / field accesses to the base identifier
// name (the container being written through), or "" if the base isn't a bare
// identifier.
func fipRootIdent(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x.Name
		case *ast.Index:
			e = x.Array
		case *ast.FieldAccess:
			e = x.Target
		default:
			return ""
		}
	}
}

// needCoreMap flags a Map construction site (map_new / Map literal)
// when core/map isn't imported. Reported once per program — Map's
// runtime helpers all come from the same module, so one diagnostic
// covers every use.
func (c *checker) needCoreMap(pos ast.Position) {
	if !c.requireMapImport || c.mapErrReported {
		return
	}
	c.mapErrReported = true
	c.errfCode(pos, "E001", "Map operations require `import \"core/map\";`")
}

// errIdent reports an unresolved-name error and tries to attach a
// near-miss fix by scanning every name visible in scope (locals,
// params, top-level functions). The error span covers the whole
// identifier so the squiggle underlines the misspelt name; the fix is
// MACHINE-APPLICABLE (diag.Suggestion, Rec §3) — replacing the ident
// with the candidate always re-parses, so the renderer's `help:` line
// doubles as the future LSP CodeAction seed.
func (c *checker) errIdent(n *ast.Ident, s *scope, format string, args ...any) {
	cands := c.collectNames(s)
	suggestion := diag.Suggest(n.Name, cands)
	e := &Error{
		Pos:     n.P,
		Span:    len(n.Name),
		Msg:     fmt.Sprintf(format, args...),
		Path:    c.currentFile(),
		ErrCode: "E001",
	}
	if suggestion != "" {
		e.Fix = &diag.Suggestion{
			Pos:         n.P,
			Length:      len(n.Name),
			Replacement: suggestion,
			Title:       fmt.Sprintf("replace `%s` with `%s`", n.Name, suggestion),
		}
	}
	c.errors = append(c.errors, e)
}

// errUnknownField reports an E043 unknown-field error (struct literal
// or field access), attaching a machine-applicable respelling fix when
// a declared field is a near miss — the same sound family as errIdent
// (replacing one identifier with another always re-parses). namePos is
// the field NAME token's own position (FieldInit.NamePos /
// FieldAccess.FieldPos); a zero position (synthetic node) skips the fix.
func (c *checker) errUnknownField(pos, namePos ast.Position, structName, field string, declared []string) {
	e := &Error{
		Pos:     pos,
		Msg:     fmt.Sprintf("struct %s has no field %q", structName, field),
		Path:    c.currentFile(),
		ErrCode: "E043",
	}
	if s := diag.Suggest(field, declared); s != "" && namePos.Line > 0 {
		e.Fix = &diag.Suggestion{
			Pos:         namePos,
			Length:      len(field),
			Replacement: s,
			Title:       fmt.Sprintf("replace `%s` with `%s`", field, s),
		}
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

// isUserFuncOrLocal reports whether `name` is bound by an in-scope
// variable or a user-declared (or checker-synthesised) function. Callers
// use it to disambiguate bare identifiers from same-named enum
// variants — a user-defined `Red` should win over `Color.Red`.
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

// capturedType reports whether `name` is NOT a local of the current
// function's scope `s` but DOES resolve in an enclosing function's
// scope via the captureChain — i.e. it's a closure capture — and if
// so returns its type. A local-function / lambda body gets a fresh
// scope chain (the captureChain is the only bridge to the enclosing
// function), so a name the current scope can't see but an enclosing
// scope can is a capture. Used to reject capture write-back of
// reference-shaped values (`cap = v` inside a closure), the
// enforcement counterpart to the field-immutability rule
// (docs/IMMUTABILITY-MIGRATION-PLAN.md §4).
func (c *checker) capturedType(name string, s *scope) (ast.Type, bool) {
	if _, ok := s.lookup(name); ok {
		return nil, false // a local of the current function
	}
	for i := len(c.captureChain) - 1; i >= 0; i-- {
		ent := c.captureChain[i]
		if ent.scope == nil {
			continue
		}
		if t, ok := ent.scope.lookup(name); ok {
			return t, true
		}
	}
	return nil, false
}

func (c *checker) checkFunction(fn *ast.FuncDecl) {
	c.current = fn
	defer func() { c.current = nil }()

	// A body-less `@import` function (extern WIT binding) has no body to
	// check; its signature is still registered so call sites resolve.
	if fn.Body == nil {
		return
	}

	// Return-type inference: a plain (non-method, non-generic) function
	// that wrote no `: Type` accumulates its return-expression types
	// while the body is checked, then unifies them into a concrete
	// return type (replacing the defaulted void). Methods / generics /
	// associated functions are out of scope for now and keep void.
	infer := fn.ReturnUnannotated && fn.Receiver == nil && fn.MethodRecv == "" && fn.AssocType == "" && len(fn.TypeParams) == 0
	prevInfer := c.inferReturns
	var rets []ast.Type
	if infer {
		c.inferReturns = &rets
	} else {
		c.inferReturns = nil
	}
	defer func() { c.inferReturns = prevInfer }()

	root := newScope(nil)
	for _, p := range fn.Params {
		if _, dup := root.names[p.Name]; dup {
			c.errfCode(fn.P, "E018", "duplicate parameter %q", p.Name)
		}
		root.names[p.Name] = p.Type
	}
	c.checkBlock(fn.Body, root)
	if infer {
		c.inferReturnType(fn, rets)
	}
	c.checkOwnedParams(fn)
	c.checkSliceEscape(fn)
	c.checkStrEscape(fn)
	c.checkMustConsume(fn)
	// A value-returning function must return on every path. Falling off
	// the end leaves the result undefined (the interpreter yields Void
	// where a real value is expected and crashes downstream; a scalar
	// silently reads 0). Void functions may fall through. See
	// docs/ADVERSARIAL-REVIEW-2026-06.md (F4).
	if fn.Body != nil && !isVoidReturn(fn.ReturnType) && !funcBodyExits(fn.Body) {
		c.errfCode(fn.P, "E052", "missing return: %q has return type %s but can fall off the end without returning a value", fn.Name, fn.ReturnType.String())
	}
}

// checkSliceEscape implements E063: a non-owning `[T]` slice must not
// outlive the storage it views. A slice is a `{data_ptr, len}` pair
// that holds no RC reference to its parent, so returning a slice whose
// backing array is function-local is a use-after-free the moment the
// frame's last owning reference drops — the documented dangling case
// in docs/LANGUAGE-DIRECTION.md's "Slice / view lifetime contract".
//
// The rule is conservative and low-false-positive: a return is rejected
// only when we can *prove* the slice views storage owned by this
// function — an array literal or a locally-declared owned array (chased
// through slice-typed local bindings and sub-slices). String slices are
// copies (`__str_slice` yields a fresh owned string), and slices of a
// parameter / receiver stay valid as long as the caller's owner does, so
// neither is flagged. Anything whose origin we can't pin down (a call
// result, a global, a field read) is assumed safe rather than rejected.
func (c *checker) checkSliceEscape(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	params := map[string]bool{}
	for _, p := range fn.Params {
		params[p.Name] = true
	}
	locals := map[string]*ast.Var{}
	for _, v := range c.info.Locals[fn] {
		locals[v.Name] = v
	}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.Return)
		if !ok || ret.Value == nil {
			return true
		}
		if c.sliceBorrowsLocal(ret.Value, locals, params, map[string]bool{}) {
			c.errfCode(ret.P, "E063", "returning a `[T]` slice that views function-local storage: the backing array is reclaimed when %q returns, leaving a dangling view — return an owned array (`T[]`) or slice a parameter instead", fn.Name)
		}
		return true
	})
}

// sliceBorrowsLocal reports whether expr evaluates to a `[T]` slice
// whose backing storage is local to the current function (so it dies
// when the function returns). `visiting` guards the binding-chase
// recursion against cycles. See checkSliceEscape.
func (c *checker) sliceBorrowsLocal(expr ast.Expr, locals map[string]*ast.Var, params, visiting map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.SliceExpr:
		if e.IsString {
			// String slicing copies into a fresh owned string.
			return false
		}
		return c.sourceIsLocalStorage(e.Source, locals, params, visiting)
	case *ast.Ident:
		if params[e.Name] {
			return false
		}
		v, ok := locals[e.Name]
		if !ok || v.Init == nil || visiting[e.Name] {
			return false
		}
		visiting[e.Name] = true
		defer delete(visiting, e.Name)
		return c.sliceBorrowsLocal(v.Init, locals, params, visiting)
	}
	return false
}

// sourceIsLocalStorage reports whether src names storage owned by the
// current function — an array literal, a locally-declared owned array,
// or a (sub)slice that itself views such storage. Parameters and
// receivers are caller-owned, so a slice of them is excluded.
func (c *checker) sourceIsLocalStorage(src ast.Expr, locals map[string]*ast.Var, params, visiting map[string]bool) bool {
	switch s := src.(type) {
	case *ast.ArrayLit:
		return true
	case *ast.SliceExpr:
		if s.IsString {
			return false
		}
		return c.sourceIsLocalStorage(s.Source, locals, params, visiting)
	case *ast.Ident:
		if params[s.Name] || visiting[s.Name] {
			return false
		}
		v, ok := locals[s.Name]
		if !ok {
			return false
		}
		if _, isArr := v.Type.(ast.ArrayType); isArr {
			// A locally-declared owned array is the backing storage.
			return true
		}
		if _, isSlice := v.Type.(ast.SliceType); isSlice && v.Init != nil {
			// A local slice borrows whatever its initializer views.
			visiting[s.Name] = true
			defer delete(visiting, s.Name)
			return c.sliceBorrowsLocal(v.Init, locals, params, visiting)
		}
		return false
	}
	return false
}

// checkStrEscape implements E065 — the `str` sibling of E063 (#4814 / #4297
// A2): a borrowed-string view must not escape via `return` unless its source
// is a parameter (caller-owned) or a string literal ('static / immortal). A
// `str` viewing a function-LOCAL owned `string` outlives storage the RC
// passes may reclaim at exit — the #4294 corruption class the `str` type
// exists to prevent. The producers are live: .trim() (P1) and s[a:b] (P2)
// both yield views, so the rejectable shapes are an ident chain or a slice
// expression bottoming out at a local `string` binding; the chase mirrors
// sliceBorrowsLocal (cycle-guarded, params excluded). Like E063, `return`
// is the only checked escape position for now, and the chase is
// intraprocedural — a view laundered through a str-returning callee is not
// chased (same known hole as the slice rule; both tighten together later).
func (c *checker) checkStrEscape(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}
	if _, ok := fn.ReturnType.(ast.StrType); !ok {
		return
	}
	params := map[string]bool{}
	for _, p := range fn.Params {
		params[p.Name] = true
	}
	locals := map[string]*ast.Var{}
	for _, v := range c.info.Locals[fn] {
		locals[v.Name] = v
	}
	ast.Walk(fn.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.Return)
		if !ok || ret.Value == nil {
			return true
		}
		if c.strViewsLocal(ret.Value, locals, params, map[string]bool{}) {
			c.errfCode(ret.P, "E065", "returning a `str` view of a function-local string: the backing string is reclaimed when %q returns, leaving a dangling view — return an owned `string` (materialise with .to_owned()) or view a parameter instead", fn.Name)
		}
		return true
	})
}

// strViewsLocal reports whether expr evaluates to a `str` view whose viewed
// storage is a function-local owned `string` (so it dies when the function
// returns). Params are caller-owned and string literals immortal — both are
// safe sources. A call result is not chased (today every callee returns an
// owned `string`, a move; the producer flip revisits this together with the
// slice rule's identical hole). `visiting` guards the binding-chase
// recursion against cycles, mirroring sliceBorrowsLocal.
func (c *checker) strViewsLocal(expr ast.Expr, locals map[string]*ast.Var, params, visiting map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.StringLit:
		return false // 'static / immortal
	case *ast.SliceExpr:
		// s[a:b] on a string is a sub-view of s's bytes (the P2 producer
		// flip): the slice escapes iff its base does.
		return c.strViewsLocal(e.Source, locals, params, visiting)
	case *ast.Ident:
		if params[e.Name] {
			return false // caller-owned
		}
		v, ok := locals[e.Name]
		if !ok || visiting[e.Name] {
			return false
		}
		if _, isStr := v.Type.(ast.StrType); isStr && v.Init != nil {
			// A local `str` binding views whatever its initializer views.
			visiting[e.Name] = true
			defer delete(visiting, e.Name)
			return c.strViewsLocal(v.Init, locals, params, visiting)
		}
		if _, isString := v.Type.(ast.StringType); isString {
			// A locally-declared owned string IS the backing storage.
			return true
		}
		return false
	}
	return false
}

// inferReturnType folds the return-expression types collected while
// checking an unannotated function's body (rets; a nil entry is a bare
// `return;`) into a single return type and stamps it on the FuncDecl +
// its registered signature. Only fires for functions that defaulted to
// void, which currently error if they return a value — so this never
// changes the meaning of already-valid code.
func (c *checker) inferReturnType(fn *ast.FuncDecl, rets []ast.Type) {
	var unified ast.Type
	hasVoid := false
	for _, t := range rets {
		if t == nil {
			hasVoid = true
			continue
		}
		if unified == nil {
			unified = t
			continue
		}
		if u, ok := unifyReturnType(unified, t); ok {
			unified = u
		} else {
			c.errfCode(fn.P, "E002", "cannot infer return type for %q: conflicting return types %s and %s; add an explicit return type", fn.Name, unified, t)
			// Keep the first type so downstream has something concrete.
		}
	}
	if unified == nil {
		// No value returns (only bare `return;` or none): stays void.
		return
	}
	if hasVoid {
		c.errfCode(fn.P, "E012", "function %q returns a value on some paths but not others; add an explicit return type", fn.Name)
	}
	fn.ReturnType = unified
	if sig, ok := c.info.FuncSigs[fn.Name]; ok {
		sig.Result = unified
	}
}

// unifyReturnType merges two inferred return-expression types into one.
// Beyond exact equality it bridges an under-specified enum constructor
// against a specified one — `return None;` types as a payload-less
// `Option`, which adopts the `Option[i32]` from a sibling `return
// Some(n);` (and the same for `Result`). Anything else is a conflict.
func unifyReturnType(a, b ast.Type) (ast.Type, bool) {
	if ast.Equal(a, b) {
		return a, true
	}
	ea, aok := a.(ast.EnumType)
	eb, bok := b.(ast.EnumType)
	if aok && bok && ea.Name == eb.Name {
		if len(ea.Args) == 0 && len(eb.Args) > 0 {
			return eb, true
		}
		if len(eb.Args) == 0 && len(ea.Args) > 0 {
			return ea, true
		}
	}
	return a, false
}

// methodConsumesReceiver reports whether the method named by a call's
// MethodCallSite takes its receiver by `own` (consuming) — for both concrete
// methods (the impl's hoisted self own-flag in c.ownFuncs) and `dyn Trait`
// dispatch (the trait method's declared self ownership). A consuming receiver is
// a MOVE, not a borrow, so the affine use-after-move analysis must record it as
// a consume; otherwise `x.consume(); x.consume()` slips past E050 and
// double-frees at runtime.
func (c *checker) methodConsumesReceiver(m *ast.MethodCallSite) bool {
	if m == nil {
		return false
	}
	if dt, ok := m.Receiver.(ast.DynTraitType); ok {
		// The method may be declared by any trait in the set — search
		// the union, matching checkDynMethodCall's resolution.
		for _, tr := range dt.Traits {
			td, ok := c.info.Traits[tr]
			if !ok {
				continue
			}
			for i := range td.Methods {
				if td.Methods[i].Name == m.Field {
					return len(td.Methods[i].Params) > 0 && td.Methods[i].Params[0].Own
				}
			}
		}
		return false
	}
	typeName, ok := methodTypeName(m.Receiver)
	if !ok {
		return false
	}
	if mangled, ok := c.info.Methods[typeName+"."+m.Field]; ok {
		flags := c.ownFuncs[mangled]
		return len(flags) > 0 && flags[0]
	}
	return false
}

// dynMethodConsumes reports whether trait method `field` on `trait` takes its
// receiver by `own` — the `dyn Trait` analogue of methodConsumesReceiver, read
// straight off the trait declaration (the dispatch is dynamic, so the trait
// signature is the contract).
func (c *checker) dynMethodConsumes(trait, field string) bool {
	td, ok := c.info.Traits[trait]
	if !ok {
		return false
	}
	for i := range td.Methods {
		if td.Methods[i].Name == field {
			return len(td.Methods[i].Params) > 0 && td.Methods[i].Params[0].Own
		}
	}
	return false
}

// checkOwnedParams is the affine use-after-move analysis for `own` (owned /
// consuming) parameters — the static foundation of Fern's ownership transfer.
// An owned param may be CONSUMED at most once on every execution path; using it
// after it's been consumed is E050. "Consume" = a whole-value use of the bare
// parameter (matched, returned, passed as a call argument, bound to a var,
// stored into a literal); "borrow" = a projection (`x.field`, `x[i]`, a method
// receiver `x.m()`, a closure call `x(...)`), which reads through the value
// without ending its life and may repeat. The classification is deliberately
// forward-compatible with the later ownership-transfer slice: a plain `f(x)`
// counts as a consume NOW, so code that would become a use-after-move once
// `own` args are lowered as moves is rejected up front.
//
// No codegen changes here — owned params still lower as borrowed; this only
// establishes the invariant the transfer + reuse slices rely on.
// SelfReassignOwnMoveArg recognizes the #4873 step-0 move shape on a
// self-reassignment `x = f(..., x, ...)`: the assign target is a bare
// ident whose name occurs EXACTLY ONCE anywhere in the RHS (a second
// read would observe the consumed value), and that one occurrence is a
// direct bare-ident argument sitting in an `own` position of the
// callee's flags. Returns that argument Ident, or nil when the shape
// doesn't match. Exported because the checker's E051 admission and the
// IR's move-on-call + overwrite-dec suppression must key on the
// IDENTICAL recognition — if they drift, either a double free (rc
// suppresses, checker rejects a shape that then re-lands via another
// path) or a leak/UAF (checker admits, rc still exit-decs) follows.
func SelfReassignOwnMoveArg(asn *ast.Assign, ownFuncs map[string][]bool) *ast.Ident {
	tid, ok := asn.Target.(*ast.Ident)
	if !ok {
		return nil
	}
	call, ok := asn.Value.(*ast.Call)
	if !ok {
		return nil
	}
	cid, ok := call.Callee.(*ast.Ident)
	if !ok {
		return nil
	}
	flags, isOwn := ownFuncs[cid.Name]
	if !isOwn {
		return nil
	}
	// Count every occurrence of the target name in the RHS, excluding
	// the callee ident itself (a call position, not a value read).
	count := 0
	ast.Walk(call, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id != cid && id.Name == tid.Name {
			count++
		}
		return true
	})
	if count != 1 {
		return nil
	}
	for i, a := range call.Args {
		if id, ok := a.(*ast.Ident); ok && id.Name == tid.Name {
			if i < len(flags) && flags[i] {
				return id
			}
			return nil
		}
	}
	return nil
}

func (c *checker) checkOwnedParams(fn *ast.FuncDecl) {
	owned := map[string]bool{}
	for _, p := range fn.Params {
		if p.Own {
			owned[p.Name] = true
		}
	}
	// Run when the current function has owned params (the affine move-check) OR
	// when the program declares ANY owned-param function (the call-site guard
	// must check every caller, even borrowed-only ones).
	if (len(owned) == 0 && len(c.ownFuncs) == 0) || fn.Body == nil {
		return
	}

	// isOwnedExpr reports whether `e` is a value the caller owns and can
	// TRANSFER into an `own` parameter: a fresh construction (struct / tuple /
	// array / map literal, string concat, variant-constructor call) or another
	// `own` parameter of the current function. A borrowed value — a borrowed
	// param, a field / index read, a plain local, a non-fresh call result —
	// cannot be transferred (the caller, or someone, still owns it), so passing
	// it to an `own` parameter is E051. Conservative: anything not provably
	// owned is rejected.
	// selfMoveArgs admits specific Ident NODES as owned arguments: the
	// exactly-once occurrence of `x` inside the RHS of a self-reassign
	// `x = f(..., x, ...)` where that occurrence is a direct argument in
	// an `own` position (#4873 step 0). The old binding dies at the
	// assignment — nothing can read it after the RHS evaluates — so the
	// caller can transfer it. Keyed by node identity so the SAME name
	// elsewhere (a second read in the RHS, a non-self-reassign call) is
	// still rejected. Populated by walkStmts' Assign case; the IR's
	// move-on-call + overwrite-dec suppression key on the identical
	// syntactic shape (SelfReassignOwnMoveArg, shared with the IR) so checker and rc
	// agree bit-for-bit on what moved.
	selfMoveArgs := map[ast.Expr]bool{}
	var isOwnedExpr func(e ast.Expr) bool
	isOwnedExpr = func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.StructLit, *ast.TupleLit, *ast.ArrayLit, *ast.MapLit:
			return true
		case *ast.Binary:
			return x.IsStringConcat
		case *ast.Ident:
			return owned[x.Name] || selfMoveArgs[e]
		case *ast.Call:
			if id, ok := x.Callee.(*ast.Ident); ok {
				if _, vrOk, _ := c.resolveVariant(id.Name, id.EnumName); vrOk {
					return true // variant-constructor call → fresh enum value
				}
				// A user function with a pointer result whose every pointer
				// parameter it could return is provably not BORROWED returns a
				// freshly-owned value. Two such cases:
				//   - no pointer parameters at all: the result is constructed fresh
				//     (it has no borrowed pointer argument to hand back);
				//   - every pointer parameter is `own`: the callee consumed each one
				//     (took ownership), so the result — whether freshly built or a
				//     threaded-and-returned `own` param — is owned by the caller, not
				//     a borrow of a caller-still-held value. (`build_stmt(own ops, s)
				//     -> Op[]` returning the grown `ops` is the self-host shape.)
				// Conservative: a BORROWED pointer parameter could be returned
				// (`id(x) -> x`), so a function with one isn't provably owned here.
				if sig, ok := c.info.FuncSigs[id.Name]; ok && sig.Result != nil && ast.IsPointerType(sig.Result) {
					flags := c.ownFuncs[id.Name]
					anyPtrParam, allPtrOwn := false, true
					for i, pt := range sig.Params {
						if pt != nil && ast.IsPointerType(pt) {
							anyPtrParam = true
							if i >= len(flags) || !flags[i] {
								allPtrOwn = false
							}
						}
					}
					if !anyPtrParam || allPtrOwn {
						return true
					}
				}
			}
			return false
		}
		return false
	}
	// guardCallArgs enforces the call-site ownership requirement: every argument
	// passed to an `own` parameter of a (plain, same-module) callee must be an
	// owned value. Method calls (receiver in Args[0]) and unresolved / mangled
	// callees are conservatively skipped here — a later slice widens the guard.
	guardCallArgs := func(x *ast.Call) {
		id, ok := x.Callee.(*ast.Ident)
		if !ok {
			return
		}
		flags, isOwn := c.ownFuncs[id.Name]
		if !isOwn {
			return
		}
		for i := 0; i < len(x.Args) && i < len(flags); i++ {
			if flags[i] && !isOwnedExpr(x.Args[i]) {
				c.errfCode(x.Args[i].Pos(), "E051", "argument to owned parameter must be an owned value (a fresh construction or another `own` parameter), not a borrowed one")
			}
		}
	}

	// moved records, per consumed owned-param name, the position of the consume
	// (a snapshot threaded through the flow-sensitive walk; a name's presence
	// means "already consumed on this path").
	type movedSet = map[string]ast.Position

	// recordExprUses classifies every owned-param occurrence in `e` and reports
	// E050 on a use after move; a fresh consume records into `moved`. Borrows
	// are the projection-target / call-callee idents; every other owned-ident
	// occurrence is a consume. Occurrences are visited in source (left-to-right
	// pre-order) order so `f(x) + x.len` flags the second use.
	recordExprUses := func(e ast.Expr, moved movedSet) {
		if e == nil {
			return
		}
		borrow := map[*ast.Ident]bool{}
		// dynConsumed holds receivers of `dyn Trait` calls to a CONSUMING (`own
		// self`) trait method. The callee is a FieldAccess, so the case below
		// would otherwise mark the receiver as a borrow; these are un-borrowed
		// after the walk so the move is recorded.
		dynConsumed := map[*ast.Ident]bool{}
		ast.Walk(e, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FieldAccess:
				if id, ok := x.Target.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.Index:
				if id, ok := x.Array.(*ast.Ident); ok {
					borrow[id] = true
				}
				if id, ok := x.Idx.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.CastExpr:
				// `x as T` READS x's value (e.g. pointer→usize for a runtime
				// call) — a borrow, never a transfer.
				if id, ok := x.Inner.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.DowncastExpr:
				// `x as? T` inspects the `dyn` value's runtime tag — a READ,
				// never a transfer.
				if id, ok := x.Inner.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.Binary:
				// Operands of `+`, `==`, `&&`, string-concat, … are READS.
				// (`sink(x) + sink(x)` keeps the x's as call-arg consumes — those
				// operands are Calls, not bare idents, so they aren't marked here.)
				if id, ok := x.Left.(*ast.Ident); ok {
					borrow[id] = true
				}
				if id, ok := x.Right.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.Unary:
				if id, ok := x.Operand.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.SliceExpr:
				// `s[lo:hi]` reads s (and the bounds) — a borrow.
				if id, ok := x.Source.(*ast.Ident); ok {
					borrow[id] = true
				}
				if id, ok := x.Low.(*ast.Ident); ok {
					borrow[id] = true
				}
				if id, ok := x.High.(*ast.Ident); ok {
					borrow[id] = true
				}
			case *ast.StructLit:
				// `S { ...base, f: v }`: the spread READS base's fields (the new
				// struct co-owns its pointer fields via the construction
				// alias-inc) — a borrow, not a transfer. The named field VALUES
				// below are still consumes (moved into the new struct).
				if x.Base != nil {
					if id, ok := x.Base.(*ast.Ident); ok {
						borrow[id] = true
					}
				}
			case *ast.Call:
				guardCallArgs(x)
				// The callee position is a borrow (function ref / closure call).
				if id, ok := x.Callee.(*ast.Ident); ok {
					borrow[id] = true
					// An owned value is CONSUMED only when passed to an `own`
					// parameter; an argument to a BORROWED parameter (or any param
					// of a callee with no `own` flags) is a read — a borrow — not a
					// move. (`contains_str(out, x)` borrows `out`, so a following
					// `out = out.append(..)` is not a use-after-move.) Without this
					// the affine walk over-approximates every whole-value argument
					// as a consume, which both rejects natural `own`-threaded
					// builder code and blocks tracking owned locals. The method
					// receiver (Args[0] when Method is set) keeps its own
					// consume/borrow classification below.
					flags := c.ownFuncs[id.Name]
					for ai, arg := range x.Args {
						if x.Method != nil && ai == 0 {
							continue
						}
						if ai < len(flags) && flags[ai] {
							continue // `own` position: a genuine consume
						}
						if aid, ok := arg.(*ast.Ident); ok {
							borrow[aid] = true
						}
					}
				}
				// A method call (`xs.len()`) is rewritten by the checker to a
				// plain Call with the receiver as Args[0] and Method set; a
				// borrowed-self receiver is BORROWED, not consumed. (A pipe
				// `x |> f()` also puts the LHS in Args[0] but Method is nil —
				// there it IS a real argument, so it stays a consume.) A
				// CONSUMING (`own self`) method, by contrast, MOVES its receiver,
				// so it is left out of `borrow` and recorded as a consume —
				// `x.consume(); x.consume()` is then a use-after-move (E050).
				if x.Method != nil && len(x.Args) > 0 && !c.methodConsumesReceiver(x.Method) {
					if id, ok := x.Args[0].(*ast.Ident); ok {
						borrow[id] = true
					}
				}
				// `dyn Trait` dispatch keeps the receiver in the FieldAccess
				// callee (no Method / Args[0]). A consuming trait method moves it.
				if x.DynTrait != "" {
					if fa, ok := x.Callee.(*ast.FieldAccess); ok {
						if rid, ok := fa.Target.(*ast.Ident); ok && c.dynMethodConsumes(x.DynTrait, fa.Field) {
							dynConsumed[rid] = true
						}
					}
				}
			}
			return true
		})
		for id := range dynConsumed {
			delete(borrow, id)
		}
		ast.Walk(e, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || !owned[id.Name] {
				return true
			}
			if at, isMoved := moved[id.Name]; isMoved {
				c.errfCode(id.Pos(), "E050", "use of owned parameter %q after it was consumed (moved at line %d)", id.Name, at.Line)
				return true
			}
			if !borrow[id] {
				moved[id.Name] = id.Pos() // a whole-value use consumes it
			}
			return true
		})
	}

	// joinInto merges a branch's post-state into `dst` IFF the branch does not
	// diverge (a diverging branch — return / break / continue — never reaches
	// the join, so its consumes don't constrain the fall-through path).
	joinInto := func(dst, branch movedSet, diverges bool) {
		if diverges {
			return
		}
		for k, v := range branch {
			if _, ok := dst[k]; !ok {
				dst[k] = v
			}
		}
	}
	cloneMoved := func(m movedSet) movedSet {
		n := make(movedSet, len(m))
		for k, v := range m {
			n[k] = v
		}
		return n
	}

	var walkStmt func(st ast.Stmt, moved movedSet)
	var walkStmts func(stmts []ast.Stmt, moved movedSet)

	// loopBody walks a loop body/step on a copy and flags any owned param it
	// consumes that was still live at loop entry — a later iteration would use
	// it after move. The loop may run zero times, so the fall-through state is
	// unchanged (`moved` is left as-is).
	loopBody := func(body ast.Stmt, step ast.Stmt, moved movedSet) {
		inner := cloneMoved(moved)
		if body != nil {
			walkStmt(body, inner)
		}
		if step != nil {
			walkStmt(step, inner)
		}
		for name, at := range inner {
			if _, wasLive := moved[name]; !wasLive {
				c.errfCode(at, "E050", "owned parameter %q is consumed inside a loop; a later iteration would use it after move", name)
			}
		}
	}

	walkStmt = func(st ast.Stmt, moved movedSet) {
		switch x := st.(type) {
		case *ast.Block:
			walkStmts(x.Stmts, moved)
		case *ast.Var:
			recordExprUses(x.Init, moved)
		case *ast.ExprStmt:
			// `x = e` is an Assign EXPRESSION wrapped in an ExprStmt. Reassigning
			// the bare owned param rebinds it to a fresh value, so its consumed
			// state resets (the new value's ownership is the transfer slice's
			// concern; here the reassign just clears the move) — distinct from a
			// plain read, which recordExprUses would treat as a consume.
			if asn, ok := x.Expr.(*ast.Assign); ok {
				// Self-reassign move admission (#4873 step 0): for
				// `x = f(..., x, ...)` with x a LOCAL passed exactly once,
				// directly, in an `own` position, admit that occurrence
				// (see selfMoveArgs). Checked before recordExprUses so
				// guardCallArgs sees the admission.
				if id, ok := asn.Target.(*ast.Ident); ok && !owned[id.Name] {
					if arg := SelfReassignOwnMoveArg(asn, c.ownFuncs); arg != nil {
						selfMoveArgs[arg] = true
					}
				}
				recordExprUses(asn.Value, moved)
				if id, ok := asn.Target.(*ast.Ident); ok && owned[id.Name] {
					delete(moved, id.Name)
				} else {
					recordExprUses(asn.Target, moved)
				}
				return
			}
			recordExprUses(x.Expr, moved)
		case *ast.Return:
			recordExprUses(x.Value, moved)
		case *ast.If:
			recordExprUses(x.Cond, moved)
			thenMoved := cloneMoved(moved)
			walkStmt(x.Then, thenMoved)
			elseMoved := cloneMoved(moved)
			if x.Else != nil {
				walkStmt(x.Else, elseMoved)
			}
			joinInto(moved, thenMoved, stmtDiverges(x.Then))
			joinInto(moved, elseMoved, x.Else != nil && stmtDiverges(x.Else))
		case *ast.While:
			recordExprUses(x.Cond, moved)
			loopBody(x.Body, nil, moved)
		case *ast.Loop:
			loopBody(x.Body, nil, moved)
		case *ast.For:
			if x.Init != nil {
				walkStmt(x.Init, moved)
			}
			recordExprUses(x.Cond, moved)
			loopBody(x.Body, x.Step, moved)
		case *ast.Match:
			// Matching an OWNED scrutinee consumes it (the box is decomposed)
			// and MOVES its pointer-typed payloads into the arm bindings — so a
			// pointer binding of an owned scrutinee is itself owned for that
			// arm's scope (the recursive `map(xs) -> Cons(.., map(t))` shape: `t`
			// is owned and may be transferred onward). Scalar bindings are copied
			// (not owned). Capture ownership BEFORE recordExprUses consumes the
			// scrutinee.
			scrutOwned := isOwnedExpr(x.Tag)
			recordExprUses(x.Tag, moved) // a bare-ident scrutinee is consumed here
			for _, arm := range x.Arms {
				armMoved := cloneMoved(moved)
				var added []string
				if scrutOwned {
					for i, bname := range arm.Bindings {
						if i < len(arm.BindingTypes) && arm.BindingTypes[i] != nil &&
							ast.IsPointerType(arm.BindingTypes[i]) && !owned[bname] {
							owned[bname] = true
							added = append(added, bname)
						}
					}
				}
				if arm.Body != nil {
					walkStmts(arm.Body.Stmts, armMoved)
				}
				for _, bname := range added {
					delete(owned, bname)
					// The binding is arm-local: it does not exist outside this arm,
					// so its consumed-state must NOT escape into the parent `moved`.
					// Otherwise a sibling match that reuses the same binding name
					// (`match (a) { Err(e) => ... }` then `match (b) { Err(e) => ...
					// }`) would see a phantom use-after-move on the second `e`. Drop
					// it before the join so only genuine own-param moves propagate.
					delete(armMoved, bname)
				}
				joinInto(moved, armMoved, arm.Body != nil && blockDiverges(arm.Body))
			}
		default:
			// Any other statement that carries expressions (IfLet, LetElse,
			// Defer, …) — conservatively scan it for owned-ident uses so a
			// consume there is still caught (over-counts as consume, never
			// misses a use-after-move).
			ast.Walk(st, func(n ast.Node) bool {
				if e, ok := n.(ast.Expr); ok {
					recordExprUses(e, moved)
					return false
				}
				return true
			})
		}
	}
	walkStmts = func(stmts []ast.Stmt, moved movedSet) {
		for _, st := range stmts {
			walkStmt(st, moved)
		}
	}

	walkStmts(fn.Body.Stmts, movedSet{})
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

// checkBlockExpr type-checks a block-expression `{ stmts; tail }` used
// in an `if`/`match` expression branch (slice 1). The statements run in
// a fresh child scope so locals they bind are visible to Tail but do
// NOT leak into the enclosing expression; the block's type is Tail's
// type checked in that scope. A value-less block (Tail == nil — its
// final element was a `;`-terminated statement) is `void`; the caller
// (if/match arm-type unification) rejects `void` where a value is
// required (see E061), so we surface the diagnostic here too.
func (c *checker) checkBlockExpr(n *ast.BlockExpr, parent *scope) ast.Type {
	s := newScope(parent)
	prevMutualRec := c.mutualRecSiblings
	c.mutualRecSiblings = nil
	for _, st := range n.Stmts {
		c.checkStmt(st, s)
	}
	c.mutualRecSiblings = prevMutualRec
	if n.Tail == nil {
		// A value-less block whose statements ALWAYS exit early
		// (`return` / `break` / `continue` on every path) never
		// reaches a trailing value, so it has no meaningful tail. It
		// is not `void` (which would be a type error where a value is
		// required) — it is the bottom type `never`, which is
		// assignable to / unifies with any type. This lets
		// `var x: i32 = { if (c) { return 1; } return 2; };` and the
		// `if`/`match`-arm forms type-check (#4522). Codegen lowers the
		// statements only — the diverging terminal makes the enclosing
		// store unreachable (the ssa lift skips it), so no tail value
		// is produced.
		if stmtsDiverge(n.Stmts) {
			return ast.NeverType{}
		}
		c.errfCode(n.P, "E061", "block-expression has no trailing value (its last element is a `;`-terminated statement); a value is required here — drop the trailing `;` to make the final expression the block's value")
		return ast.VoidType{}
	}
	return c.checkExpr(n.Tail, s)
}

// stmtsDiverge reports whether the last statement of a value-position
// block-expression's statement list exits on every path (so the block
// never falls through to a trailing value). Mirrors blockDiverges but
// over a bare `[]ast.Stmt` (BlockExpr.Stmts) rather than an *ast.Block.
func stmtsDiverge(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	return stmtDiverges(stmts[len(stmts)-1])
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
	case *ast.Loop:
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
	case *ast.DowncastExpr:
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
	case *ast.BlockExpr:
		for _, st := range n.Stmts {
			walkStmtForNames(st, selfName, siblings, seen)
		}
		walkExprForNames(n.Tail, selfName, siblings, seen)
	case *ast.StructLit:
		if n.Base != nil {
			walkExprForNames(n.Base, selfName, siblings, seen)
		}
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

// labelInScope reports whether `label` names an enclosing labeled loop.
func (c *checker) labelInScope(label string) bool {
	for _, l := range c.loopLabels {
		if l == label {
			return true
		}
	}
	return false
}

func (c *checker) checkStmt(st ast.Stmt, s *scope) {
	switch n := st.(type) {
	case *ast.Block:
		c.checkBlock(n, s)
	case *ast.If:
		t := c.checkExpr(n.Cond, s)
		if t != nil && !ast.Equal(t, ast.BoolType{}) {
			c.errfCode(n.Cond.Pos(), "E008", "if condition must be boolean, got %s", t)
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
				c.errfCode(n.Source.Pos(), "E022", "let-else source must be an enum value, got %s", st)
			}
			c.checkBlock(n.Else, s)
			return
		}
		ed := c.info.Enums[et.Name]
		if ed == nil {
			c.errfCode(n.Source.Pos(), "E023", "unknown enum %q", et.Name)
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
			c.errfCode(n.P, "E014", "variant %q is not part of enum %s", n.VariantName, ed.Name)
			c.checkBlock(n.Else, s)
			return
		}
		if len(n.Bindings) != len(variant.Payloads) {
			c.errfCode(n.P, "E015", "variant %s has %d payload(s), got %d binding(s)",
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
			c.errfCode(n.Else.P, "E022", "let-else: else branch must diverge (return / break / continue)")
		}
	case *ast.IfLet:
		// Source must produce an enum whose variant list contains
		// VariantName. Bindings are in scope for Then only.
		st := c.checkExpr(n.Source, s)
		et, ok := st.(ast.EnumType)
		if !ok {
			if st != nil {
				c.errfCode(n.Source.Pos(), "E022", "if-let source must be an enum value, got %s", st)
			}
			c.checkStmt(n.Then, s)
			if n.Else != nil {
				c.checkStmt(n.Else, s)
			}
			return
		}
		ed := c.info.Enums[et.Name]
		if ed == nil {
			c.errfCode(n.Source.Pos(), "E023", "unknown enum %q", et.Name)
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
			c.errfCode(n.P, "E014", "variant %q is not part of enum %s", n.VariantName, ed.Name)
			c.checkStmt(n.Then, s)
			if n.Else != nil {
				c.checkStmt(n.Else, s)
			}
			return
		}
		if len(n.Bindings) != len(variant.Payloads) {
			c.errfCode(n.P, "E015", "variant %s has %d payload(s), got %d binding(s)",
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
			c.errfCode(n.Cond.Pos(), "E008", "while condition must be boolean, got %s", t)
		}
		c.loopDepth++
		if n.Label != "" {
			c.loopLabels = append(c.loopLabels, n.Label)
		}
		c.checkStmt(n.Body, s)
		if n.Label != "" {
			c.loopLabels = c.loopLabels[:len(c.loopLabels)-1]
		}
		c.loopDepth--
	case *ast.Loop:
		c.loopDepth++
		if n.Label != "" {
			c.loopLabels = append(c.loopLabels, n.Label)
		}
		c.checkStmt(n.Body, s)
		if n.Label != "" {
			c.loopLabels = c.loopLabels[:len(c.loopLabels)-1]
		}
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
			c.errfCode(n.Cond.Pos(), "E008", "for condition must be boolean, got %s", ct)
		}
		c.loopDepth++
		if n.Label != "" {
			c.loopLabels = append(c.loopLabels, n.Label)
		}
		c.checkStmt(n.Body, inner)
		if n.Step != nil {
			c.checkStmt(n.Step, inner)
		}
		if n.Label != "" {
			c.loopLabels = c.loopLabels[:len(c.loopLabels)-1]
		}
		c.loopDepth--
	case *ast.Break:
		// `break` is legal inside a `for`/`while` (exits the loop).
		if c.loopDepth == 0 {
			c.errfCode(n.P, "E011", "break outside of a loop")
		} else if n.Label != "" && !c.labelInScope(n.Label) {
			c.errfCode(n.P, "E058", "break label %q does not match any enclosing loop", n.Label)
		}
	case *ast.Continue:
		if c.loopDepth == 0 {
			c.errfCode(n.P, "E011", "continue outside of a loop")
		} else if n.Label != "" && !c.labelInScope(n.Label) {
			c.errfCode(n.P, "E058", "continue label %q does not match any enclosing loop", n.Label)
		}
	case *ast.Return:
		want := c.current.ReturnType
		// Return-type inference: in an unannotated function we don't yet
		// know `want`, so instead of checking against void we record each
		// return's type (nil for a bare `return;`) and let checkFunction
		// unify them into the function's return type afterward.
		if c.inferReturns != nil && c.current.ReturnUnannotated {
			if n.Value == nil {
				*c.inferReturns = append(*c.inferReturns, nil)
				return
			}
			got := c.checkExpr(n.Value, s)
			got = c.postSettleType(n.Value, got)
			*c.inferReturns = append(*c.inferReturns, got)
			return
		}
		if n.Value == nil {
			if !ast.Equal(want, ast.VoidType{}) {
				c.errfCode(n.P, "E012", "return without value in function returning %s", want)
			}
			return
		}
		c.setElemHintFor(n.Value, want)
		c.expectedType = want
		got := c.checkExpr(n.Value, s)
		c.expectedType = nil
		c.elemHint = nil
		c.settleNumeric(n.Value, want)
		// Refresh `got` from the post-settle AST — the
		// `Var` path does the same via `postSettleType`,
		// and a tuple / numeric-literal return would
		// otherwise compare the pre-settle width against
		// the function's declared return type.
		got = c.postSettleType(n.Value, got)
		got = c.maybeWrapForUnion(want, &n.Value, got, s)
		// Same generic-call destination refinement as the
		// Var case — `return f(...)` from a function returning
		// Result[i32, i32] needs f's TypeArgs to be fully
		// concrete before monomorph runs.
		c.refineCallTypeArgsFromDest(n.Value, want)
		if got != nil && !c.assignable(want, got) {
			c.errfCode(n.P, "E002", "return type mismatch: function returns %s but expression is %s", want, got)
		}
	case *ast.Defer:
		// Just type-check the action; its result is discarded (defer is
		// statement-shaped, not expression-shaped). The IR builder is
		// responsible for replaying it at function exits.
		//
		// A block action `defer { … }` (#5153) is value-less by design, so
		// check its statements directly rather than through checkBlockExpr —
		// which would report E061 for the missing trailing value. Only the
		// immediate block is exempt; any nested value-position block inside
		// still goes through the normal value-required check.
		if blk, ok := n.Expr.(*ast.BlockExpr); ok {
			bs := newScope(s)
			prevMutualRec := c.mutualRecSiblings
			c.mutualRecSiblings = nil
			for _, st := range blk.Stmts {
				c.checkStmt(st, bs)
			}
			c.mutualRecSiblings = prevMutualRec
			if blk.Tail != nil {
				c.checkExpr(blk.Tail, bs)
			}
		} else {
			c.checkExpr(n.Expr, s)
		}
	case *ast.Var:
		if _, dup := s.names[n.Name]; dup {
			c.errfCode(n.P, "E013", "variable %q already declared in this scope", n.Name)
		}
		c.setElemHintFor(n.Init, n.Type)
		c.expectedType = n.Type
		got := c.checkExpr(n.Init, s)
		c.expectedType = nil
		c.elemHint = nil
		if n.Type != nil {
			c.settleNumeric(n.Init, n.Type)
			got = c.postSettleType(n.Init, got)
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
				c.errfCode(n.P, "E020", "empty array literal needs a type annotation")
				return
			}
			// An unannotated binding whose init is a bare integer
			// literal that doesn't fit i32 defaults to i64 rather
			// than the usual i32 (#3676). i32 is the default int, so
			// `var x = 5` stays polymorphic→i32; but a written-out
			// constant past i32 range (`var big = 5000000000`) has no
			// valid i32 reading — native would silently truncate it to
			// INT_MIN while the interp / self-host IR kept it wide, a
			// three-way divergence. Widening to i64 here makes all
			// paths agree and lets the literal "just work" (the
			// interp + self-host IR already treat a too-big bare
			// literal as i64; this pins native to match). Arithmetic
			// ON an i32 value still wraps at 32 bits (#3581) — only the
			// bare literal's own type widens.
			if gn, ok := got.(ast.NumberType); ok && gn.Polymorphic && intLitExceedsI32(n.Init) {
				i64 := ast.NumberType{Width: 64, Signed: true}
				c.settleInt(n.Init, i64)
				got = i64
			}
			n.Type = got
		} else if got != nil {
			got = c.maybeWrapForUnion(n.Type, &n.Init, got, s)
			if !c.assignable(n.Type, got) {
				c.errfCode(n.P, "E003", "cannot assign %s to variable of type %s", got, n.Type)
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
				c.errfCode(n.P, "E024", "tuple destructure needs a tuple expression, got %s", got)
			}
			return
		}
		if len(tup.Elems) != len(n.Names) {
			c.errfCode(n.P, "E024", "tuple has %d elements, but %d names given", len(tup.Elems), len(n.Names))
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
				c.errfCode(n.P, "E013", "variable %q already declared in this scope", name)
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
		c.checkUnusedCollectionResult(n.Expr)
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
// resolveVariantBindings validates a variant pattern's bindings and
// returns them, with their (type-substituted) types, in declaration
// (payload) order — ready to declare in a per-arm scope and to drive the
// position-based IR lowering. For a positional pattern it checks arity
// and pairs binding[i] with payload[i]. For a named-field pattern
// (named=true) each binding is a field name: every field must be named
// exactly once (any order), and the result is the variant's fields in
// declaration order. Errors report at pos. See docs/NAMED-FIELD-VARIANTS.md.
func (c *checker) resolveVariantBindings(pos ast.Position, variant *ast.EnumVariant, bindings []string, named bool, sub map[string]ast.Type) ([]string, []ast.Type) {
	if named && len(variant.FieldNames) > 0 {
		outNames := make([]string, len(variant.FieldNames))
		outTypes := make([]ast.Type, len(variant.FieldNames))
		copy(outNames, variant.FieldNames)
		for i := range variant.FieldNames {
			outTypes[i] = substituteType(variant.Payloads[i], sub)
		}
		seen := map[string]bool{}
		for _, b := range bindings {
			idx := -1
			for i, fn := range variant.FieldNames {
				if fn == b {
					idx = i
					break
				}
			}
			if idx < 0 {
				c.errfCode(pos, "E015", "variant %s has no field %q", variant.Name, b)
				continue
			}
			if seen[b] {
				c.errfCode(pos, "E015", "field %q bound more than once in pattern for %s", b, variant.Name)
			}
			seen[b] = true
		}
		if len(seen) != len(variant.FieldNames) {
			c.errfCode(pos, "E015", "named-field pattern for %s must bind all %d field(s) (%s)",
				variant.Name, len(variant.FieldNames), strings.Join(variant.FieldNames, ", "))
		}
		return outNames, outTypes
	}
	if named && len(variant.FieldNames) == 0 {
		c.errfCode(pos, "E015", "variant %s has positional payloads; match it as %s(...), not %s { ... }",
			variant.Name, variant.Name, variant.Name)
	}
	if len(bindings) != len(variant.Payloads) {
		c.errfCode(pos, "E015", "variant %s has %d payload(s), got %d binding(s)",
			variant.Name, len(variant.Payloads), len(bindings))
	}
	outTypes := make([]ast.Type, len(bindings))
	for k := range bindings {
		if k < len(variant.Payloads) {
			outTypes[k] = substituteType(variant.Payloads[k], sub)
		}
	}
	return bindings, outTypes
}

// wildcard). Bindings are typed against the matching variant's
// payload list and bound in a fresh per-arm scope.
func (c *checker) checkMatch(n *ast.Match, s *scope) {
	tagT := c.checkExpr(n.Tag, s)
	if tagT == nil {
		return
	}
	et, ok := tagT.(ast.EnumType)
	if !ok {
		// Tuple scrutinee: arms are tuple patterns `(p0, p1, …)` or
		// the wildcard — see checkTupleMatch.
		if tup, isTup := tagT.(ast.TupleType); isTup {
			c.checkTupleMatch(n, tup, s)
			return
		}
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
		c.errfCode(n.Tag.Pos(), "E023", "unknown enum %q", et.Name)
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
				c.errfCode(arm.P, "E026", "wildcard `_` arm must be last in the match")
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
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			c.checkBlock(arm.Body, s)
			continue
		}
		// A tuple pattern on an enum scrutinee is a shape error —
		// report it directly rather than letting the empty
		// VariantName fall through to a confusing E014.
		if arm.TupleElems != nil {
			c.errfCode(arm.P, "E035", "tuple pattern requires a tuple scrutinee, got enum %s", ed.Name)
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
					c.errfCode(arm.P, "E029", "variant pattern qualifier %q does not match scrutinee enum %s",
						arm.VariantModule, ed.Name)
				}
			} else if ed.SourceModule != "" && arm.VariantModule != ed.SourceModule {
				c.errfCode(arm.P, "E029", "variant pattern qualifier names module %q, but enum %s lives in module %q",
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
			c.errfCode(arm.P, "E014", "variant %q is not part of enum %s", arm.VariantName, ed.Name)
			c.checkBlock(arm.Body, s)
			continue
		}
		if covered[arm.VariantName] {
			c.errfCode(arm.P, "E028", "variant %q already covered earlier in this match", arm.VariantName)
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
		// Bind names in a fresh scope so they don't leak into
		// sibling arms. Payload types get the type-parameter
		// substitution applied so `Some(v)` on `Option[number]`
		// types `v` as `number`, not the abstract `T`. A named-field
		// pattern (`Rect { w, h }`) is validated + reordered into
		// declaration order here.
		arm.Bindings, arm.BindingTypes = c.resolveVariantBindings(arm.P, variant, arm.Bindings, arm.NamedFields, sub)
		armScope := newScope(s)
		for k, name := range arm.Bindings {
			armScope.names[name] = arm.BindingTypes[k]
		}
		// Guard runs in the bindings-in-scope frame so it can
		// reference the payload names. Required to be bool.
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		c.checkBlock(arm.Body, armScope)
	}
	if !sawWildcard {
		for _, v := range ed.Variants {
			if !covered[v.Name] {
				c.errfCode(n.P, "E030", "match is not exhaustive — variant %s of enum %s is not covered (add an arm or use `_`)",
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
				c.errfCode(arm.P, "E026", "wildcard `_` arm must be last in the match")
			}
			if arm.Guard == nil {
				sawWildcard = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			c.checkBlock(arm.Body, s)
			continue
		}
		if arm.Literal == nil {
			c.errfCode(arm.P, "E035", "match on non-enum value `%s` only accepts literal patterns or `_`", tagT)
			c.checkBlock(arm.Body, s)
			continue
		}
		litT := c.checkExpr(arm.Literal, s)
		if litT != nil {
			c.settleNumeric(arm.Literal, tagT)
			litT = c.postSettleType(arm.Literal, litT)
			if !c.assignable(litT, tagT) {
				c.errfCode(arm.P, "E035", "literal pattern of type %s does not match scrutinee type %s", litT, tagT)
			}
		}
		if arm.RangeHi != nil {
			// Range pattern `lo..hi`: the low bound is `Literal`, validated
			// above; the high bound needs the same type + settle. Ranges are
			// numeric-only — an ordered scalar scrutinee. Unsigned scrutinees
			// are deferred (the interpreter oracle compares signed), so they
			// are rejected here to keep native + interp in agreement.
			tagNum, tagIsNum := tagT.(ast.NumberType)
			_, tagIsFloat := tagT.(ast.FloatType)
			if !tagIsNum && !tagIsFloat {
				c.errfCode(arm.P, "E035", "range patterns require a numeric scrutinee, got %s", tagT)
			} else if tagIsNum && !tagNum.IsSigned() {
				c.errfCode(arm.P, "E035", "range patterns are not yet supported on an unsigned scrutinee (%s)", tagT)
			}
			hiT := c.checkExpr(arm.RangeHi, s)
			if hiT != nil {
				c.settleNumeric(arm.RangeHi, tagT)
				hiT = c.postSettleType(arm.RangeHi, hiT)
				if !c.assignable(hiT, tagT) {
					c.errfCode(arm.P, "E035", "range pattern bound of type %s does not match scrutinee type %s", hiT, tagT)
				}
			}
		}
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard, s)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		c.checkBlock(arm.Body, s)
	}
	if !sawWildcard {
		c.errfCode(n.P, "E030", "match on non-enum value is not exhaustive — add an unguarded `_` arm")
	}
}

// checkTupleMatch handles `match (pair) { (0, y) => …, (x, y) => … }`
// where the scrutinee is a tuple. Every arm must carry a tuple pattern
// of matching arity or be the wildcard. Literal elements type-check
// against the scrutinee's element types; binder elements are declared
// in a fresh per-arm scope with the element's type (BindingTypes is
// filled parallel to TupleElems so the IR picks the right load width).
// Exhaustiveness: an unguarded `_` OR an unguarded all-binder/wildcard
// (irrefutable) tuple arm covers everything; any arm after such an arm
// is unreachable (E026-family), and a match with neither is E030.
func (c *checker) checkTupleMatch(n *ast.Match, tup ast.TupleType, s *scope) {
	sawIrrefutable := false
	for _, arm := range n.Arms {
		if sawIrrefutable {
			c.errfCode(arm.P, "E026", "arm is unreachable — a preceding arm matches every value")
		}
		if arm.IsWildcard {
			if arm.Guard == nil {
				sawIrrefutable = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			c.checkBlock(arm.Body, s)
			continue
		}
		if arm.TupleElems == nil {
			c.errfCode(arm.P, "E035", "match on tuple `%s` only accepts tuple patterns or `_`", tup)
			c.checkBlock(arm.Body, s)
			continue
		}
		if len(arm.TupleElems) != len(tup.Elems) {
			c.errfCode(arm.P, "E035", "tuple pattern has %d elements, but scrutinee tuple has %d", len(arm.TupleElems), len(tup.Elems))
			c.checkBlock(arm.Body, s)
			continue
		}
		armScope := newScope(s)
		irrefutable := true
		arm.BindingTypes = make([]ast.Type, len(arm.TupleElems))
		seen := map[string]bool{}
		for k, el := range arm.TupleElems {
			elT := tup.Elems[k]
			arm.BindingTypes[k] = elT
			if el.Literal != nil {
				irrefutable = false
				litT := c.checkExpr(el.Literal, s)
				if litT != nil {
					c.settleNumeric(el.Literal, elT)
					litT = c.postSettleType(el.Literal, litT)
					if !c.assignable(litT, elT) {
						c.errfCode(arm.P, "E035", "literal pattern of type %s does not match tuple element %d of type %s", litT, k, elT)
					}
				}
				continue
			}
			if el.IsWildcard {
				continue
			}
			if seen[el.Name] {
				c.errfCode(arm.P, "E013", "variable %q already declared in this scope", el.Name)
				continue
			}
			seen[el.Name] = true
			armScope.names[el.Name] = elT
		}
		if arm.Guard != nil {
			irrefutable = false
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		if irrefutable {
			sawIrrefutable = true
		}
		c.checkBlock(arm.Body, armScope)
	}
	if !sawIrrefutable {
		c.errfCode(n.P, "E030", "match on tuple is not exhaustive — add an unguarded `_` or all-binder arm")
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
		// A `never` arm (one that always exits early) contributes no
		// value: the match's result type comes from the arms that do
		// yield one. If the running result is still `never`, adopt the
		// concrete arm; if this arm is `never`, keep the result. (#4522)
		if _, ok := result.(ast.NeverType); ok {
			result = armT
			return
		}
		if _, ok := armT.(ast.NeverType); ok {
			return
		}
		if c.assignable(armT, result) {
			return
		}
		if c.assignable(result, armT) {
			result = armT
			return
		}
		c.errfCode(p, "E031", "match arms have incompatible types: %s vs %s", result, armT)
	}
	for i, arm := range n.Arms {
		if arm.IsWildcard {
			if i != len(n.Arms)-1 {
				c.errfCode(arm.P, "E026", "wildcard `_` arm must be last in the match")
			}
			if arm.Guard == nil {
				sawWildcard = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			unify(c.checkExpr(arm.Body, s), arm.P)
			continue
		}
		if arm.Literal == nil {
			c.errfCode(arm.P, "E035", "match on non-enum value `%s` only accepts literal patterns or `_`", tagT)
			continue
		}
		litT := c.checkExpr(arm.Literal, s)
		if litT != nil {
			c.settleNumeric(arm.Literal, tagT)
			litT = c.postSettleType(arm.Literal, litT)
			if !c.assignable(litT, tagT) {
				c.errfCode(arm.P, "E035", "literal pattern of type %s does not match scrutinee type %s", litT, tagT)
			}
		}
		if arm.RangeHi != nil {
			// Range pattern `lo..hi`: the low bound is `Literal`, validated
			// above; the high bound needs the same type + settle. Ranges are
			// numeric-only — an ordered scalar scrutinee. Unsigned scrutinees
			// are deferred (the interpreter oracle compares signed), so they
			// are rejected here to keep native + interp in agreement.
			tagNum, tagIsNum := tagT.(ast.NumberType)
			_, tagIsFloat := tagT.(ast.FloatType)
			if !tagIsNum && !tagIsFloat {
				c.errfCode(arm.P, "E035", "range patterns require a numeric scrutinee, got %s", tagT)
			} else if tagIsNum && !tagNum.IsSigned() {
				c.errfCode(arm.P, "E035", "range patterns are not yet supported on an unsigned scrutinee (%s)", tagT)
			}
			hiT := c.checkExpr(arm.RangeHi, s)
			if hiT != nil {
				c.settleNumeric(arm.RangeHi, tagT)
				hiT = c.postSettleType(arm.RangeHi, hiT)
				if !c.assignable(hiT, tagT) {
					c.errfCode(arm.P, "E035", "range pattern bound of type %s does not match scrutinee type %s", hiT, tagT)
				}
			}
		}
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard, s)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		unify(c.checkExpr(arm.Body, s), arm.P)
	}
	if !sawWildcard {
		c.errfCode(n.P, "E030", "match on non-enum value is not exhaustive — add an unguarded `_` arm")
	}
	return result
}

// checkTupleMatchExpr is the expression-form counterpart of
// checkTupleMatch: same tuple-pattern arity / element-literal /
// binder rules, plus the arm-type unification checkLiteralMatchExpr
// does (each arm body is an Expr and the whole match evaluates to
// the unified type).
func (c *checker) checkTupleMatchExpr(n *ast.MatchExpr, tup ast.TupleType, s *scope) ast.Type {
	sawIrrefutable := false
	var result ast.Type
	unify := func(armT ast.Type, p ast.Position) {
		if armT == nil {
			return
		}
		if result == nil {
			result = armT
			return
		}
		// A `never` arm (one that always exits early) contributes no
		// value: the match's result type comes from the arms that do
		// yield one. If the running result is still `never`, adopt the
		// concrete arm; if this arm is `never`, keep the result. (#4522)
		if _, ok := result.(ast.NeverType); ok {
			result = armT
			return
		}
		if _, ok := armT.(ast.NeverType); ok {
			return
		}
		if c.assignable(armT, result) {
			return
		}
		if c.assignable(result, armT) {
			result = armT
			return
		}
		c.errfCode(p, "E031", "match arms have incompatible types: %s vs %s", result, armT)
	}
	for _, arm := range n.Arms {
		if sawIrrefutable {
			c.errfCode(arm.P, "E026", "arm is unreachable — a preceding arm matches every value")
		}
		if arm.IsWildcard {
			if arm.Guard == nil {
				sawIrrefutable = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			unify(c.checkExpr(arm.Body, s), arm.P)
			continue
		}
		if arm.TupleElems == nil {
			c.errfCode(arm.P, "E035", "match on tuple `%s` only accepts tuple patterns or `_`", tup)
			continue
		}
		if len(arm.TupleElems) != len(tup.Elems) {
			c.errfCode(arm.P, "E035", "tuple pattern has %d elements, but scrutinee tuple has %d", len(arm.TupleElems), len(tup.Elems))
			continue
		}
		armScope := newScope(s)
		irrefutable := true
		arm.BindingTypes = make([]ast.Type, len(arm.TupleElems))
		seen := map[string]bool{}
		for k, el := range arm.TupleElems {
			elT := tup.Elems[k]
			arm.BindingTypes[k] = elT
			if el.Literal != nil {
				irrefutable = false
				litT := c.checkExpr(el.Literal, s)
				if litT != nil {
					c.settleNumeric(el.Literal, elT)
					litT = c.postSettleType(el.Literal, litT)
					if !c.assignable(litT, elT) {
						c.errfCode(arm.P, "E035", "literal pattern of type %s does not match tuple element %d of type %s", litT, k, elT)
					}
				}
				continue
			}
			if el.IsWildcard {
				continue
			}
			if seen[el.Name] {
				c.errfCode(arm.P, "E013", "variable %q already declared in this scope", el.Name)
				continue
			}
			seen[el.Name] = true
			armScope.names[el.Name] = elT
		}
		if arm.Guard != nil {
			irrefutable = false
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		if irrefutable {
			sawIrrefutable = true
		}
		unify(c.checkExpr(arm.Body, armScope), arm.P)
	}
	if !sawIrrefutable {
		c.errfCode(n.P, "E030", "match on tuple is not exhaustive — add an unguarded `_` or all-binder arm")
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
		// Tuple scrutinee: arms are tuple patterns + a wildcard.
		if tup, isTup := tagT.(ast.TupleType); isTup {
			return c.checkTupleMatchExpr(n, tup, s)
		}
		// Non-enum scrutinee: arms are literal patterns + a
		// wildcard. Delegate to the literal-pattern branch.
		return c.checkLiteralMatchExpr(n, tagT, s)
	}
	ed, ok := c.info.Enums[et.Name]
	if !ok {
		c.errfCode(n.Tag.Pos(), "E023", "unknown enum %q", et.Name)
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
			c.errfCode(p, "E031", "match-expression arms differ: %s vs %s", result, armT)
		}
	}
	for i, arm := range n.Arms {
		if arm.IsWildcard {
			if i != len(n.Arms)-1 {
				c.errfCode(arm.P, "E026", "wildcard `_` arm must be last in the match")
			}
			if arm.Guard == nil {
				sawWildcard = true
			}
			if arm.Guard != nil {
				gt := c.checkExpr(arm.Guard, s)
				if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
					c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
				}
			}
			unify(c.checkExpr(arm.Body, s), arm.Body.Pos())
			continue
		}
		// Same tuple-pattern shape guard as the stmt-form arm loop.
		if arm.TupleElems != nil {
			c.errfCode(arm.P, "E035", "tuple pattern requires a tuple scrutinee, got enum %s", ed.Name)
			continue
		}
		// Same qualifier validation as the stmt-form arm loop above.
		if arm.VariantModule != "" && ed.SourceModule != "" && arm.VariantModule != ed.SourceModule {
			c.errfCode(arm.P, "E029", "variant pattern qualifier names module %q, but enum %s lives in module %q",
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
			c.errfCode(arm.P, "E014", "variant %q is not part of enum %s", arm.VariantName, ed.Name)
			unify(c.checkExpr(arm.Body, s), arm.Body.Pos())
			continue
		}
		if covered[arm.VariantName] {
			c.errfCode(arm.P, "E028", "variant %q already covered earlier in this match", arm.VariantName)
		}
		if arm.Guard == nil {
			covered[arm.VariantName] = true
		}
		arm.Bindings, arm.BindingTypes = c.resolveVariantBindings(arm.P, variant, arm.Bindings, arm.NamedFields, sub)
		armScope := newScope(s)
		for k, name := range arm.Bindings {
			armScope.names[name] = arm.BindingTypes[k]
		}
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard, armScope)
			if gt != nil && !ast.Equal(gt, ast.BoolType{}) {
				c.errfCode(arm.Guard.Pos(), "E027", "match guard must be boolean, got %s", gt)
			}
		}
		unify(c.checkExpr(arm.Body, armScope), arm.Body.Pos())
	}
	if !sawWildcard {
		for _, v := range ed.Variants {
			if !covered[v.Name] {
				c.errfCode(n.P, "E030", "match-expression is not exhaustive — variant %s of enum %s is not covered (add an arm or use `_`)",
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
		c.errfCode(fn.P, "E032", "use: cannot infer binding type for non-identifier source — add an explicit `: TYPE` annotation")
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
		c.errfCode(fn.P, "E032", "use: callee %q has no signature; add an explicit `: TYPE` annotation", id.Name)
		return
	}
	last := sig.Params[len(sig.Params)-1]
	cbSig, isFunc := last.(*ast.FuncType)
	if !isFunc {
		c.errfCode(fn.P, "E032", "use: callee %q's last parameter isn't a function — add an explicit `: TYPE` annotation", id.Name)
		return
	}
	if len(cbSig.Params) == 0 {
		c.errfCode(fn.P, "E032", "use: callee %q's callback takes no arguments — there's nothing to bind", id.Name)
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
			c.errfCode(fn.P, "E032", "use: could not infer binding type for %q from its arguments — add an explicit `: TYPE` annotation", id.Name)
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
			c.errfCode(fn.P, "E018", "duplicate parameter %q", p.Name)
		}
		root.names[p.Name] = p.Type
	}

	captured := map[string]ast.Type{}
	var captureOrder []string

	prev := c.current
	prevSink := c.captureSink
	prevOuter := c.captureOuter
	prevLoop := c.loopDepth
	c.current = fn
	c.loopDepth = 0
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
			c.errfCode(fn.P, "E044", "captured variable %q has unsupported type %s", name, t)
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
	}()

	c.checkBlock(fn.Body, root)

	// Build the capture list fresh: checkFunc can run more than once
	// over the same local function (e.g. re-analysis passes), and an
	// `append` would accumulate duplicates — which broke arm64's
	// mixed-width capture layout (a `[string, i32]` closure became
	// `[string, i32, string, i32]` and segfaulted).
	fn.Captures = nil
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
	case *ast.DowncastExpr:
		// `e as? T` — fallible downcast of a `dyn Trait` value to a
		// concrete type (docs/DYN-TRAITS.md §9). The LHS must be a
		// `dyn Trait`; the target must be a struct/enum that implements
		// that trait (slice 1 scope — primitive targets are a follow-up).
		// Result type is `Option[T]`. The runtime check + Some/None
		// construction lives in the interpreter; compiled backends reject
		// the node until a later codegen slice.
		inner := c.checkExpr(n.Inner, s)
		// The parser optimistically wraps a bare type name (`as? Color`)
		// as a StructType because it can't tell structs from enums. If the
		// name resolves to an enum, rewrite the target to an EnumType so
		// the result `Option[T]` matches a `var c: Option[Color]`
		// annotation (which resolveType already canonicalised to
		// EnumType). Without this, an enum downcast target would diverge —
		// `Option[StructType{Color}]` vs `Option[EnumType{Color}]` — and
		// fail an otherwise-correct assignment (E003).
		if st, isBareStruct := n.Target.(ast.StructType); isBareStruct && len(st.Args) == 0 {
			if _, isEnum := c.info.Enums[st.Name]; isEnum {
				n.Target = ast.EnumType{Name: st.Name}
			}
		}
		dt, ok := inner.(ast.DynTraitType)
		if !ok {
			c.errfCode(n.P, "E059", "'as?' downcast requires a 'dyn Trait' value on the left, got %s", inner)
			return ast.EnumType{Name: "Option", Args: []ast.Type{n.Target}}
		}
		// Trait records the PRIMARY trait (the bare single-trait vtable
		// key); Traits records the whole set. Compiled downcast codegen
		// keys the vtable-pointer compare by the whole set (dynVtableSetKey),
		// so a multi-trait `dyn A + B` downcast lowers via the MERGED
		// `__vtable_<A+B>_<T>` cell (docs/DYN-TRAITS.md §10). The impl gate
		// below checks the whole set.
		n.Trait = dt.Trait0()
		n.Traits = dt.Traits
		// The target must be a struct or enum (slice 1 scope).
		tn, hasName := methodTypeName(n.Target)
		_, isStruct := n.Target.(ast.StructType)
		_, isEnum := n.Target.(ast.EnumType)
		if !hasName || !(isStruct || isEnum) {
			c.errfCode(n.P, "E060", "'as?' downcast target must be a concrete struct or enum type (slice 1), got %s", n.Target)
			return ast.EnumType{Name: "Option", Args: []ast.Type{n.Target}}
		}
		// The target must implement EVERY trait in the set — mirror the
		// coercion gate (only a type that could have been coerced in can
		// be recovered).
		if !c.implementsAllDynTraits(dt, tn) {
			c.errfCode(n.P, "E060", "%s does not implement %s, so a '%s' cannot downcast to it", n.Target, c.missingDynTraits(dt, n.Target), dt.String())
		}
		return ast.EnumType{Name: "Option", Args: []ast.Type{n.Target}}
	case *ast.CastExpr:
		// Numeric ↔ numeric is the common case. The one
		// exception: a `[u8]` slice or `u8[]` array can cast
		// to `i32` to recover its data-pointer for the
		// bulk-memory primitives (__memcpy / __memset). It's
		// an explicit low-level escape hatch — useful inside
		// stdlib buffer-management helpers, marked by the
		// cast at the source level.
		inner := c.checkExpr(n.Inner, s)
		// `1 as u64`: settle the literal at the cast target's
		// width so the IR emits an i64.const, not an i32.const
		// that overflows. Without this, a literal like
		// `4611686018427387904 as u64` silently truncated to 0.
		//
		// Exception: a float→int cast must NOT settle its inner
		// toward the integer target — `settleNumeric` would route
		// a float binary/literal through `settleInt`, stamping it
		// with an integer width. The cast then sees `srcIsInt` and
		// lowers as an int→int identity, dropping the truncation
		// (the raw float bit-pattern leaked into the i32 result:
		// `(7.9 - 0.0) as i32` returned 154, the low byte of the
		// f64, instead of 7). Settle the inner toward its own float
		// type so polymorphic float literals still commit to the
		// f64 default, then lower as the float→int truncation.
		if ft, innerIsFloat := inner.(ast.FloatType); innerIsFloat {
			if _, tgtIsInt := n.Target.(ast.NumberType); tgtIsInt {
				floatHint := ast.FloatType(ft)
				if ft.Polymorphic {
					floatHint = ast.FloatType{Width: 64}
				}
				c.settleNumeric(n.Inner, floatHint)
			} else {
				c.settleNumeric(n.Inner, n.Target)
			}
		} else {
			c.settleNumeric(n.Inner, n.Target)
		}
		inner = c.postSettleType(n.Inner, inner)
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
		// the stdlib uses to call __memcpy / __store_ptr against
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
		// changes. Used by the stdlib when a builtin returns a
		// freshly allocated raw block that the caller wants to
		// expose as a typed collection (`__array_append_string`'s
		// rebuild loop) or as a wrapper struct (`map_new`'s
		// Map handle).
		if nt, ok := inner.(ast.NumberType); ok && (nt.NormalWidth() == 32 || nt.IsPointerWidth()) {
			switch n.Target.(type) {
			case ast.ArrayType, ast.StringType, ast.StructType:
				// A 32-bit-only source (i32 / u32 — NOT usize) reinterpreted as
				// a pointer-shaped handle is the #5042 truncation footgun: the
				// high 32 bits of the address were already lost when the value
				// became i32, so `k as string` / `k as T[]` / `k as Struct`
				// recovers a corrupt pointer once the heap crosses 4 GiB
				// (arm64-darwin). A `usize` source carries the full width, so
				// only the narrow case is flagged. Carry pointer-shaped values
				// in a `usize` local/param instead.
				if nt.NormalWidth() == 32 && !nt.IsPointerWidth() {
					c.errfCode(n.P, "E069", "reinterpreting a 32-bit `%s` value as the pointer-shaped type `%s` via `as` truncates the address: the high 32 bits were lost when the value became `%s`, so this recovers a corrupt pointer once the heap exceeds 4 GiB — carry pointer-shaped values in a `usize` local/param instead", inner, n.Target, inner)
				}
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
		if c.assignable(n.Target, inner) {
			return n.Target
		}
		c.errfCode(n.P, "E033", "cannot cast %s to %s; only numeric casts (and [u8]/u8[]/string ↔ i32 data-pointer hops, plus i32 → T[]) are supported", inner, n.Target)
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
		// A payload-less bare variant shared by multiple enum clones
		// (#3693) is disambiguated by the destination's expected enum.
		if n.EnumName == "" {
			if en := c.monomorphCloneEnumName(c.expectedType); en != "" {
				if _, ok, _ := c.resolveVariant(n.Name, en); ok {
					n.EnumName = en
				}
			}
		}
		if vr, ok, multi := c.resolveVariant(n.Name, n.EnumName); ok {
			if len(vr.payloads) > 0 {
				c.errfCode(n.P, "E036", "variant %s expects %d payload argument(s); call it as %s(...)",
					n.Name, len(vr.payloads), n.Name)
				return nil
			}
			n.EnumName = vr.enumName
			return ast.EnumType{Name: vr.enumName}
		} else if multi {
			c.errfCode(n.P, "E036", "variant %q is declared in multiple enums (%s) — qualify the reference, e.g. `%s.%s`",
				n.Name, c.variantEnumList(n.Name), c.variantOf[n.Name][0].enumName, n.Name)
			return nil
		} else if n.EnumName != "" {
			c.errfCode(n.P, "E036", "enum %s has no variant %q", n.EnumName, n.Name)
			return nil
		}
		c.errIdent(n, s, "undefined identifier %q", n.Name)
		return nil
	case *ast.ArrayLit:
		// Consume any element-type hint set by a coercion site
		// (var init / return / argument). For a `dyn Trait[]`
		// destination the elements need only each implement the
		// trait — they may be different concrete types — so we check
		// coercibility per element rather than mutual equality.
		hint := c.elemHint
		c.elemHint = nil
		if dt, ok := hint.(ast.DynTraitType); ok {
			for i := range n.Elems {
				t := c.checkExpr(n.Elems[i], s)
				// Record the concrete→`dyn Trait` coercion against the
				// element holder (compiled backends box it into the
				// `[data, vtable]` fat pointer via Info.DynCoercions —
				// docs/DYN-TRAITS.md §4.2.1). This mirrors the per-
				// element maybeWrapForUnion call the union-array branch
				// below makes, and is a no-op on the interpreter.
				t = c.maybeWrapForUnion(dt, &n.Elems[i], t, s)
				if t != nil && !c.assignable(dt, t) {
					c.errfCode(n.Elems[i].Pos(), "E034",
						"array element of type %s does not implement %s, so it cannot be a `%s`",
						t, c.missingDynTraits(dt, t), dt.String())
				}
			}
			n.ElemType = dt
			return ast.ArrayType{Elem: dt}
		}
		if len(n.Elems) == 0 {
			// Polymorphic-empty marker, resolved by the
			// surrounding context (Var annotation, function
			// arg, return type) via settleEmptyArray below.
			// If nothing settles it, the var-assignment site
			// raises the missing-annotation error.
			return ast.ArrayType{Elem: nil}
		}
		// Union element hint: an `N[]` literal whose elements are bare
		// variant structs (`[A { … }, B { … }]`) needs each element wrapped
		// into the union the same way a single `var n: N = A { … }`, a
		// `return`, or an `arr.push(A { … })` argument is — otherwise the
		// elements are stored as un-tagged structs and a later `match`
		// misfires (the push path wraps via the Call-argument coercion; the
		// array literal had no equivalent). maybeWrapForUnion is a no-op for
		// elements that are already enum values or don't match a variant.
		if eu, ok := hint.(ast.EnumType); ok {
			for i := range n.Elems {
				et := c.checkExpr(n.Elems[i], s)
				et = c.maybeWrapForUnion(eu, &n.Elems[i], et, s)
				if et != nil && !c.assignable(eu, et) {
					c.errfCode(n.Elems[i].Pos(), "E034", "array element type %s, expected %s", et, eu)
				}
			}
			n.ElemType = eu
			return ast.ArrayType{Elem: eu}
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
			c.errfCode(el.Pos(), "E034", "array element type %s, expected %s", t, elemT)
		}
		n.ElemType = elemT
		return ast.ArrayType{Elem: elemT}
	case *ast.Index:
		at := c.checkExpr(n.Array, s)
		it := c.checkExpr(n.Idx, s)
		if it != nil && !ast.Equal(it, ast.NumberType{}) {
			c.errfCode(n.Idx.Pos(), "E034", "index must be an integer, got %s", it)
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
		// `v[i]` on a `str` view reads the byte too — a read-only
		// operation, safe on a borrow (#4813). Same IsString lowering:
		// after erasure the operand IS a string.
		if _, ok := at.(ast.StrType); ok {
			n.IsString = true
			return ast.NumberType{}
		}
		if at != nil {
			c.errfCode(n.P, "E034", "indexing non-array value of type %s", at)
		}
		return nil
	case *ast.SliceExpr:
		st := c.checkExpr(n.Source, s)
		if n.Low != nil {
			lt := c.checkExpr(n.Low, s)
			if lt != nil && !ast.Equal(lt, ast.NumberType{}) {
				c.errfCode(n.Low.Pos(), "E037", "slice low bound must be i32, got %s", lt)
			}
		}
		if n.High != nil {
			ht := c.checkExpr(n.High, s)
			if ht != nil && !ast.Equal(ht, ast.NumberType{}) {
				c.errfCode(n.High.Pos(), "E037", "slice high bound must be i32, got %s", ht)
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
			// Slicing an owned string yields a `str` view of its bytes —
			// the #4813 P2 producer flip. The backends still copy until
			// the P3 zero-copy convergence; the erasure at LowerWith
			// keeps them seeing a plain string program either way.
			n.IsString = true
			return ast.StrType{}
		}
		// Slicing a `str` view yields another `str` — a sub-view of the
		// same bytes (#4813). The backends still copy until the P3
		// zero-copy convergence (a fresh box typed `str` is safe-leak at
		// worst); the erasure makes the lowering identical to a string
		// slice either way.
		if _, ok := st.(ast.StrType); ok {
			n.IsString = true
			return ast.StrType{}
		}
		if st != nil {
			c.errfCode(n.P, "E037", "cannot slice value of type %s", st)
		}
		return nil
	case *ast.Call:
		// Snapshot the destination type this call's result flows into
		// (set by `var x: T = …` / `return …`) and clear the field so
		// it can't leak into the argument sub-expressions we check
		// below — only *this* call's generic completion may consult it
		// for return-position inference (#2668).
		callExpected := c.expectedType
		c.expectedType = nil
		// Display spine (#2696): `print` / `write` / `eprint` accept any
		// `T: Display`, not just `string`. When the sole argument isn't
		// already a string, rewrite it to `arg.to_string()` (the same
		// desugar f-strings use) so the value is stringified through the
		// Display trait before it reaches the string-only runtime helper.
		// This removes the stringify-first dance (`print(x.to_string())`)
		// at every call site. Wrong arg counts fall through to the normal
		// path, which reports the arity error.
		if id, ok := n.Callee.(*ast.Ident); ok && len(n.Args) == 1 {
			switch id.Name {
			case "print", "write", "eprint":
				at := c.checkExpr(n.Args[0], s)
				at = c.postSettleType(n.Args[0], at)
				if at == nil {
					return ast.VoidType{}
				}
				switch at.(type) {
				case ast.StringType, ast.StrType:
					// A `str` view prints as its bytes — same box shape
					// as string at runtime (the LowerWith erasure).
					return ast.VoidType{}
				}
				if !c.typeImplementsDisplay(at) {
					c.errfCode(n.Args[0].Pos(), "E038",
						"argument 1 to %s: %s does not implement `Display` (no `to_string(): string` in scope) — add `@derive(Display)`, `impl Display for %s`, or import the module that provides it",
						id.Name, at, at)
					return ast.VoidType{}
				}
				n.Args[0] = &ast.Call{
					P: n.Args[0].Pos(),
					Callee: &ast.FieldAccess{
						P:      n.Args[0].Pos(),
						Target: n.Args[0],
						Field:  "to_string",
					},
				}
				_ = c.checkExpr(n.Args[0], s)
				return ast.VoidType{}
			}
		}
		if id, ok := n.Callee.(*ast.Ident); ok && id.Name == "map_new" {
			c.needCoreMap(n.P)
		}
		// `cell_new(v)` — the Cell[T] constructor (docs/CELL-TYPE-PLAN.md).
		// T is inferred from the argument (no destination relaxation needed:
		// the value drives T), stamped on n.TypeArgs for the IR, and checked
		// cycle-free (E057). Returns Cell[T].
		if id, ok := n.Callee.(*ast.Ident); ok && id.Name == "cell_new" {
			if len(n.Args) != 1 {
				c.errfCode(n.P, "E004", "cell_new expects 1 argument, got %d", len(n.Args))
				return ast.StructType{Name: "Cell"}
			}
			at := c.checkExpr(n.Args[0], s)
			at = c.postSettleType(n.Args[0], at)
			if at == nil {
				return ast.StructType{Name: "Cell"}
			}
			if !isCellElemType(at) {
				c.errfCode(n.Args[0].Pos(), "E057",
					"Cell[%s] is not allowed: a cell's element type must be a scalar (i32/i64/f64/bool) or string; a composite/reference type could form a cycle, which immutable data structures forbid",
					at)
			}
			n.TypeArgs = []ast.Type{at}
			return ast.StructType{Name: "Cell", Args: []ast.Type{at}}
		}
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
		// Associated-function call: `Point.origin(args)` — a FieldAccess
		// whose target is a known struct/enum TYPE name (not shadowed by a
		// value) and whose field is a registered associated function
		// (`__assoc_<T>_<f>`). Rewrite the callee to the flat assoc name so
		// the ordinary function-call path handles it with no receiver
		// prepended. Checked before the qualified-variant rewrite below,
		// which would otherwise claim every `Enum.x(...)` shape.
		if fa, ok := n.Callee.(*ast.FieldAccess); ok {
			if tid, ok := fa.Target.(*ast.Ident); ok {
				if _, shadowed := s.lookup(tid.Name); !shadowed {
					// Generic associated dispatch: `T.f(args)` where `T` is
					// a bounded type parameter of the current function whose
					// trait declares an associated function `f`. Type via
					// the trait signature (`Self` -> `ParamType(T)`) but
					// leave the Callee a FieldAccess — `T` is still abstract.
					// Monomorph substitutes `T` -> the concrete type in the
					// target Ident and re-check resolves the now-concrete
					// `Concrete.f()` to `__assoc_<Concrete>_f`. Mirrors the
					// deferred bounded-*method* path below.
					if c.current != nil && containsString(c.current.TypeParams, tid.Name) {
						if tm, _, found := c.resolveTraitMethodForParam(tid.Name, fa.Field); found && tm.Assoc {
							tp := ast.ParamType{Name: tid.Name}
							if len(n.Args) != len(tm.Params) {
								c.errfCode(n.P, "E004", "associated function %q expects %d argument(s), got %d", fa.Field, len(tm.Params), len(n.Args))
								return ast.SubstSelf(tm.Result, tp)
							}
							for i, arg := range n.Args {
								at := c.checkExpr(arg, s)
								want := ast.SubstSelf(tm.Params[i].Type, tp)
								if at != nil && !c.argAssignable(want, at, tm.Params[i].Own) {
									c.errfCode(arg.Pos(), "E038", "argument %d to %q: expected %s, got %s", i+1, fa.Field, want, at)
								}
							}
							n.Method = &ast.MethodCallSite{Field: fa.Field, FieldPos: fa.FieldPos, Receiver: tp}
							return ast.SubstSelf(tm.Result, tp)
						}
					}
					_, isStruct := c.info.Structs[tid.Name]
					_, isEnum := c.info.Enums[tid.Name]
					// A PRIMITIVE type name can be an associated-call target too
					// (`i32.default()` -> `__assoc_i32_default`, from `impl Default
					// for i32`) -- how a monomorphised `T.default()` with `T=i32`
					// resolves after the type-param rewrite.
					isPrim := isPrimitiveTypeName(tid.Name)
					if isStruct || isEnum || isPrim {
						if mangled, ok := c.info.Methods[tid.Name+"."+fa.Field]; ok {
							if strings.HasPrefix(mangled, "__assoc_") {
								n.Callee = &ast.Ident{P: fa.P, Name: mangled}
							} else {
								// A type-qualified call onto a method that
								// takes a `self` receiver — the user meant
								// `value.m()`, not `Type.m()`.
								c.errfCode(n.P, "E043", "%s.%s is a method; call it on a value (`v.%s(...)`), not on the type — only associated functions (no `self`) use `%s.%s(...)`",
									demangle(tid.Name), fa.Field, fa.Field, demangle(tid.Name), fa.Field)
								return nil
							}
						}
					}
				}
			}
		}
		if fa, ok := n.Callee.(*ast.FieldAccess); ok {
			if tid, ok := fa.Target.(*ast.Ident); ok {
				if _, isEnum := c.info.Enums[tid.Name]; isEnum {
					n.Callee = &ast.Ident{P: fa.P, Name: fa.Field, EnumName: tid.Name}
				}
			}
		}
		if id, ok := n.Callee.(*ast.Ident); ok {
			vr, vrOk, vrMulti := c.resolveVariant(id.Name, id.EnumName)
			// A bare variant shared by multiple enum clones (#3693) is
			// disambiguated by the destination's expected enum. checkCall
			// snapshotted that into `callExpected` (the live field was
			// cleared above so it can't leak into the args).
			if vrMulti && id.EnumName == "" {
				if en := c.monomorphCloneEnumName(callExpected); en != "" {
					if vr2, ok2, _ := c.resolveVariant(id.Name, en); ok2 {
						vr, vrOk, vrMulti = vr2, true, false
						id.EnumName = en
					}
				}
			}
			isVar := vrOk && !c.isUserFuncOrLocal(id.Name, s)
			// Bare-name reference to a variant that lives in two
			// or more enums — the call site has to qualify.
			// Report once, then fall through (vrOk=false) so the
			// usual function-call path runs and the caller still
			// gets a follow-up "undefined identifier" diag if the
			// name resolves to nothing else.
			if !isVar && vrMulti && id.EnumName == "" && !c.isUserFuncOrLocal(id.Name, s) {
				c.errfCode(n.P, "E036", "variant %q is declared in multiple enums (%s) — qualify the reference, e.g. `%s.%s(...)`",
					id.Name, c.variantEnumList(id.Name), c.variantOf[id.Name][0].enumName, id.Name)
				return nil
			}
			if isVar {
				if len(n.Args) != len(vr.payloads) {
					c.errfCode(n.P, "E036", "variant %s expects %d argument(s), got %d",
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
						c.errfCode(a.Pos(), "E036", "variant %s payload %d type %s, expected %s",
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
		// Method call dispatch: `target.method(args)` where target is a
		// struct or enum value with a method of that name. We rewrite
		// the Call node in place to `mangledName(target, args)` so the
		// rest of the pipeline (codegen, IR) only ever sees a regular
		// function call.
		if fa, ok := n.Callee.(*ast.FieldAccess); ok {
			tt := c.checkExpr(fa.Target, s)
			// The receiver itself failed to type — an error is already
			// reported on it (e.g. a chained `n.foo().bar()` whose inner
			// `n.foo()` is invalid). Bail now: falling through to the
			// generic `c.checkExpr(n.Callee, …)` below would re-check this
			// same target and emit the identical diagnostic a second time.
			if tt == nil {
				return nil
			}
			// Trait-bounded type parameter: `x.m(...)` where `x: T`
			// and `T: SomeTrait`. We type-check against the trait's
			// signature here but DON'T rewrite the callee — the
			// receiver type is still abstract. monomorph clones the
			// body with `T` substituted by the concrete type and
			// re-runs Check, at which point the ordinary dispatch
			// path below resolves the now-concrete receiver to the
			// impl's mangled method. See docs/TRAITS.md §4.
			if pt, ok := tt.(ast.ParamType); ok {
				tm, _, found := c.resolveTraitMethodForParam(pt.Name, fa.Field)
				if !found {
					c.errfCode(n.P, "E021",
						"no method %q on type parameter %s; add a trait bound such as [%s: SomeTrait] that provides it",
						fa.Field, pt.Name, pt.Name)
					return nil
				}
				wantParams := tm.Params[1:] // drop `self`
				if len(n.Args) != len(wantParams) {
					c.errfCode(n.P, "E004", "method %q expects %d argument(s), got %d", fa.Field, len(wantParams), len(n.Args))
					return ast.SubstSelf(tm.Result, tt)
				}
				for i, arg := range n.Args {
					at := c.checkExpr(arg, s)
					want := ast.SubstSelf(wantParams[i].Type, tt)
					if at != nil && !c.argAssignable(want, at, wantParams[i].Own) {
						c.errfCode(arg.Pos(), "E038", "argument %d to %q: expected %s, got %s", i+1, fa.Field, want, at)
					}
				}
				n.Method = &ast.MethodCallSite{Field: fa.Field, FieldPos: fa.FieldPos, Receiver: tt}
				return ast.SubstSelf(tm.Result, tt)
			}
			// Trait object: `d.m(...)` where `d: dyn Trait`. Resolve the
			// method against the TRAIT's signature (not a concrete
			// method table) and mark the call as dynamic — the callee
			// stays a FieldAccess, monomorph leaves it alone, and the
			// interpreter dispatches by the receiver value's runtime
			// concrete type. See docs/DYN-TRAITS.md §4.1.
			if dt, ok := tt.(ast.DynTraitType); ok {
				return c.checkDynMethodCall(n, fa, dt, s)
			}
			var typeName string
			switch t := tt.(type) {
			case ast.StructType:
				typeName = t.Name
			case ast.EnumType:
				typeName = t.Name
			case ast.StringType:
				_ = t
				typeName = "string"
			case ast.StrType:
				// `str` (#4813) shares the `string` method surface --
				// methods borrow their receiver, so dispatching a view
				// through the string method table is sound.
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
			case ast.BoolType:
				_ = t
				typeName = "boolean"
			case ast.ArrayType:
				// Generic array methods (today: `push`, `len`).
				// Treated as if Array were a one-type-param
				// generic struct so the receiver-TypeArgs
				// substitution path applies — `string[].push(v)`
				// checks `v` as string, `JsonValue[].push(v)` as
				// JsonValue.
				_ = t
				typeName = "Array"
			case ast.SliceType:
				// Generic slice methods (today: `len`). Same
				// one-type-param shape as Array — the element
				// type flows through `n.TypeArgs` for any
				// future per-T method.
				_ = t
				typeName = "slice"
			}
			if typeName != "" {
				key := typeName + "." + fa.Field
				if mangled, ok := c.info.Methods[key]; ok && (c.methodVisibleHere(mangled) || c.methodImplementsTrait(typeName, fa.Field)) {
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
					// Slice mirrors Array: single-element type
					// param flows through TypeArgs.
					if sl, ok := tt.(ast.SliceType); ok {
						n.TypeArgs = []ast.Type{sl.Elem}
					}
					// Wide-V Map: `m.values()` is intercepted by
					// the IR (emitMapValues) which dispatches by
					// V stride — narrow V routes to the existing
					// `__map_values_impl` stdlib function,
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
				c.errfCode(n.P, "E038", "calling non-function value of type %s", callee)
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
				} else if typeName == "slice" {
					// `slice` mirrors `Array` — builtin
					// `[T]` type-constructor with one
					// synthetic type parameter.
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
			c.errfCode(n.P, "E004", "function expects %d arguments, got %d", len(ft.Params), len(n.Args))
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
					// Too MANY type args is always wrong. Too FEW is an
					// error for an explicit call-site `f[i32](x)` (the
					// `type-arg-too-few` contract), but ALLOWED for a
					// method call (`n.Method != nil`): a generic-method
					// call seeds only the receiver's type-vars here (they
					// come first in fn.TypeParams) and the remaining
					// method-level params are inferred from the arguments
					// below. The "could not infer" check afterwards still
					// catches a param that nothing binds.
					tooMany := len(n.TypeArgs) > len(fn.TypeParams)
					tooFew := len(n.TypeArgs) < len(fn.TypeParams)
					if tooMany || (tooFew && n.Method == nil) {
						c.errfCode(n.P, "E040", "%s expects %d type argument(s), got %d",
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
		// The callee's per-parameter `own` flags (empty when the callee is
		// not a named function or declares none) — an `own` param consumes
		// its argument, which disables argAssignable's str-view borrow.
		var calleeOwnFlags []bool
		if cid, ok := n.Callee.(*ast.Ident); ok {
			calleeOwnFlags = c.info.OwnFuncs[cid.Name]
		}
		for i := range n.Args {
			if i < len(ft.Params) {
				c.setElemHintFor(n.Args[i], ft.Params[i])
			}
			at := c.checkExpr(n.Args[i], s)
			c.elemHint = nil
			if i < len(ft.Params) && at != nil {
				expected := ft.Params[i]
				// If the expected param is a bare type parameter that an
				// earlier argument already bound to a concrete numeric /
				// float type (e.g. `assert_eq[T](a + b, 8000000000)` where
				// `a + b` fixed T = i64), settle this arg's polymorphic
				// numeric literal against that bound type. Without this the
				// literal keeps its i32 default and inference reports
				// "expected T, got i32" for a value that should be i64.
				if pt, ok := expected.(ast.ParamType); ok && sub != nil {
					if bound, isBound := sub[pt.Name]; isBound {
						switch bound.(type) {
						case ast.NumberType, ast.FloatType:
							c.settleNumeric(n.Args[i], bound)
							at = c.postSettleType(n.Args[i], at)
						}
					}
				}
				// Polymorphic-literal settling: `f(1)` where f
				// expects i64 needs the literal to lock in i64
				// before assignable / unifyType run, otherwise
				// the i32-default would mismatch the expected
				// param type.
				//
				// Skip it when the expected param is parametric
				// (a generic `T` / `T[]`): settling an array literal
				// against `T[]` would stamp the literal's ElemType to
				// the bare ParamType, which then makes inference
				// circular (`unifyType(T[], T[])` binds nothing) and
				// leaves a `[Box{…}]` argument with a ParamType
				// element type — codegen then picks the wrong store
				// width and corrupts pointer-/two-word-element arrays.
				// Leaving the argument at its natural element type
				// (`Box[]`) lets unifyType bind `T = Box`.
				if !(sub != nil && containsParamType(expected)) {
					c.settleNumeric(n.Args[i], expected)
					at = c.postSettleType(n.Args[i], at)
				}
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
						c.errfCode(n.Args[i].Pos(), "E038", "argument %d: expected %s, got %s", i+1, expected, at)
					}
				} else if !c.argAssignable(expected, at, i < len(calleeOwnFlags) && calleeOwnFlags[i]) {
					c.errfCode(n.Args[i].Pos(), "E038", "argument %d: expected %s, got %s", i+1, expected, at)
				}
			}
		}
		if genericFn != nil {
			// Return-position inference (#2668): if the arguments
			// didn't pin every type parameter, fold in the call's
			// destination type. `var s: Set[i32] = set_new();` and
			// `return set_new();` leave T unbound from the (empty)
			// args, but the `Set[i32]` / declared-return type unifies
			// against the function's result `Set[T]` to bind T = i32.
			// Only the still-unbound params are filled; argument-driven
			// bindings already in `sub` win.
			if callExpected != nil && sub != nil {
				c.unifyType(ft.Result, callExpected, sub)
			}
			tpSet := make(map[string]bool, len(genericFn.TypeParams))
			for _, tp := range genericFn.TypeParams {
				tpSet[tp] = true
			}
			// Bound-driven inference (#2691): a type parameter that
			// appears ONLY inside another parameter's generic-trait
			// bound — `count[T, I: Iterator[T]](it: I)` — is never
			// pinned by an argument or the result; it is determined by
			// the *impl* the bound resolves to. For each bound param `I`
			// already pinned to a concrete type, unify the bound's trait
			// args (which mention `T`) against `I`'s impl's trait args
			// (concrete) so `T` binds. A small fixpoint loop lets one
			// bound param feed another. See docs/TRAITS.md §4a.
			if sub != nil {
				for changed := true; changed; {
					changed = false
					for _, tp := range genericFn.TypeParams {
						bt, ok := sub[tp]
						if !ok {
							continue
						}
						if _, isParam := bt.(ast.ParamType); isParam {
							continue
						}
						tn, okTn := methodTypeName(bt)
						if !okTn {
							continue
						}
						for bi, traitName := range genericFn.Bounds[tp] {
							ba := genericFn.BoundArgs[tp]
							if bi >= len(ba) || len(ba[bi]) == 0 {
								continue
							}
							implArgs := c.implTraitArgsFor(traitName, tn, bt)
							if len(implArgs) != len(ba[bi]) {
								continue
							}
							for k := range ba[bi] {
								if bindBoundParam(ba[bi][k], implArgs[k], tpSet, sub) {
									changed = true
								}
							}
						}
					}
				}
			}
			// Substitute the inferred sub through the result so
			// callers see a concrete type, AND record TypeArgs in
			// declaration order for the monomorphiser.
			args := make([]ast.Type, len(genericFn.TypeParams))
			complete := true
			for i, tp := range genericFn.TypeParams {
				if v, ok := sub[tp]; ok {
					// A type parameter pinned ONLY by a bare polymorphic
					// float literal (`snd(3.5, 4.5)` — T appears in no
					// destination/result position that would settle it)
					// stays FloatType{Polymorphic}; settle it to its
					// natural f64 default before recording the instantiation
					// arg, or the monomorphiser keys it as i32 and the clone
					// takes i32 params (re-check then fails "expected i32,
					// got f64"). Integer-polymorphic args already default to
					// i32 downstream; this is the float mirror.
					if ft, isF := v.(ast.FloatType); isF && ft.Polymorphic {
						v = ast.FloatType{Width: 64}
					}
					args[i] = v
				} else {
					c.errfCode(n.P, "E040", "could not infer type parameter %s for %s — explicit type args are not supported yet", tp, genericFn.Name)
					complete = false
				}
			}
			if complete {
				// Trait-bound satisfaction: every concrete type
				// argument must implement the traits its type
				// parameter is bound by. A still-parametric arg
				// (generic-into-generic) is left for the eventual
				// monomorphic call. See docs/TRAITS.md §5.
				for i, tp := range genericFn.TypeParams {
					if _, isParam := args[i].(ast.ParamType); isParam {
						continue
					}
					for bi, traitName := range genericFn.Bounds[tp] {
						tn, ok := methodTypeName(args[i])
						// Render `__method_Box_to_string` as the user-facing
						// `Box.to_string` when the generic decl is a hoisted
						// receiver method.
						site := demangle(genericFn.Name)
						if genericFn.MethodRecv != "" {
							site = demangle(genericFn.MethodRecv) + "." + genericFn.MethodSimpleName
						}
						if !ok || !c.info.Impls[traitName][tn] {
							c.errfCode(n.P, "E021",
								"type argument %s = %s does not implement trait %s required by %s",
								tp, demangle(args[i].String()), demangle(traitName), site)
							continue
						}
						// Generic-trait bound (`T: From[i32]`): the impl's
						// trait args must match the bound's, not merely exist.
						var boundArgs []ast.Type
						if ba := genericFn.BoundArgs[tp]; bi < len(ba) {
							boundArgs = ba[bi]
						}
						if len(boundArgs) > 0 {
							// A bound arg may name a type param the call
							// inferred (`I: Iterator[T]` with T pinned from the
							// impl) — resolve those before comparing so the
							// impl's concrete args match. See #2691.
							resolved := make([]ast.Type, len(boundArgs))
							for k, baT := range boundArgs {
								resolved[k] = substBoundArg(baT, tpSet, sub)
							}
							boundArgs = resolved
							implArgs := c.implTraitArgsFor(traitName, tn, args[i])
							if !typeArgsEqual(implArgs, boundArgs) {
								c.errfCode(n.P, "E021",
									"type argument %s = %s implements %s%s but the bound requires %s%s (in %s)",
									tp, demangle(args[i].String()), demangle(traitName), traitArgsStr(implArgs),
									demangle(traitName), traitArgsStr(boundArgs), site)
							}
						}
					}
				}
				n.TypeArgs = args
				// Resolve any associated-type projection the result picked up
				// once the type args made its base concrete (`I::Item` →
				// `IntBox::Item` → the binding). See docs/ASSOCIATED-TYPES.md.
				return c.resolveProj(substituteType(ft.Result, sub))
			}
			return nil
		}
		return ft.Result
	case *ast.Binary:
		lt := c.checkExpr(n.Left, s)
		rt := c.checkExpr(n.Right, s)
		// An operand already failed to type (its own diagnostic is
		// reported, and its type is nil). Don't pile on a cascading
		// operator-type error — which would additionally format the nil
		// type into the message as the garbage `%!s(<nil>)`. Returning
		// nil propagates the already-errored sentinel so the surrounding
		// context (return, assignment, …) doesn't re-cascade either.
		if lt == nil || rt == nil {
			return nil
		}
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
				rt = c.postSettleType(n.Right, rt)
			}
		}
		if ft, ok := rt.(ast.FloatType); ok && !ft.Polymorphic {
			if ln, ok := lt.(ast.NumberType); ok && ln.Polymorphic {
				c.settleNumeric(n.Left, ft)
				lt = c.postSettleType(n.Left, lt)
			}
		}
		switch n.Op {
		case "+":
			// Special case: string + string is concatenation. A `str` view
			// operand concats too (#4813) — concat READS both operands and
			// produces a fresh OWNED string, so borrowing views in is safe
			// and the result is a plain `string`.
			isStrOrString := func(t ast.Type) bool {
				switch t.(type) {
				case ast.StringType, ast.StrType:
					return true
				}
				return false
			}
			if isStrOrString(lt) && isStrOrString(rt) {
				n.IsStringConcat = true
				return ast.StringType{}
			}
			fallthrough
		case "-", "*", "/":
			// Composite-type arithmetic operator overloading (`+`→add,
			// `-`→sub, `*`→mul, `/`→div). See compositeOpOverload / #2706.
			if rtt, handled := c.compositeOpOverload(n, lt, rt, s); handled {
				return rtt
			}
			if isFloat(lt) || isFloat(rt) {
				c.requireFloat(n.P, lt, n.Op)
				c.requireFloat(n.P, rt, n.Op)
				n.IsFloat = true
				common, ok := commonFloatWidth(lt, rt)
				if !ok {
					// Only the both-are-floats-but-different-width case needs
					// this hint; if an operand wasn't a float, requireFloat
					// above already reported it, so suppress the redundant
					// follow-on rather than stacking two E009s on one typo.
					if isFloat(lt) && isFloat(rt) {
						c.errfCode(n.P, "E009", "operator %q requires both operands to share a float type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
					}
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
				// Only the both-are-integers-but-different-width/signedness
				// case needs this hint; if an operand wasn't an integer,
				// requireInteger above already reported it, so suppress the
				// redundant follow-on rather than stacking two E009s on one
				// typo (`i32 - "x"`).
				if isInteger(lt) && isInteger(rt) {
					c.errfCode(n.P, "E009", "operator %q requires both operands to share an integer type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
				}
				return ast.NumberType{}
			}
			c.settleNumeric(n.Left, common)
			c.settleNumeric(n.Right, common)
			// Auto-widen the narrower operand when the two
			// resolved sides differ in width (same signedness
			// already enforced by commonIntegerWidth). This
			// keeps pointer-arithmetic-style code in the
			// stdlib — `buf64 + 16` where `buf64` is i64 and
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
			// Composite-type operator overloading (`%`→rem, `&`→bitand,
			// `|`→bitor, `^`→bitxor, `<<`→shl, `>>`→shr). See #2706.
			if rtt, handled := c.compositeOpOverload(n, lt, rt, s); handled {
				return rtt
			}
			c.requireInteger(n.P, lt, n.Op)
			c.requireInteger(n.P, rt, n.Op)
			common, ok := commonIntegerWidth(lt, rt)
			if !ok {
				// Only the both-are-integers-but-different-width/signedness
				// case needs this hint; if an operand wasn't an integer,
				// requireInteger above already reported it, so suppress the
				// redundant follow-on rather than stacking two E009s on one
				// typo (`i32 - "x"`).
				if isInteger(lt) && isInteger(rt) {
					c.errfCode(n.P, "E009", "operator %q requires both operands to share an integer type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
				}
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
			// Composite-type ordering. `<` / `<=` / `>` / `>=` on a
			// struct or enum desugars to the type's `Ord` impl —
			// `a.cmp(b) <op> 0` (cmp returns -1/0/1). Without this the
			// operator hit requireInteger and errored. Arrays / slices
			// / tuples have no Ord yet.
			if lt != nil && rt != nil && ast.Equal(lt, rt) {
				switch lt.(type) {
				case ast.StructType, ast.EnumType:
					tn, _ := methodTypeName(lt)
					if mangled, ok := c.info.Methods[tn+".cmp"]; ok && c.methodVisibleHere(mangled) {
						cmpCall := &ast.Call{Callee: &ast.FieldAccess{Target: n.Left, Field: "cmp"}, Args: []ast.Expr{n.Right}}
						if rt2 := c.checkExpr(cmpCall, s); rt2 != nil {
							if nt, isNum := rt2.(ast.NumberType); !isNum || nt.NormalWidth() != 32 {
								c.errfCode(n.P, "E041", "cannot order values of type %s with %q: its `cmp` method must return i32", lt, n.Op)
							}
						}
						n.CmpCall = cmpCall
						return ast.BoolType{}
					}
					c.errfCode(n.P, "E041", "cannot order values of type %s with %q: type does not implement `Ord` — add `@derive(Ord)` (or `impl Ord for %s`) so ordering can use structural comparison", lt, n.Op, tn)
					return ast.BoolType{}
				case ast.ArrayType, ast.SliceType, ast.TupleType:
					c.errfCode(n.P, "E041", "cannot order values of type %s with %q: structural ordering for arrays / slices / tuples is not supported", lt, n.Op)
					return ast.BoolType{}
				}
			}
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
				c.errfCode(n.P, "E009", "operator %q requires both operands to share an integer type; got %s and %s — use `as` for explicit conversion", n.Op, lt, rt)
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
				lt = c.postSettleType(n.Left, lt)
				rt = c.postSettleType(n.Right, rt)
			} else if common, common_ok := commonFloatWidth(lt, rt); common_ok && !common.Polymorphic {
				c.settleNumeric(n.Left, common)
				c.settleNumeric(n.Right, common)
				lt = c.postSettleType(n.Left, lt)
				rt = c.postSettleType(n.Right, rt)
			}
			// A `str` view compares freely with `string` (and other
			// views) — comparison READS both operands' bytes (#4813);
			// after erasure both sides are the same string comparison.
			strLike := func(t ast.Type) bool {
				switch t.(type) {
				case ast.StringType, ast.StrType:
					return true
				}
				return false
			}
			if lt != nil && rt != nil && !ast.Equal(lt, rt) && !(strLike(lt) && strLike(rt)) {
				c.errfCode(n.P, "E041", "cannot compare %s and %s", lt, rt)
			}
			// Composite-type equality. `==` / `!=` on a struct or
			// enum is STRUCTURAL equality via the type's `Eq` impl —
			// it desugars to `a.eq(b)` (and `!a.eq(b)` for `!=`), not
			// heap-pointer identity. Without this, the operator
			// silently lowered to `i32.eq` on the two pointers, so
			// structurally-equal values compared unequal. Scalars /
			// strings / bools keep their fast native compare below;
			// arrays / slices / tuples have no structural eq yet.
			if lt != nil && rt != nil && ast.Equal(lt, rt) {
				switch lt.(type) {
				case ast.StructType, ast.EnumType:
					tn, _ := methodTypeName(lt)
					if mangled, ok := c.info.Methods[tn+".eq"]; ok && c.methodVisibleHere(mangled) {
						eqCall := &ast.Call{Callee: &ast.FieldAccess{Target: n.Left, Field: "eq"}, Args: []ast.Expr{n.Right}}
						if rt2 := c.checkExpr(eqCall, s); rt2 != nil {
							if _, isBool := rt2.(ast.BoolType); !isBool {
								c.errfCode(n.P, "E041", "cannot compare values of type %s with %q: its `eq` method must return boolean", lt, n.Op)
							}
						}
						n.EqCall = eqCall
						n.EqNegate = n.Op == "!="
						return ast.BoolType{}
					}
					c.errfCode(n.P, "E041", "cannot compare values of type %s with %q: type does not implement `Eq` — add `@derive(Eq)` (or `impl Eq for %s`) so `==` can use structural equality", lt, n.Op, tn)
					return ast.BoolType{}
				case ast.ArrayType, ast.SliceType, ast.TupleType:
					c.errfCode(n.P, "E041", "cannot compare values of type %s with %q: structural equality for arrays / slices / tuples is not supported — compare elements individually", lt, n.Op)
					return ast.BoolType{}
				}
			}
			// String-vs-string equality compares contents; flag so
			// codegen lowers to a runtime call rather than i32.eq. A
			// `str` view operand (#4813) compares contents the same way —
			// without the flag the backends would pointer-compare the
			// boxes and a trimmed view would never equal its literal.
			if strLike(lt) && strLike(rt) {
				n.IsStringCmp = true
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
		c.errfCode(n.P, "E041", "unknown binary operator %q", n.Op)
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
			// Unary minus over a trait-bounded TYPE PARAMETER: `-a` where
			// `a: T` and `T: Neg` desugars to `a.neg()`, resolved through the
			// bound (mirroring the binary operator-on-type-param path). See
			// #2706. A type param without a `Neg` bound falls through to the
			// numeric path's E009.
			if pt, ok := t.(ast.ParamType); ok {
				if _, _, found := c.resolveTraitMethodForParam(pt.Name, "neg"); found {
					call := &ast.Call{Callee: &ast.FieldAccess{Target: n.Operand, Field: "neg"}, Args: nil}
					rtt := c.checkExpr(call, s)
					n.NegCall = call
					return rtt
				}
			}
			// Composite-type unary minus: `-v` on a struct / enum with a
			// `neg` method desugars to `v.neg()` — operator overloading,
			// mirroring the binary `+ - * /` (add/sub/mul/div) overloads.
			// See #2706.
			switch t.(type) {
			case ast.StructType, ast.EnumType:
				tn, _ := methodTypeName(t)
				if mangled, ok := c.info.Methods[tn+".neg"]; ok && c.methodVisibleHere(mangled) {
					call := &ast.Call{Callee: &ast.FieldAccess{Target: n.Operand, Field: "neg"}, Args: nil}
					rtt := c.checkExpr(call, s)
					n.NegCall = call
					return rtt
				}
				c.errfCode(n.P, "E009", "unary `-` is not defined for %s — implement `function (self: %s) neg(): %s` to overload it", t, tn, tn)
				return t
			}
			// Unary minus applies to any integer width, not just
			// i32 — `-5i64`, `-x` on an i64, etc. requireNumber
			// only accepted the bare i32 NumberType, so negating
			// any wider/narrower integer was wrongly rejected.
			c.requireInteger(n.P, t, n.Op)
			// Propagate the operand's NumberType (including its
			// Polymorphic flag) so unary minus on a polymorphic
			// literal stays polymorphic; otherwise `var s: i64 =
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
			rt = c.postSettleType(n.Value, rt)
			rt = c.maybeWrapForUnion(lt, &n.Value, rt, s)
		}
		if lt != nil && rt != nil && !ast.Equal(lt, rt) && !c.assignable(lt, rt) {
			c.errfCode(n.P, "E003", "cannot assign %s to %s", rt, lt)
		}
		// Fields are immutable after construction: a struct value
		// can't have a field reassigned in place. This is the
		// enforcement half of the immutable-data-structures
		// migration (docs/IMMUTABILITY-MIGRATION-PLAN.md §4) — with
		// no post-construction mutation, reference cycles become
		// unconstructible, so RC stays garbage-free with no cycle
		// collector. The fix is a functional struct-update:
		// `p = Foo { ...p, field: v }`. (Local variable reassignment
		// stays legal; only `*ast.FieldAccess` targets are banned.)
		if fa, ok := n.Target.(*ast.FieldAccess); ok {
			c.errfCode(fa.Pos(), "E048",
				"cannot assign to field %q: fields are immutable after construction; rebuild with `T { ...old, %s: value }`",
				fa.Field, fa.Field)
		}
		// Array elements are immutable after construction too (E056) —
		// the subscript counterpart of E048, completing the immutable-
		// data-structures surface (docs/PURE-COLLECTION-API-PLAN.md §3a).
		// `arr[i] = v` becomes the value-returning `arr = arr.with(i, v)`,
		// which is the CoW unique-in-place branch on an unowned/`fip`
		// array (allocation-free — see E053's `.with`-on-`own` rule), so
		// subscripts are read-only just like struct fields.
		if idx, ok := n.Target.(*ast.Index); ok {
			c.errfCode(idx.Pos(), "E056",
				"cannot assign to an array element: subscripts are read-only after construction; use `arr = arr.with(i, value)`")
		}
		// A closure may not write back a REFERENCE-shaped captured
		// variable. This is the other half of immutability
		// enforcement (E048 bans field mutation; this bans
		// pointer-capture write-back), closing the remaining
		// reference-cycle vector: a closure whose env holds a
		// pointer could be made to point back at a value that points
		// at the closure. Scalar captures (i32 / i64 / f32 / f64)
		// can't hold a reference, so writing them can't form a cycle
		// — the stateful "counter closure" stays legal. Pointer-
		// shaped captures (string / array / struct / enum / slice /
		// tuple / closure, per ast.IsPointerType) are rejected;
		// thread the new value out of the closure (return it).
		if id, ok := n.Target.(*ast.Ident); ok {
			if ct, isCap := c.capturedType(id.Name, s); isCap && ast.IsPointerType(ct) {
				c.errfCode(id.Pos(), "E049",
					"cannot assign to captured %s %q: a reference-typed closure capture is read-only (it could close a reference cycle); return the new value from the closure instead",
					ct, id.Name)
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
				c.resolveType(&n.Params[i].Type, lambdaParams, orPos(n.Params[i].NamePos, n.P))
			}
			c.resolveType(&n.ReturnType, lambdaParams, n.P)
		}
		root := newScope(nil)
		for _, p := range n.Params {
			if _, dup := root.names[p.Name]; dup {
				c.errfCode(n.P, "E018", "duplicate parameter %q", p.Name)
			}
			root.names[p.Name] = p.Type
		}
		captured := map[string]ast.Type{}
		var captureOrder []string
		prev := c.current
		prevSink := c.captureSink
		prevOuter := c.captureOuter
		prevLoop := c.loopDepth
		// Stash the synthetic FuncDecl on the Lambda so
		// closureconv can recover the Var statements registered
		// against it during body-checking. Without this, the
		// hoisted FuncDecl that closureconv synthesises has no
		// entry in `info.Locals`, and `lowerFunc` panics with
		// "var X has no slot" when it tries to allocate slots
		// for body-local vars.
		synth := &ast.FuncDecl{
			P:                 n.P,
			Params:            n.Params,
			ReturnType:        n.ReturnType,
			ReturnUnannotated: n.ReturnUnannotated,
			Body:              n.Body,
		}
		n.Synthetic = synth
		c.current = synth
		// An unannotated arrow lambda (`(x) => expr`) infers its return type
		// from the body, reusing the same inferReturns channel as an
		// unannotated function. Point it at a lambda-local slice (so the
		// returns don't leak into an enclosing unannotated function) and
		// unify after the body check. An explicit-return lambda keeps the
		// outer inferReturns untouched — its synth.ReturnUnannotated is
		// false, so the return path validates against ReturnType as before.
		prevInfer := c.inferReturns
		var lamRets []ast.Type
		if n.ReturnUnannotated {
			c.inferReturns = &lamRets
		}
		c.loopDepth = 0
		c.captureSink = func(name string, t ast.Type) {
			if _, ok := captured[name]; ok {
				return
			}
			switch t.(type) {
			case ast.VoidType, ast.ParamType:
				c.errfCode(n.P, "E044", "captured variable %q has unsupported type %s", name, t)
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
		c.inferReturns = prevInfer
		if n.ReturnUnannotated {
			// Unify the body's return(s) into the lambda's return type
			// (synth.ReturnType is updated in place; mirror it onto n).
			c.inferReturnType(synth, lamRets)
			n.ReturnType = synth.ReturnType
		}
		c.current = prev
		// Fresh list, not append — see the matching note in checkFunc;
		// re-analysis would otherwise duplicate captures.
		n.Captures = nil
		for _, name := range captureOrder {
			n.Captures = append(n.Captures, ast.Param{Name: name, Type: captured[name]})
		}
		ft := &ast.FuncType{Result: n.ReturnType}
		for _, p := range n.Params {
			ft.Params = append(ft.Params, p.Type)
		}
		return ft
	case *ast.BlockExpr:
		return c.checkBlockExpr(n, s)
	case *ast.IfExpr:
		ct := c.checkExpr(n.Cond, s)
		if ct != nil && !ast.Equal(ct, ast.BoolType{}) {
			c.errfCode(n.Cond.Pos(), "E008", "if-expression condition must be boolean, got %s", ct)
		}
		tt := c.checkExpr(n.Then, s)
		et := c.checkExpr(n.Else, s)
		result := unifyIfArms(tt, et)
		if tt != nil && et != nil && result == nil {
			c.errfCode(n.P, "E031", "if-expression branches differ: %s vs %s", tt, et)
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
			c.errfCode(n.P, "E042", "`?` operator can only be used inside a function")
			return nil
		}
		ret := c.current.ReturnType
		retEnum, retOK := ret.(ast.EnumType)
		srcEnum, srcOK := inner.(ast.EnumType)
		if !srcOK {
			c.errfCode(n.P, "E042", "`?` operator requires an Option or Result value, got %s", inner)
			return nil
		}
		switch srcEnum.Name {
		case "Option":
			if len(srcEnum.Args) != 1 {
				c.errfCode(n.P, "E042", "malformed Option type %s", inner)
				return nil
			}
			if !retOK || retEnum.Name != "Option" || len(retEnum.Args) != 1 {
				c.errfCode(n.P, "E042", "`?` on Option requires the surrounding function to return Option[_], got %s", ret)
				return nil
			}
			n.Kind = ast.TryKindOption
			n.Type = srcEnum.Args[0]
			return n.Type
		case "Result":
			if len(srcEnum.Args) != 2 {
				c.errfCode(n.P, "E042", "malformed Result type %s", inner)
				return nil
			}
			if !retOK || retEnum.Name != "Result" || len(retEnum.Args) != 2 {
				c.errfCode(n.P, "E042", "`?` on Result requires the surrounding function to return Result[_, E], got %s", ret)
				return nil
			}
			if !ast.Equal(srcEnum.Args[1], retEnum.Args[1]) {
				// Error-converting `?`: a `Result[_, E]` propagated through a
				// function returning `Result[_, dyn Trait]` boxes the concrete
				// error `E` into `dyn Trait`, provided E implements Trait
				// (the `Box<dyn Error>` + `?` idiom). Desugar to a block-expr
				// that maps the error then applies an ordinary `?`. See #3234.
				if lowered, ok := c.tryConvertErrToDyn(n, srcEnum, retEnum, s); ok {
					n.Kind = ast.TryKindResult
					n.Type = srcEnum.Args[0]
					n.Lowered = lowered
					return n.Type
				}
				// Or convert via a `from` constructor: if the function's
				// error type `E2` has an associated `from(E1): E2` (e.g.
				// `impl From[E1] for E2`), `?` maps `Err(e)` to
				// `Err(E2.from(e))` — the `From`-based `?` idiom. See #2674.
				if lowered, ok := c.tryConvertErrViaFrom(n, srcEnum, retEnum, s); ok {
					n.Kind = ast.TryKindResult
					n.Type = srcEnum.Args[0]
					n.Lowered = lowered
					return n.Type
				}
				c.errfCode(n.P, "E042", "`?` on Result[_, %s] but the surrounding function returns Result[_, %s]; the error types must match (implement %s for a `dyn`-error, or a `from(%s)` constructor on %s for the conversion)",
					srcEnum.Args[1], retEnum.Args[1], srcEnum.Args[1], srcEnum.Args[1], retEnum.Args[1])
				return nil
			}
			n.Kind = ast.TryKindResult
			n.Type = srcEnum.Args[0]
			return n.Type
		default:
			c.errfCode(n.P, "E042", "`?` operator requires an Option or Result value, got %s", inner)
			return nil
		}
	case *ast.StructLit:
		sd, ok := c.info.Structs[n.TypeName]
		if !ok {
			c.errfCode(n.P, "E043", "unknown struct type %q", n.TypeName)
			return nil
		}
		c.checkOpaqueAccess(sd, n.P, "construct")
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
			// Seed the type-arg substitution from an explicit destination
			// type (`var b: Box[i32] = Box { v: … }`) so each field is
			// checked against the concrete instantiation instead of a free
			// parameter. Without this a field mismatch (`v: "x"` for a
			// Box[i32]) unifies the parameter to the wrong type, slips past
			// this check, and only surfaces at monomorph re-check as a
			// confusing "compiler bug" — the verdict monomorph already
			// reaches, just earlier and as a proper E043.
			if et, ok := c.expectedType.(ast.StructType); ok && et.Name == sd.Name && len(et.Args) == len(sd.TypeParams) {
				for i, tp := range sd.TypeParams {
					sub[tp] = et.Args[i]
				}
			}
		}
		// Struct-update literal `Foo { ...base, field: v }`: the base
		// must have this struct's type, and supplies every field the
		// overrides don't — so the completeness check below is relaxed.
		// For a generic struct the base fixes the instantiation, so
		// seed the type-arg substitution from the base's Args.
		var baseArgs []ast.Type
		if n.Base != nil {
			bt := c.checkExpr(n.Base, s)
			if bt != nil {
				bst, ok := bt.(ast.StructType)
				if !ok || bst.Name != sd.Name {
					c.errfCode(n.Base.Pos(), "E003", "struct-update base must be %s, got %s", sd.Name, bt)
				} else {
					baseArgs = bst.Args
					if sub != nil {
						for i, tp := range sd.TypeParams {
							if i < len(bst.Args) {
								sub[tp] = bst.Args[i]
							}
						}
					}
				}
			}
		}
		for i := range n.Fields {
			f := n.Fields[i]
			expected, present := fieldT[f.Name]
			if !present {
				declared := make([]string, 0, len(sd.Fields))
				for _, df := range sd.Fields {
					declared = append(declared, df.Name)
				}
				c.errUnknownField(n.P, f.NamePos, sd.Name, f.Name, declared)
				continue
			}
			if seen[f.Name] {
				c.errfCode(n.P, "E007", "duplicate field %q in struct literal", f.Name)
			}
			seen[f.Name] = true
			// Propagate the field's element type into a direct array-literal
			// value so its elements coerce to the field's element type — the
			// same hint the Var / Return / call-argument positions set. Without
			// it, `Wrap { items: [Leaf{...}] }` (field type `Node[]`, an enum
			// array) leaves each inline variant literal unwrapped: it lowers as a
			// bare struct with no variant tag, and reads back as the wrong
			// variant. maybeWrapForUnion below only widens a DIRECT variant field
			// value, not the elements of an array field.
			// When the instantiation is known (sub seeded from the
			// destination, e.g. `Box[i64]`), drive hints / literal-settling
			// with the SUBSTITUTED field type (`i64`) rather than the bare
			// parameter (`T`): a polymorphic literal `5` must settle to the
			// concrete `i64`, not default to i32 and then mismatch the seeded
			// arg.
			fieldExpected := substituteType(expected, sub)
			c.setElemHintFor(f.Value, fieldExpected)
			// Scope the expected-type to THIS field's destination while
			// checking its value, then restore it. Without this, the enclosing
			// literal's destination leaks in: a nested generic struct literal —
			// `Box { v: Box { v: 42 } }` for a `Box[Box[i32]]` target — would
			// seed the inner literal's type args from the OUTER `Box[Box[i32]]`
			// instead of its own field type `Box[i32]`, mis-typing it.
			savedExpected := c.expectedType
			c.expectedType = fieldExpected
			vt := c.checkExpr(f.Value, s)
			c.expectedType = savedExpected
			if vt == nil {
				continue
			}
			c.settleNumeric(f.Value, fieldExpected)
			vt = c.postSettleType(f.Value, vt)
			// Implicit union-wrap: a bare variant struct literal in a
			// field position widens to its union type, matching the
			// `var x: Union = Variant{...}`, return, and call-argument
			// behaviour. Mutates the real AST slot (Fields are values),
			// so index rather than the loop copy.
			vt = c.maybeWrapForUnion(expected, &n.Fields[i].Value, vt, s)
			if sub != nil {
				if !c.unifyType(expected, vt, sub) {
					// Show the substituted field type (`i32`) rather than the
					// bare parameter (`T`) when the instantiation is known —
					// e.g. seeded from a `Box[i32]` destination.
					c.errfCode(f.Value.Pos(), "E043", "field %q: expected %s, got %s", f.Name, substituteType(expected, sub), vt)
				}
			} else if !ast.Equal(vt, expected) {
				// Allow the polymorphic / argless-enum vs
				// concrete widening rules from `unifyIfArms`
				// — e.g. `struct Node { next: Option[Node] }`
				// initialised with `next: None` (which checks
				// to `Option` with empty Args). Same shape as
				// the array-element widening from #541.
				if unifyIfArms(expected, vt) == nil {
					c.errfCode(f.Value.Pos(), "E043", "field %q: expected %s, got %s", f.Name, expected, vt)
				}
			}
		}
		// A plain literal must name every field. A struct-update
		// literal (Base != nil) copies the un-named fields from the
		// base, so the completeness requirement is relaxed.
		if n.Base == nil {
			for _, f := range sd.Fields {
				if !seen[f.Name] {
					c.errfCode(n.P, "E005", "struct literal missing field %q", f.Name)
				}
			}
		}
		if len(sd.TypeParams) > 0 {
			args := make([]ast.Type, len(sd.TypeParams))
			complete := true
			for i, tp := range sd.TypeParams {
				if v, ok := sub[tp]; ok {
					args[i] = v
				} else if n.Base != nil && i < len(baseArgs) {
					// The base fixes the instantiation for any
					// type param the (subset) overrides didn't touch.
					args[i] = baseArgs[i]
				} else {
					c.errfCode(n.P, "E040", "could not infer type parameter %s for struct %s — explicit type args are not supported yet", tp, sd.Name)
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
		c.needCoreMap(n.P)
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
			kt = c.postSettleType(n.Entries[0].Key, kt)
			vt = c.postSettleType(n.Entries[0].Value, vt)
			if kt != nil {
				keyType = kt
			}
			if vt != nil {
				valueType = vt
			}
			// Usable keys: i32-sized scalar, string, or a
			// struct/enum that derives Eq + Hash (the keyed runtime
			// dispatches through its hash/eq — #2671). Everything
			// else (i64/float, tuple, array, slice, or an
			// underived struct/enum) is rejected with a message
			// pointing at the fix.
			if msg := c.mapKeyTypeError(keyType); msg != "" {
				c.errfCode(n.Entries[0].Key.Pos(), "E045", "%s", msg)
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
			kt := c.postSettleType(ent.Key, keyType)
			vt := c.postSettleType(ent.Value, valueType)
			if kt != nil && !ast.Equal(kt, keyType) {
				c.errfCode(ent.Key.Pos(), "E045", "map key type %s, expected %s", kt, keyType)
			}
			if vt != nil && !ast.Equal(vt, valueType) {
				c.errfCode(ent.Value.Pos(), "E045", "map value type %s, expected %s", vt, valueType)
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
						c.errfCode(n.P, "E036", "variant %s.%s expects %d payload argument(s); call it as %s.%s(...)",
							tid.Name, n.Field, len(vr.payloads), tid.Name, n.Field)
						return nil
					}
					return ast.EnumType{Name: vr.enumName}
				}
				c.errfCode(n.P, "E036", "enum %s has no variant %q", tid.Name, n.Field)
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
				c.errfCode(n.P, "E046", "tuple field access requires a numeric index, got %q", n.Field)
				return nil
			}
			if idx >= len(tup.Elems) {
				c.errfCode(n.P, "E046", "tuple has %d elements; index %d is out of range", len(tup.Elems), idx)
				return nil
			}
			return tup.Elems[idx]
		}
		st, ok := tt.(ast.StructType)
		if !ok {
			if tt != nil {
				c.errfCode(n.P, "E043", "field access on non-struct value of type %s", tt)
			}
			return nil
		}
		sd := c.info.Structs[st.Name]
		if sd == nil {
			c.errfCode(n.P, "E043", "unknown struct type %q", st.Name)
			return nil
		}
		c.checkOpaqueAccess(sd, n.P, "access a field of")
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
		declared := make([]string, 0, len(sd.Fields))
		for _, df := range sd.Fields {
			declared = append(declared, df.Name)
		}
		// A misspelt METHOD call also lands here (`p.puzh(2)` — method
		// resolution ran first and missed), so the struct's registered
		// method names join the near-miss candidates: the fix then
		// suggests `push` where the field set alone offers nothing.
		// Sorted so map-iteration order can't flip a distance tie
		// between runs (diagnostics must be deterministic).
		prefix := st.Name + "."
		var methodNames []string
		for key := range c.info.Methods {
			if strings.HasPrefix(key, prefix) {
				methodNames = append(methodNames, key[len(prefix):])
			}
		}
		sort.Strings(methodNames)
		declared = append(declared, methodNames...)
		c.errUnknownField(n.P, n.FieldPos, st.Name, n.Field, declared)
		return nil
	}
	return nil
}

// requireInteger matches any integer type — i32, i64, eventually
// the unsigned widths. Used by arithmetic checks that allow either
// width as long as both sides agree.
func (c *checker) requireInteger(p ast.Position, t ast.Type, op string) {
	if t == nil {
		return
	}
	if _, ok := t.(ast.NumberType); !ok {
		c.errfCode(p, "E009", "operator %q requires an integer type, got %s", op, t)
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
	// Map / MapIter share one struct + helper set across all (K, V) via a
	// runtime keyKind tag; Cell is IR-intercepted (a single opaque box for
	// every T). None are monomorphised — cloning would split their
	// dispatch across mangled names the lowering doesn't know about.
	return name == "Map" || name == "MapIter" || name == "Cell"
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
// isPrimitiveTypeName reports whether `name` is a built-in scalar type that
// can carry associated functions via `impl Trait for <prim>` (hoisted to
// `__assoc_<prim>_<f>`).
func isPrimitiveTypeName(name string) bool {
	switch name {
	case "i32", "i64", "u32", "u64", "f32", "f64", "string", "boolean":
		return true
	}
	return false
}

func (c *checker) refineSingleCallTypeArgs(call *ast.Call, dst ast.Type) {
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return
	}
	fn, isGen := c.info.GenericFuncs[id.Name]
	if !isGen || len(fn.TypeParams) == 0 {
		return
	}
	// A generic call with NO type args (a receiver-less associated call on a
	// generic struct — `Box.default()` rewritten to `__assoc_Box_default()` —
	// has no arguments to pin its type params): infer them entirely from the
	// destination type so the monomorphiser can instantiate a concrete clone.
	// Without this the generic `__assoc_…` body keeps its `T` and the
	// post-monomorph re-check fails with "undefined identifier T".
	if len(call.TypeArgs) == 0 {
		sub := make(map[string]ast.Type, len(fn.TypeParams))
		refineParamSubFromDest(fn.ReturnType, dst, sub)
		args := make([]ast.Type, len(fn.TypeParams))
		for i, tp := range fn.TypeParams {
			v, ok := sub[tp]
			if !ok || v == nil {
				return // couldn't infer every param — leave the call untouched
			}
			args[i] = v
		}
		call.TypeArgs = args
		return
	}
	if len(call.TypeArgs) != len(fn.TypeParams) {
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
		// A destination-typed map (`var m: Map[K, V] = map_new(8)`,
		// function arg, etc.) validates its annotated key type here so
		// a bad struct/enum key (no Eq + Hash) errors cleanly instead
		// of dangling at codegen as a missing `__method_<K>_hash`
		// (#2671). A MapLit destination is skipped — the MapLit's own
		// checkExpr validates its (possibly inferred) key type, so this
		// would double-report. Only struct/enum keys are gated; scalar
		// / string / tuple keys keep their existing treatment.
		if hn.Name == "Map" && len(hn.Args) == 2 {
			if _, isLit := e.(*ast.MapLit); !isLit {
				switch hn.Args[0].(type) {
				case ast.StructType, ast.EnumType:
					if msg := c.mapKeyTypeError(hn.Args[0]); msg != "" {
						c.errfCode(e.Pos(), "E045", "%s", msg)
					}
				}
			}
		}
		if ml, ok := e.(*ast.MapLit); ok && hn.Name == "Map" && len(hn.Args) == 2 {
			for _, ent := range ml.Entries {
				c.settleNumeric(ent.Key, hn.Args[0])
				c.settleNumeric(ent.Value, hn.Args[1])
			}
			// Empty `Map {}` with a destination annotation: stamp
			// K / V from the destination so the literal's type
			// flows the right shape into the assignable check
			// (via postSettleType) and into the IR's runtime
			// keyKind / valKind tags. Without this, `var m:
			// Map[string, i32] = Map {};` keeps the
			// checkExpr-default `Map[i32, i32]` shape and the
			// assignment rejects.
			if len(ml.Entries) == 0 {
				ml.KeyType = hn.Args[0]
				ml.ValueType = hn.Args[1]
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
	case *ast.BlockExpr:
		// Block-expression branch (`{ …; tail }`): the value is the
		// trailing expression, so settle it against the destination
		// width — `var n: i64 = if (c) { var k = 1; k } else { 0 }`.
		if x.Tail != nil {
			c.settleInt(x.Tail, hn)
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
	case *ast.BlockExpr:
		// Block-expression branch: settle the trailing value.
		if x.Tail != nil {
			c.settleFloat(x.Tail, hf)
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

func (c *checker) postSettleType(e ast.Expr, prior ast.Type) ast.Type {
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
		return c.postSettleType(x.Operand, prior)
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
				out[i] = c.postSettleType(el, tt.Elems[i])
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
			if t := c.postSettleType(x.Then, prior); t != nil {
				return t
			}
		}
		if x.Else != nil {
			if t := c.postSettleType(x.Else, prior); t != nil {
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
			if t := c.postSettleType(arm.Body, prior); t != nil {
				return t
			}
		}
	case *ast.BlockExpr:
		// Block-expression branch: the value is the trailing
		// expression, so its post-settle type is the block's type.
		if x.Tail != nil {
			if t := c.postSettleType(x.Tail, prior); t != nil {
				return t
			}
		}
	case *ast.Call:
		// Variant constructor calls (`Some(tupleLit)`,
		// `Ok(...)`) — after settleNumeric stamped widths
		// onto each constructor arg, the prior EnumType's
		// Args still reflect the pre-settle widths (the
		// type was unified before the settle pass touched
		// the literals). Recompute the enum's type arguments
		// by re-unifying each (now-settled) constructor arg
		// against its declared payload type — exactly as the
		// first checkExpr pass did, but with the widened
		// literals. Re-unifying (rather than pairing
		// `et.Args[i]` with `x.Args[i]` positionally) is the
		// only correct mapping when a type parameter is
		// determined by a payload whose position differs from
		// the parameter's index — e.g. `enum Box[T] { Mk(i32,
		// (i32) => T) }`, where `T` comes from the *second*
		// (function-typed) payload, not the leading `i32`.
		// Positional pairing there mis-bound `T` to the i32
		// literal and produced `Box[i32]` instead of
		// `Box[string]`. It also correctly refreshes one type
		// param of a multi-param generic (e.g. the `T` of
		// `Result[T, E]` from `Ok(v)`) while preserving the
		// other from the first-pass result.
		if et, ok := prior.(ast.EnumType); ok && len(et.Args) > 0 && isVariantCall(x) {
			if id, ok := x.Callee.(*ast.Ident); ok {
				if vr, isVar, _ := c.resolveVariant(id.Name, id.EnumName); isVar {
					if ed := c.info.Enums[vr.enumName]; ed != nil &&
						len(ed.TypeParams) == len(et.Args) &&
						len(x.Args) == len(vr.payloads) {
						// Seed from the first-pass result so payload
						// positions that don't pin a type param (and
						// nested shapes) keep their resolved types.
						priorSub := map[string]ast.Type{}
						for i, tp := range ed.TypeParams {
							priorSub[tp] = et.Args[i]
						}
						newSub := map[string]ast.Type{}
						for i, a := range x.Args {
							declared := vr.payloads[i]
							actual := c.postSettleType(a, substituteType(declared, priorSub))
							if actual != nil {
								c.unifyType(declared, actual, newSub)
							}
						}
						newArgs := make([]ast.Type, len(et.Args))
						complete := true
						for i, tp := range ed.TypeParams {
							if v, ok := newSub[tp]; ok {
								newArgs[i] = v
							} else {
								newArgs[i] = et.Args[i]
							}
							if newArgs[i] == nil {
								complete = false
							}
						}
						if complete {
							return ast.EnumType{Name: et.Name, Args: newArgs}
						}
					}
				}
			}
			return prior
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
			newK := c.postSettleType(ent.Key, st.Args[0])
			newV := c.postSettleType(ent.Value, st.Args[1])
			x.KeyType = newK
			x.ValueType = newV
			return ast.StructType{Name: "Map", Args: []ast.Type{newK, newV}}
		}
		// Empty `Map {}` whose K / V were stamped from the
		// destination by settleNumeric. The `prior` here is the
		// checkExpr-default `Map[i32, i32]`; surface the
		// post-settle K / V so the assignable check sees the
		// destination's shape.
		if len(x.Entries) == 0 && x.KeyType != nil && x.ValueType != nil {
			return ast.StructType{Name: "Map", Args: []ast.Type{x.KeyType, x.ValueType}}
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
// intLitExceedsI32 reports whether `e` is a bare integer literal (or a
// unary-negated one) whose value lies outside the signed i32 range. It drives
// the i64-widening default for an unannotated binding (#3676): a written-out
// constant past i32 range has no valid i32 reading, so it defaults to i64
// instead of being silently truncated. A typed-suffix literal (`42i64`) already
// carries a Width and isn't a default case; a float literal is excluded.
func intLitExceedsI32(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.NumberLit:
		if x.IsFloat || x.Width != 0 {
			return false
		}
		return x.Value < -1<<31 || x.Value > 1<<31-1
	case *ast.Unary:
		if x.Op == "-" {
			if nl, ok := x.Operand.(*ast.NumberLit); ok && !nl.IsFloat && nl.Width == 0 {
				v := -nl.Value
				return v < -1<<31 || v > 1<<31-1
			}
		}
	}
	return false
}

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
			c.errfCode(lit.P, "E047", "literal %d does not fit in %s", lit.Value, t)
		}
	} else {
		var max uint64
		switch w {
		case 8:
			max = 1<<8 - 1
		case 32:
			max = 1<<32 - 1
		case 64:
			return
		default:
			return
		}
		if lit.Value < 0 || uint64(lit.Value) > max {
			c.errfCode(lit.P, "E047", "literal %d does not fit in %s", lit.Value, t)
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
// `i64 + i32` (mixed-width pointer arithmetic in the stdlib,
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
	// auto-widens to usize so stdlib pointer math stays
	// readable. usize is unsigned and i32 is signed, so the
	// signedness check below would otherwise reject. The
	// 2's-complement representation makes the result identical
	// to what the stdlib computed before via explicit
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
		c.errfCode(p, "E009", "operator %q requires float, got %s", op, t)
	}
}
func isFloat(t ast.Type) bool {
	_, ok := t.(ast.FloatType)
	return ok
}

// isInteger reports whether t is an integer type (any width / signedness).
func isInteger(t ast.Type) bool {
	_, ok := t.(ast.NumberType)
	return ok
}

// hasHandleDecl reports whether the program defines a top-level
// `function handle(req: HttpRequest, plat: Platform): HttpResponse`
// — the Platform-parameter signature shape every wasi-http
// program targets (docs/PLATFORM-RESEARCH.md Rec §1). The check
// is purely structural: any top-level FuncDecl named `handle`
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

// hasInitDecl reports whether the program defines a top-level
// `init` function — recognised by the auto-`main`-from-`handle`
// synthesis as a one-shot startup entry that runs before the
// per-request loop (docs/PLATFORM-RESEARCH.md Rec §3).
//
// `init()` returns are currently dropped — Phase 1 plumbing
// only runs the function for its side effects (logging
// "starting", pre-warming caches via state-block writes,
// env-var reads). Phase 2 will thread the return value as a
// `state: InitState` third argument to `handle`.
func hasInitDecl(prog *ast.Program) bool {
	for _, fn := range prog.Funcs {
		if fn.Name == "init" {
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
// Loaded flat via `LoadStdlibFlat` (e.g. in tests), both
// `tcp_serve` and `__port_from_env` live at their bare names
// (no mangling). Loaded via modload with `import "std/tcp";`
// they get the `tcp__` prefix instead. We probe `prog.Funcs` for
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
	// Prepend an `init();` call when the user defined one
	// (docs/PLATFORM-RESEARCH.md Rec §3). The return value is
	// currently dropped — Phase 1 init() is side-effect-only;
	// Phase 2 will thread the result as a `state` parameter
	// to handle. `init` is the BARE name; if a module import
	// qualifies it, modload rewrites the call separately.
	var stmts []ast.Stmt
	if hasInitDecl(prog) {
		stmts = append(stmts, &ast.ExprStmt{
			P: pos,
			Expr: &ast.Call{
				P:      pos,
				Callee: &ast.Ident{P: pos, Name: "init"},
				Args:   nil,
			},
		})
	}
	stmts = append(stmts, &ast.Return{P: pos, Value: tcpServeCall})
	body := &ast.Block{Stmts: stmts}
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
		c.errfCode(p, "E009", "operator %q requires boolean, got %s", op, t)
	}
}
