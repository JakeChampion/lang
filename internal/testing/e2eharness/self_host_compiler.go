package e2eharness

import (
	"bytes"
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

func SelfHostStdlibRoot(t testing.TB) string {
	t.Helper()
	return RepoPath("internal", "stdlib")
}

func Stage0Compiler(t testing.TB) string {
	t.Helper()
	stage0Once.Do(func() {
		cmd := exec.Command(RepoPath("bootstrap", "bootstrap.sh"), "stage0")
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

func DriverBuildWeightMB(fernName string) int {
	if fernName == "fern.fern" || fernName == "drivers/runtime_run.fern" {
		return 6000
	}
	return 4300
}

const (
	TargetX86_64Linux = "x86-64-linux"
	TargetArm64Linux  = "arm64-linux"
	TargetArm64Darwin = "arm64-darwin"
	TargetWasm32Wasi  = "wasm32-wasi"
)

func CompileWithSelfHost(t testing.TB, compiler, target, src, binPath string, weightMB int) error {
	t.Helper()
	stdlib := SelfHostStdlibRoot(t)
	return withBuildMemory(weightMB, func() error {
		args := []string{"-target", target}
		if target == TargetWasm32Wasi {
			args = append(args, "-emit", "core-module")
		}
		cmd := exec.Command(compiler, append(args, "-o", binPath, src, stdlib)...)
		cmd.Env = strippedEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s -target %s %s: %v\n%s", filepath.Base(compiler), target, filepath.Base(src), err, out)
		}
		return os.Chmod(binPath, 0o755)
	})
}

func EmitAsmWithSelfHost(t testing.TB, compiler, target, src string) string {
	t.Helper()
	stdlib := SelfHostStdlibRoot(t)
	asmPath := filepath.Join(t.TempDir(), "out.s")
	err := withBuildMemory(DriverBuildWeightMB(filepath.Base(src)), func() error {
		cmd := exec.Command(compiler, "-target", target, "-emit", "asm", "-o", asmPath, src, stdlib)
		cmd.Env = SelfHostChildEnv()
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

func RunWasmCore(t testing.TB, corePath string, args ...string) *exec.Cmd {
	t.Helper()
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	return exec.Command(wasmtime, append([]string{"run", corePath}, args...)...)
}

func CompileSelfHostSource(t testing.TB, target, src string, env []string) string {
	t.Helper()
	srcPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	return CompileSelfHostFile(t, target, srcPath, env)
}

func CompileSelfHostFile(t testing.TB, target, entry string, env []string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "prog")
	args := []string{"-target", target}
	if target == TargetWasm32Wasi {
		out += ".wasm"
		args = append(args, "-emit", "core-module")
	}
	cmd := exec.Command(SelfHostCLI(t), append(args, "-o", out, entry, SelfHostStdlibRoot(t))...)
	cmd.Env = SelfHostChildEnv(env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		src, _ := os.ReadFile(entry)
		t.Fatalf("SELFHOST-COMPILE-FAIL -target %s: %v\n%s\nsrc:\n%s", target, err, msg, src)
	}
	if err := os.Chmod(out, 0o755); err != nil {
		t.Fatal(err)
	}
	return out
}

func SelfHostCompileCmd(t testing.TB, target, entry, out string) *exec.Cmd {
	t.Helper()
	return exec.Command(SelfHostCLI(t), "-target", target, "-o", out, entry, SelfHostStdlibRoot(t))
}

var (
	currentCLIOnce sync.Once
	currentCLIPath string
)

func SelfHostCLI(t testing.TB) string {
	t.Helper()
	host := HostSelfHostTarget()
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

func WarmSelfHostCLI(t testing.TB) {
	t.Helper()
	if HostSelfHostTarget() != "" {
		SelfHostCLI(t)
	}
}

func HostSelfHostTarget() string {
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

var (
	componentCoreOnce sync.Once
	componentCorePath string
)

func SelfHostComponentCore(t testing.TB, srcPath string) []byte {
	t.Helper()
	return selfHostComponentCore(t, srcPath)
}

func SelfHostReactorCore(t testing.TB, srcPath string) []byte {
	t.Helper()
	return selfHostComponentCore(t, srcPath, "-reactor")
}

func selfHostComponentCore(t testing.TB, srcPath string, args ...string) []byte {
	t.Helper()
	host := HostSelfHostTarget()
	if host == "" {
		t.Skipf("no self-host target runs natively on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	componentCoreOnce.Do(func() {
		dir := WriteSelfHostAsmProject(t)
		CopySelfHostDriver(t, dir, "drivers/wasm_runio_run.fern")
		componentCorePath = CachedDriverBinFor(t, dir, "drivers/wasm_runio_run.fern", host)
	})
	if componentCorePath == "" {
		t.Fatal("the self-host component-core driver failed to build; the first test to need it has the error")
	}
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	emit := exec.Command(componentCorePath, args...)
	emit.Stdin = bytes.NewReader(src)
	var stderr bytes.Buffer
	emit.Stderr = &stderr
	wat, err := emit.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("self-host component core for %s: %v\n%s", srcPath, err, stderr.Bytes())
	}
	corePath := filepath.Join(t.TempDir(), "core.wasm")
	parse := exec.Command(wasmtools, "parse", "-", "-o", corePath)
	parse.Stdin = bytes.NewReader(wat)
	if out, err := parse.CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse of the self-host component core: %v\n%s", err, out)
	}
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatal(err)
	}
	return core
}
