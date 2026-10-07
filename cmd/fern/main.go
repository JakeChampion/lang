// Command fern compiles a single .fern source file.
//
// Usage:
//
//	fern FILE.fern                       # write arm64 Linux assembly to stdout
//	fern -o OUTPUT FILE.fern             # compile with the self-hosted
//	                                     # compiler to a static arm64
//	                                     # Linux binary (-target picks
//	                                     # another; `fern -targets` lists
//	                                     # them)
//	fern -run FILE.fern [-- ARGS...]     # build a temporary binary and run
//	                                     # it, directly when the target's
//	                                     # ISA is the host's, else under
//	                                     # -qemu (forwarding stdio)
//	fern -fmt FILE...                    # write idiomatic, indented source
//	                                     # to stdout, each file in order
//	                                     # (use -w to overwrite the files
//	                                     # in place; use -d to print a
//	                                     # unified diff per file against
//	                                     # the on-disk version and exit
//	                                     # non-zero when any differs; -o
//	                                     # OUT takes a single file). A
//	                                     # FILE.fern.md formats the code
//	                                     # inside each chunk in place,
//	                                     # leaving prose / fences / headers
//	                                     # and any unparseable chunk verbatim.
//	fern -check FILE.fern                # type-check the codebase rooted
//	                                     # at FILE.fern (follows imports);
//	                                     # silent on success, prints
//	                                     # diagnostics + exits 1 on error.
//	                                     # `-check -` reads from stdin.
//	fern -tangle FILE.fern.md            # literate programming: tangle a
//	                                     # Markdown document's named `fern`
//	                                     # chunks into plain Fern source on
//	                                     # stdout (expands the `<<*>>` root).
//	fern -weave FILE.fern.md             # weave the same document into a
//	                                     # cross-referenced Markdown reading
//	                                     # file on stdout.
//
// A literate `FILE.fern.md` may be passed to -check, -interp and -fmt
// directly: it is tangled in memory first, and diagnostics are mapped back
// to the lines you wrote in the document. The compile modes hand the path
// to the self-hosted compiler, which does not tangle yet (#11838).
//
// # Program arguments
//
// Driver flags come before FILE; everything after FILE belongs to the
// program and reaches it through `args()`. One leading `--` separates the
// two and is consumed by the driver, so a literal `--` is written `-- --`.
// `args()[0]` is the program as invoked — the source path under -interp,
// the executable's own path for a compiled binary — and `args()[1:]` is
// identical between the two for the same command tail:
//
//	fern -interp prog.fern -- --filter x   # args() = [prog.fern --filter x]
//	fern --run prog.fern -- --filter x     # args() = [/tmp/…   --filter x]
//
// A flag written after FILE with no `--` before it is refused (exit 2)
// rather than handed to the program as data: `fern prog.fern -o out` is a
// misplaced driver flag far more often than it is an argument named `-o`.
//
// -qemu names the emulator --run uses for a foreign ISA.
// The formatter preserves `//` line comments (leading, trailing, and
// standalone) and an author's blank-line grouping between statements.
package main

import (
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
	"github.com/jakechampion/lang/internal/tools/literate"
	"github.com/jakechampion/lang/internal/tools/tty"
)

// absPath returns the canonical absolute form of p, or p itself if
// the conversion fails. Used to look up source text in the per-file
// map modload returns.
func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// literateExt marks a literate Fern document — a Markdown file whose
// `fern` code chunks are tangled into plain Fern before compilation.
const literateExt = ".fern.md"

// isLiterate reports whether srcPath names a literate Fern document.
func isLiterate(srcPath string) bool {
	return strings.HasSuffix(srcPath, literateExt)
}

// litRemap remaps one module's tangled diagnostics back onto the
// literate document it was generated from: docPath / docSrc identify
// the `.fern.md` to render against, and remap turns a generated-source
// position into a document position.
type litRemap struct {
	docPath string
	docSrc  string
	remap   func(ast.Position) ast.Position
}

// entry bundles a loaded program with everything needed to render its
// diagnostics. Each module generated from a literate document — the
// entry `.fern.md` (single or multi-file), or an imported `.fern.md`
// library — gets a litRemap keyed by that module's path, so a checker
// error in any generated module points at the line the author wrote in
// the right document. Plain `.fern` / stdlib modules render against
// their own source from srcs; a fully non-literate program has no
// remaps.
type entry struct {
	prog     *ast.Program
	srcs     map[string]string
	path     string               // diagnostic-header path for the entry module
	src      string               // entry-module source (non-literate fallback rendering)
	entryAbs string               // abs path of the entry module
	remaps   map[string]*litRemap // module path → its literate-document remap
	// multiFile is true for a `file=`-multi-module literate entry, where
	// every generated module shares the document source but has its own
	// tangle line map. An unattributed error then can't be remapped
	// safely (we don't know which line map applies). See
	// docs/ADVERSARIAL-REVIEW-2026-06.md (L3).
	multiFile bool
}

// litRemaps builds the per-module remap table for the literate modules
// modload tangled while loading (imported `.fern.md` libraries).
func litRemaps(litMods map[string]*modload.LiterateModule) map[string]*litRemap {
	out := map[string]*litRemap{}
	for modPath, lm := range litMods {
		out[modPath] = &litRemap{docPath: lm.DocPath, docSrc: lm.DocSrc, remap: remapFor(lm.LineMap)}
	}
	return out
}

// remapFor turns a tangle line map into a position remapper: a tangled
// position (1-based line into the generated source) maps to its origin
// line in the `.fern.md` document, with the column shifted back by the
// indentation tangling prepended. A position outside the map maps to the
// zero Position so the renderer falls back to the bare message instead of
// drawing a caret over an arbitrary document line — a generated line
// number must never be used to index the document source. See
// docs/ADVERSARIAL-REVIEW-2026-06.md (L4).
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

// loadEntry loads srcPath through modload. A literate `.fern.md` entry
// is first parsed and tangled; the generated Fern source is handed to
// modload via an in-memory override keyed by the document's own path,
// so disk-relative imports still resolve against its directory. Any
// load error is returned already formatted (remapped onto the document
// for a literate entry). The returned entry's format method renders
// later pipeline errors the same way.
func loadEntry(srcPath string) (entry, error) {
	abs := absPath(srcPath)
	if !isLiterate(srcPath) {
		// A plain `.fern` entry may still import `.fern.md` libraries,
		// so capture their remaps for diagnostics.
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
		// Tangle errors carry document-coordinate positions already.
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

// loadMultiFileEntry tangles a `file=PATH` literate document into one
// virtual module per output file, feeds them all to modload as in-memory
// overrides (keyed by their paths relative to the document's directory,
// so the generated modules' `import "./other"` lines resolve), and loads
// from the entry module. Each module carries its own document remap, so
// a diagnostic in any generated file points back at the `.fern.md` line.
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
	// Pick the compile entry: the marked / sole file, else the unique
	// module that defines a `main` function.
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
		remaps[modPath] = lr // imported `.fern.md` libraries
	}
	e := entry{prog: prog, srcs: srcs, path: srcPath, src: litSrc, entryAbs: resolve(entryRel), remaps: remaps, multiFile: true}
	if lerr != nil {
		return e, e.format(lerr)
	}
	return e, nil
}

// definesMain reports whether tangled source declares a top-level
// `main` function — used to disambiguate the compile entry among a
// multi-file document's modules when none is marked `entry`.
var mainFuncRe = regexp.MustCompile(`(?m)^\s*(pub\s+)?(function|fn)\s+main\b`)

// embeddedAssets is set from the -embed flag: the asset bundle that
// __fern_asset("name") resolves against during const folding. nil when
// -embed was not passed, which makes any use of the builtin an error.
// It is compile-time state shared by the check / interp / build paths
// rather than a parameter because `run` already carries fifteen.
var embeddedAssets *embed.Set

func definesMain(code string) bool { return mainFuncRe.MatchString(code) }

// format renders err against the right source for each entry it
// carries: each diagnostic's File() picks its module's source out of
// the srcs map, while the literate entry file's diagnostics (and any
// error with no file attribution) route through the document remap so
// positions land on the `.fern.md` source. For a non-literate entry
// (remap nil) this matches the plain per-file modload error rendering.
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
		// The entry module, and any unattributed pre-decl error, render
		// against the entry — remapped onto its document if literate.
		if path == "" || path == e.entryAbs {
			// In a multi-file literate document each generated module
			// has its own tangle line map, so an unattributed error
			// can't be remapped through the entry's map without landing
			// on the wrong document line. Render the bare message
			// instead. See docs/ADVERSARIAL-REVIEW-2026-06.md (L3).
			if path == "" && e.multiFile {
				return one.Error()
			}
			if lr, ok := e.remaps[e.entryAbs]; ok {
				return renderRemap(lr, one)
			}
			return diag.Format(e.path, e.src, one)
		}
		// An imported literate module → its own document.
		if lr, ok := e.remaps[path]; ok {
			return renderRemap(lr, one)
		}
		// A plain imported module (stdlib / on-disk `.fern`) → its source.
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
		for i, one := range es {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(render(one))
		}
		return fmt.Errorf("%s", b.String())
	}
	return fmt.Errorf("%s", render(err))
}

// warnUnusedChunks prints a non-fatal lint note to stderr for each
// chunk a literate document defines but never reaches from a tangle
// root — typically a typo in a `<<ref>>` or a leftover definition.
func warnUnusedChunks(srcPath string, doc *literate.Document) {
	for _, name := range doc.UnusedChunks() {
		fmt.Fprintf(os.Stderr, "%s: warning: chunk <<%s>> is defined but never used\n", srcPath, name)
	}
}

// runDoctests tangles and runs every `test` block in a literate
// document, reporting TAP. Each example is a standalone program (its
// `<<refs>>` expanded against the document's chunks); it passes when it
// compiles and main returns 0. Diagnostics in a failing example are
// remapped back onto the `.fern.md`. Exits non-zero if any example fails.
func runDoctests(srcPath string) (int, error) {
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return 1, err
	}
	src := string(srcBytes)
	doc := literate.Parse(src)
	tests, err := doc.Doctests()
	if err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	fmt.Printf("1..%d\n", len(tests))
	if len(tests) == 0 {
		fmt.Fprintf(os.Stderr, "%s: no `test` blocks found\n", srcPath)
		return 0, nil
	}
	failed := 0
	for i, tc := range tests {
		if err := runDoctestCase(srcPath, src, tc); err != nil {
			failed++
			fmt.Printf("not ok %d - %s\n", i+1, tc.Name)
			for _, line := range strings.Split(strings.TrimRight(err.Error(), "\n"), "\n") {
				fmt.Printf("# %s\n", line)
			}
		} else {
			fmt.Printf("ok %d - %s\n", i+1, tc.Name)
		}
	}
	if failed > 0 {
		return 1, nil
	}
	return 0, nil
}

// runDoctestCase compiles and runs one tangled example through the
// interpreter. A virtual entry in the document's directory lets the
// example resolve disk-relative imports (and stdlib); compile errors are
// remapped onto the document.
func runDoctestCase(srcPath, src string, tc literate.Doctest) error {
	remap := remapFor(tc.LineMap)
	fmtErr := func(e error) error {
		return fmt.Errorf("%s", diag.FormatRemapped(srcPath, src, remap, e))
	}
	entry := filepath.Join(filepath.Dir(srcPath), "__doctest__.fern")
	prog, _, err := modload.LoadWith(entry, map[string]string{absPath(entry): tc.Code})
	if err != nil {
		return fmtErr(err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return fmtErr(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return fmtErr(err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		return fmtErr(err)
	}
	ip := interp.New()
	ip.SetDynCoercions(info.DynCoercions)
	for _, ed := range prog.Enums {
		ip.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		ip.Register(fn)
	}
	if _, ok := ip.Funcs["main"]; !ok {
		return fmt.Errorf("doctest has no `main` function to run")
	}
	v, err := ip.CallByName("main", nil)
	if err != nil {
		return err
	}
	if n, ok := v.(interp.Number); ok && int(n) != 0 {
		return fmt.Errorf("example failed: main returned %d (expected 0)", int(n))
	}
	return nil
}

// runLiterateTool implements the `-tangle` / `-weave` literate
// subcommands: parse the `.fern.md` document and write either the
// tangled Fern source or the woven Markdown. With `outPath` empty the
// result goes to stdout; otherwise it is written to disk — for a
// multi-file tangle `outPath` is a directory that receives one file per
// `file=` module (subdirectories created as needed), and for everything
// else it is a single output file.
func runLiterateTool(srcPath string, tangle bool, outPath, chunk string, html bool) (int, error) {
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return 1, err
	}
	src := string(srcBytes)
	doc := literate.Parse(src)
	warnUnusedChunks(srcPath, doc)
	if tangle {
		// -chunk NAME extracts just that chunk's expansion (shared across
		// single- and multi-file documents), bypassing the root / file roots.
		if chunk != "" {
			code, _, err := doc.TangleChunk(chunk)
			if err != nil {
				return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
			}
			if outPath != "" {
				if err := writeGeneratedFile(outPath, code+"\n"); err != nil {
					return 1, err
				}
				fmt.Fprintf(os.Stderr, "wrote %s\n", outPath)
				return 0, nil
			}
			_, werr := os.Stdout.WriteString(code + "\n")
			return 0, werr
		}
		// A multi-file document (`file=PATH` blocks) tangles to several
		// modules. To stdout they print under `// ==> path <==` banners;
		// with -o DIR each is written to its own file under DIR.
		if doc.HasFiles() {
			results, err := doc.TangleFiles()
			if err != nil {
				return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
			}
			if outPath != "" {
				for _, r := range results {
					dest := filepath.Join(outPath, filepath.FromSlash(r.Path))
					if err := writeGeneratedFile(dest, r.Code); err != nil {
						return 1, err
					}
					fmt.Fprintf(os.Stderr, "wrote %s\n", dest)
				}
				return 0, nil
			}
			var b strings.Builder
			for i, r := range results {
				if i > 0 {
					b.WriteString("\n")
				}
				fmt.Fprintf(&b, "// ==> %s <==\n%s\n", r.Path, r.Code)
			}
			_, werr := os.Stdout.WriteString(b.String())
			return 0, werr
		}
		code, _, err := doc.Tangle()
		if err != nil {
			return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
		}
		if outPath != "" {
			if err := writeGeneratedFile(outPath, code+"\n"); err != nil {
				return 1, err
			}
			fmt.Fprintf(os.Stderr, "wrote %s\n", outPath)
			return 0, nil
		}
		_, werr := os.Stdout.WriteString(code + "\n")
		return 0, werr
	}
	woven := doc.Weave()
	if html {
		woven = doc.WeaveHTML()
	}
	if outPath != "" {
		if err := writeGeneratedFile(outPath, woven); err != nil {
			return 1, err
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", outPath)
		return 0, nil
	}
	_, werr := os.Stdout.WriteString(woven)
	return 0, werr
}

// writeGeneratedFile writes content to path, creating any missing
// parent directories (so a multi-file tangle can emit `sub/util.fern`).
func writeGeneratedFile(path, content string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// repeatedString collects a flag that may be passed multiple times (e.g.
// `-lint-set a=warn -lint-set b=deny`).
type repeatedString []string

func (r *repeatedString) String() string { return strings.Join(*r, ",") }
func (r *repeatedString) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// shouldColorize resolves the -color mode to a boolean. "auto" (the
// default) enables colour only when stderr is a terminal (a character
// device) and NO_COLOR is unset — so `fern -check` piped to a file or run
// under a test harness stays plain, while an interactive terminal gets
// colour. "always" / "never" force the decision. See the NO_COLOR informal
// standard (no-color.org) and docs/DIAGNOSTIC-UX-RESEARCH.md Rec §7.
func shouldColorize(mode string) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	default: // "auto"
		if os.Getenv("NO_COLOR") != "" {
			return false
		}
		return tty.IsTerminal(int(os.Stderr.Fd()))
	}
}

// shouldUseASCII decides whether the rich diagnostic gutter falls back to a
// plain `|`. A `--ascii` forces it; otherwise box-drawing `│` is used only
// when the locale advertises UTF-8 (LC_ALL / LC_CTYPE / LANG contain
// "UTF-8"), the same signal docs/DIAGNOSTIC-UX-RESEARCH.md Rec §7 names.
// With no UTF-8 locale we fall back to ASCII rather than risk mojibake.
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
	out := flag.String("o", "", "output binary path; if unset, assembly is written to stdout")
	target := flag.String("target", "arm64-linux", "target as `<isa>-<environment>` — the ISA half selects the backend, the environment half says what the host provides; neither is implied. arm64-linux (default, Linux ELF), arm64-android (Linux ELF as a static position-independent executable for Android), arm64-darwin (native Apple Silicon macOS), x86-64-linux (Linux ELF), wasm32-wasi (CLI component), wasm32-wasi-http (HTTP handler component implementing wasi:http/incoming-handler), arm64-freestanding / x86-64-freestanding (no host at all — type-checks against an empty capability set; no backend emits for them yet, see docs/FREESTANDING-CORE.md). `fern -targets` lists them with their capabilities.")
	runIt := flag.Bool("run", false, "build to a temporary binary and execute it: directly when the target's ISA is the host's, else under -qemu (qemu-x86_64 for an x86-64 target when -qemu is left at its default)")
	optimize := flag.Bool("O", false, "release build: elide every assert() check after type-checking (the condition is not evaluated, so asserts must be side-effect-free). Applies to compiled output; -interp and -check always keep asserts.")
	shared := flag.Bool("shared", false, "emit a shared object (.so) instead of an executable — a position-independent ET_DYN with a dynamic symbol table exporting the -export functions, loadable via dlopen / Android's System.loadLibrary. Native ELF targets only (x86-64, arm64, arm64-android); requires -o.")
	export := flag.String("export", "", "with -shared: comma-separated function names to export in the .so (default: main). Each must be a defined top-level function; it becomes a dynamic symbol resolvable by the loader.")
	qemu := flag.String("qemu", "qemu-aarch64", "user-mode emulator used by --run")
	repl := flag.Bool("repl", false, "start an interactive REPL via the AST interpreter")
	doInterp := flag.Bool("interp", false, "run FILE.fern (or `-` for stdin) through the AST interpreter — no codegen, no link, no binary. main()'s return value becomes the process exit code (clamped to 0..255). State is fresh per invocation; the REPL flag keeps an interactive session across lines.")
	backend := flag.String("backend", "", backendFlagUsage)
	emit := flag.String("emit", "", "output form for the selected -target, instead of its default. `core-module` emits a raw wasm core module (runnable via `wasmtime run --invoke <fn>`) instead of composing a component; `command-module` emits a WASI preview-1 COMMAND module — the same core bytes plus a `_start` that runs main and exits with its value, which is what a preview-1 host (`wasmtime run`, or a browser shim like web/wasi-shim.js) runs directly. Both are the wasm targets only. Replaces the old `-target wasm-bin` spelling: an output format is a property of the artifact, not of the machine it runs on, so it does not belong in the target name.")
	embedDir := flag.String("embed", "", "embed a directory of assets into the binary at compile time. `__fern_asset(\"NAME\")` in the source is replaced with a string literal holding that file's bytes, where NAME is the file's slash-separated path relative to DIR. Assets are ordinary string literals: immortal (no refcount traffic), zero-copy to hand to user code, and NUL-safe, so binary assets (images, fonts, wasm) work unchanged.")
	emitDebug := flag.Bool("g", false, "emit a symbol table and DWARF into the native binary so debuggers, nm, backtraces, and profilers can map code addresses to function names and source lines")
	doFmt := flag.Bool("fmt", false, "format the source files and write them to stdout in order (use -w to write back in place, -d to print a diff, -o OUT with a single file)")
	writeBack := flag.Bool("w", false, "with -fmt, overwrite each input file with its formatted output")
	diffMode := flag.Bool("d", false, "with -fmt, print a unified diff between each file and its formatted form; exits 1 when any differs")
	doResolve := flag.Bool("resolve", false, "run Minimum Version Selection over a fern.toml's versioned ([package] index) dependencies and write the chosen versions to fern.lock (pass the manifest, its directory, or any file inside the package; default `.`). url-sourced versions are fetched and verified into the content-addressed store. The build reads fern.lock; the compiler never reads the index.")
	doVendor := flag.Bool("vendor", false, "flatten the transitive dependency graph of a fern.toml (pass the manifest, its directory, or any file inside the package; default `.`) into <root>/vendor/<name>/, one directory per package. After vendoring, builds are fully offline — the loader resolves declared dependencies out of vendor/ and never touches the network or the deps' original path/url locations. url dependencies must be fetched (`fern -fetch`) first; vendoring copies from the store.")
	doAdd := flag.Bool("add", false, "add a dependency to the nearest fern.toml: `fern -add NAME SPEC [DIR]` where SPEC is `path:../dir`, `url:https://…/pkg.tar.gz` (the archive is fetched and its sha256 recorded automatically — no hand-computed hash), or `workspace` (a `{ workspace = true }` member dep). DIR (default `.`) selects the package whose fern.toml to edit. The manifest is edited textually so comments and formatting survive.")
	doFetch := flag.Bool("fetch", false, "download the url+hash dependencies declared by a fern.toml (pass the manifest, its directory, or any file inside the package; default `.`) into the content-addressed package store, verifying each archive against its declared sha256 before unpacking. Transitive: path dependencies' manifests are fetched too. It, `-add url:` and `-resolve` are the commands that download packages — build/check/interp read the store and error when a url dependency hasn't been fetched.")
	doLint := flag.Bool("lint", false, "lint FILE.fern or every .fern source under a directory, reporting code that compiles but reads badly (start with `fern -lint-rules`). Parse-only: no type-checking, no import resolution, so a file with a type error still lints and a whole tree costs one parse per file. Severities come from the governing fern.toml's [lint] table and then -lint-set; a `// fern-lint: allow RULE` comment silences one site. Exits 1 when a rule set to `deny` fires; a `warn` finding prints and exits 0.")
	listLintRulesFlag := flag.Bool("lint-rules", false, "list the lint rules with their default severity, description, and tunable options, then exit.")
	var lintSets repeatedString
	flag.Var(&lintSets, "lint-set", "with -lint: set one rule's severity, `RULE=allow|warn|deny`. Repeatable; wins over the manifest's [lint] table.")
	var lintOpts repeatedString
	flag.Var(&lintOpts, "lint-opt", "with -lint: set one rule option, `RULE.OPTION=VALUE` (e.g. cyclomatic-complexity.max=20). Repeatable; wins over the manifest's [lint.options] table.")
	doCheck := flag.Bool("check", false, "type-check FILE.fern (or `-` for stdin) and its transitive imports. No codegen, no link, no binary. Silent on success; prints formatted diagnostics and exits 1 on the first error.")
	doAppendReport := flag.Bool("append-report", false, "print every `.append` site in FILE.fern and its transitive imports and whether the retained Go IR grows the array in place or copies it, with the rule that decided. A copying append reallocates and copies the whole buffer, so one inside a loop is O(n\u00b2) bytes (#4838). `.with` is not listed: it tests the refcount at run time, so there is no compile-time decision to report. This is the retained Go analysis, not the primary compiler, which can grow in place an append reported here as copying (#11251). Report mode; no codegen.")
	doArrayReport := flag.Bool("array-report", false, "print the `std/array` combinator pipelines FILE.fern contains, as the IR recognises them: each chain's stages, what each stage does to the element count, and the element function it applies. A chain breaks where an intermediate is read by anything but the next stage, so a value used twice reports as two pipelines rather than one — which is the honest answer, since there is no single traversal there to fuse. Recognition only: nothing about the emitted code changes (#9730). Report mode; no codegen.")
	doCapabilities := flag.Bool("capabilities", false, "print the per-package capability usage of FILE.fern and its transitive imports — one line per package (fern.toml package name, or `(root)` when no manifest governs the program): the v1 capabilities (net, fs, env, subprocess, time, random) its declared functions can reach by call-graph reachability, with an example call chain down to the tagged runtime builtin. Stdlib usage is attributed to the calling package. The report itself enforces nothing; manifests' `capabilities` grants are enforced (E070) on the compile/-check/-interp paths (docs/PACKAGE-CAPABILITIES-BRIEF.md). No codegen.")

	doEffects := flag.Bool("effects", false, "print the per-FUNCTION effect row of FILE.fern and its transitive imports — the effects (net, fs, env, subprocess, time, random) each declared function can reach by call-graph reachability, with an example call chain, and which effects it is charged only because it calls through a function value whose target is not statically known. Ends with the row-size distribution and the split between functions that call a tagged builtin themselves and those that only inherit. `-capabilities` answers the same question per PACKAGE; this one is per function. Report mode; nothing here enforces. No codegen. (docs/EFFECT-ROWS-BRIEF.md)")
	doTangle := flag.Bool("tangle", false, "tangle a literate FILE.fern.md (Knuth-style named chunks) into plain Fern source on stdout. Expands the root chunk `<<*>>`, resolving `<<chunk>>` references in definition order. A document using `file=PATH` blocks tangles to multiple modules, each printed under a `// ==> path <==` banner. With -o set, writes to disk instead: -o DIR receives one file per `file=` module (subdirs created as needed); a single-`<<*>>` document writes -o FILE. No codegen.")
	doWeave := flag.Bool("weave", false, "weave a literate FILE.fern.md into a cross-referenced Markdown reading document on stdout (or -o FILE) — chunk definitions get ⟨name⟩≡ labels and \"used in\" cross-references. Add -html for a self-contained, styled HTML page (highlighted code + clickable chunk references). No codegen.")
	weaveHTML := flag.Bool("html", false, "with -weave, emit a self-contained styled HTML page (embedded CSS, Fern syntax highlighting, and clickable `<<chunk>>` cross-reference links) instead of Markdown.")
	tangleChunk := flag.String("chunk", "", "with -tangle, expand and print only the named chunk (e.g. -chunk 'the main loop') instead of the <<*>> root — for inspecting or extracting one chunk. Works on single- and multi-file documents.")
	doDoctest := flag.Bool("doctest", false, "run the `test`-directive example blocks in a literate FILE.fern.md. Each ```fern test block is tangled (its `<<refs>>` expand against the document's chunks) into a standalone program, compiled, and run; exit 0 = pass. Results print as TAP; the command exits non-zero if any example fails.")
	showVersion := flag.Bool("version", false, "print the commit this binary was built from (plus the Go version and platform) and exit — the nightly tag rolls, so this is how to say which build you have")
	listTargets := flag.Bool("targets", false, "list the supported -target= values with their descriptions + capability surface, then exit. Surfaces the Platform-descriptor table (internal/pkg/platforms) as the canonical source of truth for what each target accepts.")
	explain := flag.String("explain", "", "print the long-form explanation for an error code (e.g. -explain E001) and exit. Pass an empty string with no other args to list the available codes.")
	colorMode := flag.String("color", "auto", "colourise diagnostics: auto (default — colour only when stderr is a terminal and NO_COLOR is unset), always, or never.")
	asciiBoxes := flag.Bool("ascii", false, "with coloured diagnostics, draw the gutter with a plain `|` instead of the box-drawing `│` (also selected automatically when the locale isn't UTF-8).")
	sanitize := flag.Bool("sanitize", false, "debug build: turn on the heap memory-safety detectors together (native x86-64/arm64). Reports an rc over-release (double free) and a use-after-free of a quarantined block as a named `fern-sanitizer:` line on stderr followed by a fatal SIGILL, and prints a leak census at exit. Costs allocation throughput and never recycles a freed block, so it is for debugging, not for shipping; an unsanitized build is byte-identical to one from a compiler without the feature. Same as FERN_SANITIZE=1.")
	cover := flag.Bool("cover", false, "measurement build: instrument every executable source line with a hit counter, and every conditional (`if`, `while`, `&&`, `||`) with a second pair, then write the whole table to stderr at exit — one `fern-cover: <file>:<line> <count>` row per instrumented line, hit or not, plus `fern-branch:` rows carrying each conditional's evaluations and true-arm entries. Feed the saved stream to `fern -cover-report` for per-file line AND branch totals, the uncovered lists, or lcov. Costs an increment per line and per branch edge, and keeps functions nothing calls (so they report 0), so it is for measuring a test run, not for shipping; a build without it is byte-identical to one from a compiler without the feature. Native x86-64 and arm64 only — any other target errors rather than emitting an uninstrumented binary. Same as FERN_COVER=1.")
	coverReport := flag.Bool("cover-report", false, "read a saved `-cover` run's output (`fern -cover-report FILE`, or `-` for stdin) and print per-file line coverage plus the uncovered lines. Lines that don't carry the `fern-cover: ` prefix are ignored, so piping the program's whole stderr is fine. Add -lcov for an lcov tracefile instead. Report mode; no codegen.")
	lcov := flag.Bool("lcov", false, "with -cover-report, write an lcov tracefile (SF/DA/LF/LH records) instead of the human summary, for the coverage viewers that already read that format.")
	backtrace := flag.Bool("backtrace", ast.BacktraceEnabled, "emit the frame-pointer backtrace a fatal abort (bounds / arena / sanitizer) prints under its cause line, on the native x86-64 and arm64 backends. `-backtrace=false` drops the walk, the hex printer, and the \"backtrace:\" string from the binary — the size-critical opt-out; the cause line and every exit code (134 / 125 / 124) are unchanged. Same as FERN_BACKTRACE=0, which this flag defaults to.")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: fern [-target <isa>-<environment>] [-emit core-module] [-o OUTPUT] [--run] [-qemu QEMU] FILE.fern [-- ARGS...]\n       (targets: arm64-linux, arm64-darwin, arm64-android, x86-64-linux, wasm32-wasi, wasm32-wasi-http; `fern -targets` for all)")
		fmt.Fprintln(os.Stderr, "       fern -fmt [-w | -d | -o OUT] FILE...")
		fmt.Fprintln(os.Stderr, "       fern -check FILE.fern | fern -check -      (type-check only; stdin form)")
		fmt.Fprintln(os.Stderr, "       fern -repl")
		fmt.Fprintln(os.Stderr, "       fern -interp FILE.fern | fern -interp -    (read from stdin)")
		fmt.Fprintln(os.Stderr, "       fern -lint PATH...                         (lint .fern sources; `fern -lint-rules` lists the rules)")
		fmt.Fprintln(os.Stderr, "       fern -capabilities FILE.fern               (per-package capability usage report)")
		fmt.Fprintln(os.Stderr, "       fern -tangle FILE.fern.md                  (literate: emit tangled Fern source)")
		fmt.Fprintln(os.Stderr, "       fern -weave  FILE.fern.md                  (literate: emit woven Markdown)")
		fmt.Fprintln(os.Stderr, "       fern -targets                                (list supported targets + capabilities)")
		flag.PrintDefaults()
	}
	flag.Parse()
	// The instrumentation is a lowering pass, and neither the AST
	// interpreter nor the REPL lowers anything. Accepting -cover there would
	// run the program and print no report — silence a reader would take for
	// "nothing was covered".
	if *cover && (*doInterp || *repl) {
		fmt.Fprintln(os.Stderr, "fern: -cover instruments compiled code; -interp and -repl do not lower, so they have no coverage to report")
		os.Exit(2)
	}
	if *embedDir != "" {
		set, err := embed.Load(*embedDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fern:", err)
			os.Exit(1)
		}
		embeddedAssets = set
	}

	// Diagnostics colourise per -color (docs/DIAGNOSTIC-UX-RESEARCH.md
	// Rec §7). Decided once, up front, so every diag.Format call below
	// inherits it. Default "auto" keeps piped / redirected output plain
	// (and honours NO_COLOR), so scripts and the test harnesses see the
	// same text as always.
	diag.SetColor(shouldColorize(*colorMode))
	diag.SetASCII(shouldUseASCII(*asciiBoxes))

	if *showVersion {
		fmt.Println(versionString())
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

	explainSet := false
	flag.Visit(func(f *flag.Flag) { explainSet = explainSet || f.Name == "explain" })
	if explainSet {
		os.Exit(runExplain(*explain, os.Stdout, os.Stderr))
	}

	if *repl {
		if err := interp.REPL(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
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
		// Only an EXPLICIT -target enforces: the flag defaults to
		// arm64, and a bare `fern -check` must keep meaning "does this
		// type-check" rather than silently gaining a capability gate.
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
			fmt.Fprintln(os.Stderr, "usage: fern -cover-report FILE   (FILE is a saved -cover run's output; `-` reads stdin)")
			os.Exit(2)
		}
		if err := runCoverReport(flag.Arg(0), *lcov, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *doAppendReport {
		if flag.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "usage: fern -append-report FILE.fern")
			os.Exit(2)
		}
		if err := runAppendReport(flag.Arg(0), os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *doArrayReport {
		if flag.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "usage: fern -array-report FILE.fern")
			os.Exit(2)
		}
		if err := runArrayReport(flag.Arg(0), os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *listLintRulesFlag {
		listLintRules(os.Stdout)
		return
	}

	if *doLint {
		if flag.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "usage: fern -lint PATH... (a .fern file or a directory of them)")
			os.Exit(2)
		}
		if code := runLint(flag.Args(), lintSets, lintOpts, os.Stdout); code != 0 {
			os.Exit(code)
		}
		return
	}

	if *doCapabilities {
		if flag.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "usage: fern -capabilities FILE.fern")
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
			fmt.Fprintln(os.Stderr, "usage: fern -effects FILE.fern")
			os.Exit(2)
		}
		if err := runEffects(flag.Arg(0), os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
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
		if flag.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "usage: fern -add NAME SPEC   (SPEC = path:../dir | url:https://… | workspace)")
			os.Exit(1)
		}
		addDir := "."
		if flag.NArg() >= 3 {
			addDir = flag.Arg(2)
		}
		if err := runAdd(flag.Arg(0), flag.Arg(1), addDir); err != nil {
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

	if (*doTangle || *doWeave) && flag.NArg() >= 1 {
		code, err := runLiterateTool(flag.Arg(0), *doTangle, *out, *tangleChunk, *weaveHTML)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(code)
	}

	if *doDoctest && flag.NArg() >= 1 {
		code, err := runDoctests(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(code)
	}

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	srcPath := flag.Arg(0)

	// -fmt takes a file LIST, not a program tail: every positional is an
	// input, so it must not go through programArgs.
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
		cover: *cover, optimize: *optimize, progArgs: progArgs,
	}
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "backtrace" {
			req.backtrace = backtrace
		}
	})
	code, err := compileSelfHost(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fern:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// formatChunkBody formats one literate chunk body. A body is formattable
// when it parses either as a complete program (top-level declarations) or
// — wrapped in a synthetic function — as a statement list. Bodies that
// parse as neither (fragments split mid-construct, or bodies containing
// `<<ref>>` chunk references, which aren't valid Fern) are declined with
// ok=false so FormatCode keeps them verbatim. Whitespace-only bodies are
// left as-is.
func formatChunkBody(code string) (string, bool) {
	if strings.TrimSpace(code) == "" {
		return "", false
	}
	if prog, err := parser.Parse(code); err == nil {
		return strings.TrimRight(printer.Format(prog), "\n"), true
	}
	// Retry as a statement list wrapped in a throwaway function.
	const open = "function __fern_fmt_wrap__() {\n"
	if prog, err := parser.Parse(open + code + "\n}\n"); err == nil {
		return unwrapFormattedBody(printer.Format(prog)), true
	}
	return "", false
}

// unwrapFormattedBody strips the synthetic `function __fern_fmt_wrap__()`
// wrapper that formatChunkBody added, returning the body de-indented by
// one level so it sits at the chunk's own column.
func unwrapFormattedBody(formatted string) string {
	lines := strings.Split(strings.TrimRight(formatted, "\n"), "\n")
	if len(lines) < 2 {
		return "" // empty wrapped block: `function …() {}`
	}
	inner := lines[1 : len(lines)-1] // drop the `function …{` and `}` lines
	for i, ln := range inner {
		inner[i] = strings.TrimPrefix(ln, formatIndentUnit)
	}
	return strings.Join(inner, "\n")
}

// formatIndentUnit is one level of the formatter's indentation, stripped
// when unwrapping a wrapped chunk body.
const formatIndentUnit = "  "

// formatFiles is `-fmt` over a file list, gofmt-style: every file is
// processed in order even when an earlier one fails, and the exit code is
// the worst one seen (1 for any error or, under -d, any non-empty diff).
// -o names one output file, so it is refused with more than one input.
func formatFiles(paths []string, writeBack, diffMode bool, outPath string) int {
	if outPath != "" && len(paths) > 1 {
		fmt.Fprintf(os.Stderr, "fern: -fmt -o takes a single input file (%d given)\n", len(paths))
		return 2
	}
	code := 0
	for _, p := range paths {
		c, err := formatFile(p, writeBack, diffMode, outPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			c = 1
		}
		if c > code {
			code = c
		}
	}
	return code
}

// formatFile parses the file at srcPath, formats it, and either
// writes the result to stdout / back to the file / to outPath / prints a
// unified diff against the on-disk version. Returns the exit code the CLI
// should use: 0 for "no work needed" or successful write, 1 when
// `-d` saw a difference, with errors returned separately.
func formatFile(srcPath string, writeBack, diffMode bool, outPath string) (int, error) {
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return 1, err
	}
	src := string(srcBytes)
	var formatted string
	if isLiterate(srcPath) {
		// A literate document reformats the fern code inside each chunk
		// (leaving prose, fences, and headers untouched); fragments /
		// `<<ref>>`-bearing chunks the formatter can't parse stay verbatim.
		formatted = literate.Parse(src).FormatCode(formatChunkBody)
	} else {
		formatted, err = fmtsource.Format(src)
		if err != nil {
			return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
		}
	}
	if diffMode {
		diff := printer.UnifiedDiff(src, formatted, srcPath, srcPath)
		if diff == "" {
			return 0, nil
		}
		_, err := os.Stdout.WriteString(diff)
		return 1, err
	}
	if writeBack {
		// The only irreversible path: refuse to overwrite the input with
		// output the parser rejects. A printer that drops a node it has no
		// case for (#6803) produces exactly that, and a file is not
		// recoverable from a diagnostic printed after the write.
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

// runInterp parses srcPath (or stdin when srcPath is "-"), runs the
// pre-codegen passes (constfold + checker + monomorph) on it, and
// invokes `main()` through the AST interpreter. The returned int
// is `main()`'s result clamped to 0..255 — typical script-mode
// semantics. Returns an error if any pipeline stage fails or the
// program has no `main` to call.
//
// Stdin support is intentionally simple: read the whole stream into
// memory and load it as the entry module, whose std/ and core/ imports
// resolve; a relative import has no directory to resolve against.
func runInterp(srcPath string, argv []string) (int, error) {
	var prog *ast.Program
	var formatErr func(error) error
	if srcPath == "-" {
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return 1, fmt.Errorf("read stdin: %w", err)
		}
		src := string(buf)
		// modload.LoadSource (not bare parser.Parse) so a piped
		// program's std/ + core/ imports resolve — the auto-prelude
		// is gone, so stdlib is in scope only when imported.
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
	// argv[0] is conventionally the program path so `args()`
	// matches the C / Go shape. Subsequent entries are the
	// user's own arguments, written after FILE on the fern
	// command line (see programArgs).
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
	// Clamp main's return value to a process exit code. The AST
	// interpreter wraps i32 in interp.Number; void main returns
	// interp.Void. Anything else is a misuse — return 0 + a warning
	// rather than panic.
	if n, ok := v.(interp.Number); ok {
		// POSIX exit status is the low 8 bits of the value passed to
		// exit() — two's complement for a negative value — so the compiled
		// backends exit -3 as 253 (0xFD), -1 as 255. `& 0xFF` on a Go int
		// already yields that low byte, so a negative return must NOT be
		// abs'd first: `code = -code` gave -3 -> 3, diverging from every
		// compiled backend (and from POSIX). Match the backends directly.
		return int(n) & 0xFF, nil
	}
	return 0, nil
}

// runCheck loads one entry module ("-" reads stdin) and runs gates.Check on
// it, printing its warnings. target is the -target value when the user
// passed one explicitly and "" otherwise.
func runCheck(srcPath, target string) error {
	var prog *ast.Program
	var formatErr func(error) error
	if srcPath == "-" {
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		src := string(buf)
		// modload.LoadSource so a piped program's std/ + core/
		// imports resolve now that the auto-prelude is gone.
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

// execDirect runs binPath natively (host arch matches the build target),
// returning the program's exit code so the caller can mirror it.
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

// execUnderQemu runs binPath through the supplied user-mode emulator
// with stdio passed through. The first return is the program's exit
// code (so the caller can mirror it as the fern process exit code).
func execUnderQemu(qemu, binPath string, progArgs []string) (int, error) {
	if _, err := exec.LookPath(qemu); err != nil {
		return 1, fmt.Errorf("emulator %q not found on PATH (override with -qemu): %w", qemu, err)
	}
	args := append([]string{binPath}, progArgs...)
	cmd := exec.Command(qemu, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode(), nil
	}
	return 1, err
}

// runExplain prints the explanation for code, or the list of codes when
// code is empty, and returns the exit status.
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
