# One aioquic client for scripts/net-quic-interop: handshakes on ALPN
# fern-echo, sends SIZE bytes on a bidirectional stream and checks they come
# back with the stream's end, has a PING acknowledged, closes, and exits 0.
#   aioquic_client.py PORT CA MODE SIZE   (MODE: plain or chacha20)
# An IPv4 datagram endpoint is made by hand: aioquic's connect() opens an
# IPv6 socket, which a runner may lack.
import asyncio
import sys

from aioquic.asyncio.protocol import QuicConnectionProtocol
from aioquic.quic.configuration import QuicConfiguration
from aioquic.quic.connection import QuicConnection
from aioquic.quic.events import StreamDataReceived
from aioquic.tls import CipherSuite


class Collect(QuicConnectionProtocol):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.got = bytearray()
        self.ended = asyncio.get_running_loop().create_future()

    # Stream events stay out of the base class, whose stream reader for them
    # would write a second FIN when it is collected.
    def quic_event_received(self, event):
        if not isinstance(event, StreamDataReceived):
            super().quic_event_received(event)
            return
        self.got += event.data
        if event.end_stream and not self.ended.done():
            self.ended.set_result(None)


def payload(n):
    return bytes((i * 7 + 3) & 255 for i in range(n))


async def main():
    port, ca, mode, size = int(sys.argv[1]), sys.argv[2], sys.argv[3], int(sys.argv[4])
    cfg = QuicConfiguration(is_client=True, alpn_protocols=["fern-echo"], server_name="localhost")
    cfg.load_verify_locations(ca)
    if mode == "chacha20":
        cfg.cipher_suites = [CipherSuite.CHACHA20_POLY1305_SHA256]
    addr = ("127.0.0.1", port)
    conn = QuicConnection(configuration=cfg)
    loop = asyncio.get_running_loop()
    _, proto = await loop.create_datagram_endpoint(lambda: Collect(conn), remote_addr=addr)
    proto.connect(addr)
    await proto.wait_connected()
    print("aioquic client: connected alpn=%s" % conn.tls.alpn_negotiated, flush=True)
    sid = conn.get_next_available_stream_id()
    conn.send_stream_data(sid, payload(size), end_stream=True)
    proto.transmit()
    await proto.ended
    print("aioquic client: echo of %d bytes %s" % (len(proto.got), "matches" if bytes(proto.got) == payload(size) else "DIFFERS"), flush=True)
    await proto.ping()
    print("aioquic client: ping answered", flush=True)
    proto.close(error_code=0, reason_phrase="aioquic done")
    await proto.wait_closed()


asyncio.run(asyncio.wait_for(main(), 30))
