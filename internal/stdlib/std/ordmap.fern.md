# Persistent ordered maps

This module provides persistent ordered maps.

## Model

A map is a B+ tree. A leaf holds up to 64 entries in key order. A branch holds up to 64 children, the separators between them, and the size of each child. All leaves have the same depth.

Each node also keeps an 8-byte prefix of each of its keys in a `u64` array, parallel to the keys. A search counts the prefixes below the prefix of the target. Then only the keys whose prefix equals the prefix of the target go to `cmp`. For integer keys the prefix is the whole key, so a search calls `cmp` at most once in each node.

The keys of a node often start with the same bytes, as URLs and paths do. Each node records how many bytes its keys share at their start, and the prefixes start after those bytes. A search first checks that the target starts with the same bytes. A target that does not falls before or after every key of the node.

An update copies the nodes on the path from the root to the changed leaf. The other nodes stay shared with the previous map. When the map holds the only reference to a node, the update writes that node in place.

## Contracts

Key order comes from `cmp.Ord`. Ordered traversal follows that comparison. A missing key produces an optional result.

A key type that overrides `cmp_prefix` or `cmp_shared` must keep the contract that `core/cmp` states for them. A type that keeps the defaults gets a correct map that compares with `cmp` alone.

## Executable example

```fern test name=std-ordmap-example
import "std/ordmap";

function main(): i32 {
  let original = ordmap.new[string, i32]();
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

<<dependencies>>

<<types>>

<<node search>>

<<array helpers>>

<<insertion>>

<<merging neighbours>>

<<removal>>

<<joins and splits>>

<<building from sorted entries>>

<<traversal>>

<<validation>>

<<iterator>>

<<public interface>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module provides persistent ordered maps.
```

### `dependencies`

```fern
<<dependencies>>=
import "core/cmp";
import "core/iter";
```

### `types`

A leaf holds the number of bytes its keys share at their start, their prefixes, its keys and its values. A branch holds the same count and prefixes for its separators. It also holds its separators, its children, the size of each child and its own size. A branch with `n` children holds `n - 1` separators, and separator `i` is the smallest key of child `i + 1`.

The count can be lower than the number of bytes the keys share, because a removal does not raise it. The prefixes always start at the byte the count names.

```fern
<<types>>=
const NODE_MAX: i32 = 64;

const NODE_MIN: i32 = 16;

enum Node[K, V] {
  Leaf(i32, u64[], K[], V[]),
  Branch(i32, u64[], K[], Node[K, V][], i32[], i32),
}

pub struct OrdMap[K, V] { root: Node[K, V] }

function empty_leaf[K, V](): Node[K, V] {
  let ws: u64[] = [];
  let ks: K[] = [];
  let vs: V[] = [];
  return Leaf(0, ws, ks, vs);
}

// The bytes that all keys share, which are the bytes that the first and last keys share.
function shared_of[K: cmp.Ord](ks: K[]): i32 {
  if (ks.len() < 2) {
    return 0;
  }
  return ks[0].cmp_shared(ks[ks.len() - 1]);
}

function words_at[K: cmp.Ord](ks: K[], pre: i32): u64[] {
  let ws: u64[] = [];
  for k in ks {
    ws = ws.append(k.cmp_prefix(pre));
  }
  return ws;
}

function make_leaf[K: cmp.Ord, V](ks: K[], vs: V[]): Node[K, V] {
  let pre: i32 = shared_of(ks);
  return Leaf(pre, words_at(ks, pre), ks, vs);
}

function make_branch[K: cmp.Ord, V](ks: K[], kids: Node[K, V][], sizes: i32[]): Node[K, V] {
  let pre: i32 = shared_of(ks);
  let total: i32 = 0;
  for x in sizes {
    total = total + x;
  }
  return Branch(pre, words_at(ks, pre), ks, kids, sizes, total);
}

function node_size[K, V](n: Node[K, V]): i32 {
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      return ks.len();
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      return total;
    }
  }
}

function node_width[K, V](n: Node[K, V]): i32 {
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      return ks.len();
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      return kids.len();
    }
  }
}
```

### `node search`

`outside` tells whether the target starts with the bytes that the keys of a node share. When it does not, the target falls before or after every key. Otherwise `ties_from` and `ties_to` give the range of keys whose prefix equals the prefix of the target. The keys before that range are below the target and the keys after it are above. `__count_below_u64` counts the prefixes below the target with SIMD compares on each target. When all prefixes in a node are equal, the range is the whole node or nothing.

```fern
<<node search>>=
// 0 when k is below every key, the width when it is above every key, or -1 when k starts with the shared bytes.
function outside[K: cmp.Ord](pre: i32, ks: K[], k: K): i32 {
  if (pre == 0 || ks.len() == 0 || k.cmp_shared(ks[0]) >= pre) {
    return -1;
  }
  if (k.cmp(ks[0]) < 0) {
    return 0;
  }
  return ks.len();
}

function ties_from(ws: u64[], w: u64): i32 {
  let n: i32 = ws.len();
  if (n == 0 || w <= ws[0]) {
    return 0;
  }
  if (w > ws[n - 1]) {
    return n;
  }
  return __count_below_u64(ws, w);
}

function ties_to(ws: u64[], from: i32, w: u64): i32 {
  let i: i32 = from;
  while (i < ws.len() && ws[i] == w) {
    i = i + 1;
  }
  return i;
}

// The first index whose key is not below k.
function lower[K: cmp.Ord](pre: i32, ws: u64[], ks: K[], k: K): i32 {
  let o: i32 = outside(pre, ks, k);
  if (o >= 0) {
    return o;
  }
  let w: u64 = k.cmp_prefix(pre);
  let lo: i32 = ties_from(ws, w);
  let hi: i32 = ties_to(ws, lo, w);
  while (lo < hi) {
    let mid: i32 = lo + (hi - lo) / 2;
    if (ks[mid].cmp(k) < 0) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  return lo;
}

// The first index whose key is above k.
function upper[K: cmp.Ord](pre: i32, ws: u64[], ks: K[], k: K): i32 {
  let o: i32 = outside(pre, ks, k);
  if (o >= 0) {
    return o;
  }
  let w: u64 = k.cmp_prefix(pre);
  let lo: i32 = ties_from(ws, w);
  let hi: i32 = ties_to(ws, lo, w);
  while (lo < hi) {
    let mid: i32 = lo + (hi - lo) / 2;
    if (ks[mid].cmp(k) <= 0) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  return lo;
}

// The index of k in a leaf, or -1.
function leaf_index[K: cmp.Ord](pre: i32, ws: u64[], ks: K[], k: K): i32 {
  let i: i32 = lower(pre, ws, ks, k);
  if (i < ks.len() && ks[i].cmp(k) == 0) {
    return i;
  }
  return -1;
}

// The leaf that holds k when the map has it, and the index of k in that leaf.
function find_leaf[K: cmp.Ord, V](root: Node[K, V], k: K): (Node[K, V], i32) {
  let cur: Node[K, V] = root;
  while (true) {
    match (cur) {
      Leaf(pre, ws, ks, vs) => {
        return (cur, leaf_index(pre, ws, ks, k));
      },
      Branch(pre, ws, ks, kids, sizes, total) => {
        cur = kids[upper(pre, ws, ks, k)];
      }
    }
  }
  return (cur, -1);
}
```

### `array helpers`

`put_at` writes in place when the caller holds the only reference to the array.

```fern
<<array helpers>>=
function put_at[T](xs: T[], i: i32, x: T): T[] {
  let n: i32 = xs.len();
  if (n == i) {
    return xs.append(x);
  }
  xs = xs.append(xs[n - 1]);
  let j: i32 = n - 1;
  while (j > i) {
    xs = xs.with(j, xs[j - 1]);
    j = j - 1;
  }
  return xs.with(i, x);
}

function cut_at[T](xs: T[], i: i32): T[] {
  let out: T[] = [];
  let j: i32 = 0;
  while (j < xs.len()) {
    if (j != i) {
      out = out.append(xs[j]);
    }
    j = j + 1;
  }
  return out;
}

function span[T](xs: T[], from: i32, to: i32): T[] {
  let out: T[] = [];
  let j: i32 = from;
  while (j < to) {
    out = out.append(xs[j]);
    j = j + 1;
  }
  return out;
}

function joined[T](a: T[], b: T[]): T[] {
  for x in b {
    a = a.append(x);
  }
  return a;
}
```

### `insertion`

An insertion gives back the changed node, and a caller sees from its size whether the key was new. A node can come back one entry wider than `NODE_MAX`, and the caller splits it at its middle. Each half gets the count of bytes its own keys share.

A key that goes to either end of a node can share fewer bytes with the other keys. Then the node gets the new count and new prefixes. A key that goes between two keys shares their bytes.

```fern
<<insertion>>=
struct Halves[K, V] { left: Node[K, V], sep: K, right: Node[K, V] }

// The two halves of a node that is too wide. The first key of the right half separates them.
function halves[K: cmp.Ord, V](n: Node[K, V]): Halves[K, V] {
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      let h: i32 = ks.len() / 2;
      let w: i32 = ks.len();
      return Halves { left: make_leaf(span(ks, 0, h), span(vs, 0, h)), sep: ks[h], right: make_leaf(span(ks, h, w), span(vs, h, w)) };
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      let h: i32 = kids.len() / 2;
      let w: i32 = kids.len();
      let left: Node[K, V] = make_branch(span(ks, 0, h - 1), span(kids, 0, h), span(sizes, 0, h));
      let right: Node[K, V] = make_branch(span(ks, h, w - 1), span(kids, h, w), span(sizes, h, w));
      return Halves { left: left, sep: ks[h - 1], right: right };
    }
  }
}

// The shared count after key i of ks is new: lower when the key is at an end and shares fewer bytes.
function refit[K: cmp.Ord](pre: i32, ks: K[], i: i32): i32 {
  if (pre > 0 && (i == 0 || i == ks.len() - 1)) {
    let s: i32 = shared_of(ks);
    if (s < pre) {
      return s;
    }
  }
  return pre;
}

// ws with the prefix of key i of ks at pre, or every prefix again when pre is not old.
function fitted_words[K: cmp.Ord](old: i32, pre: i32, ws: u64[], ks: K[], i: i32): u64[] {
  if (pre != old) {
    return words_at(ks, pre);
  }
  return put_at(ws, i, ks[i].cmp_prefix(pre));
}

// A node too wide to keep needs no prefixes: halves makes them for each half.
function leaf_insert[K: cmp.Ord, V](pre: i32, ws: u64[], ks: K[], vs: V[], k: K, v: V): Node[K, V] {
  let i: i32 = lower(pre, ws, ks, k);
  if (i < ks.len() && ks[i].cmp(k) == 0) {
    return Leaf(pre, ws, ks.with(i, k), vs.with(i, v));
  }
  ks = put_at(ks, i, k);
  vs = put_at(vs, i, v);
  if (ks.len() > NODE_MAX) {
    return Leaf(pre, ws, ks, vs);
  }
  let np: i32 = refit(pre, ks, i);
  return Leaf(np, fitted_words(pre, np, ws, ks, i), ks, vs);
}

// Child i is now c. A child too wide to keep becomes two children with a separator between them.
function branch_put[K: cmp.Ord, V](pre: i32, ws: u64[], ks: K[], kids: Node[K, V][], sizes: i32[], total: i32, i: i32, c: Node[K, V]): Node[K, V] {
  let grow: i32 = node_size(c) - sizes[i];
  if (node_width(c) <= NODE_MAX) {
    return Branch(pre, ws, ks, kids.with(i, c), sizes.with(i, sizes[i] + grow), total + grow);
  }
  let h: Halves[K, V] = halves(c);
  sizes = sizes.with(i, node_size(h.left));
  sizes = put_at(sizes, i + 1, node_size(h.right));
  kids = kids.with(i, h.left);
  kids = put_at(kids, i + 1, h.right);
  ks = put_at(ks, i, h.sep);
  if (kids.len() > NODE_MAX) {
    return Branch(pre, ws, ks, kids, sizes, total + grow);
  }
  let np: i32 = refit(pre, ks, i);
  return Branch(np, fitted_words(pre, np, ws, ks, i), ks, kids, sizes, total + grow);
}

function node_insert[K: cmp.Ord, V](n: Node[K, V], k: K, v: V): Node[K, V] {
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      return leaf_insert(pre, ws, ks, vs, k, v);
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      let i: i32 = upper(pre, ws, ks, k);
      return branch_put(pre, ws, ks, kids, sizes, total, i, node_insert(kids[i], k, v));
    }
  }
}

// The root of a tree whose top node may be too wide to keep.
function grown_root[K: cmp.Ord, V](n: Node[K, V]): Node[K, V] {
  if (node_width(n) <= NODE_MAX) {
    return n;
  }
  let h: Halves[K, V] = halves(n);
  return make_branch([h.sep], [h.left, h.right], [node_size(h.left), node_size(h.right)]);
}
```

### `merging neighbours`

`merge_nodes` puts two neighbours of the same height into one node, which can be too wide to keep. `sep` is the separator between the two nodes. Removal and `join` both use it.

```fern
<<merging neighbours>>=
function merge_nodes[K: cmp.Ord, V](a: Node[K, V], sep: K, b: Node[K, V]): Node[K, V] {
  match (a) {
    Leaf(apre, aws, aks, avs) => {
      match (b) {
        Leaf(bpre, bws, bks, bvs) => {
          return make_leaf(joined(aks, bks), joined(avs, bvs));
        },
        Branch(bpre, bws, bks, bkids, bsizes, btotal) => {
          return a;
        }
      }
    },
    Branch(apre, aws, aks, akids, asizes, atotal) => {
      match (b) {
        Leaf(bpre, bws, bks, bvs) => {
          return a;
        },
        Branch(bpre, bws, bks, bkids, bsizes, btotal) => {
          return make_branch(joined(aks.append(sep), bks), joined(akids, bkids), joined(asizes, bsizes));
        }
      }
    }
  }
}

// Children i and i + 1 merged into c.
function branch_merged[K: cmp.Ord, V](ws: u64[], ks: K[], kids: Node[K, V][], sizes: i32[], i: i32, c: Node[K, V], pre: i32): Node[K, V] {
  if (node_width(c) <= NODE_MAX) {
    let nk: Node[K, V][] = cut_at(kids, i + 1).with(i, c);
    let ns: i32[] = cut_at(sizes, i + 1).with(i, node_size(c));
    let total: i32 = 0;
    for x in ns {
      total = total + x;
    }
    return Branch(pre, cut_at(ws, i), cut_at(ks, i), nk, ns, total);
  }
  let h: Halves[K, V] = halves(c);
  let nk: Node[K, V][] = kids.with(i, h.left).with(i + 1, h.right);
  let ns: i32[] = sizes.with(i, node_size(h.left)).with(i + 1, node_size(h.right));
  return make_branch(ks.with(i, h.sep), nk, ns);
}
```

### `removal`

A removal gives back the changed node, and a caller sees from its size whether the key was there. A removal that leaves a child narrower than `NODE_MIN` merges the child with a neighbour. A root branch with one child gives its place to that child. A removal keeps the shared count of a node, because the keys that stay share at least those bytes.

```fern
<<removal>>=
function node_remove[K: cmp.Ord, V](n: Node[K, V], k: K): Node[K, V] {
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      let i: i32 = leaf_index(pre, ws, ks, k);
      if (i < 0) {
        return Leaf(pre, ws, ks, vs);
      }
      return Leaf(pre, cut_at(ws, i), cut_at(ks, i), cut_at(vs, i));
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      let i: i32 = upper(pre, ws, ks, k);
      let c: Node[K, V] = node_remove(kids[i], k);
      let left: i32 = node_size(c);
      if (left == sizes[i]) {
        return Branch(pre, ws, ks, kids.with(i, c), sizes, total);
      }
      if (node_width(c) >= NODE_MIN || kids.len() < 2) {
        return Branch(pre, ws, ks, kids.with(i, c), sizes.with(i, left), total - 1);
      }
      if (i + 1 < kids.len()) {
        return branch_merged(ws, ks, kids, sizes, i, merge_nodes(c, ks[i], kids[i + 1]), pre);
      }
      return branch_merged(ws, ks, kids, sizes, i - 1, merge_nodes(kids[i - 1], ks[i - 1], c), pre);
    }
  }
}

function shrunk_root[K, V](n: Node[K, V]): Node[K, V] {
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      return n;
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      if (kids.len() == 1) {
        return kids[0];
      }
      return n;
    }
  }
}
```

### `joins and splits`

A tree in this section is a node and its height, where a leaf has height 0. Its root can be narrower than `NODE_MIN`, and every other node keeps the width rules. `join` puts two trees together when every key of the left tree is below every key of the right tree. It goes down the edge of the higher tree to the height of the lower tree, and it merges the two there. Thus its cost increases with the difference in height.

`split_side` cuts a tree at a key. At each branch on the path to the key, the children before the path make one tree. The children after the path make a second tree. `join` puts these trees together.

```fern
<<joins and splits>>=
struct Tree[K, V] { root: Node[K, V], height: i32 }

// A tree whose top node may be too wide to keep.
function tree_of[K: cmp.Ord, V](n: Node[K, V], height: i32): Tree[K, V] {
  if (node_width(n) <= NODE_MAX) {
    return Tree { root: n, height: height };
  }
  return Tree { root: grown_root(n), height: height + 1 };
}

function node_height[K, V](n: Node[K, V]): i32 {
  let h: i32 = 0;
  let cur: Node[K, V] = n;
  while (true) {
    match (cur) {
      Leaf(pre, ws, ks, vs) => {
        return h;
      },
      Branch(pre, ws, ks, kids, sizes, total) => {
        h = h + 1;
        cur = kids[0];
      }
    }
  }
  return h;
}

// The smallest key of a nonempty node.
function first_key[K, V](n: Node[K, V]): K {
  let cur: Node[K, V] = n;
  while (true) {
    match (cur) {
      Leaf(pre, ws, ks, vs) => {
        return ks[0];
      },
      Branch(pre, ws, ks, kids, sizes, total) => {
        cur = kids[0];
      }
    }
  }
  return first_key(cur);
}

// Join r, of height hr, below the last child of l, of height hl > hr.
function join_right[K: cmp.Ord, V](l: Node[K, V], hl: i32, r: Node[K, V], hr: i32): Node[K, V] {
  match (l) {
    Leaf(pre, ws, ks, vs) => {
      return l;
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      let i: i32 = kids.len() - 1;
      if (hl - 1 == hr) {
        return branch_put(pre, ws, ks, kids, sizes, total, i, merge_nodes(kids[i], first_key(r), r));
      }
      return branch_put(pre, ws, ks, kids, sizes, total, i, join_right(kids[i], hl - 1, r, hr));
    }
  }
}

// Join l, of height hl, below the first child of r, of height hr > hl.
function join_left[K: cmp.Ord, V](l: Node[K, V], hl: i32, r: Node[K, V], hr: i32): Node[K, V] {
  match (r) {
    Leaf(pre, ws, ks, vs) => {
      return r;
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      if (hr - 1 == hl) {
        return branch_put(pre, ws, ks, kids, sizes, total, 0, merge_nodes(l, first_key(kids[0]), kids[0]));
      }
      return branch_put(pre, ws, ks, kids, sizes, total, 0, join_left(l, hl, kids[0], hr - 1));
    }
  }
}

function join[K: cmp.Ord, V](l: Tree[K, V], r: Tree[K, V]): Tree[K, V] {
  if (node_size(l.root) == 0) {
    return r;
  }
  if (node_size(r.root) == 0) {
    return l;
  }
  if (l.height == r.height) {
    return tree_of(merge_nodes(l.root, first_key(r.root), r.root), l.height);
  }
  if (l.height > r.height) {
    return tree_of(join_right(l.root, l.height, r.root, r.height), l.height);
  }
  return tree_of(join_left(l.root, l.height, r.root, r.height), r.height);
}

// Children from..to of a branch of the given height, as a tree.
function children_tree[K: cmp.Ord, V](ks: K[], kids: Node[K, V][], sizes: i32[], from: i32, to: i32, height: i32): Tree[K, V] {
  if (to - from == 0) {
    return Tree { root: empty_leaf(), height: 0 };
  }
  if (to - from == 1) {
    return Tree { root: kids[from], height: height - 1 };
  }
  return Tree { root: make_branch(span(ks, from, to - 1), span(kids, from, to), span(sizes, from, to)), height: height };
}

// The entries below k (above k when above is true).
function split_side[K: cmp.Ord, V](n: Node[K, V], height: i32, k: K, above: boolean): Tree[K, V] {
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      if (above) {
        let i: i32 = upper(pre, ws, ks, k);
        return Tree { root: make_leaf(span(ks, i, ks.len()), span(vs, i, ks.len())), height: 0 };
      }
      let j: i32 = lower(pre, ws, ks, k);
      return Tree { root: make_leaf(span(ks, 0, j), span(vs, 0, j)), height: 0 };
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      let i: i32 = upper(pre, ws, ks, k);
      let part: Tree[K, V] = split_side(kids[i], height - 1, k, above);
      if (above) {
        return join(part, children_tree(ks, kids, sizes, i + 1, kids.len(), height));
      }
      return join(children_tree(ks, kids, sizes, 0, i, height), part);
    }
  }
}
```

### `building from sorted entries`

`build` makes a tree from entries whose keys go up in strict order. It fills each node to three quarters of `NODE_MAX`, so that the next insertions do not split at once. It divides the entries equally between the nodes, so that no node is narrower than `NODE_MIN`.

```fern
<<building from sorted entries>>=
// The number of nodes for n items.
function node_count(n: i32): i32 {
  let fill: i32 = NODE_MAX * 3 / 4;
  return (n + fill - 1) / fill;
}

function build[K: cmp.Ord, V](ks: K[], vs: V[]): Node[K, V] {
  let n: i32 = ks.len();
  let parts: i32 = node_count(n);
  if (parts <= 1) {
    return make_leaf(ks, vs);
  }
  let level: Node[K, V][] = [];
  let firsts: K[] = [];
  let p: i32 = 0;
  while (p < parts) {
    let from: i32 = n * p / parts;
    let to: i32 = n * (p + 1) / parts;
    level = level.append(make_leaf(span(ks, from, to), span(vs, from, to)));
    firsts = firsts.append(ks[from]);
    p = p + 1;
  }
  return build_up(level, firsts);
}

// Branches over one level of nodes, until one node is left.
function build_up[K: cmp.Ord, V](level: Node[K, V][], firsts: K[]): Node[K, V] {
  while (level.len() > 1) {
    let n: i32 = level.len();
    let parts: i32 = node_count(n);
    let up: Node[K, V][] = [];
    let ufirsts: K[] = [];
    let p: i32 = 0;
    while (p < parts) {
      let from: i32 = n * p / parts;
      let to: i32 = n * (p + 1) / parts;
      let ks: K[] = [];
      let kids: Node[K, V][] = [];
      let sizes: i32[] = [];
      let j: i32 = from;
      while (j < to) {
        if (j > from) {
          ks = ks.append(firsts[j]);
        }
        kids = kids.append(level[j]);
        sizes = sizes.append(node_size(level[j]));
        j = j + 1;
      }
      up = up.append(make_branch(ks, kids, sizes));
      ufirsts = ufirsts.append(firsts[from]);
      p = p + 1;
    }
    level = up;
    firsts = ufirsts;
  }
  return level[0];
}

function of_entries[K: cmp.Ord, V](ks: K[], vs: V[]): OrdMap[K, V] {
  return OrdMap { root: build(ks, vs) };
}
```

### `traversal`

```fern
<<traversal>>=
function node_fold[K, V, A](n: Node[K, V], init: A, f: (A, K, V) => A): A {
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      let a: A = init;
      let i: i32 = 0;
      while (i < ks.len()) {
        a = f(a, ks[i], vs[i]);
        i = i + 1;
      }
      return a;
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      let a: A = init;
      for c in kids {
        a = node_fold(c, a, f);
      }
      return a;
    }
  }
}

function node_for_each[K, V](n: Node[K, V], f: (K, V) => void): void {
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      let i: i32 = 0;
      while (i < ks.len()) {
        f(ks[i], vs[i]);
        i = i + 1;
      }
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      for c in kids {
        node_for_each(c, f);
      }
    }
  }
}

function node_keys_into[K, V](n: Node[K, V], acc: K[]): K[] {
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      return joined(acc, ks);
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      for c in kids {
        acc = node_keys_into(c, acc);
      }
      return acc;
    }
  }
}

function node_values_into[K, V](n: Node[K, V], acc: V[]): V[] {
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      return joined(acc, vs);
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      for c in kids {
        acc = node_values_into(c, acc);
      }
      return acc;
    }
  }
}

function node_map_values[K, V, W](n: Node[K, V], f: (V) => W): Node[K, W] {
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      let out: W[] = [];
      for v in vs {
        out = out.append(f(v));
      }
      return Leaf(pre, ws, ks, out);
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      let out: Node[K, W][] = [];
      for c in kids {
        out = out.append(node_map_values(c, f));
      }
      return Branch(pre, ws, ks, out, sizes, total);
    }
  }
}

// The leaf that holds entry i, and the index of the entry in it.
function node_at[K, V](n: Node[K, V], i: i32): (Node[K, V], i32) {
  let cur: Node[K, V] = n;
  let want: i32 = i;
  while (true) {
    match (cur) {
      Leaf(pre, ws, ks, vs) => {
        return (cur, want);
      },
      Branch(pre, ws, ks, kids, sizes, total) => {
        let j: i32 = 0;
        while (want >= sizes[j]) {
          want = want - sizes[j];
          j = j + 1;
        }
        cur = kids[j];
      }
    }
  }
  return (cur, want);
}

function node_rank[K: cmp.Ord, V](n: Node[K, V], k: K): i32 {
  let cur: Node[K, V] = n;
  let below: i32 = 0;
  while (true) {
    match (cur) {
      Leaf(pre, ws, ks, vs) => {
        let i: i32 = leaf_index(pre, ws, ks, k);
        if (i < 0) {
          return -1;
        }
        return below + i;
      },
      Branch(pre, ws, ks, kids, sizes, total) => {
        let j: i32 = upper(pre, ws, ks, k);
        let c: i32 = 0;
        while (c < j) {
          below = below + sizes[c];
          c = c + 1;
        }
        cur = kids[j];
      }
    }
  }
  return -1;
}

// The leaf at the end of the tree: the last leaf when last is true, else the first.
function edge_leaf[K, V](n: Node[K, V], last: boolean): Node[K, V] {
  let cur: Node[K, V] = n;
  while (true) {
    match (cur) {
      Leaf(pre, ws, ks, vs) => {
        return cur;
      },
      Branch(pre, ws, ks, kids, sizes, total) => {
        if (last) {
          cur = kids[kids.len() - 1];
        } else {
          cur = kids[0];
        }
      }
    }
  }
  return cur;
}
```

### `validation`

`node_valid` checks every rule of the tree. It checks the order of keys and the bounds that separators put on their children. It also checks the shared count, the stored prefixes and sizes, the width of each node, and that all leaves have the same depth. `lo` and `hi` are the bounds from the separators above the node, when they exist.

```fern
<<validation>>=
// The keys are in order, and each prefix is the prefix of its key after the shared bytes.
function prefixes_valid[K: cmp.Ord](pre: i32, ws: u64[], ks: K[]): boolean {
  if (ws.len() != ks.len() || pre < 0 || ks.len() > 0 && pre > ks[0].cmp_shared(ks[ks.len() - 1])) {
    return false;
  }
  let i: i32 = 0;
  while (i < ks.len()) {
    if (ws[i] != ks[i].cmp_prefix(pre) || i > 0 && ks[i - 1].cmp(ks[i]) >= 0) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

// No key is below lo, and every key is below hi.
function bounds_valid[K: cmp.Ord](ks: K[], lo: Option[K], hi: Option[K]): boolean {
  if (ks.len() == 0) {
    return true;
  }
  let above: boolean = true;
  match (lo) {
    Some(b) => {
      above = ks[0].cmp(b) >= 0;
    },
    None => {}
  }
  match (hi) {
    Some(b) => {
      return above && ks[ks.len() - 1].cmp(b) < 0;
    },
    None => {
      return above;
    }
  }
}

// Each child is valid between the separators on either side of it, and the sizes add up to total.
function children_valid[K: cmp.Ord, V](ks: K[], kids: Node[K, V][], sizes: i32[], total: i32, height: i32, lo: Option[K], hi: Option[K]): boolean {
  let sum: i32 = 0;
  let i: i32 = 0;
  while (i < kids.len()) {
    sum = sum + sizes[i];
    let clo: Option[K] = lo;
    let chi: Option[K] = hi;
    if (i > 0) {
      clo = Some(ks[i - 1]);
    }
    if (i < ks.len()) {
      chi = Some(ks[i]);
    }
    if (sizes[i] != node_size(kids[i]) || !node_valid(kids[i], height - 1, false, clo, chi)) {
      return false;
    }
    i = i + 1;
  }
  return sum == total;
}

function keys_valid[K: cmp.Ord](pre: i32, ws: u64[], ks: K[], lo: Option[K], hi: Option[K]): boolean {
  return prefixes_valid(pre, ws, ks) && bounds_valid(ks, lo, hi);
}

function width_valid[K, V](n: Node[K, V], is_root: boolean): boolean {
  let width: i32 = node_width(n);
  return width <= NODE_MAX && (is_root || width >= NODE_MIN);
}

// A branch with n children has n - 1 separators and n sizes.
function shape_valid[K, V](ks: K[], kids: Node[K, V][], sizes: i32[]): boolean {
  return kids.len() >= 2 && ks.len() == kids.len() - 1 && sizes.len() == kids.len();
}

function node_valid[K: cmp.Ord, V](n: Node[K, V], height: i32, is_root: boolean, lo: Option[K], hi: Option[K]): boolean {
  if (!width_valid(n, is_root)) {
    return false;
  }
  match (n) {
    Leaf(pre, ws, ks, vs) => {
      return height == 0 && vs.len() == ks.len() && keys_valid(pre, ws, ks, lo, hi);
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      if (height == 0 || !shape_valid(ks, kids, sizes) || !keys_valid(pre, ws, ks, lo, hi)) {
        return false;
      }
      return children_valid(ks, kids, sizes, total, height, lo, hi);
    }
  }
}
```

### `iterator`

`OrdIter` walks the leaves in order. It keeps the branches on the path to the current leaf and the current leaf. For each branch, it keeps the index of the child that it follows. It does not copy the entries of the map.

```fern
<<iterator>>=
pub struct OrdIter[K, V] { path: Node[K, V][], at: i32[], leaf: Node[K, V], i: i32 }

// The iterator at the first entry under n, below the given path.
function iter_down[K, V](path: Node[K, V][], at: i32[], n: Node[K, V]): OrdIter[K, V] {
  let cur: Node[K, V] = n;
  while (true) {
    match (cur) {
      Leaf(pre, ws, ks, vs) => {
        return OrdIter { path: path, at: at, leaf: cur, i: 0 };
      },
      Branch(pre, ws, ks, kids, sizes, total) => {
        path = path.append(cur);
        at = at.append(0);
        cur = kids[0];
      }
    }
  }
  return OrdIter { path: path, at: at, leaf: cur, i: 0 };
}

// The iterator at the first entry of the leaf after the current one.
function iter_next_leaf[K, V](path: Node[K, V][], at: i32[]): OrdIter[K, V] {
  while (path.len() > 0) {
    let top: i32 = path.len() - 1;
    match (path[top]) {
      Leaf(pre, ws, ks, vs) => {},
      Branch(pre, ws, ks, kids, sizes, total) => {
        let j: i32 = at[top] + 1;
        if (j < kids.len()) {
          at = at.with(top, j);
          return iter_down(path, at, kids[j]);
        }
      }
    }
    path = span(path, 0, top);
    at = span(at, 0, top);
  }
  return OrdIter { path: path, at: at, leaf: empty_leaf(), i: 0 };
}

impl[K, V] iter.Iterator[(K, V)] for OrdIter[K, V] {
  function next(self: OrdIter[K, V]): Option[((K, V), OrdIter[K, V])] {
    match (self.leaf) {
      Leaf(pre, ws, ks, vs) => {
        if (self.i < ks.len()) {
          return Some(((ks[self.i], vs[self.i]), OrdIter { ...self, i: self.i + 1 }));
        }
        if (self.path.len() == 0) {
          return None;
        }
        return iter_next_leaf(self.path, self.at).next();
      },
      Branch(pre, ws, ks, kids, sizes, total) => {
        return None;
      }
    }
  }
}
```

### `public interface`

Merge, intersection and difference walk both maps in order and build the result from sorted entries. Their time is in proportion to the two sizes. When one map is much smaller than the other, they find or update each key of the smaller map in the larger map.

```fern
<<public interface>>=
pub function new[K, V](): OrdMap[K, V] {
  return OrdMap { root: empty_leaf() };
}

pub function from[K: cmp.Ord, V](pairs: (K, V)[]): OrdMap[K, V] {
  let m: OrdMap[K, V] = new();
  for p in pairs {
    m = m.insert(p.0, p.1);
  }
  return m;
}

pub function (m: OrdMap[K, V]) len(): i32 {
  return node_size(m.root);
}

pub function (m: OrdMap[K, V]) is_empty(): boolean {
  return node_size(m.root) == 0;
}

pub function (m: OrdMap[K, V]) insert[K: cmp.Ord, V](k: K, v: V): OrdMap[K, V] {
  return OrdMap { root: grown_root(node_insert(m.root, k, v)) };
}

pub function (m: OrdMap[K, V]) remove[K: cmp.Ord, V](k: K): OrdMap[K, V] {
  return OrdMap { root: shrunk_root(node_remove(m.root, k)) };
}

pub function (m: OrdMap[K, V]) get[K: cmp.Ord, V](k: K): Option[V] {
  let f: (Node[K, V], i32) = find_leaf(m.root, k);
  if (f.1 < 0) {
    return None;
  }
  match (f.0) {
    Leaf(pre, ws, ks, vs) => {
      return Some(vs[f.1]);
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      return None;
    }
  }
}

pub function (m: OrdMap[K, V]) get_or[K: cmp.Ord, V](k: K, fallback: V): V {
  let f: (Node[K, V], i32) = find_leaf(m.root, k);
  if (f.1 < 0) {
    return fallback;
  }
  match (f.0) {
    Leaf(pre, ws, ks, vs) => {
      return vs[f.1];
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      return fallback;
    }
  }
}

pub function (m: OrdMap[K, V]) contains[K: cmp.Ord, V](k: K): boolean {
  let f: (Node[K, V], i32) = find_leaf(m.root, k);
  return f.1 >= 0;
}

pub function (m: OrdMap[K, V]) update[K: cmp.Ord, V](k: K, init: V, f: (V) => V): OrdMap[K, V] {
  let v: V = m.get_or(k, init);
  return m.insert(k, f(v));
}

pub function (m: OrdMap[K, V]) min_key(): Option[K] {
  if (m.is_empty()) {
    return None;
  }
  match (edge_leaf(m.root, false)) {
    Leaf(pre, ws, ks, vs) => {
      return Some(ks[0]);
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      return None;
    }
  }
}

pub function (m: OrdMap[K, V]) max_key(): Option[K] {
  if (m.is_empty()) {
    return None;
  }
  match (edge_leaf(m.root, true)) {
    Leaf(pre, ws, ks, vs) => {
      return Some(ks[ks.len() - 1]);
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      return None;
    }
  }
}

pub function (m: OrdMap[K, V]) remove_min[K: cmp.Ord, V](): OrdMap[K, V] {
  match (m.min_key()) {
    Some(k) => {
      return m.remove(k);
    },
    None => {
      return m;
    }
  }
}

pub function (m: OrdMap[K, V]) remove_max[K: cmp.Ord, V](): OrdMap[K, V] {
  match (m.max_key()) {
    Some(k) => {
      return m.remove(k);
    },
    None => {
      return m;
    }
  }
}

pub function (m: OrdMap[K, V]) key_at(i: i32): Option[K] {
  if (i < 0 || i >= m.len()) {
    return None;
  }
  let f: (Node[K, V], i32) = node_at(m.root, i);
  match (f.0) {
    Leaf(pre, ws, ks, vs) => {
      return Some(ks[f.1]);
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      return None;
    }
  }
}

pub function (m: OrdMap[K, V]) value_at(i: i32): Option[V] {
  if (i < 0 || i >= m.len()) {
    return None;
  }
  let f: (Node[K, V], i32) = node_at(m.root, i);
  match (f.0) {
    Leaf(pre, ws, ks, vs) => {
      return Some(vs[f.1]);
    },
    Branch(pre, ws, ks, kids, sizes, total) => {
      return None;
    }
  }
}

pub function (m: OrdMap[K, V]) index_of[K: cmp.Ord, V](k: K): Option[i32] {
  let i: i32 = node_rank(m.root, k);
  if (i < 0) {
    return None;
  }
  return Some(i);
}

pub function (m: OrdMap[K, V]) keys(): K[] {
  let acc: K[] = [];
  return node_keys_into(m.root, acc);
}

pub function (m: OrdMap[K, V]) values(): V[] {
  let acc: V[] = [];
  return node_values_into(m.root, acc);
}

pub function (m: OrdMap[K, V]) iter(): OrdIter[K, V] {
  let path: Node[K, V][] = [];
  let at: i32[] = [];
  return iter_down(path, at, m.root);
}

pub function (m: OrdMap[K, V]) fold[K, V, A](init: A, f: (A, K, V) => A): A {
  return node_fold(m.root, init, f);
}

pub function (m: OrdMap[K, V]) for_each(f: (K, V) => void): void {
  node_for_each(m.root, f);
}

pub function (m: OrdMap[K, V]) map_values[K, V, W](f: (V) => W): OrdMap[K, W] {
  return OrdMap { root: node_map_values(m.root, f) };
}

pub function (m: OrdMap[K, V]) filter[K: cmp.Ord, V](pred: (K, V) => boolean): OrdMap[K, V] {
  let ks: K[] = [];
  let vs: V[] = [];
  for e in m.iter() {
    if (pred(e.0, e.1)) {
      ks = ks.append(e.0);
      vs = vs.append(e.1);
    }
  }
  return of_entries(ks, vs);
}

// One map is much smaller than the other when it has fewer than a sixteenth of the entries.
function much_smaller(a: i32, b: i32): boolean {
  return a < b / 16;
}

// The entries of both maps; a key in both takes its value from other.
pub function (m: OrdMap[K, V]) merge[K: cmp.Ord, V](other: OrdMap[K, V]): OrdMap[K, V] {
  if (much_smaller(other.len(), m.len())) {
    let out: OrdMap[K, V] = m;
    for e in other.iter() {
      out = out.insert(e.0, e.1);
    }
    return out;
  }
  if (much_smaller(m.len(), other.len())) {
    let out: OrdMap[K, V] = other;
    for e in m.iter() {
      if (!out.contains(e.0)) {
        out = out.insert(e.0, e.1);
      }
    }
    return out;
  }
  return merged(m.keys(), m.values(), other.keys(), other.values());
}

// The union of two runs of sorted entries; a key in both takes its value from the second run.
function merged[K: cmp.Ord, V](a: K[], av: V[], b: K[], bv: V[]): OrdMap[K, V] {
  let ks: K[] = [];
  let vs: V[] = [];
  let i: i32 = 0;
  let j: i32 = 0;
  while (i < a.len() || j < b.len()) {
    let c: i32 = 1;
    if (j == b.len()) {
      c = -1;
    } else if (i < a.len()) {
      c = a[i].cmp(b[j]);
    }
    if (c < 0) {
      ks = ks.append(a[i]);
      vs = vs.append(av[i]);
      i = i + 1;
    } else {
      ks = ks.append(b[j]);
      vs = vs.append(bv[j]);
      if (c == 0) {
        i = i + 1;
      }
      j = j + 1;
    }
  }
  return of_entries(ks, vs);
}

pub function (m: OrdMap[K, V]) entries(): (K, V)[] {
  let out: (K, V)[] = [];
  for e in m.iter() {
    out = out.append(e);
  }
  return out;
}

// The entries of m whose keys are in other, with their values from m.
pub function (m: OrdMap[K, V]) intersection[K: cmp.Ord, V](other: OrdMap[K, V]): OrdMap[K, V] {
  let ks: K[] = [];
  let vs: V[] = [];
  if (much_smaller(other.len(), m.len())) {
    for e in other.iter() {
      match (m.get(e.0)) {
        Some(v) => {
          ks = ks.append(e.0);
          vs = vs.append(v);
        },
        None => {}
      }
    }
    return of_entries(ks, vs);
  }
  if (much_smaller(m.len(), other.len())) {
    return m.filter((k: K, v: V): boolean => other.contains(k));
  }
  let a: K[] = m.keys();
  let av: V[] = m.values();
  let b: K[] = other.keys();
  let i: i32 = 0;
  let j: i32 = 0;
  while (i < a.len() && j < b.len()) {
    let c: i32 = a[i].cmp(b[j]);
    if (c == 0) {
      ks = ks.append(a[i]);
      vs = vs.append(av[i]);
      i = i + 1;
      j = j + 1;
    } else if (c < 0) {
      i = i + 1;
    } else {
      j = j + 1;
    }
  }
  return of_entries(ks, vs);
}

// The entries of m whose keys are not in other.
pub function (m: OrdMap[K, V]) difference[K: cmp.Ord, V](other: OrdMap[K, V]): OrdMap[K, V] {
  if (much_smaller(other.len(), m.len())) {
    let out: OrdMap[K, V] = m;
    for e in other.iter() {
      out = out.remove(e.0);
    }
    return out;
  }
  if (much_smaller(m.len(), other.len())) {
    return m.filter((k: K, v: V): boolean => !other.contains(k));
  }
  let ks: K[] = [];
  let vs: V[] = [];
  let a: K[] = m.keys();
  let av: V[] = m.values();
  let b: K[] = other.keys();
  let i: i32 = 0;
  let j: i32 = 0;
  while (i < a.len()) {
    let c: i32 = -1;
    if (j < b.len()) {
      c = a[i].cmp(b[j]);
    }
    if (c < 0) {
      ks = ks.append(a[i]);
      vs = vs.append(av[i]);
      i = i + 1;
    } else if (c == 0) {
      i = i + 1;
      j = j + 1;
    } else {
      j = j + 1;
    }
  }
  return of_entries(ks, vs);
}

pub function (m: OrdMap[K, V]) split_lt[K: cmp.Ord, V](k: K): OrdMap[K, V] {
  let t: Tree[K, V] = split_side(m.root, node_height(m.root), k, false);
  return OrdMap { root: shrunk_root(t.root) };
}

pub function (m: OrdMap[K, V]) split_gt[K: cmp.Ord, V](k: K): OrdMap[K, V] {
  let t: Tree[K, V] = split_side(m.root, node_height(m.root), k, true);
  return OrdMap { root: shrunk_root(t.root) };
}

pub function (m: OrdMap[K, V]) is_valid[K: cmp.Ord, V](): boolean {
  return node_valid(m.root, node_height(m.root), true, None, None);
}
```
