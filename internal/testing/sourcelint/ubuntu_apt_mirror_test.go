package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPreferOfficialUbuntuAptMirror(t *testing.T) {
	script, err := filepath.Abs("../../../.github/actions/disable-chrome-apt/prefer-ubuntu-mirror.sh")
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]struct{ before, after string }{
		"sources.list": {
			"deb [arch=amd64 signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg] http://azure.archive.ubuntu.com/ubuntu noble main\n",
			"deb [arch=amd64 signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg] https://archive.ubuntu.com/ubuntu noble main\n",
		},
		"sources.list.d/ubuntu.sources": {
			"Types: deb\nURIs: https://azure.archive.ubuntu.com/ubuntu/\n https://security.ubuntu.com/ubuntu\nSuites: noble noble-updates\nComponents: main universe\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n",
			"Types: deb\nURIs: https://archive.ubuntu.com/ubuntu/\n https://security.ubuntu.com/ubuntu\nSuites: noble noble-updates\nComponents: main universe\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n",
		},
		"apt-mirrors.txt": {
			"# http://azure.archive.ubuntu.com/ubuntu\nhttp://azure.archive.ubuntu.com/ubuntu priority:1\nhttps://archive.ubuntu.com/ubuntu priority:2\n",
			"# http://azure.archive.ubuntu.com/ubuntu\nhttps://archive.ubuntu.com/ubuntu priority:1\nhttps://archive.ubuntu.com/ubuntu priority:2\n",
		},
		"sources.list.d/unchanged.list": {
			"deb https://packages.microsoft.com/ubuntu/24.04/prod noble main\ndeb http://ports.ubuntu.com/ubuntu-ports noble main\ndeb https://azure.archive.ubuntu.com.example/ubuntu noble main\ndeb http://azure.archive.ubuntu.com/ubuntu-extra noble main\n",
			"deb https://packages.microsoft.com/ubuntu/24.04/prod noble main\ndeb http://ports.ubuntu.com/ubuntu-ports noble main\ndeb https://azure.archive.ubuntu.com.example/ubuntu noble main\ndeb http://azure.archive.ubuntu.com/ubuntu-extra noble main\n",
		},
	}
	root := t.TempDir()
	for name, fixture := range fixtures {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(fixture.before), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for pass := 0; pass < 2; pass++ {
		if out, err := exec.Command("bash", script, root).CombinedOutput(); err != nil {
			t.Fatalf("pass %d: %v\n%s", pass, err, out)
		}
		for name, fixture := range fixtures {
			got, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != fixture.after {
				t.Errorf("pass %d, %s:\ngot %q\nwant %q", pass, name, got, fixture.after)
			}
		}
	}
}
