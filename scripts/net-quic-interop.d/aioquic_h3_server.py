# An aioquic HTTP/3 server for scripts/net-quic-interop: answers every
# request with 200, a body naming its method and path and how many body
# bytes it carried, and a trailer.
#   aioquic_h3_server.py PORT CERT KEY
import asyncio
import sys

from aioquic.asyncio import serve
from aioquic.asyncio.protocol import QuicConnectionProtocol
from aioquic.h3.connection import H3_ALPN, H3Connection
from aioquic.h3.events import DataReceived, HeadersReceived
from aioquic.quic.configuration import QuicConfiguration
from aioquic.quic.events import ProtocolNegotiated


class H3(QuicConnectionProtocol):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.h3 = None
        self.requests = {}

    def quic_event_received(self, event):
        if isinstance(event, ProtocolNegotiated):
            self.h3 = H3Connection(self._quic)
        if self.h3 is None:
            return
        for e in self.h3.handle_event(event):
            self.h3_event(e)

    def h3_event(self, e):
        if isinstance(e, HeadersReceived):
            self.requests[e.stream_id] = [dict(e.headers), 0]
            ended = e.stream_ended
        elif isinstance(e, DataReceived):
            self.requests[e.stream_id][1] += len(e.data)
            ended = e.stream_ended
        else:
            return
        if not ended:
            return
        head, size = self.requests.pop(e.stream_id)
        body = ("%s %s %d" % (head[b":method"].decode(), head[b":path"].decode(), size)).encode()
        print("aioquic h3 server: answered %s" % body.decode(), flush=True)
        self.h3.send_headers(e.stream_id, [(b":status", b"200"), (b"content-type", b"text/plain"), (b"content-length", str(len(body)).encode())])
        self.h3.send_data(e.stream_id, body, end_stream=False)
        self.h3.send_headers(e.stream_id, [(b"x-served-by", b"aioquic")], end_stream=True)
        self.transmit()


async def main():
    port, cert, key = int(sys.argv[1]), sys.argv[2], sys.argv[3]
    cfg = QuicConfiguration(is_client=False, alpn_protocols=H3_ALPN)
    cfg.load_cert_chain(cert, key)
    await serve("127.0.0.1", port, configuration=cfg, create_protocol=H3)
    print("aioquic h3 server: listening", flush=True)
    await asyncio.sleep(60)


asyncio.run(main())
