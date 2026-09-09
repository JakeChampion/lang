package e2eselfhost

import (
	"bytes"
	"strings"
	"testing"
)

func TestSelfHostCallableTypes(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "callable_types_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "callable_types_run.fern", "callable_types")
	want := strings.Join([]string{
		"fn()->void",
		"opaque->unknown(fn result (the coarse tag records none))",
		"fn(i64,string)->bool",
		"fn(i32)->array<string>",
		"fn(fn(i64)->string,tuple(bool,array<i32>))->fn(string)->array<i64>",
		"array<fn(i32)->string>",
		"fn(struct:Foo[string],union:Option[array<i32>])->struct:Foo[bool]",
		"fn(map<string,fn(i32)->struct:Foo[i64]>)->union:Result[string,i32]",
		"fn(unknown(unrecognised type name: Missing),opaque->unknown(fn result (the coarse tag records none)))->unknown(unrecognised type name: Missing)",
		"fn(dyn:Shape,char,u64)->bool",
		"array<dyn:Shape>",
		"fn(array<dyn:Shape>,map<string,dyn:Shape>)->dyn:Shape",
	}, "\n") + "\n"
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("callable type contract: %v\n%s", err, out)
	}
	if string(out) != want {
		t.Fatalf("callable type contract:\ngot:\n%swant:\n%s", out, want)
	}
}

func TestSelfHostCallableTypeDiagnostics(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "checker_codes_run.fern", "checker_codes")
	for _, tc := range []struct {
		name string
		src  string
		bad  bool
	}{
		{"zero-valid", `function seven(): i32 { return 7; } function main(): i32 { var t: (() => i32, i32) = (seven, 0); var h = t.0; return h(); }`, false},
		{"zero-extra", `function seven(): i32 { return 7; } function main(): i32 { var t: (() => i32, i32) = (seven, 0); var h = t.0; return h(1); }`, true},
		{"tuple-valid", `function inc(n: i32): i32 { return n + 1; } function main(): i32 { var t: ((i32) => i32, i32) = (inc, 0); var h = t.0; return h(7); }`, false},
		{"tuple-missing", `function inc(n: i32): i32 { return n + 1; } function main(): i32 { var t: ((i32) => i32, i32) = (inc, 0); var h = t.0; return h(); }`, true},
		{"tuple-wrong-type", `function inc(n: i32): i32 { return n + 1; } function main(): i32 { var t: ((i32) => i32, i32) = (inc, 0); var h = t.0; return h("bad"); }`, true},
		{"array-valid", `function inc(n: i32): i32 { return n + 1; } function main(): i32 { var fs: ((i32) => i32)[] = [inc]; var h = fs[0]; return h(7); }`, false},
		{"array-extra", `function inc(n: i32): i32 { return n + 1; } function main(): i32 { var fs: ((i32) => i32)[] = [inc]; var h = fs[0]; return h(7, 8); }`, true},
		{"tuple-direct-wrong", `function inc(n: i32): i32 { return n + 1; } function main(): i32 { var t: ((i32) => i32, i32) = (inc, 0); return t.0("bad"); }`, true},
		{"array-direct-wrong", `function inc(n: i32): i32 { return n + 1; } function main(): i32 { var fs: ((i32) => i32)[] = [inc]; return fs[0]("bad"); }`, true},
		{"local-valid", `function main(): i32 { var f: (i32) => i32 = (n: i32): i32 => { return n; }; return f(7); }`, false},
		{"local-wrong", `function main(): i32 { var f: (i32) => i32 = (n: i32): i32 => { return n; }; return f("bad"); }`, true},
		{"param-valid", `function apply(f: (i32) => i32): i32 { return f(7); } function main(): i32 { return 0; }`, false},
		{"param-wrong", `function apply(f: (i32) => i32): i32 { return f("bad"); } function main(): i32 { return 0; }`, true},
		{"array-param-valid", `function apply(fs: ((i32) => i32)[]): i32 { return fs[0](7); } function main(): i32 { return 0; }`, false},
		{"array-param-wrong", `function apply(fs: ((i32) => i32)[]): i32 { return fs[0](); } function main(): i32 { return 0; }`, true},
		{"field-valid", `struct Holder { f: (i32) => i32 } function apply(h: Holder): i32 { return h.f(7); } function main(): i32 { return 0; }`, false},
		{"field-wrong", `struct Holder { f: (i32) => i32 } function apply(h: Holder): i32 { return h.f("bad"); } function main(): i32 { return 0; }`, true},
		{"field-arity", `struct Holder { f: (i32) => i32 } function apply(h: Holder): i32 { return h.f(); } function main(): i32 { return 0; }`, true},
		{"array-field-wrong", `struct Holder { fs: ((i32) => i32)[] } function apply(h: Holder): i32 { return h.fs[0](); } function main(): i32 { return 0; }`, true},
		{"returned-valid", `function inc(n: i32): i32 { return n + 1; } function make(): (i32) => i32 { return inc; } function main(): i32 { return make()(7); }`, false},
		{"returned-wrong", `function inc(n: i32): i32 { return n + 1; } function make(): (i32) => i32 { return inc; } function main(): i32 { return make()("bad"); }`, true},
		{"returned-arity", `function inc(n: i32): i32 { return n + 1; } function make(): (i32) => i32 { return inc; } function main(): i32 { return make()(); }`, true},
		{"returned-array-wrong", `function inc(n: i32): i32 { return n + 1; } function make(): ((i32) => i32)[] { return [inc]; } function main(): i32 { return make()[0](); }`, true},
		{"enum-first-valid", `enum Task { Run((i32) => i32) } function apply(t: Task): i32 { match (t) { Run(f) => { return f(7); } } } function main(): i32 { return 0; }`, false},
		{"enum-first-wrong", `enum Task { Run((i32) => i32) } function apply(t: Task): i32 { match (t) { Run(f) => { return f("bad"); } } } function main(): i32 { return 0; }`, true},
		{"enum-second-valid", `enum Task { Run(i32, (i32) => i32) } function apply(t: Task): i32 { match (t) { Run(n, f) => { return f(n); } } } function main(): i32 { return 0; }`, false},
		{"enum-second-arity", `enum Task { Run(i32, (i32) => i32) } function apply(t: Task): i32 { match (t) { Run(n, f) => { return f(); } } } function main(): i32 { return 0; }`, true},
		{"enum-named-valid", `enum Task { Run { n: i32, f: (i32) => i32 } } function apply(t: Task): i32 { match (t) { Run { f, n } => { return f(n); } } } function main(): i32 { return 0; }`, false},
		{"enum-named-wrong", `enum Task { Run { f: (i32) => i32 } } function apply(t: Task): i32 { match (t) { Run { f } => { return f("bad"); } } } function main(): i32 { return 0; }`, true},
		{"enum-array-valid", `enum Task { Run(((i32) => i32)[]) } function apply(t: Task): i32 { match (t) { Run(fs) => { return fs[0](7); } } } function main(): i32 { return 0; }`, false},
		{"enum-array-arity", `enum Task { Run(((i32) => i32)[]) } function apply(t: Task): i32 { match (t) { Run(fs) => { return fs[0](); } } } function main(): i32 { return 0; }`, true},
		{"enum-zero-valid", `enum Task { Run(() => i32) } function apply(t: Task): i32 { match (t) { Run(f) => { return f(); } } } function main(): i32 { return 0; }`, false},
		{"enum-zero-extra", `enum Task { Run(() => i32) } function apply(t: Task): i32 { match (t) { Run(f) => { return f(7); } } } function main(): i32 { return 0; }`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := goCheckerCodes(t, dir, tc.src)
			if (len(want) > 0) != tc.bad {
				t.Fatalf("invalid oracle fixture: codes %v, want error=%v", want, tc.bad)
			}
			cmd := runX86_64Bin(runner, bin)
			cmd.Stdin = bytes.NewBufferString(tc.src)
			got := driverCodes(runCheckerDriver(t, cmd, tc.name))
			if !equalStrings(got, want) {
				t.Fatalf("self-host codes %v, native codes %v\n%s", got, want, tc.src)
			}
		})
	}
}
