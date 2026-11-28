package e2ecompiler

import (
	"os/exec"
	"testing"
)

// An @export function that takes or returns u8[] lifts and lowers the bytes
// through the canonical ABI, and the core module exports cabi_realloc so a
// host that instantiates it directly can pass them. The browser calls the
// showcase kernels this way.
func TestSelfHostExportByteList(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	cli := newStrictRuntimeCLI(t)
	output := emitCoreModule(t, cli, `import "std/array";
@export("fern:test/bytes", "reverse")
function reverse(data: u8[]): u8[] {
  let out: u8[] = [];
  let i = data.len() - 1;
  while (i >= 0) {
    out = out.append(data[i]);
    i = i - 1;
  }
  return out;
}
@export("fern:test/bytes", "join")
function join(a: u8[], b: u8[]): u8[] { return a.concat(b); }
@export("fern:test/bytes", "tail")
function tail(data: u8[]): u8[] { return data.drop(1); }
@export("fern:test/bytes", "value")
function value(n: i32): u8[] { return [n as u8, 1u8]; }
@export("fern:test/bytes", "wide")
function wide(n: i32): i64[] { return [n as i64, -1i64]; }
@export("fern:test/bytes", "text")
function text(n: i32): string { return "number " + n.to_string(); }
@export("fern:test/bytes", "invert")
function invert(data: boolean[]): boolean[] {
  let out: boolean[] = [];
  for x in data { out = out.append(!x); }
  return out;
}
@export("fern:test/bytes", "sum")
function sum(data: u8[]): i32 {
  let total = 0;
  for b in data { total = total + b as i32; }
  return total;
}
function main(): i32 { return 0; }`)
	const check = `const fs=require('node:fs'),assert=require('node:assert/strict');
(async()=>{const{instance:{exports:e}}=await WebAssembly.instantiate(fs.readFileSync(process.argv[1]),{wasi_snapshot_preview1:{proc_exit:c=>{throw Error(c)},fd_write:()=>0}});
assert.equal(typeof e.cabi_realloc,'function');
const put=b=>{const p=e.cabi_realloc(0,0,1,b.length);new Uint8Array(e.memory.buffer).set(b,p);return p;};
const take=r=>{const w=new Uint32Array(e.memory.buffer,r,2);return[...new Uint8Array(e.memory.buffer,w[0],w[1])];};
const call=(n,...lists)=>e['fern:test/bytes#'+n](...lists.flatMap(b=>[put(b),b.length]));
assert.deepEqual(take(call('reverse',[1,2,3,250])),[250,3,2,1]);
assert.deepEqual(take(call('reverse',[])),[]);
assert.deepEqual(take(call('join',[1,2],[3,4,5])),[1,2,3,4,5]);
assert.deepEqual(take(call('tail',[9,8,7])),[8,7]);
assert.equal(call('sum',[200,100,55]),355);
function result(name, i, read) {
  const r = e['fern:test/bytes#' + name](i);
  const [p, n] = new Uint32Array(e.memory.buffer, r, 2);
  const value = read(p, n);
  e['cabi_post_fern:test/bytes#' + name](r);
  return value;
}
function round(i) {
  const bits = i % 3 === 0 ? [] : [1, 0, i & 1];
  const r = call('invert', bits);
  assert.deepEqual(take(r), bits.map(x => 1-x));
  e['cabi_post_fern:test/bytes#invert'](r);
  assert.deepEqual(result('value', i, (p,n) => [...new Uint8Array(e.memory.buffer,p,n)]), [i & 255,1]);
  assert.deepEqual(result('wide', i, (p,n) => [...new BigInt64Array(e.memory.buffer,p,n)]), [BigInt(i),-1n]);
  assert.equal(result('text', i, (p,n) => new TextDecoder().decode(new Uint8Array(e.memory.buffer,p,n))), 'number ' + i);
}
for (let i=0; i<100; i++) round(i);
const bytes = e.memory.buffer.byteLength;
for (let i=0; i<10000; i++) round(i);
assert.equal(e.memory.buffer.byteLength, bytes);
for(let i=0;i<500;i++)assert.equal(take(call('reverse',[i&255,1])).length,2);})().catch(e=>{console.error(e);process.exit(1)});`
	if out, err := exec.Command(node, "-e", check, output).CombinedOutput(); err != nil {
		t.Fatalf("byte-list exports: %v\n%s", err, out)
	}
}
