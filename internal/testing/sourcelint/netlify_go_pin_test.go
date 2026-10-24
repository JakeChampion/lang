package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNetlifyGoPin(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}

	m := regexp.MustCompile(`(?m)^go = "([^"]+)"`).FindStringSubmatch(read("mise.toml"))
	if m == nil {
		t.Fatal("mise.toml has no top-level `go = \"...\"`")
	}
	want := m[1]

	n := regexp.MustCompile(`(?m)^\s*GO_VERSION = "([^"]+)"`).FindStringSubmatch(read("netlify.toml"))
	if n == nil {
		t.Fatal("netlify.toml has no GO_VERSION in [build.environment]")
	}
	got := n[1]

	if got != want && !strings.HasPrefix(got, want+".") {
		t.Errorf("netlify.toml GO_VERSION %q is not a release of mise.toml go %q — bump them together", got, want)
	}
}
