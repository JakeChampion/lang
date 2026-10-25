# Pipeline plan

The future graph separates source, decode, orientation, resize, crop, color, and encode operations.
Each node must expose dimensions, format, alpha semantics, and required input regions.
A scheduler must enforce scratch budgets and release rows after their final consumer.

Codec access patterns determine whether a node can request arbitrary regions.
Sequential codecs cannot promise efficient arbitrary tile access.

M0 contains no graph, scheduler, cancellation, or operation fusion.
The pixel module provides a scalar correctness baseline.
A future row interface must avoid a permanent requirement for complete frame allocation.

First measure copy costs and decode access patterns before selection of a tile size.
