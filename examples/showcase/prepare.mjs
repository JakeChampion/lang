// Build immutable content variants. The native server has no Node dependency.
import {
  readdir,
  readFile,
  writeFile,
  rename,
  mkdir,
  cp,
  rm,
} from "node:fs/promises";
import { resolve, join, extname, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import {
  brotliCompressSync,
  gzipSync,
  zstdCompressSync,
  constants,
} from "node:zlib";

export const encoders = {
  br: (bytes) =>
    brotliCompressSync(bytes, {
      params: { [constants.BROTLI_PARAM_QUALITY]: 11 },
    }),
  gz: (bytes) => gzipSync(bytes, { level: 9 }),
  zst: (bytes) =>
    zstdCompressSync(bytes, {
      params: { [constants.ZSTD_c_compressionLevel]: 15 },
    }),
};

export async function compressTree(root) {
  let count = 0;
  for (const entry of await readdir(root, { withFileTypes: true })) {
    const path = join(root, entry.name);
    if (entry.isDirectory()) {
      count += await compressTree(path);
    } else if (
      entry.isFile() &&
      /\.(html|css|m?js|svg|xml|txt|json)$/.test(extname(path))
    ) {
      const bytes = await readFile(path);
      for (const [suffix, encode] of Object.entries(encoders)) {
        const target = `${path}.${suffix}`;
        await writeFile(`${target}.tmp`, encode(bytes));
        await rename(`${target}.tmp`, target);
      }
      count++;
    }
  }
  return count;
}

async function main() {
  const destination = resolve(process.argv[2] ?? "examples/fractal/.showcase");
  const source = dirname(fileURLToPath(import.meta.url));
  await rm(destination, { recursive: true, force: true });
  await mkdir(destination, { recursive: true });
  for (const entry of await readdir(source, { withFileTypes: true })) {
    if (entry.name.endsWith(".fern.md") || entry.name === "web") {
      await cp(join(source, entry.name), join(destination, entry.name), {
        recursive: true,
      });
    }
  }
  const { spawnSync } = await import("node:child_process");
  const server = resolve(process.argv[3] ?? "examples/fractal/server");
  const examples = resolve(source, "..");
  const result = spawnSync(
    server,
    ["--prepare", examples, join(destination, "web/.source")],
    { stdio: "inherit" },
  );
  if (result.error) throw result.error;
  if (result.status !== 0)
    throw new Error(`Lesson preparation failed: ${result.status}`);
  const count = await compressTree(join(destination, "web"));
  const fractalCount = await compressTree(join(examples, "fractal/web"));
  console.log(
    `Prepared ${count + fractalCount} assets with Brotli, gzip, and zstd.`,
  );
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
)
  await main();
