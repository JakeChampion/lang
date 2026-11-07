# JPEG component blocks

The internal extractor reads one eight-bit component plane and returns an owned row-major block for the forward transform.
It does not choose color conversion or a public image padding policy.

`component_blocks.block` accepts a borrowed byte window, `PlaneLayout`, block coordinates, an explicit `Edge` and cancellation.
`PlaneLayout` contains width, height and byte stride.
Block coordinates count complete eight-sample steps from the plane origin.

`Replicate` repeats the final image row or column outside the plane.
`Constant(value)` uses the supplied byte outside the plane.
Both policies preserve image samples inside the plane. Neither policy reads row padding as image data.

The minimal storage footprint ends after the last image sample.
The final row does not need padding bytes. Storage checks use wide arithmetic.
Invalid layout, storage and block bounds return explicit errors before callbacks or sample access.
An origin outside the plane returns `Bounds`, even with an edge policy.

The helper checks cancellation before allocation, during rows and before success.
Input windows and retained aliases remain unchanged.
Tests cover odd dimensions, partial blocks, strides, fill values, large invalid extents, cancellation and aliases.
An independent Python oracle checks complete blocks on native and Wasm.

This helper does not complete JPEG encoding or establish a speed or physical memory bound.
