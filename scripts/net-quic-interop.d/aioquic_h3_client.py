# An aioquic HTTP/3 client for scripts/net-quic-interop: a GET and a POST
# of SIZE bytes on one connection, each response's status, body and
# trailers printed, then a close.
#   aioquic_h3_client.py PORT CA SIZE
# An IPv4 datagram endpoint is made by hand: aioquic's connect() opens an
# IPv6 socket, which a runner may lack.
import asyncio
import sys

from aioquic.asyncio.protocol import QuicConnectionProtocol
from aioquic.h3.connection import H3_ALPN, H3Connection
from aioquic.h3.events import DataReceived, HeadersReceived
from aioquic.quic.configuration import QuicConfiguration
from aioquic.quic.connection import QuicConnection
from aioquic.quic.events import StreamDataReceived


class H3(QuicConnectionProtocol):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.h3 = H3Connection(self._quic)
        self.waiting = {}

    # Stream events stay out of the base class, whose stream reader for them
    # would write a second FIN when it is collected.
    def quic_event_received(self, event):
        if not isinstance(event, StreamDataReceived):
            super().quic_event_received(event)
        for e in self.h3.handle_event(event):
            entry = self.waiting.get(e.stream_id)
            if entry is None:
                continue
            if isinstance(e, HeadersReceived):
                entry[1].append(dict(e.headers))
            elif isinstance(e, DataReceived):
                entry[2] += e.data
            if e.stream_ended and not entry[0].done():
                entry[0].set_result(None)

    async def fetch(self, method, path, body):
        sid = self._quic.get_next_available_stream_id()
        entry = [asyncio.get_running_loop().create_future(), [], bytearray()]
        self.waiting[sid] = entry
        head = [(b":method", method), (b":scheme", b"https"), (b":authority", b"localhost"), (b":path", path)]
        self.h3.send_headers(sid, head, end_stream=not body)
        if body:
            self.h3.send_data(sid, body, end_stream=True)
        self.transmit()
        await entry[0]
        return entry[1], bytes(entry[2])


async def main():
    port, ca, size = int(sys.argv[1]), sys.argv[2], int(sys.argv[3])
    cfg = QuicConfiguration(is_client=True, alpn_protocols=H3_ALPN, server_name="localhost")
    cfg.load_verify_locations(ca)
    addr = ("127.0.0.1", port)
    conn = QuicConnection(configuration=cfg)
    loop = asyncio.get_running_loop()
    _, proto = await loop.create_datagram_endpoint(lambda: H3(conn), remote_addr=addr)
    proto.connect(addr)
    await proto.wait_connected()
    for method, path, body in ((b"GET", b"/hello", b""), (b"POST", b"/upload", b"x" * size)):
        heads, data = await proto.fetch(method, path, body)
        trailers = " ".join("%s=%s" % (k.decode(), v.decode()) for h in heads[1:] for k, v in h.items())
        print("aioquic h3 client: %s %s -> %s %s | %s" % (method.decode(), path.decode(), heads[0][b":status"].decode(), data.decode(), trailers), flush=True)
    proto.close(error_code=0x100, reason_phrase="done")
    await proto.wait_closed()


asyncio.run(asyncio.wait_for(main(), 30))
