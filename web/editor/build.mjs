import {build} from "esbuild";
import {readFile, writeFile} from "node:fs/promises";
import {fileURLToPath} from "node:url";
import path from "node:path";

const root = fileURLToPath(new URL(".", import.meta.url));
const result = await build({
  absWorkingDir: root,
  entryPoints: ["index.js"],
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
await writeFile(new URL("../editor-deps.js", import.meta.url),
  result.outputFiles[0].text + "\n/*!\n" + notices.join("\n\n") + "\n*/\n");
