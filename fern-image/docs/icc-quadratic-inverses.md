# Bounded continuous quadratic ICC inverses

`codecs/icc/quadratic_inverse.invert` accepts standalone `tag_data.ParametricData`, a finite normalized input, and a cooperative cancellation callback.
Native child #13704 declared the domain and finite criteria before implementation.
The function returns an owned scalar result or an explicit error.
It does not select profiles or change other inverse APIs.

## Initial domain

Only ICC.1:2022 Table 68 functions 3 and 4 are admitted here.
The upper exponent is exactly two.
Raw signed s15Fixed16 `a` and `c` lie in `[16384,262144]`, and `b` is nonpositive.
Raw `d` lies in `[0,65536]`, and both `a` and `d` are multiples of 256.
The active power base at `d` is nonnegative, and raw `a+b` lies in `[1,65536]`.

Function 3 has zero offsets.
Function 4 admits raw `e` and `f` in `[0,32768]`.
The exact integer join equation is:

```text
N = a*d/65536 + b
N*N + e*65536 == c*d + f*65536
```

The selected lattice makes `N` integral without a rounded product.
The bounded integer products fit i64, and the join and white endpoint are exact binary64 dyadic values.
No tolerance accepts a discontinuous join.
The positive lower slope and nonnegative active quadratic base give a continuous nonconstant monotonic curve.

These restrictions define the first numerical scope, not general ICC prohibitions.
The API rejects other valid forms with `Unsupported`.
Unknown function numbers return `Function`, incorrect coefficient counts return `Shape`, and invalid inputs return `Input`.
Nonpositive exponents or upper slopes and negative active bases return `Domain`.
Full input and coefficient admission precedes callbacks.

## Inverse policy

The reference is [ICC.1:2022](https://www.color.org/specifications/ICC.1-2022-05.pdf), Table 68 and Annex F.1/F.3.
The function uses the first coordinate of a terminal interval produced by output clipping.
Missing output values select the closest attained endpoint.
The exact join selects `d`.
Selected lower identity segments preserve input bits, including negative zero.
Other computed zeros are positive zero.

The kernel keeps the offset-free target as a compensated difference before its square root.
It snapshots coefficients before the first callback.
Two callback polls cover every successful call, with cancellation checks before and after arithmetic.
The function stores no callbacks in cells and preserves caller aliases through COW.

The frozen sRGB reference profile has a quantized upward join gap.
That observation does not establish profile invalidity or a downward step.
General discontinuous and fractional-power inverse policies remain separate work.
This standalone API does not infer inverse eligibility from `ForwardData`.

## Finite qualification

The unchanged predeclared criterion is absolute error at most `1e-12` for the finite reference corpus.
Independent 90-digit Decimal forward equations and 112 monotonic bisection steps supply numeric expectations.
Exact Fraction join coordinates and declared dyadic, endpoint, and identity controls require exact bits.
Every admitted case must succeed without a `Numeric` waiver.

The private file worker and strict collector check exact errors, callback counts, process exits, reference identities, and heap censuses.
Eleven protocol tests include positive observations and ten fault groups.
The reference generator records its own hash and deterministic seed.

There is no model selection, destination transform, adaptation, BPC, encoded-image admission, or runtime CMM dependency change.
Finite results do not establish universal precision, compatibility, physical/process bounds, hard deadlines, or production readiness.
All actual M5 and epic acceptance gates remain open.
