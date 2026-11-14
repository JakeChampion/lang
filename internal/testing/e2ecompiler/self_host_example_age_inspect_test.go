package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ageInspectReport is the last line of each age-inspect error, which the
// port does not write.
const ageInspectReport = "age-inspect: report unexpected or unhelpful errors at https://filippo.io/age/report\n"

// TestSelfHostExampleAgeInspect runs the age-inspect port and age-inspect
// 1.3.2 on the same files: the 147 CCTV vectors, files that age encrypts in
// the binary format and in the armor, on the standard input and as a file,
// and bad command lines. Each tool gives the same output, message and exit
// code.
func TestSelfHostExampleAgeInspect(t *testing.T) {
	oracle, keygen := ageOracle(t)
	bin, err := exec.LookPath("age-inspect")
	if err != nil {
		t.Fatalf("age-inspect is required: it is in the release archive of age %s", ageOracleVersion)
	}
	want := ageTool{name: "age-inspect", cmd: func(args ...string) *exec.Cmd { return exec.Command(bin, args...) }}
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	r1 := ageKey(t, keygen, filepath.Join(dir, "k1"))
	pq := ageHybridKey(t, keygen, filepath.Join(dir, "pq"))
	var files []string
	for _, v := range ageVectors(t) {
		ageWrite(t, filepath.Join(dir, "cctv-"+v.name), v.file)
		files = append(files, "cctv-"+v.name)
	}
	for _, size := range []int{0, 1, 65536, 65537, 1<<20 + 3} {
		for _, args := range [][]string{{"-r", r1}, {"-r", r1, "-a"}, {"-r", pq, "-a"}, {"-r", r1, "-r", r1}, {"-r", ageTagClassic}} {
			sealed, stderr, code := oracle.run(t, dir, ageRandom(t, size), args...)
			if code != 0 {
				t.Fatalf("age %v: exit %d: %s", args, code, stderr)
			}
			name := fmt.Sprintf("f%d-%d", size, len(files))
			ageWrite(t, filepath.Join(dir, name), sealed)
			files = append(files, name)
			if size == 65537 {
				ageWrite(t, filepath.Join(dir, name+"-cut"), sealed[:len(sealed)-1])
				ageWrite(t, filepath.Join(dir, name+"-long"), append(append([]byte{}, sealed...), 'x'))
				files = append(files, name+"-cut", name+"-long")
			}
		}
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			port := ageProgram(t, cli, target, "inspect.fern")
			run := func(tool ageTool, stdin []byte, args ...string) ([]byte, string, int) {
				out, msg, code := tool.run(t, dir, stdin, args...)
				return out, strings.ReplaceAll(msg, ageInspectReport, ""), code
			}
			var cases [][]string
			for _, f := range files {
				cases = append(cases, []string{f}, []string{"--json", f})
			}
			cases = append(cases, []string{"missing"}, []string{"a", "b"}, []string{"-h"}, []string{"-x"}, []string{"--json=maybe", files[0]}, []string{dir})
			for _, args := range cases {
				wantOut, wantMsg, wantCode := run(want, nil, args...)
				gotOut, gotMsg, gotCode := run(port, nil, args...)
				if gotCode != wantCode || gotMsg != wantMsg || !bytes.Equal(gotOut, wantOut) {
					t.Errorf("age-inspect %v: age exit %d %q %q; fern exit %d %q %q", args, wantCode, wantMsg, wantOut, gotCode, gotMsg, gotOut)
				}
			}
			for _, f := range files[len(files)-6:] {
				content, err := os.ReadFile(filepath.Join(dir, f))
				if err != nil {
					t.Fatal(err)
				}
				for _, args := range [][]string{nil, {"-"}, {"--json", "-"}} {
					wantOut, wantMsg, wantCode := run(want, content, args...)
					gotOut, gotMsg, gotCode := run(port, content, args...)
					if gotCode != wantCode || gotMsg != wantMsg || !bytes.Equal(gotOut, wantOut) {
						t.Errorf("age-inspect %v < %s: age exit %d %q %q; fern exit %d %q %q", args, f, wantCode, wantMsg, wantOut, gotCode, gotMsg, gotOut)
					}
				}
			}
		})
	}
}
