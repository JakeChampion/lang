-- A map over a tree that rebuilds each node. A tree with one count is rebuilt
-- in place.
inductive Tree where
  | nil : Tree
  | node : Int → Tree → Tree → Tree

def make (item : Int) : Nat → Tree
  | 0 => .node item .nil .nil
  | d + 1 => .node item (make (2 * item - 1) d) (make (2 * item) d)

def bump : Tree → Tree
  | .nil => .nil
  | .node item l r => .node (item + 1) (bump l) (bump r)

def total : Tree → Int
  | .nil => 0
  | .node item l r => item + total l + total r

def rounds : Tree → Nat → Tree
  | t, 0 => t
  | t, k + 1 => rounds (bump t) k

def main (args : List String) : IO UInt32 := do
  let n := (args.headD "20").toNat!
  IO.println s!"{total (rounds (make 1 n) 20)}"
  return 0
