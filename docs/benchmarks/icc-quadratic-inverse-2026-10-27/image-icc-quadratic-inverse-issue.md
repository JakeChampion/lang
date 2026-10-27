Parent: #13659 under #13216.

Implement a standalone inverse for bounded ICC.1:2022 Table 68 functions 3 and 4.
This scope requires an exact continuous join and a quadratic upper branch.
General fractional exponents and discontinuous joins remain unsupported.
The frozen sRGB profile has an upward join gap, without an observed downward step.
This scope does not judge that profile invalid.

Predeclared domain:

- Input is finite binary64 in [0,1], including negative zero.
- Raw signed s15Fixed16 coefficients have g=131072, a and c in [16384,262144], b nonpositive, and d in [0,65536].
- Raw a and d are multiples of 256. The active base at d is nonnegative, and 0 < a+b <= 65536.
- Function 3 has e=f=0. Function 4 has e and f in [0,32768].
- Let N=a*d/65536+b in raw units. Exact continuity requires N*N+e*65536 == c*d+f*65536.
- These integer products fit i64 exactly. The join and squared white endpoint have exact dyadic representations in binary64.
- Nonpositive g or a and negative active power bases return Domain. Other forms outside this initial subset return Unsupported.
- Unknown function numbers return Function. Incorrect coefficient counts return Shape. Input errors precede coefficient admission.
- Other known functions remain Unsupported in this standalone API. No other inverse or forward API changes.

The positive lower slope and upper quadratic branch give a continuous nonconstant monotonic curve.
ICC output clipping creates only a possible terminal flat interval.
Annex F.1 selects its first coordinate and the closest attained range endpoint for missing values.
The exact join selects d. Selected lower identity segments preserve input bits, including negative zero.
Other computed zeros are positive zero.

Predeclared finite criteria:

- Absolute inverse error is at most 1e-12 against independent 90-digit Decimal clipped forward equations and at least 112 monotonic bisection steps.
- Declared dyadic values, joins, endpoints, and selected identity segments require exact binary64 bits.
- Every admitted finite case must succeed. Numeric errors cannot waive a case or alter the domain, workload, or criterion.
- Include both functions, coefficient edges, both branches, join neighbors, terminal clipping, closest endpoints, and malformed inputs.
- Require two cancellation polls after full admission, scalar snapshots before callbacks, COW, and fresh-call tests.
- Require strict protocol fault controls, immutable source-pinned three-target records, nonzero balanced zero-live workers, and full regressions.

Acceptance:

- [ ] Verify the declared mathematical domain and exact join admission before implementation.
- [ ] Implement and test both functions without changing existing APIs or encoded admission.
- [ ] Qualify independent finite references and exact-bit controls on native, Wasm, and ARM64/QEMU10.
- [ ] Qualify admission precedence, every cancellation poll, COW, fresh calls, and collector fault controls.
- [ ] Preserve source/tool hashes and reproduce the reference bundle exactly.
- [ ] Complete full target regressions, unchanged complexity gates, format, build, vet, selected lint, and Go oracle checks.
- [ ] Merge one READY PR normally at the exact full head and publish evidence without broader acceptance claims.

Primary reference: https://www.color.org/specifications/ICC.1-2022-05.pdf, Table 68 and Annex F.1/F.3.
No model selection, destination transform, adaptation, BPC, encoded-image admission, or runtime CMM dependency changes.
All actual M5 and epic acceptance gates remain open.
