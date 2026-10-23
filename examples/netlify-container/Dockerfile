# The compiler runs on the build machine's own architecture and
# cross-compiles, so a build on Apple Silicon does not emulate it.
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS build
ARG FERN_RELEASE=https://github.com/JakeChampion/lang/releases/download/nightly
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN case "$(uname -m)" in x86_64) arch=x86_64 ;; aarch64) arch=arm64 ;; *) echo "no fern release for $(uname -m)" >&2; exit 1 ;; esac \
 && curl -fsSLO "$FERN_RELEASE/fern-linux-$arch.tar.gz" \
 && curl -fsSLO "$FERN_RELEASE/fern-linux-$arch.tar.gz.sha256" \
 && sha256sum -c "fern-linux-$arch.tar.gz.sha256" \
 && tar xzf "fern-linux-$arch.tar.gz" -C /usr/local/bin
COPY server.fern .
RUN fern -target x86-64-linux -O -o /server server.fern

# The server is a static binary, so the image holds nothing else.
FROM scratch
COPY --from=build /server /server
EXPOSE 8080
CMD ["/server"]
