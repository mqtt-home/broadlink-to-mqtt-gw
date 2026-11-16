#!/usr/bin/env python3
"""Bringt einen Broadlink RM mini 3 per AP-Modus ins WLAN.

Ablauf:
  1. RM mini 3 in den AP-Modus bringen (Reset-Taste ~6 s halten, bis die LED
     schnell blinkt; ggf. nochmal halten bis sie langsam blinkt).
  2. Mac mit dem WLAN "BroadlinkProv" verbinden (offen, ohne Passwort).
  3. Dieses Skript ausführen:
        .venv/bin/python wifi_setup.py "<SSID>" "<Passwort>" [modus]
     modus: 0=offen 1=WEP 2=WPA1 3=WPA2 (Default) 4=WPA1/2
  4. Mac wieder ins normale WLAN, dann: .venv/bin/python wifi_setup.py --discover
"""
import sys
import broadlink

if sys.argv[1:] == ["--discover"]:
    devs = broadlink.discover(timeout=10)
    if not devs:
        print("Nichts gefunden (Mac im selben Segment? Gerät im Router sichtbar?)")
    for d in devs:
        print(d.type, hex(d.devtype), d.host[0], d.mac.hex(":"))
    sys.exit(0)

if len(sys.argv) < 3:
    sys.exit(__doc__)

ssid, password = sys.argv[1], sys.argv[2]
mode = int(sys.argv[3]) if len(sys.argv) > 3 else 3

# Payload von python-broadlink erzeugen lassen, aber selbst senden:
# gebunden an die WLAN-Schnittstelle (IP_BOUND_IF), damit das Paket nicht
# über das Kabel-LAN rausgeht. Ziele: Broadcast + AP-Modus-Adresse 192.168.0.1.
import socket

IFACE = "en1"  # WLAN-Schnittstelle des Macs (networksetup -listallhardwareports)
captured = []


class _Fake:
    def __init__(self, *a): pass
    def setsockopt(self, *a): pass
    def sendto(self, data, addr): captured.append(bytes(data))
    def close(self): pass


_real = socket.socket
socket.socket = _Fake
try:
    broadlink.setup(ssid, password, mode)
finally:
    socket.socket = _real

sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
sock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
sock.setsockopt(socket.IPPROTO_IP, 25, socket.if_nametoindex(IFACE))  # IP_BOUND_IF
for target in ("255.255.255.255", "192.168.0.255", "192.168.0.1", "192.168.10.255", "192.168.10.1"):
    for _ in range(3):
        try:
            sock.sendto(captured[0], (target, 80))
        except OSError as e:
            print(f"  {target}: {e}")
sock.close()
print("Zugangsdaten gesendet. Gerät braucht ~30 s zum Verbinden (LED hört auf zu blinken).")
