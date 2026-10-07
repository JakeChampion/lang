package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type storageFrame struct {
	page, pin int
	dirty     bool
	touched   int64
	data      []int64
}
type storageRequest struct {
	phase, kind, page, offset, delay, fault, frame, code int
	value, ticket, due, result                           int64
}
type storageAction struct {
	op, kind, page, offset, delay, fault, slot int
	value, ticket                              int64
}
type storageModel struct {
	disk                                                               [][]int64
	frames                                                             []storageFrame
	requests                                                           []storageRequest
	pins                                                               []int
	width, batch, outstanding, cursor, work, transitions, status, slot int
	serial, tick, ticket, result                                       int64
}

func newStorageModel(pages, frames, width, capacity, batch int) *storageModel {
	m := &storageModel{disk: make([][]int64, pages), frames: make([]storageFrame, frames), requests: make([]storageRequest, capacity), pins: make([]int, pages), width: width, batch: batch, slot: -1}
	for p := range m.disk {
		m.disk[p] = make([]int64, width)
		for w := range m.disk[p] {
			m.disk[p][w] = int64((p+1)*10000 + w)
		}
	}
	for f := range m.frames {
		m.frames[f] = storageFrame{page: -1, data: make([]int64, width)}
	}
	for i := range m.requests {
		m.requests[i].frame = -1
	}
	return m
}

func (m *storageModel) answer(code, slot int, ticket, value int64) {
	m.status, m.slot, m.ticket, m.result = code, slot, ticket, value
}
func (m *storageModel) finish(slot, code int, value int64) {
	r := &m.requests[slot]
	r.phase, r.code, r.result = 3, code, value
}

func (m *storageModel) dispatch(slot int) {
	r := &m.requests[slot]
	if m.pins[r.page] != 0 {
		m.finish(slot, 10, 0)
		return
	}
	frame := -1
	for f, value := range m.frames {
		if value.page == r.page {
			frame = f
			break
		}
	}
	if frame >= 0 && m.frames[frame].pin != 0 {
		m.finish(slot, 10, 0)
		return
	}
	if frame < 0 {
		if r.kind >= 2 {
			m.finish(slot, 12, 0)
			return
		}
		// Sort eligible victims by unused first, then age and stable frame id.
		var candidates []int
		for f, value := range m.frames {
			if value.pin == 0 && !value.dirty {
				candidates = append(candidates, f)
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			a, b := m.frames[candidates[i]], m.frames[candidates[j]]
			if (a.page < 0) != (b.page < 0) {
				return a.page < 0
			}
			if a.page >= 0 && a.touched != b.touched {
				return a.touched < b.touched
			}
			return candidates[i] < candidates[j]
		})
		if len(candidates) == 0 {
			m.finish(slot, 11, 0)
			return
		}
		frame = candidates[0]
	}
	if r.kind == 3 && m.frames[frame].dirty {
		m.finish(slot, 13, 0)
		return
	}
	if m.tick > 9223372036854775807-int64(r.delay) {
		m.finish(slot, 15, 0)
		return
	}
	r.frame, r.phase, r.due = frame, 2, m.tick+int64(r.delay)
	m.frames[frame].pin = slot + 1
	m.pins[r.page] = slot + 1
}

func (m *storageModel) complete(slot int) {
	r := &m.requests[slot]
	f := &m.frames[r.frame]
	f.pin = 0
	m.pins[r.page] = 0
	if r.fault != 0 {
		m.finish(slot, 14, 0)
		return
	}
	if r.kind < 2 && f.page != r.page {
		copy(f.data, m.disk[r.page])
		f.page, f.dirty = r.page, false
	}
	var value int64
	switch r.kind {
	case 0:
		value = f.data[r.offset]
	case 1:
		value = r.value
		f.data[r.offset] = r.value
		f.dirty = true
	case 2:
		if f.dirty {
			copy(m.disk[r.page], f.data)
		}
		f.dirty = false
	case 3:
		f.page = -1
	}
	f.touched = m.tick
	if r.kind == 3 {
		f.touched = 0
	}
	m.finish(slot, 0, value)
}

func (m *storageModel) apply(a storageAction) {
	switch a.op {
	case 0:
		if a.kind < 0 || a.kind > 3 || a.page < 0 || a.page >= len(m.disk) || a.offset < 0 || a.offset >= m.width || a.delay < 0 || a.delay > 1024 {
			m.answer(1, -1, 0, 0)
			return
		}
		if m.serial == 9223372036854775807 || m.tick == 9223372036854775807 {
			m.answer(5, -1, 0, 0)
			return
		}
		if m.outstanding == len(m.requests) {
			m.answer(2, -1, 0, 0)
			return
		}
		for slot, r := range m.requests {
			if r.phase == 0 {
				m.serial++
				m.outstanding++
				m.requests[slot] = storageRequest{phase: 1, kind: a.kind, page: a.page, offset: a.offset, delay: a.delay, fault: a.fault, frame: -1, value: a.value, ticket: m.serial}
				m.answer(0, slot, m.serial, 0)
				return
			}
		}
	case 1:
		m.work, m.transitions = 0, 0
		if m.tick == 9223372036854775807 {
			m.answer(5, -1, 0, 0)
			return
		}
		m.tick++
		for i := 0; i < m.batch; i++ {
			slot := m.cursor
			r := m.requests[slot]
			if r.phase == 1 {
				m.dispatch(slot)
				m.transitions++
			} else if r.phase == 2 && r.due <= m.tick {
				m.complete(slot)
				m.transitions++
			}
			m.cursor = (slot + 1) % len(m.requests)
			m.work++
		}
		m.answer(0, -1, 0, 0)
	case 2:
		if a.slot < 0 || a.slot >= len(m.requests) {
			m.answer(1, -1, 0, 0)
			return
		}
		r := &m.requests[a.slot]
		if r.phase == 0 || r.ticket != a.ticket {
			m.answer(3, a.slot, a.ticket, 0)
			return
		}
		if r.phase != 3 {
			m.answer(4, a.slot, a.ticket, 0)
			return
		}
		r.phase = 0
		m.outstanding--
		m.answer(r.code, a.slot, a.ticket, r.result)
	}
}

func (m *storageModel) state() []int64 {
	v := []int64{m.serial, m.tick, int64(m.outstanding), int64(m.cursor), int64(m.work), int64(m.transitions), int64(m.status), int64(m.slot), m.ticket, m.result}
	for _, page := range m.disk {
		v = append(v, page...)
	}
	for _, f := range m.frames {
		dirty := int64(0)
		if f.dirty {
			dirty = 1
		}
		v = append(v, int64(f.page), dirty, int64(f.pin), f.touched)
		v = append(v, f.data...)
	}
	for _, pin := range m.pins {
		v = append(v, int64(pin))
	}
	for _, r := range m.requests {
		v = append(v, int64(r.phase), int64(r.kind), int64(r.page), int64(r.offset), r.value, int64(r.delay), int64(r.fault), int64(r.frame), r.ticket, r.due, int64(r.code), r.result)
	}
	return v
}

const storageModelHelpers = `
function matches(s: storage_core.State, v: i64[]): boolean {
  if (s.serial!=v[0] || s.tick!=v[1] || s.outstanding as i64!=v[2] || s.cursor as i64!=v[3] || s.work as i64!=v[4] || s.transitions as i64!=v[5]
    || s.status as i64!=v[6] || s.result_slot as i64!=v[7] || s.result_ticket!=v[8] || s.result_value!=v[9]) { return false; }
  if (s.outstanding<0 || s.outstanding>s.capacity || s.work>s.batch || s.transitions>s.work) { return false; }
  let at:i32=10; let i:i32=0;
  while(i<s.disk.len()) { if(s.disk[i]!=v[at]) { return false; } i=i+1; at=at+1; }
  i=0;
  while(i<s.frames) {
    if(s.mapped[i] as i64!=v[at] || s.dirty[i] as i64!=v[at+1] || s.frame_pin[i] as i64!=v[at+2] || s.touched[i]!=v[at+3]) { return false; }
    at=at+4; let w:i32=0; while(w<s.width) { if(s.cache[i*s.width+w]!=v[at]) { return false; } w=w+1; at=at+1; } i=i+1;
  }
  i=0; while(i<s.pages) { if(s.page_pin[i] as i64!=v[at]) { return false; } i=i+1; at=at+1; }
  i=0; while(i<s.capacity) {
    if(s.phase[i] as i64!=v[at] || s.kind[i] as i64!=v[at+1] || s.page[i] as i64!=v[at+2] || s.offset[i] as i64!=v[at+3] || s.value[i]!=v[at+4]
      || s.delay[i] as i64!=v[at+5] || s.fault[i] as i64!=v[at+6] || s.frame[i] as i64!=v[at+7] || s.ticket[i]!=v[at+8] || s.due[i]!=v[at+9]
      || s.completion_status[i] as i64!=v[at+10] || s.completion_value[i]!=v[at+11]) { return false; }
    i=i+1; at=at+12;
  }
  return at==v.len();
}
function step(own s:storage_core.State, a:i64[]):storage_core.State {
  if(a[0]==0i64) { return storage_fip.submit(s,a[1] as i32,a[2] as i32,a[3] as i32,a[4],a[5] as i32,a[6]!=0i64); }
  if(a[0]==1i64) { return storage_fip.advance(s); }
  return storage_fip.collect(s,a[7] as i32,a[8]);
}
`

func storageI64Literal(values []int64) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.FormatInt(v, 10) + "i64"
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestSelfHostFipStorage(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range []struct {
		name                                  string
		pages, frames, width, capacity, batch int
	}{{"pilot", 3, 2, 3, 2, 2}, {"round_robin", 5, 2, 4, 3, 1}, {"pressure", 4, 1, 2, 4, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			m := newStorageModel(tc.pages, tc.frames, tc.width, tc.capacity, tc.batch)
			var actions []storageAction
			states := [][]int64{m.state()}
			add := func(a storageAction) { actions = append(actions, a); m.apply(a); states = append(states, m.state()) }
			// Independent sequential workload exercises every operation and fault
			// before queued competition. Poll only after a bounded drain period.
			for _, kind := range []int{0, 1, 3, 2, 2, 3, 0, 1, 2, 3} {
				fault := 0
				if kind == 2 && m.serial%2 == 0 {
					fault = 1
				}
				add(storageAction{op: 0, kind: kind, page: 0, offset: tc.width - 1, value: -77, delay: 2, fault: fault})
				slot, ticket := m.slot, m.ticket
				add(storageAction{op: 2, slot: slot, ticket: ticket})
				for i := 0; i < 2*tc.capacity+3; i++ {
					add(storageAction{op: 1})
				}
				add(storageAction{op: 2, slot: slot, ticket: ticket})
				add(storageAction{op: 2, slot: slot, ticket: ticket})
			}
			for i := 0; i < tc.capacity+1; i++ {
				add(storageAction{op: 0, kind: i % 2, page: i % tc.pages, offset: 0, value: int64(i * 9), delay: 3})
			}
			for i := 0; i < tc.capacity*3+4; i++ {
				add(storageAction{op: 1})
			}
			// Completed but unpolled slots still enforce the outstanding bound.
			add(storageAction{op: 0, kind: 0, page: 0})
			for i := range m.requests {
				add(storageAction{op: 2, slot: i, ticket: m.requests[i].ticket})
			}
			rng := rand.New(rand.NewSource(9590))
			for i := 0; i < 100; i++ {
				a := storageAction{op: rng.Intn(3), kind: rng.Intn(5) - 1, page: rng.Intn(tc.pages+2) - 1, offset: rng.Intn(tc.width), value: int64(i*17 - 900), delay: rng.Intn(4), fault: i % 2, slot: rng.Intn(tc.capacity+2) - 1, ticket: -1}
				if a.op == 2 && a.slot >= 0 && a.slot < tc.capacity && i%3 != 0 {
					a.ticket = m.requests[a.slot].ticket
				}
				add(a)
			}
			var fixtures, expected, events strings.Builder
			for i, state := range states {
				fmt.Fprintf(&fixtures, "@noinline function fixture%d():i64[] {return %s;}\n", i, storageI64Literal(state))
				if i > 0 {
					expected.WriteString(",")
				}
				fmt.Fprintf(&expected, "fixture%d()", i)
			}
			for i, a := range actions {
				if i > 0 {
					events.WriteString(",")
				}
				events.WriteString(storageI64Literal([]int64{int64(a.op), int64(a.kind), int64(a.page), int64(a.offset), a.value, int64(a.delay), int64(a.fault), int64(a.slot), a.ticket}))
			}
			source := "import \"./storage_core\";\nimport \"./storage_fip\";\n" + fixtures.String() + storageModelHelpers + fmt.Sprintf(`
function main():i32 {
  let expected:i64[][]=[%s]; let events:i64[][]=[%s];
  match(storage_core.new_state(%d,%d,%d,%d,%d)) {None=>{return 1;},Some(s)=>{
    let mark:i64=__heap_alloc_count(); if(!matches(s,expected[0])) {return 2;}
    let i:i32=0; while(i<events.len()) {s=step(s,events[i]);if(!matches(s,expected[i+1])) {return 3;}if(__heap_alloc_count()!=mark) {return 4;} i=i+1;}return 0;
  }}
}
`, expected.String(), events.String(), tc.pages, tc.frames, tc.width, tc.capacity, tc.batch)
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
