# The over-release counter has one spelling

irlower lowered the over-release counter under two names:
`__rc_underflow_count`, which native uses, and `__rc_underflow`, an alias
that only the self-host accepted. The alias was meant to last until the tests
moved over. The typed path contracts only `__rc_underflow_count`, so under
`FERN_SEM_IR_STRICT=1` every test program that read `__rc_underflow()` was
refused with "call target has no semantic contract".
`TestSelfHostMapBoxKeyReclaim*` was among them.

The tests now call `__rc_underflow_count()`, and the alias is gone from
irlower. The diff removes 1,283 mentions of `__rc_underflow()` from 190 test
files. 1,227 are in the Fern programs and failure messages; 56 are in Go
comments. 186 of the files held a mention; the other four changed only
comment text. Older entries in this log keep the name they measured with.

Three comments had described the alias itself. The rename left each one
contrasting `__rc_underflow_count` with itself, so they were rewritten, not
renamed.

The three detector contracts, `__rc_underflow_count` and the two
`__arr_push_shared_*` totals, sat in `value_contracts`, beside the surface
builtins. `TestSelfHostContractsEveryBuiltin` skips `__` names and
`TestSelfHostTypesEveryIntrinsicFamily` had no family for them, so no test
checked that they were contracted at all. They move to `rc_contracts`, the
`__rc_*` intrinsics' table, and an "rc probe" family gates them.

## Tests

- `TestSelfHostTypesEveryIntrinsicFamily`'s new "rc probe" family: every
  `__rc_*` and `__arr_push_shared_*` intrinsic native types must be typed by
  the self-host checker and contracted in `intrinsic_contracts`. It failed
  on all three detectors before they moved.
- `TestSelfHostMapBoxKeyReclaimX86_64` and `TestSelfHostRcRuntime*` pass
  under `FERN_SEM_IR_STRICT=1`: 34 tests. No CI lane sets the flag, so the
  rest of the renamed suite is compiled under strict only by a local sweep.
- The native checker rejects the old name as an undefined identifier (E001).
