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
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/ssa"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
	"github.com/jakechampion/lang/internal/printer"
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

// formatLoadError renders a structured modload / checker error by
// pulling each entry's File() and looking that file's source up in
// the srcs map. Errors without a File() (the checker's pre-decl
// pass before c.current is set) fall back to entryPath's source.
// Renders each entry on its own block, matching what the previous
// in-modload wrapping produced for single-file programs.
func formatLoadError(err error, srcs map[string]string, entryPath string) error {
	if err == nil {
		return nil
	}
	entryAbs := absPath(entryPath)
	entrySrc := srcs[entryAbs]
	render := func(one error) string {
		path := ""
		if f, ok := one.(diag.Filed); ok {
			path = f.File()
		}
		src := srcs[path]
		if src == "" {
			path = entryPath
			src = entrySrc
		}
		return diag.Format(path, src, one)
	}
	if es, ok := err.(diag.Errors); ok {
		var b strings.Builder
		for i, e := range es {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(render(e))
		}
		return fmt.Errorf("%s", b.String())
	}
	return fmt.Errorf("%s", render(err))
}

func main() {
	out := flag.String("o", "", "output binary path; if unset, assembly is written to stdout")
	target := flag.String("target", "arm64", "code-generation backend: arm64 (default, Linux ELF), arm64-darwin (native Apple Silicon macOS), x86-64 (Linux ELF, in-progress; PR 1 supports `return N` only), wasm (CLI component), wasi-http (HTTP handler component implementing wasi:http/incoming-handler), or wasm-ssa (experimental SSA-direct wasm core module; supports i32/i64/f32/f64, memory + alloc, string literals; pass -component-wrap-cli to lift as a wasi:cli/run component runnable via plain `wasmtime run`)")
	cc := flag.String("cc", "", "linker invoked when -o or --run is set; defaults to aarch64-linux-gnu-gcc for arm64 Linux, clang for arm64-darwin, x86_64-linux-gnu-gcc for x86-64")
	runIt := flag.Bool("run", false, "link to a temporary binary and execute it (arm64 Linux only; uses qemu-aarch64 when not on an arm64 host)")
	qemu := flag.String("qemu", "qemu-aarch64", "user-mode emulator used by --run")
	repl := flag.Bool("repl", false, "start an interactive REPL via the AST interpreter")
	doInterp := flag.Bool("interp", false, "run FILE.fern (or `-` for stdin) through the AST interpreter — no codegen, no link, no binary. main()'s return value becomes the process exit code (clamped to 0..255). State is fresh per invocation; the REPL flag keeps an interactive session across lines.")
	wasiAdapter := flag.String("wasi-adapter", "", "path to the wasi_snapshot_preview1.command.wasm adapter (required for -target wasm; see docs/WASI-PREVIEW2.md)")
	componentWrap := flag.Bool("component-wrap", false, "with -target wasm-bin and no -wasi-adapter: wrap the core module as a self-contained preview-2 component via internal/wasm/component (no wasm-tools shell-out). Lifts main() as a component-level u32-returning export. Supports programs with no WASI imports or with the migrated preview-2 imports (wasi:cli/exit, wasi:random/random, wasi:clocks/monotonic-clock); unrecognised imports surface a clear error.")
	componentWrapCli := flag.Bool("component-wrap-cli", false, "like -component-wrap but emits the wasi:cli/run@0.2.0 export shape so the produced component runs under plain `wasmtime run prog.wasm` (no --invoke). main()'s return value lowers to result<_, _>: 0 = ok, non-zero = err. void main is supported (auto-wrapped to return 0). Same WASI coverage as -component-wrap.")
	doFmt := flag.Bool("fmt", false, "format the source file and write to stdout (use -w to write back in place, -d to print a diff)")
	writeBack := flag.Bool("w", false, "with -fmt, overwrite the input file with the formatted output")
	diffMode := flag.Bool("d", false, "with -fmt, print a unified diff between the file and its formatted form; exits 1 when they differ")
	doCheck := flag.Bool("check", false, "type-check FILE.fern (or `-` for stdin) and its transitive imports. No codegen, no link, no binary. Silent on success; prints formatted diagnostics and exits 1 on the first error.")
	listTargets := flag.Bool("targets", false, "list the supported -target= values with their descriptions + capability surface, then exit. Surfaces the Platform-descriptor table (internal/platforms) as the canonical source of truth for what each target accepts.")
	explain := flag.String("explain", "", "print the long-form explanation for an error code (e.g. -explain E001) and exit. Pass an empty string with no other args to list the available codes.")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: fern [-target arm64|arm64-darwin|x86-64|wasm] [-o OUTPUT] [--run] [-cc CC] [-qemu QEMU] FILE.fern [-- ARGS...]")
		fmt.Fprintln(os.Stderr, "       fern -fmt [-w | -d] FILE.fern")
		fmt.Fprintln(os.Stderr, "       fern -check FILE.fern | fern -check -      (type-check only; stdin form)")
		fmt.Fprintln(os.Stderr, "       fern -repl")
		fmt.Fprintln(os.Stderr, "       fern -interp FILE.fern | fern -interp -    (read from stdin)")
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
	code, err := run(srcPath, *out, *target, *cc, *runIt, *qemu, *wasiAdapter, *componentWrap, *componentWrapCli, progArgs)
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
	var src string
	if srcPath == "-" {
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return 1, fmt.Errorf("read stdin: %w", err)
		}
		src = string(buf)
		p, err := parser.Parse(src)
		if err != nil {
			return 1, fmt.Errorf("%s", diag.Format("<stdin>", src, err))
		}
		prog = p
	} else {
		p, srcs, err := modload.Load(srcPath)
		if err != nil {
			return 1, formatLoadError(err, srcs, srcPath)
		}
		prog = p
		src = srcs[absPath(srcPath)]
	}
	if err := constfold.Fold(prog); err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	info, err := checker.Check(prog)
	if err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	if err := monomorph.Run(prog, info); err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
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
	var src string
	var srcs map[string]string
	if srcPath == "-" {
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		src = string(buf)
		p, err := parser.Parse(src)
		if err != nil {
			return fmt.Errorf("%s", diag.Format("<stdin>", src, err))
		}
		prog = p
		srcPath = "<stdin>"
	} else {
		p, ss, err := modload.Load(srcPath)
		if err != nil {
			return formatLoadError(err, ss, srcPath)
		}
		prog = p
		srcs = ss
		src = ss[absPath(srcPath)]
	}
	if err := constfold.Fold(prog); err != nil {
		return fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	info, err := checker.Check(prog)
	if err != nil {
		return formatLoadError(err, srcs, srcPath)
	}
	if err := monomorph.Run(prog, info); err != nil {
		return fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	return nil
}

// run drives the full pipeline. The returned int is the exit code that
// the fern process itself should exit with: 0 in compile-only mode, or
// the program's own exit code under --run.
func run(srcPath, outPath, target, cc string, runIt bool, qemu string, wasiAdapter string, componentWrap, componentWrapCli bool, progArgs []string) (int, error) {
	prog, srcs, err := modload.Load(srcPath)
	if err != nil {
		return 1, formatLoadError(err, srcs, srcPath)
	}
	src := srcs[absPath(srcPath)]
	if err := constfold.Fold(prog); err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
	}
	info, err := checker.Check(prog)
	if err != nil {
		return 1, formatLoadError(err, srcs, srcPath)
	}
	if err := monomorph.Run(prog, info); err != nil {
		return 1, fmt.Errorf("%s", diag.Format(srcPath, src, err))
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
		// Binary backend (internal/codegen/wasmbin) — produces
		// wasm core module bytes directly, no `wasm-tools parse`
		// shell-out.
		//
		// Output mode:
		//   - no `-wasi-adapter`: write the raw core module to
		//     outPath. Runnable via `wasmtime run --invoke <fn>`.
		//   - `-wasi-adapter PATH`: wrap in a preview-2 component
		//     matching the `fern` world, composing with the
		//     adapter. Runnable via `wasmtime run` and deployable
		//     to any preview-2 host.
		if outPath == "" {
			return 1, fmt.Errorf("-target wasm-bin requires -o OUTPUT")
		}
		// Pre-wrap mode forces the memory section so the WASI
		// preview-1 adapter's env::memory import is satisfied
		// during `wasm-tools component new`. Component-wrap mode
		// instead opts in to the preview-2 import migration so
		// the Go-side wrapper can route those imports through
		// `wasi:cli/exit@0.2.0` etc. without involving the
		// preview-1 adapter.
		bin, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
			ForceMemorySection: wasiAdapter != "",
			SynthStart:         wasiAdapter != "",
			Preview2WASI:       componentWrap || componentWrapCli,
			SynthCliRun:        componentWrap || componentWrapCli,
		})
		if err != nil {
			return 1, err
		}
		if wasiAdapter == "" {
			if componentWrap {
				// Wrap the core module as a preview-2 component via the
				// Go-side encoder (no wasm-tools, no adapter), lifting the
				// run func as a component-level u32-returning export named
				// "main" (callable via `wasmtime run --invoke main()`).
				// Same composer as -component-wrap-cli, only the lift tail
				// differs (named u32 export vs wasi:cli/run). `_lang_run`
				// is the SynthCliRun wrapper normalising main's signature
				// to `() -> i32`.
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
				// Wrap as a wasi:cli/run-exporting component so the
				// produced binary runs under plain `wasmtime run` (no
				// --invoke). Routing, in order:
				//
				// buildPreview2CliRunComponent composes any recognised
				// CLI-stream / filesystem-open / wall-clock-args-env /
				// structured shape (or a bare cli/run for an import-free
				// program) and errors on imports it can't place.
				//
				// The cli-run lift consumes a `() -> i32` core
				// export. wasmbin's SynthCliRun has already emitted
				// `_lang_run` as the normalised entry — main with
				// any signature (void / i32) flows through it.
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
		if err := emitPreview2ComponentFromCoreBytes(bin, outPath, wasiAdapter, "fern"); err != nil {
			return 1, err
		}
		return 0, nil
	}

	if target == "wasm" || target == "wasi-http" {
		// Both CLI wasm targets route through wasmbin. WAT is
		// out of the user-facing wasm story; what's left of
		// internal/codegen/wasm/ is dev tooling (cmd/fern-wasm,
		// cmd/dump_wat) waiting on a downstream cleanup PR.
		if outPath == "" {
			return 1, fmt.Errorf("-target %s requires -o OUTPUT (the component is a binary)", target)
		}
		// `-target wasm` without `-wasi-adapter` flows through the
		// Go-side preview-2 encoder (no `wasm-tools component new`
		// shell-out). Equivalent to `-target wasm-bin
		// -component-wrap-cli` but exposed as the friendlier
		// default. The path only works when the program's imports
		// are all preview-2-migrated (see knownPreview2Imports);
		// anything else gets a clear "use -wasi-adapter" error.
		// `-target wasi-http` still requires the adapter — its
		// imports use list / record / resource shapes the Go
		// encoder doesn't cover yet.
		if target == "wasm" && wasiAdapter == "" {
			bin, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
				Preview2WASI: true,
				SynthCliRun:  true,
			})
			if err != nil {
				return 1, err
			}
			comp, err := buildPreview2CliRunComponent(prog, info, bin)
			if err != nil {
				return 1, fmt.Errorf("-target wasm without -wasi-adapter %w", err)
			}
			if err := os.WriteFile(outPath, comp, 0o644); err != nil {
				return 1, err
			}
			return 0, nil
		}
		if wasiAdapter == "" {
			return 1, fmt.Errorf("-target %s requires -wasi-adapter PATH (see docs/WASI-PREVIEW2.md)", target)
		}
		opts := wasmbin.BuildOptions{
			ForceMemorySection: true,
			SynthStart:         true,
		}
		world := "fern"
		if target == "wasi-http" {
			opts.HttpHandler = true
			opts.SynthStart = false // empty `_start` stub emitted by the HttpHandler branch
			world = "http"
		}
		bin, err := wasmbin.BuildWithOptions(prog, info, opts)
		if err != nil {
			return 1, err
		}
		if err := emitPreview2ComponentFromCoreBytes(bin, outPath, wasiAdapter, world); err != nil {
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
	if darwin {
		if err := linkDarwin(asm, binPath, cc); err != nil {
			return 1, err
		}
	} else if err := link(asm, binPath, cc); err != nil {
		return 1, err
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
	// If the user left -qemu at its default but built for
	// x86-64, swap to qemu-x86_64 so --run picks the right
	// emulator without manual flag-flipping.
	if target == "x86-64" && qemu == "qemu-aarch64" {
		qemu = "qemu-x86_64"
	}
	return execUnderQemu(qemu, binPath, progArgs)
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

// emitPreview2ComponentFromCoreBytes takes already-binary core wasm
// bytes (e.g. from wasmbin.Build) and runs steps 3+4 of the
// pipeline. wasm-tools parse is unnecessary because the input is
// already binary. wasm-tools component new is still required for
// the adapter composition step.
func emitPreview2ComponentFromCoreBytes(coreBytes []byte, outPath, adapterPath, world string) error {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		return fmt.Errorf("wasm-tools not found on PATH (install from https://github.com/bytecodealliance/wasm-tools): %w", err)
	}
	if _, err := os.Stat(adapterPath); err != nil {
		return fmt.Errorf("wasi-preview1-component-adapter not readable at %q: %w", adapterPath, err)
	}
	tmpDir, err := os.MkdirTemp("", "fern-component-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	embeddedBytes, err := componenttype.Embed(coreBytes, world)
	if err != nil {
		return fmt.Errorf("embed component-type section: %w", err)
	}
	embeddedPath := filepath.Join(tmpDir, "prog.embedded.wasm")
	if err := os.WriteFile(embeddedPath, embeddedBytes, 0o644); err != nil {
		return fmt.Errorf("write embedded module: %w", err)
	}
	if out, err := exec.Command("wasm-tools", "component", "new",
		"--adapt", "wasi_snapshot_preview1="+adapterPath,
		embeddedPath, "-o", outPath).CombinedOutput(); err != nil {
		return fmt.Errorf("wasm-tools component new failed: %w\n%s", err, out)
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

// preview2ImportSpec describes one preview-2 WASI import the
// driver knows how to route through the composer.
// Keyed by (core-module, core-name) the core wasm module declared
// — i.e. the import-name pair wasmbin emitted under
// `EmitOptions.Preview2WASI`.
type preview2ImportSpec struct {
	interfaceName    string
	paramNames       []string
	paramValtypes    []byte
	coreImportModule string
	innerTypes       [][]byte
	resultValtypes   []byte
}

// knownPreview2Imports is the registry of (core-module, core-name)
// pairs the driver can map to component-level
// `component.WasiImport` records. Grows as more preview-2 imports
// land in wasmbin (see docs/TOOLCHAIN-SELF-HOSTING.md).
//
// `wasi:cli/exit@0.2.0::exit` takes `func(status: result<_, _>)`,
// so its WasiImport carries InnerTypes = [InnerTypeResultEmpty]
// and its param valtype is byte 0x00 — read by the binary parser
// as the inner-scope typeidx of the result type.
var knownPreview2Imports = map[[2]string]preview2ImportSpec{
	{"wasi:cli/exit@0.2.0", "exit"}: {
		interfaceName:    "wasi:cli/exit@0.2.0",
		paramNames:       []string{"status"},
		paramValtypes:    []byte{0x00},
		coreImportModule: "wasi:cli/exit@0.2.0",
		innerTypes:       [][]byte{component.InnerTypeResultEmpty},
	},
	{"wasi:random/random@0.2.0", "get-random-u64"}: {
		interfaceName:    "wasi:random/random@0.2.0",
		paramNames:       nil,
		paramValtypes:    nil,
		coreImportModule: "wasi:random/random@0.2.0",
		innerTypes:       nil,
		resultValtypes:   []byte{component.CValtypeU64},
	},
	{"wasi:clocks/monotonic-clock@0.2.0", "now"}: {
		interfaceName:    "wasi:clocks/monotonic-clock@0.2.0",
		paramNames:       nil,
		paramValtypes:    nil,
		coreImportModule: "wasi:clocks/monotonic-clock@0.2.0",
		innerTypes:       nil,
		resultValtypes:   []byte{component.CValtypeU64},
	},
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
	// TCP servers (wasi:sockets) are a self-contained shape with their
	// own composer — always the wasi:cli/run lift (a server isn't an
	// --invoke export). The socket methods write fixed-size results
	// through caller retptrs, so the module needs memory exported
	// (ForceMemorySection); no realloc (no list returns).
	if usesPreview2TcpServer(bin) {
		rb, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
			ForceMemorySection: true,
			Preview2WASI:       true,
			SynthCliRun:        true,
		})
		if err != nil {
			return nil, err
		}
		hasRead, hasWrite, hasEnv := tcpStreamUsage(bin)
		return component.ComposeTcpServerCliRun(rb, hasRead, hasWrite, hasEnv, "_lang_run"), nil
	}
	if opts, ok := classifyComposeCliStream(bin); ok {
		opts.ExportName = exportName
		needsRealloc := opts.ReadStdin || opts.FileRead || opts.FileWrite || opts.FileAppend
		for _, mt := range opts.MemTramp {
			if mt.NeedsRealloc {
				needsRealloc = true
			}
		}
		b := bin
		if needsRealloc {
			rb, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
				ForceMemorySection: true,
				Preview2WASI:       true,
				SynthCliRun:        true,
			})
			if err != nil {
				return nil, err
			}
			b = rb
		}
		return component.ComposePreview2CliRun(b, opts, "_lang_run"), nil
	}
	// classifyComposeCliStream already accepts every known import; if it
	// declined, the module is either import-free or carries something we
	// can't place.
	if _, unknown := classifyPreview2Imports(bin); len(unknown) > 0 {
		return nil, fmt.Errorf("can't wrap a core module with unrecognised imports yet (saw %d): %s. Either remove the source that pulls them in or use -wasi-adapter for now.", len(unknown), strings.Join(unknown, ", "))
	}
	if exportName != "" {
		return component.BuildLiftedExportComponent(bin, "_lang_run", exportName, nil, nil, component.CValtypeU32), nil
	}
	return component.BuildWasiCliRunComponent(bin, "_lang_run"), nil
}

// preview2TcpServerImports is the exact import set a listen/accept/close
// TCP server pulls in (no recv/send — those add blocking-read/-write,
// not yet composed). usesPreview2TcpServer reports whether every import
// is in this set and at least one wasi:sockets import is present.
var preview2TcpServerImports = map[[2]string]bool{
	{"wasi:sockets/instance-network@0.2.0", "instance-network"}:   true,
	{"wasi:sockets/tcp-create-socket@0.2.0", "create-tcp-socket"}: true,
	{"wasi:sockets/tcp@0.2.0", "[method]tcp-socket.start-bind"}:    true,
	{"wasi:sockets/tcp@0.2.0", "[method]tcp-socket.finish-bind"}:   true,
	{"wasi:sockets/tcp@0.2.0", "[method]tcp-socket.start-listen"}:  true,
	{"wasi:sockets/tcp@0.2.0", "[method]tcp-socket.finish-listen"}: true,
	{"wasi:sockets/tcp@0.2.0", "[method]tcp-socket.accept"}:        true,
	{"wasi:sockets/tcp@0.2.0", "[method]tcp-socket.subscribe"}:     true,
	{"wasi:sockets/tcp@0.2.0", "[resource-drop]tcp-socket"}:        true,
	{"wasi:io/poll@0.2.0", "[method]pollable.block"}:               true,
	{"wasi:io/poll@0.2.0", "[resource-drop]pollable"}:              true,
	{"wasi:io/streams@0.2.0", "[resource-drop]input-stream"}:       true,
	{"wasi:io/streams@0.2.0", "[resource-drop]output-stream"}:      true,
	// tcp_recv / tcp_send (read/write the accepted connection).
	{"wasi:io/streams@0.2.0", "[method]input-stream.blocking-read"}:             true,
	{"wasi:io/streams@0.2.0", "[method]output-stream.blocking-write-and-flush"}: true,
	// env() — an HTTP-over-TCP handler reads its listen port from PORT
	// (the synthesised main → __port_from_env → env() → get-environment).
	{"wasi:cli/environment@0.2.0", "get-environment"}: true,
}

// tcpStreamUsage reports whether a TCP program reads (tcp_recv →
// input-stream.blocking-read), writes (tcp_send →
// output-stream.blocking-write-and-flush), and/or reads the
// environment (env() → wasi:cli/environment.get-environment).
func tcpStreamUsage(bin []byte) (hasRead, hasWrite, hasEnv bool) {
	for _, p := range coreModuleImportPairs(bin) {
		switch {
		case p.module == "wasi:io/streams@0.2.0" && p.name == "[method]input-stream.blocking-read":
			hasRead = true
		case p.module == "wasi:io/streams@0.2.0" && p.name == "[method]output-stream.blocking-write-and-flush":
			hasWrite = true
		case p.module == "wasi:cli/environment@0.2.0" && p.name == "get-environment":
			hasEnv = true
		}
	}
	return hasRead, hasWrite, hasEnv
}

func usesPreview2TcpServer(bin []byte) bool {
	sawSocket := false
	for _, p := range coreModuleImportPairs(bin) {
		if !preview2TcpServerImports[[2]string{p.module, p.name}] {
			return false
		}
		if strings.HasPrefix(p.module, "wasi:sockets/") {
			sawSocket = true
		}
	}
	return sawSocket
}

func classifyComposeCliStream(bin []byte) (component.ComposeOpts, bool) {
	var opts component.ComposeOpts
	var getStdout, getStderr, getStdin, blockWrite, blockRead bool
	var getDirs, openAt, readVia, writeVia, appendVia bool
	var wallNow, getArgs, getEnv bool
	for _, p := range coreModuleImportPairs(bin) {
		switch {
		case p.module == "wasi:cli/stdout@0.2.0" && p.name == "get-stdout":
			getStdout = true
		case p.module == "wasi:cli/stderr@0.2.0" && p.name == "get-stderr":
			getStderr = true
		case p.module == "wasi:cli/stdin@0.2.0" && p.name == "get-stdin":
			getStdin = true
		case p.module == "wasi:io/streams@0.2.0" && p.name == "[method]output-stream.blocking-write-and-flush":
			blockWrite = true
		case p.module == "wasi:io/streams@0.2.0" && p.name == "[method]input-stream.blocking-read":
			blockRead = true
		case p.module == "wasi:filesystem/preopens@0.2.0" && p.name == "get-directories":
			getDirs = true
		case p.module == "wasi:filesystem/types@0.2.0" && p.name == "[method]descriptor.open-at":
			openAt = true
		case p.module == "wasi:filesystem/types@0.2.0" && p.name == "[method]descriptor.read-via-stream":
			readVia = true
		case p.module == "wasi:filesystem/types@0.2.0" && p.name == "[method]descriptor.write-via-stream":
			writeVia = true
		case p.module == "wasi:filesystem/types@0.2.0" && p.name == "[method]descriptor.append-via-stream":
			appendVia = true
		case p.module == "wasi:clocks/wall-clock@0.2.0" && p.name == "now":
			wallNow = true
		case p.module == "wasi:cli/environment@0.2.0" && p.name == "get-arguments":
			getArgs = true
		case p.module == "wasi:cli/environment@0.2.0" && p.name == "get-environment":
			getEnv = true
		default:
			spec, ok := knownPreview2Imports[[2]string{p.module, p.name}]
			if !ok {
				return component.ComposeOpts{}, false
			}
			opts.Structured = append(opts.Structured, component.WasiImport{
				InterfaceName:    spec.interfaceName,
				FuncName:         p.name,
				ParamNames:       spec.paramNames,
				ParamValtypes:    spec.paramValtypes,
				CoreImportModule: spec.coreImportModule,
				InnerTypes:       spec.innerTypes,
				ResultValtypes:   spec.resultValtypes,
			})
		}
	}
	if getStdout && getStderr {
		return component.ComposeOpts{}, false // single write stream only
	}
	// The filesystem open-chain (shared get-directories + open-at, plus
	// exactly one descriptor stream method: read-, write-, or
	// append-via-stream) must be complete and single-direction. Two
	// directions in one program isn't supported — the filesystem/types
	// instance type carries one method.
	fsAny := getDirs || openAt || readVia || writeVia || appendVia
	fileRead := getDirs && openAt && readVia && !writeVia && !appendVia
	fileWrite := getDirs && openAt && writeVia && !readVia && !appendVia
	fileAppend := getDirs && openAt && appendVia && !readVia && !writeVia
	if fsAny && !(fileRead || fileWrite || fileAppend) {
		return component.ComposeOpts{}, false
	}
	// blocking-write backs print/eprint and the file write/append-chain;
	// blocking-read backs stdin reads and the file read-chain. The
	// method can't appear without a producer that yields a stream to
	// it, but a producer *without* the method is fine — a bare
	// open_reader/open_writer opens a handle and never reads/writes.
	writeGetter := getStdout || getStderr
	if blockWrite && !(writeGetter || fileWrite || fileAppend) {
		return component.ComposeOpts{}, false
	}
	if blockRead && !(getStdin || fileRead) {
		return component.ComposeOpts{}, false
	}
	if getStdout {
		opts.WriteGetter = "get-stdout"
	} else if getStderr {
		opts.WriteGetter = "get-stderr"
	}
	opts.ReadStdin = getStdin
	opts.FileRead = fileRead
	opts.FileWrite = fileWrite
	opts.FileAppend = fileAppend
	opts.ReadStream = blockRead
	opts.WriteStream = blockWrite
	// Mem-trampoline imports (wall-clock now / args / env). args and
	// env share the wasi:cli/environment interface, so they can't both
	// be imported as separate instances — reject that combination.
	if getArgs && getEnv {
		return component.ComposeOpts{}, false
	}
	if wallNow {
		opts.MemTramp = append(opts.MemTramp, component.MemTrampImport{
			InstanceTypeBody: component.WasiClocksWallClockInstanceTypeBody(),
			InterfaceName:    "wasi:clocks/wall-clock@0.2.0",
			FuncName:         "now",
		})
	}
	if getArgs {
		opts.MemTramp = append(opts.MemTramp, component.MemTrampImport{
			InstanceTypeBody: component.WasiCliEnvironmentArgsInstanceTypeBody(),
			InterfaceName:    "wasi:cli/environment@0.2.0",
			FuncName:         "get-arguments",
			NeedsRealloc:     true,
		})
	}
	if getEnv {
		opts.MemTramp = append(opts.MemTramp, component.MemTrampImport{
			InstanceTypeBody: component.WasiCliEnvironmentGetEnvironmentInstanceTypeBody(),
			InterfaceName:    "wasi:cli/environment@0.2.0",
			FuncName:         "get-environment",
			NeedsRealloc:     true,
		})
	}
	// Claim any shape with at least one import (stream / file /
	// mem-trampoline / structured). Only a truly import-free program
	// falls through — to BuildWasiCliRunComponent.
	if opts.WriteGetter == "" && !opts.ReadStdin && !opts.FileRead && !opts.FileWrite && !opts.FileAppend && len(opts.MemTramp) == 0 && len(opts.Structured) == 0 {
		return component.ComposeOpts{}, false
	}
	return opts, true
}

// classifyPreview2Imports walks the core module's import section and
// bucketises each: known structured imports become component.WasiImport
// entries; anything else is returned as
// an "module.name" string so the driver can surface a clear error.
func classifyPreview2Imports(bin []byte) ([]component.WasiImport, []string) {
	pairs := coreModuleImportPairs(bin)
	var wasi []component.WasiImport
	var unknown []string
	for _, p := range pairs {
		spec, ok := knownPreview2Imports[[2]string{p.module, p.name}]
		if !ok {
			unknown = append(unknown, p.module+"."+p.name)
			continue
		}
		wasi = append(wasi, component.WasiImport{
			InterfaceName:    spec.interfaceName,
			FuncName:         p.name,
			ParamNames:       spec.paramNames,
			ParamValtypes:    spec.paramValtypes,
			CoreImportModule: spec.coreImportModule,
			InnerTypes:       spec.innerTypes,
			ResultValtypes:   spec.resultValtypes,
		})
	}
	return wasi, unknown
}

// coreModuleImport is one (module, name) pair from the import
// section.
type coreModuleImport struct{ module, name string }

// coreModuleImportPairs walks a core wasm module's import section
// and returns each (module, name) pair in declaration order. Bails
// out silently on malformed input or no import section.
func coreModuleImportPairs(bin []byte) []coreModuleImport {
	const preambleLen = 8
	if len(bin) < preambleLen {
		return nil
	}
	off := preambleLen
	for off < len(bin) {
		id := bin[off]
		off++
		size, n := readULEB(bin[off:])
		if n == 0 {
			return nil
		}
		off += n
		if off+int(size) > len(bin) {
			return nil
		}
		body := bin[off : off+int(size)]
		off += int(size)
		if id != 2 {
			continue
		}
		count, m := readULEB(body)
		if m == 0 {
			return nil
		}
		body = body[m:]
		var pairs []coreModuleImport
		for i := uint64(0); i < count && len(body) > 0; i++ {
			mod, body2 := readName(body)
			fld, body3 := readName(body2)
			if len(body3) < 1 {
				break
			}
			kind := body3[0]
			body3 = body3[1:]
			switch kind {
			case 0: // func: typeidx uleb
				_, ks := readULEB(body3)
				body3 = body3[ks:]
			case 1: // table: reftype byte + limits
				if len(body3) >= 2 {
					body3 = body3[2:]
					_, ks := readULEB(body3)
					body3 = body3[ks:]
				}
			case 2: // memory: limits
				if len(body3) >= 1 {
					flag := body3[0]
					body3 = body3[1:]
					_, ks := readULEB(body3)
					body3 = body3[ks:]
					if flag == 1 {
						_, ks2 := readULEB(body3)
						body3 = body3[ks2:]
					}
				}
			case 3: // global: valtype byte + mut byte
				if len(body3) >= 2 {
					body3 = body3[2:]
				}
			}
			body = body3
			pairs = append(pairs, coreModuleImport{module: mod, name: fld})
		}
		return pairs
	}
	return nil
}

// coreModuleHasImports peeks at a core wasm module's import
// section to check whether it declares any imports. Used by the
// -component-wrap path to give a clean error when the source's
// wasmbin output would need a preview-1 adapter (something
// `component.BuildLiftedExportComponent` doesn't handle).
//
// Returns (false, nil) when the module has no import section or
// the section is empty. The names are returned as
// "module.fieldName" tuples for the error message.
//
// The import section is identifier 2 in core wasm; layout is
// `id:u8 size:uleb body`. Body is `count:uleb (module:name name:name kind:u8 ...)*`.
// This skips section bodies it doesn't recognize and bails out
// on malformed input.
func coreModuleHasImports(bin []byte) (bool, []string) {
	const preambleLen = 8
	if len(bin) < preambleLen {
		return false, nil
	}
	off := preambleLen
	for off < len(bin) {
		id := bin[off]
		off++
		size, n := readULEB(bin[off:])
		if n == 0 {
			return false, nil
		}
		off += n
		if off+int(size) > len(bin) {
			return false, nil
		}
		body := bin[off : off+int(size)]
		off += int(size)
		if id != 2 {
			continue
		}
		count, m := readULEB(body)
		if m == 0 || count == 0 {
			return false, nil
		}
		body = body[m:]
		var names []string
		for i := uint64(0); i < count && len(body) > 0; i++ {
			mod, body2 := readName(body)
			fld, body3 := readName(body2)
			if len(body3) < 1 {
				break
			}
			kind := body3[0]
			body3 = body3[1:]
			// Skip the kind-specific suffix.
			switch kind {
			case 0: // func: typeidx uleb
				_, ks := readULEB(body3)
				body3 = body3[ks:]
			case 1: // table: reftype byte + limits
				if len(body3) >= 2 {
					body3 = body3[2:]
					_, ks := readULEB(body3)
					body3 = body3[ks:]
				}
			case 2: // memory: limits
				if len(body3) >= 1 {
					flag := body3[0]
					body3 = body3[1:]
					_, ks := readULEB(body3)
					body3 = body3[ks:]
					if flag == 1 {
						_, ks2 := readULEB(body3)
						body3 = body3[ks2:]
					}
				}
			case 3: // global: valtype byte + mut byte
				if len(body3) >= 2 {
					body3 = body3[2:]
				}
			}
			body = body3
			names = append(names, mod+"."+fld)
		}
		return len(names) > 0, names
	}
	return false, nil
}

// readULEB decodes a uleb128-encoded uint up to 10 bytes; returns
// (value, bytes consumed). Returns (0, 0) on malformed input.
func readULEB(b []byte) (uint64, int) {
	var v uint64
	var shift uint
	for i := 0; i < 10 && i < len(b); i++ {
		x := b[i]
		v |= uint64(x&0x7f) << shift
		if x&0x80 == 0 {
			return v, i + 1
		}
		shift += 7
	}
	return 0, 0
}

// readName reads a uleb-prefixed UTF-8 name; returns (name, rest)
// or ("", b) when malformed.
func readName(b []byte) (string, []byte) {
	n, k := readULEB(b)
	if k == 0 {
		return "", b
	}
	b = b[k:]
	if uint64(len(b)) < n {
		return "", b
	}
	return string(b[:n]), b[n:]
}
