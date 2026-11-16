package broadlink

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestDiscoverFindsDevice(t *testing.T) {
	dev := newFakeDevice(t, 0x27DC, false)
	_, portStr, _ := net.SplitHostPort(dev.addr())
	port, _ := strconv.Atoi(portStr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := Discover(ctx, DiscoverOptions{Networks: []string{"127.0.0.1/32"}, Wait: 500 * time.Millisecond, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d devices, want 1: %+v", len(got), got)
	}
	if got[0].Host != "127.0.0.1" || !got[0].Supported || got[0].Info.Model != "RM mini 3" || got[0].Info.MAC == "" {
		t.Fatalf("unexpected result: %+v", got[0])
	}
}

func TestDiscoverNothingThere(t *testing.T) {
	got, err := Discover(context.Background(), DiscoverOptions{Networks: []string{"127.0.0.1/32"}, Wait: 200 * time.Millisecond, Port: 9})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want empty", got, err)
	}
}

func TestDiscoverRejectsBadNetworks(t *testing.T) {
	for _, n := range []string{"nonsense", "10.0.0.0/8", "::1/128"} {
		if _, err := Discover(context.Background(), DiscoverOptions{Networks: []string{n}, Wait: time.Millisecond}); err == nil {
			t.Errorf("%q: want error", n)
		}
	}
}

func TestDiscoverCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, DiscoverOptions{Wait: time.Second}); err == nil {
		t.Fatal("want error for cancelled context")
	}
}
