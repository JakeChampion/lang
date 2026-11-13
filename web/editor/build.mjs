import {build} from "esbuild";
import {readFile, writeFile} from "node:fs/promises";
import {fileURLToPath} from "node:url";
import path from "node:path";

const root = fileURLToPath(new URL(".", import.meta.url));
const result = await build({
  absWorkingDir: root,
  entryPoints: {"editor-deps": "index.js"},
  outdir: fileURLToPath(new URL("..", import.meta.url)),
  chunkNames: "editor-deps-[hash]",
  splitting: true,
  bundle: true,
  format: "esm",
  platform: "browser",
  target: "es2022",
  minify: true,
  legalComments: "inline",
  metafile: true,
  write: false,
});
const packages = [...new Set(Object.keys(result.metafile.inputs).flatMap(name => {
  const match = name.match(/^node_modules\/((?:@[^/]+\/)?[^/]+)\//);
  return match ? [match[1]] : [];
}))].sort();
const notices = await Promise.all(packages.map(async name => {
  const text = await readFile(path.join(root, "node_modules", name, "LICENSE"), "utf8");
  return name + "\n" + text.replaceAll("*/", "* /");
}));
const entry = fileURLToPath(new URL("../editor-deps.js", import.meta.url));
const license = "\n/*!\n" + notices.join("\n\n") + "\n*/\n";
await Promise.all(result.outputFiles.map(file =>
  writeFile(file.path, file.text + (file.path === entry ? license : ""))));
