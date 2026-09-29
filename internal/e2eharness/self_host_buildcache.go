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
	"sort"
	"strings"
	"sync"
	"testing"
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
		linkCacheDir, linkCacheDirErr = ProcessScratchDir("selfhost-bincache")
	})
	return linkCacheDir, linkCacheDirErr
}

// fernImportRe matches a module import — `import "./lexer";`, `import
// "lexer";`, or `import "std/io";`. The captured path is classified by
// isExternalFernImport and, when local, resolved to a sibling `.fern` file.
var fernImportRe = regexp.MustCompile(`(?m)^\s*import\s+"([^"]+)"`)

// isExternalFernImport reports whether an import path names a module OUTSIDE
// the driver's project dir — `std/io`, `core/int` — which is deliberately not
// part of the cache key: the stdlib and the compiler are fixed for the run (see
// the cache-key note above), so hashing them would only add churn.
//
// Everything else (`./lexer`, `../lib/gnu`, a bare sibling `lexer`) is LOCAL
// and must resolve. The distinction is explicit so that a genuinely missing local
// source is an error rather than being silently skipped as if it were a
// stdlib import.
func isExternalFernImport(imp string) bool {
	return !strings.HasPrefix(imp, ".") && strings.Contains(imp, "/")
}

// SelfHostImportClosure returns the entry file plus the transitive set of local
// `.fern` files it imports, resolved relative to each importing file's dir.
// This is the set whose contents actually determine the emitted asm — stray
// `.fern` files sitting in the project dir but NOT imported by the entry (e.g. a
// sibling `asm_pathprobe_run.fern` present while building `asm_run.fern`) are
// excluded, so the same driver hashes identically regardless of what unrelated
// drivers a test happens to drop alongside it.
func SelfHostImportClosure(t testing.TB, dir, fernName string) []string {
	t.Helper()
	files, err := selfHostImportClosure(dir, fernName)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return files
}

// selfHostImportClosure is the error-returning core of SelfHostImportClosure,
// split out so the closure's own contract (a missing local import is an ERROR,
// a missing stdlib import is not) can be asserted directly instead of through a
// deliberately-failing sub-test.
func selfHostImportClosure(dir, fernName string) ([]string, error) {
	return selfHostImportClosures(dir, fernName)
}

// selfHostImportClosures shares one traversal across all requested roots.
// Staging overlapping compiler roots must not reread their common dependencies
// once per root. The visited set lasts only for this call, so later source
// changes and missing imports are still observed.
func selfHostImportClosures(dir string, fernNames ...string) ([]string, error) {
	seen := map[string]bool{}
	var order []string
	var visit func(path string) error
	visit = func(path string) error {
		if seen[path] {
			return nil
		}
		seen[path] = true
		order = append(order, path)
		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("self-host import closure: read %s: %w", path, err)
		}
		base := filepath.Dir(path)
		for _, m := range fernImportRe.FindAllStringSubmatch(string(src), -1) {
			if isExternalFernImport(m[1]) {
				continue
			}
			imp := strings.TrimPrefix(m[1], "./")
			cand := filepath.Join(base, imp+".fern")
			if _, statErr := os.Stat(cand); statErr != nil {
				// A LOCAL import that does not resolve is a staging bug — the
				// test wrote an entry whose sibling is missing from its project
				// dir. Skipping it silently (which is what this did) leaves the
				// file OUT OF THE CACHE KEY, so the driver keeps hashing the
				// same after that source changes and every later test in the run
				// gets a stale binary. That is the failure mode where a fix
				// looks applied and the tests are still exercising the old
				// compiler. Fail here instead: the build was going to fail
				// anyway, and this fails naming the missing file.
				return fmt.Errorf("self-host import closure: %s imports %q but %s does not exist "+
					"(the driver's project dir is missing a source; it would be silently "+
					"omitted from the build-cache key)", path, m[1], cand)
			}
			if err := visit(cand); err != nil {
				return err
			}
		}
		return nil
	}
	for _, fernName := range fernNames {
		if err := visit(filepath.Join(dir, fernName)); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// HashSelfHostSources hashes the entry name plus the contents of every `.fern`
// file in the entry's transitive local-import closure, so the key changes iff a
// source the driver actually compiles changes — and is INVARIANT to unrelated
// `.fern` files in the same dir. That invariance is what lets two tests building
// the same stock driver (e.g. asm_run) share one cache entry even when their
// project dirs differ in which OTHER drivers they also wrote, and lets the two
// worker processes of a CI shard share one driver through the disk cache.
func HashSelfHostSources(t testing.TB, dir, fernName string) string {
	t.Helper()
	files := SelfHostImportClosure(t, dir, fernName)
	sort.Strings(files)
	h := sha256.New()
	fmt.Fprintf(h, "driver\x00entry=%s\x00", fernName)
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

// CopySelfHostDriver copies each named driver AND its entire transitive
// local-import closure from examples/self_host into dir — the staging step
// before BuildSelfHostBin.
//
// It replaces the hand-written module list that used to sit in front of nearly
// every self-host test. There were 457 copies of that list, each a duplicate of
// some driver's import closure, and duplicating it had two costs that both came
// due at once when wasm.fern was deleted (#3457): the deletion had to touch 468
// files, and it still MISSED four tests, because a handful build their driver
// from an inline Go string literal that names the module (`import "./wasm";`)
// rather than the file, so no filename-based sweep could find them. Deriving the
// closure from the driver's own imports makes both failure modes impossible: a
// module that is no longer imported is no longer copied, with nothing to update.
//
// The closure is computed against the SOURCE tree, so it is the same walk
// HashSelfHostSources keys the build cache on — the copied set and the cache key
// cannot disagree about what a driver is made of.
func CopySelfHostDriver(t testing.TB, dir string, entries ...string) {
	t.Helper()
	var names []string
	seen := map[string]bool{}
	for _, entry := range entries {
		for _, p := range SelfHostImportClosure(t, selfHostSrcDir, entry) {
			base := filepath.Base(p)
			if seen[base] {
				continue
			}
			seen[base] = true
			names = append(names, base)
		}
	}
	CopySelfHostFiles(t, dir, names...)
}

// selfHostSrcDir is the one place the path from a test package to the self-host
// sources is written down. Both test packages sit two levels under the repo
// root, which is what makes the single relative path correct for both.
const selfHostSrcDir = "../../examples/self_host"

// CopySelfHostFiles copies the named examples/self_host sources into dir —
// the staging step before BuildSelfHostBin for tests that hand-pick a driver's
// import closure instead of copySelfHostTree'ing the whole directory.
//
// Each name is expanded through its own import closure, so a hand-written list
// names the modules a test CARES about and cannot go stale when one of them
// gains an import. Ninety-odd call sites each restate a closure by hand; every
// one of them broke at once when parser.fern gained `import "./ast"` (#6993),
// which is the same staleness #7183 fixed for drivers by deriving the set.
// Expanding is safe for a list that was already complete: the closure of a
// complete set is itself, and HashSelfHostSources rejects a set that is not.
func CopySelfHostFiles(t testing.TB, dir string, names ...string) {
	t.Helper()
	files, err := selfHostImportClosures(selfHostSrcDir, names...)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range files {
		base := filepath.Base(p)
		if seen[base] {
			continue
		}
		seen[base] = true
		src, err := os.ReadFile(filepath.Join(selfHostSrcDir, base))
		if err != nil {
			t.Fatalf("read %s: %v", base, err)
		}
		if err := os.WriteFile(filepath.Join(dir, base), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", base, err)
		}
	}
}

// BuildSelfHostBin compiles the self-host driver dir/fernName with the pinned
// stage0 compiler (Stage0Compiler) into an x86-64 binary and returns dir/out.
// The build is cached by the driver's source closure, the compiler and the
// stdlib (CachedDriverBin), so a CI shard compiles each driver once. The
// binary is copied to dir/out so callers that exec it (or drop sibling files
// next to it) see a real file in their own dir.
//
// gcc is unused here since the drivers stopped being Go-emitted asm; it stays
// in the signature because the same tests link self-host-emitted programs
// with it, and two thousand call sites pass the pair together.
func BuildSelfHostBin(t testing.TB, gcc, dir, fernName, out string) string {
	t.Helper()
	if InterpDriverMode() {
		return writeInterpDriverShim(t, dir, fernName, out)
	}
	dst := filepath.Join(dir, out)
	copyExecutable(t, CachedDriverBin(t, gcc, dir, fernName), dst)
	return dst
}

// CachedDriverBin builds (or restores) the self-host driver binary for
// dir/fernName and returns the path to the shared cached binary (callers
// copyExecutable it where they need it).
//
// The key is the driver's source closure plus driverCompilerKey (the pin's
// bytes and the stdlib), so a driver rebuilds when its sources, the pin or the
// stdlib change, and two tests staging the same stock driver share one build.
// The disk cache (FERN_SELFHOST_BUILD_CACHE) shares it across the worker
// processes of a CI shard; a static freestanding ELF from the same compiler
// and sources is the same bytes on every runner.
func CachedDriverBin(t testing.TB, gcc, dir, fernName string) string {
	t.Helper()
	compiler := Stage0Compiler(t)
	key := HashSelfHostSources(t, dir, fernName) + "-" + driverCompilerKey(t)[:16]
	path, err := selfHostDriverBinCache.get(key, func() (string, error) {
		return cachedBinary(t, "drv-"+key, key+".driverbin", func(binPath string) error {
			// The reservation serialises heavy builds on a RAM-limited host
			// (and parallelises up to the budget on a big one): two cold
			// driver builds peaking at once used to cross a 16 GB host's RAM
			// and OOM-kill the run (exit 137) — see buildMemLimiter.
			return compileWithSelfHost(t, compiler, filepath.Join(dir, fernName), binPath, driverBuildWeightMB(fernName))
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// CachedLink links asm into a static binary once per (gcc, asm) and
// returns the path to the shared cached binary. Callers copy it to
// wherever they need it; the cached file must not be mutated.
func CachedLink(t testing.TB, gcc, asm string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(gcc + "\x00" + asm))
	key := hex.EncodeToString(sum[:])
	// The cross-PROCESS disk .bin is keyed by the asm CONTENT alone (not gcc),
	// so a binary another process linked is reused even though two processes
	// may resolve different gcc paths (x86_64-linux-gnu-gcc vs gcc)
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
		// Cross-process disk hit: a binary another process linked — copy it
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
		if err := linkSelfHostAsm(gcc, base, key, asm, binPath); err != nil {
			return "", err
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

// linkSelfHostAsm turns SELF-HOST-emitted asm into a static binary at
// binPath.
//
// Small links (the overwhelming majority — a few-KB `.s` per e2e program)
// go to gcc/bfd: milliseconds, and they keep the external-toolchain path
// exercised across the suite. Self-host output is lld-correct since #4081
// (TestSelfHostLinkerAgnosticIRX86_64 gates it); bfd is just the
// no-benefit default, not a correctness requirement.
//
// HUGE links — the stage-2 self-compile of the whole compiler, ~450 MB of
// asm — first try the in-process native assembler (nativeLinkX86) under a
// build-memory reservation sized to its measured footprint. NOTE: the
// self-host x86-64 emitter currently writes AT&T-syntax asm, which the
// Intel-only native assembler rejects immediately — so today the stage-2
// link always takes the gcc fallback below; the native attempt costs
// microseconds and starts winning the moment the emitted dialect becomes
// parseable. Either way the big-link gcc path now runs under a
// reservation too: it ran GNU `as` at ~4.7 GB RSS with NO reservation at
// all (CachedLink predates the budget), which could stack with a
// concurrent driver build's peak and OOM a 16 GB host.
func linkSelfHostAsm(gcc, base, key, asm, binPath string) error {
	if len(asm) >= nativeLinkMinAsmBytes {
		if err := withBuildMemory(nativeLinkWeightMB(len(asm)), func() error {
			// The soft heap cap bounds the assembler's own GC overshoot
			// the same way it bounds the driver emit's.
			return withEmitMemLimit(func() error {
				return nativeLinkX86(asm, binPath)
			})
		}); err == nil {
			return nil
		}
	}
	asmPath := filepath.Join(base, key+".s")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		return err
	}
	gccLink := func() error {
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
			return fmt.Errorf("gcc: %w\n%s", err, out)
		}
		return nil
	}
	// Big-asm gcc links (today: every stage-2 self-compile — the self-host
	// x86-64 emitter writes AT&T syntax, which the Intel-only native
	// assembler rejects, so the native attempt above always falls through
	// for them) run GNU `as` at multi-GB RSS. Reserve the estimated peak so
	// the link can't stack with a concurrent driver build's peak — this
	// link had NO reservation historically. Small links stay unreserved.
	if len(asm) >= nativeLinkMinAsmBytes {
		return withBuildMemory(gccBigLinkWeightMB(len(asm)), gccLink)
	}
	return gccLink()
}

// copyExecutable links (preferably) or copies src to dst with 0755 perms.
// Dozens of tests per shard each materialise a cached driver binary (8-12 MB)
// into their t.TempDir; the cached binary is read-only and only ever exec'd,
// so a HARDLINK is equivalent and effectively free, and the copy is the
// fallback for src/dst on different filesystems. t.TempDir teardown just
// drops the extra link.
func copyExecutable(t testing.TB, src, dst string) {
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

// TrackFernSources makes the go command's test cache aware of the Fern sources
// a test compiles through a CHILD process.
//
// `go test` reuses a cached result until something it can see changes: the
// package's Go files, the environment, the command line, and the files the TEST
// PROCESS itself opened. It cannot see what a compiler the test exec'd opened.
// So a suite that hands `coreutils/head.fern` to the `fern` binary keeps
// reporting its previous result after that file changes — `ok … (cached)`, every
// subtest PASS, nothing executed (#9087). Reading the closure in the test
// process puts those files in its testlog, which is what the key is built from.
//
// Only `dir`-local imports need this: `std/…` and `core/…` reach the compiler
// through internal/stdlib's go:embed, so they are already part of the package's
// build inputs.
func TrackFernSources(t testing.TB, dir, fernName string) {
	t.Helper()
	// The closure walk reads every file it visits, which is the whole point
	// here; the list it returns is not needed.
	SelfHostImportClosure(t, dir, fernName)
}
