package e2eharness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
		h := sha256.New()
		fmt.Fprintf(h, "stage0=%s\x00stdlib=%s\x00", fileSHA256(t, compiler), stdlibHash(t))
		driverCompilerKeyVal = hex.EncodeToString(h.Sum(nil))
	})
	return driverCompilerKeyVal
}

// driverBuildWeightMB is the RAM reservation for a driver build: the
// self-host compiler peaks at 4.0 GB compiling asm_ir_run and 5.7 GB
// compiling fern.fern, the whole compiler (docs/LOCAL-DEV-LOOP.md).
func driverBuildWeightMB(fernName string) int {
	if fernName == "fern.fern" {
		return 6000
	}
	return 4300
}

// compileWithSelfHost runs `compiler -target x86-64-linux -o binPath src
// <stdlib>` under a RAM reservation of weightMB.
func compileWithSelfHost(t testing.TB, compiler, src, binPath string, weightMB int) error {
	t.Helper()
	stdlib := SelfHostStdlibRoot(t)
	return withBuildMemory(weightMB, func() error {
		cmd := exec.Command(compiler, "-target", "x86-64-linux", "-o", binPath, src, stdlib)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s -target x86-64-linux %s: %v\n%s", filepath.Base(compiler), filepath.Base(src), err, out)
		}
		return os.Chmod(binPath, 0o755)
	})
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
		root := SelfHostStdlibRoot(t)
		var paths []string
		stdlibHashErr = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !fi.IsDir() {
				paths = append(paths, path)
			}
			return nil
		})
		if stdlibHashErr != nil {
			return
		}
		sort.Strings(paths)
		h := sha256.New()
		for _, p := range paths {
			src, err := os.ReadFile(p)
			if err != nil {
				stdlibHashErr = err
				return
			}
			rel, _ := filepath.Rel(root, p)
			fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(src))
			h.Write(src)
		}
		stdlibHashVal = hex.EncodeToString(h.Sum(nil))
	})
	if stdlibHashErr != nil {
		t.Fatalf("hash internal/stdlib: %v", stdlibHashErr)
	}
	return stdlibHashVal
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
