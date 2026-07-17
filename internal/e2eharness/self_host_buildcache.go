// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/self_host_buildcache_test.go.
package e2eharness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

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

var (
	selfHostBinCache       = newBuildCache[string]()
	selfHostDriverBinCache = newBuildCache[string]()

	linkCacheDir     string
	linkCacheDirErr  error
	linkCacheDirOnce sync.Once
)

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

// SelfHostImportClosure returns the entry file plus the transitive set of local
// `.fern` files it imports, resolved relative to each importing file's dir.
// This is the set whose contents actually determine the emitted asm — stray
// `.fern` files sitting in the project dir but NOT imported by the entry (e.g. a
// sibling `asm_pathprobe_run.fern` present while building `asm_run.fern`) are
// excluded, so the same driver hashes identically regardless of what unrelated
// drivers a test happens to drop alongside it.
func SelfHostImportClosure(t *testing.T, dir, fernName string) []string {
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

// HashSelfHostSources hashes the entry name plus the contents of every `.fern`
// file in the entry's transitive local-import closure, so the key changes iff a
// source the driver actually compiles changes — and is INVARIANT to unrelated
// `.fern` files in the same dir. That invariance is what lets two tests building
// the same stock driver (e.g. asm_run) share one cache entry even when their
// project dirs differ in which OTHER drivers they also wrote, and lets the CI
// `build` job warm a driver under a key the test shards reproduce exactly.
func HashSelfHostSources(t *testing.T, dir, fernName string) string {
	t.Helper()
	files := SelfHostImportClosure(t, dir, fernName)
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

// CachedDriverBin builds (or restores) the LINKED self-host driver binary for
// dir/fernName, keyed by the source-closure hash, and returns the path to the
// shared cached binary (callers copyExecutable it where they need it).
//
// Unlike the old cachedSelfHostAsm+CachedLink pair it persists ONLY the
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
// rationale on CachedLink's disk key). The emit is held in memory and the
// scratch `.s` lives only in the process-local link dir until the link
// completes, then is removed — it never reaches the disk cache.
// CopySelfHostFiles copies the named examples/self_host sources into dir —
// the staging step before BuildSelfHostBin for tests that hand-pick a driver's
// import closure instead of copySelfHostTree'ing the whole directory.
func CopySelfHostFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

// BuildSelfHostBin loads a self-host driver .fern (by file name in dir),
// compiles it with the Go x86-64 backend, links it, and returns dir/out.
// BOTH steps are cached (see below): the source→asm compile by source-set
// hash, and the link by asm hash. The link cache matters for the large
// drivers (e.g. asm_ir_run / asm_load_run, which pull in asm_ir) whose gcc
// link alone runs ~minute — without it a CI shard re-links the same driver
// per test. The linked binary is copied to dir/out so callers that exec it
// (or drop sibling files next to it) see a real file in their own dir.
func BuildSelfHostBin(t *testing.T, gcc, dir, fernName, out string) string {
	t.Helper()
	dst := filepath.Join(dir, out)
	copyExecutable(t, CachedDriverBin(t, gcc, dir, fernName), dst)
	return dst
}

func CachedDriverBin(t *testing.T, gcc, dir, fernName string) string {
	t.Helper()
	key := HashSelfHostSources(t, dir, fernName)
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
		// The emit + link is the only multi-GB step (Go emit ~8 GB RSS; `as`
		// on the driver `.s` a few GB more), so reserve its estimated peak
		// against the process-wide RAM budget: two cold driver builds hitting
		// their peaks at once used to cross a 16 GB host's RAM and OOM-kill the
		// run ("signal: killed" / exit 137). The reservation serialises the
		// heavy builds on a RAM-limited host (and parallelises up to the budget
		// on a big one) — see buildMemLimiter. Disk-cache hits above return
		// before this and never reserve.
		asmPath := binPath + ".s" // scratch in the process-local link dir only
		if err := withBuildMemory(heavyBuildWeightMB(), func() error {
			if err := emitDriverAsm(dir, fernName, asmPath); err != nil {
				return err
			}
			// The emit's working set (front-end AST + checker tables + the
			// asm string) is unreachable once emitDriverAsm returns, but the
			// Go runtime keeps the spans resident. Hand them back to the OS
			// before spawning the assembler so the emit residue and the `as`
			// peak never stack within this one build.
			debug.FreeOSMemory()
			if out, lerr := exec.Command(gcc, driverLinkArgs(asmPath, binPath)...).CombinedOutput(); lerr != nil {
				return fmt.Errorf("gcc %s: %w\n%s", fernName, lerr, out)
			}
			return nil
		}); err != nil {
			return "", err
		}
		_ = os.Remove(asmPath) // never keep the big .s around
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

// emitDriverAsm compiles dir/fernName with the Go x86-64 backend and writes
// the emitted asm to asmPath. It exists as a separate function so the emit's
// multi-GB working set (AST, checker info, the asm string) goes out of scope
// on return — CachedDriverBin frees it (debug.FreeOSMemory) before spawning
// the memory-heavy assembler on the `.s`.
func emitDriverAsm(dir, fernName, asmPath string) error {
	prog, _, err := modload.Load(filepath.Join(dir, fernName))
	if err != nil {
		return fmt.Errorf("modload %s: %w", fernName, err)
	}
	if err := constfold.Fold(prog); err != nil {
		return fmt.Errorf("constfold %s: %w", fernName, err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		return fmt.Errorf("check %s: %w", fernName, err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		return fmt.Errorf("emit %s: %w", fernName, err)
	}
	// Stream the string straight to disk instead of os.WriteFile([]byte(asm)):
	// a driver's asm is ~700 MB–1 GB, and the []byte(asm) conversion allocates
	// a full second copy of it — a needless ~1 GB spike stacked on the emit's
	// already-multi-GB working set right at the peak. io.WriteString on the
	// *os.File writes the string's backing bytes directly, no copy.
	f, err := os.Create(asmPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", asmPath, err)
	}
	if _, err := io.WriteString(f, asm); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", asmPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", asmPath, err)
	}
	return nil
}

var fastLinkOnce sync.Once

var fastLinkFlagVal string

// fastLinkFlag returns the gcc `-fuse-ld=lld` flag when the LLVM linker is
// available, else "". The GNU bfd linker (gcc's default) is pathologically slow
// and memory-heavy on the self-host DRIVER binaries' ~680 MB emitted `.s` — 10+
// minutes, the source of the intermittent shard-link TIMEOUTS (a cold driver link
// hanging past the job deadline). ld.lld links the same `.s` in seconds at a
// fraction of the RSS. Detected once; degrades cleanly — a fork / minimal image
// without lld falls back to bfd (slow but correct).
//
// Used for the NATIVE-Go-emitted DRIVER asm (driverLinkArgs / CachedDriverBin),
// which is the only ~680 MB link and the source of the shard-link timeouts.
// The small SELF-HOST-emitted program links (CachedLink / BuildBin) stay on bfd
// because there is no CI-speed payoff for them — bfd links a few-KB `.s` in
// milliseconds. (Self-host freestanding output USED to link INCORRECTLY under
// lld — `string[][]` -> exit 253 vs bfd's 5 — but that was a codegen +
// heap-layout bug, now fixed: issue #4081 made the self-host x86-64 backend
// emit linker-agnostic output (mmap'd heap, no .bss-ordering assumption; nested
// string-element `.len()` reads the length field, not a layout-dependent data
// pointer). TestSelfHostLinkerAgnosticIRX86_64 gates that property across bfd,
// lld and mold. Switching the program links to lld too is a possible follow-up
// with no measurable upside.) The driver-binary disk cache keys on the `.s`
// CONTENT and a driver is always native-Go-emitted, so an lld- and a bfd-linked
// driver from the same `.s` are interchangeable across the fleet. (The
// arm64-darwin cross-link already uses `-fuse-ld=lld` — same precedent.)
func fastLinkFlag() string {
	fastLinkOnce.Do(func() {
		if _, err := exec.LookPath("ld.lld"); err == nil {
			fastLinkFlagVal = "-fuse-ld=lld"
		}
	})
	return fastLinkFlagVal
}

// driverLinkArgs builds the gcc argv for linking a NATIVE-Go-emitted self-host
// DRIVER `.s` into a static freestanding binary, preferring lld when present (see
// fastLinkFlag — lld is correct for the driver asm and is the slow link). The
// small self-host program links (CachedLink) stay on bfd purely because they're
// already fast; self-host output is lld-correct since #4081 (gated by
// TestSelfHostLinkerAgnosticIRX86_64).
func driverLinkArgs(asmPath, binPath string) []string {
	args := []string{"-static", "-nostdlib", "-no-pie"}
	if f := fastLinkFlag(); f != "" {
		args = append(args, f)
	}
	return append(args, asmPath, "-o", binPath)
}

// CachedLink links asm into a static binary once per (gcc, asm) and
// returns the path to the shared cached binary. Callers copy it to
// wherever they need it; the cached file must not be mutated.
func CachedLink(t *testing.T, gcc, asm string) string {
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
		// bfd (NOT driverLinkArgs/lld): CachedLink links SELF-HOST-emitted programs
		// (via BuildBin). These are small, so bfd is fast; lld's win is only on the
		// ~680 MB native-Go driver. Self-host output is lld-correct since #4081
		// (TestSelfHostLinkerAgnosticIRX86_64 gates it); bfd here is just the
		// no-payoff default, not a correctness requirement.
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
