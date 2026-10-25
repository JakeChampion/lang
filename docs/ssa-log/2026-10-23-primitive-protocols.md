# Primitive protocols and default display

Issues: #12704, #13081, #13082, #13083, #13084, and #13085.

Every primitive now supports `to_string()` and interpolation without a source import.
A visible user method takes precedence.
Private runtime functions share the existing integer, floating-point, and Unicode encoding kernels.
Loading those functions adds no source import edges or unrelated receiver methods.
The public `std/f64` utilities remain available.

`core/cmp` supplies the missing boolean order, floating-point hashes, and character and pointer-size display instances.
Float hashes respect the existing total equality: both zero signs agree, and all NaN signs and payloads agree.
`std/num` includes arithmetic and identity instances for `u8` and `usize`.

The interpreter preserves byte identity through arrays, generic calls, captures, and cells.
Both checkers accept contextual primitive type arguments and the required associated-function namespaces.
Wrong builtin method arity produces E004.
Formatting import hints only name modules that define the requested receiver method.

Tree shaking retains the default formatter for method syntax and direct output calls.
This includes drivers that prune before the checker rewrites output arguments.
The final three-stage bootstrap converges, and all eleven strict driver size checks pass without changing their limits.

The [validation record](../benchmarks/primitive-protocols-2026-10-23/README.md) includes source identities, regression results, allocation traces, and kernel cost measurements.
