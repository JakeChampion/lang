package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The result outlives the heap-backed source. Churn allocations before reading
// it, so an uncounted element cannot hide behind unreused memory.
// Keep expected bytes independent of either compiler and measure the lifetime
// separately from value correctness.
func containerAliasStringSource(rebuild string, rounds int, shared bool) string {
	loadResult := "return rebuild(names);"
	if shared {
		loadResult = `var first: string[] = rebuild(names);
    var second: string[] = rebuild(names);
    print(first[0]);
    print(first[1]);
    return second;`
	}
	return `function rebuild(names: string[]): string[] {
    var out: string[] = [];
    ` + rebuild + `
    return out;
}
function load(): string[] {
    var names: string[] = [];
    names = names.append("aa" + "!");
    names = names.append("bb" + "!");
    ` + loadResult + `
}
function churn(): i32 {
    var junk: string[] = [];
    var i: i32 = 0;
    while (i < 16) {
        junk = junk.append("cc" + "?");
        i = i + 1;
    }
    return junk.len();
}
function exercise(): i32 {
    var i: i32 = 0;
    while (i < ` + fmt.Sprint(rounds) + `) {
        var a: string[] = load();
        if (churn() != 16) { return 98; }
        print(a[0]);
        print(a[1]);
        i = i + 1;
    }
    return 0;
}
function main(): i32 {
    var code: i32 = exercise();
    if (__rc_underflow_count() != 0) { return 99; }
    return code;
}`
}

var containerAliasLifetimeCases = []struct{ name, body string }{
	{"direct", `out = out.append(names[0]); out = out.append(names[1]);`},
	{"bound-elements", `var x = names[0]; var y = names[1]; out = out.append(x); out = out.append(y);`},
	{"foreach", `for x in names { out = out.append(x); }`},
	{"array-alias", `var alias = names; out = out.append(alias[0]); out = out.append(alias[1]);`},
	{"array-alias-bound-elements", `var alias = names; var x = alias[0]; var y = alias[1]; out = out.append(x); out = out.append(y);`},
	{"array-alias-foreach", `var alias = names; for x in alias { out = out.append(x); }`},
}

func TestSelfHostContainerAliasLifetimeArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	runContainerAliasLifetime(t, "arm64-linux", func(t *testing.T, asm string) string {
		return buildBinArm64(t, armgcc, t.TempDir(), "out", asm)
	}, func(bin string) *exec.Cmd { return runArm64Bin(qemu, bin) })
}

func TestSelfHostContainerAliasLifetimeX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	runContainerAliasLifetime(t, "x86-64-linux", func(t *testing.T, asm string) string {
		return buildBin(t, gcc, t.TempDir(), "out", asm)
	}, func(bin string) *exec.Cmd { return runX86_64Bin(runner, bin) })
}

func TestSelfHostContainerAliasLifetimeWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	for _, tc := range containerAliasLifetimeCases {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%t", tc.name, shared), func(t *testing.T) {
				// The second identical exercise must reuse the first one's freed
				// allocations. Check after both frames have released their arrays.
				src := containerAliasStringSource(tc.body, 32, shared)
				src = strings.Replace(src, "var code: i32 = exercise();", `var first: i32 = exercise();
    var before = __heap_bump_bytes();
    var code: i32 = exercise();
    var after = __heap_bump_bytes();
    if (first != 0) { return first; }
    if (after != before) { return 97; }`, 1)
				wat := runCapture(t, gcc, runner, driver, []byte(src), "-ir")
				path := filepath.Join(t.TempDir(), "out.wat")
				if err := os.WriteFile(path, wat, 0o644); err != nil {
					t.Fatal(err)
				}
				want := strings.Repeat("aa!\nbb!\n", 64)
				if shared {
					want += want
				}
				var stderr bytes.Buffer
				cmd := exec.Command("wasmtime", "run", path)
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				if err != nil || string(out) != want {
					t.Fatalf("self-host wasm: %v, got %q, want %q\n%s", err, out, want, stderr.String())
				}
			})
		}
	}
}

func runContainerAliasLifetime(t *testing.T, target string, link func(*testing.T, string) string, run func(string) *exec.Cmd) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
	native := buildLangBinForInterp(t)
	for _, tc := range containerAliasLifetimeCases {
		for _, shared := range []bool{false, true} {
			for _, rounds := range []int{1, 8, 32} {
				t.Run(fmt.Sprintf("%s/shared=%t/%d", tc.name, shared, rounds), func(t *testing.T) {
					src := containerAliasStringSource(tc.body, rounds, shared)
					path := filepath.Join(t.TempDir(), "main.fern")
					if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
						t.Fatal(err)
					}
					want := strings.Repeat("aa!\nbb!\n", rounds)
					if shared {
						want += want
					}
					out, err := exec.Command(native, "-interp", path).CombinedOutput()
					if err != nil || string(out) != want {
						t.Fatalf("interpreter: %v, got %q, want %q", err, out, want)
					}
					nativeBin := filepath.Join(t.TempDir(), "native")
					compile := exec.Command(native, "-target", target, "-o", nativeBin, path)
					compile.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
					if out, err := compile.CombinedOutput(); err != nil {
						t.Fatalf("native compile: %v\n%s", err, out)
					}
					var nativeErr bytes.Buffer
					nativeCmd := run(nativeBin)
					nativeCmd.Stderr = &nativeErr
					out, err = nativeCmd.Output()
					if err != nil || string(out) != want {
						t.Fatalf("native: %v, got %q, want %q\n%s", err, out, want, nativeErr.String())
					}
					t.Logf("native %s", leakSummaryLine(nativeErr.String()))
					var nativeAllocs, nativeFrees, nativeLive int64
					if _, err := fmtSscan(leakSummaryLine(nativeErr.String()), &nativeAllocs, &nativeFrees, &nativeLive); err != nil {
						t.Fatal(err)
					}
					if nativeAllocs == 0 || nativeAllocs != nativeFrees || nativeLive != 0 {
						t.Fatalf("native must balance allocations: %s", nativeErr.String())
					}
					asm := runCaptureEnv(t, runner, driver, []byte(src), []string{"PATH=/usr/bin:/bin", "FERN_LEAKCHECK=1"}, "-target", target)
					if rounds == 1 && target == "arm64-linux" {
						for _, fn := range []string{"rebuild", "load", "exercise"} {
							body := arm64FnBody(t, string(asm), "__fn_"+fn)
							var calls []string
							for _, line := range strings.Split(body, "\n") {
								if strings.HasPrefix(strings.TrimSpace(line), "bl ") {
									calls = append(calls, strings.TrimSpace(line))
								}
							}
							t.Logf("%s calls: %s", fn, strings.Join(calls, ", "))
						}
					}
					bin := link(t, string(asm))
					cmd := run(bin)
					var stderr bytes.Buffer
					cmd.Stderr = &stderr
					out, err = cmd.Output()
					if err != nil || string(out) != want {
						t.Errorf("self-host: %v, got %q, want %q\n%s", err, out, want, stderr.String())
					}
					summary := leakSummaryLine(stderr.String())
					var allocs, frees, live int64
					if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
						t.Fatalf("parse %q: %v", summary, err)
					}
					t.Log(summary)
					if allocs == 0 || frees != allocs || live != 0 {
						t.Errorf("container transfer must balance allocations and frees: %s", summary)
					}
				})
			}
		}
	}
}
