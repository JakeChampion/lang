package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostCLIWitWorld composes a wasm32-wasi component against a world
// that -wit gives, and runs it with a provider of the world's own interface.
func TestSelfHostCLIWitWorld(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	write := func(path, text string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	const iface = "interface sink { sum-i32: func(data: list<s32>) -> s32; }"
	provWit := filepath.Join(dir, "provwit")
	write(filepath.Join(provWit, "sink.wit"), "package local:test@0.1.0;\n"+iface+"\nworld provider { export sink; }\n")
	write(filepath.Join(dir, "prov_core.wat"), `(module
  (memory (export "memory") 1)
  (global $h (mut i32) (i32.const 1024))
  (func (export "cabi_realloc") (param $op i32) (param $os i32) (param $al i32) (param $ns i32) (result i32)
    (local $p i32)
    (local.set $p (global.get $h))
    (global.set $h (i32.add (global.get $h) (local.get $ns)))
    (local.get $p))
  (func (export "local:test/sink@0.1.0#sum-i32") (param $ptr i32) (param $len i32) (result i32)
    (local $sum i32) (local $i i32)
    (block $d (loop $c
      (br_if $d (i32.ge_u (local.get $i) (local.get $len)))
      (local.set $sum (i32.add (local.get $sum)
        (i32.load (i32.add (local.get $ptr) (i32.mul (local.get $i) (i32.const 4))))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $c)))
    (local.get $sum)))`)
	provider := filepath.Join(dir, "provider.wasm")
	run(wasmtools, "parse", filepath.Join(dir, "prov_core.wat"), "-o", filepath.Join(dir, "prov_core.wasm"))
	run(wasmtools, "component", "embed", provWit, "-w", "provider", filepath.Join(dir, "prov_core.wasm"), "-o", filepath.Join(dir, "prov_embed.wasm"))
	run(wasmtools, "component", "new", filepath.Join(dir, "prov_embed.wasm"), "-o", provider)

	userWit := filepath.Join(dir, "userwit")
	if err := os.MkdirAll(userWit, 0o755); err != nil {
		t.Fatal(err)
	}
	run("cp", "-r", "../../../cmd/fern/wit/deps", filepath.Join(userWit, "deps"))
	write(filepath.Join(userWit, "deps", "test", "sink.wit"), "package local:test@0.1.0;\n"+iface+"\n")
	write(filepath.Join(userWit, "world.wit"), `package local:userworld@0.0.0;
world u {
    import wasi:cli/stdout@0.2.0;
    import wasi:cli/stderr@0.2.0;
    import wasi:cli/exit@0.2.0;
    import local:test/sink@0.1.0;
}
world bare {
    import wasi:cli/stdout@0.2.0;
    import wasi:cli/stderr@0.2.0;
    import wasi:cli/exit@0.2.0;
}
`)
	world := filepath.Join(dir, "u.wasm")
	run(wasmtools, "component", "embed", userWit, "-w", "u", "--dummy", "-o", world)
	bare := filepath.Join(dir, "bare.wasm")
	run(wasmtools, "component", "embed", userWit, "-w", "bare", "--dummy", "-o", bare)
	pkg := filepath.Join(dir, "pkg.wasm")
	run(wasmtools, "component", "wit", "--wasm", userWit, "-o", pkg)

	const want = "sum-ok"
	src := write(filepath.Join(dir, "sum.fern"), `@import("local:test/sink@0.1.0", "sum-i32")
function sum_i32(data: i32[]): i32;
function main(): i32 {
    let b: i32[] = [10, 20, 30];
    if (sum_i32(b) == 60) { write("`+want+`"); } else { write("sum-bad"); }
    return 0;
}
`)
	compile := func(target, out string, extra ...string) (string, int) {
		t.Helper()
		cmd := cli.compileCmd(target, out, src, extra...)
		got, _ := cmd.CombinedOutput()
		return string(got), cmd.ProcessState.ExitCode()
	}

	t.Run("runs-against-the-user-world", func(t *testing.T) {
		user := filepath.Join(dir, "user.wasm")
		if out, code := compile("wasm32-wasi", user, "-wit", world); code != 0 {
			t.Fatalf("compile with -wit: exit %d\n%s", code, out)
		}
		witOut, err := exec.Command(wasmtools, "component", "wit", user).CombinedOutput()
		if err != nil {
			t.Fatalf("wasm-tools component wit: %v\n%s", err, witOut)
		}
		if !strings.Contains(string(witOut), "import local:test/sink@0.1.0;") {
			t.Errorf("component does not import the user interface:\n%s", witOut)
		}
		if strings.Contains(string(witOut), "wasi:filesystem") {
			t.Errorf("component imports an interface outside the user world:\n%s", witOut)
		}
		final := filepath.Join(dir, "final.wasm")
		run(wasmtools, "compose", user, "--definitions", provider, "-o", final)
		run(wasmtools, "validate", final)
		out, err := exec.Command(wasmtime, "run", final).CombinedOutput()
		if err != nil {
			t.Fatalf("wasmtime run: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), want) {
			t.Fatalf("stdout = %q, want it to contain %q", out, want)
		}
	})

	t.Run("without-wit-the-fern-world-refuses", func(t *testing.T) {
		out, code := compile("wasm32-wasi", filepath.Join(dir, "fern.wasm"))
		if code != 2 || !strings.Contains(out, "the fern world cannot take this program") {
			t.Fatalf("exit %d, want 2 and the fern world refusal:\n%s", code, out)
		}
	})

	for _, tc := range []struct {
		name, target, wit, want string
		extra                   []string
	}{
		{"world-without-the-interface", "wasm32-wasi", bare, "the world in " + bare + " cannot take this program: core imports interface local:test/sink@0.1.0 not declared by the world", nil},
		{"package-of-several-worlds", "wasm32-wasi", pkg, "a WIT package with more than one world or interface is not a world", nil},
		{"not-wasm", "wasm32-wasi", src, "it is not a wasm module or component", nil},
		{"missing-file", "wasm32-wasi", filepath.Join(dir, "absent.wasm"), "cannot read " + filepath.Join(dir, "absent.wasm"), nil},
		{"native-target", "x86-64-linux", world, "-wit gives the world of a wasm32-wasi component; -target x86-64-linux composes no component", nil},
		{"core-module", "wasm32-wasi", world, "-wit gives the world of a component; -emit core-module writes no component", []string{"-emit", "core-module"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extra := append([]string{"-wit", tc.wit}, tc.extra...)
			out, code := compile(tc.target, filepath.Join(dir, tc.name+".out"), extra...)
			if code != 2 || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d, want 2 and %q:\n%s", code, tc.want, out)
			}
		})
	}
}
