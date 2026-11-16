package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"broadlink-to-mqtt-gw/bridge"
)

type fakeBackend struct {
	err   error
	calls []string
}

func (f *fakeBackend) rec(format string, a ...any) error {
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
	return f.err
}
func (f *fakeBackend) ListDevices() []bridge.DeviceView {
	return []bridge.DeviceView{{Name: "rm", Commands: []bridge.CommandView{}}}
}
func (f *fakeBackend) Send(d string, c bridge.SetCommand) error {
	return f.rec("send %s %q %x x%d", d, c.Name, c.Code, c.Repeat)
}
func (f *fakeBackend) StartLearn(d, h string) error { return f.rec("learn %s %q", d, h) }
func (f *fakeBackend) LearnStatus(d string) (bridge.LearnView, error) {
	return bridge.LearnView{State: "idle"}, f.err
}
func (f *fakeBackend) SaveCommand(d, n string, c []byte) error {
	return f.rec("save %s %s %x", d, n, c)
}
func (f *fakeBackend) RenameCommand(d, a, b string) error { return f.rec("rename %s %s %s", d, a, b) }
func (f *fakeBackend) DeleteCommand(d, n string) error    { return f.rec("delete %s %s", d, n) }

func do(h http.Handler, method, path, body, ctype string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRoutes(t *testing.T) {
	fb := &fakeBackend{}
	h := New(fb, nil)
	const js = "application/json"
	cases := []struct {
		method, path, body string
		want               int
		call               string
	}{
		{"GET", "/api/devices", "", 200, ""},
		{"GET", "/api/devices/rm/learn", "", 200, ""},
		{"POST", "/api/devices/rm/learn", `{"hint":"tv"}`, 200, `learn rm "tv"`},
		{"POST", "/api/devices/rm/learn", ``, 200, `learn rm ""`},
		{"PUT", "/api/devices/rm/commands/power", `{"code":"JgA="}`, 200, "save rm power 2600"},
		{"DELETE", "/api/devices/rm/commands/power", "", 200, "delete rm power"},
		{"POST", "/api/devices/rm/commands/a/rename", `{"to":"b"}`, 200, "rename rm a b"},
		{"POST", "/api/devices/rm/commands/a/send", `{"repeat":2}`, 200, `send rm "a"  x2`},
		{"POST", "/api/devices/rm/send", `{"code":"JgA="}`, 200, `send rm "" 2600 x0`},
		{"PUT", "/api/devices/rm/commands/power", `{"code":"!!"}`, 400, ""},
		{"PUT", "/api/devices/rm/commands/power", `{"nope":1}`, 400, ""},
		{"PUT", "/api/devices/rm/commands/power", `{bad`, 400, ""},
		{"GET", "/api/nothing", "", 404, ""},
	}
	for _, tc := range cases {
		fb.calls = nil
		w := do(h, tc.method, tc.path, tc.body, js)
		if w.Code != tc.want {
			t.Errorf("%s %s: status %d, want %d (%s)", tc.method, tc.path, w.Code, tc.want, w.Body)
		}
		if tc.call != "" && (len(fb.calls) != 1 || fb.calls[0] != tc.call) {
			t.Errorf("%s %s: calls %v, want %q", tc.method, tc.path, fb.calls, tc.call)
		}
	}
}

func TestContentTypeEnforced(t *testing.T) {
	fb := &fakeBackend{}
	h := New(fb, nil)
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data"} {
		if w := do(h, "POST", "/api/devices/rm/commands/a/send", `{}`, ct); w.Code != http.StatusUnsupportedMediaType {
			t.Errorf("content type %q: status %d", ct, w.Code)
		}
	}
	if len(fb.calls) != 0 {
		t.Errorf("backend was called: %v", fb.calls)
	}
	if w := do(h, "POST", "/api/devices/rm/commands/a/send", `{}`, "application/json; charset=utf-8"); w.Code != 200 {
		t.Errorf("json with charset: %d", w.Code)
	}
}

func TestBodyLimit(t *testing.T) {
	h := New(&fakeBackend{}, nil)
	big := `{"code":"` + strings.Repeat("A", maxBody) + `"}`
	if w := do(h, "PUT", "/api/devices/rm/commands/a", big, "application/json"); w.Code != http.StatusBadRequest {
		t.Errorf("status %d", w.Code)
	}
}

func TestErrorMapping(t *testing.T) {
	for err, want := range map[error]int{
		bridge.ErrNotFound:        404,
		bridge.ErrInvalid:         400,
		bridge.ErrReadOnly:        403,
		bridge.ErrExists:          409,
		bridge.ErrBusy:            409,
		fmt.Errorf("udp timeout"): 502,
		fmt.Errorf("wrapped: %w", bridge.ErrNotFound): 404,
	} {
		h := New(&fakeBackend{err: err}, nil)
		if w := do(h, "DELETE", "/api/devices/rm/commands/a", "", ""); w.Code != want {
			t.Errorf("%v: status %d, want %d", err, w.Code, want)
		}
	}
}

func TestStaticAndSPAFallback(t *testing.T) {
	ui := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("js")},
	}
	h := New(&fakeBackend{}, ui)

	if w := do(h, "GET", "/", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "app") || w.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("index: %d %q %q", w.Code, w.Body, w.Header().Get("Cache-Control"))
	}
	if w := do(h, "GET", "/some/client/route", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "app") {
		t.Errorf("fallback: %d", w.Code)
	}
	if w := do(h, "GET", "/assets/app.js", "", ""); w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset: %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
	if w := do(h, "GET", "/assets/missing.js", "", ""); w.Code != 404 {
		t.Errorf("missing asset must 404, got %d", w.Code)
	}
	// The mux redirects to the cleaned path; either way nothing outside the
	// UI file system may be served.
	if w := do(h, "GET", "/../../etc/passwd", "", ""); strings.Contains(w.Body.String(), "root:") {
		t.Errorf("traversal served a file: %d", w.Code)
	}
	if w := do(h, "GET", "/assets/../../etc/passwd", "", ""); strings.Contains(w.Body.String(), "root:") {
		t.Errorf("traversal served a file: %d", w.Code)
	}
}

func TestNoUI(t *testing.T) {
	if w := do(New(&fakeBackend{}, nil), "GET", "/", "", ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d", w.Code)
	}
}
