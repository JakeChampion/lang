package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The self-host half of #9684: a module whose FILE NAME is not an identifier.
//
// Both compilers derive a non-entry module's mangle prefix from its basename,
// and both used it verbatim, so `[.fern` produced `__fn_[__help_text` and the
// assembler refused it. The two have to agree about the sanitised form as well
// as about the raw one — a disagreement is an unresolved symbol rather than a
// diagnostic — which is why this leg exists beside the native one in
// internal/e2e rather than instead of it.
//
// The helper is deliberately big: a one-line private function is inlined away
// and never emitted, which is how three smaller reductions of this bug all
// built cleanly.
const selfHostMangleBracket = `function help_text(): string {
    var out: string = "";
    out = out + "line 00\n"; out = out + "line 01\n"; out = out + "line 02\n";
    out = out + "line 03\n"; out = out + "line 04\n"; out = out + "line 05\n";
    out = out + "line 06\n"; out = out + "line 07\n"; out = out + "line 08\n";
    out = out + "line 09\n"; out = out + "line 10\n"; out = out + "line 11\n";
    out = out + "line 12\n"; out = out + "line 13\n"; out = out + "line 14\n";
    out = out + "line 15\n"; out = out + "line 16\n"; out = out + "line 17\n";
    out = out + "line 18\n"; out = out + "line 19\n"; out = out + "line 20\n";
    out = out + "line 21\n"; out = out + "line 22\n"; out = out + "line 23\n";
    out = out + "line 24\n"; out = out + "line 25\n"; out = out + "line 26\n";
    out = out + "line 27\n"; out = out + "line 28\n"; out = out + "line 29\n";
    out = out + "line 30\n"; out = out + "line 31\n"; out = out + "line 32\n";
    out = out + "line 33\n"; out = out + "line 34\n"; out = out + "line 35\n";
    out = out + "line 36\n"; out = out + "line 37\n"; out = out + "line 38\n";
    out = out + "line 39\n";
    return out;
}

pub function answer(): i32 { return help_text().len() / 8; }
`

const selfHostMangleMain = `import "./[" as bracket;

function main(): i32 { return bracket.answer(); }
`

const selfHostMangleWant = 40

// The self-host leg of the native collision fixture.
//
// Sanitising is not injective: `[.fern` and `_.fern` both reduce to `_`. Native
// has had a collision loop in assignManglePrefixes all along; the self-host
// bundler had none, so sanitising alone would have made these two modules
// mangle their decls to the same symbol — a divergence introduced by the fix
// rather than by the input. Both compilers now suffix the second one.
const selfHostMangleCollisionMain = `import "./[" as bracket;
import "./_" as under;

function main(): i32 { return bracket.answer() + under.answer(); }
`

func TestSelfHostModuleNameMangleCollision(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	for name, src := range map[string]string{
		"[.fern":    selfHostMangleBracket,
		"_.fern":    selfHostMangleBracket,
		"main.fern": selfHostMangleCollisionMain,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	entry := filepath.Join(dir, "main.fern")
	for _, tg := range h.targets {
		t.Run(tg.target, func(t *testing.T) {
			out := filepath.Join(dir, "prog-"+tg.target)
			h.compileWith(t, tg, entry, out)
			var cmd *exec.Cmd
			if len(tg.runner) == 0 {
				cmd = exec.Command(out)
			} else {
				cmd = exec.Command(tg.runner[0], append(append([]string{}, tg.runner[1:]...), out)...)
			}
			_, _ = cmd.CombinedOutput()
			// Both modules' answer(), so a collision that lost one definition
			// would not reach this number even if it linked.
			if got, want := cmd.ProcessState.ExitCode(), 2*selfHostMangleWant; got != want {
				t.Errorf("exit code: got %d, want %d", got, want)
			}
		})
	}
}

func TestSelfHostModuleNameMangle(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	for name, src := range map[string]string{
		"[.fern":    selfHostMangleBracket,
		"main.fern": selfHostMangleMain,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	entry := filepath.Join(dir, "main.fern")
	for _, tg := range h.targets {
		t.Run(tg.target, func(t *testing.T) {
			out := filepath.Join(dir, "prog-"+tg.target)
			h.compileWith(t, tg, entry, out)
			var cmd *exec.Cmd
			if len(tg.runner) == 0 {
				cmd = exec.Command(out)
			} else {
				cmd = exec.Command(tg.runner[0], append(tg.runner[1:], out)...)
			}
			_, _ = cmd.CombinedOutput()
			if got := cmd.ProcessState.ExitCode(); got != selfHostMangleWant {
				t.Errorf("exit code: got %d, want %d", got, selfHostMangleWant)
			}
		})
	}
}
