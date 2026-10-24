package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostAmbiguousMethodRankedByImports(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)
	fern := buildFernCLIBin(t)
	files := map[string]string{
		"shape.fern": "pub struct P { v: i32 }\n",
		"a.fern":     "import \"./shape\";\npub trait A { function scale(self: Self): i32; }\nimpl A for shape.P { function scale(self: Self): i32 { return self.v; } }\n",
		"b.fern":     "import \"./shape\";\npub trait B { function scale(self: Self): i32; }\nimpl B for shape.P { function scale(self: Self): i32 { return self.v + 1; } }\n",
		"ab.fern":    "import \"./a\";\nimport \"./b\";\npub function both(): i32 { return 0; }\n",
	}
	cases := []struct {
		name string
		main string
		want bool
	}{
		{"direct-import-outranks", "import \"./shape\";\nimport \"./a\";\nimport \"./ab\";\nfunction main(): i32 { let p: shape.P = shape.P { v: 3 }; return p.scale() + ab.both(); }\n", false},
		{"two-direct-imports-tie", "import \"./shape\";\nimport \"./a\";\nimport \"./b\";\nfunction main(): i32 { let p: shape.P = shape.P { v: 3 }; return p.scale(); }\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			entry := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(entry, []byte(c.main), 0o644); err != nil {
				t.Fatal(err)
			}
			goOut, _ := exec.Command(fern, "-check", entry).CombinedOutput()
			if got := strings.Contains(string(goOut), "E074"); got != c.want {
				t.Errorf("Go checker: E074 = %v, want %v\n%s", got, c.want, goOut)
			}
			shOut := checkFileModload(t, runner, driverBin, entry)
			if got := strings.Contains(shOut, "E074"); got != c.want {
				t.Errorf("self-host checker: E074 = %v, want %v\n%s", got, c.want, shOut)
			}
		})
	}
}
