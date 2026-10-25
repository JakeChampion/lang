# Codec plan

No image codec exists in M0.
No external codec or image engine enters the runtime.
The first codec milestone targets a restricted PNG subset.

It must audit the current Fern DEFLATE module before reuse.
Source presence alone does not prove zlib compatibility or bounded decompression.

The PNG feasibility issue must specify RGB8/RGBA8, noninterlaced input, CRC checks, and chunk order checks.
It must also specify compressed bytes, decompressed bytes, pixels, metadata, and CPU budgets.
Unsupported palette, depth, and interlace modes require explicit errors.

A container parser alone does not establish decode support.

JPEG baseline decode requires a separate milestone.
WebP lossy and lossless codecs require separate compatibility plans.
AVIF requires a feasibility decision for both the container and AV1 compression.

Codec interfaces must separate bytes, decoded metadata, pixel rows, and explicit errors.
HTTP fetch belongs outside those interfaces.
