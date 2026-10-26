#!/bin/sh
# Stop the single writer before copying its persistent volume.
set -eu
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
config=${FERN_DEPLOY_ENV:-$root/deploy/production.env}
compose() { docker compose --env-file "$config" -f "$root/compose.production.yaml" "$@"; }
command -v flock >/dev/null || { echo "Install flock before a backup." >&2; exit 1; }
if [ "$#" -ne 1 ]; then
  echo 'Usage: deploy/backup.sh /absolute/path/backup.tar.gz' >&2
  exit 2
fi
archive=$1
case "$archive" in *.tar.gz) ;; *) echo 'Use a .tar.gz archive.' >&2; exit 2 ;; esac
mkdir -p -- "$(dirname -- "$archive")"
output=$(CDPATH='' cd -- "$(dirname -- "$archive")" && pwd)
name=$(basename -- "$archive")
if [ -e "$output/$name" ]; then echo 'The archive already exists.' >&2; exit 1; fi
exec 9>"$root/deploy/.backup.lock"
flock -n 9 || { echo "Another backup holds the deployment lock." >&2; exit 1; }
container=$(compose ps -a -q app)
proxy=$(compose ps -a -q proxy)
[ -n "$container" ] && [ -n "$proxy" ] || { echo 'Create the deployment before a backup.' >&2; exit 1; }
volume=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}' "$container")
image=$(docker inspect -f '{{.Config.Image}}' "$proxy")
[ -n "$volume" ] || { echo 'The app has no named data volume.' >&2; exit 1; }
running=$(docker inspect -f '{{.State.Running}}' "$container")
temporary=$(mktemp "$output/.fern-backup.XXXXXX")
cleanup() {
  rm -f -- "$temporary"
  if [ "$running" = true ]; then compose start app >/dev/null; fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if [ "$running" = true ]; then compose stop app >/dev/null; fi
umask 077
docker run --rm --network none --entrypoint tar \
  --volume "$volume:/data:ro" "$image" -czf - -C /data . >"$temporary"
ln -- "$temporary" "$output/$name"
printf '%s\n' "$output/$name"
