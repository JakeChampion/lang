package platforms_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
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

// Table consistency: every capability named in the gate table is
// either provided by at least one target or is the documented
// interp-only case (subprocess). Guards against typos like gating on
// "filesystem" while descriptors say "fs".
func TestGatedCapabilitiesResolvable(t *testing.T) {
	caps := map[string]bool{}
	for _, name := range []string{"subprocess", "read_line", "stdin", "tcp_listen", "read_file", "stat", "temp_dir", "udp_send"} {
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
