package bridge

import "testing"

func TestParseSetCommand(t *testing.T) {
	ok := []struct {
		in     string
		name   string
		code   string
		repeat int
	}{
		{"tv_power", "tv_power", "", 1},
		{"  tv_power\n", "tv_power", "", 1},
		{`"tv_power"`, "tv_power", "", 1},
		{`{"command":"vol_up","repeat":3}`, "vol_up", "", 3},
		{`{"command":"vol_up"}`, "vol_up", "", 1},
		{`{"code":"JgA="}`, "", "\x26\x00", 1},
	}
	for _, tc := range ok {
		got, err := ParseSetCommand([]byte(tc.in))
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if got.Name != tc.name || string(got.Code) != tc.code || got.Repeat != tc.repeat {
			t.Errorf("%q: got %+v", tc.in, got)
		}
	}
	bad := []string{"", "   ", `""`, `{}`, `{"command":"a","code":"JgA="}`, `{"command":"a","repeat":99}`,
		`{"command":"a","repeat":-1}`, `{"code":"!!"}`, `{bad`, `"unterminated`}
	for _, in := range bad {
		if _, err := ParseSetCommand([]byte(in)); err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
}

func TestParseLearnHint(t *testing.T) {
	cases := map[string]string{
		"": "", "learn": "", "tv_power": "tv_power", `"tv_power"`: "tv_power",
		`{"command":"x"}`: "x", `{bad`: "", "  y ": "y",
	}
	for in, want := range cases {
		if got := ParseLearnHint([]byte(in)); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestParseTopic(t *testing.T) {
	if n, a, ok := parseTopic("bl", "bl/rm/set"); !ok || n != "rm" || a != "set" {
		t.Errorf("got %q %q %v", n, a, ok)
	}
	for _, in := range []string{"other/rm/set", "bl/rm", "bl/"} {
		if _, _, ok := parseTopic("bl", in); ok {
			t.Errorf("%q should not match", in)
		}
	}
}
