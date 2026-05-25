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
	componentWrap := flag.Bool("component-wrap", false, "with -target wasm-bin: wrap the core module as a self-contained preview-2 component via internal/wasm/component (no wasm-tools shell-out, no preview-1 adapter). Lifts main() as a component-level u32-returning export. Supports any mix of the migrated preview-2 imports; unrecognised imports surface a clear error.")
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
	code, err := run(srcPath, *out, *target, *cc, *runIt, *qemu, *componentWrap, *componentWrapCli, progArgs)
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
func run(srcPath, outPath, target, cc string, runIt bool, qemu string, componentWrap, componentWrapCli bool, progArgs []string) (int, error) {
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
		// Both CLI wasm targets route through wasmbin. WAT is
		// out of the user-facing wasm story; what's left of
		// internal/codegen/wasm/ is dev tooling (cmd/fern-wasm,
		// cmd/dump_wat) waiting on a downstream cleanup PR.
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
		req, unsupported := classifyComposeRequest(core)
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

// classifyComposeRequest walks a core module's preview-2 imports and
// fills a component.ComposeRequest — the single description the unified
// composer (component.Compose) consumes. Every recognised import maps to
// a request field (CLI stdio, io/streams methods + drops, the filesystem
// open-chain, TCP / UDP / HTTP method surfaces, the standalone clock /
// args / env capabilities, and the structured exit/random/monotonic
// no-opts). Anything unrecognised is returned in unsupported so the
// caller can surface a clear error. Because the request fields are
// independent, ANY mix composes — sockets + files, stdout + stderr,
// args + env, an HTTP handler that logs, etc.
func classifyComposeRequest(bin []byte) (component.ComposeRequest, []string) {
	var req component.ComposeRequest
	var unsupported []string
	var getDirs, openAt, readVia, writeVia, appendVia bool
	for _, p := range coreModuleImportPairs(bin) {
		m, n := p.module, p.name
		switch {
		case m == "wasi:cli/stdout@0.2.0" && n == "get-stdout":
			req.Stdout = true
		case m == "wasi:cli/stderr@0.2.0" && n == "get-stderr":
			req.Stderr = true
		case m == "wasi:cli/stdin@0.2.0" && n == "get-stdin":
			req.Stdin = true
		case m == "wasi:io/streams@0.2.0" && n == "[method]output-stream.blocking-write-and-flush":
			req.BlockWrite = true
		case m == "wasi:io/streams@0.2.0" && n == "[method]input-stream.blocking-read":
			req.BlockRead = true
		case m == "wasi:io/streams@0.2.0" && n == "[resource-drop]input-stream":
			req.DropInput = true
		case m == "wasi:io/streams@0.2.0" && n == "[resource-drop]output-stream":
			req.DropOutput = true
		case m == "wasi:filesystem/preopens@0.2.0" && n == "get-directories":
			getDirs = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.open-at":
			openAt = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.read-via-stream":
			readVia = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.write-via-stream":
			writeVia = true
		case m == "wasi:filesystem/types@0.2.0" && n == "[method]descriptor.append-via-stream":
			appendVia = true
		case m == "wasi:clocks/wall-clock@0.2.0" && n == "now":
			req.WallNow = true
		case m == "wasi:cli/environment@0.2.0" && n == "get-arguments":
			req.Args = true
		case m == "wasi:cli/environment@0.2.0" && n == "get-environment":
			req.Env = true
		case strings.HasPrefix(m, "wasi:sockets/tcp"):
			req.Tcp = true // wasi:sockets/tcp@ + tcp-create-socket@
		case strings.HasPrefix(m, "wasi:sockets/udp"):
			req.Udp = true
		case m == "wasi:sockets/instance-network@0.2.0":
			// consumed by the TCP / UDP shape; accepted implicitly
		case m == "wasi:io/poll@0.2.0":
			// consumed by the TCP shape; accepted implicitly
		case m == "wasi:http/types@0.2.0":
			req.Http = true
		default:
			if spec, ok := knownPreview2Imports[[2]string{m, n}]; ok {
				req.Structured = append(req.Structured, component.WasiImport{
					InterfaceName:    spec.interfaceName,
					FuncName:         n,
					ParamNames:       spec.paramNames,
					ParamValtypes:    spec.paramValtypes,
					CoreImportModule: spec.coreImportModule,
					InnerTypes:       spec.innerTypes,
					ResultValtypes:   spec.resultValtypes,
				})
			} else {
				unsupported = append(unsupported, m+"."+n)
			}
		}
	}
	// Resolve the filesystem open-chain into a single descriptor mode (or
	// the combined read+write). An incomplete chain or an unrepresentable
	// combination (append mixed with read/write — the filesystem/types
	// instance type is single-direction or combined read+write) is
	// unsupported.
	if getDirs || openAt || readVia || writeVia || appendVia {
		switch {
		case getDirs && openAt && readVia && !writeVia && !appendVia:
			req.FileRead = true
		case getDirs && openAt && writeVia && !readVia && !appendVia:
			req.FileWrite = true
		case getDirs && openAt && appendVia && !readVia && !writeVia:
			req.FileAppend = true
		case getDirs && openAt && readVia && writeVia && !appendVia:
			req.FileReadWrite = true
		default:
			unsupported = append(unsupported, "wasi:filesystem (incomplete or unsupported open-chain combination)")
		}
	}
	return req, unsupported
}

// composeRequestEmpty reports whether the request carries no preview-2
// imports at all — an import-free program that routes to the plain
// cli/run (or lifted-export) builder rather than the composer.
func composeRequestEmpty(req component.ComposeRequest) bool {
	return !req.Stdout && !req.Stderr && !req.Stdin &&
		!req.BlockWrite && !req.BlockRead && !req.DropInput && !req.DropOutput &&
		!req.FileRead && !req.FileWrite && !req.FileAppend && !req.FileReadWrite &&
		!req.Tcp && !req.Udp && !req.Http &&
		!req.WallNow && !req.Args && !req.Env && len(req.Structured) == 0
}

// buildPreview2Component is buildPreview2CliRunComponent generalised
// over the lift tail: exportName == "" produces the wasi:cli/run
// shape; a non-empty exportName lifts the run func as a u32-returning
// component func exported under that name (the non-cli
// `-component-wrap` shape, callable via `--invoke <name>()`). Both
// share the composer for every recognised import shape and fall back
// to the matching import-free builder.
func buildPreview2Component(prog *ast.Program, info *checker.Info, bin []byte, exportName string) ([]byte, error) {
	req, unsupported := classifyComposeRequest(bin)
	if len(unsupported) > 0 {
		return nil, fmt.Errorf("can't wrap a core module with unrecognised imports yet (saw %d): %s. Remove the source that pulls them in.", len(unsupported), strings.Join(unsupported, ", "))
	}
	if composeRequestEmpty(req) {
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
		})
		if err != nil {
			return nil, err
		}
		b = rb
	}
	return component.Compose(b, req, "_lang_run"), nil
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
