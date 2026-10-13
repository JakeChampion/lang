package e2eharness

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

// H2CClient is the least HTTP/2 client a gate needs over a plain
// connection (RFC 9113, prior knowledge): the connection preface, the
// peer's SETTINGS acknowledged, a GET as a HEADERS frame whose fields are
// HPACK static-table entries and literals (`Open`), and the response's
// DATA read back until END_STREAM (`Await`), the connection window topped
// up as it is consumed; `Get` is the two in turn. Streams may be open at
// once: what arrives on one while another is awaited is kept for its own
// Await. It decodes no response header block: a gate that counts or times
// requests reads the body.
type H2CClient struct {
	conn   net.Conn
	next   uint32
	window int64
	bodies map[uint32][]byte
	ended  map[uint32]bool
}

// DialH2C opens addr and sends the preface and an empty SETTINGS frame,
// then reads the server's SETTINGS and acknowledges it.
func DialH2C(addr string, timeout time.Duration) (*H2CClient, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	c := &H2CClient{conn: conn, next: 1, bodies: map[uint32][]byte{}, ended: map[uint32]bool{}}
	if _, err := conn.Write([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")); err != nil {
		return nil, err
	}
	if err := c.frame(4, 0, 0, nil); err != nil {
		return nil, err
	}
	return c, nil
}

// Close closes the connection.
func (c *H2CClient) Close() error { return c.conn.Close() }

// SetDeadline bounds every read and write.
func (c *H2CClient) SetDeadline(t time.Time) { _ = c.conn.SetDeadline(t) }

func (c *H2CClient) frame(typ, flags byte, stream uint32, payload []byte) error {
	head := make([]byte, 9, 9+len(payload))
	head[0], head[1], head[2] = byte(len(payload)>>16), byte(len(payload)>>8), byte(len(payload))
	head[3], head[4] = typ, flags
	binary.BigEndian.PutUint32(head[5:], stream&0x7fffffff)
	_, err := c.conn.Write(append(head, payload...))
	return err
}

// Get sends GET path with `localhost` as the authority on a new stream
// and answers the response body once the stream has ended.
func (c *H2CClient) Get(path string) (string, error) {
	stream, err := c.Open(path)
	if err != nil {
		return "", err
	}
	return c.Await(stream)
}

// Open sends GET path with `localhost` as the authority on a new stream
// and answers the stream, whose response Await reads.
func (c *H2CClient) Open(path string) (uint32, error) {
	stream := c.next
	c.next += 2
	// :method GET (static 2), :scheme http (static 6), :path as a literal
	// with the indexed name :path (static 4), :authority (static 1).
	block := []byte{0x82, 0x86, 0x04, byte(len(path))}
	block = append(block, path...)
	block = append(block, 0x01, 9)
	block = append(block, "localhost"...)
	return stream, c.frame(1, 0x05, stream, block)
}

// Await reads frames until the response on stream has ended and answers
// its body; frames for other open streams are kept for their own Await.
func (c *H2CClient) Await(stream uint32) (string, error) {
	for !c.ended[stream] {
		if err := c.readFrame(); err != nil {
			return "", err
		}
	}
	body := string(c.bodies[stream])
	delete(c.bodies, stream)
	delete(c.ended, stream)
	return body, nil
}

// readFrame reads one frame: a stream's DATA or final HEADERS is kept
// against it, SETTINGS and PING are answered, RST_STREAM and GOAWAY end
// the read with their reason.
func (c *H2CClient) readFrame() error {
	{
		head := make([]byte, 9)
		if _, err := io.ReadFull(c.conn, head); err != nil {
			return err
		}
		length := int(head[0])<<16 | int(head[1])<<8 | int(head[2])
		typ, flags := head[3], head[4]
		id := binary.BigEndian.Uint32(head[5:]) & 0x7fffffff
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.conn, payload); err != nil {
			return err
		}
		switch typ {
		case 0: // DATA
			if flags&0x08 != 0 && len(payload) > 0 {
				pad := int(payload[0])
				payload = payload[1 : len(payload)-pad]
			}
			c.bodies[id] = append(c.bodies[id], payload...)
			c.window += int64(length)
			if c.window >= 32768 {
				inc := make([]byte, 4)
				binary.BigEndian.PutUint32(inc, uint32(c.window))
				if err := c.frame(8, 0, 0, inc); err != nil {
					return err
				}
				c.window = 0
			}
			if flags&0x01 != 0 {
				c.ended[id] = true
			}
		case 1: // HEADERS
			if flags&0x01 != 0 {
				c.ended[id] = true
			}
		case 3: // RST_STREAM
			return fmt.Errorf("stream %d reset with code %d", id, binary.BigEndian.Uint32(payload))
		case 4: // SETTINGS
			if flags&0x01 == 0 {
				if err := c.frame(4, 0x01, 0, nil); err != nil {
					return err
				}
			}
		case 6: // PING
			if flags&0x01 == 0 {
				if err := c.frame(6, 0x01, 0, payload); err != nil {
					return err
				}
			}
		case 7: // GOAWAY
			return fmt.Errorf("GOAWAY code %d: %s", binary.BigEndian.Uint32(payload[4:8]), payload[8:])
		case 8, 9: // WINDOW_UPDATE, CONTINUATION (the body is what is read)
		default:
		}
	}
	return nil
}
