package pkgcache

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func Root() (string, error) {
	if d := os.Getenv("FERN_CACHE_DIR"); d != "" {
		return filepath.Join(d, "pkgs"), nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating user cache dir: %w", err)
	}
	return filepath.Join(base, "fern", "pkgs"), nil
}

func ValidateHash(hash string) error {
	hexpart, ok := strings.CutPrefix(hash, "sha256:")
	if !ok || len(hexpart) != 64 {
		return fmt.Errorf("hash must be `sha256:` + 64 lowercase hex digits of the archive bytes, got %q", hash)
	}
	for _, r := range hexpart {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("hash must be `sha256:` + 64 lowercase hex digits of the archive bytes, got %q", hash)
		}
	}
	return nil
}

func Dir(hash string) (string, bool, error) {
	if err := ValidateHash(hash); err != nil {
		return "", false, err
	}
	hexpart, _ := strings.CutPrefix(hash, "sha256:")
	root, err := Root()
	if err != nil {
		return "", false, err
	}
	d := filepath.Join(root, hexpart)
	if st, err := os.Stat(d); err == nil && st.IsDir() {
		return d, true, nil
	}
	return d, false, nil
}

func Fetch(url, hash string) (string, error) {
	dir, present, err := Dir(hash)
	if err != nil {
		return "", err
	}
	if present {
		return dir, nil
	}
	raw, err := download(url)
	if err != nil {
		return "", err
	}
	got := HashBytes(raw)
	if got != hash {
		return "", fmt.Errorf("fetch %s: hash mismatch — manifest declares %s, server sent %s; refusing to unpack", url, hash, got)
	}
	return dir, unpackInto(dir, raw)
}

func FetchUnverified(url string) (hash, dir string, err error) {
	raw, err := download(url)
	if err != nil {
		return "", "", err
	}
	hash = HashBytes(raw)
	dir, present, err := Dir(hash)
	if err != nil {
		return "", "", err
	}
	if present {
		return hash, dir, nil
	}
	return hash, dir, unpackInto(dir, raw)
}

func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func download(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %s", url, resp.Status)
	}
	const maxArchive = 64 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxArchive+1))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	if len(raw) > maxArchive {
		return nil, fmt.Errorf("fetch %s: archive exceeds %d bytes", url, maxArchive)
	}
	return raw, nil
}

func unpackInto(dir string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".unpack-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	gz, err := gzip.NewReader(strings.NewReader(string(raw)))
	if err != nil {
		return fmt.Errorf("not a gzip archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if name == "." {
			continue
		}
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("archive entry %q escapes the package directory", hdr.Name)
		}
		dst := filepath.Join(tmp, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, 64<<20)); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive entry %q has unsupported type %d (only files and directories)", hdr.Name, hdr.Typeflag)
		}
	}
	src := tmp
	if entries, err := os.ReadDir(tmp); err == nil && len(entries) == 1 && entries[0].IsDir() {
		src = filepath.Join(tmp, entries[0].Name())
	}
	if err := os.Rename(src, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}
