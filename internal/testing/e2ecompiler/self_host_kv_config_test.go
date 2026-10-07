package e2ecompiler

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The reference stores byte strings in a Go dictionary. It does not reproduce
// the subject's hash, probe order or backward-shift deletion.
func kvConfigReference(entries map[string][]byte, input []byte, capacity, keyBytes, valueBytes int) []byte {
	var output []byte
	stride := 1 + keyBytes + valueBytes
	for at := 0; at < len(input); at += stride {
		key := string(input[at+1 : at+1+keyBytes])
		value, found := entries[key]
		switch input[at] {
		case 1:
			if !found && len(entries) == capacity {
				output = append(output, 3)
			} else {
				entries[key] = append([]byte(nil), input[at+1+keyBytes:at+stride]...)
				output = append(output, 1)
			}
		case 2, 4:
			if !found {
				output = append(output, 2)
				continue
			}
			if input[at] == 4 {
				value = append([]byte(nil), value...)
				binary.LittleEndian.PutUint64(value, binary.LittleEndian.Uint64(value)+binary.LittleEndian.Uint64(input[at+1+keyBytes:]))
				entries[key] = value
			}
			output = append(append(output, 4), value...)
		case 3:
			if found {
				delete(entries, key)
				output = append(output, 1)
			} else {
				output = append(output, 2)
			}
		default:
			panic("invalid oracle operation")
		}
	}
	return output
}

func kvConfigState(entries map[string][]byte) []byte {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var state []byte
	for _, key := range keys {
		state = append(state, []byte(key)...)
		state = append(state, entries[key]...)
	}
	return state
}

func kvConfigFixture(name string, data []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "@noinline\nfunction %s(): u8[] { return [", name)
	for i, value := range data {
		if i != 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%d as u8", value)
	}
	b.WriteString("]; }\n")
	return b.String()
}

const kvConfigMatches = `
function saved(db: kv_config_core.Db): kv_config_core.Db { return db; }
function matches(db: kv_config_core.Db, expected: u8[], output: u8[]): boolean {
  let width: i32 = db.key_bytes + db.value_bytes;
  if (db.refused || db.live != expected.len() / width || db.out_count != output.len()) { return false; }
  let i: i32 = 0;
  while (i < output.len()) { if (db.output[i] != output[i]) { return false; } i = i + 1; }
  let occupied: i32 = 0; i = 0;
  while (i < db.slots) { if (db.used[i] != 0) { occupied = occupied + 1; } i = i + 1; }
  if (occupied != db.live) { return false; }
  let at: i32 = 0;
  while (at < expected.len()) {
    let copies: i32 = 0; let slot: i32 = 0;
    while (slot < db.slots) {
      if (db.used[slot] != 0) {
        let same: boolean = true; i = 0;
        while (i < db.key_bytes) { if (db.keys[slot * db.key_bytes + i] != expected[at + i]) { same = false; } i = i + 1; }
        if (same) {
          copies = copies + 1; i = 0;
          while (i < db.value_bytes) { if (db.values[slot * db.value_bytes + i] != expected[at + db.key_bytes + i]) { return false; } i = i + 1; }
        }
      }
      slot = slot + 1;
    }
    if (copies != 1 || kv_config_core.find(db, expected, at) < 0) { return false; }
    at = at + width;
  }
  return true;
}
function same_snapshot(a: kv_config_core.Db, b: kv_config_core.Db): boolean {
  if (a.live != b.live || a.out_count != b.out_count || a.refused != b.refused) { return false; }
  let i: i32 = 0;
  while (i < a.slots) { if (a.used[i] != b.used[i] || a.hashes[i] != b.hashes[i]) { return false; } i = i + 1; }
  i = 0; while (i < a.keys.len()) { if (a.keys[i] != b.keys[i]) { return false; } i = i + 1; }
  i = 0; while (i < a.values.len()) { if (a.values[i] != b.values[i]) { return false; } i = i + 1; }
  i = 0; while (i < a.output.len()) { if (a.output[i] != b.output[i]) { return false; } i = i + 1; }
  return true;
}
`

const kvConfigOrdinaryMatches = `
function saved(db: subject.Db): subject.Db { return db; }
function matches(db: subject.Db, expected: u8[], output: u8[]): boolean {
  let width:i32=db.key_bytes+db.value_bytes;
  if(db.refused || db.entries.len()!=expected.len()/width || db.output.len()!=output.len()){return false;}
  let i:i32=0;while(i<output.len()){if(db.output[i]!=output[i]){return false;}i=i+1;}
  let at:i32=0;while(at<expected.len()){
    let bytes:u8[]=[];i=0;while(i<db.key_bytes){bytes=bytes.append(expected[at+i]);i=i+1;}
    let key:string=string_from_bytes_unchecked(bytes);
    let value:string=db.entries.get_or(key,"");if(value.len()!=db.value_bytes){return false;}
    i=0;while(i<db.value_bytes){if(value[i]!=expected[at+db.key_bytes+i]){return false;}i=i+1;}
    at=at+width;
  }
  return true;
}
function same_snapshot(a:subject.Db,b:subject.Db):boolean {
  if(a.refused!=b.refused || a.entries.len()!=b.entries.len() || a.output.len()!=b.output.len()){return false;}
  let keys:string[]=a.entries.keys();let i:i32=0;
  while(i<keys.len()){if(a.entries.get_or(keys[i],"")!=b.entries.get_or(keys[i],"")){return false;}i=i+1;}
  i=0;while(i<a.output.len()){if(a.output[i]!=b.output[i]){return false;}i=i+1;}
  return true;
}
`

func TestSelfHostKVConfigModel(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		name                                  string
		capacity, keyBytes, valueBytes, batch int
	}{
		{"pilot", 5, 2, 9, 3}, {"single", 1, 1, 8, 1},
		{"odd", 17, 3, 13, 7}, {"wide", 3, 64, 256, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stride := 1 + tc.keyBytes + tc.valueBytes
			rng := rand.New(rand.NewSource(9585))
			var actions [][]byte
			request := func(op byte, id int) []byte {
				x := make([]byte, stride)
				x[0], x[1] = op, byte(id)
				for j := 1; j < tc.keyBytes; j++ {
					x[1+j] = byte(id*31 + j)
				}
				for j := 0; j < tc.valueBytes; j++ {
					x[1+tc.keyBytes+j] = byte(rng.Intn(256))
				}
				return x
			}
			// Full insertion, overwrite at capacity, deletion and reinsertion precede
			// reproducible mixed requests. All generated keys fit a one-byte universe.
			for i := 0; i < tc.capacity+2; i++ {
				actions = append(actions, request(1, i))
			}
			for _, op := range []byte{1, 2, 4, 3, 2, 1} {
				actions = append(actions, request(op, 0))
			}
			for i := 0; i < 30; i++ {
				actions = append(actions, request(byte(1+rng.Intn(4)), rng.Intn(tc.capacity+3)))
			}
			entries := map[string][]byte{}
			var fixtures, body strings.Builder
			fmt.Fprintf(&body, "function main(): i32 { match (kv_config_core.new_db(%d,%d,%d,%d)) { Some(db) => {\n", tc.capacity, tc.keyBytes, tc.valueBytes, tc.batch)
			step := 0
			for at := 0; at < len(actions); at += tc.batch {
				end := min(at+tc.batch, len(actions))
				var input []byte
				for _, action := range actions[at:end] {
					input = append(input, action...)
				}
				output := kvConfigReference(entries, input, tc.capacity, tc.keyBytes, tc.valueBytes)
				for _, fixture := range []struct {
					name string
					data []byte
				}{{"input", input}, {"state", kvConfigState(entries)}, {"output", output}} {
					fixtures.WriteString(kvConfigFixture(fmt.Sprintf("%s%d", fixture.name, step), fixture.data))
				}
				// Every fixture is allocated before the marks; unique state must be
				// allocation-free from its very first operation, with no warm-up.
				fmt.Fprintf(&body, "if (true) { let input: u8[] = input%d(); let state: u8[] = state%d(); let output: u8[] = output%d();\nlet before: i64 = __heap_alloc_count(); db = kv_config_core.process(db,input,0,%d); let allocations: i64 = __heap_alloc_count()-before;\nif (allocations != 0 || !matches(db,state,output)) { return %d; } }\n", step, step, step, end-at, step+1)
				step++
			}
			// Retain the complete old database and its output across an update.
			fixtures.WriteString(kvConfigFixture("replacement", request(1, 0)))
			body.WriteString("let input: u8[] = replacement(); let old: kv_config_core.Db = saved(db); db = kv_config_core.process(db,input,0,1); if (same_snapshot(old,db)) { return 100; }\n")
			fmt.Fprintf(&body, "if (!matches(old,state%d(),output%d())) { return 101; } return 0; }, None => { return 102; }, } }\n", step-1, step-1)
			for _, representation := range []string{"core", "map", "pmap"} {
				t.Run(representation, func(t *testing.T) {
					module := "kv_config_" + representation
					helpers, program := kvConfigMatches, body.String()
					if representation != "core" {
						helpers = strings.ReplaceAll(kvConfigOrdinaryMatches, "subject.", module+".")
						program = strings.ReplaceAll(program, "allocations != 0 || ", "")
					}
					program = strings.ReplaceAll(program, "kv_config_core.", module+".")
					source := "import \"./" + module + "\";\n" + helpers + fixtures.String() + program
					for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
						t.Run(target, func(t *testing.T) {
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
							if err := os.WriteFile(path, []byte(source), 0600); err != nil {
								t.Fatal(err)
							}
							stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
							if code != 0 {
								t.Fatalf("exit %d:\n%s", code, stderr)
							}
							assertBalancedCensus(t, stderr)
						})
					}
				})
			}
		})
	}
}
