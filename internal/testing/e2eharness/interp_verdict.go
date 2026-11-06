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
)

const fernModulePath = "github.com/jakechampion/lang"

// InterpVerdict runs `interpBin -interp src` and returns its stdout and exit
// status. FERN_SELFHOST_BUILD_CACHE enables the cache. Its key covers the Go
// interpreter's source and embedded files, the suite's relative import closure,
// and the FERN_* environment. It excludes the compiler package's embedded
// self-host sources, which -interp does not read.
// FERN_BIN suites also run an external executable. Run them
// without the verdict cache because that executable can change at the same path.
func InterpVerdict(t testing.TB, interpBin, src string) ([]byte, int) {
	t.Helper()
	var key string
	if os.Getenv("FERN_BIN") == "" {
		var err error
		key, err = interpVerdictKey(src)
		if err != nil {
			t.Fatalf("interpreter verdict key for %s: %v", src, err)
		}
		if out, exit, ok := readInterpVerdict(key); ok {
			return out, exit
		}
	}
	var out []byte
	exit := 0
	err := WithBuildMemoryMB(1024, func() error {
		var runErr error
		out, runErr = exec.Command(interpBin, "-interp", src).Output()
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exit = ee.ExitCode()
			return nil
		}
		return runErr
	})
	if err != nil {
		t.Fatalf("interpreter on %s: %v", src, err)
	}
	if key != "" {
		writeInterpVerdict(key, out, exit)
	}
	return out, exit
}

func interpVerdictKey(src string) (string, error) {
	oracle, err := interpOracleKey()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	closure, err := selfHostImportClosure(filepath.Dir(abs), filepath.Base(abs))
	if err != nil {
		return "", err
	}
	sort.Strings(closure)
	h := sha256.New()
	fmt.Fprintf(h, "interp-verdict-v1\x00%s\x00%s\x00", oracle, abs)
	for _, p := range closure {
		if err := hashFile(h, p, p); err != nil {
			return "", err
		}
	}
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "FERN_") && !strings.HasPrefix(kv, "FERN_SELFHOST_BUILD_CACHE=") {
			env = append(env, kv)
		}
	}
	sort.Strings(env)
	fmt.Fprintf(h, "env\x00%s\x00", strings.Join(env, "\x00"))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// interpOracleKey hashes the files of every package in this module that
// cmd/fern links, so a change to the parser, checker, interpreter or stdlib
// changes it. The compiler package is left out: it only embeds the self-host
// sources that `-target` runs, which `-interp` never reads.
var interpOracleKey = sync.OnceValues(func() (string, error) {
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
		for _, list := range fields[2:] {
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

func hashFile(h interface{ Write([]byte) (int, error) }, path, name string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Fprintf(h, "%s\x00%d\x00", name, len(data))
	h.Write(data)
	return nil
}

func readInterpVerdict(key string) ([]byte, int, bool) {
	for _, dir := range diskCacheReadDirs() {
		path := filepath.Join(dir, key+".interp")
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

func writeInterpVerdict(key string, out []byte, exit int) {
	dir := diskCacheWriteDir()
	if dir == "" {
		return
	}
	tmp, err := os.CreateTemp(dir, key+".interp.tmp*")
	if err != nil {
		return
	}
	fmt.Fprintf(tmp, "exit=%d\n", exit)
	_, werr := tmp.Write(out)
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, key+".interp")); err != nil {
		os.Remove(tmp.Name())
	}
}
