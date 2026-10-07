package e2ecompiler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type simulationEntity struct {
	x, y, vx, vy, health, kind, target, mode, neighbors int
}

func simulationInitial(count int, clustered bool) []simulationEntity {
	rows := make([]simulationEntity, count)
	for i := range rows {
		r := simulationEntity{x: i * 73 % 4096, y: i * 151 % 4096, vx: i*7%9 - 4, vy: i*11%9 - 4, health: 100, kind: i % 3, target: -1}
		if clustered {
			r.x, r.y = i%8, i/8%8
		}
		if i%3 != 0 {
			r.target, r.mode = (i+1)%count, 1
		}
		rows[i] = r
	}
	return rows
}

func simulationDisplacement(to, from int) int {
	d := to - from
	if d >= 2048 {
		d -= 4096
	} else if d < -2048 {
		d += 4096
	}
	return d
}

// Deliberately use all-pairs proximity, independent of the subject's grid.
func simulationOracle(previous []simulationEntity, tick int) ([]simulationEntity, int64) {
	rows := append([]simulationEntity(nil), previous...)
	for i, old := range previous {
		r := old
		if (i+tick)%17 == 0 {
			r.target = -1
		} else if (i+tick)%11 == 0 {
			r.target = (i + tick) % len(rows)
		}
		r.vx, r.vy = 0, 0
		if old.mode != 2 {
			if r.target < 0 {
				r.vx, r.vy = (i*7+tick)%9-4, (i*11+tick*3)%9-4
			} else {
				target := previous[r.target]
				steer := func(d int) int {
					if d == 0 {
						return 0
					}
					if d < 0 {
						return -(old.kind + 1)
					}
					return old.kind + 1
				}
				r.vx, r.vy = steer(simulationDisplacement(target.x, old.x)), steer(simulationDisplacement(target.y, old.y))
			}
		}
		r.x, r.y = (old.x+r.vx+4096)%4096, (old.y+r.vy+4096)%4096
		rows[i] = r
	}
	for i := range rows {
		rows[i].neighbors = 0
		for j := range rows {
			dx := simulationDisplacement(rows[j].x, rows[i].x)
			dy := simulationDisplacement(rows[j].y, rows[i].y)
			if i != j && dx*dx+dy*dy <= 128*128 {
				rows[i].neighbors++
			}
		}
	}
	for i := range rows {
		r := &rows[i]
		if r.mode == 2 {
			r.health = min(100, r.health+3)
		} else {
			r.health = max(0, min(100, r.health-min(4, r.neighbors)+r.kind))
		}
		if r.health < 25 || r.mode == 2 && r.health < 75 {
			r.mode = 2
		} else if r.target >= 0 {
			r.mode = 1
		} else {
			r.mode = 0
		}
	}
	hash := int64(tick + 1)
	for _, r := range rows {
		for _, v := range []int{r.x, r.y, r.vx + 4, r.vy + 4, r.health, r.kind, r.target + 1, r.mode, r.neighbors} {
			hash = (hash*65599 + int64(v)) % 1000000007
		}
	}
	return rows, hash
}

func simulationFields(rows []simulationEntity) []int {
	var values []int
	for _, r := range rows {
		values = append(values, r.x, r.y, r.vx, r.vy, r.health, r.kind, r.target, r.mode, r.neighbors)
	}
	return values
}

func simulationLiteral(values []int) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprint(value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func simulationFiles(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"simulation.fern", "simulation_rules.fern", "simulation_fip.fern", "simulation_entity.fern", "simulation_fbip.fern", "simulation_persistent.fern"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSelfHostFipSimulation(t *testing.T) {
	testSimulation(t, "fip")
}

func TestSelfHostCollectionSimulation(t *testing.T) {
	for _, variant := range []string{"fbip", "persistent"} {
		t.Run(variant, func(t *testing.T) { testSimulation(t, variant) })
	}
}

func testSimulation(t *testing.T, variant string) {
	t.Helper()
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		name         string
		count, ticks int
		clustered    bool
	}{
		{"pilot", 33, 6, false},
		{"empty", 0, 3, false},
		{"single", 1, 20, false},
		{"distributed", 65, 24, false},
		{"clustered", 33, 96, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := simulationInitial(tc.count, tc.clustered)
			values := simulationFields(rows)
			hashes := []int{0}
			for tick := 0; tick <= tc.ticks; tick++ {
				var hash int64
				rows, hash = simulationOracle(rows, tick)
				values = append(values, simulationFields(rows)...)
				hashes = append(hashes, int(hash))
			}
			var src strings.Builder
			src.WriteString("import \"./simulation_fip\";\n")
			if variant != "fip" {
				src.WriteString("import \"./simulation_entity\";\n")
			}
			src.WriteString("function same(w: simulation_fip.World, expected: i32[], at: i32): boolean { let i: i32 = 0; while (i < w.count) { let p: i32 = at + i * 9;\n")
			if variant != "fip" {
				src.WriteString("let row: simulation_entity.Entity = simulation_fip.get(w, i);\n")
			}
			for field, name := range []string{"x", "y", "vx", "vy", "health", "kind", "target", "mode", "neighbors"} {
				access := "w." + name + "[i]"
				if variant != "fip" && name != "neighbors" {
					access = "row." + name
				}
				fmt.Fprintf(&src, "if (%s != expected[p + %d]) { return false; }\n", access, field)
			}
			src.WriteString("i = i + 1; } return true; }\nfunction main(): i32 {\n")
			fmt.Fprintf(&src, "let expected: i32[] = %s; let hashes: i32[] = %s;\nmatch (simulation_fip.new_world(%d, %t)) { Some(w) => {\n", simulationLiteral(values), simulationLiteral(hashes), tc.count, tc.clustered)
			src.WriteString("if (!same(w, expected, 0)) { return 10; } w = simulation_fip.step(w); let mark: i64 = __heap_alloc_count();\nlet tick: i32 = 1;\n")
			fmt.Fprintf(&src, "while (tick <= %d) { if (w.tick != tick || w.checksum != hashes[tick] as i64 || !same(w, expected, tick * %d)) { return 11; }\nif (w.candidates < %di64 || w.candidates > %di64) { return 12; }\nif (tick < %d) { w = simulation_fip.step(w); } tick = tick + 1; }\n", tc.ticks, tc.count*9, tc.count, tc.count*tc.count, tc.ticks)
			if variant != "persistent" {
				src.WriteString("if (__heap_alloc_count() != mark) { return 13; }\n")
			}
			src.WriteString("let saved: simulation_fip.World = w; w = simulation_fip.step(w);\n")
			fmt.Fprintf(&src, "if (!same(saved, expected, %d) || saved.checksum != hashes[%d] as i64 || saved.tick != %d) { return 14; }\nif (!same(w, expected, %d) || w.checksum != hashes[%d] as i64) { return 15; }\nreturn 0; }, None => { return 16; } } }\n", tc.count*9*tc.ticks, tc.ticks, tc.ticks, tc.count*9*(tc.ticks+1), tc.ticks+1)
			dir := t.TempDir()
			simulationFiles(t, dir)
			path := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(path, []byte(strings.ReplaceAll(src.String(), "simulation_fip", "simulation_"+variant)), 0600); err != nil {
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

func TestSelfHostFipSimulationBoundaries(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := `import "./simulation_fip";
import "./simulation_rules";
function main(): i32 {
  if (simulation_rules.delta(4095, 0) != -1 || simulation_rules.delta(0, 4095) != 1
      || simulation_rules.delta(2048, 0) != -2048 || simulation_rules.delta(0, 2048) != -2048
      || simulation_rules.wrap(-4) != 4092 || simulation_rules.wrap(4099) != 3) { return 10; }
  if (simulation_rules.health(0, 0, 10, 0) != 0 || simulation_rules.health(100, 0, 0, 2) != 100
      || simulation_rules.health(99, 2, 20, 0) != 100
      || simulation_rules.mode(0, 24, 1) != 2 || simulation_rules.mode(0, 25, 1) != 1
      || simulation_rules.mode(2, 74, -1) != 2 || simulation_rules.mode(2, 75, -1) != 0
      || simulation_rules.mode(2, 75, 0) != 1) { return 11; }
  let mark: i64 = __heap_alloc_count();
  match (simulation_fip.new_world(-1, false)) { Some(w) => { return 12; }, None => {} }
  match (simulation_fip.new_world(4097, false)) { Some(w) => { return 13; }, None => {} }
  match (simulation_fip.new_world(2147483647, false)) { Some(w) => { return 14; }, None => {} }
  if (__heap_alloc_count() != mark) { return 15; }
  match (simulation_fip.new_world(4096, false)) {
    Some(w) => { if (w.count != 4096 || w.x.len() != 4096 || w.heads.len() != 256) { return 16; } },
    None => { return 17; }
  }
  match (simulation_fip.new_world(7, false)) {
    Some(w) => {
      w = simulation_fip.World { ...w, x: [0, 4095, 128, 129, 0, 0, 4095] };
      w = simulation_fip.World { ...w, y: [0, 0, 0, 0, 128, 129, 4095] };
      w = simulation_fip.rebuild(w);
      w = simulation_fip.proximity(w);
      let expected: i32[] = [4, 2, 2, 1, 2, 1, 2];
      let i: i32 = 0;
      while (i < 7) { if (w.neighbors[i] != expected[i]) { return 18; } i = i + 1; }
      if (w.candidates != 49i64) { return 19; }
      w = simulation_fip.World { ...w, vx: w.vx.with(0, -4) };
      w = simulation_fip.World { ...w, vy: w.vy.with(6, 4) };
      w = simulation_fip.move(w);
      if (w.x[0] != 4092 || w.y[6] != 3) { return 20; }
    },
    None => { return 21; }
  }
  match (simulation_fip.new_world(0, false)) {
    Some(w) => {
      w = simulation_fip.World { ...w, tick: 999999 };
      w = simulation_fip.step(w);
      if (w.tick != 1000000 || w.checksum != 1000000i64 || w.refused != 0) { return 22; }
      w = simulation_fip.step(w);
      w = simulation_fip.step(w);
      if (w.tick != 1000000 || w.checksum != 1000000i64 || w.refused != 1) { return 23; }
    },
    None => { return 24; }
  }
  return 0;
}`
	dir := t.TempDir()
	simulationFiles(t, dir)
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
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

func TestSelfHostCollectionSimulationBoundaries(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, variant := range []string{"fbip", "persistent"} {
		t.Run(variant, func(t *testing.T) {
			src := `import "./simulation_fbip";
import "./simulation_entity";
function main(): i32 {
  let mark: i64 = __heap_alloc_count();
  match (simulation_fbip.new_world(-1, false)) { Some(w) => { return 10; }, None => {} }
  match (simulation_fbip.new_world(4097, false)) { Some(w) => { return 11; }, None => {} }
  match (simulation_fbip.new_world(2147483647, false)) { Some(w) => { return 12; }, None => {} }
  if (__heap_alloc_count() != mark) { return 13; }
  match (simulation_fbip.new_world(4096, false)) {
    Some(w) => { if (w.count != 4096 || w.entities.len() != 4096 || w.heads.len() != 256) { return 14; } },
    None => { return 15; }
  }
  match (simulation_fbip.new_world(7, false)) {
    Some(w) => {
      let xs: i32[] = [0, 4095, 128, 129, 0, 0, 4095];
      let ys: i32[] = [0, 0, 0, 0, 128, 129, 4095];
      let i: i32 = 0;
      while (i < 7) {
        let row: simulation_entity.Entity = simulation_fbip.get(w, i);
        row = simulation_entity.Entity { ...row, x: xs[i], y: ys[i], vx: 0, vy: 0 };
        w = simulation_fbip.World { ...w, entities: w.entities.with(i, row) };
        i = i + 1;
      }
      w = simulation_fbip.rebuild(w);
      let saved: simulation_fbip.World = w;
      w = simulation_fbip.proximity(w);
      let expected: i32[] = [4, 2, 2, 1, 2, 1, 2];
      i = 0;
      while (i < 7) {
        if (w.neighbors[i] != expected[i] || saved.neighbors[i] != 0) { return 16; }
        i = i + 1;
      }
      if (w.candidates != 49i64 || saved.candidates != 0i64) { return 17; }
      let row: simulation_entity.Entity = simulation_fbip.get(w, 0);
      row = simulation_entity.Entity { ...row, vx: -4 };
      w = simulation_fbip.World { ...w, entities: w.entities.with(0, row) };
      row = simulation_fbip.get(w, 6);
      row = simulation_entity.Entity { ...row, vy: 4 };
      w = simulation_fbip.World { ...w, entities: w.entities.with(6, row) };
      let before_move: simulation_fbip.World = w;
      w = simulation_fbip.move(w);
      if (simulation_fbip.get(w, 0).x != 4092 || simulation_fbip.get(w, 6).y != 3
          || simulation_fbip.get(before_move, 0).x != 0 || simulation_fbip.get(before_move, 6).y != 4095) { return 18; }
      row = simulation_fbip.get(w, 0);
      row = simulation_entity.Entity { ...row, health: 24, kind: 0, mode: 0, target: 1 };
      w = simulation_fbip.World { ...w, entities: w.entities.with(0, row) };
      row = simulation_fbip.get(w, 1);
      row = simulation_entity.Entity { ...row, health: 74, kind: 0, mode: 2, target: -1 };
      w = simulation_fbip.World { ...w, entities: w.entities.with(1, row) };
      w = simulation_fbip.update(w);
      if (simulation_fbip.get(w, 0).health != 20 || simulation_fbip.get(w, 0).mode != 2
          || simulation_fbip.get(w, 1).health != 77 || simulation_fbip.get(w, 1).mode != 0) { return 19; }
      w = simulation_fbip.World { ...w, tick: 1000000 };
      let stopped: simulation_fbip.World = w;
      w = simulation_fbip.step(w);
      w = simulation_fbip.step(w);
      if (w.tick != 1000000 || w.refused != 1 || w.checksum != stopped.checksum
          || simulation_fbip.checksum(w) != simulation_fbip.checksum(stopped)) { return 20; }
    }, None => { return 21; }
  }
  return 0;
}`
			dir := t.TempDir()
			simulationFiles(t, dir)
			path := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(path, []byte(strings.ReplaceAll(src, "simulation_fbip", "simulation_"+variant)), 0600); err != nil {
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

func TestSelfHostSimulationDriver(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	simulationFiles(t, dir)
	path := filepath.Join(dir, "simulation.fern")
	bin := cli.x86Binary(t, path, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	for _, tc := range []struct {
		name         string
		count, ticks int
		cluster      bool
	}{
		{"pilot", 33, 5, false}, {"empty", 0, 2, false}, {"cluster", 7, 12, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, hash := simulationOracle(simulationInitial(tc.count, tc.cluster), 0)
			var checks int64
			for tick := 1; tick <= tc.ticks; tick++ {
				checks = (checks + hash) % 1000000007
				rows, hash = simulationOracle(rows, tick)
			}
			layout := "spread"
			if tc.cluster {
				layout = "cluster"
			}
			for _, variant := range []string{"persistent", "fbip", "fip"} {
				for _, sharing := range []string{"unique", "shared"} {
					t.Run(variant+"/"+sharing, func(t *testing.T) {
						out, err := runX86_64Bin(cli.runner, bin, variant, strconv.Itoa(tc.count), strconv.Itoa(tc.ticks), layout, sharing, "samples").CombinedOutput()
						if err != nil {
							t.Fatalf("%v: %s", err, out)
						}
						assertBalancedCensus(t, string(out))
						var samples []int64
						var report map[string]json.RawMessage
						reports := 0
						for _, line := range strings.Split(string(out), "\n") {
							if strings.HasPrefix(line, "sample,") {
								fields := strings.Split(line, ",")
								if len(fields) != 3 {
									t.Fatalf("bad sample: %q", line)
								}
								index, e1 := strconv.Atoi(fields[1])
								value, e2 := strconv.ParseInt(fields[2], 10, 64)
								if e1 != nil || e2 != nil || index != len(samples) || value < 0 {
									t.Fatalf("bad sample: %q", line)
								}
								samples = append(samples, value)
							} else if strings.HasPrefix(line, "{") {
								reports++
								if err := json.Unmarshal([]byte(line), &report); err != nil {
									t.Fatal(err)
								}
							}
						}
						if reports != 1 || len(samples) != tc.ticks {
							t.Fatalf("reports=%d samples=%d: %s", reports, len(samples), out)
						}
						value := func(key string) int64 {
							t.Helper()
							var n int64
							if err := json.Unmarshal(report[key], &n); err != nil {
								t.Fatalf("%s: %v", key, err)
							}
							return n
						}
						for key, want := range map[string]int64{"entities": int64(tc.count), "ticks": int64(tc.ticks), "initial_tick": 1, "final_tick": int64(tc.ticks + 1), "checksum": hash, "snapshot_checks": checks} {
							if got := value(key); got != want {
								t.Errorf("%s=%d want %d", key, got, want)
							}
						}
						if sharing == "unique" && variant != "persistent" && value("steady_allocs") != 0 {
							t.Fatal("unique bounded representation allocated")
						}
						candidates := value("candidates")
						if candidates < int64(tc.count*tc.ticks) || candidates > int64(tc.count*tc.count*tc.ticks) {
							t.Fatalf("candidate bound: %d", candidates)
						}
						sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
						for key, p := range map[string]int{"tick_p50_ns": 500, "tick_p95_ns": 950, "tick_p99_ns": 990, "tick_p999_ns": 999, "tick_max_ns": 1000} {
							index := (len(samples)*p+999)/1000 - 1
							if got := value(key); got != samples[index] {
								t.Errorf("%s=%d want %d", key, got, samples[index])
							}
						}
					})
				}
			}
		})
	}
	for _, target := range []string{"arm64-linux", "wasm32-wasi"} {
		for _, variant := range []string{"persistent", "fbip", "fip"} {
			for _, sharing := range []string{"unique", "shared"} {
				t.Run(target+"/"+variant+"/"+sharing, func(t *testing.T) {
					stderr, code := cli.exitOfFileArgs(t, path, target, nil, []string{variant, "7", "3", "cluster", sharing, "samples"}, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					assertBalancedCensus(t, stderr)
				})
			}
		}
	}
}

func TestSelfHostSimulationDriverArguments(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	simulationFiles(t, dir)
	bin := cli.x86Binary(t, filepath.Join(dir, "simulation.fern"))
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing", nil},
		{"variant", []string{"other", "1", "1", "spread", "unique"}},
		{"count-text", []string{"fip", "bad", "1", "spread", "unique"}},
		{"count-negative", []string{"fip", "-1", "1", "spread", "unique"}},
		{"count-capacity", []string{"fip", "4097", "1", "spread", "unique"}},
		{"count-overflow", []string{"fip", "4294967296", "1", "spread", "unique"}},
		{"ticks-text", []string{"fip", "1", "bad", "spread", "unique"}},
		{"ticks-zero", []string{"fip", "1", "0", "spread", "unique"}},
		{"ticks-negative", []string{"fip", "1", "-1", "spread", "unique"}},
		{"ticks-capacity", []string{"fip", "1", "1000000", "spread", "unique"}},
		{"ticks-overflow", []string{"fip", "1", "4294967297", "spread", "unique"}},
		{"layout", []string{"fip", "1", "1", "other", "unique"}},
		{"sharing", []string{"fip", "1", "1", "spread", "other"}},
		{"samples", []string{"fip", "1", "1", "spread", "unique", "other"}},
		{"extra", []string{"fip", "1", "1", "spread", "unique", "samples", "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runX86_64Bin(cli.runner, bin, tc.args...)
			out, err := cmd.CombinedOutput()
			if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 2 {
				t.Fatalf("expected exit2, got %v: %s", err, out)
			}
		})
	}
}

func TestSelfHostFipSimulationRejectsAllocatingEntity(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, claim := range []string{"fip", "fbip"} {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(claim+"/"+target, func(t *testing.T) {
				dir := t.TempDir()
				src := fmt.Sprintf("struct Entity { x: i32, y: i32 }\n%s function spawn(x: i32): Entity { return Entity { x: x, y: 0 }; }\nfunction main(): i32 { return spawn(0).x; }\n", claim)
				path := filepath.Join(dir, "main.fern")
				if err := os.WriteFile(path, []byte(src), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := runX86_64Bin(cli.runner, cli.bin, "-target", target, "-emit", "asm", "-o", filepath.Join(dir, "out"), path, cli.stdlib)
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "E068") || !strings.Contains(string(out), "un-reused allocation site") {
					t.Fatalf("expected allocation-contract refusal, got %v: %s", err, out)
				}
			})
		}
	}
}
