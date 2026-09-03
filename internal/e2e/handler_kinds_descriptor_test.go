package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/platforms"
)

// The two entry shapes a program can be written in today. `handle` gets the
// synthesised main; `main` is the program writing its own.
var handlerKindPrograms = map[string]string{
	"handle": `import "std/http";
import "std/tcp";
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return http.http_response_ok("hi");
}
`,
	"main": "function main(): i32 { return 0; }\n",
}

// TestHandlerKindsMatchWhatTheCompilerAccepts pins Descriptor.HandlerKinds to
// what the compiler will actually build, in BOTH directions: a declared kind
// that no longer compiles fails, and a kind that compiles without being
// declared fails too.
//
// The second direction is the one that matters. HandlerKinds shipped as a
// purely declarative field -- `docs/PLATFORM-RESEARCH.md` Rec §5 is what will
// consume it -- and because nothing read it, every one of the six emitting
// targets declared a single kind while accepting both. A test that pinned the
// declared list against a hand-written table would have encoded the same wrong
// values; only compiling separates the two.
//
// It is also the gate a new kind lands against: adding `scheduled` or `alarm`
// to a descriptor without the entry-point recognition to match fails here
// rather than at whatever tries to invoke it.
func TestHandlerKindsMatchWhatTheCompilerAccepts(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a program per target per handler kind; not a -short test")
	}
	bin := e2eharness.BuildLangBinForInterp(t)
	stdlib := langSrcAbs(t, "internal/stdlib")

	for _, target := range platforms.Targets() {
		d := platforms.ForTarget(target)
		if d == nil || d.NoBackend {
			continue
		}
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			var accepted []string
			for _, kind := range sortedKeys(handlerKindPrograms) {
				src := filepath.Join(dir, kind+".fern")
				if err := os.WriteFile(src, []byte(handlerKindPrograms[kind]), 0o644); err != nil {
					t.Fatalf("write %s: %v", src, err)
				}
				out := filepath.Join(dir, kind+".out")
				cmd := exec.Command(bin, "-target", target, "-o", out, src, stdlib)
				if b, err := cmd.CombinedOutput(); err == nil {
					accepted = append(accepted, kind)
				} else {
					t.Logf("%s rejects %q: %v\n%s", target, kind, err, b)
				}
			}
			declared := append([]string(nil), d.HandlerKinds...)
			sort.Strings(declared)
			sort.Strings(accepted)
			if !equalStrings(declared, accepted) {
				t.Errorf("%s: descriptor declares handler kinds %v, compiler accepts %v\n"+
					"the descriptor is what Rec §5 will dispatch on, so a difference here is a\n"+
					"target that either advertises an entry shape it cannot build or builds one\n"+
					"it does not advertise", target, declared, accepted)
			}
		})
	}
}

// TestHandlerKindsCanonicalFirst pins the ordering contract the field's own
// doc-comment states: the FIRST entry is canonical, and auto-`main` synthesis
// targets it. A target whose canonical kind changed by accident -- a sort, an
// append landing at the front -- would otherwise be invisible until something
// dispatched on it.
func TestHandlerKindsCanonicalFirst(t *testing.T) {
	want := map[string]string{
		"arm64-linux":      "handle",
		"arm64-darwin":     "handle",
		"arm64-android":    "handle",
		"x86-64-linux":     "handle",
		"wasm32-wasi":      "main",
		"wasm32-wasi-http": "handle",
	}
	for target, canonical := range want {
		d := platforms.ForTarget(target)
		if d == nil {
			t.Errorf("no descriptor for %s", target)
			continue
		}
		if len(d.HandlerKinds) == 0 {
			t.Errorf("%s declares no handler kinds", target)
			continue
		}
		if d.HandlerKinds[0] != canonical {
			t.Errorf("%s canonical handler kind = %q, want %q (first entry is what auto-main synthesis targets)",
				target, d.HandlerKinds[0], canonical)
		}
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
