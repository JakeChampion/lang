# Architecture

M0 contains one Fern package with a pixel module.
The manifest uses that module as its library entry.
Tests, examples, and probes import local files.

No codec, HTTP client, cache, or storage library enters the pixel dependency graph.

Future dependency direction:

```text
pixels -> color and resize -> pipeline -> image facade -> image server
pixels and bytes -> codecs -> pipeline
```

An arrow means that the component on the right depends on the component on the left.
Codec modules depend on bytes and pixels. They do not depend on HTTP.
Only the pixel module exists in M0.

Future packages require explicit manifests and dependencies when their code exists.
The package location keeps the image experiment separate from compiler sources.

The current operations use complete images for the smallest useful test surface.
They do not define the future pipeline interface.
Future region sources can expose rows without allocation of a complete frame.

See [the pipeline plan](pipeline-design.md) and [decision 0001](decisions/0001-foundation.md).
