package broadlink

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeDevice is a minimal Broadlink remote answering hello, auth and the
// update / enter-learning / check-data / send-data commands over UDP.
type fakeDevice struct {
	t       *testing.T
	conn    *net.UDPConn
	devtype uint16
	framed  bool
	mac     []byte
	session []byte
	id      uint32

	mu       sync.Mutex
	sent     [][]byte
	learning bool
	captured []byte
	authCnt  int
	failNext bool // answer the next command with an error code
}

func newFakeDevice(t *testing.T, devtype uint16, framed bool) *fakeDevice {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDevice{
		t: t, conn: conn, devtype: devtype, framed: framed,
		mac:     []byte{0x97, 0x19, 0x63, 0x42, 0xf7, 0xc8}, // c8:f7:42:63:19:97 reversed
		session: []byte("0123456789abcdef"),
		id:      0xCAFE,
	}
	t.Cleanup(func() { _ = conn.Close() })
	go f.serve()
	return f
}

func (f *fakeDevice) addr() string { return f.conn.LocalAddr().String() }

func (f *fakeDevice) serve() {
	buf := make([]byte, 2048)
	for {
		n, from, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req := append([]byte(nil), buf[:n]...)
		if resp := f.handle(req); resp != nil {
			_, _ = f.conn.WriteToUDP(resp, from)
		}
	}
}

func (f *fakeDevice) reply(typ uint16, errCode int16, payload []byte, key []byte) []byte {
	pkt := make([]byte, 0x38)
	binary.LittleEndian.PutUint16(pkt[0x22:], uint16(errCode))
	binary.LittleEndian.PutUint16(pkt[0x26:], typ)
	if payload != nil {
		c := &Client{key: key}
		if pad := (16 - len(payload)%16) % 16; pad > 0 {
			payload = append(payload, make([]byte, pad)...)
		}
		enc, _ := c.encrypt(payload)
		pkt = append(pkt, enc...)
	}
	putChecksum(pkt, 0x20)
	return pkt
}

func (f *fakeDevice) handle(req []byte) []byte {
	if len(req) == 0x30 && req[0x26] == 6 { // hello
		pkt := make([]byte, 0x80)
		binary.LittleEndian.PutUint16(pkt[0x34:], f.devtype)
		copy(pkt[0x3A:], f.mac)
		copy(pkt[0x40:], "Living Room")
		putChecksum(pkt, 0x20)
		return pkt
	}
	if len(req) < 0x38 {
		return nil
	}
	typ := binary.LittleEndian.Uint16(req[0x26:])
	// The client must put the raw MAC and its devtype into every packet.
	if string(req[0x2A:0x30]) != string(f.mac) || binary.LittleEndian.Uint16(req[0x24:]) != f.devtype {
		f.t.Errorf("bad header: mac=%x devtype=%x", req[0x2A:0x30], req[0x24:0x26])
		return nil
	}
	switch typ {
	case cmdAuth:
		init, _ := hexKey(initKeyHex)
		plain, _ := (&Client{key: init}).decrypt(req[0x38:])
		if string(plain[0x30:0x36]) != "Test 1" {
			f.t.Errorf("auth payload wrong: %x", plain)
		}
		f.mu.Lock()
		f.authCnt++
		f.mu.Unlock()
		out := make([]byte, 0x14)
		binary.LittleEndian.PutUint32(out, f.id)
		copy(out[4:], f.session)
		return f.reply(cmdAuth, 0, out, init)
	case cmdCommand:
		if binary.LittleEndian.Uint32(req[0x30:]) != f.id {
			f.t.Errorf("wrong session id")
		}
		plain, err := (&Client{key: f.session}).decrypt(req[0x38:])
		if err != nil {
			f.t.Errorf("decrypt: %v", err)
			return nil
		}
		return f.command(plain)
	}
	return nil
}

func (f *fakeDevice) command(plain []byte) []byte {
	var op uint32
	var data []byte
	if f.framed {
		n := int(binary.LittleEndian.Uint16(plain))
		op = binary.LittleEndian.Uint32(plain[2:])
		data = plain[6 : n+2]
	} else {
		op = binary.LittleEndian.Uint32(plain)
		data = plain[4:]
		// Trailing zero padding cannot be told apart from data without a
		// length prefix; the send test uses codes that cope with that.
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		f.failNext = false
		return f.reply(cmdCommand, -1, nil, f.session)
	}
	var body []byte
	switch op {
	case opUpdate:
		body = make([]byte, 0x90)
		copy(body[0x48:], "Living Room")
		body[0x87] = 1
	case opEnterLearning:
		f.learning = true
	case opCheckData:
		if f.captured == nil {
			return f.reply(cmdCommand, -5, nil, f.session)
		}
		body = f.captured
	case opSendData:
		f.sent = append(f.sent, append([]byte(nil), data...))
	}
	return f.reply(cmdCommand, 0, f.wrap(body), f.session)
}

func (f *fakeDevice) wrap(body []byte) []byte {
	if f.framed {
		out := make([]byte, 6+len(body))
		binary.LittleEndian.PutUint16(out, uint16(len(body)+4))
		copy(out[6:], body)
		return out
	}
	return append(make([]byte, 4), body...)
}

func hexKey(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := range out {
		var b byte
		for _, ch := range s[2*i : 2*i+2] {
			b <<= 4
			switch {
			case ch >= '0' && ch <= '9':
				b |= byte(ch - '0')
			default:
				b |= byte(ch-'a') + 10
			}
		}
		out[i] = b
	}
	return out, nil
}

func TestClientAgainstFakeDevice(t *testing.T) {
	for _, tc := range []struct {
		name    string
		devtype uint16
		framed  bool
		model   string
	}{
		{"legacy RM mini 3", 0x2737, false, "RM mini 3"},
		{"framed RM mini 3", 0x5F36, true, "RM mini 3"},
		{"unknown type defaults to framed", 0x7777, true, "Broadlink 0x7777"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dev := newFakeDevice(t, tc.devtype, tc.framed)
			c := &Client{Host: dev.addr(), Timeout: 2 * time.Second}
			ctx := context.Background()

			if err := c.Connect(ctx); err != nil {
				t.Fatalf("connect: %v", err)
			}
			info := c.Info()
			if info.Model != tc.model || info.MAC != "c8:f7:42:63:19:97" || info.Name != "Living Room" || info.DevType != tc.devtype {
				t.Errorf("info = %+v", info)
			}

			info, err := c.Update(ctx)
			if err != nil || info.Name != "Living Room" || !info.Locked {
				t.Fatalf("update = %+v, %v", info, err)
			}

			// Code length is a multiple of 16 minus nothing — use a length
			// that survives zero padding on legacy framing.
			code := []byte{0x26, 0x00, 0x04, 0x00, 0x11, 0x22, 0x33, 0x44}
			if err := c.SendData(ctx, code); err != nil {
				t.Fatalf("send: %v", err)
			}
			dev.mu.Lock()
			got := dev.sent[0]
			dev.mu.Unlock()
			if string(got[:len(code)]) != string(code) {
				t.Errorf("device received %x, want prefix %x", got, code)
			}
			if tc.framed && len(got) != len(code) {
				t.Errorf("framed device must see exact length, got %d", len(got))
			}
		})
	}
}

func TestLearnFlow(t *testing.T) {
	dev := newFakeDevice(t, 0x5F36, true)
	c := &Client{Host: dev.addr(), Timeout: 2 * time.Second}

	go func() {
		time.Sleep(1500 * time.Millisecond)
		dev.mu.Lock()
		dev.captured = []byte{0x26, 0x00, 0x01, 0x00, 0xAA}
		dev.mu.Unlock()
	}()
	code, err := c.Learn(context.Background(), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	dev.mu.Lock()
	learning := dev.learning
	dev.mu.Unlock()
	if string(code) != "\x26\x00\x01\x00\xAA" || !learning {
		t.Errorf("code = %x learning=%v", code, learning)
	}
}

func TestLearnTimeout(t *testing.T) {
	dev := newFakeDevice(t, 0x5F36, true)
	c := &Client{Host: dev.addr(), Timeout: 2 * time.Second}
	if _, err := c.Learn(context.Background(), 1500*time.Millisecond); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestReauthAfterDeviceError(t *testing.T) {
	dev := newFakeDevice(t, 0x5F36, true)
	c := &Client{Host: dev.addr(), Timeout: 2 * time.Second}
	ctx := context.Background()
	if _, err := c.Update(ctx); err != nil {
		t.Fatal(err)
	}
	dev.mu.Lock()
	dev.failNext = true
	dev.mu.Unlock()
	if _, err := c.Update(ctx); err != nil {
		t.Fatalf("update should recover via re-auth: %v", err)
	}
	dev.mu.Lock()
	defer dev.mu.Unlock()
	if dev.authCnt != 2 {
		t.Errorf("auth count = %d, want 2", dev.authCnt)
	}
}

func TestTimeoutWhenNobodyAnswers(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	c := &Client{Host: conn.LocalAddr().String(), Timeout: 1200 * time.Millisecond}
	err = c.Connect(context.Background())
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

func TestContextCancel(t *testing.T) {
	conn, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	defer func() { _ = conn.Close() }()
	c := &Client{Host: conn.LocalAddr().String(), Timeout: 30 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := c.Connect(ctx); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}

func TestSendEmptyCode(t *testing.T) {
	if err := (&Client{Host: "127.0.0.1:1"}).SendData(context.Background(), nil); err == nil {
		t.Fatal("expected error")
	}
}
