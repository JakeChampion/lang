package e2ecompiler

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostShowcaseCore(t *testing.T) {
	path, err := filepath.Abs("../../../examples/showcase/tests/core.fern")
	if err != nil {
		t.Fatal(err)
	}
	testFractalTargets(t, path, true)
}

func TestSelfHostImportedCastTypes(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"main.fern": `import "./store"; function main(): i32 { return store.check(); }`,
		"store.fern": `import "std/option"; import "std/array";
trait Value { function number(self: Self): i32; }
struct Item { n: i32 }
impl Value for Item { function number(self: Self): i32 { return self.n; } }
pub function check(): i32 {
 let items = cell_new([] as Item[]);
 let current = cell_new(None as Option[Item]);
 items.set(items.get().append(Item { n: 42 }));
 current.set(Some(items.get()[0]));
 let Some(value) = current.get() else { return 3; };
 let boxed: dyn Value = value;
 let Some(item) = boxed as? Item else { return 1; };
 if (item.n != 42) { return 2; }
 return 0;
}`,
	})
	testFractalTargets(t, filepath.Join(dir, "main.fern"))
}

// Regression for #13149: return checks need the types of pattern bindings.
func TestSelfHostPatternCallbackReturnScope(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"main.fern": `function invoke(f: Option[(i32) => void]): void {
 if let Some(callback) = f {
  let nested = (): void => callback(21);
  nested();
 }
}
function main(): i32 {
 let total = cell_new(0);
 invoke(Some((n: i32): void => { total.set(total.get() + n); }));
 let result: Result[(i32) => i32, string] = Ok((n: i32): i32 => n * 2);
 match (result) {
  Ok(callback) when total.get() == 21 => {
   let nested = (): i32 => callback(total.get());
   if (nested() != 42) { return 1; }
  },
  _ => { return 2; }
 }
 return 0;
}`,
	})
	testFractalTargets(t, filepath.Join(dir, "main.fern"), true)
}

func TestSelfHostShowcaseWasm(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	entry, err := filepath.Abs("../../../examples/showcase/swarm.fern.md")
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "kernel.wasm")
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-emit", "core-module", "-O", "-o", output, entry, cli.stdlib)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Wasm build: %v\n%s", err, out)
	}
	if out, err := exec.Command(node, "../../../examples/showcase/tests/wasm.test.mjs", output).CombinedOutput(); err != nil {
		t.Fatalf("browser core: %v\n%s", err, out)
	}
}

// Regression for #13150: the internal assembler must encode both f32 memory operations.
func TestSelfHostWasmF32MemoryOpcodes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	cli := newStrictRuntimeCLI(t)
	output := emitCoreModule(t, cli, `import "std/array";
@export("fern:test/f32", "create")
function create(): f32[] { return [1.25 as f32, 2.5 as f32]; }
@export("fern:test/f32", "step")
function step(own p: f32[]): f32[] { return p.with(0, p[0] + 3.5 as f32); }
function main(): i32 { return 0; }`)
	const check = `const fs=require('node:fs'),assert=require('node:assert/strict');
(async()=>{const{instance:{exports:e}}=await WebAssembly.instantiate(fs.readFileSync(process.argv[1]),{wasi_snapshot_preview1:{proc_exit:c=>{throw Error(c)},fd_write:()=>0}});
const a=e['fern:test/f32#create'](),d=new Uint32Array(e.memory.buffer,a,2);
assert.deepEqual([...new Float32Array(e.memory.buffer,d[0],d[1])],[1.25,2.5]);
const b=e['fern:test/f32#step'](d[0],d[1]),r=new Uint32Array(e.memory.buffer,b,2);
assert.deepEqual([...new Float32Array(e.memory.buffer,r[0],r[1])],[4.75,2.5]);})().catch(e=>{console.error(e);process.exit(1)});`
	if out, err := exec.Command(node, "-e", check, output).CombinedOutput(); err != nil {
		t.Fatalf("f32 binary execution: %v\n%s", err, out)
	}
}
