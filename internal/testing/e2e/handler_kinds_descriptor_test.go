package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var handlerKindPrograms = map[string]string{
	"handle": `import "std/http";
import "std/serve";
import "std/platform";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    return http.ok("hi");
}
`,
	"main": "function main(): i32 { return 0; }\n",
}

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
