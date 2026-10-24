package platforms_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/oracle/monomorph"
	"github.com/jakechampion/lang/internal/oracle/treeshake"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func prepared(t *testing.T, src string, httpDropMain bool) *ast.Program {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	if httpDropMain {
		kept := prog.Funcs[:0]
		for _, fn := range prog.Funcs {
			if fn.IsSynthesisedHandlerMain {
				continue
			}
			kept = append(kept, fn)
		}
		prog.Funcs = kept
	}
	var extras []string
	if httpDropMain {
		extras = append(extras, "handle", "__method_HeaderMap_append")
	}
	treeshake.Run(prog, info, extras...)
	return prog
}

func TestEnforceSubprocessByTarget(t *testing.T) {
	src := `function main(): i32 {
    let r = subprocess("/bin/echo", ["hi"], "");
    return r.exit_code;
}`
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin", "arm64-android"} {
		if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
			t.Errorf("%s: unexpected violations: %+v", target, vs)
		}
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http", "arm64-freestanding", "x86-64-freestanding"} {
		t.Run(target, func(t *testing.T) {
			vs := platforms.Enforce(prepared(t, src, false), target)
			if len(vs) != 1 {
				t.Fatalf("violations = %d, want 1: %+v", len(vs), vs)
			}
			if vs[0].Builtin != "subprocess" || vs[0].Capability != "subprocess" {
				t.Errorf("violation = %+v, want subprocess/subprocess", vs[0])
			}
			want := "targets providing it: [arm64-android arm64-darwin arm64-linux x86-64-linux]"
			if msg := vs[0].Message("<stdin>"); !strings.Contains(msg, want) {
				t.Errorf("message does not list the hosted natives (%q): %s", want, msg)
			}
			if vs[0].Pos.Line != 2 {
				t.Errorf("violation position line = %d, want 2", vs[0].Pos.Line)
			}
		})
	}
}

func TestEnforceProcByTarget(t *testing.T) {
	src := `function main(): i32 {
    let pid: i32 = proc_fork();
    if (pid == 0) {
        return proc_exec("/bin/true", []);
    }
    return proc_waitpid(pid);
}`
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin", "arm64-android"} {
		if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
			t.Errorf("%s: unexpected violations: %+v", target, vs)
		}
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		t.Run(target, func(t *testing.T) {
			vs := platforms.Enforce(prepared(t, src, false), target)
			if len(vs) != 3 {
				t.Fatalf("violations = %d, want 3 (proc_fork + proc_exec + proc_waitpid): %+v", len(vs), vs)
			}
			for _, v := range vs {
				if v.Capability != "proc" {
					t.Errorf("violation capability = %q, want proc: %+v", v.Capability, v)
				}
			}
			got := []string{vs[0].Builtin, vs[1].Builtin, vs[2].Builtin}
			want := []string{"proc_fork", "proc_exec", "proc_waitpid"}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("builtins = %v, want %v", got, want)
					break
				}
			}
			if msg := vs[0].Message("<stdin>"); !strings.Contains(msg, "x86-64-linux") || !strings.Contains(msg, "arm64-linux") {
				t.Errorf("provider hint missing native targets: %s", msg)
			}
		})
	}
}

func TestEnforceHostByTarget(t *testing.T) {
	src := `function main(): i32 {
    let h: string = hostname();
    return 0;
}`
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin", "arm64-android", "wasm32-wasi"} {
		if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
			t.Errorf("%s: unexpected violations: %+v", target, vs)
		}
	}
	vs := platforms.Enforce(prepared(t, src, false), "wasm32-wasi-http")
	if len(vs) != 1 || vs[0].Builtin != "hostname" || vs[0].Capability != "host" {
		t.Fatalf("wasm32-wasi-http: violations = %+v, want one hostname/host", vs)
	}
}

func TestEnforceFsByTarget(t *testing.T) {
	src := `function main(): i32 {
    let r = read_file("/etc/config");
    return 0;
}`
	for _, target := range []string{"x86-64-linux", "wasm32-wasi", "arm64-linux"} {
		if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
			t.Errorf("%s: unexpected violations: %+v", target, vs)
		}
	}
	vs := platforms.Enforce(prepared(t, src, false), "wasm32-wasi-http")
	if len(vs) != 1 || vs[0].Capability != "fs" {
		t.Fatalf("wasi-http violations = %+v, want one fs violation", vs)
	}
}

func TestEnforceUnusedImportsDontTrip(t *testing.T) {
	src := `import "std/http";
import "std/serve";
import "std/platform";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("ok");
}`
	prog := prepared(t, src, true)
	if vs := platforms.Enforce(prog, "wasm32-wasi-http"); len(vs) != 0 {
		t.Fatalf("clean handler tripped enforcement: %+v", vs)
	}
}

func TestEnforceTransitiveReach(t *testing.T) {
	src := `function helper(): string {
    let r = read_file("/etc/x");
    return "y";
}
function main(): i32 {
    let s = helper();
    return 0;
}`
	vs := platforms.Enforce(prepared(t, src, false), "wasm32-wasi-http")
	if len(vs) != 1 {
		t.Fatalf("violations = %d, want 1: %+v", len(vs), vs)
	}
	if vs[0].FuncName != "helper" || vs[0].Builtin != "read_file" {
		t.Errorf("violation = %+v, want read_file in helper", vs[0])
	}
}

func TestEnforceUnknownTargetSkips(t *testing.T) {
	src := `function main(): i32 {
    let r = subprocess("/bin/echo", [], "");
    return r.exit_code;
}`
	if vs := platforms.Enforce(prepared(t, src, false), "no-such-target"); vs != nil {
		t.Fatalf("unknown target should skip enforcement, got %+v", vs)
	}
}

func TestEnforceHeapCheckpointNativeOnly(t *testing.T) {
	src := `function main(): i32 {
    let m: i64 = __heap_mark();
    __heap_release_to(m);
    return (__heap_bump_bytes() as i32);
}`
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin", "arm64-android"} {
		t.Run(target+"/allowed", func(t *testing.T) {
			if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
				t.Errorf("native target %q should provide `arena`; violations = %+v", target, vs)
			}
		})
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		t.Run(target+"/rejected", func(t *testing.T) {
			vs := platforms.Enforce(prepared(t, src, false), target)
			if len(vs) != 2 {
				t.Fatalf("violations = %d, want 2 (mark + release_to): %+v", len(vs), vs)
			}
			for _, v := range vs {
				if v.Capability != "arena" {
					t.Errorf("violation %+v: capability = %q, want arena", v, v.Capability)
				}
				if v.Builtin != "__heap_mark" && v.Builtin != "__heap_release_to" {
					t.Errorf("unexpected gated builtin %q", v.Builtin)
				}
				if msg := v.Message("<stdin>"); !strings.Contains(msg, "x86-64-linux") {
					t.Errorf("message should list the providing targets: %s", msg)
				}
			}
		})
	}
}

func TestEnforceWasmInexpressibleBuiltins(t *testing.T) {
	cases := []struct {
		builtin    string
		capability string
		src        string
	}{
		{"timer_fd", "pollfd", `function main(): i32 { return timer_fd(50); }`},
		{"write_file_exec", "fsmode", `function main(): i32 {
    let r = write_file_exec("/tmp/x", "#!/bin/sh\n");
    return 0;
}`},
		{"__c_call2_f64", "cabi", `function main(): i32 {
    let v: f64 = __c_call2_f64((0 as usize), (0 as usize), (0 as usize));
    return 0;
}`},
		{"access", "fsmode", `function main(): i32 {
    let r = access("/tmp/x", 4);
    return 0;
}`},
		{"geteuid", "userid", `function main(): i32 { return (geteuid() as i32); }`},
		{"getegid", "userid", `function main(): i32 { return (getegid() as i32); }`},
		{"getuid", "userid", `function main(): i32 { return (getuid() as i32); }`},
		{"getgid", "userid", `function main(): i32 { return (getgid() as i32); }`},
		{"getgroups", "userid", `function main(): i32 {
    let g: i64[] = getgroups();
    return (g.len() as i32);
}`},
	}
	for _, tc := range cases {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin", "arm64-android"} {
			t.Run(tc.builtin+"/"+target+"/allowed", func(t *testing.T) {
				if vs := platforms.Enforce(prepared(t, tc.src, false), target); len(vs) != 0 {
					t.Errorf("native target %q should provide `%s`; violations = %+v", target, tc.capability, vs)
				}
			})
		}
		for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
			t.Run(tc.builtin+"/"+target+"/rejected", func(t *testing.T) {
				vs := platforms.Enforce(prepared(t, tc.src, false), target)
				if len(vs) != 1 {
					t.Fatalf("violations = %d, want 1: %+v", len(vs), vs)
				}
				if vs[0].Builtin != tc.builtin || vs[0].Capability != tc.capability {
					t.Errorf("violation = %+v, want %s/%s", vs[0], tc.builtin, tc.capability)
				}
				if msg := vs[0].Message("<stdin>"); !strings.Contains(msg, tc.capability) || !strings.Contains(msg, "x86-64-linux") {
					t.Errorf("message should name the capability and its providers: %s", msg)
				}
			})
		}
	}
}

func TestEnforceLogAndClockUniversal(t *testing.T) {
	srcs := map[string]string{
		"print":  `function main(): i32 { print("hi"); return 0; }`,
		"eprint": `function main(): i32 { eprint("hi"); return 0; }`,
		"clock":  `function main(): i32 { return (now_unix_ms() as i32); }`,
		"sleep":  `function main(): i32 { sleep_ms(1); return 0; }`,
	}
	for name, src := range srcs {
		for _, target := range platforms.Targets() {
			if platforms.ForTarget(target).NoBackend {
				continue
			}
			t.Run(name+"/"+target, func(t *testing.T) {
				if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
					t.Errorf("%s on %s: unexpected violations: %+v", name, target, vs)
				}
			})
		}
	}
}

func TestEnforceStdoutStreamByTarget(t *testing.T) {
	srcs := map[string]string{
		"write":   `function main(): i32 { write("hi"); return 0; }`,
		"putchar": `function main(): i32 { putchar(65); return 0; }`,
		"handle":  `function main(): i32 { let w = stdout(); return 0; }`,
	}
	for name, src := range srcs {
		for _, target := range []string{"arm64-linux", "arm64-darwin", "arm64-android", "x86-64-linux", "wasm32-wasi"} {
			t.Run(name+"/"+target, func(t *testing.T) {
				if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
					t.Errorf("%s on %s: unexpected violations: %+v", name, target, vs)
				}
			})
		}
		t.Run(name+"/wasi-http", func(t *testing.T) {
			vs := platforms.Enforce(prepared(t, src, false), "wasm32-wasi-http")
			if len(vs) != 1 || vs[0].Capability != "stdout" {
				t.Fatalf("%s on wasi-http: violations = %+v, want one stdout violation", name, vs)
			}
		})
	}
}

func TestReachDeclaredFunctionShadowsBuiltin(t *testing.T) {
	prog, err := parser.Parse(`function write(data: u8[]): i32 { return data.len(); }
function main(): i32 { let b: u8[] = []; return write(b); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := platforms.Reach(prog); len(got) != 0 {
		t.Fatalf("reach = %v, want none: write is a declared function", got)
	}
	if vs := platforms.Enforce(prog, "wasm32-wasi-http"); len(vs) != 0 {
		t.Fatalf("violations = %+v, want none: write is a declared function", vs)
	}
}

func TestClassificationCoversCheckerRegistry(t *testing.T) {
	prog, err := parser.Parse("function main(): void {}")
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	for name := range info.FuncSigs {
		if name == "main" || strings.HasPrefix(name, "__") {
			continue
		}
		_, gated := platforms.GatedBuiltin(name)
		core := platforms.CoreBuiltin(name)
		if !gated && !core {
			t.Errorf("builtin %q is unclassified: gate it in gatedBuiltins or declare it core in coreBuiltins (docs/FREESTANDING-CORE.md)", name)
		}
		if gated && core {
			t.Errorf("builtin %q is both gated and core", name)
		}
	}
}

func TestEnforceArgsAndEnvNotOnProxyWorld(t *testing.T) {
	srcs := map[string]string{
		"args": `function main(): i32 { let a: string[] = args(); return 0; }`,
		"env":  `function main(): i32 { let v = env("HOME"); return 0; }`,
	}
	for capability, src := range srcs {
		for _, target := range []string{"arm64-linux", "arm64-darwin", "arm64-android", "x86-64-linux", "wasm32-wasi"} {
			t.Run(capability+"/"+target, func(t *testing.T) {
				if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
					t.Errorf("%s: unexpected violations: %+v", target, vs)
				}
			})
		}
		t.Run(capability+"/wasm32-wasi-http", func(t *testing.T) {
			vs := platforms.Enforce(prepared(t, src, false), "wasm32-wasi-http")
			if len(vs) != 1 || vs[0].Capability != capability {
				t.Fatalf("violations = %+v, want one %s violation", vs, capability)
			}
		})
	}
}

func TestEnforceRandomUniversal(t *testing.T) {
	srcs := map[string]string{
		"random": `function main(): i32 { return random_i32(); }`,
	}
	for name, src := range srcs {
		for _, target := range platforms.Targets() {
			if platforms.ForTarget(target).NoBackend {
				continue
			}
			t.Run(name+"/"+target, func(t *testing.T) {
				if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
					t.Errorf("%s on %s: unexpected violations: %+v", name, target, vs)
				}
			})
		}
	}
}

func TestEnforceFreestandingGrantsNoHost(t *testing.T) {
	srcs := map[string]string{
		"print":  `function main(): i32 { print("hi"); return 0; }`,
		"clock":  `function main(): i32 { return (now_unix_ms() as i32); }`,
		"env":    `function main(): i32 { let v = env("HOME"); return 0; }`,
		"args":   `function main(): i32 { let a: string[] = args(); return 0; }`,
		"random": `function main(): i32 { return random_i32(); }`,
		"fs":     `function main(): i32 { let r = read_file("/etc/x"); return 0; }`,
	}
	for name, src := range srcs {
		t.Run(name, func(t *testing.T) {
			vs := platforms.Enforce(prepared(t, src, false), "arm64-freestanding")
			if len(vs) == 0 {
				t.Fatalf("%s: freestanding should grant no host capability, got no violations", name)
			}
		})
	}
}

func TestEnforceFreestandingAllowsCore(t *testing.T) {
	src := `function main(): i32 {
    let b: i64 = f64_bits(1.5);
    return ((b + 1) as i32);
}`
	if vs := platforms.Enforce(prepared(t, src, false), "arm64-freestanding"); len(vs) != 0 {
		t.Fatalf("core-only program rejected on freestanding: %+v", vs)
	}
}

func TestFreestandingDescriptorIsCheckOnly(t *testing.T) {
	free := []string{"arm64-freestanding", "x86-64-freestanding"}
	for _, name := range free {
		t.Run(name, func(t *testing.T) {
			d := platforms.ForTarget(name)
			if d == nil {
				t.Fatalf("%s has no descriptor", name)
			}
			if !d.NoBackend {
				t.Errorf("%s should be marked NoBackend until #6510/#6511 land", name)
			}
			if len(d.Capabilities) != 0 {
				t.Errorf("%s grants %v, want nothing", name, d.Capabilities)
			}
			if d.Environment != "freestanding" {
				t.Errorf("%s environment = %q, want freestanding", name, d.Environment)
			}
		})
	}
	listed := map[string]bool{}
	for _, n := range platforms.Targets() {
		listed[n] = true
		emits := !platforms.ForTarget(n).NoBackend
		isFree := platforms.ForTarget(n).Environment == "freestanding"
		if emits == isFree {
			t.Errorf("target %q: emits=%v but freestanding=%v — they must be opposites", n, emits, isFree)
		}
	}
	for _, n := range free {
		if !listed[n] {
			t.Errorf("%s missing from Targets(), so `fern -targets` won't show it", n)
		}
	}
}

func TestRawMemoryFloorNativeOnly(t *testing.T) {
	floor := []string{"__raw_alloc", "__raw_string", "__raw_arr_box", "__raw_scratch", "__raw_environ",
		"__raw_splice_pipe", "__raw_cover", "__raw_task_state", "__raw_vdso", "__raw_vg_request"}
	for _, b := range floor {
		if c, ok := platforms.GatedBuiltin(b); !ok || c != "rawmem" {
			t.Errorf("%s gated on %q (gated=%v), want rawmem", b, c, ok)
		}
	}
	for _, b := range []string{"__raw_load8", "__raw_store8", "__raw_load_ptr", "__raw_store_ptr", "__raw_data", "__raw_array", "__raw_arr_ptr"} {
		if c, ok := platforms.GatedBuiltin(b); ok {
			t.Errorf("%s gated on %q; wasm lowers it, so it stays ungated", b, c)
		}
	}
	want := "arm64-android arm64-darwin arm64-linux x86-64-linux"
	if got := strings.Join(platforms.TargetsProviding("rawmem"), " "); got != want {
		t.Errorf("rawmem providers = %s, want %s", got, want)
	}
}

func TestViolationNamesTheEquivalent(t *testing.T) {
	for target, want := range map[string]bool{"wasm32-wasi": true, "wasm32-wasi-http": true, "arm64-freestanding": false} {
		msg := platforms.Violation{Builtin: "timer_fd", Capability: "pollfd", Target: target}.Message("")
		if got := strings.Contains(msg, "use `wasm_timer_pollable`, which this target provides"); got != want {
			t.Errorf("%s: names the equivalent = %v, want %v: %s", target, got, want, msg)
		}
	}
}

func TestGatedCapabilitiesResolvable(t *testing.T) {
	for builtin, c := range platforms.GatedBuiltins() {
		if got := platforms.TargetsProviding(c); len(got) == 0 {
			t.Errorf("builtin %q is gated on `%s`, which no target provides — descriptor/gate-table drift", builtin, c)
		}
	}
}

func TestEnforceIgnoresCallsBehindAnUnreachableCoercion(t *testing.T) {
	src := `trait Greet { function hello(self: Self): i32; }
struct Loud { n: i32 }
impl Greet for Loud { function hello(self: Self): i32 { return logs_a_line(); } }

function logs_a_line(): i32 {
    print("nothing can reach this");
    return 1;
}

function dead_coercion(): i32 {
    let g: dyn Greet = Loud { n: 1 };
    return g.hello();
}

function main(): i32 { return 7; }`
	prog := prepared(t, src, false)
	if vs := platforms.Enforce(prog, "arm64-freestanding"); len(vs) != 0 {
		t.Errorf("want no violation for an unreachable coercion, got %d: %v", len(vs), vs)
	}

	live := strings.Replace(src, "function main(): i32 { return 7; }",
		"function main(): i32 { return dead_coercion(); }", 1)
	vs := platforms.Enforce(prepared(t, live, false), "arm64-freestanding")
	if len(vs) == 0 {
		t.Fatal("a reachable coercion must still surface its callee's capability use")
	}
	if vs[0].Builtin != "print" {
		t.Errorf("want the print violation, got %+v", vs[0])
	}
}
