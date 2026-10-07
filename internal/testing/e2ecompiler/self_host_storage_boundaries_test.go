package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const storageBoundaryHelpers = `
function state(p: i32, f: i32, w: i32, c: i32, b: i32): storage_core.State {
  match (storage_core.new_state(p,f,w,c,b)) {
    Some(s) => { return s; }, None => { exit(90); return state(1,1,1,1,1); }
  }
}
function snapshot(s: storage_core.State): storage_core.State { return s; }
fip function transaction(own s: storage_core.State, kind: i32, page: i32, offset: i32, value: i64, fault: boolean): storage_core.State {
  s=storage_fip.submit(s,kind,page,offset,value,0,fault);
  if(s.status!=0) { return s; }
  let slot:i32=s.result_slot; let ticket:i64=s.result_ticket;
  let steps:i32=0;
  while(s.phase[slot]!=3 && steps<2*s.capacity) { s=storage_fip.advance(s); steps=steps+1; }
  return storage_fip.collect(s,slot,ticket);
}
function same_data(a: storage_core.State,b: storage_core.State):boolean {
  let i:i32=0;
  while(i<a.disk.len()) { if(a.disk[i]!=b.disk[i]) {return false;} i=i+1; }
  i=0; while(i<a.cache.len()) { if(a.cache[i]!=b.cache[i]) {return false;} i=i+1; }
  i=0; while(i<a.frames) {if(a.mapped[i]!=b.mapped[i] || a.dirty[i]!=b.dirty[i] || a.touched[i]!=b.touched[i]) {return false;} i=i+1;}
  return true;
}
`

func TestSelfHostStorageBoundaries(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, source string }{
		{"lru_and_tie", `
function main():i32 {
  let s:storage_core.State=state(4,2,3,1,1); let mark:i64=__heap_alloc_count();
  s=transaction(s,0,0,2,0i64,false); s=transaction(s,0,1,2,0i64,false);
  if(s.mapped[0]!=0 || s.mapped[1]!=1 || s.result_value!=20002i64) {return 1;}
  s=transaction(s,0,0,0,0i64,false); s=transaction(s,0,2,1,0i64,false);
  if(s.mapped[0]!=0 || s.mapped[1]!=2 || s.result_value!=30001i64) {return 2;}
  s=storage_core.State{...s,touched:s.touched.with(0,7i64)};
  s=storage_core.State{...s,touched:s.touched.with(1,7i64)};
  s=transaction(s,0,3,0,0i64,false);
  if(s.mapped[0]!=3 || s.mapped[1]!=2 || s.cache[0]!=40000i64 || s.cache[3]!=30000i64) {return 3;}
  s=transaction(s,3,3,0,0i64,false);
  if(s.status!=0 || s.mapped[0]!=-1 || s.touched[0]!=0i64) {return 4;}
  s=transaction(s,0,1,0,0i64,false);
  if(s.mapped[0]!=1 || s.mapped[1]!=2 || __heap_alloc_count()!=mark) {return 5;}
  return 0;
}`},
		{"faults_and_snapshots", `
function main():i32 {
  let kind:i32=0;
  while(kind<4) {
    let s:storage_core.State=state(3,2,3,2,2);
    s=transaction(s,0,0,0,0i64,false);
    if(kind==2) {s=transaction(s,1,0,2,-9223372036854775808i64,false);}
    let saved:storage_core.State=snapshot(s);
    s=transaction(s,kind,0,2,9223372036854775807i64,true);
    if(s.status!=14 || s.outstanding!=0 || !same_data(s,saved) || s.frame_pin[0]!=0 || s.page_pin[0]!=0) {return 1;}
    if(saved.phase[0]!=0 || saved.outstanding!=0 || saved.ticket[0]!=saved.serial || saved.tick+2i64!=s.tick) {return 2;}
    s=transaction(s,kind,0,2,9223372036854775807i64,false);
    if(s.status!=0) {return 3;}
    if(kind==0 && s.result_value!=10002i64) {return 4;}
    if(kind==1 && (s.cache[2]!=9223372036854775807i64 || s.disk[2]!=10002i64 || s.dirty[0]!=1)) {return 5;}
    if(kind==2 && (s.disk[2]!=-9223372036854775808i64 || s.dirty[0]!=0)) {return 6;}
    if(kind==3 && s.mapped[0]!=-1) {return 7;}
    if(saved.disk[2]!=10002i64 || saved.mapped[0]!=0 || saved.phase[0]!=0) {return 8;}
    kind=kind+1;
  }
  // A failed miss must keep the victim's page and full contents available.
  let s:storage_core.State=state(2,1,3,1,1);
  s=transaction(s,0,0,2,0i64,false); let mark:i64=__heap_alloc_count();
  s=transaction(s,0,1,2,0i64,true);
  if(s.status!=14 || s.mapped[0]!=0 || s.cache[2]!=10002i64 || s.frame_pin[0]!=0 || s.page_pin[1]!=0 || s.touched[0]!=2i64) {return 9;}
  s=transaction(s,1,1,2,99i64,true);
  if(s.status!=14 || s.mapped[0]!=0 || s.cache[2]!=10002i64 || s.dirty[0]!=0 || __heap_alloc_count()!=mark) {return 10;}
  return 0;
}`},
		{"completion_backlog_and_order", `
function main():i32 {
  let s:storage_core.State=state(3,2,1,2,1); let mark:i64=__heap_alloc_count();
  s=storage_fip.submit(s,0,0,0,0i64,4,false);
  s=storage_fip.submit(s,0,1,0,0i64,0,false);
  s=storage_fip.advance(s); s=storage_fip.advance(s);
  if(s.phase[0]!=2 || s.phase[1]!=2 || s.due[0]!=5i64 || s.due[1]!=2i64) {return 1;}
  s=storage_fip.advance(s); s=storage_fip.advance(s);
  if(s.phase[0]!=2 || s.phase[1]!=3 || s.work!=1 || s.transitions!=1) {return 2;}
  s=storage_fip.submit(s,0,2,0,0i64,0,false);
  if(s.status!=2 || s.outstanding!=2 || s.serial!=2i64) {return 3;}
  s=storage_fip.collect(s,0,1i64); if(s.status!=4) {return 4;}
  s=storage_fip.collect(s,1,2i64); if(s.status!=0 || s.result_value!=20000i64) {return 5;}
  s=storage_fip.submit(s,0,0,0,0i64,0,false);
  if(s.result_slot!=1 || s.result_ticket!=3i64) {return 6;}
  s=storage_fip.collect(s,1,2i64); if(s.status!=3 || s.phase[1]!=1 || s.outstanding!=2) {return 7;}
  s=storage_fip.advance(s); if(s.phase[0]!=3 || s.tick!=5i64) {return 8;}
  s=storage_fip.advance(s); s=storage_fip.advance(s); s=storage_fip.advance(s);
  s=storage_fip.collect(s,0,1i64); s=storage_fip.collect(s,1,3i64);
  if(s.outstanding!=0 || s.result_value!=10000i64 || __heap_alloc_count()!=mark) {return 9;}
  // Conflict refuses rather than modifying a pinned page.
  s=storage_fip.submit(s,1,0,0,9i64,2,false); s=storage_fip.submit(s,1,0,0,10i64,0,false);
  s=storage_fip.advance(s); s=storage_fip.advance(s);
  if(s.phase[1]!=3 || s.completion_status[1]!=10 || s.cache[0]!=10000i64) {return 10;}
  return 0;
}`},
		{"capacity_and_extremes", `
function main():i32 {
  let invalid:i32[]=[-2147483648,-1,0,257,2147483647]; let i:i32=0; let mark:i64=__heap_alloc_count();
  while(i<invalid.len()) {
    match(storage_core.new_state(invalid[i],1,1,1,1)) {Some(s)=>{return 1;},None=>{}}
    match(storage_core.new_state(1,1,invalid[i],1,1)) {Some(s)=>{return 2;},None=>{}}
    match(storage_core.new_state(1,1,1,invalid[i],1)) {Some(s)=>{return 3;},None=>{}}
    match(storage_core.new_state(1,1,1,1,invalid[i])) {Some(s)=>{return 4;},None=>{}}
    match(storage_core.new_state(256,invalid[i],1,1,1)) {Some(s)=>{return 5;},None=>{}}
    i=i+1;
  }
  match(storage_core.new_state(256,129,1,1,1)) {Some(s)=>{return 6;},None=>{}}
  match(storage_core.new_state(1,1,1,129,1)) {Some(s)=>{return 7;},None=>{}}
  match(storage_core.new_state(1,2,1,1,1)) {Some(s)=>{return 8;},None=>{}}
  match(storage_core.new_state(1,1,1,1,2)) {Some(s)=>{return 9;},None=>{}}
  if(__heap_alloc_count()!=mark) {return 10;}
  let s:storage_core.State=state(256,128,256,128,128); mark=__heap_alloc_count();
  s=transaction(s,1,255,255,-9223372036854775808i64,false); s=transaction(s,2,255,0,0i64,false);
  if(s.disk[65535]!=-9223372036854775808i64 || s.work!=128 || s.outstanding!=0 || __heap_alloc_count()!=mark) {return 11;}
  let small:storage_core.State=state(1,1,1,1,1);
  small=storage_core.State{...small,serial:9223372036854775806i64}; mark=__heap_alloc_count();
  small=transaction(small,0,0,0,0i64,false);
  if(small.status!=0 || small.result_ticket!=9223372036854775807i64) {return 12;}
  small=storage_fip.submit(small,0,0,0,0i64,0,false);
  if(small.status!=5 || small.outstanding!=0 || __heap_alloc_count()!=mark) {return 13;}
  let clock:storage_core.State=state(1,1,1,1,1);
  clock=storage_core.State{...clock,tick:9223372036854775805i64}; mark=__heap_alloc_count();
  clock=storage_fip.submit(clock,0,0,0,0i64,2,false); clock=storage_fip.advance(clock);
  if(clock.phase[0]!=3 || clock.completion_status[0]!=15 || clock.frame_pin[0]!=0 || clock.page_pin[0]!=0) {return 14;}
  clock=storage_fip.collect(clock,0,1i64);
  clock=storage_fip.submit(clock,0,0,0,0i64,0,false); clock=storage_fip.advance(clock);
  if(clock.tick!=9223372036854775807i64 || clock.phase[0]!=2) {return 15;}
  clock=storage_fip.advance(clock);
  if(clock.status!=5 || clock.work!=0 || clock.transitions!=0 || clock.phase[0]!=2) {return 16;}
  clock=storage_fip.submit(clock,0,0,0,0i64,0,false);
  if(clock.status!=5 || clock.serial!=2i64 || __heap_alloc_count()!=mark) {return 17;}
  return 0;
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"storage_core.fern", "storage_fip.fern"} {
				data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", name))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(path, []byte("import \"./storage_core\";\nimport \"./storage_fip\";\n"+storageBoundaryHelpers+tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		})
	}
}

func TestSelfHostStorageRejectsFreshCompletion(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, claim := range []string{"fip", "fbip"} {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(claim+"/"+target, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "main.fern")
				source := fmt.Sprintf("struct Completion { value:i64 }\n%s function fresh(v:i64):Completion {return Completion{value:v};}\nfunction main():i32 {return fresh(0i64).value as i32;}\n", claim)
				if err := os.WriteFile(path, []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := runX86_64Bin(cli.runner, cli.bin, "-target", target, "-emit", "asm", "-o", filepath.Join(dir, "out"), path, cli.stdlib)
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "E068") {
					t.Fatalf("expected fresh completion rejection, got %v: %s", err, out)
				}
			})
		}
	}
}
