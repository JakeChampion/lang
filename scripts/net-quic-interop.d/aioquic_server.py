# One aioquic server for scripts/net-quic-interop: completes handshakes on
# ALPN fern-echo, sends each stream's data back as it arrives, and prints
# each handshake and each termination.
#   aioquic_server.py PORT CERT KEY MODE   (MODE: plain, retry or chacha20)
import asyncio
import sys

from aioquic.asyncio import serve
from aioquic.asyncio.protocol import QuicConnectionProtocol
from aioquic.quic.configuration import QuicConfiguration
from aioquic.quic.events import ConnectionTerminated, HandshakeCompleted, StreamDataReceived
from aioquic.tls import CipherSuite


class Echo(QuicConnectionProtocol):
    def quic_event_received(self, event):
        if isinstance(event, HandshakeCompleted):
            print("aioquic server: handshake completed alpn=%s" % event.alpn_protocol, flush=True)
        elif isinstance(event, StreamDataReceived):
            self._quic.send_stream_data(event.stream_id, event.data, end_stream=event.end_stream)
            self.transmit()
        elif isinstance(event, ConnectionTerminated):
            print("aioquic server: terminated code=%d reason=%r" % (event.error_code, event.reason_phrase), flush=True)


async def main():
    port, cert, key, mode = int(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4]
    cfg = QuicConfiguration(is_client=False, alpn_protocols=["fern-echo"])
    cfg.load_cert_chain(cert, key)
    if mode == "chacha20":
        cfg.cipher_suites = [CipherSuite.CHACHA20_POLY1305_SHA256]
    await serve("127.0.0.1", port, configuration=cfg, create_protocol=Echo, retry=mode == "retry")
    print("aioquic server: listening", flush=True)
    await asyncio.sleep(60)


asyncio.run(main())
