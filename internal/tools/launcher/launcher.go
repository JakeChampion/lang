package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/jakechampion/lang/internal/stdlib"
)

const EnvCompiler = "FERN_SELFHOST"

func Compiler() (string, error) {
	if p := os.Getenv(EnvCompiler); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("fern: %s=%s: %v", EnvCompiler, p, err)
		}
		return p, nil
	}
	exe, err := os.Executable()
	if err == nil {
		sib := filepath.Join(filepath.Dir(exe), "fern-selfhost")
		if current(sib) {
			return sib, nil
		}
	}
	p, err := built()
	if err != nil {
		return "", fmt.Errorf("fern: no self-host compiler to compile with, and none could be built (%v): run `make bootstrap`, which installs bin/fern-selfhost beside bin/fern, or set %s", err, EnvCompiler)
	}
	return p, nil
}

func StdlibRoot() (string, error) {
	src := stdlibFS()
	files, sum, err := stdlibFiles(src)
	if err != nil {
		return "", err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("fern: no cache directory for the stdlib: %v", err)
	}
	dir := filepath.Join(cache, "fern", "stdlib-"+sum[:16])
	if _, err := os.Stat(filepath.Join(dir, ".complete")); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), "stdlib-tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	for _, name := range files {
		b, err := fs.ReadFile(src, name)
		if err != nil {
			return "", err
		}
		p := filepath.Join(tmp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, ".complete"), nil, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if _, serr := os.Stat(filepath.Join(dir, ".complete")); serr != nil {
			return "", err
		}
	}
	return dir, nil
}

func stdlibFS() fs.FS { return stdlib.FS() }

func stdlibFiles(src fs.FS) ([]string, string, error) {
	var files []string
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, name := range files {
		b, err := fs.ReadFile(src, name)
		if err != nil {
			return nil, "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(b))
		h.Write(b)
	}
	return files, hex.EncodeToString(h.Sum(nil)), nil
}
