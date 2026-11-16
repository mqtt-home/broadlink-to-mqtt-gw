//go:build windows

package broadlink

import "net"

func enableBroadcast(*net.UDPConn) error { return nil }
