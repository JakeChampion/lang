# The TUPB payload tier forgives a non-escaping alias bind

The tuple flavor of the aliased-param borrow carve-out — the payload-tier port
of the box tier's forgiveness. The two `tuple_mixed__*__alias_param` matrix
cells sat at a constant keep-sweep leak with the box flag already '1': the
refusal was `tuple_payload_borrow_flags` feeding
`rctuple_payload_escapes_alias` a hardwired-empty `alias_ok`, so the callee's
`var x: (i32, i32[]) = src` read as a bare-ident payload escape. The same
hardwired-empty shape the box tier had before the interproc builder learned
`param_alias_bind_sites` — one tier down, and the forgiveness machinery
(`rctuple_esc_stmt_alias`'s site-keyed StmtVar arm) already present, only
never fed.

## The port, and the one proof the box tier does not need

`tuple_param_alias_sites` collects the forgivable binds under the payload
tier's own rules:

- **Registry-independent** — the alias local's `body_unsafe_for` runs with an
  empty registry, matching the tier's documented invariance, so the interproc
  fixpoint's monotone-decreasing argument is untouched.
- **The alias must be payload-safe in its own right** —
  `rctuple_payload_escapes_alias` runs on the alias name too. This is the
  load-bearing difference from the box tier's collector: `var x = src;
  var e = x.1;` extracts an element one alias hop removed, which is the
  indirect form of the sanitizer-confirmed use-after-free the
  `tuple_mixed__elemret__payload_refused` row pins (a box-borrowable callee
  returning `src.1` while the caller's TUPRCS deep free walks every rc
  position). Forgiving the bind on box-safety alone would have rebuilt that
  grant.

The bind-only half is deliberate: `rctuple_esc_stmt_alias`'s StmtAssign arm
has no alias forgiveness, so a reassign-shaped alias (`x = src`) stays refused
— a leak-at-worst, and no matrix cell exercises it. The `arrenum` ELB tier has
the same hardwired-empty structure and presumably the same class of refusal
for array-of-boxes kinds; no matrix cell measures it today, so it is a lead,
not a debt entry.

## Measured (x86-64, 100 rounds; native column for the answers)

| shape | before | after | native |
|---|---|---|---|
| fnscope alias bind | 2/0, 80 live | **2/2, 0** | 2/2, 0 (exit 43 both) |
| if_block alias bind | 2/0, 80 live | **2/2, 0** | 2/2, 0 (exit 75 both) |
| caller reads back after churn (20 rounds) | — | **100/100, 0** (exit 58 = native) | 100/100, 0 |
| alias escapes by whole-tuple return | 2/0, 80 live | 2/0, 80 live (held) | 2/2 via dup-at-extract |
| element extracted through the alias | 2/0, 80 live | 2/0, 80 live (held) | 2/2 via dup-at-extract |

Sanitize leg on both granted cells: zero findings, exits unmoved.

## Witnessed vs contract-only

The soundness row (caller reads its own tuple back after three fresh
allocations) is a witnessed guard on both granted shapes; the two refusal
rows are witnessed holds. The registry-independence of the collector is
contract-only — argued from the empty registry, not measured across fixpoint
iterations.

## Gates

Pinned by `self_host_tuple_alias_param_borrow_test.go` (six rows, counts
asserted). Matrix rows flipped in both files;
`tuple_mixed__elemret__payload_refused`, `__fnscope__borrowed_arg` and
`__ownedret_alias__bind_local` unmoved. Stage-2 arm64 fixpoint run for the
exit-sweep credit, per the standing rule.

## Next lead

`tuple_mixed__ownedret_alias__bind_local` is the remaining tuple row — a
different mechanism (owned-return admission walks frame-freshness with empty
alias lists; no registry entry, no caller credit), but the same
hardwired-empty pattern one admission over.
