package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const brokerBoundaryHelpers = `
function broker(topics: i32, subscribers: i32, pool: i32, capacity: i32): broker_core.Broker {
  match (broker_core.new_broker(topics, subscribers, pool, capacity)) {
    Some(b) => { return b; }, None => { exit(90); return broker(1, 1, 1, 1); }
  }
}
function snapshot(b: broker_core.Broker): broker_core.Broker { return b; }
`

func TestSelfHostBrokerBoundaries(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct{ name, source string }{
		{"atomic_fanout", `
function main(): i32 {
  let b: broker_core.Broker = broker(1, 2, 3, 1);
  let mark: i64 = __heap_alloc_count();
  b = broker_core.reserve(b, 0, 111i64); b = broker_core.publish(b);
  b = broker_core.consume(b, 0);
  b = broker_core.reserve(b, 0, 222i64); b = broker_core.publish(b);
  if (b.status != 4 || b.producer.payload != 222i64 || b.producer.serial != 2i64 || b.lengths[0] != 0 || b.lengths[1] != 1 || b.free_count != 1) { return 1; }
  b = broker_core.acknowledge(b, 0, 1i64);
  if (b.free_count != 1 || b.refs[0] != 1) { return 2; }
  b = broker_core.consume(b, 1);
  if (broker_core.message(b, 1).payload != 111i64) { return 3; }
  b = broker_core.acknowledge(b, 1, 1i64);
  if (b.free_count != 2) { return 4; }
  b = broker_core.publish(b);
  if (b.status != 0 || b.producer_slot != -1 || b.lengths[0] != 1 || b.lengths[1] != 1) { return 5; }
  b = broker_core.consume(b, 0); b = broker_core.consume(b, 1);
  if (broker_core.message(b, 0).payload != 222i64 || broker_core.message(b, 1).payload != 222i64) { return 6; }
  b = broker_core.acknowledge(b, 0, 1i64);
  if (b.status != 7 || b.free_count != 2 || broker_core.message(b, 0).serial != 2i64) { return 7; }
  b = broker_core.acknowledge(b, 0, 2i64); b = broker_core.acknowledge(b, 1, 2i64);
  if (b.free_count != 3 || __heap_alloc_count() != mark) { return 8; }
  return 0;
}`},
		{"lease_pool_limit", `
function main(): i32 {
  let b: broker_core.Broker = broker(1, 2, 1, 1);
  let mark: i64 = __heap_alloc_count();
  b = broker_core.reserve(b, 0, -9223372036854775808i64); b = broker_core.publish(b);
  b = broker_core.consume(b, 0); b = broker_core.consume(b, 1);
  b = broker_core.reserve(b, 0, 77i64);
  if (b.status != 3 || b.lengths[0] != 0 || b.lengths[1] != 0 || b.free_count != 0) { return 1; }
  b = broker_core.acknowledge(b, 0, 1i64);
  if (b.free_count != 0 || broker_core.message(b, 1).payload != -9223372036854775808i64) { return 2; }
  b = broker_core.acknowledge(b, 1, 1i64);
  if (b.free_count != 1) { return 3; }
  b = broker_core.reserve(b, 0, 9223372036854775807i64); b = broker_core.cancel(b);
  if (b.free_count != 1 || b.serial != 2i64 || b.producer_slot != -1 || __heap_alloc_count() != mark) { return 4; }
  return 0;
}`},
		{"snapshots", `
function main(): i32 {
  let b: broker_core.Broker = broker(1, 2, 1, 1);
  b = broker_core.reserve(b, 0, 42i64); b = broker_core.publish(b);
  b = broker_core.consume(b, 0);
  let saved: broker_core.Broker = snapshot(b);
  let message: broker_core.Message = broker_core.message(b, 0);
  b = broker_core.acknowledge(b, 0, 1i64);
  b = broker_core.consume(b, 1); b = broker_core.acknowledge(b, 1, 1i64);
  b = broker_core.reserve(b, 0, 99i64); b = broker_core.publish(b);
  if (message.payload != 42i64 || message.serial != 1i64 || saved.serial != 1i64 || saved.free_count != 0
    || saved.lengths[0] != 0 || saved.lengths[1] != 1 || saved.refs[0] != 2
    || broker_core.message(saved, 0).payload != 42i64 || saved.slots[0].payload != 42i64) { return 1; }
  b = broker_core.consume(b, 0);
  if (broker_core.message(b, 0).payload != 99i64 || broker_core.message(b, 0).serial != 2i64) { return 2; }
  return 0;
}`},
		{"capacity_and_serial", `
function main(): i32 {
  let small: i32[] = [-2147483648, -1, 0, 17, 2147483647];
  let large: i32[] = [-2147483648, -1, 0, 4097, 2147483647];
  let mark: i64 = __heap_alloc_count(); let i: i32 = 0;
  while (i < small.len()) {
    match (broker_core.new_broker(small[i], 1, 1, 1)) { Some(b) => { return 1; }, None => {} }
    match (broker_core.new_broker(1, small[i], 1, 1)) { Some(b) => { return 2; }, None => {} }
    match (broker_core.new_broker(1, 1, large[i], 1)) { Some(b) => { return 3; }, None => {} }
    match (broker_core.new_broker(1, 1, 1, large[i])) { Some(b) => { return 4; }, None => {} }
    i = i + 1;
  }
  if (__heap_alloc_count() != mark) { return 5; }
  let max: broker_core.Broker = broker(16, 16, 4096, 4096);
  mark = __heap_alloc_count();
  max = broker_core.reserve(max, 15, 88i64); max = broker_core.publish(max); max = broker_core.consume(max, 15);
  if (broker_core.message(max, 15).payload != 88i64 || max.free_count != 4095 || max.queues.len() != 65536) { return 6; }
  max = broker_core.acknowledge(max, 15, 1i64);
  if (max.free_count != 4096 || __heap_alloc_count() != mark) { return 7; }
  let b: broker_core.Broker = broker(2, 1, 1, 1);
  b = broker_core.Broker { ...b, serial: 9223372036854775806i64 };
  mark = __heap_alloc_count();
  b = broker_core.reserve(b, 1, 0i64);
  if (b.status != 9 || b.serial != 9223372036854775806i64 || b.free_count != 1) { return 8; }
  b = broker_core.reserve(b, 0, 7i64);
  if (b.status != 0 || b.serial != 9223372036854775807i64) { return 9; }
  b = broker_core.cancel(b); b = broker_core.reserve(b, 0, 8i64);
  if (b.status != 8 || b.free_count != 1 || b.producer_slot != -1 || b.serial != 9223372036854775807i64 || __heap_alloc_count() != mark) { return 10; }
  return 0;
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", "broker_core.fern"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "broker_core.fern"), data, 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(path, []byte("import \"./broker_core\";\n"+brokerBoundaryHelpers+tc.source), 0600); err != nil {
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

func TestSelfHostBrokerRejectsFreshMessage(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, claim := range []string{"fip", "fbip"} {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(claim+"/"+target, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "main.fern")
				source := fmt.Sprintf("struct Message { payload: i64 }\n%s function fresh(value: i64): Message { return Message { payload: value }; }\nfunction main(): i32 { return fresh(7i64).payload as i32; }\n", claim)
				if err := os.WriteFile(path, []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := runX86_64Bin(cli.runner, cli.bin, "-target", target, "-emit", "asm", "-o", filepath.Join(dir, "out"), path, cli.stdlib)
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "E068") {
					t.Fatalf("expected fresh allocation refusal, got %v: %s", err, out)
				}
			})
		}
	}
}
