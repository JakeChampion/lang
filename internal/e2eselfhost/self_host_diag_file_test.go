package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// A diagnostic from an imported module names its file, as native's does
// (#9523). Without one, `1:27` in a sibling module or a stdlib module read as
// a position in the file being compiled. The entry's own diagnostics keep the
// bare `line:col`.

var diagPosRE = regexp.MustCompile(`^(?:(\S+):)?(\d+):(\d+): error\[(E\d{3})\]`)

// diagPositions reduces a `-check` transcript to `file:line:col CODE` lines,
// with the entry's path dropped the way the self-host prints it.
func diagPositions(out, entry string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		m := diagPosRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		file := m[1]
		if file == entry {
			file = ""
		}
		got = append(got, file+":"+m[2]+":"+m[3]+" "+m[4])
	}
	sort.Strings(got)
	return got
}

// diagFullLines is diagPositions keeping each diagnostic's whole line.
func diagFullLines(out, entry string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if diagPosRE.MatchString(line) {
			got = append(got, strings.TrimPrefix(line, entry+":"))
		}
	}
	sort.Strings(got)
	return got
}

func selfHostCheckDriver(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern"), nil
	}
	gcc, runner := x86_64Tooling(t)
	return buildSelfHostBin(t, gcc, dir, "fern.fern", "fern"), runner
}

func TestSelfHostDiagnosticNamesItsFile(t *testing.T) {
	driver, runner := selfHostCheckDriver(t)
	native := buildLangBinForInterp(t)
	root, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		files map[string]string
		// exact compares whole diagnostic lines, message text included.
		exact bool
	}{
		{
			// One error in the entry and one at the same line in a sibling, so
			// only the file tells them apart.
			name: "sibling",
			files: map[string]string{
				"main.fern": "import \"./lib\";\nfunction main(): i32 {\n    var x: i32 = \"s\"; return lib.bad(); }\n",
				"lib.fern":  "pub function bad(): i32 {\n    return \"x\";\n}\n",
			},
		},
		{
			// E051 is anchored at the argument, not the call.
			name: "owned_argument",
			files: map[string]string{
				"main.fern": "function take(own s: string): i32 { return s.len(); }\n" +
					"function f(a: i32, s: string): i32 {\n    return take(  s);\n}\n" +
					"function main(): i32 { return f(1, \"x\"); }\n",
			},
		},
		{
			// An index argument carries its own position, the `[`, so E051
			// names the argument's line in a call spread over two.
			name: "owned_index_argument",
			files: map[string]string{
				"main.fern": "function take(own s: string): i32 { return s.len(); }\n" +
					"function f(xs: string[]): i32 {\n    return take(\n        xs[0]);\n}\n" +
					"function main(): i32 { return f([\"a\"]); }\n",
			},
		},
		{
			// E050 quotes the line of the move. In a sibling module that line
			// is decoded like the position is.
			name:  "sibling_moved_at",
			exact: true,
			files: map[string]string{
				"main.fern": "import \"./lib\";\nfunction main(): i32 { return lib.f(\"x\"); }\n",
				"lib.fern": "pub function take(own s: string): i32 { return s.len(); }\n" +
					"pub function f(own s: string): i32 {\n    var n: i32 = take(s);\n    return n + s.len();\n}\n",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			entry := filepath.Join(dir, "main.fern")
			nout, _ := exec.Command(native, "-check", entry).CombinedOutput()
			cmd := runX86_64Bin(runner, driver)
			cmd.Args = append(cmd.Args, "-check", entry, root)
			sout, _ := cmd.CombinedOutput()
			want := diagPositions(string(nout), entry)
			got := diagPositions(string(sout), entry)
			if tc.exact {
				want, got = diagFullLines(string(nout), entry), diagFullLines(string(sout), entry)
			}
			if len(want) == 0 {
				t.Fatalf("native reported nothing — the probe is not exercising the path:\n%s", nout)
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("positions differ\nnative:\n%s\nself-host:\n%s\n\nnative output:\n%s\nself-host output:\n%s",
					strings.Join(want, "\n"), strings.Join(got, "\n"), nout, sout)
			}
		})
	}
}

// Native reads its stdlib from the binary, so a stdlib module carrying a
// diagnostic can only be staged for the self-host, which takes the root as an
// argument. Native's form for one is `stdlib://std/bad.fern:2:5`.
func TestSelfHostStdlibDiagnosticNamesItsFile(t *testing.T) {
	driver, runner := selfHostCheckDriver(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "std"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "std", "bad.fern"), []byte("pub function bad(): i32 {\n    return \"x\";\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(entry, []byte("import \"std/bad\";\nfunction main(): i32 { return bad.bad(); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{root, root + "/"} {
		cmd := runX86_64Bin(runner, driver)
		cmd.Args = append(cmd.Args, "-check", entry, r)
		out, _ := cmd.CombinedOutput()
		got := diagPositions(string(out), entry)
		if want := "stdlib://std/bad.fern:2:5 E002"; strings.Join(got, "\n") != want {
			t.Errorf("root %q: got %q, want %q\n%s", r, got, want, out)
		}
	}
}
