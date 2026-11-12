// CI-DARK: FERN_BIN — A person sets it to run fern_binary_e2e_test.fern against one fern executable. CI never sets it, and a run with it skips the verdict cache.
package e2eharness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/stdlib"
	"github.com/jakechampion/lang/internal/tools/literate"
)

const fernModulePath = "github.com/jakechampion/lang"

// InterpVerdict runs `oracle -interp src` and returns its stdout and exit
// status, through the verdict cache of oracleVerdict.
func InterpVerdict(t testing.TB, oracle, src string) ([]byte, int) {
	t.Helper()
	return oracleVerdict(t, oracle, []string{"-interp"}, src, "", false)
}

// InterpInputVerdict is InterpVerdict with stdin as the program's input.
func InterpInputVerdict(t testing.TB, oracle, src, stdin string) ([]byte, int) {
	t.Helper()
	return oracleVerdict(t, oracle, []string{"-interp"}, src, stdin, false)
}

// CheckVerdict runs `oracle -check src` and returns its combined stdout and
// stderr and its exit status, through the verdict cache of oracleVerdict.
func CheckVerdict(t testing.TB, oracle, src string) ([]byte, int) {
	t.Helper()
	return oracleVerdict(t, oracle, []string{"-check"}, src, "", true)
}

// CheckTargetVerdict runs `oracle -check -target target src` and returns its
// combined stdout and stderr and its exit status, through the verdict cache.
func CheckTargetVerdict(t testing.TB, oracle, src, target string) ([]byte, int) {
	t.Helper()
	return oracleVerdict(t, oracle, []string{"-check", "-target", target}, src, "", true)
}

// oracleVerdict runs the Go oracle with flags on src, with stdin as its input.
// FERN_SELFHOST_BUILD_CACHE enables the cache. Its key covers the oracle's
// sources (oracleSourcesKey), the source's relative import closure, the stdlib
// modules that the closure imports, and the FERN_* environment.
// FERN_BIN suites also run an external executable. Run them
// without the verdict cache because that executable can change at the same path.
func oracleVerdict(t testing.TB, oracle string, flags []string, src, stdin string, combined bool) ([]byte, int) {
	t.Helper()
	var key string
	if os.Getenv("FERN_BIN") == "" {
		keyed := flags
		if stdin != "" {
			keyed = append(append([]string{}, flags...), "stdin", stdin)
		}
		var err error
		key, err = oracleVerdictKey(keyed, combined, src)
		if err != nil {
			t.Fatalf("oracle verdict key for %s %s: %v", strings.Join(flags, " "), src, err)
		}
	}
	cmd := exec.Command(oracle, append(append([]string{}, flags...), src)...)
	cmd.Stdin = strings.NewReader(stdin)
	return cachedOracleRun(t, key, cmd, combined)
}

// CheckSourceVerdict runs `oracle -check -` with source on stdin, and returns
// its combined stdout and stderr and its exit status, through the verdict
// cache. source can import only modules such as std/test: a program on stdin
// has no directory for a relative import.
func CheckSourceVerdict(t testing.TB, oracle, source string) ([]byte, int) {
	t.Helper()
	key := sourceVerdictKey(t, "-check -", source)
	cmd := exec.Command(oracle, "-check", "-")
	cmd.Stdin = strings.NewReader(source)
	return cachedOracleRun(t, key, cmd, true)
}

// InProcessSourceVerdict is InProcessVerdict for a program given as text.
// Like CheckSourceVerdict, it refuses a source with a relative import.
func InProcessSourceVerdict(t testing.TB, mode, source string, run func() ([]byte, int)) ([]byte, int) {
	t.Helper()
	return keptVerdict(sourceVerdictKey(t, "in-process\x00"+mode, source), run)
}

// sourceVerdictKey is the key of what the oracle does with source, or empty
// when FERN_BIN names another oracle.
func sourceVerdictKey(t testing.TB, what, source string) string {
	t.Helper()
	imports, err := fernSourceImports(source)
	if err != nil {
		t.Fatalf("tokenize the source of a verdict: %v", err)
	}
	for _, imp := range imports {
		if !isExternalFernImport(imp) {
			t.Fatalf("the source of a verdict imports %q, a relative module", imp)
		}
	}
	if os.Getenv("FERN_BIN") != "" {
		return ""
	}
	oracleKey, err := oracleSourcesKey()
	if err != nil {
		t.Fatalf("oracle verdict key for %s: %v", what, err)
	}
	h := sha256.New()
	fmt.Fprintf(h, "oracle-source-verdict-v3\x00%s\x00%s\x00%d\x00%s\x00%s\x00env\x00%s\x00", what, oracleKey, len(source), source, stdlibClosureKey(imports, stdlib.ReadModule), fernEnvKey())
	return hex.EncodeToString(h.Sum(nil))
}

// InProcessVerdict returns the verdict that run computes for src, through the
// verdict cache. run must compute it with the oracle's own Go code in this
// process, which the oracle's key covers. mode names what run does, and keeps
// its verdicts apart from those of other modes.
func InProcessVerdict(t testing.TB, mode, src string, run func() ([]byte, int)) ([]byte, int) {
	t.Helper()
	var key string
	if os.Getenv("FERN_BIN") == "" {
		var err error
		key, err = oracleVerdictKey([]string{"in-process", mode}, true, src)
		if err != nil {
			t.Fatalf("oracle verdict key for %s %s: %v", mode, src, err)
		}
	}
	return keptVerdict(key, run)
}

// keptVerdict returns the verdict under key, or the one that run computes,
// which it then keeps under key. An empty key calls run without the cache.
func keptVerdict(key string, run func() ([]byte, int)) ([]byte, int) {
	if key != "" {
		if out, exit, ok := readOracleVerdict(key); ok {
			return out, exit
		}
	}
	out, exit := run()
	if key != "" {
		writeOracleVerdict(key, out, exit)
	}
	return out, exit
}

// cachedOracleRun returns the verdict under key, or runs cmd and keeps its
// verdict there. An empty key runs cmd without the cache.
func cachedOracleRun(t testing.TB, key string, cmd *exec.Cmd, combined bool) ([]byte, int) {
	t.Helper()
	if key != "" {
		if out, exit, ok := readOracleVerdict(key); ok {
			return out, exit
		}
	}
	var out []byte
	exit := 0
	err := WithBuildMemoryMB(1024, func() error {
		var runErr error
		if combined {
			out, runErr = cmd.CombinedOutput()
		} else {
			out, runErr = cmd.Output()
		}
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exit = ee.ExitCode()
			return nil
		}
		return runErr
	})
	if err != nil {
		t.Fatalf("oracle %s: %v", strings.Join(cmd.Args, " "), err)
	}
	if key != "" {
		writeOracleVerdict(key, out, exit)
	}
	return out, exit
}

func oracleVerdictKey(flags []string, combined bool, src string) (string, error) {
	oracle, err := oracleSourcesKey()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	closure, missing, err := importClosureNotingMissing(filepath.Dir(abs), filepath.Base(abs))
	if err != nil {
		return "", err
	}
	sort.Strings(closure)
	sort.Strings(missing)
	h := sha256.New()
	fmt.Fprintf(h, "oracle-verdict-v3\x00%s\x00%t\x00%s\x00%s\x00%s\x00", strings.Join(flags, "\x00"), combined, oracle, src, abs)
	var imports []string
	for _, p := range closure {
		if err := hashFile(h, p, p); err != nil {
			return "", err
		}
		code, err := literate.ReadFile(p)
		if err != nil {
			return "", err
		}
		// A file that does not lex imports nothing, as in importClosure.
		if found, err := fernSourceImports(string(code)); err == nil {
			imports = append(imports, found...)
		}
	}
	fmt.Fprintf(h, "stdlib\x00%s\x00", stdlibClosureKey(imports, stdlib.ReadModule))
	for _, p := range missing {
		fmt.Fprintf(h, "missing\x00%s\x00", p)
	}
	fmt.Fprintf(h, "env\x00%s\x00", fernEnvKey())
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fernEnvKey is the FERN_* environment, less the cache directory itself and
// the *_SHARD selectors. A selector chooses which cases a CI leg runs, so it
// must not keep one leg from the verdicts of another.
func fernEnvKey() string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "FERN_") && name != "FERN_SELFHOST_BUILD_CACHE" && !strings.HasSuffix(name, "_SHARD") {
			env = append(env, kv)
		}
	}
	sort.Strings(env)
	return strings.Join(env, "\x00")
}

// oracleSourcesKey hashes the files of every package in this module that
// cmd/fern links, so a change to the parser, checker or interpreter changes
// it. The compiler package is left out: it only embeds the self-host sources
// that `-target` runs, which `-interp` and `-check` never read. The stdlib
// package keeps its Go files but not its embedded modules. stdlibClosureKey
// hashes the modules that a program imports.
var oracleSourcesKey = sync.OnceValues(func() (string, error) {
	tmpl := `{{if .Module}}{{if eq .Module.Path "` + fernModulePath + `"}}{{.ImportPath}}` + "\t" +
		`{{.Dir}}` + "\t" + `{{join .GoFiles ","}}` + "\t" + `{{join .EmbedFiles ","}}{{end}}{{end}}`
	cmd := exec.Command("go", "list", "-deps", "-f", tmpl, fernModulePath+"/cmd/fern")
	cmd.Dir = RepoPath()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list: %v\n%s", err, stderr.String())
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s/%s\x00", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	for _, name := range []string{"go.mod", "go.sum"} {
		if err := hashFile(h, RepoPath(name), name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	lines := strings.Split(string(out), "\n")
	sort.Strings(lines)
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return "", fmt.Errorf("go list: unexpected line %q", line)
		}
		if fields[0] == fernModulePath+"/compiler" {
			continue
		}
		lists := fields[2:]
		if fields[0] == fernModulePath+"/internal/stdlib" {
			lists = fields[2:3]
		}
		for _, list := range lists {
			for _, f := range strings.Split(list, ",") {
				if f == "" {
					continue
				}
				if err := hashFile(h, filepath.Join(fields[1], f), fields[0]+"/"+f); err != nil {
					return "", err
				}
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
})

// stdlibClosureKey hashes the stdlib modules that imports reach, directly or
// through other stdlib modules. The oracle reads a stdlib module only when an
// import names it. Thus a change to another stdlib module keeps the key.
func stdlibClosureKey(imports []string, read func(string) (stdlib.ModuleSource, error)) string {
	modules := map[string]string{}
	var visit func(imp string)
	visit = func(imp string) {
		name := stdlib.ModuleKey(imp)
		if name == "" {
			return
		}
		if _, ok := modules[name]; ok {
			return
		}
		m, err := read(imp)
		if err != nil {
			modules[name] = "error\x00" + err.Error()
			return
		}
		modules[name] = fmt.Sprintf("%d\x00%s\x00%s\x00%d\x00%s", len(m.Code), m.Code, m.DocPath, len(m.DocSource), m.DocSource)
		// A module that does not lex imports nothing, as in importClosure.
		nested, err := fernSourceImports(m.Code)
		if err != nil {
			return
		}
		for _, n := range nested {
			visit(n)
		}
	}
	for _, imp := range imports {
		visit(imp)
	}
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		fmt.Fprintf(h, "%s\x00%s\x00", name, modules[name])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashFile(h interface{ Write([]byte) (int, error) }, path, name string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Fprintf(h, "%s\x00%d\x00", name, len(data))
	h.Write(data)
	return nil
}

func readOracleVerdict(key string) ([]byte, int, bool) {
	for _, dir := range diskCacheReadDirs() {
		path := filepath.Join(dir, key+".verdict")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		head, out, ok := bytes.Cut(data, []byte("\n"))
		if !ok || !bytes.HasPrefix(head, []byte("exit=")) {
			continue
		}
		exit, err := strconv.Atoi(string(head[len("exit="):]))
		if err != nil {
			continue
		}
		now := time.Now()
		_ = os.Chtimes(path, now, now)
		return out, exit, true
	}
	return nil, 0, false
}

func writeOracleVerdict(key string, out []byte, exit int) {
	dir := diskCacheWriteDir()
	if dir == "" {
		return
	}
	tmp, err := os.CreateTemp(dir, key+".verdict.tmp*")
	if err != nil {
		return
	}
	fmt.Fprintf(tmp, "exit=%d\n", exit)
	_, werr := tmp.Write(out)
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, key+".verdict")); err != nil {
		os.Remove(tmp.Name())
	}
}
