# Build the playground editor locally

The playground editor loads its dependencies from the same origin. The build uses exact package versions and a lock file. esbuild combines all seven CodeMirror packages and their dependencies into `web/editor-deps.js`. The bundle contains the license text of each package.

Both `web/build.sh` and `scripts/netlify-build` build the editor before the static assets reach the site. The Pages workflow already calls `web/build.sh`. Its asset glob copies the generated JavaScript file.

The browser regression blocks every external HTTP request. It requires a visible editor and an enabled Run button. The original page fails this case because its imports require esm.sh. The repaired page passes. All 25 playground cases pass locally, with zero skips and zero flaky results.

The browser fixture uses the unchanged compiler inputs from PR #13897 at `280b5f8054d5e808f08b90fdf6047175d2d096db`. The compiler emits a real Wasm driver of 14,455,705 bytes. Local validation uses Chromium 154. The CI failure used Chromium 156. The local configuration changes the browser executable, absolute paths, and the reporter. It preserves the test assertions and timeouts.

A clean `npm ci` rebuild produces identical bundle bytes. JavaScript and shell syntax checks pass. This record supports the repair for #13911. It does not establish a CI pass or complete #4412.
