# The compiler runs on the build machine's own architecture and
# cross-compiles, so a build on Apple Silicon does not emulate it.
# Alpine needs no package install: busybox wget reads https and has
# sha256sum. An apt-get install fails under rootless podman with one UID.
FROM --platform=$BUILDPLATFORM alpine:3.20 AS build
ARG FERN_RELEASE=https://github.com/JakeChampion/lang/releases/download/nightly
WORKDIR /src
RUN case "$(uname -m)" in x86_64) arch=x86_64 ;; aarch64) arch=arm64 ;; *) echo "no fern release for $(uname -m)" >&2; exit 1 ;; esac \
 && wget -q "$FERN_RELEASE/fern-linux-$arch.tar.gz" "$FERN_RELEASE/fern-linux-$arch.tar.gz.sha256" \
 && sha256sum -c "fern-linux-$arch.tar.gz.sha256" \
 && tar xzf "fern-linux-$arch.tar.gz" -C /usr/local/bin
COPY server.fern .
RUN fern -target x86-64-linux -O -o /server server.fern

# The server is a static binary, so the image holds nothing else.
FROM scratch
COPY --from=build /server /server
EXPOSE 8080
CMD ["/server"]
