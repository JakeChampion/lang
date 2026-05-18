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
//
// The -cc and -qemu flags override the linker and emulator.
// Note: the formatter strips `//` line comments and blank lines
// because the lexer drops both before they reach the AST.
package main

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/wasm"
	x86_64codegen "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/printer"
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
	doFmt := flag.Bool("fmt", false, "format the source file and write to stdout (use -w to write back in place, -d to print a diff)")
	writeBack := flag.Bool("w", false, "with -fmt, overwrite the input file with the formatted output")
	diffMode := flag.Bool("d", false, "with -fmt, print a unified diff between the file and its formatted form; exits 1 when they differ")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: lang [-target arm64|arm64-darwin|x86-64|wasm] [-o OUTPUT] [--run] [-cc CC] [-qemu QEMU] FILE.lang [-- ARGS...]")
		fmt.Fprintln(os.Stderr, "       lang -fmt [-w | -d] FILE.lang")
		fmt.Fprintln(os.Stderr, "       lang -repl")
		fmt.Fprintln(os.Stderr, "       lang -interp FILE.lang | lang -interp -    (read from stdin)")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *repl {
		if err := interp.REPL(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if *doInterp {
		path := ""
		if flag.NArg() >= 1 {
			path = flag.Arg(0)
		} else {
			path = "-"
		}
		code, err := runInterp(path)
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

	code, err := run(srcPath, *out, *target, *cc, *runIt, *qemu, *wasiAdapter, progArgs)
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
func runInterp(srcPath string) (int, error) {
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

// run drives the full pipeline. The returned int is the exit code that
// the lang process itself should exit with: 0 in compile-only mode, or
// the program's own exit code under --run.
func run(srcPath, outPath, target, cc string, runIt bool, qemu string, wasiAdapter string, progArgs []string) (int, error) {
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

	if target == "wasm" || target == "wasi-http" {
		opts := wasm.EmitOptions{}
		world := "lang"
		if target == "wasi-http" {
			opts.HttpHandler = true
			world = "http"
		}
		text, err := wasm.EmitWithOptions(prog, info, opts)
		if err != nil {
			return 1, err
		}
		if outPath == "" {
			return 1, fmt.Errorf("-target %s requires -o OUTPUT (the component is a binary)", target)
		}
		if wasiAdapter == "" {
			return 1, fmt.Errorf("-target %s requires -wasi-adapter PATH (see docs/WASI-PREVIEW2.md)", target)
		}
		if err := emitPreview2ComponentWorld(text, outPath, wasiAdapter, world); err != nil {
			return 1, err
		}
		return 0, nil
	}

	if target != "arm64" && target != "arm64-darwin" && target != "x86-64" {
		return 1, fmt.Errorf("unknown target %q (want arm64-darwin, arm64, x86-64, wasm, or wasi-http)", target)
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

// witFS bundles the WIT package(s) the wasm backend's preview-2
// imports refer to. Embedding lets `lang` ship as a single binary —
// `wasm-tools component embed` reads them from a temp directory we
// extract at preview-2 emission time. Add new WIT under
// cmd/lang/wit/ and they'll be picked up automatically.
//
//go:embed wit
var witFS embed.FS

// emitPreview2ComponentWorld wraps the WAT in a Component Model
// component matching the named WIT world (currently `lang` for the
// CLI target or `http` for the HTTP-handler target). Pipeline:
//  1. write WAT to a temp file;
//  2. `wasm-tools parse` lowers it to a binary core module;
//  3. `wasm-tools component embed` annotates the module with the
//     `local:lang/<world>` WIT world so the component-new step
//     knows how to lift the native preview-2 imports we emit and,
//     for the http world, where the exported
//     `wasi:http/incoming-handler.handle` lives;
//  4. `wasm-tools component new --adapt wasi_snapshot_preview1=ADAPTER`
//     composes the module with the adapter; preview-1 imports
//     (args/env/proc_exit) get translated to preview-2 by the
//     adapter, native preview-2 imports flow through unchanged.
//     The result satisfies any preview-2 host (`wasmtime run`,
//     `wasmtime serve`, edge-function runtimes, etc.).
func emitPreview2ComponentWorld(wat, outPath, adapterPath, world string) error {
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

	witDir := filepath.Join(tmpDir, "wit")
	if err := extractWIT(witDir); err != nil {
		return fmt.Errorf("extract embedded WIT: %w", err)
	}

	watPath := filepath.Join(tmpDir, "prog.wat")
	if err := os.WriteFile(watPath, []byte(wat), 0o644); err != nil {
		return err
	}
	modulePath := filepath.Join(tmpDir, "prog.wasm")
	if out, err := exec.Command("wasm-tools", "parse", watPath, "-o", modulePath).CombinedOutput(); err != nil {
		return fmt.Errorf("wasm-tools parse failed: %w\n%s", err, out)
	}
	embeddedPath := filepath.Join(tmpDir, "prog.embedded.wasm")
	if out, err := exec.Command("wasm-tools", "component", "embed",
		witDir, "-w", world,
		modulePath, "-o", embeddedPath).CombinedOutput(); err != nil {
		return fmt.Errorf("wasm-tools component embed failed: %w\n%s", err, out)
	}
	if out, err := exec.Command("wasm-tools", "component", "new",
		"--adapt", "wasi_snapshot_preview1="+adapterPath,
		embeddedPath, "-o", outPath).CombinedOutput(); err != nil {
		return fmt.Errorf("wasm-tools component new failed: %w\n%s", err, out)
	}
	return nil
}

// extractWIT walks the embedded `wit/` tree and writes it under
// dstRoot, preserving the relative directory structure. Used to
// hand a real on-disk path to `wasm-tools component embed`, which
// resolves WIT imports through the filesystem.
func extractWIT(dstRoot string) error {
	return fs.WalkDir(witFS, "wit", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("wit", p)
		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := witFS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
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
