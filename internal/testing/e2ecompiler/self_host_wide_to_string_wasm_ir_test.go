package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const wideToStringProg = `function main(): i32 {
    let a: i64 = 1234567890123 as i64;
    let b: i64 = (0 as i64) - (9223372036854775807 as i64) - (1 as i64);
    let c: u64 = 18446744073709551615 as u64;
    let z: i64 = 0 as i64;
    let s: string = a.to_string() + "|" + b.to_string() + "|" + c.to_string() + "|" + z.to_string();
    write(s + "\n");
    return 0;
}
`

const wideImports = "import \"std/i32\";\nimport \"std/i64\";\nimport \"std/u64\";\n"

func TestSelfHostWideToStringWasmIR(t *testing.T) {
	dir := t.TempDir()
	l := newWasmStdlibLoader(t)

	emit := func(t *testing.T, src string) string {
		t.Helper()
		return string(l.emit(t, wideImports+src))
	}

	runWAT := func(t *testing.T, name, wat string) string {
		t.Helper()
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
		watFile := filepath.Join(dir, "wide_"+name+".wat")
		if err := os.WriteFile(watFile, []byte(wat), 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		out, err := exec.Command("wasmtime", "run", watFile).Output()
		if err != nil {
			t.Fatalf("wasmtime run %s: %v", name, err)
		}
		return string(out)
	}

	t.Run("digits", func(t *testing.T) {
		const want = "1234567890123|-9223372036854775808|18446744073709551615|0\n"
		if got := runWAT(t, "digits", emit(t, wideToStringProg)); got != want {
			t.Errorf("wide to_string = %q, want %q", got, want)
		}
	})

	t.Run("fstring", func(t *testing.T) {
		src := `function main(): i32 {
    let n: i64 = 42000000000 as i64;
    write(f"n={n}\n");
    return 0;
}
`
		if got := runWAT(t, "fstring", emit(t, src)); got != "n=42000000000\n" {
			t.Errorf("f-string i64 = %q, want %q", got, "n=42000000000\n")
		}
	})

	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{
			name: "i64-only",
			src:  "function main(): i32 { let n: i64 = 7 as i64; write(n.to_string() + \"\\n\"); return 0; }",
			want: "7\n",
		},
		{
			name: "u64-only",
			src:  "function main(): i32 { let n: u64 = 9 as u64; write(n.to_string() + \"\\n\"); return 0; }",
			want: "9\n",
		},
		{
			name: "i32-only",
			src:  "function main(): i32 { let n: i32 = 5; write(n.to_string() + \"\\n\"); return 0; }",
			want: "5\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runWAT(t, tc.name, emit(t, tc.src)); got != tc.want {
				t.Errorf("%s = %q, want %q", tc.name, got, tc.want)
			}
		})
	}

	t.Run("zero-churn", func(t *testing.T) {
		src := `function main(): i32 {
    let n: i64 = 0 as i64;
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 20000) {
        let s: string = n.to_string();
        total = (total + s.len()) % 251;
        i = i + 1;
    }
    write(total.to_string() + "\n");
    return 0;
}
`
		if got := runWAT(t, "zero-churn", emit(t, src)); got != "171\n" {
			t.Errorf("i64 zero churn = %q, want %q", got, "171\n")
		}
	})
}
