# Standalone ICC curve inverses

The new kernel implements explicit identity, gamma, and sampled inverses under #13698.
It preserves the predeclared finite criterion and Annex F.1 flat-interval policies.
All three immutable campaigns and full regression runs passed.
No model selection or encoded admission changed.

The [qualification record](../benchmarks/icc-curv-inverse-2026-10-27/README.md) contains the exact source identities, raw evidence, and limitations.
All actual M5 and epic acceptance gates remain open.
