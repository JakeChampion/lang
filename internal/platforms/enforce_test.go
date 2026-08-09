package platforms_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
	"github.com/jakechampion/lang/internal/treeshake"
)

// prepared mirrors cmd/fern's pre-enforcement pipeline: load (stdlib
// imports resolved), check, monomorphise, then tree-shake with the
// dyn roots — so Enforce sees exactly the call graph a backend would
// compile. httpDropMain mirrors the wasi-http-only drop of the
// synthesised tcp_serve main.
func prepared(t *testing.T, src string, httpDropMain bool) *ast.Program {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
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
	extras := append(treeshake.DynCoercionImplMethods(info), treeshake.DowncastImplMethods(prog, info)...)
	if httpDropMain {
		extras = append(extras, "handle", "__method_HeaderMap_append")
	}
	treeshake.Run(prog, extras...)
	return prog
}

// subprocess is interp-only: every compiled target rejects it, and the
// message says so instead of listing zero providers.
func TestEnforceSubprocessRejectedOnCompiledTargets(t *testing.T) {
	src := `function main(): i32 {
    var r = subprocess("/bin/echo", ["hi"], "");
    return r.exit_code;
}`
	for _, target := range []string{"wasm", "x86-64", "arm64", "arm64-darwin", "arm64-android"} {
		t.Run(target, func(t *testing.T) {
			prog := prepared(t, src, false)
			vs := platforms.Enforce(prog, target)
			if len(vs) != 1 {
				t.Fatalf("violations = %d, want 1: %+v", len(vs), vs)
			}
			if vs[0].Builtin != "subprocess" || vs[0].Capability != "subprocess" {
				t.Errorf("violation = %+v, want subprocess/subprocess", vs[0])
			}
			if msg := vs[0].Message("<stdin>"); !strings.Contains(msg, "fern -interp") {
				t.Errorf("interp-only hint missing from message: %s", msg)
			}
			if vs[0].Pos.Line != 2 {
				t.Errorf("violation position line = %d, want 2", vs[0].Pos.Line)
			}
		})
	}
}

// `proc` (proc_fork / proc_exec / proc_waitpid — docs/CRASH-ONLY-SERVE.md
// D2') is
// granted by the four native targets only: wasm worlds have no
// processes, so both wasm targets reject at check time. The interp is
// deliberately ungated (Enforce only runs for compiled targets): its
// proc_fork returns -38/ENOSYS so callers degrade at runtime instead.
func TestEnforceProcByTarget(t *testing.T) {
	src := `function main(): i32 {
    var pid: i32 = proc_fork();
    if (pid == 0) {
        return proc_exec("/bin/true", []);
    }
    return proc_waitpid(pid);
}`
	for _, target := range []string{"x86-64", "arm64", "arm64-darwin", "arm64-android"} {
		if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
			t.Errorf("%s: unexpected violations: %+v", target, vs)
		}
	}
	for _, target := range []string{"wasm", "wasi-http"} {
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
			if msg := vs[0].Message("<stdin>"); !strings.Contains(msg, "x86-64") || !strings.Contains(msg, "arm64") {
				t.Errorf("provider hint missing native targets: %s", msg)
			}
		})
	}
}

// The same fs-touching program is fine on targets granting `fs` and a
// violation under wasi-http.
func TestEnforceFsByTarget(t *testing.T) {
	src := `function main(): i32 {
    var r = read_file("/etc/config");
    return 0;
}`
	for _, target := range []string{"x86-64", "wasm", "arm64"} {
		if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
			t.Errorf("%s: unexpected violations: %+v", target, vs)
		}
	}
	vs := platforms.Enforce(prepared(t, src, false), "wasi-http")
	if len(vs) != 1 || vs[0].Capability != "fs" {
		t.Fatalf("wasi-http violations = %+v, want one fs violation", vs)
	}
}

// Importing a module whose OTHER functions use gated builtins is fine:
// tree-shaking drops the unreached wrappers before Enforce runs. The
// canonical case is a wasi-http handler importing std/tcp (whose
// tcp_serve → tcp_listen chain is only reachable through the DROPPED
// synthesised main).
func TestEnforceUnusedImportsDontTrip(t *testing.T) {
	src := `import "std/http";
import "std/tcp";

function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("ok");
}`
	prog := prepared(t, src, true)
	if vs := platforms.Enforce(prog, "wasi-http"); len(vs) != 0 {
		t.Fatalf("clean handler tripped enforcement: %+v", vs)
	}
}

// A gated builtin reached TRANSITIVELY (entry → helper → builtin) is
// still caught, and the violation names the containing function.
func TestEnforceTransitiveReach(t *testing.T) {
	src := `function helper(): string {
    var r = read_file("/etc/x");
    return "y";
}
function main(): i32 {
    var s = helper();
    return 0;
}`
	vs := platforms.Enforce(prepared(t, src, false), "wasi-http")
	if len(vs) != 1 {
		t.Fatalf("violations = %d, want 1: %+v", len(vs), vs)
	}
	if vs[0].FuncName != "helper" || vs[0].Builtin != "read_file" {
		t.Errorf("violation = %+v, want read_file in helper", vs[0])
	}
}

// Unknown targets (e.g. experimental backends without a descriptor)
// skip enforcement entirely.
func TestEnforceUnknownTargetSkips(t *testing.T) {
	src := `function main(): i32 {
    var r = subprocess("/bin/echo", [], "");
    return r.exit_code;
}`
	if vs := platforms.Enforce(prepared(t, src, false), "wasm-ssa"); vs != nil {
		t.Fatalf("unknown target should skip enforcement, got %+v", vs)
	}
}

// The one-level arena checkpoint (__heap_mark / __heap_release_to) is
// native-only: both natives rewind __fern_heap_ptr and snapshot the freelist
// heads into a .bss shadow, which wasm's linear-memory allocator has no room
// for below its head table. wasm must therefore reject the pair HERE, at check
// time — before the gate existed, a wasm build died inside the backend with
// `unknown callee "__fern_heap_mark"`, an internal message carrying an IR op
// index and no source position. `__heap_bump_bytes` stays ungated: reading the
// cursor works on every target, only rewinding it is native.
func TestEnforceHeapCheckpointNativeOnly(t *testing.T) {
	src := `function main(): i32 {
    var m: i64 = __heap_mark();
    __heap_release_to(m);
    return (__heap_bump_bytes() as i32);
}`
	for _, target := range []string{"x86-64", "arm64", "arm64-darwin", "arm64-android"} {
		t.Run(target+"/allowed", func(t *testing.T) {
			if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
				t.Errorf("native target %q should provide `arena`; violations = %+v", target, vs)
			}
		})
	}
	for _, target := range []string{"wasm", "wasi-http"} {
		t.Run(target+"/rejected", func(t *testing.T) {
			vs := platforms.Enforce(prepared(t, src, false), target)
			// Both calls are gated; __heap_bump_bytes is not.
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
				// The message must point at the targets that DO provide it,
				// not read as interp-only the way subprocess does.
				if msg := v.Message("<stdin>"); !strings.Contains(msg, "x86-64") {
					t.Errorf("message should list the providing targets: %s", msg)
				}
			}
		})
	}
}

// `log` and `now` are universal today — every descriptor grants both —
// so gating them must not reject anything. The point of the gate is the
// freestanding target that will not grant them (#6506), not a change to
// what the six hosted targets accept.
func TestEnforceLogAndClockUniversal(t *testing.T) {
	srcs := map[string]string{
		"print":  `function main(): i32 { print("hi"); return 0; }`,
		"eprint": `function main(): i32 { eprint("hi"); return 0; }`,
		"clock":  `function main(): i32 { return (now_unix_ms() as i32); }`,
		"sleep":  `function main(): i32 { sleep_ms(1); return 0; }`,
	}
	for name, src := range srcs {
		for _, target := range platforms.Targets() {
			t.Run(name+"/"+target, func(t *testing.T) {
				if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
					t.Errorf("%s on %s: unexpected violations: %+v", name, target, vs)
				}
			})
		}
	}
}

// The stdout STREAM is not universal: wasi-http is the proxy world,
// which has no wasi:cli/stdout to hand out. `print` still works there
// (it gates on `log`), which is the whole reason the two capabilities
// are distinct.
func TestEnforceStdoutStreamByTarget(t *testing.T) {
	srcs := map[string]string{
		"write":   `function main(): i32 { write("hi"); return 0; }`,
		"putchar": `function main(): i32 { putchar(65); return 0; }`,
		"handle":  `function main(): i32 { var w = stdout(); return 0; }`,
	}
	for name, src := range srcs {
		for _, target := range []string{"arm64", "arm64-darwin", "arm64-android", "x86-64", "wasm"} {
			t.Run(name+"/"+target, func(t *testing.T) {
				if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
					t.Errorf("%s on %s: unexpected violations: %+v", name, target, vs)
				}
			})
		}
		t.Run(name+"/wasi-http", func(t *testing.T) {
			vs := platforms.Enforce(prepared(t, src, false), "wasi-http")
			if len(vs) != 1 || vs[0].Capability != "stdout" {
				t.Fatalf("%s on wasi-http: violations = %+v, want one stdout violation", name, vs)
			}
		})
	}
}

// The completeness contract at the TARGET level (#6508), mirroring the
// one internal/caps holds at the package level. Every user-callable
// builtin the checker pre-declares is either gated on a capability or
// explicitly core — a new one that is neither fails here rather than
// silently landing on the hosted side of the freestanding boundary.
//
// The checker registry is the right universe because Enforce only runs
// for COMPILED targets; the interpreter is not a target and has no
// descriptor.
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

// argv exists only because a process was exec'd, so it is a capability
// of its own rather than part of `env`: the proxy world has envp and no
// argv. This is the same shape as the stdout finding in #6507.
func TestEnforceArgsNotOnProxyWorld(t *testing.T) {
	src := `function main(): i32 { var a: string[] = args(); return 0; }`
	for _, target := range []string{"arm64", "arm64-darwin", "arm64-android", "x86-64", "wasm"} {
		t.Run(target, func(t *testing.T) {
			if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
				t.Errorf("%s: unexpected violations: %+v", target, vs)
			}
		})
	}
	t.Run("wasi-http", func(t *testing.T) {
		vs := platforms.Enforce(prepared(t, src, false), "wasi-http")
		if len(vs) != 1 || vs[0].Capability != "args" {
			t.Fatalf("violations = %+v, want one args violation", vs)
		}
	})
}

// `env` and `random` are granted by every descriptor, so wiring them
// into the gate table rejects nothing today — they exist for the target
// that will not grant them.
func TestEnforceEnvAndRandomUniversal(t *testing.T) {
	srcs := map[string]string{
		"env":    `function main(): i32 { var v = env("HOME"); return 0; }`,
		"random": `function main(): i32 { return random_i32(); }`,
	}
	for name, src := range srcs {
		for _, target := range platforms.Targets() {
			t.Run(name+"/"+target, func(t *testing.T) {
				if vs := platforms.Enforce(prepared(t, src, false), target); len(vs) != 0 {
					t.Errorf("%s on %s: unexpected violations: %+v", name, target, vs)
				}
			})
		}
	}
}

// Table consistency: every capability named in the gate table is
// either provided by at least one target or is the documented
// interp-only case (subprocess). Guards against typos like gating on
// "filesystem" while descriptors say "fs".
func TestGatedCapabilitiesResolvable(t *testing.T) {
	caps := map[string]bool{}
	for _, name := range []string{"subprocess", "read_line", "stdin", "tcp_listen", "read_file", "stat", "temp_dir", "udp_send", "proc_exec", "__heap_mark", "__heap_release_to", "print", "eprint", "write", "putchar", "stdout", "stderr", "now_unix_ms", "now_ns", "monotonic_ns", "sleep_ms"} {
		c, ok := platforms.GatedBuiltin(name)
		if !ok {
			t.Errorf("expected %q to be gated", name)
			continue
		}
		caps[c] = true
	}
	for c := range caps {
		if c == "subprocess" {
			// Interp-only by design — no compiled provider today.
			if got := platforms.TargetsProviding(c); len(got) != 0 {
				t.Errorf("subprocess should have no compiled providers until a backend lowers it; got %v", got)
			}
			continue
		}
		if got := platforms.TargetsProviding(c); len(got) == 0 {
			t.Errorf("gated capability %q is provided by no target — descriptor/gate-table drift", c)
		}
	}
}
