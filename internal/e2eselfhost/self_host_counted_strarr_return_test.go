package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Test the ownership admission itself. Correct output alone does not prove a
// refused case is safe to deep-free: uncounted aliases can survive until a
// later allocation, and leaked references can mask an extra release.
func TestSelfHostCountedStrArrReturnProofX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	const probe = `import "./rundriver";
import "./irlower";
function main(): i32 {
    var m = rundriver.parse_stdin("counted-strarr-proof");
    var rows = irlower.return_fresh_struct_ret_fns_of(m.funcs, irlower.struct_tab(m.structs), []);
    for row in rows { print(row); }
    return 0;
}`
	if err := os.WriteFile(filepath.Join(dir, "proof.fern"), []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "proof.fern", "proof")
	native := buildLangBinForInterp(t)
	cases := []struct {
		name, source string
		owned        bool
	}{
		{"direct", `function build(p: string[]): string[] { var out: string[] = []; out = out.append(p[0]); return out; }`, true},
		{"array-literal", `function build(p: string[]): string[] { return [p[0], p[1]]; }`, true},
		{"alias-chain", `function build(p: string[]): string[] { var q = p; var r = q; var x = r[0]; var y = x; var out: string[] = []; out = out.append(y); return out; }`, true},
		{"foreach-alias", `function build(p: string[]): string[] { var q = p; var out: string[] = []; for x in q { out = out.append(x); } return out; }`, true},
		{"forward-result", `function build(p: string[]): string[] { return helper(p); } function helper(p: string[]): string[] { return [p[0]]; }`, true},
		{"bound-forward-result", `function build(p: string[]): string[] { var out: string[] = helper(p); return out; } function helper(p: string[]): string[] { return [p[0]]; }`, true},
		{"conditional-store", `function build(p: string[], yes: boolean): string[] { var out: string[] = []; if (yes) { out = out.append(p[0]); } else { out = out.append(p[1]); } return out; }`, true},
		{"unowned-string", `function build(p: string): string[] { var out: string[] = []; out = out.append(p); return out; }`, false},
		{"parameter-return", `function build(p: string[]): string[] { return p; }`, false},
		{"owned-parameter-return", `function build(own p: string[]): string[] { return p; }`, false},
		{"tuple-fresh-destructure", `function w(a: string): string { return a + "!"; } function build(): string[] { var p: (i32, string[]) = (0, [w("x"), w("y")]); var (a, out) = p; return out; }`, false},
		{"tuple-borrowed-destructure", `function build(p: (i32, string[])): string[] { var (a, out) = p; return out; }`, false},
		{"foreach-bound-return", `function build(p: string[][]): string[] { for out in p { return out; } return []; }`, false},
		{"match-bound-return", `function build(p: Option[string[]]): string[] { match (p) { Some(out) => { return out; }, None => { return []; } } }`, false},
		{"local-array-element", `function build(): string[] { var p: string[] = ["aa" + "!"]; var out: string[] = []; out = out.append(p[0]); return out; }`, false},
		{"mutable-array-alias", `function build(p: string[], q: string[]): string[] { var a = p; a = q; return [a[0]]; }`, false},
		{"mutable-element-alias", `function build(p: string[], x: string): string[] { var a = p[0]; a = x; return [a]; }`, false},
		{"shadowed-source", `function build(p: string[], yes: boolean): string[] { var out: string[] = []; if (yes) { var p: string[] = ["aa" + "!"]; out = out.append(p[0]); } return out; }`, false},
		{"shadowed-element", `function build(p: string[], yes: boolean): string[] { var x = p[0]; var out: string[] = []; if (yes) { var x = "bb" + "!"; out = out.append(x); } return out; }`, false},
		{"parameter-shadowed-by-element", `function build(p: string[], x: string, yes: boolean): string[] { var out: string[] = []; if (yes) { var x = p[0]; out = out.append(x); } out = out.append(x); return out; }`, false},
		{"unproven-call", `function build(p: string[], f: (string[]) => string[]): string[] { return f(p); }`, false},
		{"unproven-branch", `function build(p: string[], yes: boolean): string[] { if (yes) { return [p[0]]; } return p; }`, false},
		{"builder-element-handout", `function build(p: string[]): string[] { var out: string[] = []; out = out.append(p[0]); var x = out[0]; return out; }`, false},
		{"builder-buffer-alias", `function build(p: string[]): string[] { var out: string[] = []; out = out.append(p[0]); var x = out; return out; }`, false},
		{"builder-replaced", `function build(p: string[]): string[] { var out: string[] = []; out = p; return out; }`, false},
		{"ungrounded-cycle", `function build(p: string[]): string[] { return helper(p); } function helper(p: string[]): string[] { return build(p); }`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Pin source validity independently of the proof's parser-only
			// entry point, so malformed binding fixtures cannot pass by refusal.
			path := filepath.Join(t.TempDir(), "proof-input.fern")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(native, "-check", path).CombinedOutput(); err != nil {
				t.Fatalf("native source validity: %v\n%s", err, out)
			}
			out := runCapture(t, gcc, runner, bin, []byte(tc.source))
			got := false
			for _, row := range strings.Fields(string(out)) {
				if row == "SARRC:build" {
					got = true
				}
			}
			if got != tc.owned {
				t.Fatalf("counted return = %v, want %v; registry:\n%s", got, tc.owned, out)
			}
		})
	}
}
