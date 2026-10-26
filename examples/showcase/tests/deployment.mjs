// Build fern-showcase:latest before this test. All resources use a unique project.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, readFile, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import https from "node:https";
import {
  brotliDecompressSync,
  gunzipSync,
  zstdDecompressSync,
} from "node:zlib";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const temporary = await mkdtemp(join(tmpdir(), "fern-deployment-"));
const project = `fern-deploy-test-${process.pid}-${Date.now()}`;
const image = process.env.FERN_SHOWCASE_IMAGE || "fern-showcase:latest";
const restored = `${project}-restored`;
const env = {
  ...process.env,
  COMPOSE_PROJECT_NAME: project,
  FERN_SITE_ADDRESS: "localhost",
  FERN_HTTP_PORT: "0",
  FERN_HTTPS_PORT: "0",
  FERN_SHOWCASE_IMAGE: image,
};
delete env.FERN_SOAK_PID;
const composeArgs = [
  "compose",
  "-p",
  project,
  "-f",
  join(root, "compose.production.yaml"),
];
function run(command, args, extra = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    env,
    encoding: "utf8",
    timeout: 120000,
    ...extra,
  });
  if (result.status !== 0)
    throw Error(
      `${command} ${args[0]} failed (exit ${result.status}): ${result.error?.message || result.stderr?.slice(-2000) || result.stdout?.slice(-2000)}`,
    );
  return result.stdout.trim();
}
const compose = (...args) => run("docker", [...composeArgs, ...args]);
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
let ca, port;
function request(path, headers = {}) {
  return new Promise((resolve, reject) => {
    const req = https.get(
      { hostname: "localhost", port, path, ca, headers },
      (response) => {
        const chunks = [];
        response.on("data", (b) => chunks.push(b));
        response.on("end", () =>
          resolve({
            status: response.statusCode,
            headers: response.headers,
            body: Buffer.concat(chunks),
          }),
        );
      },
    );
    req.on("error", reject);
    req.setTimeout(5000, () => req.destroy(Error("HTTPS request timed out")));
  });
}
async function ready() {
  const until = Date.now() + 45000;
  while (Date.now() < until) {
    try {
      if ((await request("/healthz")).status === 200) return;
    } catch {}
    await pause(500);
  }
  throw Error("TLS proxy did not reach the app");
}
function labs(mode) {
  run(process.execPath, [
    join(root, "tests/labs-server.test.mjs"),
    `https://localhost:${port}`,
    ...(mode ? [mode] : []),
  ]);
}
try {
  run("docker", ["image", "inspect", image]);
  compose("up", "-d");
  port = Number(compose("port", "proxy", "443").split(":").at(-1));
  const rootFile = join(temporary, "root.crt");
  for (let attempt = 0; attempt < 30; attempt++) {
    try {
      compose(
        "cp",
        "proxy:/data/caddy/pki/authorities/local/root.crt",
        rootFile,
      );
      ca = await readFile(rootFile);
      break;
    } catch {
      await pause(500);
    }
  }
  assert.ok(ca, "The TLS root certificate is missing");
  const previousCA = process.env.NODE_EXTRA_CA_CERTS
    ? await readFile(process.env.NODE_EXTRA_CA_CERTS)
    : Buffer.alloc(0);
  const combined = join(temporary, "combined.pem");
  await writeFile(combined, Buffer.concat([previousCA, Buffer.from("\n"), ca]));
  env.NODE_EXTRA_CA_CERTS = combined;
  await ready();
  for (const [encoding, decode] of [
    ["br", brotliDecompressSync],
    ["gzip", gunzipSync],
    ["zstd", zstdDecompressSync],
  ]) {
    for (const path of ["/", "/prism/reference?w=64&h=40"]) {
      const reply = await request(path, {
        "Accept-Encoding": encoding,
        "Netlify-Original-Accept-Encoding": "identity",
      });
      assert.equal(reply.status, 200);
      assert.equal(reply.headers["content-encoding"], encoding);
      const text = decode(reply.body).toString();
      if (path === "/") assert.ok(text.includes("<html"));
      else assert.equal(JSON.parse(text).pixels.length, 2560);
    }
  }
  labs();
  run(process.execPath, [
    join(root, "tests/labs-rpc.test.mjs"),
    `https://localhost:${port}`,
  ]);
  run(process.execPath, [
    join(root, "tests/soak.mjs"),
    `https://localhost:${port}`,
    "foundry",
    "10",
    "16",
  ]);
  compose("kill", "-s", "SIGKILL", "app");
  compose("up", "-d", "app");
  await ready();
  labs("recover");
  const config = join(temporary, "production.env");
  await writeFile(
    config,
    `FERN_SITE_ADDRESS=localhost\nFERN_HTTP_PORT=0\nFERN_HTTPS_PORT=0\nFERN_SHOWCASE_IMAGE=${image}\n`,
  );
  const archive = join(temporary, "backup.tar.gz");
  run(join(root, "deploy/backup.sh"), [archive], {
    env: { ...env, FERN_DEPLOY_ENV: config },
  });
  assert.ok((await readFile(archive)).length > 0);
  const originalArchive = await readFile(archive);
  assert.throws(() =>
    run(join(root, "deploy/backup.sh"), [archive], {
      env: { ...env, FERN_DEPLOY_ENV: config },
    }),
  );
  assert.deepEqual(await readFile(archive), originalArchive);
  const proxyImage = JSON.parse(compose("config", "--format", "json")).services
    .proxy.image;
  run("docker", ["volume", "create", restored]);
  run(
    "docker",
    [
      "run",
      "--rm",
      "--network",
      "none",
      "--entrypoint",
      "/bin/sh",
      "--interactive",
      "--mount",
      `type=volume,source=${restored},target=/data,volume-nocopy`,
      proxyImage,
      "-ec",
      'test -z "$(ls -A /data)"; tar -xzf - -C /data',
    ],
    { input: await readFile(archive) },
  );
  compose("stop", "app");
  env.FERN_RESTORE_VOLUME = restored;
  run("docker", [
    ...composeArgs,
    "-f",
    join(root, "deploy/restore.volume.yaml"),
    "up",
    "-d",
    "app",
  ]);
  await ready();
  labs("recover");
  console.log(
    "TLS, compression, WebSocket reconnects, forced restart, backup, and empty-volume restore OK",
  );
} finally {
  try {
    compose("down", "--volumes", "--remove-orphans");
  } catch (error) {
    console.error(error.message);
  }
  spawnSync("docker", ["volume", "rm", restored], { encoding: "utf8", env });
  await rm(temporary, { recursive: true, force: true });
}
