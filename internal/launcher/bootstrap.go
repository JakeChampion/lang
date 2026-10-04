package launcher

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/jakechampion/lang/bootstrap"
	selfhost "github.com/jakechampion/lang/examples/self_host"
)

// hostTarget is the -target the self-host compiler runs as on this machine,
// "" for a host no stage0 is published for.
func hostTarget() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "x86-64-linux"
	case "linux/arm64":
		return "arm64-linux"
	case "darwin/arm64":
		return "arm64-darwin"
	}
	return ""
}

// lockField is a stage0.lock value: `url`, or a host's sha256.
func lockField(lock, key string) string {
	sc := bufio.NewScanner(strings.NewReader(lock))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[0] == key {
			return f[1]
		}
	}
	return ""
}

// built is the self-host compiler this `fern` built from its embedded
// sources, building it first when the cache has none: the pinned stage0,
// fetched and checked as `make bootstrap` does, compiles fern.fern once per
// source, stdlib and pin.
func built() (string, error) {
	host := hostTarget()
	if host == "" {
		return "", fmt.Errorf("no stage0 compiler is published for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	stdlibDir, err := StdlibRoot()
	if err != nil {
		return "", err
	}
	key, err := sourcesKey(host)
	if err != nil {
		return "", err
	}
	out := filepath.Join(cache, "fern", "selfhost-"+key[:16], "fern-selfhost")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	stage0, err := fetchStage0(filepath.Join(cache, "fern"), host)
	if err != nil {
		return "", err
	}
	src, err := os.MkdirTemp("", "fern-selfhost-src-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(src)
	if err := writeFS(selfhost.Sources, src); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	tmp := out + fmt.Sprintf(".%d.tmp", os.Getpid())
	fmt.Fprintln(os.Stderr, "fern: building the self-host compiler with the pinned stage0 (once per version; a minute or so)")
	cmd := exec.Command(stage0, "-target", host, "-o", tmp, filepath.Join(src, "fern.fern"), stdlibDir)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("the stage0 compiler could not build the self-host compiler: %v", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, out); err != nil {
		return "", err
	}
	return out, nil
}

// sourcesKey hashes what the built compiler depends on: its sources, the
// stdlib it compiles against, the pin that builds it and the host.
func sourcesKey(host string) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", host, bootstrap.Lock)
	if err := hashFS(h, selfhost.Sources); err != nil {
		return "", err
	}
	_, sum, err := stdlibFiles(stdlibFS())
	if err != nil {
		return "", err
	}
	h.Write([]byte(sum))
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashFS(h io.Writer, src fs.FS) error {
	var names []string
	if err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, p)
		}
		return err
	}); err != nil {
		return err
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := fs.ReadFile(src, n)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", n, len(b))
		h.Write(b)
	}
	return nil
}

func writeFS(src fs.FS, dir string) error {
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
}

// fetchStage0 is the pinned stage0 for host under dir, downloaded and
// checked against the lock's sha256 when it is not already there.
func fetchStage0(dir, host string) (string, error) {
	url := lockField(bootstrap.Lock, "url")
	want := lockField(bootstrap.Lock, host)
	if url == "" || want == "" {
		return "", fmt.Errorf("stage0.lock pins no compiler for %s", host)
	}
	tag := url[strings.LastIndex(url, "/")+1:]
	path := filepath.Join(dir, "stage0", tag, "fern-selfhost-"+host)
	if got, err := fileSHA256(path); err == nil {
		if got != want {
			return "", fmt.Errorf("cached %s has sha256 %s, the lock pins %s: delete it and retry", path, got, want)
		}
		return path, nil
	}
	asset := url + "/fern-selfhost-" + host + ".gz"
	fmt.Fprintln(os.Stderr, "fern: downloading the stage0 compiler "+asset)
	resp, err := http.Get(asset)
	if err != nil {
		return "", fmt.Errorf("download %s: %v", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", asset, resp.Status)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%s is not gzip data: %v", asset, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp := path + fmt.Sprintf(".%d.tmp", os.Getpid())
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), zr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("download %s: %v", asset, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(tmp)
		return "", fmt.Errorf("sha256 mismatch for %s: the lock pins %s, downloaded %s", asset, want, got)
	}
	return path, os.Rename(tmp, path)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
