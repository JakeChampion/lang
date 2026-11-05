# The room server of the walkie-talkie. Build the static binary first, from
# the repository root:
#   bin/fern -target x86-64-linux -O -o examples/android/walkie/walkie-server examples/android/walkie/server.fern
FROM scratch
COPY walkie-server /walkie-server
EXPOSE 47130/tcp 47130/udp
ENTRYPOINT ["/walkie-server"]
