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

	"github.com/jakechampion/lang/internal/syntax/lexer"
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

func diskCacheWriteDir() string {
	dirs := diskCacheReadDirs()
	if len(dirs) == 0 {
		return ""
	}
	return dirs[0]
}

func linkCacheBaseDir() (string, error) {
	linkCacheDirOnce.Do(func() {
		linkCacheDir, linkCacheDirErr = ProcessScratchDir("selfhost-bincache")
	})
	return linkCacheDir, linkCacheDirErr
}

var fernImportRe = regexp.MustCompile(`(?m)^\s*import\s+"([^"]+)"`)

func fernSourceImports(src string) ([]string, error) {
	tokens, _, err := lexer.Tokenize(src)
	if err != nil {
		return nil, err
	}
	var imports []string
	for i, tok := range tokens {
		moduleImport := tok.Kind == lexer.Keyword && tok.Text == "import"
		reexport := tok.Kind == lexer.Keyword && tok.Text == "use" && i > 0 &&
			tokens[i-1].Kind == lexer.Keyword && tokens[i-1].Text == "pub"
		if (moduleImport || reexport) && i+1 < len(tokens) && tokens[i+1].Kind == lexer.String {
			imports = append(imports, tokens[i+1].Text)
		}
	}
	return imports, nil
}

func isExternalFernImport(imp string) bool {
	return !strings.HasPrefix(imp, ".") && strings.Contains(imp, "/")
}

func SelfHostImportClosure(t testing.TB, dir, fernName string) []string {
	t.Helper()
	files, err := selfHostImportClosure(dir, fernName)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return files
}

func selfHostImportClosure(dir, fernName string) ([]string, error) {
	return selfHostImportClosures(dir, fernName)
}

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
		imports, err := fernSourceImports(string(src))
		if err != nil {
			return fmt.Errorf("self-host import closure: tokenize %s: %w", path, err)
		}
		for _, imported := range imports {
			if isExternalFernImport(imported) {
				continue
			}
			imp := strings.TrimPrefix(imported, "./")
			cand := filepath.Join(base, imp+".fern")
			if _, statErr := os.Stat(cand); statErr != nil {
				return fmt.Errorf("self-host import closure: %s imports %q but %s does not exist "+
					"(the driver's project dir is missing a source; it would be silently "+
					"omitted from the build-cache key)", path, imported, cand)
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

func CopySelfHostDriver(t testing.TB, dir string, entries ...string) {
	t.Helper()
	var names []string
	seen := map[string]bool{}
	for _, entry := range entries {
		for _, p := range SelfHostImportClosure(t, selfHostSrcDir(), entry) {
			rel := selfHostRel(t, p)
			if seen[rel] {
				continue
			}
			seen[rel] = true
			names = append(names, rel)
		}
	}
	CopySelfHostFiles(t, dir, names...)
}

func selfHostSrcDir() string { return RepoPath("compiler") }

func CopySelfHostFiles(t testing.TB, dir string, names ...string) {
	t.Helper()
	files, err := selfHostImportClosures(selfHostSrcDir(), names...)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range files {
		rel := selfHostRel(t, p)
		if seen[rel] {
			continue
		}
		seen[rel] = true
		src, err := os.ReadFile(filepath.Join(selfHostSrcDir(), rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		target := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(target, src, 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func selfHostRel(t testing.TB, p string) string {
	t.Helper()
	rel, err := filepath.Rel(selfHostSrcDir(), p)
	if err != nil {
		t.Fatalf("%s is not under %s: %v", p, selfHostSrcDir(), err)
	}
	return filepath.ToSlash(rel)
}

func BuildSelfHostBin(t testing.TB, gcc, dir, fernName, out string) string {
	t.Helper()
	return BuildSelfHostBinFor(t, dir, fernName, out, TargetX86_64Linux)
}

func BuildSelfHostBinFor(t testing.TB, dir, fernName, out, target string) string {
	t.Helper()
	if target == TargetX86_64Linux && InterpDriverMode() {
		return writeInterpDriverShim(t, dir, fernName, out)
	}
	dst := filepath.Join(dir, out)
	copyExecutable(t, CachedDriverBinFor(t, dir, fernName, target), dst)
	return dst
}

func CachedDriverBin(t testing.TB, gcc, dir, fernName string) string {
	t.Helper()
	return CachedDriverBinFor(t, dir, fernName, TargetX86_64Linux)
}

func CachedDriverBinFor(t testing.TB, dir, fernName, target string) string {
	t.Helper()
	compiler := Stage0Compiler(t)
	key := HashSelfHostSources(t, dir, fernName) + "-" + target + "-" + driverCompilerKey(t)[:16]
	path, err := selfHostDriverBinCache.get(key, func() (string, error) {
		return cachedBinary(t, "drv-"+key, key+".driverbin", func(binPath string) error {
			return CompileWithSelfHost(t, compiler, target, filepath.Join(dir, fernName), binPath, DriverBuildWeightMB(fernName))
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func CachedLink(t testing.TB, gcc, asm string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(gcc + "\x00" + asm))
	key := hex.EncodeToString(sum[:])
	asmSum := sha256.Sum256([]byte(asm))
	diskKey := hex.EncodeToString(asmSum[:])
	path, err := selfHostBinCache.get(key, func() (string, error) {
		base, err := linkCacheBaseDir()
		if err != nil {
			return "", fmt.Errorf("link cache dir: %w", err)
		}
		binPath := filepath.Join(base, key)
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

func linkSelfHostAsm(gcc, base, key, asm, binPath string) error {
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
	if len(asm) >= bigLinkMinAsmBytes {
		return withBuildMemory(gccBigLinkWeightMB(len(asm)), gccLink)
	}
	return gccLink()
}

func copyExecutable(t testing.TB, src, dst string) {
	t.Helper()
	_ = os.Remove(dst)
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

func TrackFernSources(t testing.TB, dir, fernName string) {
	t.Helper()
	SelfHostImportClosure(t, dir, fernName)
}

var parentImportRe = regexp.MustCompile(`(?m)^(\s*import\s+")\.\./`)

func FlatDriverSource(src string) string {
	return parentImportRe.ReplaceAllString(src, "${1}./")
}
