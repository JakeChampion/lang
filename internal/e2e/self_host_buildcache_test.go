package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
	selfHostBinCache       = newBuildCache[string]()
	selfHostDriverBinCache = newBuildCache[string]()

	linkCacheDir     string
	linkCacheDirErr  error
	linkCacheDirOnce sync.Once
)

// FERN_SELFHOST_BUILD_CACHE is an optional cross-PROCESS cache location: a
// single directory, or a PATH-list (os.PathListSeparator, ':') of directories.
// When set, the asm + linked-binary caches read from and write to it, so CI
// `warm` jobs can pre-compile the self-host drivers once and the sharded test
// jobs consume the artifacts instead of recompiling the ~35k-line compiler per
// shard — the heavy, RAM/disk-hungry work that exhausts a hosted runner mid-
// shard ("received a shutdown signal"). Empty (the default, e.g. local
// `go test`) leaves the in-process caches as the only layer. Content-addressed
// by the same source-set / asm hash as the in-process caches, so a hit is
// byte-identical to a fresh build and a stale dir only costs a miss (recompile).
//
// The list form exists because `actions/cache/restore` only populates the FIRST
// restore into a given path — restoring several caches into one shared dir
// silently drops all but the first. So each warm group is restored into its own
// dir and the shards point FERN_SELFHOST_BUILD_CACHE at the whole list; reads
// scan every dir, writes go to the first.

// diskCacheReadDirs is the ordered list of cache dirs to consult on a lookup.
func diskCacheReadDirs() []string {
	v := os.Getenv("FERN_SELFHOST_BUILD_CACHE")
	if v == "" {
		return nil
	}
	var dirs []string
	for _, d := range filepath.SplitList(v) {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// diskCacheWriteDir is where a freshly-built artifact is published (the first
// configured dir), or "" when no disk cache is set.
func diskCacheWriteDir() string {
	dirs := diskCacheReadDirs()
	if len(dirs) == 0 {
		return ""
	}
	return dirs[0]
}

// linkCacheBaseDir is a process-lifetime scratch dir holding the cached
// linked binaries. It is intentionally NOT a t.TempDir (those are torn
// down per-test); the cache must outlive any single test.
func linkCacheBaseDir() (string, error) {
	linkCacheDirOnce.Do(func() {
		linkCacheDir, linkCacheDirErr = os.MkdirTemp("", "selfhost-bincache-")
	})
	return linkCacheDir, linkCacheDirErr
}

// fernImportRe matches a local module import — `import "./lexer";` or
// `import "lexer";`. The captured path is resolved to a sibling `.fern` file.
// `std/…` / `core/…` imports resolve to paths that don't exist under the test
// dir and so are naturally excluded (the stdlib + compiler are fixed for the
// run; see the cache-key note above).
var fernImportRe = regexp.MustCompile(`(?m)^\s*import\s+"([^"]+)"`)

// selfHostImportClosure returns the entry file plus the transitive set of local
// `.fern` files it imports, resolved relative to each importing file's dir.
// This is the set whose contents actually determine the emitted asm — stray
// `.fern` files sitting in the project dir but NOT imported by the entry (e.g. a
// sibling `asm_pathprobe_run.fern` present while building `asm_run.fern`) are
// excluded, so the same driver hashes identically regardless of what unrelated
// drivers a test happens to drop alongside it.
func selfHostImportClosure(t *testing.T, dir, fernName string) []string {
	t.Helper()
	seen := map[string]bool{}
	var order []string
	var visit func(path string)
	visit = func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		order = append(order, path)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		base := filepath.Dir(path)
		for _, m := range fernImportRe.FindAllStringSubmatch(string(src), -1) {
			imp := strings.TrimPrefix(m[1], "./")
			cand := filepath.Join(base, imp+".fern")
			if _, statErr := os.Stat(cand); statErr == nil {
				visit(cand)
			}
		}
	}
	visit(filepath.Join(dir, fernName))
	return order
}

// hashSelfHostSources hashes the entry name plus the contents of every `.fern`
// file in the entry's transitive local-import closure, so the key changes iff a
// source the driver actually compiles changes — and is INVARIANT to unrelated
// `.fern` files in the same dir. That invariance is what lets two tests building
// the same stock driver (e.g. asm_run) share one cache entry even when their
// project dirs differ in which OTHER drivers they also wrote, and lets the CI
// `build` job warm a driver under a key the test shards reproduce exactly.
func hashSelfHostSources(t *testing.T, dir, fernName string) string {
	t.Helper()
	files := selfHostImportClosure(t, dir, fernName)
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

// cachedDriverBin builds (or restores) the LINKED self-host driver binary for
// dir/fernName, keyed by the source-closure hash, and returns the path to the
// shared cached binary (callers copyExecutable it where they need it).
//
// Unlike the old cachedSelfHostAsm+cachedLink pair it persists ONLY the
// ~190 MB linked binary to the disk cache — NEVER the ~680 MB emitted `.s`.
// That `.s` was cached only to skip the ~47 s Go x86-64 emit and to derive the
// binary's content key, but binary-only consumers (every self-host driver
// build — they just run the driver) never need the asm text. Restoring it was
// the bug: a warmed driver dragged ~826 MB onto the runner (679 MB of it dead-
// weight .s), and restoring all the warmed drivers exhausted the runner's disk,
// so restores silently dropped and the test re-emitted the driver COLD (~47 s
// — the ~50 s tail the heavy-split was chasing). Caching just the binary keeps
// each warmed driver ~4x smaller, so the restores fit and the emit is skipped.
//
// The binary is a static -nostdlib -no-pie ELF, independent of which gcc
// produced it, so the source-hash key is sound across runners (matching the
// rationale on cachedLink's disk key). The emit is held in memory and the
// scratch `.s` lives only in the process-local link dir until the link
// completes, then is removed — it never reaches the disk cache.
func cachedDriverBin(t *testing.T, gcc, dir, fernName string) string {
	t.Helper()
	key := hashSelfHostSources(t, dir, fernName)
	path, err := selfHostDriverBinCache.get(key, func() (string, error) {
		base, err := linkCacheBaseDir()
		if err != nil {
			return "", fmt.Errorf("link cache dir: %w", err)
		}
		binPath := filepath.Join(base, "drv-"+key)
		// Cross-process disk hit: a driver binary a warm job pre-linked. Scan
		// every configured dir; copy it in and skip both the emit and the link.
		for _, d := range diskCacheReadDirs() {
			if in, oerr := os.ReadFile(filepath.Join(d, key+".driverbin")); oerr == nil {
				if werr := os.WriteFile(binPath, in, 0o755); werr == nil {
					return binPath, nil
				}
			}
		}
		// Cold: emit (held in memory — no disk `.s`), link, publish the binary.
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
		asmPath := binPath + ".s" // scratch in the process-local link dir only
		if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
			return "", err
		}
		if out, lerr := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); lerr != nil {
			return "", fmt.Errorf("gcc %s: %w\n%s", fernName, lerr, out)
		}
		_ = os.Remove(asmPath) // never keep the ~680 MB .s around
		// Publish the linked binary to the disk cache (atomic), so a warm job
		// seeds it for the test shards.
		if d := diskCacheWriteDir(); d != "" {
			dst := filepath.Join(d, key+".driverbin")
			_ = os.MkdirAll(filepath.Dir(dst), 0o755)
			if in, rerr := os.ReadFile(binPath); rerr == nil {
				tmp := dst + ".tmp"
				if werr := os.WriteFile(tmp, in, 0o755); werr == nil {
					_ = os.Rename(tmp, dst) // atomic publish; safe under parallel shards
				}
			}
		}
		return binPath, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// cachedLink links asm into a static binary once per (gcc, asm) and
// returns the path to the shared cached binary. Callers copy it to
// wherever they need it; the cached file must not be mutated.
func cachedLink(t *testing.T, gcc, asm string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(gcc + "\x00" + asm))
	key := hex.EncodeToString(sum[:])
	// The cross-PROCESS disk .bin is keyed by the asm CONTENT alone (not gcc),
	// so a binary a `warm` job linked is reused by a test shard even though the
	// two runners may resolve different gcc paths (x86_64-linux-gnu-gcc vs gcc)
	// and thus different in-process keys. The asm is byte-identical when the .s
	// cache hits, and the output is a static -nostdlib -no-pie ELF — independent
	// of which gcc produced it — so this is sound. Without it the .bin silently
	// missed across runners and the (minute-long) link of the big drivers was
	// repeated per shard, keeping shards in the preemption window.
	asmSum := sha256.Sum256([]byte(asm))
	diskKey := hex.EncodeToString(asmSum[:])
	path, err := selfHostBinCache.get(key, func() (string, error) {
		base, err := linkCacheBaseDir()
		if err != nil {
			return "", fmt.Errorf("link cache dir: %w", err)
		}
		binPath := filepath.Join(base, key)
		// Cross-process disk hit: a pre-linked binary from a warm job — copy it
		// into this process's link dir and skip gcc. Scan every configured dir.
		for _, d := range diskCacheReadDirs() {
			diskBin := filepath.Join(d, diskKey+".bin")
			if in, oerr := os.ReadFile(diskBin); oerr == nil {
				if werr := os.WriteFile(binPath, in, 0o755); werr == nil {
					return binPath, nil
				}
			}
		}
		diskBin := ""
		if d := diskCacheWriteDir(); d != "" {
			diskBin = filepath.Join(d, diskKey+".bin")
		}
		asmPath := filepath.Join(base, key+".s")
		if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
			return "", err
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			return "", fmt.Errorf("gcc: %w\n%s", err, out)
		}
		if diskBin != "" {
			_ = os.MkdirAll(filepath.Dir(diskBin), 0o755)
			if in, rerr := os.ReadFile(binPath); rerr == nil {
				tmp := diskBin + ".tmp"
				if werr := os.WriteFile(tmp, in, 0o755); werr == nil {
					_ = os.Rename(tmp, diskBin)
				}
			}
		}
		return binPath, nil
	})
	if err != nil {
		t.Fatalf("cached link: %v", err)
	}
	return path
}

// copyExecutable links (preferably) or copies src to dst with 0755 perms.
// The cached self-host driver binaries are large (~180-250 MB) and dozens of
// tests per shard each materialise one into their t.TempDir — a plain copy is
// ~2s of IO apiece, which accumulates into minutes and (stacked on the heavy
// run-tests) pushes a shard into the runner-preemption window. The cached
// binary is read-only and only ever exec'd, so a HARDLINK is equivalent and
// effectively free; we fall back to a copy when the link fails (e.g. src/dst on
// different filesystems). t.TempDir teardown just drops the extra link.
func copyExecutable(t *testing.T, src, dst string) {
	t.Helper()
	_ = os.Remove(dst) // os.Link fails if dst exists
	if err := os.Link(src, dst); err == nil {
		return
	}
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
