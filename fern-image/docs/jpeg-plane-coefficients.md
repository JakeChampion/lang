# JPEG component coefficients

The internal `plane_coefficients` module connects component blocks, the forward transform and explicit quantization.
`inspect` checks the layout, minimal storage, quantizers and complete block count before callbacks.
`fold_blocks` delivers owned blocks in plane row order with explicit coordinates.
Each block contains 64 natural-order integers. Quantizers use transmitted order.

The caller supplies replication or constant fill and a maximum block count.
The module does not retain a complete coefficient plane.
Consumer state and retained blocks remain the responsibility of the caller.
The block limit controls operations. It does not establish a physical memory bound.

A consumer error stops the fold and retains the original error value.
Cancellation returns an error, even after the final callback.
Earlier callback effects remain visible. Callers must not assume transactional callbacks.
Borrowed sample windows, row padding and quantizer aliases remain unchanged.

This helper does not define a public image encoder or choose quality, color conversion, alpha or sample factors.
Full format coverage and measured performance remain open.
