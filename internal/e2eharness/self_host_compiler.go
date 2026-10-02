package e2eharness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The self-host suites run Fern DRIVERS — fern.fern, asm_ir_run.fern,
// wasm_ir_run.fern and some fifty smaller ones — as native x86-64 binaries.
// Until the native backends were frozen those binaries came from the Go x86-64
// backend. They now come from the pinned stage0 compiler (bootstrap/stage0.lock),
// resolved and verified by `bootstrap/bootstrap.sh stage0` — the same compiler
// `make bootstrap` starts from, and the only compiler a checkout with no Go and
// no native backend has (docs/BOOTSTRAP.md). A driver is thus the tree's
// current source compiled by an earlier, trusted compiler, exactly as stage1
// is; what the drivers exercise is the current source's behaviour, and the
// current compiler's own output is what the fixture and differential lanes
// run. The cost of the choice is that every driver is held to what the pin
// can compile (the Go 1.4 rule the `verify` lanes enforce on fern.fern), so a
// driver using a construct newer than the pin fails here the way fern.fern
// fails under `make bootstrap`, and the answer is the same: refresh the pin.

var (
	stage0Once sync.Once
	stage0Path string
	stage0Err  error

	driverCompilerKeyOnce sync.Once
	driverCompilerKeyVal  string

	stdlibHashOnce sync.Once
	stdlibHashVal  string
	stdlibHashErr  error
)

// repoRootDir is the checkout root, from a test package two levels below it
// (the same assumption selfHostSrcDir makes).
const repoRootDir = "../.."

// SelfHostStdlibRoot is the stdlib root a self-host compiler invocation takes
// as its trailing argument.
func SelfHostStdlibRoot(t testing.TB) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join(repoRootDir, "internal", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib root: %v", err)
	}
	return p
}

// Stage0Compiler returns the pinned stage0 compiler for this host, downloaded
// and sha256-verified by bootstrap/bootstrap.sh on first use. STAGE0=<path>
// in the environment names a local compiler instead, as it does for
// `make bootstrap`.
func Stage0Compiler(t testing.TB) string {
	t.Helper()
	stage0Once.Do(func() {
		script, err := filepath.Abs(filepath.Join(repoRootDir, "bootstrap", "bootstrap.sh"))
		if err != nil {
			stage0Err = err
			return
		}
		cmd := exec.Command(script, "stage0")
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			stage0Err = fmt.Errorf("bootstrap.sh stage0: %v\n%s", err, stderr.String())
			return
		}
		stage0Path = strings.TrimSpace(string(out))
		if stage0Path == "" {
			stage0Err = fmt.Errorf("bootstrap.sh stage0 printed no path\n%s", stderr.String())
		}
	})
	if stage0Err != nil {
		t.Fatalf("resolve stage0: %v", stage0Err)
	}
	return stage0Path
}

// driverCompilerKey names, for a driver's cache key, everything about the
// build other than the driver's own sources: the compiler's bytes and the
// stdlib tree it compiles in. A pin refresh or a stdlib edit rebuilds every
// driver; nothing else does.
func driverCompilerKey(t testing.TB) string {
	t.Helper()
	compiler := Stage0Compiler(t)
	driverCompilerKeyOnce.Do(func() {
		driverCompilerKeyVal = driverCompilerKeyFor(fileSHA256(t, compiler), stdlibHash(t))
	})
	return driverCompilerKeyVal
}

func driverCompilerKeyFor(compilerSHA256, stdlibHash string) string {
	h := sha256.New()
	fmt.Fprintf(h, "stage0=%s\x00stdlib=%s\x00", compilerSHA256, stdlibHash)
	return hex.EncodeToString(h.Sum(nil))
}

// DriverBuildWeightMB is the RAM reservation for a driver build: the
// self-host compiler peaks at 4.0 GB compiling asm_ir_run and 5.7 GB
// compiling fern.fern, the whole compiler (docs/LOCAL-DEV-LOOP.md).
func DriverBuildWeightMB(fernName string) int {
	if fernName == "fern.fern" {
		return 6000
	}
	return 4300
}

// The -target names a self-host build takes. The pin cross-compiles every
// one of them from any host: a runnable static ELF for the two Linux targets,
// and for arm64-darwin a Mach-O, or with -emit asm the assembly text.
const (
	TargetX86_64Linux = "x86-64-linux"
	TargetArm64Linux  = "arm64-linux"
	TargetArm64Darwin = "arm64-darwin"
	// TargetWasm32Wasi builds the raw WASI core module (`-emit core-module`),
	// which `wasmtime run` executes with main's result as the exit code; the
	// default output form for the target is a cli/run component, whose
	// result is only ok or err.
	TargetWasm32Wasi = "wasm32-wasi"
)

// CompileWithSelfHost runs `compiler -target target -o binPath src <stdlib>`
// under a RAM reservation of weightMB.
func CompileWithSelfHost(t testing.TB, compiler, target, src, binPath string, weightMB int) error {
	t.Helper()
	stdlib := SelfHostStdlibRoot(t)
	return withBuildMemory(weightMB, func() error {
		args := []string{"-target", target}
		if target == TargetWasm32Wasi {
			args = append(args, "-emit", "core-module")
		}
		cmd := exec.Command(compiler, append(args, "-o", binPath, src, stdlib)...)
		// The driver cache keys a build by its sources and compiler alone, so
		// no FERN_* knob a test sets for the driver's run may reach its build.
		cmd.Env = ChildEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s -target %s %s: %v\n%s", filepath.Base(compiler), target, filepath.Base(src), err, out)
		}
		return os.Chmod(binPath, 0o755)
	})
}

// EmitAsmWithSelfHost runs `compiler -target target -emit asm` on src and
// returns the assembly text. It reserves what a driver build of that name
// takes: the compiler's own footprint dominates, and over-reserving only
// delays another build.
func EmitAsmWithSelfHost(t testing.TB, compiler, target, src string) string {
	t.Helper()
	stdlib := SelfHostStdlibRoot(t)
	asmPath := filepath.Join(t.TempDir(), "out.s")
	err := withBuildMemory(DriverBuildWeightMB(filepath.Base(src)), func() error {
		cmd := exec.Command(compiler, "-target", target, "-emit", "asm", "-o", asmPath, src, stdlib)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s -target %s -emit asm %s: %v\n%s", filepath.Base(compiler), target, filepath.Base(src), err, out)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	asm, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(asm)
}

// cachedBinary returns a binary from the process cache dir, restoring it from
// the disk cache (diskName) when another process on this host built it, and
// otherwise building it with build and publishing it there.
func cachedBinary(t testing.TB, name, diskName string, build func(binPath string) error) (string, error) {
	t.Helper()
	base, err := linkCacheBaseDir()
	if err != nil {
		return "", fmt.Errorf("link cache dir: %w", err)
	}
	binPath := filepath.Join(base, name)
	for _, d := range diskCacheReadDirs() {
		if in, oerr := os.ReadFile(filepath.Join(d, diskName)); oerr == nil {
			if werr := os.WriteFile(binPath, in, 0o755); werr == nil {
				return binPath, nil
			}
		}
	}
	if err := build(binPath); err != nil {
		return "", err
	}
	publishToDiskCache(binPath, diskName)
	return binPath, nil
}

// publishToDiskCache copies binPath into the disk cache as diskName, atomically,
// so the other worker process of a CI shard finds it. A missing or unwritable
// cache is not an error: the binary is still in the process cache.
func publishToDiskCache(binPath, diskName string) {
	d := diskCacheWriteDir()
	if d == "" {
		return
	}
	dst := filepath.Join(d, diskName)
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	in, err := os.ReadFile(binPath)
	if err != nil {
		return
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, in, 0o755); err == nil {
		_ = os.Rename(tmp, dst)
	}
}

// stdlibHash hashes every file under internal/stdlib, once per process. The
// self-host compiler reads the stdlib from disk (unlike the Go compiler, which
// embeds it), so it is part of every driver's build key — and reading it here
// is also what puts those files in the go test cache's testlog, so a stdlib
// edit invalidates a cached `ok` (docs/TEST-GATES.md, "ok … (cached)").
func stdlibHash(t testing.TB) string {
	t.Helper()
	stdlibHashOnce.Do(func() {
		stdlibHashVal, stdlibHashErr = treeHash(SelfHostStdlibRoot(t))
	})
	if stdlibHashErr != nil {
		t.Fatalf("hash internal/stdlib: %v", stdlibHashErr)
	}
	return stdlibHashVal
}

// treeHash hashes every file under root by relative path and content, so a
// file's edit, rename, addition or removal all change it.
func treeHash(root string) (string, error) {
	var paths []string
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		rel, _ := filepath.Rel(root, p)
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(src))
		h.Write(src)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileSHA256(t testing.TB, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RunWasmCore builds the exec.Cmd that runs a WASI core module under
// wasmtime; the process exit code is main's result. It skips when wasmtime is
// not on PATH.
func RunWasmCore(t testing.TB, corePath string, args ...string) *exec.Cmd {
	t.Helper()
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	return exec.Command(wasmtime, append([]string{"run", corePath}, args...)...)
}

// compileSelfHostProgram writes src to a temp dir and compiles it with the
// current self-host compiler for target, with env added to the compiler's
// environment. It returns the output path.
func compileSelfHostProgram(t testing.TB, target, src string, env []string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog")
	args := []string{"-target", target}
	if target == TargetWasm32Wasi {
		out += ".wasm"
		args = append(args, "-emit", "core-module")
	}
	cmd := exec.Command(SelfHostCLI(t), append(args, "-o", out, srcPath, SelfHostStdlibRoot(t))...)
	cmd.Env = append(os.Environ(), env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("SELFHOST-COMPILE-FAIL -target %s: %v\n%s\nsrc:\n%s", target, err, msg, src)
	}
	if err := os.Chmod(out, 0o755); err != nil {
		t.Fatal(err)
	}
	return out
}

// SelfHostCompileCmd is the current self-host compiler compiling the entry
// file to out for target, with the stdlib root its `std/` imports resolve
// against.
func SelfHostCompileCmd(t testing.TB, target, entry, out string) *exec.Cmd {
	t.Helper()
	return exec.Command(SelfHostCLI(t), "-target", target, "-o", out, entry, SelfHostStdlibRoot(t))
}

var (
	currentCLIOnce sync.Once
	currentCLIPath string
)

// SelfHostCLI is the current self-host compiler (fern.fern) built by
// the pin for the host, shared by every program compile in the process.
func SelfHostCLI(t testing.TB) string {
	t.Helper()
	host := hostSelfHostTarget()
	if host == "" {
		t.Skipf("no self-host target runs natively on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	currentCLIOnce.Do(func() {
		dir := WriteSelfHostAsmProject(t)
		CopySelfHostDriver(t, dir, "fern.fern")
		currentCLIPath = CachedDriverBinFor(t, dir, "fern.fern", host)
	})
	if currentCLIPath == "" {
		t.Fatal("the self-host compiler failed to build; the first test to need it has the error")
	}
	return currentCLIPath
}

// hostSelfHostTarget is the self-host target whose binaries this host runs
// directly, or "" when there is none.
func hostSelfHostTarget() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return TargetX86_64Linux
	case "linux/arm64":
		return TargetArm64Linux
	case "darwin/arm64":
		return TargetArm64Darwin
	}
	return ""
}
