package broadlink

// deviceSpec describes what the bridge needs to know about a device type.
type deviceSpec struct {
	model string
	// framed is true for newer firmware / RM4 generation devices that wrap
	// command payloads in a 2-byte length prefix (python-broadlink: rmminib,
	// rm4mini, rm4pro). Older RM mini 3 devices (e.g. 0x2737) do not.
	framed bool
}

// specs lists the IR-capable remotes ported from python-broadlink's
// SUPPORTED_TYPES (rmmini, rmpro, rmminib, rm4mini, rm4pro).
var specs = map[uint16]deviceSpec{
	// rmmini
	0x2737: {"RM mini 3", false},
	0x278F: {"RM mini", false},
	0x27B7: {"RM mini 3", false},
	0x27C2: {"RM mini 3", false},
	0x27C7: {"RM mini 3", false},
	0x27CC: {"RM mini 3", false},
	0x27CD: {"RM mini 3", false},
	0x27D0: {"RM mini 3", false},
	0x27D1: {"RM mini 3", false},
	0x27D3: {"RM mini 3", false},
	0x27DC: {"RM mini 3", false},
	0x27DE: {"RM mini 3", false},
	// rmpro (legacy framing)
	0x2712: {"RM pro/pro+", false},
	0x272A: {"RM pro", false},
	0x273D: {"RM pro", false},
	0x277C: {"RM home", false},
	0x2783: {"RM home", false},
	0x2787: {"RM pro", false},
	0x278B: {"RM plus", false},
	0x2797: {"RM pro+", false},
	0x279D: {"RM pro+", false},
	0x27A1: {"RM plus", false},
	0x27A6: {"RM plus", false},
	0x27A9: {"RM pro+", false},
	0x27C3: {"RM pro+", false},
	// rmminib
	0x5F36: {"RM mini 3", true},
	0x6507: {"RM mini 3", true},
	0x6508: {"RM mini 3", true},
	// rm4mini
	0x51DA: {"RM4 mini", true},
	0x5209: {"RM4 TV mate", true},
	0x520C: {"RM4 mini", true},
	0x520D: {"RM4C mini", true},
	0x5211: {"RM4C mate", true},
	0x5212: {"RM4 TV mate", true},
	0x5216: {"RM4 mini", true},
	0x521C: {"RM4 mini", true},
	0x6070: {"RM4C mini", true},
	0x610E: {"RM4 mini", true},
	0x610F: {"RM4C mini", true},
	0x62BC: {"RM4 mini", true},
	0x62BE: {"RM4C mini", true},
	0x6364: {"RM4S", true},
	0x648D: {"RM4 mini", true},
	0x6539: {"RM4C mini", true},
	0x653A: {"RM4 mini", true},
	// rm4pro
	0x520B: {"RM4 pro", true},
	0x5213: {"RM4 pro", true},
	0x5218: {"RM4C pro", true},
	0x6026: {"RM4 pro", true},
	0x6184: {"RM4C pro", true},
	0x61A2: {"RM4 pro", true},
	0x649B: {"RM4 pro", true},
	0x653C: {"RM4 pro", true},
}
