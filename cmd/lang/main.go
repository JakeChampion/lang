// Command lang compiles a single .lang source file.
//
// Usage:
//
//	lang FILE.lang                       # write arm64 Linux assembly to stdout
//	lang -o OUTPUT FILE.lang             # link with the aarch64 cross-
//	                                     # compiler and write a static ELF
//	                                     # binary
//	lang --run FILE.lang [-- ARGS...]    # link to a temporary binary and
//	                                     # execute it under qemu-aarch64
//	                                     # (forwarding stdio)
//	lang -fmt FILE.lang                  # write idiomatic, indented source
//	                                     # to stdout (use -w to overwrite
//	                                     # the input file in place; use -d
//	                                     # to print a unified diff against
//	                                     # the on-disk version and exit
//	                                     # non-zero when they differ)
//	lang -check FILE.lang                # type-check the codebase rooted
//	                                     # at FILE.lang (follows imports);
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
	x86_64codegen "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/modload"
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
	target := flag.String("target", "arm64", "code-generation backend: arm64 (default, Linux ELF), arm64-darwin (native Apple Silicon macOS), x86-64 (Linux ELF, in-progress; PR 1 supports `return N` only), wasm (CLI component), or wasi-http (HTTP handler component implementing wasi:http/incoming-handler)")
	cc := flag.String("cc", "", "linker invoked when -o or --run is set; defaults to aarch64-linux-gnu-gcc for arm64 Linux, clang for arm64-darwin, x86_64-linux-gnu-gcc for x86-64")
	runIt := flag.Bool("run", false, "link to a temporary binary and execute it (arm64 Linux only; uses qemu-aarch64 when not on an arm64 host)")
	qemu := flag.String("qemu", "qemu-aarch64", "user-mode emulator used by --run")
	repl := flag.Bool("repl", false, "start an interactive REPL via the AST interpreter")
	doInterp := flag.Bool("interp", false, "run FILE.lang (or `-` for stdin) through the AST interpreter — no codegen, no link, no binary. main()'s return value becomes the process exit code (clamped to 0..255). State is fresh per invocation; the REPL flag keeps an interactive session across lines.")
	wasiAdapter := flag.String("wasi-adapter", "", "path to the wasi_snapshot_preview1.command.wasm adapter (required for -target wasm; see docs/WASI-PREVIEW2.md)")
	componentWrap := flag.Bool("component-wrap", false, "with -target wasm-bin and no -wasi-adapter: wrap the core module as a self-contained preview-2 component via internal/wasm/component (no wasm-tools shell-out). Lifts main() as a component-level u32-returning function. Only valid for Lang programs with no WASI imports.")
	componentWrapCli := flag.Bool("component-wrap-cli", false, "like -component-wrap but emits the wasi:cli/run@0.2.0 export shape so the produced component runs under plain `wasmtime run prog.wasm` (no --invoke). main()'s return value lowers to result<_, _>: 0 = ok, non-zero = err. Currently only supported for programs with no WASI imports.")
	doFmt := flag.Bool("fmt", false, "format the source file and write to stdout (use -w to write back in place, -d to print a diff)")
	writeBack := flag.Bool("w", false, "with -fmt, overwrite the input file with the formatted output")
	diffMode := flag.Bool("d", false, "with -fmt, print a unified diff between the file and its formatted form; exits 1 when they differ")
	doCheck := flag.Bool("check", false, "type-check FILE.lang (or `-` for stdin) and its transitive imports. No codegen, no link, no binary. Silent on success; prints formatted diagnostics and exits 1 on the first error.")
	listTargets := flag.Bool("targets", false, "list the supported -target= values with their descriptions + capability surface, then exit. Surfaces the Platform-descriptor table (internal/platforms) as the canonical source of truth for what each target accepts.")
	explain := flag.String("explain", "", "print the long-form explanation for an error code (e.g. -explain E001) and exit. Pass an empty string with no other args to list the available codes.")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: lang [-target arm64|arm64-darwin|x86-64|wasm] [-o OUTPUT] [--run] [-cc CC] [-qemu QEMU] FILE.lang [-- ARGS...]")
		fmt.Fprintln(os.Stderr, "       lang -fmt [-w | -d] FILE.lang")
		fmt.Fprintln(os.Stderr, "       lang -check FILE.lang | lang -check -      (type-check only; stdin form)")
		fmt.Fprintln(os.Stderr, "       lang -repl")
		fmt.Fprintln(os.Stderr, "       lang -interp FILE.lang | lang -interp -    (read from stdin)")
		fmt.Fprintln(os.Stderr, "       lang -targets                                (list supported targets + capabilities)")
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
			// can do `lang -interp test.lang -- --filter foo`
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
	// user's own arguments, passed after `--` on the lang
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
// the lang process itself should exit with: 0 in compile-only mode, or
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

	if target == "wasm-bin" {
		// Binary backend (internal/codegen/wasmbin) — produces
		// wasm core module bytes directly, no `wasm-tools parse`
		// shell-out.
		//
		// Output mode:
		//   - no `-wasi-adapter`: write the raw core module to
		//     outPath. Runnable via `wasmtime run --invoke <fn>`.
		//   - `-wasi-adapter PATH`: wrap in a preview-2 component
		//     matching the `lang` world, composing with the
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
				// Wrap the core module as a preview-2 component via
				// the Go-side encoder — no wasm-tools shell-out, no
				// adapter. Lifts `main` as a component-level u32-
				// returning function.
				//
				// Three branches:
				//
				//   - No imports → BuildLiftedExportComponent (simplest).
				//   - Only preview-2 imports we know how to route →
				//     WrapWasiImportedWithExport.
				//   - Anything else (preview-1 imports we haven't
				//     migrated yet) → error pointing at -wasi-adapter.
				wasiImports, unknown := classifyPreview2Imports(bin)
				if len(unknown) > 0 {
					return 1, fmt.Errorf("-component-wrap can't wrap a core module with unrecognised imports yet (saw %d): %s. Either remove the source that pulls them in or use -wasi-adapter PATH to wrap through wasm-tools.", len(unknown), strings.Join(unknown, ", "))
				}
				// `_lang_run` is the wasmbin-synthesised wrapper
				// (SynthCliRun above) that normalises main's
				// signature to `() -> i32` regardless of what the
				// user declared. The component-level export name
				// stays as "main" so `wasmtime run --invoke main()`
				// keeps working.
				var comp []byte
				if len(wasiImports) == 0 {
					comp = component.BuildLiftedExportComponent(bin, "_lang_run", "main", nil, nil, component.CValtypeU32)
				} else {
					comp = component.WrapWasiImportedWithExport(bin, wasiImports, "_lang_run", "main", nil, nil, component.CValtypeU32)
				}
				if err := os.WriteFile(outPath, comp, 0o644); err != nil {
					return 1, err
				}
				return 0, nil
			}
			if componentWrapCli {
				// Wrap as a wasi:cli/run-exporting component so the
				// produced binary runs under plain `wasmtime run` (no
				// --invoke). Three branches:
				//
				//   - No imports → BuildWasiCliRunComponent.
				//   - Only known preview-2 imports →
				//     WrapWasiImportedAsCliRun.
				//   - Anything else → error pointing at
				//     -component-wrap / -wasi-adapter.
				//
				// The cli-run lift consumes a `() -> i32` core
				// export. wasmbin's SynthCliRun has already emitted
				// `_lang_run` as the normalised entry — main with
				// any signature (void / i32) flows through it.
				wasiImports, unknown := classifyPreview2Imports(bin)
				if len(unknown) > 0 {
					return 1, fmt.Errorf("-component-wrap-cli can't wrap a core module with unrecognised imports yet (saw %d): %s. Either remove the source that pulls them in or use -component-wrap / -wasi-adapter for now.", len(unknown), strings.Join(unknown, ", "))
				}
				var comp []byte
				if len(wasiImports) == 0 {
					comp = component.BuildWasiCliRunComponent(bin, "_lang_run")
				} else {
					comp = component.WrapWasiImportedAsCliRun(bin, wasiImports, "_lang_run")
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
		if err := emitPreview2ComponentFromCoreBytes(bin, outPath, wasiAdapter, "lang"); err != nil {
			return 1, err
		}
		return 0, nil
	}

	if target == "wasm" || target == "wasi-http" {
		// Both CLI wasm targets route through wasmbin. WAT is
		// out of the user-facing wasm story; what's left of
		// internal/codegen/wasm/ is dev tooling (cmd/lang-wasm,
		// cmd/dump_wat) waiting on a downstream cleanup PR.
		if outPath == "" {
			return 1, fmt.Errorf("-target %s requires -o OUTPUT (the component is a binary)", target)
		}
		if wasiAdapter == "" {
			return 1, fmt.Errorf("-target %s requires -wasi-adapter PATH (see docs/WASI-PREVIEW2.md)", target)
		}
		opts := wasmbin.BuildOptions{
			ForceMemorySection: true,
			SynthStart:         true,
		}
		world := "lang"
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
		f, err := os.CreateTemp("", "lang-bin-*")
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
// Linux dev hosts (cross-compiling) and Macs natively as long
// as clang + lld are installed. The output is a full Mach-O
// executable that runs on Apple Silicon Macs without further
// processing.
func linkDarwin(asm, outPath, cc string) error {
	if _, err := exec.LookPath(cc); err != nil {
		return fmt.Errorf("compiler %q not found on PATH (override with -cc): %w", cc, err)
	}
	tmpDir, err := os.MkdirTemp("", "lang-build-*")
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
	tmpDir, err := os.MkdirTemp("", "lang-build-*")
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
	tmpDir, err := os.MkdirTemp("", "lang-component-*")
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
// code (so the caller can mirror it as the lang process exit code).
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
// driver knows how to route through `WrapWasiImportedWithExport`.
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

// classifyPreview2Imports walks the core module's import section
// and bucketises each import:
//
//   - wasi: returned as a `component.WasiImport` ready to feed
//     `WrapWasiImportedWithExport`.
//   - unknown: the "module.name" string returned so the driver
//     can surface a useful error pointing at -wasi-adapter.
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
