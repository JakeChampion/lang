package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func kvConfigSource(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	core, err := os.ReadFile(filepath.Join(repoRootFromTest(t), "examples/fip/kv_config_core.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kv_config_core.fern"), core, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte("import \"./kv_config_core\";\n"+kvConfigMatches+source), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostKVConfigBoundaries(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := `
function zeros(n:i32):u8[] {let a:u8[]=__alloc_u8(n);let i:i32=0;while(i<n){a=a.with(i,0 as u8);i=i+1;}return a;}
function unchanged(a:kv_config_core.Db,b:kv_config_core.Db):boolean {return same_snapshot(a,kv_config_core.Db{...b,refused:a.refused});}
function main():i32 {
  let mark:i64=__heap_alloc_count();
  match(kv_config_core.new_db(0,1,8,1)){Some(db)=>{return 1;},None=>{}}
  match(kv_config_core.new_db(4097,1,8,1)){Some(db)=>{return 2;},None=>{}}
  match(kv_config_core.new_db(1,0,8,1)){Some(db)=>{return 3;},None=>{}}
  match(kv_config_core.new_db(1,65,8,1)){Some(db)=>{return 4;},None=>{}}
  match(kv_config_core.new_db(1,1,7,1)){Some(db)=>{return 5;},None=>{}}
  match(kv_config_core.new_db(1,1,257,1)){Some(db)=>{return 6;},None=>{}}
  match(kv_config_core.new_db(1,1,8,0)){Some(db)=>{return 7;},None=>{}}
  match(kv_config_core.new_db(1,1,8,1025)){Some(db)=>{return 8;},None=>{}}
  match(kv_config_core.new_db(-2147483648,2147483647,2147483647,2147483647)){Some(db)=>{return 9;},None=>{}}
  if(__heap_alloc_count()!=mark){return 10;}
  match(kv_config_core.new_db(3,1,9,2)){None=>{return 11;},Some(db)=>{
    let put:u8[]=[1 as u8,255 as u8,255 as u8,255 as u8,255 as u8,255 as u8,255 as u8,255 as u8,255 as u8,255 as u8,77 as u8];
    let increment:u8[]=[4 as u8,255 as u8,1 as u8,0 as u8,0 as u8,0 as u8,0 as u8,0 as u8,0 as u8,0 as u8,99 as u8];
    let bad:u8[]=put;bad=bad.append(0 as u8);let i:i32=0;while(i<10){bad=bad.append(0 as u8);i=i+1;}
    mark=__heap_alloc_count();db=kv_config_core.process(db,put,0,1);db=kv_config_core.process(db,increment,0,1);
    if(__heap_alloc_count()!=mark || db.out_count!=10 || db.output[0]!=4 as u8 || db.output[9]!=77 as u8){return 12;}
    i=1;while(i<9){if(db.output[i]!=0 as u8){return 13;}i=i+1;}
    let before:kv_config_core.Db=saved(db);
    db=kv_config_core.process(db,bad,0,2);if(!db.refused || !unchanged(before,db)){return 14;}
    db=kv_config_core.process(db,put,-1,1);if(!db.refused || !unchanged(before,db)){return 15;}
    db=kv_config_core.process(db,put,2147483647,1);if(!db.refused || !unchanged(before,db)){return 16;}
    db=kv_config_core.process(db,put,0,-1);if(!db.refused || !unchanged(before,db)){return 17;}
    db=kv_config_core.process(db,put,0,2147483647);if(!db.refused || !unchanged(before,db)){return 18;}
    db=kv_config_core.process(db,put,1,1);if(!db.refused || !unchanged(before,db)){return 19;}
    db=kv_config_core.process(db,put,put.len(),0);if(db.refused || db.out_count!=0 || db.live!=1 || before.out_count!=10){return 20;}
  }}
  // Keys7,15,23 all start at slot7 in an eight-slot table. Deleting the
  // middle and then the first entry exercises the wrapped cluster explicitly.
  match(kv_config_core.new_db(3,1,8,3)){None=>{return 30;},Some(db)=>{
    let input:u8[]=zeros(30);input=input.with(0,1 as u8);input=input.with(1,7 as u8);input=input.with(2,70 as u8);
    input=input.with(10,1 as u8);input=input.with(11,15 as u8);input=input.with(12,150 as u8);
    input=input.with(20,1 as u8);input=input.with(21,23 as u8);input=input.with(22,230 as u8);
    mark=__heap_alloc_count();db=kv_config_core.process(db,input,0,3);
    if(db.keys[7]!=7 as u8 || db.keys[0]!=15 as u8 || db.keys[1]!=23 as u8){return 31;}
    input=input.with(10,3 as u8);db=kv_config_core.process(db,input,10,1);
    if(db.live!=2 || db.keys[0]!=23 as u8 || db.used[1]!=0 || kv_config_core.find(db,input,21)!=0){return 32;}
    input=input.with(0,3 as u8);db=kv_config_core.process(db,input,0,1);
    if(db.live!=1 || db.keys[7]!=23 as u8 || db.used[0]!=0 || kv_config_core.find(db,input,21)!=7 || db.values[56]!=230 as u8){return 33;}
    if(__heap_alloc_count()!=mark){return 34;}
  }}
  // Different dimensions coexist, including every upper limit. Fill all4096
  // distinct binary keys in four1024-record batches and reject a new key.
  match(kv_config_core.new_db(4096,64,256,1024)){None=>{return 21;},Some(db)=>{
    match(kv_config_core.new_db(1,1,8,1)){None=>{return 22;},Some(other)=>{
      let input:u8[]=zeros(321*1024);let i:i32=0;while(i<1024){input=input.with(i*321,1 as u8);i=i+1;}
      let batch:i32=0;mark=__heap_alloc_count();
      while(batch<4){i=0;while(i<1024){let id:i32=batch*1024+i;input=input.with(i*321+1,(id & 255) as u8);input=input.with(i*321+2,(id >> 8) as u8);input=input.with(i*321+65,(id & 255) as u8);i=i+1;}
        db=kv_config_core.process(db,input,0,1024);if(db.refused || db.live!=(batch+1)*1024 || db.out_count!=1024){return 23;}
        i=0;while(i<1024){if(db.output[i]!=1 as u8){return 24;}i=i+1;}batch=batch+1;
      }
      if(__heap_alloc_count()!=mark || other.capacity!=1 || other.key_bytes!=1 || other.live!=0 || db.slots!=8192){return 25;}
      input=input.with(2,16 as u8);db=kv_config_core.process(db,input,0,1);
      if(db.output[0]!=3 as u8 || db.live!=4096){return 26;}
      // Delete an existing key and reuse capacity; every remaining key must
      // still be reachable, including entries shifted across the end.
      input=input.with(0,3 as u8);input=input.with(1,0 as u8);input=input.with(2,0 as u8);
      db=kv_config_core.process(db,input,0,1);if(db.output[0]!=1 as u8 || db.live!=4095){return 27;}
      let key:u8[]=zeros(64);i=1;while(i<4096){key=key.with(0,(i & 255) as u8);key=key.with(1,(i >> 8) as u8);let slot:i32=kv_config_core.find(db,key,0);if(slot<0 || db.values[slot*256]!=(i & 255) as u8){return 28;}i=i+1;}
      input=input.with(0,1 as u8);input=input.with(2,16 as u8);db=kv_config_core.process(db,input,0,1);
      if(db.output[0]!=1 as u8 || db.live!=4096){return 29;}
    }}
  }}
  return 0;
}
`
	path := kvConfigSource(t, source)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostKVConfigAllocationContracts(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, contract := range []string{"fip", "fbip"} {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(contract+"/"+target, func(t *testing.T) {
				path := kvConfigSource(t, fmt.Sprintf(`%s function bad():Option[kv_config_core.Db]{return kv_config_core.new_db(3,1,8,1);} function main():i32 {match(bad()){Some(db)=>{return db.live;},None=>{return 1;}}}`, contract))
				cmd := runX86_64Bin(cli.runner, cli.bin, "-target", target, "-emit", "asm", "-o", filepath.Join(t.TempDir(), "out"), path, cli.stdlib)
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "E053") {
					t.Fatalf("expected allocating constructor refusal, got %v: %s", err, out)
				}
			})
		}
	}
}

func TestSelfHostKVConfigOrdinaryBoundaries(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, representation := range []string{"map", "pmap"} {
		module := "kv_config_" + representation
		source := `
function unchanged(a:subject.Db,b:subject.Db):boolean{return same_snapshot(a,subject.Db{...b,refused:a.refused});}
function main():i32 {
  let mark:i64=__heap_alloc_count();
  match(subject.new_db(0,1,8,1)){Some(db)=>{return 1;},None=>{}}
  match(subject.new_db(4097,1,8,1)){Some(db)=>{return 2;},None=>{}}
  match(subject.new_db(1,0,8,1)){Some(db)=>{return 3;},None=>{}}
  match(subject.new_db(1,65,8,1)){Some(db)=>{return 4;},None=>{}}
  match(subject.new_db(1,1,7,1)){Some(db)=>{return 5;},None=>{}}
  match(subject.new_db(1,1,257,1)){Some(db)=>{return 6;},None=>{}}
  match(subject.new_db(1,1,8,0)){Some(db)=>{return 7;},None=>{}}
  match(subject.new_db(1,1,8,1025)){Some(db)=>{return 8;},None=>{}}
  if(__heap_alloc_count()!=mark){return 9;}
  match(subject.new_db(4096,64,256,1024)){None=>{return 10;},Some(maximum)=>{
    match(subject.new_db(1,1,9,2)){None=>{return 11;},Some(db)=>{
      let put:u8[]=[1 as u8,255 as u8,255 as u8,255 as u8,255 as u8,255 as u8,255 as u8,255 as u8,255 as u8,255 as u8,77 as u8];
      let increment:u8[]=[4 as u8,255 as u8,1 as u8,0 as u8,0 as u8,0 as u8,0 as u8,0 as u8,0 as u8,0 as u8,99 as u8];
      db=subject.process(db,put,0,1);db=subject.process(db,increment,0,1);
      if(db.output.len()!=10 || db.output[0]!=4 as u8 || db.output[9]!=77 as u8){return 12;}
      let i:i32=1;while(i<9){if(db.output[i]!=0 as u8){return 13;}i=i+1;}
      let before:subject.Db=saved(db);let bad:u8[]=put;bad=bad.append(0 as u8);i=0;while(i<10){bad=bad.append(0 as u8);i=i+1;}
      db=subject.process(db,bad,0,2);if(!db.refused || !unchanged(before,db)){return 14;}
      db=subject.process(db,put,-1,1);if(!db.refused || !unchanged(before,db)){return 15;}
      db=subject.process(db,put,2147483647,1);if(!db.refused || !unchanged(before,db)){return 16;}
      db=subject.process(db,put,0,-1);if(!db.refused || !unchanged(before,db)){return 17;}
      db=subject.process(db,put,0,2147483647);if(!db.refused || !unchanged(before,db)){return 18;}
      db=subject.process(db,put,1,1);if(!db.refused || !unchanged(before,db)){return 19;}
      db=subject.process(db,put,put.len(),0);if(db.refused || db.output.len()!=0 || db.entries.len()!=1 || before.output.len()!=10){return 20;}
      if(maximum.capacity!=4096 || maximum.key_bytes!=64 || maximum.value_bytes!=256 || maximum.batch!=1024 || maximum.entries.len()!=0 || maximum.output.len()!=0){return 21;}
    }}
  }}
  return 0;
}
`
		program := "import \"./" + module + "\";\n" + strings.ReplaceAll(kvConfigOrdinaryMatches+source, "subject.", module+".")
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(representation+"/"+target, func(t *testing.T) {
				dir := t.TempDir()
				for _, name := range []string{"core", "records", "map", "pmap"} {
					filename := "kv_config_" + name + ".fern"
					data, err := os.ReadFile(filepath.Join(repoRootFromTest(t), "examples/fip", filename))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, filename), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				path := filepath.Join(dir, "main.fern")
				if err := os.WriteFile(path, []byte(program), 0600); err != nil {
					t.Fatal(err)
				}
				stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit %d: %s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
