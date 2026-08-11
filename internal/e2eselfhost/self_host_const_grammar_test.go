package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Native defines a deliberately narrow constant grammar — literals, earlier
// consts, and arithmetic / comparison / logical operations on them. The
// self-host enforced none of it, so it admitted a strictly LARGER const
// language: `const C: Cfg = Cfg { n: 41 };` built here and not under native
// (#6618). That is the dangerous direction — the same source builds under one
// compiler and not the other, and nothing fails loudly.
//
// The diagnostic is UNCODED on both sides, so the checker-code differential
// cannot gate it: an uncoded diagnostic contributes no code, and a row there
// would pass whether or not the rule exists. This compares the message TEXT
// and position against native instead.
func TestSelfHostConstGrammarX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("const-grammar differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	for _, c := range []struct {
		name string
		src  string
	}{
		// Rejected — outside the grammar. Each is a distinct AST shape, so a
		// fix that only special-cased struct literals would fail the others.
		{"struct-literal", "struct Cfg { n: i32 }\nconst C: Cfg = Cfg { n: 41 };\nfunction main(): i32 { return C.n; }\n"},
		{"array-literal", "const XS: i32[] = [1, 2];\nfunction main(): i32 { return 0; }\n"},
		{"call", "function f(): i32 { return 1; }\nconst C: i32 = f();\nfunction main(): i32 { return C; }\n"},
		// Native words the non-earlier IDENT case differently, so these pin
		// that the second message is reproduced rather than folded into the
		// generic one.
		{"forward-reference", "const B: i32 = A + 1;\nconst A: i32 = 2;\nfunction main(): i32 { return B; }\n"},
		{"unknown-ident", "const B: i32 = ZZZ;\nfunction main(): i32 { return B; }\n"},
		// Accepted — what stops the rule being a blanket rejection of consts.
		{"plain-literal-ok", "const N: i32 = 41;\nfunction main(): i32 { return N; }\n"},
		{"arith-over-earlier-const-ok", "const A: i32 = 2;\nconst B: i32 = A * 3 + 1;\nfunction main(): i32 { return B; }\n"},
		{"unary-ok", "const A: i32 = 0 - 5;\nfunction main(): i32 { return A; }\n"},
		{"string-ok", "const S: string = \"ab\";\nfunction main(): i32 { return S.len(); }\n"},
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

// constDiagLines keeps only the const-grammar diagnostics, normalising away
// the leading file path native prints and the self-host does not. Other
// diagnostics are filtered out: a const-bearing program can also trip the
// self-host's #4346 "cannot represent yet" note, which is a separate gap and
// would otherwise mask this comparison.
func constDiagLines(out string) []string {
	var keep []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "is not a constant") {
			continue
		}
		// native: "<path>:2:1: const C: …"  self-host: "2:1: const C: …"
		if i := strings.Index(line, ": const "); i >= 0 {
			if j := strings.LastIndex(line[:i], ".fern:"); j >= 0 {
				line = line[j+len(".fern:"):]
			}
		}
		keep = append(keep, line)
	}
	return keep
}
