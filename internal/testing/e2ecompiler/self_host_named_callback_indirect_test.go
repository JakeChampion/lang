package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var namedCallbackIndirectCases = []struct{ name, source string }{
	{"field", `struct Action { run: (i32, () => boolean) => i32 }
function never(): boolean { return false; }
function main(): i32 {
    let action = Action { run: (value: i32, stop: () => boolean): i32 => { if (stop()) { return 1; } return value; } };
    return action.run(42, never) - 42;
}`},
	{"nested", `struct Action { run: (string, () => boolean) => string }
struct Outer { action: Action }
function never(): boolean { return false; }
function main(): i32 {
    let suffix = args()[0] + ": callback";
    let outer = Outer { action: Action { run: (value: string, stop: () => boolean): string => { if (stop()) { return "bad"; } return value + suffix; } } };
    if (outer.action.run("ok", never) != "ok" + suffix) { return 1; }
    return 0;
}`},
	{"tuple", `function never(): boolean { return false; }
function main(): i32 {
    let action = ((value: i32, stop: () => boolean): i32 => { if (stop()) { return 1; } return value; }, 9);
    return action.0(42, never) - 42;
}`},
	{"index", `function never(): boolean { return false; }
function main(): i32 {
    let actions: ((i32, () => boolean) => i32)[] = [(value: i32, stop: () => boolean): i32 => { if (stop()) { return 1; } return value; }];
    return actions[0](42, never) - 42;
}`},
	{"generic", `struct Action[T] { run: (T, () => boolean) => T }
function never(): boolean { return false; }
function dispatch[T](action: Action[T], value: T): T { return action.run(value, never); }
function main(): i32 {
    let number = Action[i32] { run: (value: i32, stop: () => boolean): i32 => { if (stop()) { return 0; } return value; } };
    let text = Action[string] { run: (value: string, stop: () => boolean): string => { if (stop()) { return "bad"; } return value; } };
    let expected = args()[0] + ": generic";
    if (dispatch(number, 42) != 42 || dispatch(text, expected) != expected) { return 1; }
    return 0;
}`},
	{"shadow", `struct Action { run: (() => boolean) => i32 }
function stop(): boolean { return false; }
function main(): i32 {
    let stop = (): boolean => true;
    let action = Action { run: (test: () => boolean): i32 => { if (test()) { return 42; } return 1; } };
    return action.run(stop) - 42;
}`},
	{"local-field", `struct Action { run: (i32, () => boolean) => i32 }
function never(): boolean { return false; }
function main(): i32 {
    let action = Action { run: (value: i32, stop: () => boolean): i32 => { if (stop()) { return 1; } return value; } };
    let invoke = action.run;
    return invoke(42, never) - 42;
}`},
	{"returned", `function never(): boolean { return false; }
function make(): ((i32, () => boolean) => i32) {
    return (value: i32, stop: () => boolean): i32 => { if (stop()) { return 1; } return value; };
}
function main(): i32 { return make()(42, never) - 42; }`},
}

func TestSelfHostNamedCallbackIndirect(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range namedCallbackIndirectCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.source), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					var allocs, frees, live int64
					summary := leakSummaryLine(stderr)
					if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
						t.Fatalf("parse census %q: %v", summary, err)
					}
					if allocs != frees || live != 0 {
						t.Fatalf("unbalanced census: %s", summary)
					}
					if (tc.name == "nested" || tc.name == "index" || tc.name == "generic") && allocs == 0 {
						t.Fatal("heap probe made no allocation")
					}
				})
			}
		})
	}
}

func TestSelfHostNamedCallbackIndirectRejectsSignature(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, signature := range []string{"function stop(value: i32): boolean { return false; }", "function stop(): i32 { return 0; }"} {
		path := filepath.Join(t.TempDir(), "main.fern")
		source := `struct Action { run: (() => boolean) => i32 }
` + signature + `
function main(): i32 {
    let action = Action { run: (test: () => boolean): i32 => { if (test()) { return 1; } return 0; } };
    return action.run(stop);
}`
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"check", "x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+signature, func(t *testing.T) {
				cmd := cli.compileCmd(target, filepath.Join(t.TempDir(), "out"), path)
				if target == "check" {
					cmd = runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib)
				}
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "error[E038]") {
					t.Fatalf("expected callback signature rejection, got %v: %s", err, out)
				}
			})
		}
	}
}
