package native

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Thunderbird accepts at most 1 MB per message from the host, but may send far larger ones.
const (
	MaxMessageBytes        = 1024 * 1024
	MaxInboundMessageBytes = 64 * 1024 * 1024
)

// Conn speaks Mozilla/Thunderbird 4-byte little-endian length-prefixed JSON over stdio.
type Conn struct {
	r  io.Reader
	w  io.Writer
	mu sync.Mutex
}

func NewConn(r io.Reader, w io.Writer) *Conn {
	return &Conn{r: r, w: w}
}

func (c *Conn) Read(dst any) error {
	var lenBuf [4]byte
	if _, err := io.ReadFull(c.r, lenBuf[:]); err != nil {
		return err
	}
	length := binary.LittleEndian.Uint32(lenBuf[:])
	if length == 0 || length > MaxInboundMessageBytes {
		return fmt.Errorf("invalid native message length: %d", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		return err
	}
	return json.Unmarshal(payload, dst)
}

func (c *Conn) Write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > MaxMessageBytes {
		return fmt.Errorf("outbound native message exceeds %d bytes", MaxMessageBytes)
	}
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(data)))

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err = c.w.Write(data)
	return err
}
