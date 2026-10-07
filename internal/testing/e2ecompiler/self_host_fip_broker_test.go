package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type brokerMessage struct{ topic, serial, payload int64 }
type brokerAction struct {
	kind, argument int
	value          int64
}
type brokerModel struct {
	topics, subscribers, capacity int
	slots                         []brokerMessage
	free, phase                   []int
	queues                        [][]int
	leases                        []int
	producer                      brokerMessage
	producerSlot, status, result  int
	serial                        int64
}

func newBrokerModel(topics, subscribers, pool, capacity int) *brokerModel {
	m := &brokerModel{topics: topics, subscribers: subscribers, capacity: capacity,
		slots: make([]brokerMessage, pool), phase: make([]int, pool), queues: make([][]int, subscribers),
		leases: make([]int, subscribers), producer: brokerMessage{topic: -1}, producerSlot: -1, result: -1}
	for i := range m.slots {
		m.slots[i].topic = -1
		m.free = append(m.free, pool-i-1)
	}
	for i := range m.leases {
		m.leases[i] = -1
	}
	return m
}

func (m *brokerModel) apply(a brokerAction) {
	m.status, m.result = 0, -1
	fail := func(status int) { m.status = status }
	switch a.kind {
	case 0: // reserve
		if a.argument < 0 || a.argument >= m.topics {
			fail(1)
			return
		}
		if m.producerSlot >= 0 {
			fail(2)
			return
		}
		if a.argument >= m.subscribers {
			fail(9)
			return
		}
		if m.serial == 9223372036854775807 {
			fail(8)
			return
		}
		if len(m.free) == 0 {
			fail(3)
			return
		}
		slot := m.free[len(m.free)-1]
		m.free = m.free[:len(m.free)-1]
		m.serial++
		m.producer = brokerMessage{int64(a.argument), m.serial, a.value}
		m.producerSlot, m.phase[slot], m.result = slot, 1, slot
		m.slots[slot] = brokerMessage{topic: -1}
	case 1: // publish: no changes until every subscriber can receive
		if m.producerSlot < 0 {
			fail(6)
			return
		}
		for s, queue := range m.queues {
			if int64(s%m.topics) == m.producer.topic && len(queue) == m.capacity {
				fail(4)
				return
			}
		}
		slot := m.producerSlot
		m.slots[slot] = m.producer
		for s := range m.queues {
			if int64(s%m.topics) == m.producer.topic {
				m.queues[s] = append(m.queues[s], slot)
			}
		}
		m.phase[slot], m.result, m.producerSlot = 2, slot, -1
		m.producer = brokerMessage{topic: -1}
	case 2: // consume: independent slice FIFO, not a ring
		s := a.argument
		if s < 0 || s >= m.subscribers {
			fail(1)
			return
		}
		if m.leases[s] >= 0 {
			fail(6)
			return
		}
		if len(m.queues[s]) == 0 {
			fail(5)
			return
		}
		m.leases[s] = m.queues[s][0]
		m.queues[s] = m.queues[s][1:]
		m.result = m.leases[s]
	case 3: // ack: derive remaining references from queues and leases
		s := a.argument
		if s < 0 || s >= m.subscribers {
			fail(1)
			return
		}
		slot := m.leases[s]
		if slot < 0 {
			fail(6)
			return
		}
		if a.value != m.slots[slot].serial {
			fail(7)
			return
		}
		m.leases[s], m.result = -1, slot
		if m.references(slot) == 0 {
			m.phase[slot] = 0
			m.free = append(m.free, slot)
		}
	case 4: // cancel
		slot := m.producerSlot
		if slot < 0 {
			fail(6)
			return
		}
		m.slots[slot] = m.producer
		m.producer = brokerMessage{topic: -1}
		m.producerSlot, m.result, m.phase[slot] = -1, slot, 0
		m.free = append(m.free, slot)
	}
}

func (m *brokerModel) references(slot int) int {
	n := 0
	for s, queue := range m.queues {
		if m.leases[s] == slot {
			n++
		}
		for _, queued := range queue {
			if queued == slot {
				n++
			}
		}
	}
	return n
}

func (m *brokerModel) state() []int64 {
	values := []int64{int64(len(m.free)), int64(m.producerSlot), m.serial, int64(m.status), int64(m.result), m.producer.topic, m.producer.serial, m.producer.payload}
	for i := range m.slots {
		free := -1
		if i < len(m.free) {
			free = m.free[i]
		}
		values = append(values, int64(free))
	}
	for i, message := range m.slots {
		values = append(values, int64(m.phase[i]), int64(m.references(i)), message.topic, message.serial, message.payload)
	}
	for s, queue := range m.queues {
		message := brokerMessage{topic: -1}
		if m.leases[s] >= 0 {
			message = m.slots[m.leases[s]]
		}
		values = append(values, int64(len(queue)), int64(m.leases[s]), message.topic, message.serial, message.payload)
		for i := 0; i < m.capacity; i++ {
			slot := -1
			if i < len(queue) {
				slot = queue[i]
			}
			values = append(values, int64(slot))
		}
	}
	return values
}

const brokerModelHelpers = `
function matches(b: broker_core.Broker, v: i64[]): boolean {
  if (b.free_count as i64 != v[0] || b.producer_slot as i64 != v[1] || b.serial != v[2] || b.status as i64 != v[3] || b.result_slot as i64 != v[4]
    || b.producer.topic as i64 != v[5] || b.producer.serial != v[6] || b.producer.payload != v[7]) { return false; }
  let at: i32 = 8; let i: i32 = 0;
  while (i < b.slots.len()) { let value: i32 = -1; if (i < b.free_count) { value = b.free[i]; } if (value as i64 != v[at]) { return false; } at = at + 1; i = i + 1; }
  i = 0;
  while (i < b.slots.len()) {
    if (b.phase[i] as i64 != v[at] || b.refs[i] as i64 != v[at+1] || b.slots[i].topic as i64 != v[at+2] || b.slots[i].serial != v[at+3] || b.slots[i].payload != v[at+4]) { return false; }
    at = at + 5; i = i + 1;
  }
  let s: i32 = 0;
  while (s < b.subscribers) {
    if (b.lengths[s] as i64 != v[at] || b.lease_slots[s] as i64 != v[at+1] || b.leases[s].topic as i64 != v[at+2] || b.leases[s].serial != v[at+3] || b.leases[s].payload != v[at+4]) { return false; }
    at = at + 5; i = 0;
    while (i < b.queue_capacity) {
      let slot: i32 = -1; if (i < b.lengths[s]) { slot = b.queues[s*b.queue_capacity+(b.heads[s]+i)%b.queue_capacity]; }
      if (slot as i64 != v[at]) { return false; } at = at + 1; i = i + 1;
    }
    s = s + 1;
  }
  return at == v.len();
}
function step(own b: broker_core.Broker, kind: i32, argument: i32, value: i64): broker_core.Broker {
  if (kind == 0) { return broker_core.reserve(b, argument, value); }
  if (kind == 1) { return broker_core.publish(b); }
  if (kind == 2) { return broker_core.consume(b, argument); }
  if (kind == 3) { return broker_core.acknowledge(b, argument, value); }
  return broker_core.cancel(b);
}
`

func TestSelfHostFipBroker(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		name                                string
		topics, subscribers, pool, capacity int
	}{
		{"pilot", 1, 1, 3, 2}, {"fanout", 2, 4, 7, 3}, {"single_slot", 1, 3, 1, 1}, {"unsubscribed_topic", 3, 1, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := newBrokerModel(tc.topics, tc.subscribers, tc.pool, tc.capacity)
			var actions []brokerAction
			states := [][]int64{model.state()}
			add := func(a brokerAction) {
				actions = append(actions, a)
				model.apply(a)
				states = append(states, model.state())
			}
			// Fill, hit queue or pool capacity, exercise asymmetric consumers, then
			// retry/cancel. Every result and complete state is checked independently.
			for i := 0; i < tc.pool+2; i++ {
				add(brokerAction{0, 0, int64(i)})
				add(brokerAction{1, 0, 0})
			}
			add(brokerAction{2, 0, 0})
			add(brokerAction{1, 0, 0})
			add(brokerAction{4, 0, 0})
			for i := 0; i < tc.capacity+2; i++ {
				for s := 0; s < tc.subscribers; s++ {
					add(brokerAction{2, s, 0})
					serial := int64(-1)
					if model.leases[s] >= 0 {
						serial = model.slots[model.leases[s]].serial
					}
					add(brokerAction{3, s, serial - 1})
					add(brokerAction{3, s, serial})
					add(brokerAction{3, s, serial})
				}
			}
			// Several complete cycles force physical queue wrap and pool reuse.
			for i := 0; i < tc.capacity+3; i++ {
				add(brokerAction{0, 0, int64(-100 - i)})
				add(brokerAction{1, 0, 0})
				for s := 0; s < tc.subscribers; s++ {
					add(brokerAction{2, s, 0})
					add(brokerAction{3, s, model.serial})
				}
			}
			rng := rand.New(rand.NewSource(9589))
			for i := 0; i < 80; i++ {
				a := brokerAction{rng.Intn(5), rng.Intn(tc.subscribers+2) - 1, rng.Int63n(1000) - 500}
				if a.kind == 0 {
					a.argument = rng.Intn(tc.topics+2) - 1
				}
				if a.kind == 3 && a.argument >= 0 && a.argument < tc.subscribers && model.leases[a.argument] >= 0 && i%2 == 0 {
					a.value = model.slots[model.leases[a.argument]].serial
				}
				add(a)
			}
			var fixtures, expected, kinds, arguments, values strings.Builder
			for i, state := range states {
				parts := make([]string, len(state))
				for j, value := range state {
					parts[j] = strconv.FormatInt(value, 10) + "i64"
				}
				fmt.Fprintf(&fixtures, "@noinline function fixture%d(): i64[] { return [%s]; }\n", i, strings.Join(parts, ","))
				if i > 0 {
					expected.WriteString(",")
				}
				fmt.Fprintf(&expected, "fixture%d()", i)
			}
			for i, a := range actions {
				if i > 0 {
					kinds.WriteString(",")
					arguments.WriteString(",")
					values.WriteString(",")
				}
				fmt.Fprint(&kinds, a.kind)
				fmt.Fprint(&arguments, a.argument)
				fmt.Fprintf(&values, "%di64", a.value)
			}
			source := "import \"./broker_core\";\n" + fixtures.String() + brokerModelHelpers + fmt.Sprintf(`
function main(): i32 {
  let expected: i64[][] = [%s]; let kinds: i32[] = [%s]; let arguments: i32[] = [%s]; let values: i64[] = [%s];
  match (broker_core.new_broker(%d, %d, %d, %d)) {
    None => { return 1; }, Some(b) => {
      let mark: i64 = __heap_alloc_count(); if (!matches(b, expected[0])) { return 2; }
      let i: i32 = 0; while (i < kinds.len()) {
        b = step(b, kinds[i], arguments[i], values[i]);
        if (!matches(b, expected[i+1])) { return 3; }
        if (__heap_alloc_count() != mark) { return 4; }
        i = i + 1;
      }
      return 0;
    }
  }
}
`, expected.String(), kinds.String(), arguments.String(), values.String(), tc.topics, tc.subscribers, tc.pool, tc.capacity)
			dir := t.TempDir()
			data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", "broker_core.fern"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "broker_core.fern"), data, 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
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
