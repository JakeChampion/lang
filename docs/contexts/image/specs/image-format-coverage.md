# Image format coverage

**Status:** In progress. No format-wide completion or performance claim.
**Date:** 2026-11-07.

The user requires complete decode and encode support in pure Fern.
This inventory keeps restricted delivery stages separate from the final acceptance scope.
Each row needs independent inputs, explicit errors, resource checks and evidence for both directions.

## PNG

The [PNG third edition](https://www.w3.org/TR/2025/REC-png-3-20250624/) defines static images, animation and color declarations.

| Feature group | Current coverage | Remaining work |
| --- | --- | --- |
| Sample representation | RGB8/RGBA8 output, packed grayscale and indexed input | Sixteen-bit precision and corresponding output modes |
| Grayscale | Decode at depths 1/2/4/8, grayscale-alpha8 | Sixteen-bit input and grayscale output options |
| Reconstruction | Five filters, noninterlaced input, packed sample expansion | Adam7 and wider output modes and filter selection |
| Transparency | Explicit alpha, palette alpha and grayscale/RGB keys | Complete precision and output options |
| Color | Restricted admission | Standard color declarations, profiles and HDR interpretation |
| Metadata | Most data is discarded or rejected | Defined ancillary chunks and explicit preservation policies |
| Animation | Unsupported | Frame control, composition, disposal, timing and output |

The final PNG audit must enumerate each defined chunk and every legal color/depth combination.
Ancillary data requires an explicit handling rule even when it does not affect pixels.

## JPEG

[ITU-T T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf) defines sequential DCT, progressive DCT, lossless and hierarchical processes.
It includes Huffman and arithmetic entropy methods, with precision rules for each process.

| Feature group | Current coverage | Remaining work |
| --- | --- | --- |
| Image input | Restricted baseline component and JFIF decode | Full process, precision, component and scan combinations |
| Image output | Internal single-component frames from blocks or a borrowed plane | Color assembly, image options and other JPEG processes |
| Transform | Inverse DCT, forward DCT and explicit quantization | Qualify complete encode/decode paths for each applicable process |
| Stream control | Restricted baseline checks | All legal scan scripts, restart rules and table forms |
| Image interpretation | Explicit forward and inverse sample kernels, separate restricted color/metadata modules | Integrate complete color, profile, orientation and metadata behavior |

The first baseline writer does not complete JPEG support.
The final audit must map every supported process to its normative conformance requirements.

## JPEG XL

The [reference project's overview](https://github.com/libjxl/libjxl/blob/main/doc/format_overview.md) describes the codestream and container separately.
The implementation must follow ISO/IEC 18181 and its conformance requirements.
No JPEG XL runtime codec exists in the current image engine.

The inventory includes Modular and VarDCT, lossless and lossy operation, color and extra channels, and progressive groups.
It also includes frames, animation, blending, orientation, profiles, patches, splines, noise and reconstruction filters.
Container coverage includes complete and partial codestream boxes, metadata, compressed boxes, frame indexes and exact JPEG bitstream reconstruction.
Precision and extension limits require a normative field-by-field audit before implementation.
All required entropy, compression and color operations must use Fern.

## QOI

Merged QOI support implements RGB8/RGBA8 decode and encode, all six operations and both color declarations.
It validates exact pixel count, termination, dimensions, limits, cancellation and caller ownership.
Independent bidirectional cases cover the published QOI reference.
The maintainer merged QOI and expanded PNG while PR CI was incomplete.
Their local evidence remains separate from full GitHub CI qualification.

## Shared contracts and performance

The current RGB8/RGBA8 pixel model cannot express every required sample, metadata and frame form.
The user selected explicit integer and floating-point formats in the shared pixel model.
Retain the existing RGB8/RGBA8 APIs.
Specify precision, frames and metadata contracts before new public interfaces.
Preserve existing callers while these interfaces develop.

Benchmark decode and encode separately with exact tool versions and input hashes.
Record native hardware, compiler, options, quality, compressed size, elapsed time and memory.
Use equal feature coverage and correctness criteria for each comparison.
Keep setup and process overhead separate from codec execution.
Run a small campaign before larger inputs or repeat counts.
The fastest-in-the-world objective remains open until measurements establish a specific result.
