package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostConstRefCheckX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("check differential runs only natively (argv paths)")
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
		{"const-i32-returned", "const N: i32 = 41;\nfunction main(): i32 { return N - 41; }\n"},
		{"const-i32-in-arith", "const N: i32 = 41;\nfunction main(): i32 { let x: i32 = N * 2 - 82; return x; }\n"},
		{"const-i32-as-argument", "const N: i32 = 41;\nfunction take(x: i32): i32 { return x - 41; }\nfunction main(): i32 { return take(N); }\n"},
		{"const-boolean-in-condition", "const B: boolean = true;\nfunction main(): i32 { if (B) { return 0; } return 1; }\n"},
		{"const-f64-in-arith", "const F: f64 = 1.5;\nfunction main(): i32 { let g: f64 = F + 0.5; if (g > 1.9) { return 0; } return 1; }\n"},
		{"const-reads-earlier-const", "const A: i32 = 2;\nconst B: i32 = A * 3;\nfunction main(): i32 { return B - 6; }\n"},
		{"zero-arg-function-unaffected", "function z(): i32 { return 1; }\nfunction main(): i32 { return z() - 1; }\n"},
		{"const-outside-the-grammar", "struct Cfg { n: i32 }\nfunction f(): i32 { return 1; }\nconst C: Cfg = Cfg { n: f() };\nfunction main(): i32 { return C.n; }\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, "cref_"+c.name+".fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			nativeCmd := exec.Command(nativeBin, "-check", src)
			nativeOut, _ := nativeCmd.CombinedOutput()
			shCmd := exec.Command(driverBin, "-check", src, stdlib)
			shOut, _ := shCmd.CombinedOutput()

			nativeOK := nativeCmd.ProcessState.ExitCode() == 0
			shOK := shCmd.ProcessState.ExitCode() == 0
			if nativeOK != shOK {
				t.Errorf("native accepts = %v, self-host accepts = %v\n--- native ---\n%s\n--- self-host ---\n%s",
					nativeOK, shOK, nativeOut, shOut)
			}
		})
	}
}
