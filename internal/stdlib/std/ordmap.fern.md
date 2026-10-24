# Persistent ordered maps

This module provides persistent ordered maps.

## Model

A map uses a balanced binary tree with cached subtree sizes. Updates return a new map and preserve the previous map value.

## Contracts

Key order comes from `cmp.Ord`. Ordered traversal follows that comparison. A missing key produces an optional result.

## Executable example

```fern test name=std-ordmap-example
import "std/ordmap";

function main(): i32 {
  let original = ordmap.ordmap_new[string, i32]();
  let changed = original.insert("a", 1);
  if (!original.is_empty() || changed.len() != 1) { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<dependencies 2>>

<<3 pub enum OrdMap[K, V]>>

<<4 pub function ordmap_new[K, V](): OrdMap[K, V]>>

<<5 function node_size[K, V](t: OrdMap[K, V]): i32>>

<<6 function node_min_node[K, V](t: OrdMap[K, V]): OrdMap[K, V]>>

<<7 function node_delete[K: cmp.Ord, V](t: OrdMap[K, V], k: K): OrdMap[K, V]>>

<<8 struct OrdSplit[K, V]>>

<<9 function node_split[K: cmp.Ord, V](t: OrdMap[K, V], k: K): OrdSplit[K, V]>>

<<10 function node_fold[K, V, A](t: OrdMap[K, V], init: A, f: (A, K, V)>>

<<11 function node_valid[K: cmp.Ord, V](t: OrdMap[K, V]): boolean>>

<<12 pub function (m: OrdMap[K, V]) len(): i32>>

<<13 pub function (m: OrdMap[K, V]) iter(): iter.ArrayIter[(K, V)]>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides persistent ordered maps.
```

### `dependencies 2`

```fern
<<dependencies 2>>=
import "core/cmp";
import "core/iter";
```

### `3 pub enum OrdMap[K, V]`

```fern
<<3 pub enum OrdMap[K, V]>>=
pub enum OrdMap[K, V] { Tip, Bin(OrdMap[K, V], K, V, OrdMap[K, V], i32) }
```

### `4 pub function ordmap_new[K, V](): OrdMap[K, V]`

```fern
<<4 pub function ordmap_new[K, V](): OrdMap[K, V]>>=
pub function ordmap_new[K, V](): OrdMap[K, V] {
  let e: OrdMap[K, V] = Tip;
  return e;
}

pub function from[K: cmp.Ord, V](pairs: (K, V)[]): OrdMap[K, V] {
  let m: OrdMap[K, V] = ordmap_new();
  for p in pairs {
    m = m.insert(p.0, p.1);
  }
  return m;
}
```

### `5 function node_size[K, V](t: OrdMap[K, V]): i32`

```fern
<<5 function node_size[K, V](t: OrdMap[K, V]): i32>>=
function node_size[K, V](t: OrdMap[K, V]): i32 {
  match (t) {
    Tip => {
      return 0;
    },
    Bin(l, k, v, r, s) => {
      return s;
    }
  }
}

function node_bin[K, V](l: OrdMap[K, V], k: K, v: V, r: OrdMap[K, V]): OrdMap[K, V] {
  return Bin(l, k, v, r, node_size(l) + node_size(r) + 1);
}

function node_single[K, V](k: K, v: V): OrdMap[K, V] {
  let e: OrdMap[K, V] = Tip;
  return Bin(e, k, v, e, 1);
}

function node_rotate_l[K, V](l: OrdMap[K, V], k: K, v: V, r: OrdMap[K, V]): OrdMap[K, V] {
  match (r) {
    Tip => {
      return node_bin(l, k, v, r);
    },
    Bin(rl, rk, rv, rr, rs) => {
      if (node_size(rl) < 2 * node_size(rr)) {
        let nl: OrdMap[K, V] = node_bin(l, k, v, rl);
        return node_bin(nl, rk, rv, rr);
      }
      match (rl) {
        Tip => {
          let nl: OrdMap[K, V] = node_bin(l, k, v, rl);
          return node_bin(nl, rk, rv, rr);
        },
        Bin(rll, rlk, rlv, rlr, rls) => {
          let nl: OrdMap[K, V] = node_bin(l, k, v, rll);
          let nr: OrdMap[K, V] = node_bin(rlr, rk, rv, rr);
          return node_bin(nl, rlk, rlv, nr);
        }
      }
    }
  }
}

function node_rotate_r[K, V](l: OrdMap[K, V], k: K, v: V, r: OrdMap[K, V]): OrdMap[K, V] {
  match (l) {
    Tip => {
      return node_bin(l, k, v, r);
    },
    Bin(ll, lk, lv, lr, ls) => {
      if (node_size(lr) < 2 * node_size(ll)) {
        let nr: OrdMap[K, V] = node_bin(lr, k, v, r);
        return node_bin(ll, lk, lv, nr);
      }
      match (lr) {
        Tip => {
          let nr: OrdMap[K, V] = node_bin(lr, k, v, r);
          return node_bin(ll, lk, lv, nr);
        },
        Bin(lrl, lrk, lrv, lrr, lrs) => {
          let nl: OrdMap[K, V] = node_bin(ll, lk, lv, lrl);
          let nr: OrdMap[K, V] = node_bin(lrr, k, v, r);
          return node_bin(nl, lrk, lrv, nr);
        }
      }
    }
  }
}

function node_balance[K, V](l: OrdMap[K, V], k: K, v: V, r: OrdMap[K, V]): OrdMap[K, V] {
  let sl: i32 = node_size(l);
  let sr: i32 = node_size(r);
  if (sl + sr <= 1) {
    return Bin(l, k, v, r, sl + sr + 1);
  }
  if (sr > 3 * sl) {
    return node_rotate_l(l, k, v, r);
  }
  if (sl > 3 * sr) {
    return node_rotate_r(l, k, v, r);
  }
  return Bin(l, k, v, r, sl + sr + 1);
}

function node_insert[K: cmp.Ord, V](t: OrdMap[K, V], k: K, v: V): OrdMap[K, V] {
  match (t) {
    Tip => {
      return node_single(k, v);
    },
    Bin(l, nk, nv, r, s) => {
      let c: i32 = k.cmp(nk);
      if (c < 0) {
        let nl: OrdMap[K, V] = node_insert(l, k, v);
        return node_balance(nl, nk, nv, r);
      }
      if (c > 0) {
        let nr: OrdMap[K, V] = node_insert(r, k, v);
        return node_balance(l, nk, nv, nr);
      }
      return Bin(l, k, v, r, s);
    }
  }
}

function node_find[K: cmp.Ord, V](t: OrdMap[K, V], k: K): OrdMap[K, V] {
  let cur: OrdMap[K, V] = t;
  let searching: boolean = true;
  while (searching) {
    match (cur) {
      Tip => {
        searching = false;
      },
      Bin(l, nk, nv, r, s) => {
        let c: i32 = k.cmp(nk);
        if (c == 0) {
          return cur;
        }
        if (c < 0) {
          cur = l;
        } else {
          cur = r;
        }
      }
    }
  }
  return cur;
}
```

### `6 function node_min_node[K, V](t: OrdMap[K, V]): OrdMap[K, V]`

```fern
<<6 function node_min_node[K, V](t: OrdMap[K, V]): OrdMap[K, V]>>=
function node_min_node[K, V](t: OrdMap[K, V]): OrdMap[K, V] {
  let cur: OrdMap[K, V] = t;
  let walking: boolean = true;
  while (walking) {
    match (cur) {
      Tip => {
        walking = false;
      },
      Bin(l, k, v, r, s) => {
        match (l) {
          Tip => {
            return cur;
          },
          Bin(ll, lk, lv, lr, ls) => {
            cur = l;
          }
        }
      }
    }
  }
  return cur;
}

function node_max_node[K, V](t: OrdMap[K, V]): OrdMap[K, V] {
  let cur: OrdMap[K, V] = t;
  let walking: boolean = true;
  while (walking) {
    match (cur) {
      Tip => {
        walking = false;
      },
      Bin(l, k, v, r, s) => {
        match (r) {
          Tip => {
            return cur;
          },
          Bin(rl, rk, rv, rr, rs) => {
            cur = r;
          }
        }
      }
    }
  }
  return cur;
}

function node_delete_min[K, V](t: OrdMap[K, V]): OrdMap[K, V] {
  match (t) {
    Tip => {
      return t;
    },
    Bin(l, k, v, r, s) => {
      match (l) {
        Tip => {
          return r;
        },
        Bin(ll, lk, lv, lr, ls) => {
          let nl: OrdMap[K, V] = node_delete_min(l);
          return node_balance(nl, k, v, r);
        }
      }
    }
  }
}

function node_delete_max[K, V](t: OrdMap[K, V]): OrdMap[K, V] {
  match (t) {
    Tip => {
      return t;
    },
    Bin(l, k, v, r, s) => {
      match (r) {
        Tip => {
          return l;
        },
        Bin(rl, rk, rv, rr, rs) => {
          let nr: OrdMap[K, V] = node_delete_max(r);
          return node_balance(l, k, v, nr);
        }
      }
    }
  }
}

function node_glue[K, V](l: OrdMap[K, V], r: OrdMap[K, V]): OrdMap[K, V] {
  match (l) {
    Tip => {
      return r;
    },
    Bin(ll, lk, lv, lr, ls) => {
      match (r) {
        Tip => {
          return l;
        },
        Bin(rl, rk, rv, rr, rs) => {
          if (ls > rs) {
            match (node_max_node(l)) {
              Tip => {
                return r;
              },
              Bin(ml, mk, mv, mr, ms) => {
                let nl: OrdMap[K, V] = node_delete_max(l);
                return node_balance(nl, mk, mv, r);
              }
            }
          }
          match (node_min_node(r)) {
            Tip => {
              return l;
            },
            Bin(ml, mk, mv, mr, ms) => {
              let nr: OrdMap[K, V] = node_delete_min(r);
              return node_balance(l, mk, mv, nr);
            }
          }
        }
      }
    }
  }
}
```

### `7 function node_delete[K: cmp.Ord, V](t: OrdMap[K, V], k: K): OrdMap[K, V]`

```fern
<<7 function node_delete[K: cmp.Ord, V](t: OrdMap[K, V], k: K): OrdMap[K, V]>>=
function node_delete[K: cmp.Ord, V](t: OrdMap[K, V], k: K): OrdMap[K, V] {
  match (t) {
    Tip => {
      return t;
    },
    Bin(l, nk, nv, r, s) => {
      let c: i32 = k.cmp(nk);
      if (c < 0) {
        let nl: OrdMap[K, V] = node_delete(l, k);
        return node_balance(nl, nk, nv, r);
      }
      if (c > 0) {
        let nr: OrdMap[K, V] = node_delete(r, k);
        return node_balance(l, nk, nv, nr);
      }
      return node_glue(l, r);
    }
  }
}

function node_insert_min[K, V](k: K, v: V, t: OrdMap[K, V]): OrdMap[K, V] {
  match (t) {
    Tip => {
      return node_single(k, v);
    },
    Bin(l, nk, nv, r, s) => {
      let nl: OrdMap[K, V] = node_insert_min(k, v, l);
      return node_balance(nl, nk, nv, r);
    }
  }
}

function node_insert_max[K, V](k: K, v: V, t: OrdMap[K, V]): OrdMap[K, V] {
  match (t) {
    Tip => {
      return node_single(k, v);
    },
    Bin(l, nk, nv, r, s) => {
      let nr: OrdMap[K, V] = node_insert_max(k, v, r);
      return node_balance(l, nk, nv, nr);
    }
  }
}

function node_join[K, V](l: OrdMap[K, V], k: K, v: V, r: OrdMap[K, V]): OrdMap[K, V] {
  match (l) {
    Tip => {
      return node_insert_min(k, v, r);
    },
    Bin(ll, lk, lv, lr, ls) => {
      match (r) {
        Tip => {
          return node_insert_max(k, v, l);
        },
        Bin(rl, rk, rv, rr, rs) => {
          if (3 * ls < rs) {
            let nl: OrdMap[K, V] = node_join(l, k, v, rl);
            return node_balance(nl, rk, rv, rr);
          }
          if (3 * rs < ls) {
            let nr: OrdMap[K, V] = node_join(lr, k, v, r);
            return node_balance(ll, lk, lv, nr);
          }
          return Bin(l, k, v, r, ls + rs + 1);
        }
      }
    }
  }
}

function node_merge[K, V](l: OrdMap[K, V], r: OrdMap[K, V]): OrdMap[K, V] {
  match (l) {
    Tip => {
      return r;
    },
    Bin(ll, lk, lv, lr, ls) => {
      match (r) {
        Tip => {
          return l;
        },
        Bin(rl, rk, rv, rr, rs) => {
          if (3 * ls < rs) {
            let nl: OrdMap[K, V] = node_merge(l, rl);
            return node_balance(nl, rk, rv, rr);
          }
          if (3 * rs < ls) {
            let nr: OrdMap[K, V] = node_merge(lr, r);
            return node_balance(ll, lk, lv, nr);
          }
          return node_glue(l, r);
        }
      }
    }
  }
}
```

### `8 struct OrdSplit[K, V]`

```fern
<<8 struct OrdSplit[K, V]>>=
struct OrdSplit[K, V] { lt: OrdMap[K, V], hit: OrdMap[K, V], gt: OrdMap[K, V] }
```

### `9 function node_split[K: cmp.Ord, V](t: OrdMap[K, V], k: K): OrdSplit[K, V]`

```fern
<<9 function node_split[K: cmp.Ord, V](t: OrdMap[K, V], k: K): OrdSplit[K, V]>>=
function node_split[K: cmp.Ord, V](t: OrdMap[K, V], k: K): OrdSplit[K, V] {
  match (t) {
    Tip => {
      return OrdSplit { lt: t, hit: t, gt: t };
    },
    Bin(l, nk, nv, r, s) => {
      let c: i32 = k.cmp(nk);
      if (c < 0) {
        let sp: OrdSplit[K, V] = node_split(l, k);
        let gt: OrdMap[K, V] = node_join(sp.gt, nk, nv, r);
        return OrdSplit { lt: sp.lt, hit: sp.hit, gt: gt };
      }
      if (c > 0) {
        let sp: OrdSplit[K, V] = node_split(r, k);
        let lt: OrdMap[K, V] = node_join(l, nk, nv, sp.lt);
        return OrdSplit { lt: lt, hit: sp.hit, gt: sp.gt };
      }
      return OrdSplit { lt: l, hit: t, gt: r };
    }
  }
}

function node_union[K: cmp.Ord, V](a: OrdMap[K, V], b: OrdMap[K, V]): OrdMap[K, V] {
  match (a) {
    Tip => {
      return b;
    },
    Bin(al, ak, av, ar, asz) => {
      match (b) {
        Tip => {
          return a;
        },
        Bin(bl, bk, bv, br, bsz) => {
          let sp: OrdSplit[K, V] = node_split(b, ak);
          let l: OrdMap[K, V] = node_union(al, sp.lt);
          let r: OrdMap[K, V] = node_union(ar, sp.gt);
          return node_join(l, ak, av, r);
        }
      }
    }
  }
}

function node_intersection[K: cmp.Ord, V](a: OrdMap[K, V], b: OrdMap[K, V]): OrdMap[K, V] {
  match (a) {
    Tip => {
      return a;
    },
    Bin(al, ak, av, ar, asz) => {
      match (b) {
        Tip => {
          return b;
        },
        Bin(bl, bk, bv, br, bsz) => {
          let sp: OrdSplit[K, V] = node_split(b, ak);
          let l: OrdMap[K, V] = node_intersection(al, sp.lt);
          let r: OrdMap[K, V] = node_intersection(ar, sp.gt);
          match (sp.hit) {
            Tip => {
              return node_merge(l, r);
            },
            Bin(hl, hk, hv, hr, hs) => {
              return node_join(l, ak, av, r);
            }
          }
        }
      }
    }
  }
}

function node_difference[K: cmp.Ord, V](a: OrdMap[K, V], b: OrdMap[K, V]): OrdMap[K, V] {
  match (a) {
    Tip => {
      return a;
    },
    Bin(al, ak, av, ar, asz) => {
      match (b) {
        Tip => {
          return a;
        },
        Bin(bl, bk, bv, br, bsz) => {
          let sp: OrdSplit[K, V] = node_split(a, bk);
          let l: OrdMap[K, V] = node_difference(sp.lt, bl);
          let r: OrdMap[K, V] = node_difference(sp.gt, br);
          return node_merge(l, r);
        }
      }
    }
  }
}

function node_keys_into[K, V](t: OrdMap[K, V], own acc: K[]): K[] {
  match (t) {
    Tip => {
      return acc;
    },
    Bin(l, k, v, r, s) => {
      acc = node_keys_into(l, acc);
      acc = acc.append(k);
      acc = node_keys_into(r, acc);
      return acc;
    }
  }
}

function node_values_into[K, V](t: OrdMap[K, V], own acc: V[]): V[] {
  match (t) {
    Tip => {
      return acc;
    },
    Bin(l, k, v, r, s) => {
      acc = node_values_into(l, acc);
      acc = acc.append(v);
      acc = node_values_into(r, acc);
      return acc;
    }
  }
}

function node_entries_into[K, V](t: OrdMap[K, V], own acc: (K, V)[]): (K, V)[] {
  match (t) {
    Tip => {
      return acc;
    },
    Bin(l, k, v, r, s) => {
      acc = node_entries_into(l, acc);
      acc = acc.append((k, v));
      acc = node_entries_into(r, acc);
      return acc;
    }
  }
}
```

### `10 function node_fold[K, V, A](t: OrdMap[K, V], init: A, f: (A, K, V)`

```fern
<<10 function node_fold[K, V, A](t: OrdMap[K, V], init: A, f: (A, K, V)>>=
function node_fold[K, V, A](t: OrdMap[K, V], init: A, f: (A, K, V) => A): A {
  match (t) {
    Tip => {
      return init;
    },
    Bin(l, k, v, r, s) => {
      let a: A = node_fold(l, init, f);
      a = f(a, k, v);
      return node_fold(r, a, f);
    }
  }
}

function node_for_each[K, V](t: OrdMap[K, V], f: (K, V) => void): void {
  match (t) {
    Tip => {
      return;
    },
    Bin(l, k, v, r, s) => {
      node_for_each(l, f);
      f(k, v);
      node_for_each(r, f);
    }
  }
}

function node_map_values[K, V, W](t: OrdMap[K, V], f: (V) => W): OrdMap[K, W] {
  match (t) {
    Tip => {
      let e: OrdMap[K, W] = Tip;
      return e;
    },
    Bin(l, k, v, r, s) => {
      let nl: OrdMap[K, W] = node_map_values(l, f);
      let nr: OrdMap[K, W] = node_map_values(r, f);
      return Bin(nl, k, f(v), nr, s);
    }
  }
}

function node_filter[K, V](t: OrdMap[K, V], pred: (K, V) => boolean): OrdMap[K, V] {
  match (t) {
    Tip => {
      return t;
    },
    Bin(l, k, v, r, s) => {
      let fl: OrdMap[K, V] = node_filter(l, pred);
      let fr: OrdMap[K, V] = node_filter(r, pred);
      if (pred(k, v)) {
        return node_join(fl, k, v, fr);
      }
      return node_merge(fl, fr);
    }
  }
}

function node_at[K, V](t: OrdMap[K, V], i: i32): OrdMap[K, V] {
  let cur: OrdMap[K, V] = t;
  let want: i32 = i;
  if (want < 0) {
    let e: OrdMap[K, V] = Tip;
    return e;
  }
  let walking: boolean = true;
  while (walking) {
    match (cur) {
      Tip => {
        walking = false;
      },
      Bin(l, k, v, r, s) => {
        let sl: i32 = node_size(l);
        if (want < sl) {
          cur = l;
        } else if (want == sl) {
          return cur;
        } else {
          want = want - sl - 1;
          cur = r;
        }
      }
    }
  }
  return cur;
}

function node_index_of[K: cmp.Ord, V](t: OrdMap[K, V], k: K): i32 {
  let cur: OrdMap[K, V] = t;
  let below: i32 = 0;
  let walking: boolean = true;
  while (walking) {
    match (cur) {
      Tip => {
        walking = false;
      },
      Bin(l, nk, nv, r, s) => {
        let c: i32 = k.cmp(nk);
        if (c == 0) {
          return below + node_size(l);
        }
        if (c < 0) {
          cur = l;
        } else {
          below = below + node_size(l) + 1;
          cur = r;
        }
      }
    }
  }
  return 0 - 1;
}

function node_ordered[K: cmp.Ord, V](l: OrdMap[K, V], k: K, r: OrdMap[K, V]): boolean {
  match (node_max_node(l)) {
    Tip => {},
    Bin(ml, mk, mv, mr, ms) => {
      if (mk.cmp(k) >= 0) {
        return false;
      }
    }
  }
  match (node_min_node(r)) {
    Tip => {},
    Bin(ml, mk, mv, mr, ms) => {
      if (mk.cmp(k) <= 0) {
        return false;
      }
    }
  }
  return true;
}
```

### `11 function node_valid[K: cmp.Ord, V](t: OrdMap[K, V]): boolean`

```fern
<<11 function node_valid[K: cmp.Ord, V](t: OrdMap[K, V]): boolean>>=
function node_valid[K: cmp.Ord, V](t: OrdMap[K, V]): boolean {
  match (t) {
    Tip => {
      return true;
    },
    Bin(l, k, v, r, s) => {
      let sl: i32 = node_size(l);
      let sr: i32 = node_size(r);
      if (s != sl + sr + 1) {
        return false;
      }
      if (sl + sr > 1 && (sr > 3 * sl || sl > 3 * sr)) {
        return false;
      }
      if (!node_ordered(l, k, r)) {
        return false;
      }
      return node_valid(l) && node_valid(r);
    }
  }
}
```

### `12 pub function (m: OrdMap[K, V]) len(): i32`

```fern
<<12 pub function (m: OrdMap[K, V]) len(): i32>>=
pub function (m: OrdMap[K, V]) len(): i32 {
  return node_size(m);
}

pub function (m: OrdMap[K, V]) is_empty(): boolean {
  return node_size(m) == 0;
}

pub function (m: OrdMap[K, V]) insert[K: cmp.Ord, V](k: K, v: V): OrdMap[K, V] {
  return node_insert(m, k, v);
}

pub function (m: OrdMap[K, V]) remove[K: cmp.Ord, V](k: K): OrdMap[K, V] {
  return node_delete(m, k);
}

pub function (m: OrdMap[K, V]) get[K: cmp.Ord, V](k: K): Option[V] {
  match (node_find(m, k)) {
    Tip => {
      return None;
    },
    Bin(l, nk, nv, r, s) => {
      return Some(nv);
    }
  }
}

pub function (m: OrdMap[K, V]) get_or[K: cmp.Ord, V](k: K, fallback: V): V {
  match (node_find(m, k)) {
    Tip => {
      return fallback;
    },
    Bin(l, nk, nv, r, s) => {
      return nv;
    }
  }
}

pub function (m: OrdMap[K, V]) contains[K: cmp.Ord, V](k: K): boolean {
  match (node_find(m, k)) {
    Tip => {
      return false;
    },
    Bin(l, nk, nv, r, s) => {
      return true;
    }
  }
}

pub function (m: OrdMap[K, V]) update[K: cmp.Ord, V](k: K, init: V, f: (V) => V): OrdMap[K, V] {
  match (node_find(m, k)) {
    Tip => {
      return node_insert(m, k, f(init));
    },
    Bin(l, nk, nv, r, s) => {
      return node_insert(m, k, f(nv));
    }
  }
}

pub function (m: OrdMap[K, V]) min_key(): Option[K] {
  match (node_min_node(m)) {
    Tip => {
      return None;
    },
    Bin(l, k, v, r, s) => {
      return Some(k);
    }
  }
}

pub function (m: OrdMap[K, V]) max_key(): Option[K] {
  match (node_max_node(m)) {
    Tip => {
      return None;
    },
    Bin(l, k, v, r, s) => {
      return Some(k);
    }
  }
}

pub function (m: OrdMap[K, V]) remove_min(): OrdMap[K, V] {
  return node_delete_min(m);
}

pub function (m: OrdMap[K, V]) remove_max(): OrdMap[K, V] {
  return node_delete_max(m);
}

pub function (m: OrdMap[K, V]) key_at(i: i32): Option[K] {
  match (node_at(m, i)) {
    Tip => {
      return None;
    },
    Bin(l, k, v, r, s) => {
      return Some(k);
    }
  }
}

pub function (m: OrdMap[K, V]) value_at(i: i32): Option[V] {
  match (node_at(m, i)) {
    Tip => {
      return None;
    },
    Bin(l, k, v, r, s) => {
      return Some(v);
    }
  }
}

pub function (m: OrdMap[K, V]) index_of[K: cmp.Ord, V](k: K): Option[i32] {
  let i: i32 = node_index_of(m, k);
  if (i < 0) {
    return None;
  }
  return Some(i);
}

pub function (m: OrdMap[K, V]) keys(): K[] {
  let acc: K[] = [];
  acc = node_keys_into(m, acc);
  return acc;
}

pub function (m: OrdMap[K, V]) values(): V[] {
  let acc: V[] = [];
  acc = node_values_into(m, acc);
  return acc;
}
```

### `13 pub function (m: OrdMap[K, V]) iter(): iter.ArrayIter[(K, V)]`

```fern
<<13 pub function (m: OrdMap[K, V]) iter(): iter.ArrayIter[(K, V)]>>=
pub function (m: OrdMap[K, V]) iter(): iter.ArrayIter[(K, V)] {
  let acc: (K, V)[] = [];
  acc = node_entries_into(m, acc);
  return iter.of(acc);
}

pub function (m: OrdMap[K, V]) fold[K, V, A](init: A, f: (A, K, V) => A): A {
  return node_fold(m, init, f);
}

pub function (m: OrdMap[K, V]) for_each(f: (K, V) => void): void {
  node_for_each(m, f);
}

pub function (m: OrdMap[K, V]) map_values[K, V, W](f: (V) => W): OrdMap[K, W] {
  return node_map_values(m, f);
}

pub function (m: OrdMap[K, V]) filter(pred: (K, V) => boolean): OrdMap[K, V] {
  return node_filter(m, pred);
}

pub function (m: OrdMap[K, V]) merge[K: cmp.Ord, V](other: OrdMap[K, V]): OrdMap[K, V] {
  return node_union(other, m);
}

pub function (m: OrdMap[K, V]) entries(): (K, V)[] {
  let out: (K, V)[] = [];
  let ks: K[] = m.keys();
  let vs: V[] = m.values();
  let i: i32 = 0;
  while (i < ks.len()) {
    out = out.append((ks[i], vs[i]));
    i = i + 1;
  }
  return out;
}

pub function (m: OrdMap[K, V]) intersection[K: cmp.Ord, V](other: OrdMap[K, V]): OrdMap[K, V] {
  return node_intersection(m, other);
}

pub function (m: OrdMap[K, V]) difference[K: cmp.Ord, V](other: OrdMap[K, V]): OrdMap[K, V] {
  return node_difference(m, other);
}

pub function (m: OrdMap[K, V]) split_lt[K: cmp.Ord, V](k: K): OrdMap[K, V] {
  let sp: OrdSplit[K, V] = node_split(m, k);
  return sp.lt;
}

pub function (m: OrdMap[K, V]) split_gt[K: cmp.Ord, V](k: K): OrdMap[K, V] {
  let sp: OrdSplit[K, V] = node_split(m, k);
  return sp.gt;
}

pub function (m: OrdMap[K, V]) is_valid[K: cmp.Ord, V](): boolean {
  return node_valid(m);
}
```
