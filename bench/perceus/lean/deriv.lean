/- Benchmark for new code generator -/
inductive Expr
| Val : Int → Expr
| Var : String → Expr
| Add : Expr → Expr → Expr
| Mul : Expr → Expr → Expr
| Pow : Expr → Expr → Expr
| Ln  : Expr → Expr

open Expr

partial def pown : Int → Int → Int
| a, 0 => 1
| a, 1 => a
| a, n =>
  let b := pown a (n / 2);
  b * b * (if n % 2 = 0 then 1 else a)

partial def add : Expr → Expr → Expr
| Val n,     Val m           => Val (n + m)
| Val 0,     f               => f
| f,         Val 0           => f
| f,         Val n           => add (Val n) f
| Val n,     Expr.Add (Val m) f   => add (Val (n+m)) f
| f,         Expr.Add (Val n) g   => add (Val n) (add f g)
| Expr.Add f g,   h               => add f (add g h)
| f,         g               => Expr.Add f g

partial def mul : Expr → Expr → Expr
| Val n,     Val m           => Val (n*m)
| Val 0,     _               => Val 0
| _,         Val 0           => Val 0
| Val 1,     f               => f
| f,         Val 1           => f
| f,         Val n           => mul (Val n) f
| Val n,     Expr.Mul (Val m) f   => mul (Val (n*m)) f
| f,         Expr.Mul (Val n) g   => mul (Val n) (mul f g)
| Expr.Mul f g,   h               => mul f (mul g h)
| f,         g               => Expr.Mul f g

def pow : Expr → Expr → Expr
| Val m,   Val n   => Val (pown m n)
| _,       Val 0   => Val 1
| f,       Val 1   => f
| Val 0,   _       => Val 0
| f,       g       => Expr.Pow f g

def ln : Expr → Expr
| Val 1   => Val 0
| f       => Ln f

def d (x : String) : Expr → Expr
| Val _     => Val 0
| Var y     => if x = y then Val 1 else Val 0
| Expr.Add f g   => add (d x f) (d x g)
| Expr.Mul f g   => add (mul f (d x g)) (mul g (d x f))
| Expr.Pow f g   => mul (pow f g) (add (mul (mul g (d x f)) (pow f (Val (-1)))) (mul (ln f) (d x g)))
| Ln f      => mul (d x f) (pow f (Val (-1)))

def count : Expr → UInt32
| Val _   => 1
| Var _   => 1
| Expr.Add f g   => count f + count g
| Expr.Mul f g   => count f + count g
| Expr.Pow f g   => count f + count g
| Ln f      => count f


def nestAux (s : Nat) (f : Nat → Expr → IO Expr) : Nat → Expr → IO Expr
| 0, e => pure e
| n+1, e => do
  let next ← f (s - (n+1)) e
  nestAux s f n next

def deriv (i : Nat) (e : Expr) : IO Expr := do
  let next := d "x" e
  IO.println (toString (i+1) ++ " count: " ++ toString (count next))
  return next

def main (args : List String) : IO UInt32 := do
  let n := (args.headD "10").toNat!
  let x := Var "x"
  let _ ← nestAux n deriv n (pow x x)
  IO.println "done"
  return 0
