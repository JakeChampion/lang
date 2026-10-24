package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSelfHostX86Capstone(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("native x86-64 run requires an amd64 host")
	}
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host x86 capstone")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/wasm_run.fern")
	asmRun := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "asm_run")
	wasmRun := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	nat := mustRead(t, "../../../compiler/x86_native.fern")
	elf := mustRead(t, "../../../compiler/elf.fern")
	prelude := string(nat) + "\n" + string(elf) + toU8Src

	driverWat := runCapture(t, gcc, runner, wasmRun, []byte(prelude+x86CapstoneDriver))
	if len(driverWat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the capstone driver")
	}
	driverPath := filepath.Join(dir, "capstone_driver.wat")
	if err := os.WriteFile(driverPath, driverWat, 0o644); err != nil {
		t.Fatalf("write driver wat: %v", err)
	}

	cases := []struct {
		name    string
		prog    string
		want    int
		wantOut string
	}{
		{"arith", "function main(): i32 { let x: i32 = 40; let y: i32 = 2; return x + y; }\n", 42, ""},
		{"while", "function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < 7) { s = s + 6; i = i + 1; } return s; }\n", 42, ""},
		{"ifelse", "function main(): i32 { let x: i32 = 10; if (x > 5) { return 42; } return 0; }\n", 42, ""},
		{"call", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(40, 2); }\n", 42, ""},
		{"recur", "function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }\nfunction main(): i32 { return fib(9) + 8; }\n", 42, ""},
		{"float", "function main(): i32 { let x: f64 = 84.0; let y: f64 = 2.0; let z: f64 = x / y; return z as i32; }\n", 42, ""},
		{"string", "function main(): i32 { write(\"hi!\"); return 0; }\n", 0, "hi!"},
		{"struct", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p = P { x: 40, y: 2 }; return p.x + p.y; }\n", 42, ""},
		{"array", "function main(): i32 { let a = [10, 20, 12]; let s = 0; let i = 0; while (i < 3) { s = s + a[i]; i = i + 1; } return s; }\n", 42, ""},
		{"strlen", "function main(): i32 { let s = \"hello world\"; return s.len() as i32 + 31; }\n", 42, ""},
		{"strchar", "function main(): i32 { let s = \"*abc\"; return s[0] as i32; }\n", 42, ""},
		{"memchr", "function main(): i32 { let s = \"aaaaaaaaaaaaaaaaaaaa*aaa\"; return __memchr(s, 42, 0) + 22; }\n", 42, ""},
		{"asciirun", "function main(): i32 { let s = \"aaaaaaaaaaaaaaaaaaaaaaaa\"; return __ascii_run(s, 0) + 18; }\n", 42, ""},
		{"rmemchr", "function main(): i32 { let s = \"aa*aaaaaaaaaaaaaaaaaaaaa\"; return __rmemchr(s, 42, 23) + 40; }\n", 42, ""},
		{"mismatch", "function main(): i32 { let a = \"aaaaaaaaaaaaaaaaaaaabaaa\"; let b = \"aaaaaaaaaaaaaaaaaaaacaaa\"; return __mismatch(a, 0, b, 0, 24) + 22; }\n", 42, ""},
		{"mismatchwindow", "function main(): i32 { let a = \"abcdefghijklm\"; let b = \"abcdefghijXlm\"; return __mismatch(a, 0, b, 0, 13) + 32; }\n", 42, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asmText := runCapture(t, gcc, runner, asmRun, []byte(tc.prog))
			if len(asmText) == 0 {
				t.Fatal("asm.fern produced no assembly")
			}
			if err := os.WriteFile(filepath.Join(dir, "in.s"), asmText, 0o644); err != nil {
				t.Fatalf("write asm: %v", err)
			}
			bin, err := exec.Command(wasmtime, "run", "--dir", dir+"::/", driverPath).Output()
			if err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					t.Fatalf("wasmtime run (driver): %v\n%s", err, ee.Stderr)
				}
				t.Fatalf("wasmtime run (driver): %v", err)
			}
			if len(bin) < 4 || bin[0] != 0x7f || bin[1] != 'E' || bin[2] != 'L' || bin[3] != 'F' {
				t.Fatalf("output is not an ELF (bad magic): % x\n--- asm ---\n%s", bin[:min(4, len(bin))], asmText)
			}
			binPath := filepath.Join(dir, tc.name+".bin")
			if err := os.WriteFile(binPath, bin, 0o755); err != nil {
				t.Fatalf("write binary: %v", err)
			}
			stdout, runErr := exec.Command(binPath).Output()
			got := 0
			if runErr != nil {
				ee, ok := runErr.(*exec.ExitError)
				if !ok {
					t.Fatalf("run failed (not an exit code): %v\n--- asm ---\n%s", runErr, asmText)
				}
				got = ee.ExitCode()
			}
			if got != tc.want {
				t.Fatalf("exit code = %d, want %d\n--- asm ---\n%s", got, tc.want, asmText)
			}
			if tc.wantOut != "" && string(stdout) != tc.wantOut {
				t.Fatalf("stdout = %q, want %q\n--- asm ---\n%s", string(stdout), tc.wantOut, asmText)
			}
		})
	}
}

const x86CapstoneDriver = `
function main(): i32 {
    match (read_file("in.s")) {
        Ok(asmtext) => {
            let a: X86Asm = x86_gas_assemble(asmtext);
            // An absolute .quad <label> in data is assembled as a zero
            // placeholder and patched only once the load address is known, so
            // this step is not optional — skipping it leaves every static
            // string box pointing at address 0. x86_elf_binary (fern.fern) is
            // the pipeline this mirrors, including the order: the unknown
            // check comes AFTER, because an unresolvable data symbol is
            // recorded here rather than by the assembler.
            a = x86_resolve_data(a, elf_text_vaddr_x86());
            if (a.unknown.len() > 0) {
// Naming the lines is the whole point of this probe: a bare
                // exit code says an instruction was refused but not which
                // one. Several distinct causes can be live at once, so this
                // reports the first few rather than only the first.
                let i: i32 = 0;
                let msg: string = "";
                while (i < a.unknown.len() && i < 8) {
                    msg = msg + "unencodable: " + a.unknown[i] + "\n";
                    i = i + 1;
                }
                eprint(msg);
                return 2;
            }
            let entry: i32 = x86_label_off(a, "_start");
            let tv: i64 = elf_text_vaddr_x86() as i64;
            let hdr_len: i32 = x86_eh_frame_hdr_len(a);
            let hv: i64 = elf_eh_hdr_vaddr_x86(a.text.len()) as i64;
            let ev: i64 = elf_eh_frame_vaddr_x86(a.text.len(), hdr_len) as i64;
            let eh: i32[] = x86_eh_frame(a, tv, ev);
            let hdr: i32[] = x86_eh_frame_hdr(a, tv, ev, hv);
            write(string_from_bytes_unchecked(elf_program_x86(a.text, hdr, eh, a.rodata, a.bss_size, entry)));
            return 0;
        }
    }
    return 1;
}
`

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
