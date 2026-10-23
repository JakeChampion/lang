# A Fern web server in a Netlify container

This example deploys a Fern HTTP server to Netlify as a container. The
container product is a private beta. Netlify runs the image that the build
makes, so it does not need to know about Fern.

The server answers three routes:

| Route | Response |
| --- | --- |
| `/` | An HTML page. |
| `/api/hello?name=X` | JSON: `{"message":"Hello, X!","served_at_ms":...}`. |
| `/healthz` | `ok` as plain text. |

Other paths get 404. A route with the wrong method gets 405.

## Files

- `server.fern`: the server. It listens on port 8080 and stops cleanly on SIGTERM.
- `build.sh`: downloads the nightly Fern release and compiles the server for
  `x86-64-linux`. The result is a static binary of about 1.2 MB. The script
  then builds the image for `linux/amd64` and saves it as
  `netlify/functions/fern-server.tar`. The Netlify build image has no Docker
  and no Podman, so the script installs a static, rootless Podman 4.9.5. It
  uses the same configuration as `netlify/php-on-netlify`.
- `Dockerfile`: copies the binary into a `scratch` image. It has no `RUN`
  step, because Podman cannot start a container in the Netlify build sandbox.
- `netlify.toml`: runs `build.sh` and sends every request to the container.

The compiler runs on the build machine and cross-compiles. Thus a build on
Apple Silicon does not run the compiler under x86-64 emulation.

## Deploy

1. Add the site ID to the targeting of the two feature flags,
   `bitballoon_container_functions` and `netlify-server-play-functions`.
2. Set the base directory of the site to `examples/netlify-container`.
3. Deploy from Git. The Netlify build runs `build.sh`.

To deploy from your own machine, use Docker and the Netlify CLI:

```sh
CONTAINER_TOOL=docker ./build.sh
netlify deploy --prod
```

## Run it locally

```sh
fern -target arm64-darwin -o server server.fern && ./server
curl 'localhost:8080/api/hello?name=Netlify'
```

The x86-64 image needs AVX2. The x86-64 emulation in Docker on Apple Silicon
does not supply AVX2, so the image does not start there. Run the server
natively, as above, or run the image on an x86-64 host.

`examples/tests/netlify_container_sim.fern` tests the routes in simulation.
