package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

// CellBytesSource tests byte snapshots through shared cells and containers.
// Distinct return codes identify the violated value/ownership contract.
const CellBytesSource = `struct Holder { c: Cell[u8[]] }
enum Wrapped { Has(Cell[u8[]]), Missing }
function size(b: u8[]): i32 { return b.len(); }
function exercise(): i32 {
  let data: u8[] = [];
  let i: i32 = 0;
  while (i < 256) { data = data.append(i as u8); i = i + 1; }
  let c: Cell[u8[]] = cell_new(data);
  let held: u8[] = c.get();
  c.set(c.get());
  c.set(c.get().append(77 as u8));
  if (held.len() != 256 || data.len() != 256 || c.get().len() != 257) { return 1; }
  i = 0;
  while (i < 256) {
    if (held[i] != i as u8 || data[i] != i as u8) { return 2; }
    i = i + 1;
  }
  let box: Holder = Holder { c: c };
  let pair: (i32, Cell[u8[]]) = (1, c);
  let wrapped: Wrapped = Has(c);
  let mutate: () => void = (): void => { box.c.set([255 as u8, 0 as u8, 128 as u8]); };
  mutate();
  if (pair.1.get().len() != 3) { return 3; }
  match (wrapped) {
    Has(alias) => {
      let saved: u8[] = alias.get();
      alias.set([]);
      if (saved.len() != 3 || saved[0] != 255 as u8 || saved[1] != 0 as u8 || saved[2] != 128 as u8) { return 4; }
    },
    Missing => { return 5; }
  }
  if (size(c.get()) != 0 || held.len() != 256) { return 6; }
  i = 0;
  while (i < 64) {
    c.set([i as u8, 255 as u8]);
    let snapshot: u8[] = c.get();
    c.set(c.get().with(0, 200 as u8));
    if (snapshot[0] != i as u8 || c.get()[0] != 200 as u8) { return 7; }
    i = i + 1;
  }
  return 0;
}
function main(): i32 {
  let i: i32 = 0;
  while (i < 8) {
    let result: i32 = exercise();
    if (result != 0) { return result; }
    i = i + 1;
  }
  return 0;
}
`

func WriteCellBytesFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cell-bytes.fern")
	if err := os.WriteFile(p, []byte(CellBytesSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
