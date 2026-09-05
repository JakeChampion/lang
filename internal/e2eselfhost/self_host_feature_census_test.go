package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// The self-host feature census (#6993). `examples/self_host/*.fern` is validated
// largely by compiling itself, so a language feature the self-host's own sources
// do not use gets no fixpoint coverage at all — the gate can only prove what the
// code already exercises. That makes "what does the self-host actually use?" a
// number that steers decisions: it is what docs/SELFHOST-LANGUAGE-FRICTION.md §1
// is built from, and what says whether a new e2eselfhost fixture covers a real
// gap or a hypothetical one. A measurement that steers a decision has to be
// reproducible and has to fail when the thing it measured moves.
//
// Counting has to strip first. The self-host embeds whole test programs as
// string literals and discusses its own syntax in prose comments, so a raw grep
// for `=>` or `Map[` counts the compiler talking ABOUT a construct as one that
// uses it — on the `as` row the raw and stripped counts differ by a factor of
// five, which is how three successive hand-measurements of this census
// disagreed with each other in both directions. The strip is the
// correctness-critical half: TestStripFernLiterals pins it against the cases
// that break a naive one, and the census re-proves it on the real corpus before
// counting anything.

// stripFernLiterals blanks `//` comments, string and f-string literals, and char
// and byte literals, leaving the code text the census counts over. Line
// structure is preserved, so a hit still reports a usable line number.
//
// Every Fern literal is line-bounded — a newline inside a string, f-string or
// char literal is a lex error (internal/lexer) — so the strip runs per line and
// an unterminated literal can never swallow the rest of the file.
func stripFernLiterals(src string) string {
	lines := strings.Split(src, "\n")
	for i, ln := range lines {
		lines[i] = stripFernLine(ln)
	}
	return strings.Join(lines, "\n")
}

func stripFernLine(line string) string {
	var b strings.Builder
	var last byte
	emit := func(s string) {
		b.WriteString(s)
		last = s[len(s)-1]
	}
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == '/' && i+1 < len(line) && line[i+1] == '/':
			return b.String()
		case c == '"':
			emit(`""`)
			i = skipFernString(line, i)
		case c == 'f' && i+1 < len(line) && line[i+1] == '"' && !identCont(last):
			// `f"…{expr}…"`. The interpolants are real code, but nothing
			// the census counts is ever spelled inside one, so dropping
			// them keeps the strip to a single rule: a literal contributes
			// nothing.
			emit(`""`)
			i = skipFernFString(line, i+1)
		case c == '\'':
			if end := skipFernChar(line, i); end > 0 {
				emit(`''`)
				i = end
			} else {
				emit(line[i : i+1])
				i++
			}
		default:
			emit(line[i : i+1])
			i++
		}
	}
	return b.String()
}

func identCont(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// skipFernString returns the index just past the string literal opening at i, or
// len(line) when the literal does not close on this line.
func skipFernString(line string, i int) int {
	for i++; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(line)
}

// skipFernFString is skipFernString for an f-string body, whose interpolants nest
// braces and may hold a string of their own, so the closing quote is only the one
// seen at brace depth zero.
func skipFernFString(line string, i int) int {
	depth := 0
	for i++; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '{':
			if depth == 0 && i+1 < len(line) && line[i+1] == '{' {
				i++ // `{{` is a literal brace, not an interpolant
				continue
			}
			depth++
		case '}':
			if depth == 0 && i+1 < len(line) && line[i+1] == '}' {
				i++ // `}}` is a literal brace
				continue
			}
			if depth > 0 {
				depth--
			}
		case '"':
			if depth == 0 {
				return i + 1
			}
			i = skipFernString(line, i) - 1
		}
	}
	return len(line)
}

// skipFernChar returns the index just past the char or byte literal opening at i,
// or -1 when what follows is not a literal — which is how an apostrophe that
// reaches code text stays one character instead of swallowing the line up to the
// next quote.
func skipFernChar(line string, i int) int {
	j := i + 1
	if j < len(line) && line[j] == '\\' {
		j += 2
	} else {
		_, sz := utf8.DecodeRuneInString(line[j:])
		j += sz
	}
	if j < len(line) && line[j] == '\'' {
		return j + 1
	}
	return -1
}

// strippedSource is one self-host module with its literals gone.
type strippedSource struct {
	name  string
	lines []string
}

func selfHostStripped(t *testing.T) []strippedSource {
	t.Helper()
	paths, err := filepath.Glob(langSrcAbs(t, filepath.Join("examples", "self_host", "*.fern")))
	if err != nil {
		t.Fatalf("globbing self-host sources: %v", err)
	}
	if len(paths) < 90 {
		t.Fatalf("found %d self-host modules, expected the full set — a shrunken sweep passes every floor below by vacuity", len(paths))
	}
	sort.Strings(paths)
	out := make([]strippedSource, 0, len(paths))
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		out = append(out, strippedSource{
			name:  filepath.Base(p),
			lines: strings.Split(stripFernLiterals(string(src)), "\n"),
		})
	}
	return out
}

// censusHit is one match, kept with its location so a row that moves says where.
type censusHit struct {
	file string
	line int
	text string
}

func (h censusHit) String() string {
	return fmt.Sprintf("%s:%d: %s", h.file, h.line, strings.TrimSpace(h.text))
}

// census maps a row name to every site that row counted.
type census map[string][]censusHit

func (c census) n(name string) int { return len(c[name]) }

// where names the sites of a row, capped so a 5,000-hit row stays readable.
func (c census) where(name string) string {
	hits := c[name]
	const shown = 12
	parts := make([]string, 0, shown+1)
	for i, h := range hits {
		if i == shown {
			parts = append(parts, fmt.Sprintf("… and %d more", len(hits)-shown))
			break
		}
		parts = append(parts, h.String())
	}
	return strings.Join(parts, "\n        ")
}

// censusRow is one line of the table. Rows carrying no assertion are still
// reported: docs/SELFHOST-LANGUAGE-FRICTION.md quotes them, and `-v` on this test
// is how they are re-measured.
type censusRow struct {
	name string
	pat  string
	// must is a literal substring every match of pat contains. It is a
	// prefilter — the census is 175k lines and a `\b`-anchored regex over all
	// of them costs ~140ms each — and getting one wrong drops hits, which the
	// pins below catch.
	must string
}

var censusRows = []censusRow{
	// The language features. Each is pinned below: the fixpoint's coverage of
	// the feature is exactly these sites and nothing else.
	{"generic functions", `\bfunction\s+[A-Za-z_][A-Za-z0-9_]*\s*\[`, "function"},
	{"generic structs", `\bstruct\s+[A-Za-z_][A-Za-z0-9_]*\s*\[`, "struct"},
	// An arrow lambda's parameter list is annotated and holds no nested
	// parens; a fn-TYPE annotation like `(parser.Expr, T) => T` has neither,
	// which is what separates the two spellings textually.
	{"arrow lambdas", `(^|[^A-Za-z0-9_])\(\s*[A-Za-z_][A-Za-z0-9_]*\s*:[^()]*\)\s*=>`, "=>"},
	// `function(x: T): R {` as an expression. The `[:{]` is what tells it from
	// a method declaration, whose parameter list is a RECEIVER and is followed
	// by the method name.
	{"anonymous function exprs", `\bfunction\s*\([^()]*\)\s*[:{]`, "function"},
	{"nested named fns", `^[ \t]+(pub\s+)?function\s+[A-Za-z_]`, "function"},
	{"for..in loops", `\bfor\s+[A-Za-z_][A-Za-z0-9_]*\s+in\s`, "for"},
	// Every `?` token. There are none at all, so nothing here yet needs to tell
	// the try operator from an optional-type suffix.
	{"try op", `\?`, "?"},
	{"Map type spellings", `\bMap\s*\[`, "Map"},
	{"astwalk call sites", `\bastwalk\s*\.`, "astwalk"},

	// The dialect the self-host writes instead. Ceilings, or context for them.
	{"wildcard match arms", `(^|[^A-Za-z0-9_])_\s*=>`, "=>"},
	{"arrow tokens", `=>`, "=>"},
	{"while loops", `\bwhile\s*\(`, "while"},
	{"as casts", `\bas\b`, "as"},
	{"minus-one sentinel returns", `\breturn\s+0\s*-\s*1\b`, "return"},
	{"method decls", `\bfunction\s*\([^()]*\)\s*[A-Za-z_]`, "function"},
	{"annotated var decls", `\bvar\s+[A-Za-z_][A-Za-z0-9_]*\s*:`, "var"},
	{"inferred var decls", `\bvar\s+[A-Za-z_][A-Za-z0-9_]*\s*=`, "var"},
}

// incrementRe is the hand-written `x = x + 1` the index-loop dialect is built
// from. It needs the two names compared, which RE2 has no backreference for, so
// it is counted apart from the table.
var incrementRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([A-Za-z_][A-Za-z0-9_]*)\s*\+\s*1\b`)

func takeCensus(t *testing.T, srcs []strippedSource) census {
	t.Helper()
	c := census{}
	for _, r := range censusRows {
		re := regexp.MustCompile(r.pat)
		for _, s := range srcs {
			for i, ln := range s.lines {
				if !strings.Contains(ln, r.must) {
					continue
				}
				for range re.FindAllStringIndex(ln, -1) {
					c[r.name] = append(c[r.name], censusHit{file: s.name, line: i + 1, text: ln})
				}
			}
		}
	}
	for _, s := range srcs {
		for i, ln := range s.lines {
			if !strings.Contains(ln, "+") {
				continue
			}
			for _, m := range incrementRe.FindAllStringSubmatch(ln, -1) {
				if m[1] == m[2] {
					c["increment by one"] = append(c["increment by one"], censusHit{file: s.name, line: i + 1, text: ln})
				}
			}
		}
	}
	return c
}

func logCensus(t *testing.T, srcs []strippedSource, c census) {
	t.Helper()
	names := make([]string, 0, len(c))
	for _, r := range censusRows {
		names = append(names, r.name)
	}
	names = append(names, "increment by one")
	t.Logf("self-host feature census over %d modules, literals stripped:", len(srcs))
	for _, name := range names {
		mods := map[string]bool{}
		for _, h := range c[name] {
			mods[h.file] = true
		}
		t.Logf("  %-26s %6d  in %d modules", name, c.n(name), len(mods))
	}
}

// pinned asserts a row has not moved in either direction. The feature rows are
// small enough that any move is worth a look, and pinning them is what keeps the
// doc's table honest — a floor alone lets the number drift up unnoticed, which is
// how the disagreeing hand-counts arose.
func pinned(t *testing.T, c census, name string, want int, why string) {
	t.Helper()
	if got := c.n(name); got != want {
		t.Errorf("%s: census counts %d, pinned at %d.\n"+
			"    %s\n"+
			"    Re-measure with `go test ./internal/e2eselfhost/ -run TestSelfHostFeatureCensus -v`, then move BOTH this number and the row in docs/SELFHOST-LANGUAGE-FRICTION.md §1.\n"+
			"    sites:\n        %s", name, got, want, why, c.where(name))
	}
}

func atLeast(t *testing.T, c census, name string, want int, why string) {
	t.Helper()
	if got := c.n(name); got < want {
		t.Errorf("%s: census counts %d, floor %d.\n    %s\n    sites:\n        %s", name, got, want, why, c.where(name))
	}
}

func atMost(t *testing.T, c census, name string, limit, measured int, why string) {
	t.Helper()
	if got := c.n(name); got > limit {
		t.Errorf("%s: census counts %d, ceiling %d (measured %d, plus deliberate headroom).\n"+
			"    %s\n"+
			"    Re-measure with `go test ./internal/e2eselfhost/ -run TestSelfHostFeatureCensus -v`. Then either convert the new call sites, or — if the growth is legitimate — move the ceiling and say in the commit message what added the sites.\n"+
			"    sites:\n        %s", name, got, limit, measured, why, c.where(name))
	}
}

func TestSelfHostFeatureCensus(t *testing.T) {
	srcs := selfHostStripped(t)
	assertNoLiteralResidue(t, srcs)
	c := takeCensus(t, srcs)
	logCensus(t, srcs, c)

	// The features. These sites are the ENTIRE fixpoint coverage of each row:
	// delete them and the self-host stops exercising the feature, whatever the
	// e2eselfhost fixtures do.
	pinned(t, c, "generic functions", 12,
		"Every one is astwalk's — nine on the fold spine, three on the accumulator-carrying map spine (map_expr_acc / map_stmt_acc / map_stmts_acc). It is the only generic code the self-host compiles, so it is the only monomorphisation the fixpoint exercises.")
	pinned(t, c, "generic structs", 0,
		"The self-host declares no generic struct, so nothing on the fixpoint path monomorphises a generic TYPE — only generic functions. This is load-bearing, not incidental: a generic struct in a signature promotes its type param to the monomorphiser, and the per-module emit path runs no monomorphiser, so the un-cloned template fails IR verify there — the accumulator spine returns bare tuples for exactly that reason.")
	pinned(t, c, "arrow lambdas", 6,
		"Two are astwalk's no-op statement visitors and one is the checker's, in e060_collect_dyn_locals, which wants fold_stmt_nodes for the statement half and has nothing to say about expressions; the other three are constfold's assert probe, the first arrow lambdas here that compute rather than return the accumulator untouched.")
	pinned(t, c, "anonymous function exprs", 4,
		"`function(x: T): R { … }` in expression position — astwalk's splice, checker's diag fold, and two parser rewriters. All capture, so these plus the capturing nested named fns are the self-host's only closures.")
	pinned(t, c, "nested named fns", 104,
		"Thirty are irlower's, riding astwalk — the cap-family's cap_type_of, init_of and fn_ret_of, body_binds_lambda's binds_lambda_at, and the closure-array family's four (the credit's cand_site_of and binds_fresh_at, and the empty-`fn[]`-declaration scan's box_appended_names and empty_fnarr_decl, #4354) on the statement spine, each closing over the name or body being resolved, the five visit/descend wrapper pairs of the Perceus escape-scanner trio (rctuple_esc_expr, arrtup_elem_esc_expr + its payload driver, arrstruct_elem_esc_expr + its payload driver), each a two-line closure handing the family's context to its top-level shape classifier and visitor (#6993; the assign-targets family's visitors capture nothing and are plain top-level functions), plus the env-box lift's nine on the accumulator spine: lift_inline_closures_expr, lift_inline_closures and lift_iife_conditions each carry a statement/expression wrapper pair handing fd/gfns/structs/mfuncs to ilc_stmt_at / ilc_expr_at, and box_iife_arm_values, box_iife_arm_array_elems and lift_iife_arm_values each carry one statement visitor over the shared top-level claim-all ilc_keep_expr, and the superseded-field own move's three (#8186): field_move_bases_of's bad_of, closing over the signature registry and the bound set, and the fold_expr tallies of count_ident_reads and count_field_reads, each closing over the name being counted — its capture-free scans (defer_at, field_move_decl_of, lambda_body_reads) are top-level, over the shared no-op visitors no_expr_bool, no_expr_strs and no_stmt_strs. Sixteen are parser's: the mentions visitors (expr_mentions / stmt_mentions) and the fn-value-call visitors (fnv_name_called_in_expr / _stmt), each closing over the name being sought, the moves-handle pair in stmts_move_handle — an expression visitor closing over the function table and the name, and a statement visitor for StmtAssign's target — the deep defer scan's pair in dl_stmts_have_defer_deep (a StmtDefer probe plus a no-op expression visitor), the elb guard pairs in elb_assigns_stmt and elb_idx_monotonic_stmts, each a statement visitor closing over the guarded name plus a no-op expression visitor, and the cf const-rule family (the two scanners' expression+statement visitors and the two rewrite closures, eight in all, each closing over the name being folded), the lambda-lift's two capture-threading rewrite closures, the named/default-argument pass's two rewrite closures, and the hl pairs in hl_collect_var_names and hl_has_rec_local — a var-name collector and a recursive-local probe, each with a no-op expression visitor — the first consumers in parser.fern to ride the spine (#6993). Four are astwalk visitors closing over their enclosing function's locals. Nineteen are wasm_ir's helper-gate predicates behind any_op(cache, pred), two of which capture a parameter and the rest capture nothing. The last seventeen are checker collectors riding astwalk: mc_mentions_expr and mc_mentions_stmts take two visitors each (an expression visitor for ExprIdent, a statement visitor for StmtAssign's target, which is a bare string rather than an expression), ow_count_ident takes a tally, as does ow_count_field_reads (#8186), e049_expr_lambdas, e049_stmt_own_lambdas, vref_expr and e032_expr each take a visitor plus a descent predicate that prunes at ExprLambda, e060_e062_stmts and e060_collect_dyn_locals take one visitor each — the first over expressions closing over the dyn name set, the second over statements, since a `var`'s name and annotation are fields on the statement, e053_own_exprs wraps e053_at_node so the visitor closes over PARAMETERS rather than the driver loop's per-statement `cur`, and match_stmt_own_lambdas, dupvar_stmt_own_lambdas and assign_stmt_own_lambdas each take a visitor plus a prune predicate to reach the statements inside a lambda body without re-reporting nested ones — the last of those builds the body's SCOPE (params plus return type) before recursing, since its pass is scope-threaded.")
	pinned(t, c, "try op", 0,
		"The self-host propagates errors by hand, so `?` has NO fixpoint coverage. A rise here is good news and means this row and the doc's have to move.")
	pinned(t, c, "Map type spellings", 11,
		"irverify's NameIndex, wasm_ir's call set, and builtins' mirror of std/json's JObject payload. The only hash map the self-host compiles.")

	// The two adoption metrics. Both are floors rather than pins: they are meant
	// to climb, and pinning one would fight the migration it measures. The
	// feature rows above stay pinned because a move there is news either way;
	// a module converting more loops is not.
	atLeast(t, c, "for..in loops", 1053,
		"704 in irlower.fern, 301 in checker.fern, 32 in astwalk.fern, 15 in visibility.fern, 1 in asm_ir.fern. A fall USUALLY means a module went back to hand-indexing — but not always: folding a collector onto astwalk removes its for..in loops along with its variant arms, and INDEXING a table deletes the scan whole rather than converting it, so the SH-022 walker migration, the index-loop conversion and #6888's table indexes move this row in different directions and a floor here protects nothing in between. Lower it for a fold or an index; raise it to the measurement for a loop conversion; investigate it otherwise. The astwalk floor is the one carrying the signal for the fold half.")
	atLeast(t, c, "astwalk call sites", 194,
		"Hand-written AST walkers collapsing onto the shared fold spine is what this counts. It should only climb; a fall means a consumer went back to spelling its own traversal. Raise it to the measurement on every conversion — left at a stale 85 while the count reached 96, it would have accepted a walker going back to hand-indexing without a word.")

	// The ratchet. These two are what the self-host writes INSTEAD of the
	// features above, and both are only supposed to fall. A ceiling carries
	// enough headroom for a normal PR's worth of new arms or loops without a red
	// build, and no more: the wildcard row spent ~10% in three weeks without one
	// conversion, so headroom that generous buys drift rather than tolerance.
	// Size it in PRs, not percent — see the wildcard row's own note.
	atMost(t, c, "wildcard match arms", 3001, 2998,
		"A `_ =>` arm is a match that does not enumerate its cases, so a new parser node added later is silently swallowed instead of caught. The fold spine exists to remove them. The 2563/2800 pair this replaces had been overrun on MAIN — 2807 there, red on its own — because nothing runs this workflow outside a pull request, so the arms that consumed the headroom each passed against an older base. The headroom is ~3 arms, not a percentage: the row climbed 244 in three weeks under a ceiling that could absorb it silently, and a ratchet that only ever moves up is not one. The 2908 -> 2915 step is four arms from the checker's map-key destination check: map_key_dest_diag matches a declared Type for TypeMap and its key for TypeStruct / TypeUnion, and mlit_is_desugared matches an Expr for ExprCall and its callee for ExprFieldAccess. All four are `_ => {}` over the Type and Expr sums, where enumerating every variant is longer and no safer — the arms discriminate ONE shape and ignore the rest by construction, which is the case the fold spine does not improve. The 2915 -> 2953 step is the assembler vocabulary lookups cmd/x86tblgen and cmd/arm64tblgen write into x86_native.fern and arm64_native.fern as string matches (#7903): a match over the open string domain REQUIRES a `_` arm (E030), so each generated lookup carries one `_ => { return 0 - 1; }` — the not-a-mnemonic answer its callers test for — and there is no variant sum to enumerate instead. The 2953 -> 2967 step is the #8224 field-append analysis in irlower.fern: fourteen arms across the candidate / excused-read / escape walks and the caller-side bracket, every one of them discriminating ONE shape out of the Expr or Stmt sum — an ident-rooted `<root>.<field>` receiver, a struct literal's spread base, a method call's callee, an ident argument — and contributing nothing for the rest by construction. Enumerating either sum at those arms is longer and no safer, and the excused-read walk is deliberately the case the fold spine cannot take: it prunes at an ident-rooted field chain and treats a method callee differently from a field read, which a descent PREDICATE over one node cannot express. The 2967 -> 2991 step is the #8186 superseded-field own move, landed against a main that had already spent two of the three on the way to 2969: twenty-two arms across the checker's recognizer (ow_field_move_fields and its ow_ident_name, ow_is_field_read and ow_count_field_reads helpers, plus the StmtReturn probe in ow_stmt) and irlower's mirror of it (field_move_owned_value, field_move_keys, field_move_keys_of_stmt, count_ident_reads, count_field_reads, is_field_read_of, defer_at, field_move_decl_of, lambda_body_reads, bad_of, and the own-position bracket in lower_call_named_generic), every one discriminating ONE shape out of the Expr or Stmt sum — an ident, an ident-rooted field read, a direct call and its ident callee, a struct literal with a base, a lambda, a `var`/assign/`for`/match binder, a defer — and contributing nothing for the rest by construction; none is over a small closed sum where explicit arms would be the safer spelling. The 2991 -> 2998 step is the #8409 identity-return handback fix in irlower.fern: seven arms across str_recv_may_be, expr_may_be_str_param, handback_call_on_fresh_arg and str_arg_is_fresh_syntactic, each discriminating ONE shape out of the Expr sum — a bare ident receiver / param, a call, a method callee's field access, a string literal — and contributing nothing for the rest by construction, so enumerating the whole Expr sum at each would be longer and no safer. The headroom is unchanged at 3, so the next PR gets the same budget this one did.")
	atMost(t, c, "increment by one", 5200, 4185,
		"Every `x = x + 1` is one hand-written index loop that `for x in xs` would carry. This is the dialect the compiler is written in, and the count is the size of the migration left.")
}

// assertNoLiteralResidue re-proves the strip on the real corpus before anything
// is counted over it: nothing that survives may contain a `//`, or a string or
// char quote outside the empty pair the strip leaves behind. A mis-scanned
// literal shows up here as residue rather than silently as a wrong count.
func assertNoLiteralResidue(t *testing.T, srcs []strippedSource) {
	t.Helper()
	for _, s := range srcs {
		for i, ln := range s.lines {
			bad := ""
			switch {
			case strings.Contains(ln, "//"):
				bad = "comment"
			case strings.Contains(strings.ReplaceAll(ln, `""`, ""), `"`):
				bad = "string"
			case strings.Contains(strings.ReplaceAll(ln, "''", ""), "'"):
				bad = "char"
			}
			if bad != "" {
				t.Errorf("%s:%d: %s literal survived the strip, so every count below it is unsound: %s", s.name, i+1, bad, ln)
			}
		}
	}
}

func TestStripFernLiterals(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"plain code", `var i: i32 = 0;`, `var i: i32 = 0;`},
		{"line comment", `var i: i32 = 0; // count _ => arms`, `var i: i32 = 0; `},
		{"whole-line comment", `// for x in xs { }`, ``},
		{"string", `emit("for x in xs");`, `emit("");`},
		{"comment marker inside string", `emit("http://x _ => y");`, `emit("");`},
		{"escaped quote in string", `emit("he said \"x = x + 1\" ok");`, `emit("");`},
		{"string ending in escaped backslash", `emit("c:\\");`, `emit("");`},
		{"apostrophe inside string", `emit("don't _ => x");`, `emit("");`},
		{"quote inside char literal", `if (c == '"') { }`, `if (c == '') { }`},
		{"escaped quote char literal", `if (c == '\'') { }`, `if (c == '') { }`},
		{"escaped backslash char literal", `if (c == '\\') { }`, `if (c == '') { }`},
		{"byte literal", `if (b == b'\n') { }`, `if (b == b'') { }`},
		{"multi-byte char literal", `if (c == '∃') { }`, `if (c == '') { }`},
		{"f-string", `out = f"i={i} _ => x";`, `out = "";`},
		{"f-string with nested string", `out = f"{join(xs, ", ")} _ => x";`, `out = "";`},
		{"f-string with brace escapes", `out = f"{{ _ => }} {i}";`, `out = "";`},
		{"identifier ending in f", `var buf: string = "x";`, `var buf: string = "";`},
		// An apostrophe that reaches code text is not a literal, and must stay
		// one character rather than eating the line up to the next quote.
		{"lone apostrophe", `a ' b _ => c`, `a ' b _ => c`},
		// An unterminated literal is a lex error, not something the strip is
		// entitled to carry into the next line.
		{"unterminated string", "emit(\"oops\nvar i: i32 = 0;", "emit(\"\"\nvar i: i32 = 0;"},
		{"unterminated comment-free f-string", "out = f\"{a\nvar i: i32 = 0;", "out = \"\"\nvar i: i32 = 0;"},
		{"line count preserved", "a\n// b\nc", "a\n\nc"},
		{"comment then char literal next line", "// don't\nif (c == 'x') { }", "\nif (c == '') { }"},
		{"string then comment", `emit("a"); // don't count "b"`, `emit(""); `},
		{"two strings on a line", `f("a", "b");`, `f("", "");`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripFernLiterals(tc.src); got != tc.want {
				t.Errorf("stripFernLiterals(%q)\n = %q\nwant %q", tc.src, got, tc.want)
			}
		})
	}
}
