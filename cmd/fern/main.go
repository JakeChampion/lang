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
//	                                     # non-zero when they differ)
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
// Note: the formatter strips `//` line comments and blank lines
// because the lexer drops both before they reach the AST.
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
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/codegen/wasmssa"
	x86_64codegen "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/diag"
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
	"github.com/jakechampion/lang/internal/wasm/component"
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

// entry bundles a loaded program with everything needed to render its
// diagnostics. For a literate document it also carries the original
// `.fern.md` source and, per generated file, a position remap from
// tangled coordinates back to document coordinates, so a checker error
// in generated code points at the line the author actually wrote. A
// multi-file document (`file=PATH` blocks) tangles to several modules,
// each with its own remap keyed by that module's absolute path; a
// single-`<<*>>` document has exactly one remap (keyed by the document
// path). A non-literate entry has no remaps.
type entry struct {
	prog     *ast.Program
	srcs     map[string]string
	path     string                                     // diagnostic-header path: the .fern.md for literate, else the source path
	src      string                                     // document/entry source for remapped or entry diagnostics
	entryAbs string                                     // abs path of the entry module
	remaps   map[string]func(ast.Position) ast.Position // module-abs → tangled→document remap; nil when not literate
}

// remapFor turns a tangle line map into a position remapper: a tangled
// position (1-based line into the generated source) maps to its origin
// line in the `.fern.md` document, with the column shifted back by the
// indentation tangling prepended. Positions outside the map pass
// through unchanged.
func remapFor(lineMap []literate.Line) func(ast.Position) ast.Position {
	return func(p ast.Position) ast.Position {
		if p.Line < 1 || p.Line > len(lineMap) {
			return p
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
		prog, srcs, err := modload.Load(srcPath)
		e := entry{prog: prog, srcs: srcs, path: srcPath, entryAbs: abs}
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
	if doc.HasFiles() {
		return loadMultiFileEntry(srcPath, abs, litSrc, doc)
	}
	tangled, lineMap, err := doc.Tangle()
	if err != nil {
		// Tangle errors carry document-coordinate positions already.
		return entry{}, fmt.Errorf("%s", diag.Format(srcPath, litSrc, err))
	}
	prog, srcs, lerr := modload.LoadWith(srcPath, map[string]string{abs: tangled})
	e := entry{prog: prog, srcs: srcs, path: srcPath, src: litSrc, entryAbs: abs,
		remaps: map[string]func(ast.Position) ast.Position{abs: remapFor(lineMap)}}
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
	remaps := map[string]func(ast.Position) ast.Position{}
	for _, r := range results {
		fileAbs := resolve(r.Path)
		overrides[fileAbs] = r.Code
		remaps[fileAbs] = remapFor(r.LineMap)
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
	prog, srcs, lerr := modload.LoadWith(entryFile, overrides)
	e := entry{prog: prog, srcs: srcs, path: srcPath, src: litSrc, entryAbs: resolve(entryRel), remaps: remaps}
	if lerr != nil {
		return e, e.format(lerr)
	}
	return e, nil
}

// definesMain reports whether tangled source declares a top-level
// `main` function — used to disambiguate the compile entry among a
// multi-file document's modules when none is marked `entry`.
var mainFuncRe = regexp.MustCompile(`(?m)^\s*(pub\s+)?(function|fn)\s+main\b`)

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
	render := func(one error) string {
		path := ""
		if f, ok := one.(diag.Filed); ok {
			path = f.File()
		}
		if e.remaps != nil {
			// Literate: an error attributed to a generated module remaps
			// onto the document; an unattributed one (the checker's
			// pre-decl pass) is charged to the entry module's remap.
			if r, ok := e.remaps[path]; ok {
				return diag.FormatRemapped(e.path, e.src, r, one)
			}
			if path == "" {
				if r, ok := e.remaps[e.entryAbs]; ok {
					return diag.FormatRemapped(e.path, e.src, r, one)
				}
			}
			// A real (non-generated) module imported by the document —
			// stdlib or an on-disk `.fern` — renders against its own source.
			if src := e.srcs[path]; src != "" {
				return diag.Format(path, src, one)
			}
			if r, ok := e.remaps[e.entryAbs]; ok {
				return diag.FormatRemapped(e.path, e.src, r, one)
			}
			return diag.Format(e.path, e.src, one)
		}
		if path == "" || path == e.entryAbs {
			return diag.Format(e.path, e.src, one)
		}
		if src := e.srcs[path]; src != "" {
			return diag.Format(path, src, one)
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

// runLiterateTool implements the `-tangle` / `-weave` literate
// subcommands: parse the `.fern.md` document and write either the
// tangled Fern source or the woven Markdown. With `outPath` empty the
// result goes to stdout; otherwise it is written to disk — for a
// multi-file tangle `outPath` is a directory that receives one file per
// `file=` module (subdirectories created as needed), and for everything
// else it is a single output file.
func runLiterateTool(srcPath string, tangle bool, outPath string) (int, error) {
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return 1, err
	}
	src := string(srcBytes)
	doc := literate.Parse(src)
	if tangle {
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

func main() {
	out := flag.String("o", "", "output binary path; if unset, assembly is written to stdout")
	target := flag.String("target", "arm64", "code-generation backend: arm64 (default, Linux ELF), arm64-darwin (native Apple Silicon macOS), x86-64 (Linux ELF, in-process native backend by default), wasm (CLI component), wasi-http (HTTP handler component implementing wasi:http/incoming-handler), or wasm-ssa (experimental SSA-direct wasm core module; supports i32/i64/f32/f64, memory + alloc, string literals; pass -component-wrap-cli to lift as a wasi:cli/run component runnable via plain `wasmtime run`)")
	cc := flag.String("cc", "", "external assembler/linker invoked when -o or --run is set. arm64/x86-64 Linux and arm64-darwin all default to the in-process native backend (no external toolchain); passing -cc opts out to it (e.g. aarch64-linux-gnu-gcc / x86_64-linux-gnu-gcc on Linux, clang on darwin).")
	runIt := flag.Bool("run", false, "link to a temporary binary and execute it (arm64 Linux only; uses qemu-aarch64 when not on an arm64 host)")
	native := flag.Bool("native", false, "force the in-process pure-Go assembler+linker (internal/native). Already the DEFAULT for arm64/x86-64 Linux and arm64-darwin, so the flag is only needed to override an explicit -cc. No external assembler or linker; errors clearly on any unsupported instruction (pass -cc to fall back to an external toolchain).")
	qemu := flag.String("qemu", "qemu-aarch64", "user-mode emulator used by --run")
	repl := flag.Bool("repl", false, "start an interactive REPL via the AST interpreter")
	doInterp := flag.Bool("interp", false, "run FILE.fern (or `-` for stdin) through the AST interpreter — no codegen, no link, no binary. main()'s return value becomes the process exit code (clamped to 0..255). State is fresh per invocation; the REPL flag keeps an interactive session across lines.")
	componentWrap := flag.Bool("component-wrap", false, "with -target wasm-bin: wrap the core module as a self-contained preview-2 component via internal/wasm/component (no wasm-tools shell-out, no preview-1 adapter). Lifts main() as a component-level u32-returning export. Supports any mix of the migrated preview-2 imports; unrecognised imports surface a clear error.")
	componentWrapCli := flag.Bool("component-wrap-cli", false, "like -component-wrap but emits the wasi:cli/run@0.2.0 export shape so the produced component runs under plain `wasmtime run prog.wasm` (no --invoke). main()'s return value lowers to result<_, _>: 0 = ok, non-zero = err. void main is supported (auto-wrapped to return 0). Same WASI coverage as -component-wrap.")
	doFmt := flag.Bool("fmt", false, "format the source file and write to stdout (use -w to write back in place, -d to print a diff)")
	writeBack := flag.Bool("w", false, "with -fmt, overwrite the input file with the formatted output")
	diffMode := flag.Bool("d", false, "with -fmt, print a unified diff between the file and its formatted form; exits 1 when they differ")
	doCheck := flag.Bool("check", false, "type-check FILE.fern (or `-` for stdin) and its transitive imports. No codegen, no link, no binary. Silent on success; prints formatted diagnostics and exits 1 on the first error.")
	doTangle := flag.Bool("tangle", false, "tangle a literate FILE.fern.md (Knuth-style named chunks) into plain Fern source on stdout. Expands the root chunk `<<*>>`, resolving `<<chunk>>` references in definition order. A document using `file=PATH` blocks tangles to multiple modules, each printed under a `// ==> path <==` banner. With -o set, writes to disk instead: -o DIR receives one file per `file=` module (subdirs created as needed); a single-`<<*>>` document writes -o FILE. No codegen.")
	doWeave := flag.Bool("weave", false, "weave a literate FILE.fern.md into a cross-referenced Markdown reading document on stdout (or -o FILE) — chunk definitions get ⟨name⟩≡ labels and \"used in\" cross-references. No codegen.")
	listTargets := flag.Bool("targets", false, "list the supported -target= values with their descriptions + capability surface, then exit. Surfaces the Platform-descriptor table (internal/platforms) as the canonical source of truth for what each target accepts.")
	explain := flag.String("explain", "", "print the long-form explanation for an error code (e.g. -explain E001) and exit. Pass an empty string with no other args to list the available codes.")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: fern [-target arm64|arm64-darwin|x86-64|wasm] [-o OUTPUT] [--run] [-cc CC] [-qemu QEMU] FILE.fern [-- ARGS...]")
		fmt.Fprintln(os.Stderr, "       fern -fmt [-w | -d] FILE.fern")
		fmt.Fprintln(os.Stderr, "       fern -check FILE.fern | fern -check -      (type-check only; stdin form)")
		fmt.Fprintln(os.Stderr, "       fern -repl")
		fmt.Fprintln(os.Stderr, "       fern -interp FILE.fern | fern -interp -    (read from stdin)")
		fmt.Fprintln(os.Stderr, "       fern -tangle FILE.fern.md                  (literate: emit tangled Fern source)")
		fmt.Fprintln(os.Stderr, "       fern -weave  FILE.fern.md                  (literate: emit woven Markdown)")
		fmt.Fprintln(os.Stderr, "       fern -targets                                (list supported targets + capabilities)")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *listTargets {
		for _, name := range platforms.Targets() {
			d := platforms.ForTarget(name)
			fmt.Println(d.String())
			fmt.Printf("    capabilities: %v\n", d.Capabilities)
			fmt.Printf("    handlers:     %v\n", d.HandlerKinds)
			if len(d.Bindings) > 0 {
				fmt.Printf("    bindings:     %v\n", d.Bindings)
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
		if err := runCheck(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if (*doTangle || *doWeave) && flag.NArg() >= 1 {
		code, err := runLiterateTool(flag.Arg(0), *doTangle, *out)
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
	code, err := run(srcPath, *out, *target, *cc, *runIt, *native, *qemu, *componentWrap, *componentWrapCli, progArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

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
	prog, err := parser.Parse(src)
	if err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	formatted := printer.Format(prog)
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
	if err := constfold.Fold(prog); err != nil {
		return 1, formatErr(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return 1, formatErr(err)
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
		code := int(n)
		if code < 0 {
			code = -code
		}
		return code & 0xFF, nil
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
func runCheck(srcPath string) error {
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
	if err := constfold.Fold(prog); err != nil {
		return formatErr(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return formatErr(err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		return formatErr(err)
	}
	return nil
}

// run drives the full pipeline. The returned int is the exit code that
// the fern process itself should exit with: 0 in compile-only mode, or
// the program's own exit code under --run.
func run(srcPath, outPath, target, cc string, runIt, native bool, qemu string, componentWrap, componentWrapCli bool, progArgs []string) (int, error) {
	e, err := loadEntry(srcPath)
	if err != nil {
		return 1, err
	}
	prog := e.prog
	if err := constfold.Fold(prog); err != nil {
		return 1, e.format(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return 1, e.format(err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		return 1, e.format(err)
	}

	if target == "wasm-ssa" {
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
			return 1, fmt.Errorf("-target wasm-ssa requires -o OUTPUT")
		}
		bin, err := buildWasmSSA(prog, info)
		if err != nil {
			return 1, fmt.Errorf("wasm-ssa: %v", err)
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
		if req.Args || req.Env || req.Stdin || req.FileRead || req.FileWrite || req.FileAppend || req.FileReadWrite {
			return 1, fmt.Errorf("-target wasi-http: a handler can't use env / args / files / stdin — the http proxy world doesn't grant them")
		}
		comp := component.Compose(core, req, "wasi:http/incoming-handler@0.2.0#handle")
		if err := os.WriteFile(outPath, comp, 0o644); err != nil {
			return 1, err
		}
		return 0, nil
	}

	if target != "arm64" && target != "arm64-darwin" && target != "x86-64" {
		return 1, fmt.Errorf("unknown target %q (want arm64-darwin, arm64, x86-64, wasm, wasm-bin, or wasi-http)", target)
	}

	darwin := target == "arm64-darwin"
	var asm string
	switch target {
	case "x86-64":
		asm, err = x86_64codegen.EmitWithOptions(prog, info, x86_64codegen.Options{})
	default:
		asm, err = arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{Darwin: darwin})
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
	if native && target != "arm64" && target != "x86-64" && target != "arm64-darwin" {
		return 1, fmt.Errorf("-native is only supported with -target arm64, x86-64, or arm64-darwin (got %q)", target)
	}
	// arm64/x86-64 Linux and arm64-darwin all use the pure-Go
	// assembler+linker by default (no external toolchain). Pass -cc to opt
	// out to an external assembler/linker (gcc on Linux, clang on darwin).
	useNative := native || (!ccExplicit && (target == "arm64" || target == "x86-64" || darwin))
	switch {
	case useNative && darwin:
		if err := linkNativeDarwin(asm, binPath); err != nil {
			return 1, err
		}
	case useNative && target == "x86-64":
		if err := linkNativeX86(asm, binPath); err != nil {
			return 1, err
		}
	case useNative:
		if err := linkNative(asm, binPath); err != nil {
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
	if (target == "arm64" && runtime.GOARCH == "arm64") ||
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
	return wasmssa.EmitModule(f, "main")
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
// (the pure-Go internal/native backend). Unsupported instructions
// surface as an error rather than a miscompile.
func linkNative(asm, outPath string) error {
	text, rodata, err := nativearm64.AssembleProgram(asm, nativeelf.TextVAddr)
	if err != nil {
		return fmt.Errorf("native assembler: %w", err)
	}
	bin := nativeelf.StaticExecutableData(text, rodata)
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

// linkNativeX86 is the x86-64 counterpart of linkNative: it assembles and
// links x86-64 assembly into a static ELF executable entirely in-process
// via the pure-Go internal/native/x86_64 backend.
func linkNativeX86(asm, outPath string) error {
	text, rodata, err := nativex86.AssembleProgram(asm, nativeelf.TextVAddr)
	if err != nil {
		return fmt.Errorf("native assembler: %w", err)
	}
	bin := nativeelf.StaticExecutableDataX86(text, rodata)
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
	textVAddr, dataVAddr := nativemacho.SegmentAddrs(a.MachOTextLen(), a.MachODataLen())
	text, data, err := a.LinkMachO(textVAddr, dataVAddr)
	if err != nil {
		return fmt.Errorf("native assembler: %w", err)
	}
	bin := nativemacho.StaticExecutable(text, data, filepath.Base(outPath))
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

// buildPreview2Component is buildPreview2CliRunComponent generalised
// over the lift tail: exportName == "" produces the wasi:cli/run
// shape; a non-empty exportName lifts the run func as a u32-returning
// component func exported under that name (the non-cli
// `-component-wrap` shape, callable via `--invoke <name>()`). Both
// share the composer for every recognised import shape and fall back
// to the matching import-free builder.
func buildPreview2Component(prog *ast.Program, info *checker.Info, bin []byte, exportName string) ([]byte, error) {
	req, unsupported := component.ClassifyCore(bin)
	if len(unsupported) > 0 {
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
	// open-chain, and the list-returning args/env imports allocate
	// through cabi_realloc, so rebuild with ForceMemorySection when any
	// is present.
	req.ExportName = exportName
	b := bin
	if req.Stdin || req.FileRead || req.FileWrite || req.FileAppend || req.FileReadWrite || req.Args || req.Env {
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
