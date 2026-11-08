package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostPrimitiveDisplayRoots(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []struct {
		name, source, want string
		pruned             []string
	}{
		{"typed-i32", `function main():i32 { let n:i32=args().len()+6; write(n.to_string()); return 0; }`, "7", []string{"f32", "f64", "char", "u64"}},
		{"typed-i64", `function main():i32 { let n:i64=args().len() as i64+6i64; write(n.to_string()); return 0; }`, "7", []string{"f32", "f64", "char"}},
		{"display-lowering", `function main():i32 { let n:i32=args().len()+6; write(n); return 0; }`, "7", []string{"f32", "f64", "char", "u64"}},
		{"float-live", `function main():i32 { let n:f64=args().len() as f64+0.25; write(n.to_string()); return 0; }`, "1.25", nil},
		{"generic-primitive", `import "core/cmp";
function show[T:cmp.Display](value:T):string {return value.to_string();}
function main():i32 {write(show(7));write("|");write(show(1.25));return 0;}`, "7|1.25", nil},
		{"generic-struct", `trait Display {function to_string(self:Self):string;}
struct Label {text:string}
impl Display for Label {function to_string(self:Self):string {return self.text;}}
function show[T:Display](value:T):string {return value.to_string();}
function main():i32 {write(show(Label {text:"label"}));return 0;}`, "label", nil},
		{"function-value", `function convert(value:i32):string {return value.to_string();}
function main():i32 {let f:(i32)=>string=convert;write(f(args().len()+6));return 0;}`, "7", []string{"f32", "f64", "char"}},
		{"module-free-function", `import "std/i32";
function main():i32 {match(i32.parse_i32_radix("7",10)){Ok(value)=>write(value.to_string()),Err(_)=>{return 1;}}return 0;}`, "7", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-android", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					assembly := filepath.Join(t.TempDir(), "main.s")
					cmd := exec.Command(cli.bin, "-stdlib", cli.stdlib, "-target", target, "-emit", "asm", "-o", assembly, src)
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("compile: %v\n%s", err, out)
					}
					text, err := os.ReadFile(assembly)
					if err != nil {
						t.Fatal(err)
					}
					for _, name := range tc.pruned {
						if strings.Contains(string(text), "__primitive_"+name+"_to_string") {
							t.Errorf("unused %s conversion survived a typed display root", name)
						}
					}
					if target == "arm64-android" {
						return
					}
					var run *exec.Cmd
					if target == "wasm32-wasi" {
						run = exec.Command("wasmtime", "run", cli.emit(t, src, target))
					} else {
						run = runX86_64Bin(cli.runner, cli.x86Binary(t, src))
					}
					out, err := run.CombinedOutput()
					if err != nil || string(out) != tc.want {
						t.Fatalf("run: %v, output %q, want %q", err, out, tc.want)
					}
				})
			}
		})
	}
}
