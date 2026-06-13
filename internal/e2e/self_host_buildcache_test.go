package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// Content-addressed build caches shared across every TestSelfHost* in
// one `go test` process (i.e. within a CI shard). Dozens of self-host
// tests compile the *same* ~35k-line self-host driver through the Go
// x86-64 backend and then gcc-link the result; without caching that
// full compile + link is repeated per test and is the suite's dominant
// cost. Both caches key on the exact inputs (source-set hash → asm,
// asm-hash → linked binary), so a hit returns exactly what a fresh
// build would have produced — this only deduplicates identical work
// and can never return a stale or wrong artifact. (Determinism tests —
// fixpoint / cross-validation — compare asm produced by running the
// *compiler binary* as a subprocess, not the output of these helpers,
// so the caches don't make any of those assertions tautological.)
//
// Keys deliberately exclude the in-tree stdlib + the compiler itself:
// both are fixed for the lifetime of the process, so they can't vary
// between two cache lookups in the same run.

type cacheEntry[T any] struct {
	once sync.Once
	val  T
	err  error
}

type buildCache[T any] struct {
	mu sync.Mutex
	m  map[string]*cacheEntry[T]
}

func newBuildCache[T any]() *buildCache[T] {
	return &buildCache[T]{m: map[string]*cacheEntry[T]{}}
}

// get returns the cached value for key, computing it once via build on
// first request. Concurrent callers for the same key block on the same
// build; callers for distinct keys never contend on the result fields.
func (c *buildCache[T]) get(key string, build func() (T, error)) (T, error) {
	c.mu.Lock()
	e := c.m[key]
	if e == nil {
		e = &cacheEntry[T]{}
		c.m[key] = e
	}
	c.mu.Unlock()
	e.once.Do(func() { e.val, e.err = build() })
	return e.val, e.err
}

var (
	selfHostAsmCache = newBuildCache[string]()
	selfHostBinCache = newBuildCache[string]()

	linkCacheDir     string
	linkCacheDirErr  error
	linkCacheDirOnce sync.Once
)

// linkCacheBaseDir is a process-lifetime scratch dir holding the cached
// linked binaries. It is intentionally NOT a t.TempDir (those are torn
// down per-test); the cache must outlive any single test.
func linkCacheBaseDir() (string, error) {
	linkCacheDirOnce.Do(func() {
		linkCacheDir, linkCacheDirErr = os.MkdirTemp("", "selfhost-bincache-")
	})
	return linkCacheDir, linkCacheDirErr
}

// hashSelfHostSources hashes the entry name plus every *.fern file under
// dir (the test's project tree), so the key changes iff any source the
// driver could compile changes. Stray files only cost a cache miss.
func hashSelfHostSources(t *testing.T, dir, fernName string) string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".fern" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(files)
	h := sha256.New()
	fmt.Fprintf(h, "x86_64\x00entry=%s\x00", fernName)
	for _, p := range files {
		rel, _ := filepath.Rel(dir, p)
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(src))
		h.Write(src)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// cachedSelfHostAsm compiles dir/fernName through the Go x86-64 backend,
// caching the emitted asm by source-set hash so repeated tests building
// the same driver pay the ~10s compile only once per process.
func cachedSelfHostAsm(t *testing.T, dir, fernName string) string {
	t.Helper()
	key := hashSelfHostSources(t, dir, fernName)
	asm, err := selfHostAsmCache.get(key, func() (string, error) {
		prog, _, err := modload.Load(filepath.Join(dir, fernName))
		if err != nil {
			return "", fmt.Errorf("modload %s: %w", fernName, err)
		}
		if err := constfold.Fold(prog); err != nil {
			return "", fmt.Errorf("constfold %s: %w", fernName, err)
		}
		info, err := checker.Check(prog)
		if err != nil {
			return "", fmt.Errorf("check %s: %w", fernName, err)
		}
		asm, err := x86_64.Emit(prog, info)
		if err != nil {
			return "", fmt.Errorf("emit %s: %w", fernName, err)
		}
		return asm, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return asm
}

// cachedLink links asm into a static binary once per (gcc, asm) and
// returns the path to the shared cached binary. Callers copy it to
// wherever they need it; the cached file must not be mutated.
func cachedLink(t *testing.T, gcc, asm string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(gcc + "\x00" + asm))
	key := hex.EncodeToString(sum[:])
	path, err := selfHostBinCache.get(key, func() (string, error) {
		base, err := linkCacheBaseDir()
		if err != nil {
			return "", fmt.Errorf("link cache dir: %w", err)
		}
		asmPath := filepath.Join(base, key+".s")
		binPath := filepath.Join(base, key)
		if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
			return "", err
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			return "", fmt.Errorf("gcc: %w\n%s", err, out)
		}
		return binPath, nil
	})
	if err != nil {
		t.Fatalf("cached link: %v", err)
	}
	return path
}

// copyExecutable copies src to dst with 0755 perms.
func copyExecutable(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open cached bin %s: %v", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatalf("copy %s: %v", dst, err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("close %s: %v", dst, err)
	}
}
