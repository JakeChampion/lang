package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostRingEventLoopCapacity(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path := ringExampleSource(t, `
import "./event_loop_ring";
function snapshot(s:event_loop_ring.State):event_loop_ring.State{return s;}
function main():i32 {
 let invalid:i32[]=[-2147483648,-1,0,4097,2147483647];
 let mark:i64=__heap_alloc_count();let i:i32=0;
 while(i<invalid.len()){
  match(event_loop_ring.new_state(1,invalid[i])){Some(_)=>{return 1;},None=>{}}
  i=i+1;
 }
 match(event_loop_ring.new_state(0,1)){Some(_)=>{return 2;},None=>{}}
 match(event_loop_ring.new_state(257,1)){Some(_)=>{return 3;},None=>{}}
 if(__heap_alloc_count()!=mark){return 4;}
 let e:event_loop_ring.Event=event_loop_ring.Event{kind:1i64,id:17i64,value:-2147483649i64};
 match(event_loop_ring.new_state(256,4096)){None=>{return 5;},Some(s)=>{
  if(s.table.len()!=768 || bounded_ring.capacity(s.input)!=4096 || bounded_ring.capacity(s.output)!=4096){return 6;}
  mark=__heap_alloc_count();i=0;
  while(i<4096){s=event_loop_ring.enqueue(s,e);i=i+1;}
  s=event_loop_ring.enqueue(s,e);
  if(s.input.count!=4096 || s.dropped!=1i64){return 7;}
  i=0;while(i<4096){s=event_loop_ring.step(s);i=i+1;}
  if(s.input.count!=0 || s.output.count!=4096 || s.live!=1 || s.ok!=4096i64){return 8;}
  if(s.input.data[0].kind!=0i64 || s.input.data[4095].kind!=0i64){return 9;}
  s=event_loop_ring.enqueue(s,e);s=event_loop_ring.step(s);
  if(s.ok!=4097i64 || s.dropped!=2i64 || s.output.count!=4096){return 10;}
  if(__heap_alloc_count()!=mark){return 11;}
  let saved:event_loop_ring.State=snapshot(s);s=event_loop_ring.drain(s);
  if(s.output.count!=0 || saved.output.count!=4096){return 12;}
  i=0;while(i<4096){if(bounded_ring.at_or(saved.output,i,0i64)!=1i64){return 13;}i=i+1;}
  return 0;
 }}
}
`)
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", "event_loop_ring.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "event_loop_ring.fern"), data, 0600); err != nil {
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
}

// Dictionary and slice queues deliberately do not reproduce the ring or table layout.
func TestSelfHostRingEventLoop(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ entries, capacity int }{{1, 1}, {3, 3}, {7, 17}} {
		t.Run(fmt.Sprintf("%d_%d", tc.entries, tc.capacity), func(t *testing.T) {
			table := map[int]int64{}
			var input [][3]int64
			var output []int64
			var ok, missing, full, dropped int64
			var fixtures, expected, events, actions strings.Builder
			rng := rand.New(rand.NewSource(9593))
			for i := 0; i < 180; i++ {
				action := rng.Intn(10)
				event := [3]int64{int64(rng.Intn(6)), int64(rng.Intn(9)), int64(i*7 - 300)}
				if i < 60 {
					action = i % 3
					if action != 0 {
						action = 7 // Drain input while deliberately retaining output.
					}
					event = [3]int64{int64((i/12)%4 + 1), int64(i / 3 % 9), int64(i)}
				}
				if action < 6 {
					if len(input) == tc.capacity {
						dropped++
					} else {
						input = append(input, event)
					}
				} else if action < 9 {
					if len(input) > 0 {
						e := input[0]
						input = input[1:]
						kind, id, value := int(e[0]), int(e[1]), e[2]
						code := int64(0)
						previous, present := table[id]
						if kind == 1 {
							if !present && len(table) == tc.entries {
								full++
								code = 3
							} else {
								table[id] = value
								ok++
								code = 1
							}
						} else if kind >= 2 && kind <= 4 {
							if !present {
								missing++
								code = 2
							} else {
								ok++
								code = 1
								if kind == 2 {
									table[id] = previous + value
								} else if kind == 3 {
									code = 4
								} else {
									delete(table, id)
								}
							}
						}
						if code != 0 {
							if len(output) == tc.capacity {
								dropped++
							} else {
								output = append(output, code)
							}
						}
					}
				} else {
					output = nil
				}
				state := []int64{int64(len(table)), ok, missing, full, dropped, int64(len(input)), int64(len(output))}
				for id := 0; id < 9; id++ {
					present := int64(0)
					if _, found := table[id]; found {
						present = 1
					}
					state = append(state, present, table[id])
				}
				for _, e := range input {
					state = append(state, e[:]...)
				}
				state = append(state, output...)
				fmt.Fprintf(&fixtures, "@noinline function fixture%d():i64[]{return [", i)
				for j, v := range state {
					if j > 0 {
						fixtures.WriteString(",")
					}
					fmt.Fprintf(&fixtures, "%di64", v)
				}
				fixtures.WriteString("]; }\n")
				if i > 0 {
					expected.WriteString(",")
					events.WriteString(",")
					actions.WriteString(",")
				}
				fmt.Fprintf(&expected, "fixture%d()", i)
				fmt.Fprintf(&events, "event_loop_ring.Event{kind:%di64,id:%di64,value:%di64}", event[0], event[1], event[2])
				fmt.Fprint(&actions, action)
			}
			source := "import \"./event_loop_ring\";\n" + fixtures.String() + `
function snapshot(s:event_loop_ring.State):event_loop_ring.State{return s;}
function matches(s:event_loop_ring.State,v:i64[]):boolean {
 if(s.live as i64!=v[0] || s.ok!=v[1] || s.missing!=v[2] || s.full!=v[3] || s.dropped!=v[4] || s.input.count as i64!=v[5] || s.output.count as i64!=v[6]){return false;}
 let id:i32=0;while(id<9){let at:i32=event_loop_ring.lookup(s,id as i64);if(at<0){if(v[7+id*2]!=0i64){return false;}}else{if(v[7+id*2]!=1i64 || s.table[s.entries+at]!=v[8+id*2]){return false;}}id=id+1;}
 let at:i32=25;let i:i32=0;
 while(i<s.input.count){let e:event_loop_ring.Event=bounded_ring.at_or(s.input,i,s.empty_event);if(e.kind!=v[at]||e.id!=v[at+1]||e.value!=v[at+2]){return false;}at=at+3;i=i+1;}
 i=0;while(i<s.output.count){if(bounded_ring.at_or(s.output,i,0i64)!=v[at]){return false;}at=at+1;i=i+1;}
 return at==v.len();
}
` + fmt.Sprintf(`
function main():i32 {
 let expected:i64[][]=[%s];let events:event_loop_ring.Event[]=[%s];let actions:i32[]=[%s];
 match(event_loop_ring.new_state(%d,%d)){None=>{return 1;},Some(s)=>{
 let mark:i64=__heap_alloc_count();let i:i32=0;
 while(i<actions.len()) {if(actions[i]<6){s=event_loop_ring.enqueue(s,events[i]);}else if(actions[i]<9){s=event_loop_ring.step(s);}else{s=event_loop_ring.drain(s);}
 if(!matches(s,expected[i])){return 2;}if(__heap_alloc_count()!=mark){return 3;}i=i+1;}
 let saved:event_loop_ring.State=snapshot(s);s=event_loop_ring.enqueue(s,events[0]);s=event_loop_ring.step(s);s=event_loop_ring.drain(s);
 if(!matches(saved,expected[expected.len()-1])){return 4;}return 0;
 }}
}
`, expected.String(), events.String(), actions.String(), tc.entries, tc.capacity)
			path := ringExampleSource(t, source)
			data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", "event_loop_ring.fern"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), "event_loop_ring.fern"), data, 0600); err != nil {
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
