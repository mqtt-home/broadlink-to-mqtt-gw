// Package broadlink implements the small subset of the Broadlink LAN protocol
// (UDP :80) that is needed to drive an IR remote such as the RM mini 3:
// hello/discovery, authentication, learn and send. It is a Go port of the
// relevant parts of python-broadlink.
package broadlink

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	// DefaultPort is the UDP port every Broadlink device listens on.
	DefaultPort = 80
	// DefaultTimeout bounds a single request/response exchange (with resends).
	DefaultTimeout = 5 * time.Second

	retryInterval = time.Second
	checksumSeed  = 0xBEAF

	initKeyHex = "097628343fe99e23765c1513accf8b02"
	ivHex      = "562e17996d093d28ddb3ba695a2e6f58"

	cmdAuth    = 0x65
	cmdCommand = 0x6A

	opUpdate        = 0x1
	opSendData      = 0x2
	opEnterLearning = 0x3
	opCheckData     = 0x4
)

// ErrTimeout is returned when the device does not answer in time.
var ErrTimeout = errors.New("broadlink: network timeout")

// Error is an error code reported by the device itself.
type Error struct{ Code int16 }

func (e *Error) Error() string {
	return fmt.Sprintf("broadlink: device error %d (0x%04x)", e.Code, uint16(e.Code))
}

// Info describes a device as reported by the hello / update exchange.
type Info struct {
	DevType uint16
	Model   string
	Name    string
	Locked  bool
	MAC     string
}

// Client talks to one Broadlink remote. Safe for concurrent use; exchanges
// are serialised.
type Client struct {
	Host    string        // IP or hostname, optional ":port"
	Timeout time.Duration // zero means DefaultTimeout

	mu      sync.Mutex
	hello   bool
	authed  bool
	devtype uint16
	framed  bool
	mac     []byte // raw bytes as they appear in the packet header
	count   uint16
	id      uint32
	key     []byte
	info    Info
}

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

func (c *Client) addr() string {
	if _, _, err := net.SplitHostPort(c.Host); err == nil {
		return c.Host
	}
	return net.JoinHostPort(c.Host, fmt.Sprint(DefaultPort))
}

// Info returns what is known about the device so far (after Connect).
func (c *Client) Info() Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// Connect performs hello + authentication. It is called implicitly by every
// other method, so calling it explicitly is only needed to get early errors.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ensureAuth(ctx)
}

// Update asks the device for its name and lock state (used as health check).
func (c *Client) Update(ctx context.Context) (Info, error) {
	resp, err := c.command(ctx, opUpdate, nil, true)
	if err != nil {
		return Info{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(resp) > 0x48 {
		name := resp[0x48:]
		for i, b := range name {
			if b == 0 {
				name = name[:i]
				break
			}
		}
		c.info.Name = string(name)
	}
	if len(resp) > 0x87 {
		c.info.Locked = resp[0x87] != 0
	}
	return c.info, nil
}

// SendData transmits a previously learned IR code.
func (c *Client) SendData(ctx context.Context, code []byte) error {
	if len(code) == 0 {
		return errors.New("broadlink: empty code")
	}
	_, err := c.command(ctx, opSendData, code, true)
	return err
}

// EnterLearning puts the device into IR learning mode.
func (c *Client) EnterLearning(ctx context.Context) error {
	_, err := c.command(ctx, opEnterLearning, nil, true)
	return err
}

// CheckData returns the last captured code. While nothing has been captured
// the device answers with an error code (*Error).
func (c *Client) CheckData(ctx context.Context) ([]byte, error) {
	return c.command(ctx, opCheckData, nil, false)
}

// Learn enters learning mode and waits until a code was captured or the
// timeout / context expires.
func (c *Client) Learn(ctx context.Context, wait time.Duration) ([]byte, error) {
	if err := c.EnterLearning(ctx); err != nil {
		return nil, fmt.Errorf("enter learning: %w", err)
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("learn timeout: no IR signal received")
		case <-tick.C:
		}
		data, err := c.CheckData(ctx)
		var devErr *Error
		switch {
		case err == nil && len(data) > 0:
			return data, nil
		case err == nil, errors.As(err, &devErr):
			// nothing captured yet
		default:
			return nil, fmt.Errorf("check data: %w", err)
		}
	}
}

// command runs one authenticated command. With retry, a device-side error or
// timeout triggers one re-authentication and a repeat of the command.
func (c *Client) command(ctx context.Context, op uint32, data []byte, retry bool) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	out, err := c.commandLocked(ctx, op, data)
	if err == nil || !retry || ctx.Err() != nil {
		return out, err
	}
	c.authed = false
	return c.commandLocked(ctx, op, data)
}

func (c *Client) commandLocked(ctx context.Context, op uint32, data []byte) ([]byte, error) {
	if err := c.ensureAuth(ctx); err != nil {
		return nil, err
	}

	var payload []byte
	if c.framed {
		payload = make([]byte, 6, 6+len(data))
		binary.LittleEndian.PutUint16(payload[0:], uint16(len(data)+4))
		binary.LittleEndian.PutUint32(payload[2:], op)
	} else {
		payload = make([]byte, 4, 4+len(data))
		binary.LittleEndian.PutUint32(payload, op)
	}
	payload = append(payload, data...)

	resp, err := c.exchange(ctx, cmdCommand, payload)
	if err != nil {
		return nil, err
	}
	plain, err := c.decrypt(resp[0x38:])
	if err != nil {
		return nil, err
	}
	if c.framed {
		if len(plain) < 6 {
			return nil, errors.New("broadlink: short response")
		}
		n := int(binary.LittleEndian.Uint16(plain[:2]))
		if n+2 > len(plain) || n < 4 {
			return nil, errors.New("broadlink: malformed response length")
		}
		return plain[6 : n+2], nil
	}
	if len(plain) < 4 {
		return nil, errors.New("broadlink: short response")
	}
	return plain[4:], nil
}

// ensureAuth runs hello (once) and authentication (when needed). Lock held.
func (c *Client) ensureAuth(ctx context.Context) error {
	if c.authed {
		return nil
	}
	if !c.hello {
		if err := c.doHello(ctx); err != nil {
			return err
		}
	}

	key, _ := hex.DecodeString(initKeyHex)
	c.id = 0
	c.key = key

	payload := make([]byte, 0x50)
	for i := 0x04; i < 0x14; i++ {
		payload[i] = 0x31
	}
	payload[0x1E] = 0x01
	payload[0x2D] = 0x01
	copy(payload[0x30:], "Test 1")

	resp, err := c.exchange(ctx, cmdAuth, payload)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	plain, err := c.decrypt(resp[0x38:])
	if err != nil || len(plain) < 0x14 {
		return fmt.Errorf("auth: malformed response")
	}
	c.id = binary.LittleEndian.Uint32(plain[:4])
	c.key = append([]byte(nil), plain[4:0x14]...)
	c.authed = true
	return nil
}

// doHello sends the unicast discovery packet and learns devtype and MAC.
func (c *Client) doHello(ctx context.Context) error {
	pkt := make([]byte, 0x30)
	now := time.Now()
	_, offset := now.Zone()
	putDatetime(pkt[0x08:0x14], now, offset/3600)
	pkt[0x26] = 6
	putChecksum(pkt, 0x20)

	resp, err := c.roundtrip(ctx, pkt)
	if err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if len(resp) < 0x40 {
		return errors.New("hello: short response")
	}

	c.devtype = binary.LittleEndian.Uint16(resp[0x34:0x36])
	c.mac = append([]byte(nil), resp[0x3A:0x40]...)
	spec, known := specs[c.devtype]
	if !known {
		spec = deviceSpec{model: fmt.Sprintf("Broadlink 0x%04x", c.devtype), framed: true}
	}
	c.framed = spec.framed

	name := resp[0x40:]
	for i, b := range name {
		if b == 0 {
			name = name[:i]
			break
		}
	}
	c.info = Info{
		DevType: c.devtype,
		Model:   spec.model,
		Name:    string(name),
		Locked:  len(resp) > 0x7F && resp[0x7F] != 0,
		MAC:     formatMAC(c.mac),
	}
	c.hello = true
	return nil
}

func formatMAC(raw []byte) string {
	b := make([]byte, 0, 17)
	for i := len(raw) - 1; i >= 0; i-- {
		if len(b) > 0 {
			b = append(b, ':')
		}
		b = append(b, fmt.Sprintf("%02x", raw[i])...)
	}
	return string(b)
}

// exchange builds, encrypts and sends one packet and validates the answer.
func (c *Client) exchange(ctx context.Context, typ uint16, payload []byte) ([]byte, error) {
	c.count = (c.count + 1) | 0x8000

	pkt := make([]byte, 0x38)
	copy(pkt, []byte{0x5a, 0xa5, 0xaa, 0x55, 0x5a, 0xa5, 0xaa, 0x55})
	binary.LittleEndian.PutUint16(pkt[0x24:], c.devtype)
	binary.LittleEndian.PutUint16(pkt[0x26:], typ)
	binary.LittleEndian.PutUint16(pkt[0x28:], c.count)
	copy(pkt[0x2A:0x30], c.mac)
	binary.LittleEndian.PutUint32(pkt[0x30:], c.id)

	var sum uint16 = checksumSeed
	for _, b := range payload {
		sum += uint16(b)
	}
	binary.LittleEndian.PutUint16(pkt[0x34:], sum)

	padded := append([]byte(nil), payload...)
	if pad := (16 - len(padded)%16) % 16; pad > 0 {
		padded = append(padded, make([]byte, pad)...)
	}
	enc, err := c.encrypt(padded)
	if err != nil {
		return nil, err
	}
	pkt = append(pkt, enc...)
	putChecksum(pkt, 0x20)

	resp, err := c.roundtrip(ctx, pkt)
	if err != nil {
		return nil, err
	}
	if len(resp) < 0x38 {
		return nil, fmt.Errorf("broadlink: response too short (%d bytes)", len(resp))
	}
	if code := int16(binary.LittleEndian.Uint16(resp[0x22:0x24])); code != 0 {
		return nil, &Error{Code: code}
	}
	return resp, nil
}

// roundtrip sends pkt and waits for one validated answer, resending every
// second until the timeout expires.
func (c *Client) roundtrip(ctx context.Context, pkt []byte) ([]byte, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", c.addr())
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(c.timeout())
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	buf := make([]byte, 2048)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		left := time.Until(deadline)
		if left <= 0 {
			return nil, ErrTimeout
		}
		if _, err := conn.Write(pkt); err != nil {
			return nil, err
		}
		_ = conn.SetReadDeadline(time.Now().Add(min(retryInterval, left)))
		n, err := conn.Read(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return nil, err
		}
		if n < 0x30 {
			return nil, fmt.Errorf("broadlink: response too short (%d bytes)", n)
		}
		resp := append([]byte(nil), buf[:n]...)
		if !validChecksum(resp) {
			return nil, errors.New("broadlink: response checksum mismatch")
		}
		return resp, nil
	}
}

func (c *Client) encrypt(p []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	iv, _ := hex.DecodeString(ivHex)
	out := make([]byte, len(p))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, p)
	return out, nil
}

func (c *Client) decrypt(p []byte) ([]byte, error) {
	if len(p) == 0 || len(p)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("broadlink: encrypted payload has invalid length %d", len(p))
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, err
	}
	iv, _ := hex.DecodeString(ivHex)
	out := make([]byte, len(p))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, p)
	return out, nil
}

// putChecksum writes the packet checksum at pos (the field itself must be
// zero while summing).
func putChecksum(pkt []byte, pos int) {
	var sum uint16 = checksumSeed
	for _, b := range pkt {
		sum += uint16(b)
	}
	binary.LittleEndian.PutUint16(pkt[pos:], sum)
}

func validChecksum(resp []byte) bool {
	var sum uint16 = checksumSeed
	for i, b := range resp {
		if i == 0x20 || i == 0x21 {
			continue
		}
		sum += uint16(b)
	}
	return sum == binary.LittleEndian.Uint16(resp[0x20:0x22])
}

// putDatetime packs a timestamp the way the hello packet expects it.
func putDatetime(dst []byte, t time.Time, utcOffsetHours int) {
	binary.LittleEndian.PutUint32(dst[0:], uint32(int32(utcOffsetHours)))
	binary.LittleEndian.PutUint16(dst[4:], uint16(t.Year()))
	dst[6] = byte(t.Minute())
	dst[7] = byte(t.Hour())
	dst[8] = byte(t.Year() % 100)
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	dst[9] = byte(wd)
	dst[10] = byte(t.Day())
	dst[11] = byte(t.Month())
}
