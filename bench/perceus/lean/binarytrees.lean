-- The binary-trees workload of the Computer Language Benchmarks Game, in the
-- form where each node holds an item.
inductive Tree where
  | nil : Tree
  | node : Int → Tree → Tree → Tree

def make (item : Int) : Nat → Tree
  | 0 => .node item .nil .nil
  | d + 1 => .node item (make (2 * item - 1) d) (make (2 * item) d)

def check : Tree → Int
  | .nil => 0
  | .node item l r => item + check l - check r

def sweep (depth : Nat) (iterations : Nat) : Nat → Int → Int
  | 0, total => total
  | k + 1, total =>
    let i : Int := iterations - k
    sweep depth iterations k (total + check (make i depth) + check (make (0 - i) depth))

partial def levels (depth low high : Nat) : IO Unit := do
  if depth <= high then
    let iterations := 2 ^ (high - depth + low)
    IO.println s!"{2 * iterations} trees of depth {depth} check: {sweep depth iterations iterations 0}"
    levels (depth + 2) low high

def main (args : List String) : IO UInt32 := do
  let n := (args.headD "21").toNat!
  let low := 4
  let high := if n < low + 2 then low + 2 else n
  IO.println s!"stretch tree of depth {high + 1} check: {check (make 0 (high + 1))}"
  let longLived := make 0 high
  levels low low high
  IO.println s!"long lived tree of depth {high} check: {check longLived}"
  return 0
