// Command fern compiles a single .fern source file.
//
// Usage:
//
//	fern FILE.fern                       # write arm64 Linux assembly to stdout
//	fern -o OUTPUT FILE.fern             # link with the aarch64 cross-
//	                                     # compiler and write a static ELF
//	                                     # binary
//	fern --run FILE.fern [-- ARGS...]    # link to a temporary binary and
//	                                     # execute it under qemu-aarch64
//	                                     # (forwarding stdio)
//	fern -fmt FILE.fern                  # write idiomatic, indented source
//	                                     # to stdout (use -w to overwrite
//	                                     # the input file in place; use -d
//	                                     # to print a unified diff against
//	                                     # the on-disk version and exit
//	                                     # non-zero when they differ). A
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
// A literate `FILE.fern.md` may be passed to any of the compile / -run /
// -check / -interp modes directly: it is tangled in memory first, and
// diagnostics are mapped back to the lines you wrote in the document.
//
// The -cc and -qemu flags override the linker and emulator.
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
	"runtime"
	"sort"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	arm64ssa "github.com/jakechampion/lang/internal/codegen/arm64ssa"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/codegen/wasmssa"
	x86_64codegen "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/embed"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/literate"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativemacho "github.com/jakechampion/lang/internal/native/macho"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
	"github.com/jakechampion/lang/internal/printer"
	"github.com/jakechampion/lang/internal/ssa"
	"github.com/jakechampion/lang/internal/treeshake"
	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/componenttype"
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

// emitDebugSyms is set from the -g flag: emit a static .symtab into native
// binaries so debuggers / nm / backtraces / profilers can name code addresses.
var emitDebugSyms bool

// embeddedAssets is set from the -embed flag: the asset bundle that
// __fern_asset("name") resolves against during const folding. nil when
// -embed was not passed, which makes any use of the builtin an error.
// It is compile-time state shared by the check / interp / build paths
// rather than a parameter because `run` already carries fifteen.
var embeddedAssets *embed.Set

// sanitizerTargets are the -target values whose backend emits the #5545 heap
// detectors. The arm64 family shares one generator, so android / darwin ride
// along with plain arm64 (with that backend's coverage: census + rc
// over-release, no use-after-free quarantine). The SSA-direct and wasm
// backends carry no instrumentation, so -sanitize on those is a warning, not
// a silently unchecked build.
var sanitizerTargets = map[string]bool{
	"x86-64":        true,
	"arm64":         true,
	"arm64-android": true,
	"arm64-darwin":  true,
}

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
// `-async-provider a=x.wasm -async-provider b=y.wasm`).
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
		fi, err := os.Stderr.Stat()
		return err == nil && fi.Mode()&os.ModeCharDevice != 0
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
	target := flag.String("target", "arm64", "code-generation backend: arm64 (default, Linux ELF), arm64-android (arm64 Linux ELF as a static position-independent executable for Android), arm64-darwin (native Apple Silicon macOS), x86-64 (Linux ELF, in-process native backend by default), wasm (CLI component), wasi-http (HTTP handler component implementing wasi:http/incoming-handler), freestanding (no host at all — type-checks against an empty capability set; no backend emits for it yet, see docs/FREESTANDING-CORE.md)")
	cc := flag.String("cc", "", "external assembler/linker invoked when -o or --run is set. arm64/x86-64 Linux and arm64-darwin all default to the in-process native backend (no external toolchain); passing -cc opts out to it (e.g. aarch64-linux-gnu-gcc / x86_64-linux-gnu-gcc on Linux, clang on darwin).")
	runIt := flag.Bool("run", false, "link to a temporary binary and execute it (arm64 Linux only; uses qemu-aarch64 when not on an arm64 host)")
	optimize := flag.Bool("O", false, "release build: elide every assert() check after type-checking (the condition is not evaluated, so asserts must be side-effect-free). Applies to compiled output; -interp and -check always keep asserts.")
	native := flag.Bool("native", false, "force the in-process pure-Go assembler+linker (internal/native). Already the DEFAULT for arm64/x86-64 Linux and arm64-darwin, so the flag is only needed to override an explicit -cc. No external assembler or linker; errors clearly on any unsupported instruction (pass -cc to fall back to an external toolchain).")
	shared := flag.Bool("shared", false, "emit a shared object (.so) instead of an executable — a position-independent ET_DYN with a dynamic symbol table exporting the -export functions, loadable via dlopen / Android's System.loadLibrary. Native ELF targets only (x86-64, arm64, arm64-android); requires -o.")
	export := flag.String("export", "", "with -shared: comma-separated function names to export in the .so (default: main). Each must be a defined top-level function; it becomes a dynamic symbol resolvable by the loader.")
	qemu := flag.String("qemu", "qemu-aarch64", "user-mode emulator used by --run")
	repl := flag.Bool("repl", false, "start an interactive REPL via the AST interpreter")
	doInterp := flag.Bool("interp", false, "run FILE.fern (or `-` for stdin) through the AST interpreter — no codegen, no link, no binary. main()'s return value becomes the process exit code (clamped to 0..255). State is fresh per invocation; the REPL flag keeps an interactive session across lines.")
	backend := flag.String("backend", "", "alternate code-generation backend for the selected -target, instead of its default emitter. `ssa` selects the SSA-direct backend (register allocation instead of the stack-machine emitter, so the emitted .text is markedly smaller), available for -target wasm and -target arm64. Coverage is a subset of the language — the integer core, control flow, calls, memory, strings, arrays, and the RC runtime — and an unsupported op errors rather than miscompiles. Unlike the old `-target wasm-ssa` / `-target arm64-ssa` spellings this replaces, the target keeps its descriptor, so capability enforcement (E066) applies here exactly as it does to the default emitter.")
	componentWrap := flag.Bool("component-wrap", false, "with -target wasm-bin: wrap the core module as a self-contained preview-2 component via internal/wasm/component (no wasm-tools shell-out, no preview-1 adapter). Lifts main() as a component-level u32-returning export. Supports any mix of the migrated preview-2 imports; unrecognised imports surface a clear error.")
	componentWrapCli := flag.Bool("component-wrap-cli", false, "like -component-wrap but emits the wasi:cli/run@0.2.0 export shape so the produced component runs under plain `wasmtime run prog.wasm` (no --invoke). main()'s return value lowers to result<_, _>: 0 = ok, non-zero = err. void main is supported (auto-wrapped to return 0). Same WASI coverage as -component-wrap.")
	asyncExport := flag.Bool("async-export", false, "with -target wasm-bin: wrap the core as a WASI Preview-3 component-model-async component exporting `run: async func() -> u32` (lifted from main, which must return i32). The result is delivered via `canon task.return`. Run with `wasmtime run -W component-model-async,component-model-async-stackful --invoke 'run()'`. See docs/WASI-PREVIEW3-ASYNC-PLAN.md.")
	var asyncProviders repeatedString
	flag.Var(&asyncProviders, "async-provider", "with -target wasm-bin and an `@import async` (WASI Preview-3) program: bundle a pre-built provider *component* (.wasm) that exports the matching async function, so the result is a single self-contained runnable component (no separate host needed). Repeatable: `WITNAME=PATH` maps a provider to the async import whose WIT name is WITNAME; a single bare `PATH` is shorthand when the program has exactly one async import. Each provider must export its WITNAME. Scalar params + scalar result only (e.g. `@import(\"iface\",\"name\") async function add(a: i32, b: i32): i32;`). See docs/WASI-PREVIEW3-ASYNC-PLAN.md.")
	embedDir := flag.String("embed", "", "embed a directory of assets into the binary at compile time. `__fern_asset(\"NAME\")` in the source is replaced with a string literal holding that file's bytes, where NAME is the file's slash-separated path relative to DIR. Assets are ordinary string literals: immortal (no refcount traffic), zero-copy to hand to user code, and NUL-safe, so binary assets (images, fonts, wasm) work unchanged.")
	emitDebug := flag.Bool("g", false, "emit a static symbol table (.symtab) into the native binary so debuggers, nm, backtraces, and profilers can map code addresses to function names")
	doFmt := flag.Bool("fmt", false, "format the source file and write to stdout (use -w to write back in place, -d to print a diff)")
	writeBack := flag.Bool("w", false, "with -fmt, overwrite the input file with the formatted output")
	diffMode := flag.Bool("d", false, "with -fmt, print a unified diff between the file and its formatted form; exits 1 when they differ")
	doResolve := flag.Bool("resolve", false, "run Minimum Version Selection over a fern.toml's versioned ([package] index) dependencies and write the chosen versions to fern.lock (pass the manifest, its directory, or any file inside the package; default `.`). url-sourced versions are fetched and verified into the content-addressed store. The build reads fern.lock; the compiler never reads the index.")
	doVendor := flag.Bool("vendor", false, "flatten the transitive dependency graph of a fern.toml (pass the manifest, its directory, or any file inside the package; default `.`) into <root>/vendor/<name>/, one directory per package. After vendoring, builds are fully offline — the loader resolves declared dependencies out of vendor/ and never touches the network or the deps' original path/url locations. url dependencies must be fetched (`fern -fetch`) first; vendoring copies from the store.")
	doAdd := flag.Bool("add", false, "add a dependency to the nearest fern.toml: `fern -add NAME SPEC [DIR]` where SPEC is `path:../dir`, `url:https://…/pkg.tar.gz` (the archive is fetched and its sha256 recorded automatically — no hand-computed hash), or `workspace` (a `{ workspace = true }` member dep). DIR (default `.`) selects the package whose fern.toml to edit. The manifest is edited textually so comments and formatting survive.")
	doFetch := flag.Bool("fetch", false, "download the url+hash dependencies declared by a fern.toml (pass the manifest, its directory, or any file inside the package; default `.`) into the content-addressed package store, verifying each archive against its declared sha256 before unpacking. Transitive: path dependencies' manifests are fetched too. This is the ONLY command that touches the network — build/check/interp read the store and error when a url dependency hasn't been fetched.")
	doCheck := flag.Bool("check", false, "type-check FILE.fern (or `-` for stdin) and its transitive imports. No codegen, no link, no binary. Silent on success; prints formatted diagnostics and exits 1 on the first error.")
	doCapabilities := flag.Bool("capabilities", false, "print the per-package capability usage of FILE.fern and its transitive imports — one line per package (fern.toml package name, or `(root)` when no manifest governs the program): the v1 capabilities (net, fs, env, subprocess, time, random) its declared functions can reach by call-graph reachability, with an example call chain down to the tagged runtime builtin. Stdlib usage is attributed to the calling package. The report itself enforces nothing; manifests' `capabilities` grants are enforced (E070) on the compile/-check/-interp paths (docs/PACKAGE-CAPABILITIES-BRIEF.md). No codegen.")
	doTangle := flag.Bool("tangle", false, "tangle a literate FILE.fern.md (Knuth-style named chunks) into plain Fern source on stdout. Expands the root chunk `<<*>>`, resolving `<<chunk>>` references in definition order. A document using `file=PATH` blocks tangles to multiple modules, each printed under a `// ==> path <==` banner. With -o set, writes to disk instead: -o DIR receives one file per `file=` module (subdirs created as needed); a single-`<<*>>` document writes -o FILE. No codegen.")
	doWeave := flag.Bool("weave", false, "weave a literate FILE.fern.md into a cross-referenced Markdown reading document on stdout (or -o FILE) — chunk definitions get ⟨name⟩≡ labels and \"used in\" cross-references. Add -html for a self-contained, styled HTML page (highlighted code + clickable chunk references). No codegen.")
	weaveHTML := flag.Bool("html", false, "with -weave, emit a self-contained styled HTML page (embedded CSS, Fern syntax highlighting, and clickable `<<chunk>>` cross-reference links) instead of Markdown.")
	tangleChunk := flag.String("chunk", "", "with -tangle, expand and print only the named chunk (e.g. -chunk 'the main loop') instead of the <<*>> root — for inspecting or extracting one chunk. Works on single- and multi-file documents.")
	doDoctest := flag.Bool("doctest", false, "run the `test`-directive example blocks in a literate FILE.fern.md. Each ```fern test block is tangled (its `<<refs>>` expand against the document's chunks) into a standalone program, compiled, and run; exit 0 = pass. Results print as TAP; the command exits non-zero if any example fails.")
	showVersion := flag.Bool("version", false, "print the commit this binary was built from (plus the Go version and platform) and exit — the nightly tag rolls, so this is how to say which build you have")
	listTargets := flag.Bool("targets", false, "list the supported -target= values with their descriptions + capability surface, then exit. Surfaces the Platform-descriptor table (internal/platforms) as the canonical source of truth for what each target accepts.")
	explain := flag.String("explain", "", "print the long-form explanation for an error code (e.g. -explain E001) and exit. Pass an empty string with no other args to list the available codes.")
	colorMode := flag.String("color", "auto", "colourise diagnostics: auto (default — colour only when stderr is a terminal and NO_COLOR is unset), always, or never.")
	asciiBoxes := flag.Bool("ascii", false, "with coloured diagnostics, draw the gutter with a plain `|` instead of the box-drawing `│` (also selected automatically when the locale isn't UTF-8).")
	sanitize := flag.Bool("sanitize", false, "debug build: turn on the heap memory-safety detectors together (native x86-64/arm64). Reports an rc over-release (double free) and a use-after-free of a quarantined block as a named `fern-sanitizer:` line on stderr followed by a fatal SIGILL, and prints a leak census at exit. Costs allocation throughput and never recycles a freed block, so it is for debugging, not for shipping; an unsanitized build is byte-identical to one from a compiler without the feature. Same as FERN_SANITIZE=1.")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: fern [-target arm64|arm64-android|arm64-darwin|x86-64|wasm] [-o OUTPUT] [--run] [-cc CC] [-qemu QEMU] FILE.fern [-- ARGS...]")
		fmt.Fprintln(os.Stderr, "       fern -fmt [-w | -d] FILE.fern")
		fmt.Fprintln(os.Stderr, "       fern -check FILE.fern | fern -check -      (type-check only; stdin form)")
		fmt.Fprintln(os.Stderr, "       fern -repl")
		fmt.Fprintln(os.Stderr, "       fern -interp FILE.fern | fern -interp -    (read from stdin)")
		fmt.Fprintln(os.Stderr, "       fern -capabilities FILE.fern               (per-package capability usage report)")
		fmt.Fprintln(os.Stderr, "       fern -tangle FILE.fern.md                  (literate: emit tangled Fern source)")
		fmt.Fprintln(os.Stderr, "       fern -weave  FILE.fern.md                  (literate: emit woven Markdown)")
		fmt.Fprintln(os.Stderr, "       fern -targets                                (list supported targets + capabilities)")
		flag.PrintDefaults()
	}
	flag.Parse()
	emitDebugSyms = *emitDebug
	if *sanitize {
		// The backends read the component detector flags directly, so a
		// late -sanitize has to be pushed down into them; ApplySanitize
		// only ever turns flags on, so FERN_SANITIZE=1 -sanitize and any
		// individually-set FERN_* flag all compose.
		ast.SanitizeEnabled = true
		ast.ApplySanitize()
		// Only the two mainline native backends carry the detectors. Say
		// so rather than accepting the flag and emitting nothing —
		// silence here reads as "sanitizer ran, program is clean", which
		// is the one wrong conclusion this mode must never support.
		if !sanitizerTargets[*target] {
			fmt.Fprintf(os.Stderr, "fern: warning: -sanitize has no effect on -target %s (native x86-64 and arm64 only); this build carries no checks\n", *target)
		}
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

	if *explain != "" {
		body := diag.Explain(*explain)
		if body == "" {
			fmt.Fprintf(os.Stderr, "unknown error code %q\navailable codes: %v\n", *explain, diag.AvailableCodes())
			os.Exit(1)
		}
		fmt.Print(diag.FormatExplain(*explain, body))
		return
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
			// Anything after the source path is forwarded to the
			// program through `args()`. Mirrors the compile-and-
			// run path's flag.Args()[1:] behaviour so test runners
			// can do `fern -interp test.fern -- --filter foo`
			// without going through env vars. Strip a literal `--`
			// separator if present — Go's `flag` package doesn't
			// consume it, but conventional `program -- args`
			// invocations expect it to disappear.
			rest := flag.Args()[1:]
			if len(rest) > 0 && rest[0] == "--" {
				rest = rest[1:]
			}
			interpArgs = rest
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
	progArgs := flag.Args()[1:] // anything after the source path is forwarded to the program

	if *doFmt {
		code, err := formatFile(srcPath, *writeBack, *diffMode)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(code)
	}

	if *componentWrap && *componentWrapCli {
		fmt.Fprintln(os.Stderr, "-component-wrap and -component-wrap-cli are mutually exclusive")
		os.Exit(1)
	}
	if *asyncExport && (*componentWrap || *componentWrapCli) {
		fmt.Fprintln(os.Stderr, "-async-export is mutually exclusive with -component-wrap / -component-wrap-cli")
		os.Exit(1)
	}
	code, err := run(srcPath, *out, *target, *backend, *cc, *runIt, *native, *qemu, *componentWrap, *componentWrapCli, *asyncExport, asyncProviders, *shared, *export, *optimize, progArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
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

// formatFile parses the file at srcPath, formats it, and either
// writes the result to stdout / back to the file / prints a unified
// diff against the on-disk version. Returns the exit code the CLI
// should use: 0 for "no work needed" or successful write, 1 when
// `-d` saw a difference, with errors returned separately.
func formatFile(srcPath string, writeBack, diffMode bool) (int, error) {
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
		prog, err := parser.Parse(src)
		if err != nil {
			return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
		}
		formatted = printer.Format(prog)
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
		info, err := os.Stat(srcPath)
		if err != nil {
			return 1, err
		}
		return 0, os.WriteFile(srcPath, []byte(formatted), info.Mode())
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
// memory, parse + check + interpret as a single file. No imports
// supported in the stdin case because modload reads files from disk
// — a file at path "-" doesn't exist. File-path callers go through
// the full modload pipeline.
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
		if err := enforceCapabilities(srcPath, prog, os.Stderr); err != nil {
			return 1, formatErr(err)
		}
	}
	if err := monomorph.Run(prog, info); err != nil {
		return 1, formatErr(err)
	}

	ip := interp.New()
	// argv[0] is conventionally the program path so `args()`
	// matches the C / Go shape. Subsequent entries are the
	// user's own arguments, passed after `--` on the fern
	// command line.
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

// runCheck parses srcPath (or stdin when srcPath is "-"), runs the
// pre-codegen pipeline (constfold + checker + monomorph), and returns
// nil iff the program type-checks cleanly. Unlike runInterp, this does
// not require a `main` — library packages should check successfully.
// Errors come back already formatted with diag.Format so the caller
// can print them straight to stderr.
//
// Stdin form ("-"): the whole stream is read into memory and parsed
// as a single file with no import resolution (modload reads from
// disk; the synthetic "-" path has no on-disk source). File-path
// callers go through the full modload pipeline so transitive imports
// are checked too.
// enforceTargetCapabilities runs the platforms Phase 2 gate (E066):
// reject, before codegen, any call to a runtime builtin the target's
// descriptor doesn't grant (`subprocess` on wasm, filesystem/tcp/stdin
// under wasi-http, anything host-mediated under freestanding) — turning
// mid-build "undefined label"/"unsupported" failures into positioned,
// `fern explain`-able errors. Tree-shake first so unused imported
// stdlib wrappers don't trip gates: this mirrors each backend's own
// pre-shake (same dyn-dispatch roots + -shared exports; backends
// re-shake idempotently), including wasi-http's drop of the synthesised
// tcp_serve `main` (see internal/codegen/wasmbin/build.go).
//
// Returns nil when the target has no descriptor (e.g. the experimental
// wasm-ssa) or nothing violates its capability set. NOTE this mutates
// prog by tree-shaking it.
func enforceTargetCapabilities(srcPath string, prog *ast.Program, info *checker.Info, target string, shared bool, export string) diag.Errors {
	if platforms.ForTarget(target) == nil {
		return nil
	}
	if target == "wasi-http" {
		kept := prog.Funcs[:0]
		for _, fn := range prog.Funcs {
			if fn.IsSynthesisedHandlerMain {
				continue
			}
			kept = append(kept, fn)
		}
		prog.Funcs = kept
	}
	extras := append(treeshake.DynCoercionImplMethods(info), treeshake.DowncastImplMethods(prog, info)...)
	if shared && export != "" {
		extras = append(extras, strings.Split(export, ",")...)
	}
	if target == "wasi-http" {
		extras = append(extras, "handle", "__method_HeaderMap_append")
	}
	// WIT-exported functions are entry points the AST walk can't
	// see — keep them (and what they call) in the scanned set.
	for _, fn := range prog.Funcs {
		if fn.ExportIface != "" || fn.ExportWITName != "" {
			extras = append(extras, fn.Name)
		}
	}
	treeshake.Run(prog, extras...)
	vs := platforms.Enforce(prog, target)
	if len(vs) == 0 {
		return nil
	}
	var errs diag.Errors
	for _, v := range vs {
		ce := &checker.Error{Pos: v.Pos, Msg: v.Message(srcPath), ErrCode: "E066"}
		// modload stamps the ENTRY module's functions with the entry
		// source path itself; only those positions index the file the
		// renderer displays. Violations inside imported modules
		// (std/…, ./util, …) degrade to a position-less entry — the
		// message names the function and module instead.
		if v.FuncModule != "" && v.FuncModule != srcPath {
			ce.Pos = ast.Position{}
		}
		errs = append(errs, ce)
	}
	return errs
}

// runCheck type-checks one entry module. target is the -target value
// when the user passed one explicitly and "" otherwise: an unrequested
// check must not start enforcing the arm64 capability set against
// programs that check clean today, so the gate runs only on an explicit
// `-check -target NAME`.
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
	if err := constfold.Fold(prog, embeddedAssets); err != nil {
		return formatErr(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return formatErr(err)
	}
	if srcPath != "-" {
		if err := enforceCapabilities(srcPath, prog, os.Stderr); err != nil {
			return formatErr(err)
		}
	}
	if err := monomorph.Run(prog, info); err != nil {
		return formatErr(err)
	}
	// A clean check still inventories the entry module's remaining
	// `todo` stubs — warnings on stderr, exit stays 0. Imported
	// modules' sites aren't tracked (see ast.Program.TodoSites).
	name := srcPath
	if name == "-" {
		name = "<stdin>"
	}
	for _, site := range prog.TodoSites {
		fmt.Fprintf(os.Stderr, "%s:%d:%d: warning: `todo` stub remaining\n", name, site.Line, site.Col)
	}
	// Last, because it tree-shakes prog.
	if target != "" {
		if errs := enforceTargetCapabilities(name, prog, info, target, false, ""); errs != nil {
			return formatErr(errs)
		}
	}
	return nil
}

// run drives the full pipeline. The returned int is the exit code that
// the fern process itself should exit with: 0 in compile-only mode, or
// the program's own exit code under --run.
func run(srcPath, outPath, target, backend, cc string, runIt, native bool, qemu string, componentWrap, componentWrapCli, asyncExport bool, asyncProviders []string, shared bool, export string, optimize bool, progArgs []string) (int, error) {
	e, err := loadEntry(srcPath)
	if err != nil {
		return 1, err
	}
	prog := e.prog
	if err := constfold.Fold(prog, embeddedAssets); err != nil {
		return 1, e.format(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return 1, e.format(err)
	}
	// Per-package capability grants (E070 — the manifest-boundary
	// sibling of the target-boundary E066 pass below).
	if err := enforceCapabilities(srcPath, prog, os.Stderr); err != nil {
		return 1, e.format(err)
	}
	// -O: drop assert() checks AFTER type-checking (an ill-typed assert
	// still fails a release build) and before monomorph/codegen.
	if optimize {
		constfold.ElideAsserts(prog)
	}
	if err := monomorph.Run(prog, info); err != nil {
		return 1, e.format(err)
	}

	// Capability enforcement (platforms Phase 2 — E066): reject, before
	// codegen, any call to a runtime builtin the target's descriptor
	// doesn't grant (`subprocess` on wasm, filesystem/tcp/stdin under
	// wasi-http, …) — turning mid-build "undefined label"/"unsupported"
	// failures into positioned, `fern explain`-able errors. Tree-shake
	// first so unused imported stdlib wrappers don't trip gates: this
	// mirrors each backend's own pre-shake (same dyn-dispatch roots +
	// -shared exports; backends re-shake idempotently), including
	// wasi-http's drop of the synthesised tcp_serve `main` (see
	// internal/codegen/wasmbin/build.go). Targets without a descriptor
	// (e.g. the experimental wasm-ssa) skip enforcement.
	if errs := enforceTargetCapabilities(srcPath, prog, info, target, shared, export); errs != nil {
		return 1, e.format(errs)
	}

	// A declared-but-unemitted target refuses HERE, after enforcement, so a
	// program that also violates the capability set gets the E066 naming what
	// it reached for rather than this. Without the check the target would fall
	// through to the "unknown target" error below, which would be a lie: the
	// descriptor exists and `-check` against it works.
	// `-backend` selects an alternate emitter for the SAME target, so an
	// unsupported combination is rejected by name rather than silently
	// falling through to the default emitter — which would produce a
	// working binary that is not the one asked for.
	switch backend {
	case "":
	case "ssa":
		if target != "wasm" && target != "arm64" {
			return 1, fmt.Errorf("-backend ssa is not available for -target %s (available for: arm64, wasm)", target)
		}
	default:
		return 1, fmt.Errorf("unknown -backend %q (want ssa, or omit it for the target's default emitter)", backend)
	}

	if d := platforms.ForTarget(target); d != nil && d.NoBackend {
		return 1, fmt.Errorf("-target %s: no backend emits for this target yet — `fern -check -target %s` type-checks against its capability set, but there is nothing to compile to (#6506)", target, target)
	}

	if backend == "ssa" && target == "wasm" {
		// Experimental SSA-direct backend (internal/codegen/wasmssa)
		// — lowers via parse → check → ir.LowerWith → ssa.LiftFromIR
		// → ssa.Optimize → wasmssa.EmitModule. Covers i32/i64/f32/
		// f64 programs with memory ops, string literals, recursion,
		// and the full reducible-CFG surface — see
		// internal/codegen/wasmssa/emit.go's package doc.
		//
		// Output modes:
		//   - no flag: write the raw core module to outPath. Run
		//     with `wasmtime run --invoke main module.wasm`.
		//   - -component-wrap-cli: wrap as a preview-2 component
		//     exporting wasi:cli/run@0.2.0. Run with plain
		//     `wasmtime run prog.wasm` (no --invoke). main must
		//     have signature () -> i32 for the canonical lift.
		if outPath == "" {
			return 1, fmt.Errorf("-backend ssa requires -o OUTPUT")
		}
		bin, err := buildWasmSSA(prog, info)
		if err != nil {
			return 1, fmt.Errorf("wasm/ssa: %v", err)
		}
		if componentWrapCli {
			// Wrap as a wasi:cli/run-exporting component. The
			// wasmssa module already exports `main`; lift it as
			// the component-level `run` function.
			comp := component.BuildWasiCliRunComponent(bin, "main")
			if err := os.WriteFile(outPath, comp, 0o644); err != nil {
				return 1, err
			}
			return 0, nil
		}
		if err := os.WriteFile(outPath, bin, 0o644); err != nil {
			return 1, err
		}
		return 0, nil
	}

	if backend == "ssa" && target == "arm64" {
		// Experimental SSA-direct arm64 backend (internal/codegen/arm64ssa)
		// — lowers via parse → check → ir.LowerWith → ssa.LiftFromIR →
		// ssa.Optimize → arm64ssa.EmitAsmModule, then links the same in-process
		// W^X static ELF as -target arm64 (linkNative). It uses SSA register
		// allocation instead of the stack-machine emitter, so the emitted .text
		// is markedly smaller. Coverage is a subset of the language (the integer
		// core, control flow, calls, memory, strings, arrays, and the RC runtime);
		// an unsupported op surfaces as a clean error rather than a miscompile —
		// this is the path the binary-size epic widens until the self-host
		// compiler itself can be built through it.
		if outPath == "" {
			return 1, fmt.Errorf("-backend ssa requires -o OUTPUT")
		}
		asm, err := buildArm64SSA(prog, info)
		if err != nil {
			return 1, fmt.Errorf("arm64/ssa: %v", err)
		}
		if err := linkNative(asm, outPath, "", "", nil); err != nil {
			return 1, fmt.Errorf("arm64/ssa link: %v", err)
		}
		return 0, nil
	}

	if target == "wasm-bin" {
		// Binary backend (internal/codegen/wasmbin) — produces wasm core
		// module bytes directly. Output mode:
		//   - default: write the raw core module to outPath (runnable via
		//     `wasmtime run --invoke <fn>`).
		//   - -component-wrap / -component-wrap-cli: wrap the core as a
		//     self-contained preview-2 component via internal/wasm/component
		//     (no wasm-tools, no preview-1 adapter).
		if outPath == "" {
			return 1, fmt.Errorf("-target wasm-bin requires -o OUTPUT")
		}
		// WASI Preview-3 component-model-async export, triggered either
		// by an `async function` in the source (lifted under its own
		// name) or by the `-async-export` flag (wraps main, exported as
		// `run`). The wasmbin emits the async core-func shape (call the
		// source fn → task.return → void); the composer lifts it with
		// the `async` canonical option. The source fn must return i32.
		asyncSrc, asyncExportNm := "", ""
		for _, fn := range prog.Funcs {
			// Pick the async function WITH a body to lift — never an `@import async`
			// (body-less) declaration, which is a colorless import, not the export.
			if fn.Async && fn.ImportIface == "" {
				asyncSrc, asyncExportNm = fn.Name, fn.Name
				break
			}
		}
		if asyncSrc == "" && asyncExport {
			asyncSrc, asyncExportNm = "main", "run"
		}
		if asyncSrc != "" {
			acore, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
				Preview2WASI:    true,
				AsyncExportName: "__async_run",
				AsyncSourceFunc: asyncSrc,
			})
			if err != nil {
				return 1, fmt.Errorf("async export: %w", err)
			}
			// Lift the async export with the component valtype matching the
			// source function's (scalar) result, so `async function foo():
			// u64 / f64` is exported as `foo: async func() -> u64 / f64`
			// rather than forced to u32.
			var rvt byte = component.CValtypeU32
			for _, fn := range prog.Funcs {
				if fn.Name == asyncSrc {
					rvt = asyncResultCValtype(fn.ReturnType)
					break
				}
			}
			// -async-provider: BUNDLE bring-your-own provider component(s) so the
			// program's async `@import`s are satisfied in-component, yielding one
			// self-contained runnable artifact (rather than a component with
			// unresolved imports for a separate host to satisfy). See
			// docs/WASI-PREVIEW3-ASYNC-PLAN.md. Each import is mapped to a provider
			// by `WITNAME=PATH` (repeatable); a single bare `PATH` is shorthand for
			// the sole import. Scalar params + scalar result only for now.
			if len(asyncProviders) > 0 {
				var imps []*ast.FuncDecl
				for _, fn := range prog.Funcs {
					if fn.ImportIface != "" && fn.Async {
						imps = append(imps, fn)
					}
				}
				if len(imps) == 0 {
					return 1, fmt.Errorf("-async-provider: the program has no async @import to bundle")
				}
				// Resolve provider entries to a WIT-name → path map. `WITNAME=PATH`
				// is explicit; one bare `PATH` is allowed only for a single import.
				byName := map[string]string{}
				var bare []string
				for _, entry := range asyncProviders {
					if i := strings.IndexByte(entry, '='); i >= 0 {
						byName[entry[:i]] = entry[i+1:]
					} else {
						bare = append(bare, entry)
					}
				}
				if len(bare) > 0 {
					if len(bare) == 1 && len(byName) == 0 && len(imps) == 1 {
						byName[imps[0].ImportWITName] = bare[0]
					} else {
						return 1, fmt.Errorf("-async-provider: a bare PATH is only allowed with a single async import; use WITNAME=PATH for %d imports", len(imps))
					}
				}
				specs := make([]component.AsyncImportSpec, 0, len(imps))
				for _, imp := range imps {
					path, ok := byName[imp.ImportWITName]
					if !ok {
						return 1, fmt.Errorf("-async-provider: no provider for async import %q; pass -async-provider %s=PATH", imp.Name, imp.ImportWITName)
					}
					delete(byName, imp.ImportWITName) // mark consumed
					// Lower signature: scalar params (flattened) + return-area ptr;
					// the result is the i32 async status.
					lowerParams := []byte{}
					for _, p := range imp.Params {
						vt, ok := asyncScalarCoreValtype(p.Type)
						if !ok {
							return 1, fmt.Errorf("-async-provider: async import %q parameter %q has non-scalar type %s; only scalar params are supported so far", imp.Name, p.Name, p.Type.String())
						}
						lowerParams = append(lowerParams, vt)
					}
					lowerParams = append(lowerParams, 0x7f) // retptr (i32)
					provBytes, err := os.ReadFile(path)
					if err != nil {
						return 1, fmt.Errorf("-async-provider: reading provider %q: %w", path, err)
					}
					// COMPONENT-level signature of this import, distinct from the
					// core-level LowerParams above. Since the v46 async port
					// (#5456) the consumer is its own nested component that
					// IMPORTS each awaited function, so it must declare that
					// function's component functype — and an empty
					// ImportParamNames means "no-param import". Omitting these
					// made the consumer declare `add: async func() -> u32`
					// against a provider exporting `add(a: u32, b: u32) -> u32`,
					// so instantiation failed with an export arity mismatch
					// (#5490). The no-param case was unaffected, which is why
					// only the params variant broke.
					impParamNames := make([]string, len(imp.Params))
					impParamVals := make([][]byte, len(imp.Params))
					for i, p := range imp.Params {
						impParamNames[i] = p.Name
						impParamVals[i] = []byte{asyncResultCValtype(p.Type)}
					}
					specs = append(specs, component.AsyncImportSpec{
						Iface:               imp.ImportIface,
						WITName:             imp.ImportWITName,
						Provider:            provBytes,
						ProviderExportName:  imp.ImportWITName,
						LowerParams:         lowerParams,
						LowerResults:        []byte{0x7f}, // i32 status
						ImportParamNames:    impParamNames,
						ImportParamVals:     impParamVals,
						ImportResultValtype: asyncResultCValtype(imp.ReturnType),
					})
				}
				if len(byName) > 0 {
					unused := make([]string, 0, len(byName))
					for name := range byName {
						unused = append(unused, name)
					}
					sort.Strings(unused)
					return 1, fmt.Errorf("-async-provider: no async import matches %v", unused)
				}
				comp := component.BuildAsyncImportsAwaitComponent(acore, specs, "__async_run", asyncExportNm, rvt)
				if err := os.WriteFile(outPath, comp, 0o644); err != nil {
					return 1, err
				}
				return 0, nil
			}
			// An async export with scalar parameters lifts as
			// `name: async func(<params>) -> rvt` (the wasmbin wrapper forwards the
			// params); a no-parameter source keeps the plain `() -> rvt` lift.
			var srcDecl *ast.FuncDecl
			for _, fn := range prog.Funcs {
				if fn.Name == asyncSrc {
					srcDecl = fn
					break
				}
			}
			var comp []byte
			if srcDecl != nil && len(srcDecl.Params) > 0 {
				names := make([]string, len(srcDecl.Params))
				vals := make([]byte, len(srcDecl.Params))
				for i, p := range srcDecl.Params {
					if _, ok := asyncScalarCoreValtype(p.Type); !ok {
						return 1, fmt.Errorf("async export: %q parameter %q has non-scalar type %s; only scalar params are supported", asyncSrc, p.Name, p.Type.String())
					}
					names[i], vals[i] = p.Name, asyncResultCValtype(p.Type)
				}
				comp = component.BuildAsyncLiftedExportComponentParams(acore, "__async_run", asyncExportNm, names, vals, rvt)
			} else {
				comp = component.BuildAsyncLiftedExportComponent(acore, "__async_run", asyncExportNm, rvt)
			}
			if err := os.WriteFile(outPath, comp, 0o644); err != nil {
				return 1, err
			}
			return 0, nil
		}
		bin, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
			Preview2WASI: componentWrap || componentWrapCli,
			SynthCliRun:  componentWrap || componentWrapCli,
			// -component-wrap-cli lifts _lang_run as a wasi:cli/run
			// result (needs a 0/1 discriminant); -component-wrap lifts
			// it raw as a u32 export, so leave it unnormalised there.
			CliRunResult: componentWrapCli,
		})
		if err != nil {
			return 1, err
		}
		if componentWrap {
			// Lift the run func as a component-level u32-returning export
			// named "main" (callable via `wasmtime run --invoke main()`).
			// `_lang_run` is the SynthCliRun wrapper normalising main's
			// signature to `() -> i32`.
			comp, err := buildPreview2Component(prog, info, bin, "main")
			if err != nil {
				return 1, fmt.Errorf("-component-wrap %w", err)
			}
			if err := os.WriteFile(outPath, comp, 0o644); err != nil {
				return 1, err
			}
			return 0, nil
		}
		if componentWrapCli {
			// Emit the wasi:cli/run@0.2.0 export shape so the component runs
			// under plain `wasmtime run` (no --invoke). Composes any
			// recognised import shape and errors on imports it can't place.
			comp, err := buildPreview2CliRunComponent(prog, info, bin)
			if err != nil {
				return 1, fmt.Errorf("-component-wrap-cli %w", err)
			}
			if err := os.WriteFile(outPath, comp, 0o644); err != nil {
				return 1, err
			}
			return 0, nil
		}
		if err := os.WriteFile(outPath, bin, 0o644); err != nil {
			return 1, err
		}
		return 0, nil
	}

	if target == "wasm" || target == "wasi-http" {
		// Both CLI wasm targets route through wasmbin. The old
		// WAT-text backend (internal/codegen/wasm) has been
		// removed; wasmbin emits the component binary directly.
		if outPath == "" {
			return 1, fmt.Errorf("-target %s requires -o OUTPUT (the component is a binary)", target)
		}
		// `-target wasm` composes a wasi:cli/run component natively via the
		// Go-side preview-2 encoder (no wasm-tools, no preview-1 adapter).
		// The path composes any mix of the migrated preview-2 imports;
		// anything else surfaces a clear error.
		if target == "wasm" {
			bin, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
				Preview2WASI: true,
				SynthCliRun:  true,
				CliRunResult: true,
			})
			if err != nil {
				return 1, err
			}
			comp, err := buildPreview2CliRunComponent(prog, info, bin)
			if err != nil {
				return 1, fmt.Errorf("-target wasm %w", err)
			}
			if err := os.WriteFile(outPath, comp, 0o644); err != nil {
				return 1, err
			}
			return 0, nil
		}
		// `-target wasi-http` composes the wasi:http/incoming-handler
		// component natively, the same Go-side path the CLI target uses.
		core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
			HttpHandler:        true,
			Preview2WASI:       true,
			ForceMemorySection: true,
		})
		if err != nil {
			return 1, err
		}
		req, unsupported := component.ClassifyCore(core)
		if len(unsupported) > 0 {
			return 1, fmt.Errorf("-target wasi-http can't compose a handler that imports %s yet — remove the source that pulls them in", strings.Join(unsupported, ", "))
		}
		// The wasi:http/proxy world `wasmtime serve` runs grants clocks +
		// random but NOT wasi:cli/environment or filesystem, so a handler
		// that reads env / args / files / stdin can't run there.
		if req.Args || req.Env || req.Stdin || req.File.Any() {
			return 1, fmt.Errorf("-target wasi-http: a handler can't use env / args / files / stdin — the http proxy world doesn't grant them")
		}
		comp := component.Compose(core, req, "wasi:http/incoming-handler@0.2.0#handle")
		if err := os.WriteFile(outPath, comp, 0o644); err != nil {
			return 1, err
		}
		return 0, nil
	}

	if target != "arm64" && target != "arm64-darwin" && target != "arm64-android" && target != "x86-64" {
		return 1, fmt.Errorf("unknown target %q (want arm64-darwin, arm64, arm64-android, x86-64, wasm, wasm-bin, or wasi-http)", target)
	}

	darwin := target == "arm64-darwin"
	// arm64-android is arm64 Linux ELF emitted as a static position-
	// independent executable (ET_DYN): Android rejects non-PIE for normal
	// exec, so the codegen emits the self-relocation prologue and the
	// linker produces a .rela.dyn / PT_DYNAMIC image.
	android := target == "arm64-android"
	// -shared exports are kept as tree-shake roots so the .so can export
	// functions the program never calls itself (e.g. JVM-invoked JNI entries).
	var exportNames []string
	if shared {
		exportNames = []string{"main"}
		if export != "" {
			exportNames = strings.Split(export, ",")
		}
	}
	// Under -g, the DWARF .debug_line table is built from per-statement `.loc`
	// markers the native backends emit (#5537 slice 2). The source path (as
	// compiled) names file 1 in the line program and DW_AT_name; the compile
	// directory (cwd) is DW_AT_comp_dir, so a debugger resolves a relative
	// source path and displays the source, not just addresses.
	dbgSrc := srcPath
	dbgCompDir, _ := os.Getwd()
	// DWARF variable DIEs (#5537 slice 3 locals/params): map each function to
	// its scalar parameters + locals with frame offsets, so gdb/lldb can
	// `info args` / `print <var>`. x86-64 and arm64 (Linux ELF); gated on -g.
	var dbgVars map[string][]nativeelf.LocalVar
	if emitDebugSyms && (target == "x86-64" || target == "arm64") && !darwin && !android {
		dbgVars = dwarfLocalVars(prog, info, target)
	}

	var asm string
	switch target {
	case "x86-64":
		asm, err = x86_64codegen.EmitWithOptions(prog, info, x86_64codegen.Options{Exports: exportNames, DebugLines: emitDebugSyms})
	default:
		asm, err = arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{Darwin: darwin, PIE: android, Exports: exportNames, DebugLines: emitDebugSyms && !darwin && !android})
	}
	if err != nil {
		return 1, err
	}
	// An explicit -cc opts out of the in-process native backend (the
	// default for arm64 Linux) and routes through that external toolchain.
	ccExplicit := cc != ""
	if cc == "" {
		switch target {
		case "x86-64":
			cc = "x86_64-linux-gnu-gcc"
		case "arm64-darwin":
			cc = "clang"
		default:
			cc = "aarch64-linux-gnu-gcc"
		}
	}
	if !runIt && outPath == "" {
		if _, err := os.Stdout.WriteString(asm); err != nil {
			return 1, err
		}
		return 0, nil
	}
	binPath := outPath
	var cleanupBin string
	if binPath == "" {
		f, err := os.CreateTemp("", "fern-bin-*")
		if err != nil {
			return 1, err
		}
		f.Close()
		binPath = f.Name()
		cleanupBin = binPath
	}
	defer func() {
		if cleanupBin != "" {
			os.Remove(cleanupBin)
		}
	}()
	if shared {
		if outPath == "" {
			return 1, fmt.Errorf("-shared requires -o OUTPUT.so")
		}
		if target != "x86-64" && target != "arm64" && target != "arm64-android" {
			return 1, fmt.Errorf("-shared is only supported with -target x86-64, arm64, or arm64-android (got %q)", target)
		}
		if err := linkNativeShared(asm, binPath, target, exportNames); err != nil {
			return 1, err
		}
		return 0, nil
	}
	if native && target != "arm64" && target != "x86-64" && target != "arm64-darwin" && target != "arm64-android" {
		return 1, fmt.Errorf("-native is only supported with -target arm64, arm64-android, x86-64, or arm64-darwin (got %q)", target)
	}
	// arm64/x86-64 Linux, arm64-darwin, and arm64-android all use the
	// pure-Go assembler+linker by default (no external toolchain). Pass -cc
	// to opt out to an external assembler/linker (gcc on Linux, clang on
	// darwin); arm64-android only supports the in-process PIE linker.
	useNative := native || (!ccExplicit && (target == "arm64" || target == "x86-64" || darwin || android))
	switch {
	case useNative && darwin:
		if err := linkNativeDarwin(asm, binPath); err != nil {
			return 1, err
		}
	case useNative && android:
		if err := linkNativePIE(asm, binPath); err != nil {
			return 1, err
		}
	case useNative && target == "x86-64":
		if err := linkNativeX86(asm, binPath, dbgSrc, dbgCompDir, dbgVars); err != nil {
			return 1, err
		}
	case useNative:
		if err := linkNative(asm, binPath, dbgSrc, dbgCompDir, dbgVars); err != nil {
			return 1, err
		}
	case darwin:
		if err := linkDarwin(asm, binPath, cc); err != nil {
			return 1, err
		}
	default:
		if err := link(asm, binPath, cc); err != nil {
			return 1, err
		}
	}
	if !runIt {
		return 0, nil
	}
	if darwin {
		// arm64 Darwin binaries run natively on Apple
		// Silicon Macs; qemu-aarch64 emulates Linux
		// arm64 only and can't load Mach-O.
		return 1, fmt.Errorf("--run is not supported for -target arm64-darwin (Mach-O binaries need an Apple Silicon Mac to execute; the output at %q is ready to run there)", binPath)
	}
	// Run the binary directly when its target matches the host
	// architecture — no emulator (or qemu install) needed. Only fall back
	// to a user-mode emulator for the cross case.
	if ((target == "arm64" || target == "arm64-android") && runtime.GOARCH == "arm64") ||
		(target == "x86-64" && runtime.GOARCH == "amd64") {
		return execDirect(binPath, progArgs)
	}
	// If the user left -qemu at its default but built for
	// x86-64, swap to qemu-x86_64 so --run picks the right
	// emulator without manual flag-flipping.
	if target == "x86-64" && qemu == "qemu-aarch64" {
		qemu = "qemu-x86_64"
	}
	return execUnderQemu(qemu, binPath, progArgs)
}

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

// linkDarwin writes asm to a temp .s file and invokes clang with
// the aarch64-apple-darwin triple + lld's Mach-O backend to
// produce a native arm64 macOS binary at outPath. Works on both
// buildWasmSSA lowers the program through ir.LowerWith →
// ssa.LiftFromIR → ssa.Optimize → wasmssa.EmitModule and
// returns the wasm core module bytes that export `main`.
// Returns an error when the program has no `main` function,
// when the lift fails (gap in lift coverage), or when emit
// rejects the SSA (gap in wasmssa coverage).
//
// Used by -target wasm-ssa. Ptr-width is fixed at 4 (wasm32).
func buildWasmSSA(prog *ast.Program, info *checker.Info) ([]byte, error) {
	irProg, err := ir.LowerWith(prog, info, 4)
	if err != nil {
		return nil, fmt.Errorf("ir.LowerWith: %v", err)
	}
	var target *ir.Func
	for _, fn := range irProg.Funcs {
		if fn.Name == "main" {
			target = fn
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("no `main` function in program")
	}
	f, err := ssa.LiftFromIR(target)
	if err != nil {
		return nil, fmt.Errorf("ssa.LiftFromIR: %v", err)
	}
	ssa.Optimize(f)
	// Same contract as buildArm64SSA, and after Optimize for the same reason.
	if err := ssa.Verify(f); err != nil {
		return nil, fmt.Errorf("ssa.Verify: %v", err)
	}
	return wasmssa.EmitModule(f, "main")
}

// buildArm64SSA lowers a whole program through the SSA-direct arm64 pipeline —
// ir.LowerWith (ptr width 8) → ssa.LiftFromIR + ssa.Optimize per function →
// arm64ssa.EmitAsmModule — and returns the AArch64 assembly text (a complete
// `_start` + all functions + referenced runtime helpers), ready for linkNative.
// Unlike buildWasmSSA (single `main`), it lifts every function so cross-function
// calls and recursion work. Returns an error when the program has no `main`, the
// lift fails, or emit rejects the SSA (a coverage gap) — never a miscompile.
// Ptr width is fixed at 8 (arm64). numAlloc is 12, the largest register file the
// renderer's x0..x15 mapping supports (12 allocatable + 4 scratch = 16).
func buildArm64SSA(prog *ast.Program, info *checker.Info) (string, error) {
	// DynSupported enables `dyn Trait` lowering (OpConstVtable / OpBoxDyn /
	// OpCallDyn + the per-(trait,concrete) vtables). DynRcSupported is
	// deliberately NOT passed: this path does not yet reclaim `dyn` values
	// (no `__drop_dyn_*` helper), so the box leaks — matching the arm64 native
	// `dyn` slice. See docs/DYN-TRAITS.md §4.2.2 / §4.4.
	irProg, err := ir.LowerWith(prog, info, 8, ir.DynSupported())
	if err != nil {
		return "", fmt.Errorf("ir.LowerWith: %v", err)
	}
	ir.ElideClosurePair(irProg, 8)
	// Dead-function elimination: lift only the functions reachable from `main`
	// (transitively, via direct/closure calls). Without this the whole of every
	// imported stdlib module is lifted, so an `abs`-only program would drag in
	// `cos` and bail on the still-unported __cos_f64 helper. A missing live
	// function can only ever surface as a clean "undefined label" link error,
	// never a miscompile. `nil` (no entry point) means keep everything.
	// A `dyn Trait` vtable's method implementations (`__method_<C>_<m>`) are
	// reached ONLY through the vtable's function-pointer slots — an indirect
	// dispatch the reachability walk can't follow — so root them explicitly or
	// dead-function elimination culls them and OpConstVtable's `.rodata` cell
	// references a missing symbol (docs/DYN-TRAITS.md §4.2.2; mirrors the wasm
	// backend's `dynImplMethods` roots). The trailing drop slot isn't emitted
	// on this path (no `dyn` RC), so its Drop fn needs no rooting.
	var dynRoots []string
	for _, vt := range irProg.Vtables {
		for _, m := range vt.Methods {
			dynRoots = append(dynRoots, m.Func)
		}
	}
	live := ir.LiveFunctionsWithAliases(irProg, nil, dynRoots...)
	funcs := map[string]*ssa.Func{}
	for _, fn := range irProg.Funcs {
		if live != nil && !live[fn.Name] {
			continue
		}
		f, err := ssa.LiftFromIR(fn)
		if err != nil {
			return "", fmt.Errorf("ssa.LiftFromIR %s: %v", fn.Name, err)
		}
		ssa.Optimize(f)
		// Verify AFTER Optimize, not before. This backend promises that an
		// unsupported construct ERRORS rather than miscompiles, and without
		// any Verify call on a build path that promise did not hold: invalid
		// SSA sailed through regalloc and emit and yielded a binary that
		// SIGSEGVs.
		//
		// After-Optimize only, because the lifter deliberately leaves blocks
		// unreachable — endBlockScope / endLoopScope say so explicitly, for
		// PruneUnreachable to drop — and Verify's use-before-def rule wants a
		// def in an ancestor block, which nothing in an unreachable block has.
		// Checking before Optimize would therefore reject programs the lifter
		// considers well-formed, and it buys no detection: an invalid lift
		// survives the passes, which is how it reached emit in the first
		// place.
		if err := ssa.Verify(f); err != nil {
			return "", fmt.Errorf("ssa.Verify %s: %v", fn.Name, err)
		}
		funcs[fn.Name] = f
	}
	if _, ok := funcs["main"]; !ok {
		return "", fmt.Errorf("no `main` function in program")
	}
	// Resolve each direct call's result width from the callee's signature, so a
	// 64-bit (i64/f64) return isn't masked back to i32 by the backend. The IR
	// call op carries no return width, so this needs the whole module.
	ssa.AnnotateCallWidths(funcs)
	return arm64ssa.EmitAsmModule(funcs, "main", 12, nil, irProg.Vtables...)
}

// Linux dev hosts (cross-compiling) and Macs natively as long
// as clang + lld are installed. The output is a full Mach-O
// executable that runs on Apple Silicon Macs without further
// processing.
func linkDarwin(asm, outPath, cc string) error {
	if _, err := exec.LookPath(cc); err != nil {
		return fmt.Errorf("compiler %q not found on PATH (override with -cc): %w", cc, err)
	}
	tmpDir, err := os.MkdirTemp("", "fern-build-*")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(tmpDir)
		}
	}()
	asmPath := filepath.Join(tmpDir, "prog.s")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		return err
	}
	// On a native macOS arm64 host, the default clang IS the
	// arm64-apple-darwin clang and ld64 is its default linker
	// — `lld` typically isn't installed. From a Linux dev host
	// or CI we need clang+lld's Mach-O backend to cross-compile.
	native := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
	var args []string
	if native {
		// Newer ld64 (Xcode 16+ on macOS Sequoia/Tahoe) refuses
		// to link dynamic executables without libSystem.dylib —
		// `-nostdlib` alone gets rejected with "dynamic
		// executables or dylibs must link with libSystem.dylib".
		// Workaround: keep `-nostdlib` to skip crt0/libc start-
		// files (we provide our own `_main`), but add `-lSystem`
		// explicitly to satisfy the dyld-stub linkage check.
		// Our user-program code doesn't call into libSystem;
		// linking it is purely a load-time formality.
		args = []string{"-nostdlib", "-lSystem", asmPath, "-o", outPath}
	} else {
		args = []string{
			"--target=arm64-apple-darwin",
			"-fuse-ld=lld",
			"-nostdlib",
			"-Wl,-arch,arm64",
			asmPath,
			"-o", outPath,
		}
	}
	cmd := exec.Command(cc, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		keep = true
		return fmt.Errorf("%s failed: %w\n%s\n(temporary assembly retained at %s)", cc, err, out, asmPath)
	}
	return nil
}

// link writes asm to a temp .s file and invokes the cross-compiler to
// produce a static binary at outPath. The temp file is removed on
// success; on failure we leave it in place so the user can inspect it.
//
// `-nostdlib` drops libc + libgcc + the crt startfiles; we provide
// our own `_start`, syscall wrappers, allocator, and memcpy /
// strcmp / strlen, so the resulting binary contains only language
// code + direct svc 0 syscalls.
// linkNative assembles and links arm64 assembly into a static ELF
// executable entirely in-process, with no external assembler or linker
// (the pure-Go internal/native backend). The output uses the W^X
// two-segment layout (R+X code, R+W data) so no mapping is both writable
// and executable — required by W^X-enforcing loaders such as Android's.
// Unsupported instructions surface as an error rather than a miscompile.
func linkNative(asm, outPath, srcFile, compDir string, funcVars map[string][]nativeelf.LocalVar) error {
	if emitDebugSyms {
		text, rodata, syms, locRows, err := nativearm64.AssembleProgramWXSyms(asm, nativeelf.TextVAddrWX)
		if err != nil {
			return fmt.Errorf("native assembler: %w", err)
		}
		textEnd := nativeelf.TextVAddrWX + uint64(len(text))
		fs := nativeelf.FuncSyms(syms, textEnd)
		// Per-statement DWARF .debug_line rows from the assembler's `.loc`
		// markers (#5537 slice 2); Offset is text-relative → absolute vaddr.
		rows := make([]nativeelf.LineRow, 0, len(locRows))
		for _, r := range locRows {
			rows = append(rows, nativeelf.LineRow{Addr: nativeelf.TextVAddrWX + uint64(r.Offset), Line: r.Line})
		}
		bin := nativeelf.StaticExecutableDataWXSymsRows(text, rodata, fs, rows, srcFile, compDir, textEnd, funcVars)
		if err := os.WriteFile(outPath, bin, 0o755); err != nil {
			return err
		}
		return os.Chmod(outPath, 0o755)
	}
	text, rodata, err := nativearm64.AssembleProgramWX(asm, nativeelf.TextVAddrWX)
	if err != nil {
		return fmt.Errorf("native assembler: %w", err)
	}
	bin := nativeelf.StaticExecutableDataWX(text, rodata)
	if err := os.WriteFile(outPath, bin, 0o755); err != nil {
		return err
	}
	// WriteFile keeps existing permissions, and --run's temp binary is
	// pre-created by CreateTemp at 0600 — chmod so it's executable.
	if err := os.Chmod(outPath, 0o755); err != nil {
		return err
	}
	return nil
}

// linkNativePIE assembles arm64 assembly into a static position-independent
// (ET_DYN) executable entirely in-process — the -target arm64-android path.
// The codegen emitted the self-relocation prologue (Options.PIE); the
// assembler returns the R_AARCH64_RELATIVE relocations for the
// `.quad <symbol>` slots, which the ELF writer records in .rela.dyn /
// .dynamic. Same W^X two-segment layout, but ET_DYN at a load base of 0 so
// the kernel can map it at an arbitrary address (required by Android).
func linkNativePIE(asm, outPath string) error {
	text, rodata, relocs, err := nativearm64.AssembleProgramPIE(asm, nativeelf.TextVAddrPIE)
	if err != nil {
		return fmt.Errorf("native assembler: %w", err)
	}
	elfRelocs := make([]nativeelf.Reloc, len(relocs))
	for i, r := range relocs {
		elfRelocs[i] = nativeelf.Reloc{Offset: r.Offset, Addend: r.Addend}
	}
	bin := nativeelf.StaticPieExecutable(text, rodata, elfRelocs)
	if err := os.WriteFile(outPath, bin, 0o755); err != nil {
		return err
	}
	return os.Chmod(outPath, 0o755)
}

// linkNativeShared assembles a program into a shared object (.so) exporting
// the named functions, entirely in-process. It is the -shared path: the
// same base-0 PIE layout as linkNativePIE, but wrapped by
// elf.SharedLibrary{,X86} with a dynamic symbol table so a loader (dlopen /
// Android's System.loadLibrary) can resolve the exports. target selects the
// machine (x86-64 vs arm64/arm64-android).
func linkNativeShared(asm, outPath, target string, exportNames []string) error {
	soname := filepath.Base(outPath)
	var so []byte
	if target == "x86-64" {
		text, rodata, relocs, ev, err := nativex86.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exportNames)
		if err != nil {
			return fmt.Errorf("native assembler: %w", err)
		}
		elfRelocs := make([]nativeelf.Reloc, len(relocs))
		for i, r := range relocs {
			elfRelocs[i] = nativeelf.Reloc{Offset: r.Offset, Addend: r.Addend}
		}
		so = nativeelf.SharedLibraryX86(text, rodata, elfRelocs, sharedExports(exportNames, ev), soname)
	} else { // arm64 / arm64-android
		text, rodata, relocs, ev, err := nativearm64.AssembleProgramShared(asm, nativeelf.TextVAddrPIE, exportNames)
		if err != nil {
			return fmt.Errorf("native assembler: %w", err)
		}
		elfRelocs := make([]nativeelf.Reloc, len(relocs))
		for i, r := range relocs {
			elfRelocs[i] = nativeelf.Reloc{Offset: r.Offset, Addend: r.Addend}
		}
		so = nativeelf.SharedLibrary(text, rodata, elfRelocs, sharedExports(exportNames, ev), soname)
	}
	if err := os.WriteFile(outPath, so, 0o755); err != nil {
		return err
	}
	return os.Chmod(outPath, 0o755)
}

// sharedExports pairs each export name with the vaddr the assembler
// resolved for it, preserving the requested order.
func sharedExports(names []string, vaddr map[string]uint64) []nativeelf.Export {
	out := make([]nativeelf.Export, len(names))
	for i, n := range names {
		out[i] = nativeelf.Export{Name: n, Value: vaddr[n]}
	}
	return out
}

// linkNativeX86 is the x86-64 counterpart of linkNative: it assembles and
// links x86-64 assembly into a static ELF executable entirely in-process
// via the pure-Go internal/native/x86_64 backend, using the same W^X
// two-segment layout (R+X code, R+W data).
// scalarTypeKey maps a Fern scalar type to its DWARF base-type key (matching
// elf.scalarBaseType), or "" for a non-scalar type (which gets no variable DIE
// in this slice — composite type DIEs are a follow-up).
func scalarTypeKey(t ast.Type) string {
	switch v := t.(type) {
	case ast.NumberType:
		if v.IsPointerWidth() {
			return "" // usize/isize — target-width, skip for now
		}
		sign := "i"
		if !v.IsSigned() { // i32 is NumberType{Width:0}; IsSigned handles that
			sign = "u"
		}
		switch v.NormalWidth() {
		case 8:
			return sign + "8"
		case 16:
			return sign + "16"
		case 32:
			return sign + "32"
		case 64:
			return sign + "64"
		}
	case ast.FloatType:
		if v.Width == 32 {
			return "f32"
		}
		return "f64"
	case ast.BoolType:
		return "bool"
	}
	return ""
}

// structDesc describes a struct for a DWARF pointer-to-struct variable DIE, or
// returns nil if t isn't a struct with at least one describable member. Scalar
// fields become base-typed members; a NESTED struct field becomes a
// pointer-to-struct member (the field holds a pointer to the nested box), so
// `print *p` on a `Rect { origin: Point, … }` surfaces `origin` too. Other
// non-scalar fields (string / array / enum) are omitted — a valid partial
// description; gdb shows the members it has. Field offsets come from the same
// layout the codegen uses (ir.StructFieldLayout, ptrW=8); the user-visible
// struct pointer already points past the rc header, so offsets are relative to
// that pointer.
func structDesc(t ast.Type, info *checker.Info) *nativeelf.StructType {
	return structDescPath(t, info, nil)
}

// structDescPath is structDesc with a cycle guard: `path` is the set of struct
// names currently being described (ancestors). A field whose struct is already
// on the path is omitted, breaking recursive / mutually-recursive types (a
// linked-list `next: Node` field, a tree node's children) instead of looping
// forever; the enclosing struct is still described from its remaining members.
func structDescPath(t ast.Type, info *checker.Info, path map[string]bool) *nativeelf.StructType {
	st, ok := t.(ast.StructType)
	if !ok {
		return nil
	}
	if path[st.Name] {
		return nil // cycle: this field closes a reference loop
	}
	sd := info.Structs[st.Name]
	if sd == nil || len(sd.Fields) == 0 {
		return nil
	}
	offs, size := ir.StructFieldLayout(sd.Fields, 8)
	sub := map[string]bool{st.Name: true}
	for k := range path {
		sub[k] = true
	}
	fields := make([]nativeelf.StructField, 0, len(sd.Fields))
	for _, f := range sd.Fields {
		if key := scalarTypeKey(f.Type); key != "" {
			fields = append(fields, nativeelf.StructField{Name: f.Name, TypeKey: key, Offset: int(offs[f.Name])})
		} else if nested := structDescPath(f.Type, info, sub); nested != nil {
			fields = append(fields, nativeelf.StructField{Name: f.Name, Struct: nested, Offset: int(offs[f.Name])})
		}
	}
	if len(fields) == 0 {
		return nil // nothing describable
	}
	return &nativeelf.StructType{Name: st.Name, Size: int(size), Fields: fields}
}

// enumDesc describes a payloadless (C-style) enum for a DWARF pointer-to-enum
// variable DIE, or returns nil if t isn't an enum whose every variant is
// payloadless. A Fern payloadless enum value is a pointer to a 4-byte i32 tag
// sentinel, so the variable's DWARF type is a pointer to an enumeration_type;
// the tag value of each variant is its declaration index (the same tag the
// codegen assigns). Enums with any payload-carrying variant are not described
// (their runtime layout is a boxed tagged union, a follow-up).
func enumDesc(t ast.Type, info *checker.Info) *nativeelf.EnumType {
	et, ok := t.(ast.EnumType)
	if !ok {
		return nil
	}
	ed := info.Enums[et.Name]
	if ed == nil || len(ed.Variants) == 0 {
		return nil
	}
	enumerators := make([]nativeelf.Enumerator, 0, len(ed.Variants))
	for i, vr := range ed.Variants {
		if len(vr.Payloads) > 0 {
			return nil // payload-carrying variant: not a plain enum
		}
		enumerators = append(enumerators, nativeelf.Enumerator{Name: vr.Name, Value: i})
	}
	return &nativeelf.EnumType{Name: et.Name, Enumerators: enumerators}
}

// dwarfLocalVars builds the per-function scalar parameter/local variable list
// for the DWARF subprogram DIEs (#5537 slice 3). Both native backends place
// parameters in slots 0..N-1 then info.Locals in order, with slot k relative
// to the frame pointer (rbp / x29). The per-slot byte size differs: x86-64
// uses a single-word string ABI so every slot is 8 bytes (offset -8*(k+1));
// arm64 uses two-word strings, so a string slot is 16 bytes and shifts every
// later slot's offset. Closures are skipped (capture slots shift the layout);
// only scalar-typed variables get a DIE, but non-scalar slots still count
// toward the arm64 running offset.
func dwarfLocalVars(prog *ast.Program, info *checker.Info, target string) map[string][]nativeelf.LocalVar {
	out := map[string][]nativeelf.LocalVar{}
	for _, fn := range prog.Funcs {
		if len(fn.Captures) > 0 {
			continue
		}
		type slotVar struct {
			name    string
			typ     ast.Type
			isParam bool
		}
		slots := make([]slotVar, 0, len(fn.Params)+len(info.Locals[fn]))
		for _, p := range fn.Params {
			slots = append(slots, slotVar{p.Name, p.Type, true})
		}
		for _, v := range info.Locals[fn] {
			slots = append(slots, slotVar{v.Name, v.Type, false})
		}

		var vars []nativeelf.LocalVar
		cum := 0 // arm64 running byte offset (sum of slot sizes so far)
		for k, s := range slots {
			off := -8 * (k + 1) // x86-64: uniform 8-byte slots
			if target != "x86-64" {
				sz := 8
				if _, isStr := s.typ.(ast.StringType); isStr {
					sz = 16 // arm64 two-word string
				}
				cum += sz
				off = -cum
			}
			if key := scalarTypeKey(s.typ); key != "" {
				vars = append(vars, nativeelf.LocalVar{Name: s.name, TypeKey: key, Offset: off, IsParam: s.isParam})
			} else if sd := structDesc(s.typ, info); sd != nil {
				vars = append(vars, nativeelf.LocalVar{Name: s.name, Struct: sd, Offset: off, IsParam: s.isParam})
			} else if ed := enumDesc(s.typ, info); ed != nil {
				vars = append(vars, nativeelf.LocalVar{Name: s.name, Enum: ed, Offset: off, IsParam: s.isParam})
			}
		}
		if len(vars) > 0 {
			out[fn.Name] = vars
		}
	}
	return out
}

func linkNativeX86(asm, outPath, srcFile, compDir string, funcVars map[string][]nativeelf.LocalVar) error {
	if emitDebugSyms {
		text, rodata, syms, locRows, err := nativex86.AssembleProgramWXSyms(asm, nativeelf.TextVAddrWX)
		if err != nil {
			return fmt.Errorf("native assembler: %w", err)
		}
		textEnd := nativeelf.TextVAddrWX + uint64(len(text))
		fs := nativeelf.FuncSyms(syms, textEnd)
		// Per-statement DWARF .debug_line rows from the assembler's `.loc`
		// markers (#5537 slice 2); Offset is text-relative → absolute vaddr.
		rows := make([]nativeelf.LineRow, 0, len(locRows))
		for _, r := range locRows {
			rows = append(rows, nativeelf.LineRow{Addr: nativeelf.TextVAddrWX + uint64(r.Offset), Line: r.Line})
		}
		bin := nativeelf.StaticExecutableDataX86WXSymsRows(text, rodata, fs, rows, srcFile, compDir, textEnd, funcVars)
		if err := os.WriteFile(outPath, bin, 0o755); err != nil {
			return err
		}
		return os.Chmod(outPath, 0o755)
	}
	text, rodata, err := nativex86.AssembleProgramWX(asm, nativeelf.TextVAddrWX)
	if err != nil {
		return fmt.Errorf("native assembler: %w", err)
	}
	bin := nativeelf.StaticExecutableDataX86WX(text, rodata)
	if err := os.WriteFile(outPath, bin, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(outPath, 0o755); err != nil {
		return err
	}
	return nil
}

// linkNativeDarwin assembles arm64 asm and wraps it in a static, ad-hoc-
// signed Mach-O executable entirely in-process (internal/native/macho) —
// no clang/ld64. Experimental: covers the integer/control-flow surface
// the assembler handles; @PAGE/@PAGEOFF data addressing (strings, heap)
// is not yet supported and surfaces as a clear assembler error.
func linkNativeDarwin(asm, outPath string) error {
	a, err := nativearm64.ParseProgram(asm)
	if err != nil {
		return fmt.Errorf("native assembler: %w", err)
	}
	// Code lives in __TEXT, data (string constants + globals) in a separate
	// __DATA segment; the assembler resolves adrp @PAGE / @PAGEOFF against
	// the addresses the Mach-O layout will place them at.
	textLen, dataLen := a.MachOTextLen(), a.MachODataLen()
	// Under -g, an LC_SYMTAB naming every function is emitted into __LINKEDIT
	// (#5537 slice 1 for arm64-darwin). Its 24-byte load command shifts every
	// address, so the assembler must resolve against the syms-inclusive layout.
	if emitDebugSyms {
		textVAddr, dataVAddr := nativemacho.SegmentAddrsSyms(textLen, dataLen)
		text, data, err := a.LinkMachO(textVAddr, dataVAddr)
		if err != nil {
			return fmt.Errorf("native assembler: %w", err)
		}
		syms := nativemacho.FuncSyms(a.TextLabelVAddrs(textVAddr), textVAddr+uint64(len(text)))
		bin := nativemacho.StaticExecutableSyms(text, data, filepath.Base(outPath), syms, a.MachODataRebaseOffsets())
		if err := os.WriteFile(outPath, bin, 0o755); err != nil {
			return err
		}
		return os.Chmod(outPath, 0o755)
	}
	textVAddr, dataVAddr := nativemacho.SegmentAddrs(textLen, dataLen)
	text, data, err := a.LinkMachO(textVAddr, dataVAddr)
	if err != nil {
		return fmt.Errorf("native assembler: %w", err)
	}
	bin := nativemacho.StaticExecutable(text, data, filepath.Base(outPath), a.MachODataRebaseOffsets())
	if err := os.WriteFile(outPath, bin, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(outPath, 0o755); err != nil {
		return err
	}
	return nil
}

func link(asm, outPath, cc string) error {
	if _, err := exec.LookPath(cc); err != nil {
		return fmt.Errorf("cross-compiler %q not found on PATH (override with -cc): %w", cc, err)
	}
	tmpDir, err := os.MkdirTemp("", "fern-build-*")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(tmpDir)
		}
	}()

	asmPath := filepath.Join(tmpDir, "prog.s")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		return err
	}
	args := []string{"-static", "-nostdlib", "-s", asmPath, "-o", outPath}
	cmd := exec.Command(cc, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		keep = true
		return fmt.Errorf("%s failed: %w\n%s\n(temporary assembly retained at %s)", cc, err, out, asmPath)
	}
	return nil
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

// classifyComposeCliStream inspects a core module's imports and, if
// they all fall within the CLI-stream + structured family that
// component.ComposePreview2CliRun handles, returns the ComposeOpts to
// build it. The family: an optional write side (get-stdout OR
// get-stderr paired with output-stream.blocking-write-and-flush), an
// optional read side (get-stdin + input-stream.blocking-read), and
// any number of no-memory structured imports (the
// knownPreview2Imports set: exit / random / monotonic). Returns
// ok=false if anything else appears, if both stdout and stderr are
// used at once (the composer handles a single write stream), or if a
// getter/method pair is half-present.
// buildPreview2CliRunComponent routes a wasmbin core module to a
// wasi:cli/run component without the wasm-tools adapter: the general
// composer (ComposePreview2CliRun) for any recognised CLI-stream /
// filesystem-open / wall-clock-args-env / structured shape, or
// BuildWasiCliRunComponent for an import-free program. Imports the
// composer can't place surface a clear error (the caller prefixes the
// flag context). Shared by -component-wrap-cli and the -target wasm
// default so the two stay in lock-step. The read side, the filesystem
// open-chain, and the list-returning args/env imports allocate
// through cabi_realloc, so the module is rebuilt with
// ForceMemorySection when any is present.
func buildPreview2CliRunComponent(prog *ast.Program, info *checker.Info, bin []byte) ([]byte, error) {
	return buildPreview2Component(prog, info, bin, "")
}

// asyncResultCValtype maps a WASI Preview-3 async export's (scalar) Fern result
// type to the component valtype it lifts as. 64-bit ints map to s64/u64 and
// floats to f32/f64; every 8/16/32-bit integer (and anything else) keeps the
// established u32 default — the core function returns i32 for those, and the
// signedness only affects how a `--invoke` caller displays the value.
func asyncResultCValtype(t ast.Type) byte {
	switch x := t.(type) {
	case ast.NumberType:
		if x.NormalWidth() == 64 {
			if x.Signed {
				return component.CValtypeS64
			}
			return component.CValtypeU64
		}
	case ast.FloatType:
		if x.NormalWidth() == 32 {
			return component.CValtypeF32
		}
		return component.CValtypeF64
	}
	return component.CValtypeU32
}

// asyncScalarCoreValtype maps a scalar Fern type to its core wasm valtype byte
// (i32=0x7f, i64=0x7e, f32=0x7d, f64=0x7c), used to build the canon-lower
// parameter signature for a bundled async import (-async-provider). Returns
// ok=false for any non-scalar type (string / array / record / …), which the
// caller rejects — those need realloc/marshalling and are not bundled yet.
func asyncScalarCoreValtype(t ast.Type) (byte, bool) {
	switch x := t.(type) {
	case ast.NumberType:
		if x.NormalWidth() == 64 {
			return 0x7e, true // i64
		}
		return 0x7f, true // i32 (i32 + usize on wasm32 both lower to i32)
	case ast.BoolType:
		return 0x7f, true // i32
	case ast.FloatType:
		if x.NormalWidth() == 32 {
			return 0x7d, true // f32
		}
		return 0x7c, true // f64
	}
	return 0, false
}

// buildPreview2Component is buildPreview2CliRunComponent generalised
// over the lift tail: exportName == "" produces the wasi:cli/run
// shape; a non-empty exportName lifts the run func as a u32-returning
// component func exported under that name (the non-cli
// `-component-wrap` shape, callable via `--invoke <name>()`). Both
// share the composer for every recognised import shape and fall back
// to the matching import-free builder.
// hasExternImports reports whether the program declares any `@import` extern
// (bring-your-own WIT, P4). Such a program's imports go beyond the legacy
// composer's recognised set, so it composes via the world-driven path.
func hasExternImports(prog *ast.Program) bool {
	for _, fn := range prog.Funcs {
		if fn.ImportIface != "" {
			return true
		}
	}
	return false
}

func buildPreview2Component(prog *ast.Program, info *checker.Info, bin []byte, exportName string) ([]byte, error) {
	req, unsupported := component.ClassifyCore(bin)
	if len(unsupported) > 0 {
		// `@import` extern declarations (bring-your-own WIT, P4) pull in
		// imports the legacy composer doesn't know. When the program declares
		// any, route the whole module through the world-driven composer, which
		// classifies every import (built-in and extern alike) against the
		// embedded fern world — no hardcoded import set. The cli/run lift is
		// the only shape it produces, so a named-export wrap can't combine with
		// externs yet.
		if exportName == "" && hasExternImports(prog) {
			rb, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
				ForceMemorySection: true,
				Preview2WASI:       true,
				SynthCliRun:        true,
				CliRunResult:       true,
			})
			if err != nil {
				return nil, err
			}
			w, err := componenttype.DecodeWorld("fern")
			if err != nil {
				return nil, err
			}
			return component.ComposeFromWorldAuto(rb, w)
		}
		return nil, fmt.Errorf("can't wrap a core module with unrecognised imports yet (saw %d): %s. Remove the source that pulls them in.", len(unsupported), strings.Join(unsupported, ", "))
	}
	if component.RequestEmpty(req) {
		if exportName != "" {
			return component.BuildLiftedExportComponent(bin, "_lang_run", exportName, nil, nil, component.CValtypeU32), nil
		}
		return component.BuildWasiCliRunComponent(bin, "_lang_run"), nil
	}
	// Sockets (TCP server / UDP client) always lift wasi:cli/run (a server
	// isn't an --invoke export) and need memory exported (the socket
	// methods write results through caller retptrs). Build with
	// ForceMemorySection; the engine composes the union (sockets + any
	// stdio / files / clocks the program also uses).
	if req.Tcp || req.Udp {
		rb, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
			ForceMemorySection: true,
			Preview2WASI:       true,
			SynthCliRun:        true,
			// A server always lifts wasi:cli/run (never a u32 --invoke
			// export), so its result is a 0/1 discriminant.
			CliRunResult: true,
		})
		if err != nil {
			return nil, err
		}
		return component.Compose(rb, req, "_lang_run"), nil
	}
	// CLI-stream / filesystem / clock family. The read side, the file
	// open-chain, the list-returning args/env imports, and the reactor
	// multiplexer (wasi:io/poll.poll returns list<u32>) all allocate
	// through cabi_realloc, so rebuild with ForceMemorySection when any
	// is present.
	req.ExportName = exportName
	b := bin
	if req.Stdin || req.File.Any() || req.Args || req.Env || req.Poll {
		rb, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
			ForceMemorySection: true,
			Preview2WASI:       true,
			SynthCliRun:        true,
			// cli/run lift (empty exportName) needs a 0/1 discriminant;
			// a named u32 export surfaces _lang_run's value raw.
			CliRunResult: exportName == "",
		})
		if err != nil {
			return nil, err
		}
		b = rb
	}
	return component.Compose(b, req, "_lang_run"), nil
}
