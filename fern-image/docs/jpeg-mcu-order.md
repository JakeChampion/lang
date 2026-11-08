# JPEG MCU block order

The internal `mcu_order` module describes a complete known-height DCT frame in one scan.
It accepts one through four components in frame order, with explicit horizontal and vertical sample factors.
It does not assign color interpretation or table selectors to those components.
All runtime code is pure Fern.

`inspect` checks frame dimensions, component count, factors and the caller's maximum block count.
It returns MCU columns, rows, count, blocks per MCU, total blocks and maximum sample factors.
The calculation does not allocate sample or coefficient storage.
It uses wide arithmetic for the block limit.

`fold_blocks` visits each MCU in raster order.
Within each MCU, it visits components in frame order and each component's block rectangle in raster order.
A single-component scan always contains one block per MCU, even with factors larger than one.
Interleaved scans contain at most ten blocks per MCU.
The factors can have nonintegral ratios.

Each address contains its MCU number, unit number, component index, component block position and actual sample dimensions.
Its `dummy` flag identifies a block whose entire sample area lies outside those dimensions.
A partial edge block does not set this flag.
The separate component extractor's `block_padded` helper fills those blocks under an explicit edge policy.

The fold borrows the factor list and keeps its own state local to each call.
Invalid input fails before cancellation callbacks.
Cancellation checks precede traversal, each block callback and success.
A consumer error stops traversal immediately.
Earlier consumer effects remain after an error.

The reference campaign enumerates every factor tuple of length one through four over factors one through four.
It includes invalid interleaved sums, odd dimensions, wide arithmetic and exact limits.
An independent Python oracle sorts complete component grids by MCU and unit number.
Native and Wasm workers must match every address and report positive, balanced heaps with zero live bytes.
These checks do not establish throughput, physical memory bounds or complete JPEG support.

The color writer still needs separate component DC predictors, MCU restart intervals, table selectors and frame serialization.
Public JPEG option and alpha decisions remain open.

[ITU-T T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf), A.1.1 and A.2, defines component dimensions and MCU order.
Section B.2.3 defines interleaved component order and the ten-block limit.
