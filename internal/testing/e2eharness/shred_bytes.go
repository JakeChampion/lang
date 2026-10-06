package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func RunShredByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "shred"
	if runtime.GOOS == "darwin" {
		name = "gshred"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		path := filepath.Join(dir, "shred")
		if info, e := os.Stat(path); e == nil && !info.IsDir() {
			oracle, err = path, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU shred")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU shred")
	}
	type testCase struct {
		name                string
		size, source, files int
		args                []string
		random              bool
	}
	var cases []testCase
	for _, size := range []int{0, 1, 2, 3, 31, 4095, 4096, 4097, 61439, 61440, 61441, 65535, 65536, 65537, 122879, 122880, 122881, 131071, 131072, 131073} {
		for _, args := range [][]string{{"-n1"}, {"-n2"}, {"-n0", "-z"}} {
			cases = append(cases, testCase{name: fmt.Sprintf("%d %s", size, strings.Join(args, " ")), size: size, source: size*3 + 61440, files: 1, args: args})
		}
	}
	for _, size := range []int{1, 61441, 122881} {
		cases = append(cases, testCase{name: fmt.Sprintf("CSPRNG then zero %d", size), size: size, files: 1, args: []string{"-n1", "-z"}, random: true})
	}
	for _, size := range []int{1, 61440, 61441, 65536, 65537, 122881, 131073} {
		cases = append(cases, testCase{name: fmt.Sprintf("short source %d", size), size: size, source: size - 1, files: 1, args: []string{"-n1"}})
	}
	cases = append(cases, testCase{name: "source spans files", size: 61441, source: 4 * 61441, files: 2, args: []string{"-n1"}})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := make([]byte, tc.source)
			state := uint32(0x1234567)
			for i := range source {
				state = state*1664525 + 1013904223
				source[i] = byte(state >> 24)
			}
			run := func(prefix []string) (string, string, int, [][]byte) {
				dir := t.TempDir()
				args := []string{"-x"}
				args = append(args, tc.args...)
				if !tc.random {
					if err := os.WriteFile(filepath.Join(dir, "source"), source, 0o644); err != nil {
						t.Fatal(err)
					}
					args = append(args, "--random-source=source")
				}
				for i := 0; i < tc.files; i++ {
					name := fmt.Sprintf("target-%d", i)
					if err := os.WriteFile(filepath.Join(dir, name), bytes.Repeat([]byte{0xa5}, tc.size), 0o644); err != nil {
						t.Fatal(err)
					}
					args = append(args, name)
				}
				argv := append(append([]string{}, prefix...), args...)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
				cmd.Dir, cmd.Env = dir, append(os.Environ(), "LC_ALL=C")
				var out, diag bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &diag
				err := cmd.Run()
				if ctx.Err() != nil || cmd.ProcessState == nil || (err != nil && cmd.ProcessState.ExitCode() < 0) {
					t.Fatalf("run: %v\n%s", err, diag.String())
				}
				var files [][]byte
				for i := 0; i < tc.files; i++ {
					b, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("target-%d", i)))
					if err != nil {
						t.Fatal(err)
					}
					files = append(files, b)
				}
				return out.String(), diag.String(), cmd.ProcessState.ExitCode(), files
			}
			want, wantErr, wantCode, wantFiles := run([]string{oracle})
			got, diag, code, gotFiles := run(append(append([]string{}, runner...), bin))
			if code != wantCode || got != want {
				t.Fatalf("exit/stdout differ: %d/%d\n%s", code, wantCode, diag)
			}
			for i := range wantFiles {
				if !bytes.Equal(gotFiles[i], wantFiles[i]) {
					t.Fatalf("target %d differs: %d/%d bytes", i, len(gotFiles[i]), len(wantFiles[i]))
				}
			}
			if census != nil && code == 0 {
				census(t, diag)
			}
			var clean []string
			for _, line := range strings.SplitAfter(diag, "\n") {
				if strings.HasPrefix(line, "leakcheck:") || strings.HasPrefix(line, "fern-sanitizer: leak ") {
					if code != 0 {
						t.Log(strings.TrimSpace(line))
					}
					continue
				}
				clean = append(clean, line)
			}
			actual := strings.ReplaceAll(strings.Join(clean, ""), bin+":", "shred:")
			actual = strings.ReplaceAll(actual, filepath.Base(bin)+":", "shred:")
			wantErr = strings.ReplaceAll(wantErr, oracle+":", "shred:")
			wantErr = strings.ReplaceAll(wantErr, filepath.Base(oracle)+":", "shred:")
			if actual != wantErr {
				t.Fatalf("stderr differs:\ngot %q\nwant %q", actual, wantErr)
			}
		})
	}
}

// Compile the utility's actual private pattern function with a test entry.
func WriteShredPatternFixture(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	data, err := os.ReadFile(RepoPath("coreutils", "shred.fern"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	var fixture strings.Builder
	fixture.WriteString("import \"std/array\";\n")
	// These arithmetic/builder functions contain no quoted braces. Extract
	// their actual bodies without importing the CLI's unrelated fsmode need.
	for _, name := range []string{"base_patterns", "flip_first", "copy_bytes", "pattern_block"} {
		start := strings.Index(source, "function "+name+"(")
		if start < 0 {
			t.Fatalf("missing function %s", name)
		}
		brace := strings.Index(source[start:], "{") + start
		depth, end := 1, brace+1
		for depth > 0 && end < len(source) {
			if source[end] == '{' {
				depth++
			}
			if source[end] == '}' {
				depth--
			}
			end++
		}
		if depth != 0 {
			t.Fatalf("unterminated function %s", name)
		}
		fixture.WriteString(source[start:end])
		fixture.WriteByte('\n')
	}
	fixture.WriteString(`
function main(): i32 {
    let patterns = base_patterns();
    patterns = patterns.append(0xff00c3);
    for pattern in patterns {
        for p in [pattern, flip_first(pattern)] {
            for n in [0, 1, 2, 3, 4, 17, 61439, 61440, 61441] {
                let bytes = pattern_block(p, n);
                if (bytes.len() != n) { return 1; }
                let i: i32 = 0;
                while (i < n) {
                    let divisor: i32 = 1;
                    if (i % 3 == 0) { divisor = 65536; }
                    if (i % 3 == 1) { divisor = 256; }
                    if (bytes[i] as i32 != p / divisor % 256) { return 2; }
                    i = i + 1;
                }
            }
        }
    }
    return 0;
}
`)
	if err := os.WriteFile(filepath.Join(dir, "shred.fern"), []byte(fixture.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "shred.fern")
}
