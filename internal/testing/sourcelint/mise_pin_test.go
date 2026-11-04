package sourcelint

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMiseActionPinsTheMiseRelease(t *testing.T) {
	toml, err := os.ReadFile(filepath.Join("..", "..", "..", "mise.toml"))
	if err != nil {
		t.Fatalf("read mise.toml: %v", err)
	}
	m := regexp.MustCompile(`(?m)^FERN_MISE_VERSION = "([^"]+)"`).FindSubmatch(toml)
	if m == nil {
		t.Fatal("mise.toml has no FERN_MISE_VERSION under [env]; the mise release is pinned there")
	}
	if regexp.MustCompile(`(?m)^min_version = `).Match(toml) {
		t.Error("mise.toml declares min_version again; the release pin belongs in [env] FERN_MISE_VERSION, see #8423")
	}
	want := string(m[1])

	steps := 0
	err = filepath.WalkDir(filepath.Join("..", "..", "..", ".github"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yml") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(b), "\n")
		for i, l := range lines {
			if !strings.Contains(l, "uses: jdx/mise-action@") {
				continue
			}
			steps++
			got := ""
			indent := len(l) - len(strings.TrimLeft(l, " "))
			for _, w := range lines[i+1:] {
				if strings.TrimSpace(w) == "" {
					break
				}
				if len(w)-len(strings.TrimLeft(w, " ")) <= indent && strings.TrimSpace(w) != "with:" {
					break
				}
				if v, ok := strings.CutPrefix(strings.TrimSpace(w), "version:"); ok {
					got = strings.TrimSpace(v)
				}
			}
			if got != want {
				t.Errorf("%s:%d: jdx/mise-action step passes version %q, mise.toml FERN_MISE_VERSION is %q", path, i+1, got, want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk .github: %v", err)
	}
	if steps == 0 {
		t.Fatal("no jdx/mise-action step found under .github; the toolchain is installed through it")
	}
}

// mise lock downloads an http: tool only for the platform it runs on, and
// mise install --locked refuses a platform with no checksum.
func TestMiseLockChecksumsEveryHTTPPlatform(t *testing.T) {
	lock, err := os.ReadFile(filepath.Join("..", "..", "..", "mise.lock"))
	if err != nil {
		t.Fatalf("read mise.lock: %v", err)
	}
	entry := regexp.MustCompile(`(?m)^\[tools\."(http:[^"]+)"\."platforms\.([^"]+)"\]$`)
	locs := entry.FindAllSubmatchIndex(lock, -1)
	if len(locs) == 0 {
		t.Fatal("mise.lock holds no http: tool platform entry")
	}
	for _, at := range locs {
		body := string(lock[at[1]:])
		if next := strings.Index(body, "\n["); next >= 0 {
			body = body[:next]
		}
		if strings.Contains(body, "\nurl = ") && !strings.Contains(body, "\nchecksum = ") {
			t.Errorf("mise.lock: %s on %s has a url and no checksum; run scripts/lock-http-checksums", lock[at[2]:at[3]], lock[at[4]:at[5]])
		}
	}
}
