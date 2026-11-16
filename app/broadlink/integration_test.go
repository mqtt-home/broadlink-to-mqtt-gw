package broadlink

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRealDevice talks to a real remote. Skipped unless BROADLINK_HOST is set:
//
//	BROADLINK_HOST=10.0.10.211 go test ./broadlink -run RealDevice -v
//
// With BROADLINK_LEARN=1 it additionally learns one IR code (press a button
// on a remote within 20 s) and sends it back.
func TestRealDevice(t *testing.T) {
	host := os.Getenv("BROADLINK_HOST")
	if host == "" {
		t.Skip("BROADLINK_HOST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c := &Client{Host: host}
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Logf("info after hello: %+v", c.Info())

	info, err := c.Update(ctx)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	t.Logf("info after update: %+v", info)

	if os.Getenv("BROADLINK_LEARN") == "" {
		return
	}
	t.Log("press a remote button now (20 s)")
	code, err := c.Learn(ctx, 20*time.Second)
	if err != nil {
		t.Fatalf("learn: %v", err)
	}
	t.Logf("learned %d bytes: %x", len(code), code)
	if err := c.SendData(ctx, code); err != nil {
		t.Fatalf("send: %v", err)
	}
}
