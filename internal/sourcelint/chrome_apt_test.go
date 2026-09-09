package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDisableChromeAptSources(t *testing.T) {
	script, err := filepath.Abs("../../.github/actions/disable-chrome-apt/disable.sh")
	if err != nil {
		t.Fatal(err)
	}
	const ubuntu = "deb http://archive.ubuntu.com/ubuntu noble main\n"
	const chrome = "https://dl.google.com/linux/chrome-stable/deb/"
	const signed = "Signed-By: /usr/share/keyrings/example.gpg\n"
	cases := []struct {
		name, path, source, want string
	}{
		{"legacy-main", "sources.list", "deb [arch=amd64 signed-by=/key] http://dl.google.com/linux/chrome/deb/ stable main\n" + ubuntu, ubuntu},
		{"arbitrary-list-name", "sources.list.d/browser.list", ubuntu + "deb-src " + chrome + " stable main\n", ubuntu},
		{"dedicated-deb822", "sources.list.d/browser.sources", "Types: deb\nURIs: " + chrome + "\nSuites: stable\nComponents: main\n" + signed, ""},
		{"mixed-stanzas", "sources.list.d/mixed.sources", "Types: deb\nURIs: " + chrome + "\nSuites: stable\n\nTypes: deb\nURIs: http://archive.ubuntu.com/ubuntu\nSuites: noble\n" + signed, "Types: deb\nURIs: http://archive.ubuntu.com/ubuntu\nSuites: noble\n" + signed + "\n"},
		{"mixed-uris", "sources.list.d/mixed.sources", "Types: deb\nURIs: " + chrome + " http://archive.ubuntu.com/ubuntu\nSuites: noble\n" + signed, "Types: deb\nURIs: http://archive.ubuntu.com/ubuntu\nSuites: noble\n" + signed + "\n"},
		{"continued-uris", "sources.list.d/mixed.sources", "Types: deb\nURIs: " + chrome + "\n http://archive.ubuntu.com/ubuntu\nSuites: noble\n" + signed, "Types: deb\nURIs:\n http://archive.ubuntu.com/ubuntu\nSuites: noble\n" + signed + "\n"},
		{"chrome-continuation", "sources.list.d/mixed.sources", "Types: deb\nURIs: http://archive.ubuntu.com/ubuntu\n " + chrome + "\nSuites: noble\n" + signed, "Types: deb\nURIs: http://archive.ubuntu.com/ubuntu\nSuites: noble\n" + signed + "\n"},
		{"unrelated-host", "sources.list.d/keep.list", "deb https://dl.google.com.example/linux/chrome-stable/deb/ stable main\n", "deb https://dl.google.com.example/linux/chrome-stable/deb/ stable main\n"},
		{"comment-only", "sources.list.d/keep.list", "# deb " + chrome + " stable main\n" + ubuntu, "# deb " + chrome + " stable main\n" + ubuntu},
		{"unrelated-deb822", "sources.list.d/keep.sources", "Types: deb\nURIs: http://archive.ubuntu.com/ubuntu\nSuites: noble\n" + signed, "Types: deb\nURIs: http://archive.ubuntu.com/ubuntu\nSuites: noble\n" + signed},
		{"ignored-extension", "sources.list.d/keep.disabled", "deb " + chrome + " stable main\n", "deb " + chrome + " stable main\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "sources.list.d"), 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, tc.path)
			if err := os.WriteFile(path, []byte(tc.source), 0o640); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if out, err := exec.Command("bash", script, root).CombinedOutput(); err != nil {
					t.Fatalf("cleanup: %v\n%s", err, out)
				}
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tc.want {
					t.Fatalf("sources:\n%s\nwant:\n%s", got, tc.want)
				}
			}
			if stat, err := os.Stat(path); err != nil || stat.Mode().Perm() != 0o640 {
				t.Fatalf("source permissions changed: %v, %v", stat, err)
			}
		})
	}
}

func TestDisableChromeAptKeepsUbuntuTargets(t *testing.T) {
	apt, err := exec.LookPath("apt-get")
	if err != nil {
		t.Skip("apt-get is only available on apt-based Linux hosts")
	}
	root := t.TempDir()
	parts := filepath.Join(root, "sources.list.d")
	if err := os.MkdirAll(parts, 0o755); err != nil {
		t.Fatal(err)
	}
	const mixed = "Types: deb\nURIs: https://dl.google.com/linux/chrome-stable/deb/\n http://archive.ubuntu.com/ubuntu\nSuites: noble\nComponents: main\n"
	if err := os.WriteFile(filepath.Join(parts, "mixed.sources"), []byte(mixed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sources.list"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	targets := func() string {
		t.Helper()
		cmd := exec.Command(apt, "--print-uris",
			"-o", "Dir::Etc::sourcelist="+filepath.Join(root, "sources.list"),
			"-o", "Dir::Etc::sourceparts="+parts,
			"-o", "Dir::State::lists="+filepath.Join(root, "lists"), "update")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("print apt targets without fetching: %v\n%s", err, out)
		}
		return string(out)
	}
	before := targets()
	if !strings.Contains(before, "dl.google.com/linux/chrome-stable") || !strings.Contains(before, "archive.ubuntu.com/ubuntu") {
		t.Fatalf("fixture did not configure both repositories: %s", before)
	}
	script, err := filepath.Abs("../../.github/actions/disable-chrome-apt/disable.sh")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", script, root).CombinedOutput(); err != nil {
		t.Fatalf("cleanup: %v\n%s", err, out)
	}
	after := targets()
	if strings.Contains(after, "dl.google.com/linux/chrome-stable") || !strings.Contains(after, "archive.ubuntu.com/ubuntu") {
		t.Fatalf("cleanup lost Ubuntu or kept Chrome targets: %s", after)
	}
}
