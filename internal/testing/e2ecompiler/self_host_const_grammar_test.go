package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostConstGrammarX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("const-grammar differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	for _, c := range []struct {
		name string
		src  string
	}{
		{"struct-literal-call-field", "struct Cfg { n: i32 }\nfunction f(): i32 { return 1; }\nconst C: Cfg = Cfg { n: f() };\nfunction main(): i32 { return C.n; }\n"},
		{"struct-update", "struct Cfg { n: i32, m: i32 }\nconst A: Cfg = Cfg { n: 1, m: 2 };\nconst C: Cfg = Cfg { ...A, n: 41 };\nfunction main(): i32 { return C.n; }\n"},
		{"call", "function f(): i32 { return 1; }\nconst C: i32 = f();\nfunction main(): i32 { return C; }\n"},
		{"cast-array-element", "const ZERO: u8[] = [0 as u8, 0 as u8];\nfunction main(): i32 { return ZERO[0] as i32; }\n"},
		{"cast-scalar", "const N: u8 = 7 as u8;\nfunction main(): i32 { return N as i32; }\n"},
		{"cast-earlier-const", "const A: i32 = 7;\nconst N: u8 = A as u8;\nfunction main(): i32 { return N as i32; }\n"},
		{"cast-arithmetic", "const N: i32 = (2.5 as i32) + 1;\nfunction main(): i32 { return N; }\n"},
		{"forward-reference", "const B: i32 = A + 1;\nconst A: i32 = 2;\nfunction main(): i32 { return B; }\n"},
		{"unknown-ident", "const B: i32 = ZZZ;\nfunction main(): i32 { return B; }\n"},
		{"saturating-add", "const B: u8 = 300 +| 1;\nfunction main(): i32 { return B as i32; }\n"},
		{"saturating-mul", "const B: i32 = 3 *| 2;\nfunction main(): i32 { return B; }\n"},
		{"saturating-shl", "const B: i32 = 3 <<| 2;\nfunction main(): i32 { return B; }\n"},
		{"checked-add", "const B: i32 = 3 +? 2;\nfunction main(): i32 { return B; }\n"},
		{"checked-div", "const B: i32 = 6 /? 2;\nfunction main(): i32 { return B; }\n"},

		{"struct-literal-ok", "struct Cfg { n: i32 }\nconst B: i32 = 40;\nconst C: Cfg = Cfg { n: B + 1 };\nfunction main(): i32 { return C.n; }\n"},
		{"array-literal-ok", "const XS: i32[] = [1, 2];\nfunction main(): i32 { return 0; }\n"},
		{"typed-array-literal-ok", "const XS: u8[] = [0u8, 7u8];\nfunction main(): i32 { return XS[1] as i32; }\n"},
		{"plain-literal-ok", "const N: i32 = 41;\nfunction main(): i32 { return N; }\n"},
		{"arith-over-earlier-const-ok", "const A: i32 = 2;\nconst B: i32 = A * 3 + 1;\nfunction main(): i32 { return B; }\n"},
		{"unary-ok", "const A: i32 = 0 - 5;\nfunction main(): i32 { return A; }\n"},
		{"string-ok", "const S: string = \"ab\";\nfunction main(): i32 { return S.len(); }\n"},
		{"bitwise-ok", "const A: i32 = (6 >> 1) & 3;\nfunction main(): i32 { return A; }\n"},
		{"logical-ok", "const A: boolean = true && false;\nfunction main(): i32 { if (A) { return 1; } return 0; }\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			nativeOut, _ := exec.Command(nativeBin, "-check", src).CombinedOutput()
			shOut, _ := exec.Command(driverBin, "-check", src, stdlib).CombinedOutput()

			wantConst := constDiagLines(string(nativeOut))
			gotConst := constDiagLines(string(shOut))
			if strings.Join(wantConst, "\n") != strings.Join(gotConst, "\n") {
				t.Errorf("const diagnostics differ\n--- native ---\n%s\n--- self-host ---\n%s",
					strings.Join(wantConst, "\n"), strings.Join(gotConst, "\n"))
			}
		})
	}
}

func constDiagLines(out string) []string {
	var keep []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, ": const ") {
			continue
		}
		if i := strings.Index(line, ": const "); i >= 0 {
			if j := strings.LastIndex(line[:i], ".fern:"); j >= 0 {
				line = line[j+len(".fern:"):]
			}
		}
		keep = append(keep, line)
	}
	return keep
}
