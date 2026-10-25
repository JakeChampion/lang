package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/oracle/interp"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/pkg/embed"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/fmtsource"
	"github.com/jakechampion/lang/internal/syntax/parser"
	"github.com/jakechampion/lang/internal/syntax/printer"
	"github.com/jakechampion/lang/internal/tools/gates"
	"github.com/jakechampion/lang/internal/tools/launcher"
	"github.com/jakechampion/lang/internal/tools/literate"
	"github.com/jakechampion/lang/internal/tools/tty"
)

func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

const literateExt = ".fern.md"

func isLiterate(srcPath string) bool {
	return strings.HasSuffix(srcPath, literateExt)
}

type litRemap struct {
	docPath string
	docSrc  string
	remap   func(ast.Position) ast.Position
}

type entry struct {
	prog      *ast.Program
	srcs      map[string]string
	path      string
	src       string
	entryAbs  string
	remaps    map[string]*litRemap
	multiFile bool
}

func litRemaps(litMods map[string]*modload.LiterateModule) map[string]*litRemap {
	out := map[string]*litRemap{}
	for modPath, lm := range litMods {
		out[modPath] = &litRemap{docPath: lm.DocPath, docSrc: lm.DocSrc, remap: remapFor(lm.LineMap)}
	}
	return out
}

func remapFor(lineMap []literate.Line) func(ast.Position) ast.Position {
	return func(p ast.Position) ast.Position {
		if p.Line < 1 || p.Line > len(lineMap) {
			return ast.Position{}
		}
		m := lineMap[p.Line-1]
		col := p.Col - m.ColShift
		if col < 1 {
			col = 1
		}
		return ast.Position{Line: m.Lit, Col: col}
	}
}

func loadEntry(srcPath string) (entry, error) {
	abs := absPath(srcPath)
	if !isLiterate(srcPath) {
		prog, srcs, litMods, err := modload.LoadWithLiterate(srcPath, nil)
		e := entry{prog: prog, srcs: srcs, path: srcPath, entryAbs: abs, remaps: litRemaps(litMods)}
		if srcs != nil {
			e.src = srcs[abs]
		}
		if err != nil {
			return e, e.format(err)
		}
		return e, nil
	}
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return entry{}, err
	}
	litSrc := string(srcBytes)
	doc := literate.Parse(litSrc)
	warnUnusedChunks(srcPath, doc)
	if doc.HasFiles() {
		return loadMultiFileEntry(srcPath, abs, litSrc, doc)
	}
	tangled, lineMap, err := doc.Tangle()
	if err != nil {
		return entry{}, fmt.Errorf("%s", diag.Format(srcPath, litSrc, err))
	}
	prog, srcs, litMods, lerr := modload.LoadWithLiterate(srcPath, map[string]string{abs: tangled})
	remaps := litRemaps(litMods)
	remaps[abs] = &litRemap{docPath: srcPath, docSrc: litSrc, remap: remapFor(lineMap)}
	e := entry{prog: prog, srcs: srcs, path: srcPath, src: litSrc, entryAbs: abs, remaps: remaps}
	if lerr != nil {
		return e, e.format(lerr)
	}
	return e, nil
}

func loadMultiFileEntry(srcPath, abs, litSrc string, doc *literate.Document) (entry, error) {
	results, err := doc.TangleFiles()
	if err != nil {
		return entry{}, fmt.Errorf("%s", diag.Format(srcPath, litSrc, err))
	}
	dir := filepath.Dir(abs)
	resolve := func(p string) string {
		if filepath.IsAbs(p) {
			return absPath(p)
		}
		return absPath(filepath.Join(dir, p))
	}
	overrides := map[string]string{}
	remaps := map[string]*litRemap{}
	for _, r := range results {
		fileAbs := resolve(r.Path)
		overrides[fileAbs] = r.Code
		remaps[fileAbs] = &litRemap{docPath: srcPath, docSrc: litSrc, remap: remapFor(r.LineMap)}
	}
	entryRel, eerr := doc.EntryFile()
	if eerr != nil {
		var mains []string
		for _, r := range results {
			if definesMain(r.Code) {
				mains = append(mains, r.Path)
			}
		}
		if len(mains) != 1 {
			return entry{}, fmt.Errorf("%s", diag.Format(srcPath, litSrc, eerr))
		}
		entryRel = mains[0]
	}
	entryFile := filepath.Join(dir, entryRel)
	if filepath.IsAbs(entryRel) {
		entryFile = entryRel
	}
	prog, srcs, litMods, lerr := modload.LoadWithLiterate(entryFile, overrides)
	for modPath, lr := range litRemaps(litMods) {
		remaps[modPath] = lr
	}
	e := entry{prog: prog, srcs: srcs, path: srcPath, src: litSrc, entryAbs: resolve(entryRel), remaps: remaps, multiFile: true}
	if lerr != nil {
		return e, e.format(lerr)
	}
	return e, nil
}

var mainFuncRe = regexp.MustCompile(`(?m)^\s*(pub\s+)?(function|fn)\s+main\b`)

var embeddedAssets *embed.Set

func definesMain(code string) bool { return mainFuncRe.MatchString(code) }

func (e entry) format(err error) error {
	if err == nil {
		return nil
	}
	renderRemap := func(lr *litRemap, one error) string {
		return diag.FormatRemapped(lr.docPath, lr.docSrc, lr.remap, one)
	}
	render := func(one error) string {
		path := ""
		if f, ok := one.(diag.Filed); ok {
			path = f.File()
		}
		if path == "" || path == e.entryAbs {
			if path == "" && e.multiFile {
				return one.Error()
			}
			if lr, ok := e.remaps[e.entryAbs]; ok {
				return renderRemap(lr, one)
			}
			return diag.Format(e.path, e.src, one)
		}
		if lr, ok := e.remaps[path]; ok {
			return renderRemap(lr, one)
		}
		if src := e.srcs[path]; src != "" {
			return diag.Format(path, src, one)
		}
		if lr, ok := e.remaps[e.entryAbs]; ok {
			return renderRemap(lr, one)
		}
		return diag.Format(e.path, e.src, one)
	}
	if es, ok := err.(diag.Errors); ok {
		var b strings.Builder
		for i, one := range es.SortedByPosition(e.entryAbs) {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(render(one))
		}
		return fmt.Errorf("%s", b.String())
	}
	return fmt.Errorf("%s", render(err))
}

func warnUnusedChunks(srcPath string, doc *literate.Document) {
	for _, name := range doc.UnusedChunks() {
		fmt.Fprintf(os.Stderr, "%s:%d:1: warning[W003]: chunk <<%s>> is defined but never used\n", srcPath, doc.ChunkLine(name), name)
	}
}

type repeatedString []string

func (r *repeatedString) String() string { return strings.Join(*r, ",") }
func (r *repeatedString) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func shouldColorize(mode string) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	default:
		if os.Getenv("NO_COLOR") != "" {
			return false
		}
		return tty.IsTerminal(int(os.Stderr.Fd()))
	}
}

func shouldUseASCII(force bool) bool {
	if force {
		return true
	}
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			up := strings.ToUpper(v)
			return !strings.Contains(up, "UTF-8") && !strings.Contains(up, "UTF8")
		}
	}
	return true
}

func main() {
	out := flag.String("o", "", "output path; if unset, a native target writes its assembly to stdout, and a wasm target refuses unless -emit asm asks for its WAT")
	target := flag.String("target", "arm64-linux", "target as `<isa>-<environment>` — the ISA half selects the backend, the environment half says what the host provides; neither is implied. arm64-linux (default, Linux ELF), arm64-android (Linux ELF as a static position-independent executable for Android), arm64-darwin (native Apple Silicon macOS), x86-64-linux (Linux ELF), wasm32-wasi (CLI component), wasm32-wasi-http (HTTP handler component implementing wasi:http/incoming-handler), arm64-freestanding / x86-64-freestanding (no host at all — type-checks against an empty capability set; no backend emits for them yet, see docs/FREESTANDING-CORE.md). `fern-oracle -targets` lists them with their capabilities.")
	runIt := flag.Bool("run", false, "build to a temporary binary and execute it: directly when the target's ISA is the host's, else under -qemu (qemu-x86_64 for an x86-64 target when -qemu is left at its default); a wasm32-wasi target runs under `wasmtime run`")
	optimize := flag.Bool("O", false, "release build: elide every assert() check after type-checking (the condition is not evaluated, so asserts must be side-effect-free). Applies to compiled output; -interp and -check always keep asserts.")
	shared := flag.Bool("shared", false, "emit a shared object (.so) instead of an executable — a position-independent ET_DYN with a dynamic symbol table exporting the -export functions, loadable via dlopen / Android's System.loadLibrary. Native ELF targets only (x86-64, arm64, arm64-android); requires -o.")
	export := flag.String("export", "", "with -shared: comma-separated function names to export in the .so (default: main). Each must be a defined top-level function; it becomes a dynamic symbol resolvable by the loader.")
	qemu := flag.String("qemu", "qemu-aarch64", "user-mode emulator used by -run")
	repl := flag.Bool("repl", false, "start an interactive REPL via the AST interpreter")
	doInterp := flag.Bool("interp", false, "run FILE.fern (or `-` for stdin) through the AST interpreter — no codegen, no link, no binary. main()'s return value becomes the process exit code (clamped to 0..255). State is fresh per invocation; the REPL flag keeps an interactive session across lines.")
	backend := flag.String("backend", "", backendFlagUsage)
	emit := flag.String("emit", "", "output form for the selected -target, instead of its default. `core-module` emits a raw wasm core module (runnable via `wasmtime run --invoke <fn>`) instead of composing a component; `command-module` emits a WASI preview-1 COMMAND module — the same core bytes plus a `_start` that runs main and exits with its value, which is what a preview-1 host (`wasmtime run`, or a browser shim like web/wasi-shim.js) runs directly. Both are the wasm targets only. Replaces the old `-target wasm-bin` spelling: an output format is a property of the artifact, not of the machine it runs on, so it does not belong in the target name.")
	embedDir := flag.String("embed", "", "embed a directory of assets into the binary at compile time. `__fern_asset(\"NAME\")` in the source is replaced with a string literal holding that file's text, which must be valid UTF-8, and `__fern_asset_bytes(\"NAME\")` with a u8[] literal holding its bytes, where NAME is the file's slash-separated path relative to DIR; `__fern_assets()` and `__fern_assets_bytes()` enumerate the bundle the same two ways. Assets are immortal constants: no refcount traffic, zero-copy to hand to user code.")
	emitDebug := flag.Bool("g", false, "emit a symbol table and DWARF into the native binary so debuggers, nm, backtraces, and profilers can map code addresses to function names and source lines")
	doFmt := flag.Bool("fmt", false, "format the source files (- for stdin) and write them to stdout in order (use -w to write back in place, -d to print a diff, -o OUT with a single file)")
	writeBack := flag.Bool("w", false, "with -fmt, overwrite each input file with its formatted output")
	diffMode := flag.Bool("d", false, "with -fmt, print a unified diff between each file and its formatted form; exits 1 when any differs")
	doResolve := flag.Bool("resolve", false, "run Minimum Version Selection over a fern.toml's versioned ([package] index) dependencies and write the chosen versions to fern.lock (pass the manifest, its directory, or any file inside the package; default `.`). url-sourced versions are fetched and verified into the content-addressed store. The build reads fern.lock; the compiler never reads the index.")
	doVendor := flag.Bool("vendor", false, "flatten the transitive dependency graph of a fern.toml (pass the manifest, its directory, or any file inside the package; default `.`) into <root>/vendor/<name>/, one directory per package. After vendoring, builds are fully offline — the loader resolves declared dependencies out of vendor/ and never touches the network or the deps' original path/url locations. url dependencies must be fetched (`fern-oracle -fetch`) first; vendoring copies from the store.")
	doAdd := flag.Bool("add", false, "add a dependency to the nearest fern.toml: `fern-oracle -add NAME SPEC [DIR]` where SPEC is `path:../dir`, `url:https://…/pkg.tar.gz` (the archive is fetched and its sha256 recorded automatically — no hand-computed hash), `workspace` (a `{ workspace = true }` member dep), or `version:X.Y.Z` (an MVS dependency). DIR (default `.`) selects the package whose fern.toml to edit. The manifest is edited textually so comments and formatting survive.")
	addGrant := flag.String("grant", "", "with -add: the comma-separated capabilities the dependency may reach, or `none` for an empty grant")
	doFetch := flag.Bool("fetch", false, "download the url+hash dependencies declared by a fern.toml (pass the manifest, its directory, or any file inside the package; default `.`) into the content-addressed package store, verifying each archive against its declared sha256 before unpacking. Transitive: path dependencies' manifests are fetched too. It, `-add url:` and `-resolve` are the commands that download packages — build/check/interp read the store and error when a url dependency hasn't been fetched.")
	doLint := flag.Bool("lint", false, "lint FILE.fern or every .fern source under a directory, reporting code that compiles but reads badly (start with `fern-oracle -lint-rules`). Parse-only: no type-checking, no import resolution, so a file with a type error still lints and a whole tree costs one parse per file. Severities come from the governing fern.toml's [lint] table and then -lint-set; a `// fern-lint: allow RULE` comment silences one site. Exits 1 when a rule set to `deny` fires; a `warn` finding prints and exits 0.")
	listLintRulesFlag := flag.Bool("lint-rules", false, "list the lint rules with their default severity, description, and tunable options, then exit.")
	var lintSets repeatedString
	flag.Var(&lintSets, "lint-set", "with -lint: set one rule's severity, `RULE=allow|warn|deny`. Repeatable; wins over the manifest's [lint] table.")
	var lintOpts repeatedString
	flag.Var(&lintOpts, "lint-opt", "with -lint: set one rule option, `RULE.OPTION=VALUE` (e.g. cyclomatic-complexity.max=20). Repeatable; wins over the manifest's [lint.options] table.")
	lintCover := flag.String("lint-cover", "", "with -lint: score the change-risk rule from `FILE`, a saved -cover run's stderr.")
	doCheck := flag.Bool("check", false, "type-check FILE.fern (or `-` for stdin) and its transitive imports. No codegen, no link, no binary. Silent on success; otherwise prints every diagnostic, ordered by file and position, and exits 1.")
	doCapabilities := flag.Bool("capabilities", false, "print the per-package capability usage of FILE.fern and its transitive imports — one line per package (fern.toml package name, or `(root)` when no manifest governs the program): the v1 capabilities (net, fs, env, subprocess, time, random) its declared functions can reach by call-graph reachability, with an example call chain down to the tagged runtime builtin. Stdlib usage is attributed to the calling package. The report itself enforces nothing; manifests' `capabilities` grants are enforced (E070) on the compile/-check/-interp paths (docs/PACKAGE-CAPABILITIES-BRIEF.md). No codegen.")

	doEffects := flag.Bool("effects", false, "print the per-FUNCTION effect row of FILE.fern and its transitive imports — the effects (net, fs, env, subprocess, time, random) each declared function can reach by call-graph reachability, with an example call chain, and which effects it is charged only because it calls through a function value whose target is not statically known. Ends with the row-size distribution and the split between functions that call a tagged builtin themselves and those that only inherit. `-capabilities` answers the same question per PACKAGE; this one is per function. Report mode; nothing here enforces. No codegen. (docs/EFFECT-ROWS-BRIEF.md)")
	doTangle := flag.Bool("tangle", false, "tangle a literate FILE.fern.md (Knuth-style named chunks) into plain Fern source on stdout. Expands the root chunk `<<*>>`, resolving `<<chunk>>` references in definition order. A document using `file=PATH` blocks tangles to multiple modules, each printed under a `// ==> path <==` banner. With -o set, writes to disk instead: -o DIR receives one file per `file=` module (subdirs created as needed); a single-`<<*>>` document writes -o FILE. No codegen.")
	doWeave := flag.Bool("weave", false, "weave a literate FILE.fern.md into a cross-referenced Markdown reading document on stdout (or -o FILE) — chunk definitions get ⟨name⟩≡ labels and \"used in\" cross-references. Add -html for a self-contained, styled HTML page (highlighted code + clickable chunk references). No codegen.")
	weaveHTML := flag.Bool("html", false, "with -weave, emit a self-contained styled HTML page (embedded CSS, Fern syntax highlighting, and clickable `<<chunk>>` cross-reference links) instead of Markdown.")
	tangleChunk := flag.String("chunk", "", "with -tangle, expand and print only the named chunk (e.g. -chunk 'the main loop') instead of the <<*>> root — for inspecting or extracting one chunk. Works on single- and multi-file documents.")
	doDoctest := flag.Bool("doctest", false, "run the `test`-directive example blocks in a literate FILE.fern.md. Each ```fern test block is tangled (its `<<refs>>` expand against the document's chunks) into a standalone program, compiled, and run; exit 0 = pass. Results print as TAP; the command exits non-zero if any example fails.")
	showVersion := flag.Bool("version", false, "print the commit this binary was built from (plus the Go version and platform) and exit — the nightly tag rolls, so this is how to say which build you have")
	showSelfhostKey := flag.Bool("selfhost-key", false, "print the key of the compiler and stdlib sources this binary embeds and exit. `make selfhost-cli` and `make bootstrap` write it to bin/fern-selfhost.key, and a fern uses the bin/fern-selfhost beside it only when that key is its own.")
	listTargets := flag.Bool("targets", false, "list the supported -target= values with their descriptions + capability surface, then exit. Surfaces the Platform-descriptor table (internal/pkg/platforms) as the canonical source of truth for what each target accepts.")
	var explain explainFlag
	flag.Var(&explain, "explain", "print the long-form explanation of an error code (`-explain E001`) and exit; a bare -explain lists the codes.")
	colorMode := flag.String("color", "auto", "colourise diagnostics: auto (default — colour only when stderr is a terminal and NO_COLOR is unset), always, or never.")
	asciiBoxes := flag.Bool("ascii", false, "with coloured diagnostics, draw the gutter with a plain `|` instead of the box-drawing `│` (also selected automatically when the locale isn't UTF-8).")
	terseDiagnostics := flag.Bool("q", os.Getenv("FERN_DIAG_FORMAT") == "terse", "print one line per diagnostic without source context, notes, fixes or colour")
	sanitize := flag.Bool("sanitize", false, "debug build: turn on the heap memory-safety detectors together (native x86-64/arm64). Reports an rc over-release (double free) and a use-after-free of a quarantined block as a named `fern-sanitizer:` line on stderr followed by a fatal SIGILL, and prints a leak census at exit. Costs allocation throughput and never recycles a freed block, so it is for debugging, not for shipping; an unsanitized build is byte-identical to one from a compiler without the feature. Same as FERN_SANITIZE=1.")
	cover := flag.Bool("cover", false, "measurement build: instrument every executable source line with a hit counter, and every conditional (`if`, `while`, `&&`, `||`) with a second pair, then write the whole table to stderr at exit — one `fern-cover: <file>:<line> <count>` row per instrumented line, hit or not, plus `fern-branch:` rows carrying each conditional's evaluations and true-arm entries. Feed the saved stream to `fern-oracle -cover-report` for per-file line AND branch totals, the uncovered lists, or lcov. Costs an increment per line and per branch edge, and keeps functions nothing calls (so they report 0), so it is for measuring a test run, not for shipping; a build without it is byte-identical to one from a compiler without the feature. Native x86-64 and arm64 only — any other target errors rather than emitting an uninstrumented binary. Same as FERN_COVER=1.")
	coverReport := flag.Bool("cover-report", false, "read a saved `-cover` run's output (`fern-oracle -cover-report FILE`, or `-` for stdin) and print per-file line coverage plus the uncovered lines. Lines that don't carry the `fern-cover: ` prefix are ignored, so piping the program's whole stderr is fine. Add -lcov for an lcov tracefile instead. Report mode; no codegen.")
	lcov := flag.Bool("lcov", false, "with -cover-report, write an lcov tracefile (SF/DA/LF/LH records) instead of the human summary, for the coverage viewers that already read that format.")
	backtrace := flag.Bool("backtrace", ast.BacktraceEnabled, "emit the frame-pointer backtrace a fatal abort (bounds / arena / sanitizer) prints under its cause line, on the native x86-64 and arm64 backends. `-backtrace=false` drops the walk, the hex printer, and the \"backtrace:\" string from the binary — the size-critical opt-out; the cause line and every exit code (134 / 125 / 124) are unchanged. Same as FERN_BACKTRACE=0, which this flag defaults to.")
	harden := flag.Bool("harden", false, "arm64-linux executables: start every indirect-call target with a BTI landing pad, mark the image so the kernel traps an indirect branch anywhere else, and sign each frame's return address with pointer authentication.")
	size := flag.Bool("size", false, "build for size over speed on the native backends: reference-count operations call their runtime helpers rather than testing the count inline, and the backtrace walk is dropped unless -backtrace is given. Same as FERN_SIZE=1.")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: fern-oracle [-target <isa>-<environment>] [-emit core-module] [-o OUTPUT] [-run] [-qemu QEMU] FILE.fern [-- ARGS...]\n       (targets: arm64-linux, arm64-darwin, arm64-android, x86-64-linux, wasm32-wasi, wasm32-wasi-http; `fern-oracle -targets` for all)")
		fmt.Fprintln(os.Stderr, "       fern-oracle -fmt [-w | -d | -o OUT] FILE...")
		fmt.Fprintln(os.Stderr, "       fern-oracle -check FILE.fern | fern-oracle -check -      (type-check only; stdin form)")
		fmt.Fprintln(os.Stderr, "       fern-oracle -repl")
		fmt.Fprintln(os.Stderr, "       fern-oracle -interp FILE.fern | fern-oracle -interp -    (read from stdin)")
		fmt.Fprintln(os.Stderr, "       fern-oracle -lint PATH...                         (lint .fern sources; `fern-oracle -lint-rules` lists the rules)")
		fmt.Fprintln(os.Stderr, "       fern-oracle -capabilities FILE.fern               (per-package capability usage report)")
		fmt.Fprintln(os.Stderr, "       fern-oracle -tangle FILE.fern.md                  (literate: emit tangled Fern source)")
		fmt.Fprintln(os.Stderr, "       fern-oracle -weave  FILE.fern.md                  (literate: emit woven Markdown)")
		fmt.Fprintln(os.Stderr, "       fern-oracle -targets                                (list supported targets + capabilities)")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *cover && (*doInterp || *repl) {
		fmt.Fprintln(os.Stderr, "fern-oracle: -cover instruments compiled code; -interp and -repl do not lower, so they have no coverage to report")
		os.Exit(2)
	}
	if *embedDir != "" {
		set, err := embed.Load(*embedDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fern-oracle:", err)
			os.Exit(1)
		}
		embeddedAssets = set
	}

	diag.SetColor(shouldColorize(*colorMode))
	diag.SetASCII(shouldUseASCII(*asciiBoxes))
	diag.SetTerse(*terseDiagnostics)

	if *showVersion {
		fmt.Println(versionString())
		return
	}

	if *showSelfhostKey {
		key, err := launcher.SourcesKey()
		if err != nil {
			fmt.Fprintln(os.Stderr, "fern-oracle:", err)
			os.Exit(1)
		}
		fmt.Println(key)
		return
	}

	if *listTargets {
		for _, name := range platforms.Targets() {
			d := platforms.ForTarget(name)
			fmt.Println(d.String())
			fmt.Printf("    capabilities: %v\n", d.Capabilities)
			if len(d.HandlerKinds) > 0 {
				fmt.Printf("    handlers:     %v\n", d.HandlerKinds)
			}
			if len(d.Bindings) > 0 {
				fmt.Printf("    bindings:     %v\n", d.Bindings)
			}
			if d.NoBackend {
				fmt.Printf("    note:         check-only — no backend emits for this target yet\n")
			}
		}
		return
	}

	if explain.set {
		code, err := explain.codeWith(flag.Args())
		if err != nil {
			fmt.Fprintln(os.Stderr, "fern-oracle:", err)
			os.Exit(2)
		}
		os.Exit(runExplain(code, os.Stdout, os.Stderr))
	}

	if *repl {
		os.Exit(runREPL())
	}

	if *doInterp {
		path := ""
		var interpArgs []string
		if flag.NArg() >= 1 {
			path = flag.Arg(0)
			a, err := programArgs(flag.Args()[1:])
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			interpArgs = a
		} else {
			path = "-"
		}
		code, err := runInterp(path, interpArgs)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			var abort *interp.Abort
			if errors.As(err, &abort) {
				os.Exit(interp.AbortStatus)
			}
			os.Exit(1)
		}
		os.Exit(code)
	}

	if *doCheck {
		path := ""
		if flag.NArg() >= 1 {
			path = flag.Arg(0)
		} else {
			path = "-"
		}
		checkTarget := ""
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "target" {
				checkTarget = *target
			}
		})
		if err := runCheckTarget(path, checkTarget); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *coverReport {
		if flag.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "usage: fern-oracle -cover-report FILE   (FILE is a saved -cover run's output; `-` reads stdin)")
			os.Exit(2)
		}
		if err := runCoverReport(flag.Arg(0), *lcov, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *listLintRulesFlag {
		os.Exit(listLintRules(os.Stdout))
	}

	if *doLint {
		if flag.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "usage: fern-oracle -lint PATH... (a .fern file or a directory of them)")
			os.Exit(2)
		}
		if code := runLint(flag.Args(), lintSets, lintOpts, *lintCover, *colorMode, os.Stdout); code != 0 {
			os.Exit(code)
		}
		return
	}

	if *doCapabilities {
		if flag.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "usage: fern-oracle -capabilities FILE.fern")
			os.Exit(2)
		}
		if err := runCapabilities(flag.Arg(0), os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *doEffects {
		if flag.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "usage: fern-oracle -effects FILE.fern")
			os.Exit(2)
		}
		os.Exit(runEffects(flag.Arg(0)))
	}

	if *doFetch {
		start := "."
		if flag.NArg() >= 1 {
			start = flag.Arg(0)
		}
		if err := runFetch(start); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *doAdd {
		for _, a := range flag.Args() {
			if len(a) > 1 && strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "fern-oracle: %s comes after -add's arguments, where flags are no longer parsed; put it before -add\n", a)
				os.Exit(2)
			}
		}
		if flag.NArg() < 2 || flag.NArg() > 3 {
			fmt.Fprintln(os.Stderr, "usage: fern-oracle [-grant CAPS] -add NAME SPEC [DIR]   (SPEC = path:../dir | url:https://… | workspace | version:X.Y.Z)")
			os.Exit(2)
		}
		addDir := "."
		if flag.NArg() >= 3 {
			addDir = flag.Arg(2)
		}
		if err := runAdd(flag.Arg(0), flag.Arg(1), *addGrant, addDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *doResolve {
		start := "."
		if flag.NArg() >= 1 {
			start = flag.Arg(0)
		}
		if err := runResolve(start); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *doVendor {
		start := "."
		if flag.NArg() >= 1 {
			start = flag.Arg(0)
		}
		if err := runVendor(start); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if (*doTangle || *doWeave || *doDoctest) && flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, documentArgsError(flag.Args()))
		os.Exit(2)
	}

	if (*doTangle || *doWeave || *doDoctest) && flag.NArg() >= 1 {
		mode := "-tangle"
		if *doWeave {
			mode = "-weave"
		} else if *doDoctest {
			mode = "-doctest"
		}
		os.Exit(runLiterate(flag.Arg(0), mode, *out, *tangleChunk, *weaveHTML))
	}

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	srcPath := flag.Arg(0)

	if *doFmt {
		os.Exit(formatFiles(flag.Args(), *writeBack, *diffMode, *out))
	}

	progArgs, err := programArgs(flag.Args()[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	req := compileRequest{
		src: srcPath, out: *out, target: *target, emit: *emit, backend: *backend,
		embed: *embedDir, export: *export, qemu: *qemu,
		runIt: *runIt, shared: *shared, debug: *emitDebug, sanitize: *sanitize,
		cover: *cover, optimize: *optimize, harden: *harden, progArgs: progArgs,
	}
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "backtrace" {
			req.backtrace = backtrace
		}
		if f.Name == "size" {
			req.size = size
		}
	})
	code, err := compileSelfHost(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fern-oracle:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func formatFiles(paths []string, writeBack, diffMode bool, outPath string) int {
	if outPath != "" && len(paths) > 1 {
		fmt.Fprintf(os.Stderr, "fern-oracle: -fmt -o takes a single input file (%d given)\n", len(paths))
		return 2
	}
	code := 0
	for _, p := range paths {
		c, err := formatFile(p, writeBack, diffMode, outPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			c = max(c, 1)
		}
		if c > code {
			code = c
		}
	}
	return code
}

func formatFile(srcPath string, writeBack, diffMode bool, outPath string) (int, error) {
	name := srcPath
	var srcBytes []byte
	var err error
	if srcPath == "-" {
		if writeBack {
			return 2, fmt.Errorf("fern-oracle: -fmt -w cannot write back stdin; drop -w to print the result")
		}
		name = "<stdin>"
		srcBytes, err = io.ReadAll(os.Stdin)
	} else {
		srcBytes, err = os.ReadFile(srcPath)
	}
	if err != nil {
		return 1, err
	}
	src := string(srcBytes)
	var formatted string
	if isLiterate(srcPath) {
		formatted = fmtsource.FormatLiterate(src)
	} else {
		formatted, err = fmtsource.Format(src)
		if err != nil {
			return 1, fmt.Errorf("%s", diag.Format(name, src, err))
		}
	}
	if diffMode {
		diff := printer.UnifiedDiff(src, formatted, name, name)
		if diff == "" {
			return 0, nil
		}
		_, err := os.Stdout.WriteString(diff)
		return 1, err
	}
	if writeBack {
		if !isLiterate(srcPath) {
			if _, err := parser.Parse(formatted); err != nil {
				return 1, fmt.Errorf("%s: refusing to write back — the formatted output does not parse; this is a formatter bug, please report it:\n%s", srcPath, diag.Format(srcPath, formatted, err))
			}
		}
		info, err := os.Stat(srcPath)
		if err != nil {
			return 1, err
		}
		return 0, os.WriteFile(srcPath, []byte(formatted), info.Mode())
	}
	if outPath != "" {
		return 0, os.WriteFile(outPath, []byte(formatted), 0o644)
	}
	_, err = os.Stdout.WriteString(formatted)
	return 0, err
}

func runInterp(srcPath string, argv []string) (int, error) {
	var prog *ast.Program
	var formatErr func(error) error
	if srcPath == "-" {
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return 1, fmt.Errorf("read stdin: %w", err)
		}
		src := string(buf)
		p, _, err := modload.LoadSource(src)
		if err != nil {
			return 1, fmt.Errorf("%s", diag.Format("<stdin>", src, err))
		}
		prog = p
		formatErr = func(e error) error { return fmt.Errorf("%s", diag.Format("<stdin>", src, e)) }
	} else {
		e, err := loadEntry(srcPath)
		if err != nil {
			return 1, err
		}
		prog = e.prog
		formatErr = e.format
	}
	if err := constfold.Fold(prog, embeddedAssets); err != nil {
		return 1, formatErr(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return 1, formatErr(err)
	}
	constfold.PruneDeadArms(prog)
	if srcPath != "-" {
		warns, err := gates.Capabilities(srcPath, prog)
		for _, w := range warns {
			fmt.Fprintln(os.Stderr, w.Format(srcPath))
		}
		if err != nil {
			return 1, formatErr(err)
		}
	}
	if errs := gates.Ambient(srcPath, prog); errs != nil {
		return 1, formatErr(errs)
	}
	if err := monomorph.Run(prog, info); err != nil {
		return 1, formatErr(err)
	}

	ip := interp.New()
	ip.SetDynCoercions(info.DynCoercions)
	ip.Args = append([]string{srcPath}, argv...)
	for _, ed := range prog.Enums {
		ip.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		ip.Register(fn)
	}
	if _, ok := ip.Funcs["main"]; !ok {
		return 1, fmt.Errorf("program has no `main` function to interpret")
	}
	v, err := ip.CallByName("main", nil)
	if err != nil {
		return 1, err
	}
	status, _ := interp.ExitStatus(v)
	return status, nil
}

func runCheck(srcPath, target string) error {
	var prog *ast.Program
	var formatErr func(error) error
	if srcPath == "-" {
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		src := string(buf)
		p, _, err := modload.LoadSource(src)
		if err != nil {
			return fmt.Errorf("%s", diag.Format("<stdin>", src, err))
		}
		prog = p
		formatErr = func(e error) error { return fmt.Errorf("%s", diag.Format("<stdin>", src, e)) }
	} else {
		e, err := loadEntry(srcPath)
		if err != nil {
			return err
		}
		prog = e.prog
		formatErr = e.format
	}
	warns, err := gates.Check(srcPath, prog, target, embeddedAssets)
	name := srcPath
	if name == "-" {
		name = "<stdin>"
	}
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, w.Format(name))
	}
	if err != nil {
		return formatErr(err)
	}
	return nil
}

const backendFlagUsage = "code-generation backend for the selected -target: `ssa`, the register-allocating emitter and the only one on the native ISAs, or `flat`, the stack-machine emitter wasm uses. Empty picks the target's own. The target keeps its descriptor either way, so capability enforcement (E066) applies regardless."

func execDirect(binPath string, progArgs []string) (int, error) {
	cmd := exec.Command(binPath, progArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode(), nil
	}
	return 1, err
}

func execUnderQemu(qemu, binPath string, progArgs []string) (int, error) {
	if _, err := exec.LookPath(qemu); err != nil {
		return 1, fmt.Errorf("emulator %q not found on PATH (override with -qemu): %w", qemu, err)
	}
	return execTool(qemu, append([]string{binPath}, progArgs...))
}

func execTool(tool string, args []string) (int, error) {
	if _, err := exec.LookPath(tool); err != nil {
		return 1, fmt.Errorf("%s not found on PATH: %w", tool, err)
	}
	cmd := exec.Command(tool, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode(), nil
	}
	return 1, err
}

type explainFlag struct {
	set  bool
	bare bool
	code string
}

func (e *explainFlag) String() string   { return e.code }
func (e *explainFlag) IsBoolFlag() bool { return true }

func (e *explainFlag) Set(v string) error {
	e.set = true
	e.bare = v == "true"
	if !e.bare {
		e.code = v
	}
	return nil
}

func (e *explainFlag) codeWith(args []string) (string, error) {
	switch {
	case len(args) == 0:
		return e.code, nil
	case e.bare && len(args) == 1:
		return args[0], nil
	}
	return "", fmt.Errorf("-explain takes one error code (got %q)", args)
}

func runExplain(code string, stdout, stderr io.Writer) int {
	if code == "" {
		fmt.Fprintln(stdout, strings.Join(diag.AvailableCodes(), "\n"))
		return 0
	}
	body := diag.Explain(code)
	if body == "" {
		fmt.Fprintf(stderr, "unknown error code %q\navailable codes: %v\n", code, diag.AvailableCodes())
		return 1
	}
	fmt.Fprint(stdout, diag.FormatExplain(code, body))
	return 0
}
