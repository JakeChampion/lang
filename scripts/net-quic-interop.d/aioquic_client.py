# One aioquic client for scripts/net-quic-interop: handshakes on ALPN h3,
# sends a PING and waits for its acknowledgement, closes, and exits 0.
#   aioquic_client.py PORT CA MODE   (MODE: plain or chacha20)
# An IPv4 datagram endpoint is made by hand: aioquic's connect() opens an
# IPv6 socket, which a runner may lack.
import asyncio
import sys

from aioquic.asyncio.protocol import QuicConnectionProtocol
from aioquic.quic.configuration import QuicConfiguration
from aioquic.quic.connection import QuicConnection
from aioquic.tls import CipherSuite


async def main():
    port, ca, mode = int(sys.argv[1]), sys.argv[2], sys.argv[3]
    cfg = QuicConfiguration(is_client=True, alpn_protocols=["h3"], server_name="localhost")
    cfg.load_verify_locations(ca)
    if mode == "chacha20":
        cfg.cipher_suites = [CipherSuite.CHACHA20_POLY1305_SHA256]
    addr = ("127.0.0.1", port)
    conn = QuicConnection(configuration=cfg)
    loop = asyncio.get_running_loop()
    _, proto = await loop.create_datagram_endpoint(lambda: QuicConnectionProtocol(conn), remote_addr=addr)
    proto.connect(addr)
    await proto.wait_connected()
    print("aioquic client: connected alpn=%s" % conn.tls.alpn_negotiated, flush=True)
    await proto.ping()
    print("aioquic client: ping answered", flush=True)
    proto.close(error_code=0, reason_phrase="aioquic done")
    await proto.wait_closed()


asyncio.run(asyncio.wait_for(main(), 15))
