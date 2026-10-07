package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ringExampleSource(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", "bounded_ring.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bounded_ring.fern"), data, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte("import \"./bounded_ring\";\n"+source), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostBoundedRingModel(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, capacity := range []int{1, 3, 17} {
		t.Run(fmt.Sprint(capacity), func(t *testing.T) {
			var fixtures, states, ops strings.Builder
			var queue []int
			rng := rand.New(rand.NewSource(9593))
			for step := 0; step < 160; step++ {
				push := rng.Intn(3) != 0
				if step < capacity+2 {
					push = true
				}
				if step >= 140 {
					push = false
				}
				value := step*17 - 900
				status := 0
				if push {
					if len(queue) == capacity {
						status = 1
					} else {
						queue = append(queue, value)
					}
				} else if len(queue) == 0 {
					status = 2
				} else {
					queue = queue[1:]
				}
				if step > 0 {
					states.WriteString(",")
					ops.WriteString(",")
				}
				fmt.Fprintf(&states, "fixture%d()", step)
				fmt.Fprintf(&ops, "[%t,%d]", push, status)
				// Each fixture remains small on ARM, outside allocation marks.
				fmt.Fprintf(&fixtures, "@noinline function fixture%d():i32[] {return [", step)
				for i, v := range queue {
					if i > 0 {
						fixtures.WriteString(",")
					}
					fmt.Fprint(&fixtures, v)
				}
				fixtures.WriteString("]; }\n")
			}
			// Encode action and status separately to keep homogeneous Fern arrays.
			actions := strings.ReplaceAll(strings.ReplaceAll(ops.String(), "true", "1"), "false", "0")
			source := fixtures.String() + fmt.Sprintf(`
function main():i32 {
  let expected:i32[][]=[%s]; let actions:i32[][]=[%s];
  match(bounded_ring.new_ring(%d,0)) {None=>{return 1;},Some(q)=>{
    let mark:i64=__heap_alloc_count();let i:i32=0;
    while(i<actions.len()) {
      if(actions[i][0]==1) {q=bounded_ring.push(q,i*17-900);} else {q=bounded_ring.drop(q);}
      if(q.status!=actions[i][1] || q.count!=expected[i].len() || bounded_ring.capacity(q)!=%d) {return 2;}
      let j:i32=0;while(j<q.count) {if(bounded_ring.at_or(q,j,999999)!=expected[i][j]) {return 3;}j=j+1;}
      if(bounded_ring.at_or(q,-1,77)!=77 || bounded_ring.at_or(q,q.count,88)!=88) {return 4;}
      if(__heap_alloc_count()!=mark) {return 5;}
      i=i+1;
    }
    return 0;
  }}
}
`, states.String(), actions, capacity, capacity)
			path := ringExampleSource(t, source)
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

func TestSelfHostBoundedRingSnapshots(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path := ringExampleSource(t, `
struct Item { id:i32, text:string }
function saved(q:bounded_ring.Ring[Item]):bounded_ring.Ring[Item] {return q;}
function main():i32 {
  let empty:Item=Item{id:0,text:""};let a:Item=Item{id:7,text:"seven"};let b:Item=Item{id:8,text:"eight"};
  let mark:i64=__heap_alloc_count();
  match(bounded_ring.new_ring(0,empty)) {Some(q)=>{return 1;},None=>{}}
  match(bounded_ring.new_ring(4097,empty)) {Some(q)=>{return 2;},None=>{}}
  if(__heap_alloc_count()!=mark) {return 3;}
  match(bounded_ring.new_ring(4096,empty)) {None=>{return 4;},Some(q)=>{
    mark=__heap_alloc_count();let i:i32=0;
    while(i<4096) {q=bounded_ring.push(q,a);i=i+1;}
    q=bounded_ring.push(q,b);
    if(q.status!=1 || q.count!=4096 || __heap_alloc_count()!=mark) {return 5;}
    let snapshot:bounded_ring.Ring[Item]=saved(q);
    q=bounded_ring.drop(q);q=bounded_ring.push(q,b);
    if(bounded_ring.front_or(snapshot,empty).id!=7 || bounded_ring.at_or(snapshot,4095,empty).text!="seven") {return 6;}
    if(bounded_ring.at_or(q,4095,empty).text!="eight" || snapshot.head!=0 || q.head!=1) {return 7;}
    i=0;while(i<4096) {q=bounded_ring.drop(q);i=i+1;}
    q=bounded_ring.drop(q);
    if(q.status!=2 || q.count!=0 || bounded_ring.front_or(q,empty).id!=0) {return 8;}
    i=0;while(i<4096) {if(q.data[i].id!=0 || bounded_ring.at_or(snapshot,i,empty).id!=7) {return 9;}i=i+1;}
    return 0;
  }}
}
`)
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

func TestSelfHostBoundedRingAllocationContracts(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, claim := range []string{"fip", "fbip"} {
		for _, tc := range []struct{ name, source, diagnostic string }{
			{"constructor", `%s function bad():Option[bounded_ring.Ring[i32]] {return bounded_ring.new_ring(3,0);} function main():i32 {match(bad()){Some(q)=>{return q.count;},None=>{return 1;}}}`, "E053"},
			{"fresh_payload", `struct Item {id:i32} %s function bad(own q:bounded_ring.Ring[Item],id:i32):bounded_ring.Ring[Item] {return bounded_ring.push(q,Item{id:id});} function main():i32 {let empty:Item=Item{id:0};match(bounded_ring.new_ring(3,empty)){Some(q)=>{q=bad(q,7);return q.count;},None=>{return 1;}}}`, "E068"},
		} {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(claim+"/"+tc.name+"/"+target, func(t *testing.T) {
					path := ringExampleSource(t, fmt.Sprintf(tc.source, claim))
					cmd := runX86_64Bin(cli.runner, cli.bin, "-target", target, "-emit", "asm", "-o", filepath.Join(t.TempDir(), "out"), path, cli.stdlib)
					out, err := cmd.CombinedOutput()
					if err == nil || !strings.Contains(string(out), tc.diagnostic) {
						t.Fatalf("expected %s refusal, got %v: %s", tc.diagnostic, err, out)
					}
				})
			}
		}
	}
}
