# A durable showcase server

This deployment runs one Fern process behind Caddy. WebSocket upgrades and HTTP requests reach the same owner.
Caddy supplies automatic HTTPS. The native server supplies Brotli, gzip, and zstd.
The app has no public port. Its root filesystem is read-only.

The environment needs Docker Compose, `flock`, and an x86-64 Linux host with the Fern CPU baseline: x86-64-v3 and AES-NI.
Point a public hostname at that host. Permit inbound TCP ports 80 and 443 for HTTPS and certificate renewal.
UDP port 443 permits HTTP/3. HTTP and WebSocket operation do not require UDP.

## Build and start

From the repository root, build the compiler and image:

```sh
make build
FERN="$PWD/bin/fern-selfhost" CONTAINER_TOOL=docker ./examples/fractal/build.sh
docker tag fern-server fern-showcase:latest
cd examples/showcase
cp deploy/deploy.env.example deploy/production.env
```

Set `FERN_SITE_ADDRESS` in `deploy/production.env` to the public hostname.
Set `FERN_SHOWCASE_IMAGE` to the image tag or digest that you built.
Start the services:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml up -d
```

Use one app replica. Do not mount this data volume into another active writer.
This deployment keeps shared rooms on one process. It does not distribute one world across machines.
The proxy keeps its certificates in a separate persistent volume.

For a local TLS check, set `FERN_SITE_ADDRESS=localhost`.
Caddy uses its local certificate authority for that hostname.
Copy its public root certificate and verify the server with that certificate:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml \
  cp proxy:/data/caddy/pki/authorities/local/root.crt ./local-root.crt
curl --cacert ./local-root.crt https://localhost/healthz
```

Do not disable certificate verification. Public hostnames use public certificates automatically.

## The walkie-talkie room server

The `walkie` service runs the room server of the walkie-talkie app, `examples/android/walkie/server.fern`. It is in the `walkie` profile, so it starts only when you ask for it.
The server uses the certificate that Caddy keeps for `FERN_SITE_ADDRESS`, through a read-only mount of the proxy volume. Caddy keeps a P-256 key, which the server accepts.
Before it accepts new clients, the server reads the certificate again. A renewal by Caddy thus takes effect with no restart.

Permit inbound TCP port 47130 and UDP port 47130. The rooms use TCP, and the voice uses UDP. Point the hostname at this host directly: a proxy that passes only HTTP blocks the voice.

Build the image from the repository root:

```sh
bin/fern -target x86-64-linux -O -o examples/android/walkie/walkie-server examples/android/walkie/server.fern
docker build -t fern-walkie:latest examples/android/walkie
```

Start the services with the profile:

```sh
docker compose --env-file deploy/production.env -f compose.production.yaml --profile walkie up -d
```

Caddy must get the certificate before the room server can start. Until then, the room server stops, and Compose starts it again.
Caddy keeps each certificate in a directory for its issuer. `FERN_WALKIE_ISSUER` names that directory. When Caddy gets the certificate from ZeroSSL, set it to `acme.zerossl.com-v2-dv90`.

Set the `WALKIE_SERVER` repository variable to `FERN_SITE_ADDRESS`. The next nightly APK then shows the room buttons.

## Retained state

The app volume contains two directories:

| Directory | Retained data |
| --- | --- |
| `/data/worlds` | Blockyard block journals and compacted snapshots |
| `/data/projects` | Fernbase and Lantern commits, plus studio checkpoints |

Fernbase and Lantern retain each successful mutation before they confirm it.
Select Save checkpoint in Foundry, Patchbay, or Circuit to retain that project.
A studio checkpoint includes factory cargo, pause state, layouts, and processor programs.

Player positions, Gather rounds, Rally matches, Mosaic boards, and guestbook entries remain temporary.
A container restart ends live connections. Clients reconnect to the surviving data when the app returns.
Named volumes survive container replacement. `docker compose down --volumes` deletes them.

## Backup and restore

Run the backup helper from `examples/showcase`:

```sh
./deploy/backup.sh /absolute/backup/path/fern.tar.gz
```

The helper stops the app before it copies the data. It restarts an app that was active, including after a backup failure.
The proxy remains active during that interval. Requests can receive HTTP 503 until the app returns.
The helper refuses concurrent backups and existing archive names. It publishes an archive only after the copy succeeds.

Retain backups outside the server disk.

Restore a trusted backup into a new empty volume. Keep the current volume for rollback.
Set the archive path and restore volume name:

```sh
export FERN_RESTORE_VOLUME=fern-restored
docker volume create "$FERN_RESTORE_VOLUME"
docker run --rm --interactive --network none --entrypoint /bin/sh \
  --mount "type=volume,source=$FERN_RESTORE_VOLUME,target=/data,volume-nocopy" \
  caddy:2-alpine@sha256:eb4d35b7452d1ab6171fbdbe8d9275b2301dad6dad92115d91e19fb9b24d510f \
  -ec 'test -z "$(ls -A /data)"; tar -xzf - -C /data' < /absolute/backup/path/fern.tar.gz
docker compose --env-file deploy/production.env -f compose.production.yaml down
docker compose --env-file deploy/production.env -f compose.production.yaml \
  -f deploy/restore.volume.yaml up -d
```

The restore command refuses a nonempty volume. The Fern process validates its checkpoint checksums and journal records at startup.
Remove the restore override to return to the original volume. Stop the app before each volume change.
