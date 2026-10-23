#!/bin/sh
# Builds the container image and saves it where Netlify finds functions.
# CONTAINER_TOOL=docker uses Docker in place of Podman.
set -eu
tool="${CONTAINER_TOOL:-podman}"
cd "$(dirname "$0")"
mkdir -p netlify/functions public
"$tool" build --platform linux/amd64 -t fern-server .
rm -f netlify/functions/fern-server.tar
case "$tool" in
  podman) "$tool" save --format docker-archive -o netlify/functions/fern-server.tar fern-server ;;
  *) "$tool" save -o netlify/functions/fern-server.tar fern-server ;;
esac
