import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, writeFile, readFile, rm, symlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  brotliDecompressSync,
  gunzipSync,
  zstdDecompressSync,
} from "node:zlib";
import { compressTree } from "../prepare.mjs";

test("asset variants round trip through independent platform decoders", async () => {
  const root = await mkdtemp(join(tmpdir(), "fern-compression-"));
  try {
    const bytes = Buffer.from(
      "<h1>Fern: one native server.</h1>\n".repeat(1024),
    );
    await writeFile(join(root, "lesson.html"), bytes);
    await writeFile(join(root, "image.png"), bytes);
    const wasm = Buffer.from([0, 97, 115, 109, 1, 0, 0, 0]);
    await writeFile(join(root, "kernel.wasm"), wasm);
    await symlink(join(root, "lesson.html"), join(root, "link.html"));
    assert.equal(await compressTree(root), 2);
    for (const [suffix, decode] of Object.entries({
      br: brotliDecompressSync,
      gz: gunzipSync,
      zst: zstdDecompressSync,
    })) {
      assert.deepEqual(
        decode(await readFile(join(root, `kernel.wasm.${suffix}`))),
        wasm,
      );
      const encoded = await readFile(join(root, `lesson.html.${suffix}`));
      assert.ok(encoded.length < bytes.length / 4);
      assert.deepEqual(decode(encoded), bytes);
      await assert.rejects(readFile(join(root, `image.png.${suffix}`)), {
        code: "ENOENT",
      });
      await assert.rejects(readFile(join(root, `link.html.${suffix}`)), {
        code: "ENOENT",
      });
    }
    assert.equal(await compressTree(root), 2);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
