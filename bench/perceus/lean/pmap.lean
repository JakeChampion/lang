-- A persistent red-black map whose key is two words, compared in order.
structure Key where
  hi : Int
  lo : Int

inductive Color where
  | red | black

inductive PMap where
  | leaf : PMap
  | node : Color → PMap → Key → Int → PMap → PMap

open Color PMap

def compareKey (a b : Key) : Int :=
  if a.hi != b.hi then (if a.hi < b.hi then -1 else 1)
  else if a.lo != b.lo then (if a.lo < b.lo then -1 else 1)
  else 0

def isRed : PMap → Bool
  | node red .. => true
  | _ => false

@[inline] def balanceLeft : PMap → Key → Int → PMap → PMap
  | node _ (node red lx kx vx rx) ky vy ry, k, v, r => node red (node black lx kx vx rx) ky vy (node black ry k v r)
  | node _ ly ky vy (node red lx kx vx rx), k, v, r => node red (node black ly ky vy lx) kx vx (node black rx k v r)
  | node _ lx kx vx rx, k, v, r => node black (node red lx kx vx rx) k v r
  | leaf, _, _, _ => leaf

@[inline] def balanceRight : PMap → Key → Int → PMap → PMap
  | l, k, v, node _ (node red lx kx vx rx) ky vy ry => node red (node black l k v lx) kx vx (node black rx ky vy ry)
  | l, k, v, node _ lx kx vx (node red ly ky vy ry) => node red (node black l k v lx) kx vx (node black ly ky vy ry)
  | l, k, v, node _ lx kx vx rx => node black l k v (node red lx kx vx rx)
  | _, _, _, leaf => leaf

def ins : PMap → Key → Int → PMap
  | node red l kx vx r, k, v =>
    let c := compareKey k kx
    if c < 0 then node red (ins l k v) kx vx r
    else if c > 0 then node red l kx vx (ins r k v)
    else node red l k v r
  | node black l kx vx r, k, v =>
    let c := compareKey k kx
    if c < 0 then (if isRed l then balanceLeft (ins l k v) kx vx r else node black (ins l k v) kx vx r)
    else if c > 0 then (if isRed r then balanceRight l kx vx (ins r k v) else node black l kx vx (ins r k v))
    else node black l k v r
  | leaf, k, v => node red leaf k v leaf

def setBlack : PMap → PMap
  | node _ l k v r => node black l k v r
  | t => t

def insert (t : PMap) (k : Key) (v : Int) : PMap := setBlack (ins t k v)

def find : PMap → Key → Int
  | node _ l kx vx r, k =>
    let c := compareKey k kx
    if c < 0 then find l k else if c > 0 then find r k else vx
  | leaf, _ => -1

def next (x : Int) : Int := (1103515245 * x + 12345) % 2147483648

def keyOf (x : Int) : Key := { hi := x % 1024, lo := x / 1024 }

def build : PMap → Nat → Nat → Int → PMap
  | m, _, 0, _ => m
  | m, i, k + 1, x =>
    let y := next x
    build (insert m (keyOf y) i) (i + 1) k y

def look (m : PMap) : Nat → Int → Int → Int → Int × Int
  | 0, _, hits, sum => (hits, sum)
  | k + 1, x, hits, sum =>
    let y := next x
    let v := find m (keyOf y)
    if v >= 0 then look m k y (hits + 1) (sum + v) else look m k y hits sum

def main (args : List String) : IO UInt32 := do
  let n := (args.headD "1000000").toNat!
  let m := build leaf 0 n 42
  let (hits, sum) := look m n 42 0 0
  let (otherHits, otherSum) := look m n 7 0 0
  IO.println s!"{hits} {sum} {otherHits} {otherSum}"
  return 0
