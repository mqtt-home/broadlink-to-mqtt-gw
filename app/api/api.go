// Package api serves the JSON API and the embedded React UI.
//
// There is no authentication: the listener is meant for a trusted LAN or to
// sit behind a reverse proxy. State-changing requests must carry
// "Content-Type: application/json", which browsers cannot send cross-site
// without a CORS preflight (no CORS headers are served), so other websites
// cannot trigger IR commands through a visitor's browser.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"broadlink-to-mqtt-gw/bridge"
	"broadlink-to-mqtt-gw/config"
	"github.com/philipparndt/go-logger"
)

// Backend is what the API needs from the bridge manager.
type Backend interface {
	ListDevices() []bridge.DeviceView
	Send(device string, cmd bridge.SetCommand) error
	StartLearn(device, hint string) error
	LearnStatus(device string) (bridge.LearnView, error)
	SaveCommand(device, name string, code []byte) error
	RenameCommand(device, from, to string) error
	DeleteCommand(device, name string) error
}

const maxBody = 64 << 10

// New returns the handler for /api/* plus the static UI (ui may be nil).
func New(b Backend, ui fs.FS) http.Handler {
	s := &server{b: b}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/devices", s.listDevices)
	mux.HandleFunc("POST /api/devices/{device}/learn", s.json(s.startLearn))
	mux.HandleFunc("GET /api/devices/{device}/learn", s.learnStatus)
	mux.HandleFunc("POST /api/devices/{device}/send", s.json(s.sendRaw))
	mux.HandleFunc("PUT /api/devices/{device}/commands/{name}", s.json(s.saveCommand))
	mux.HandleFunc("DELETE /api/devices/{device}/commands/{name}", s.deleteCommand)
	mux.HandleFunc("POST /api/devices/{device}/commands/{name}/rename", s.json(s.rename))
	mux.HandleFunc("POST /api/devices/{device}/commands/{name}/send", s.json(s.sendCommand))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, errors.New("unknown endpoint"))
	})
	mux.Handle("/", staticHandler(ui))
	return mux
}

type server struct{ b Backend }

// json wraps handlers that read a JSON body: enforces the content type and
// the body size limit.
func (s *server) json(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mt != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, errors.New("Content-Type must be application/json"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		h(w, r)
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *server) listDevices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.b.ListDevices())
}

func (s *server) startLearn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hint string `json:"hint"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.b.StartLearn(r.PathValue("device"), body.Hint); err != nil {
		writeBackendError(w, err)
		return
	}
	s.learnStatus(w, r)
}

func (s *server) learnStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.b.LearnStatus(r.PathValue("device"))
	if err != nil {
		writeBackendError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) sendRaw(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code   string `json:"code"`
		Repeat int    `json:"repeat"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	code, err := config.DecodeCode(body.Code)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.send(w, r.PathValue("device"), bridge.SetCommand{Code: code, Repeat: body.Repeat})
}

func (s *server) sendCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repeat int `json:"repeat"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.send(w, r.PathValue("device"), bridge.SetCommand{Name: r.PathValue("name"), Repeat: body.Repeat})
}

func (s *server) send(w http.ResponseWriter, device string, cmd bridge.SetCommand) {
	if err := s.b.Send(device, cmd); err != nil {
		writeBackendError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) saveCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	code, err := config.DecodeCode(body.Code)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.b.SaveCommand(r.PathValue("device"), r.PathValue("name"), code); err != nil {
		writeBackendError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) rename(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To string `json:"to"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.b.RenameCommand(r.PathValue("device"), r.PathValue("name"), body.To); err != nil {
		writeBackendError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) deleteCommand(w http.ResponseWriter, r *http.Request) {
	if err := s.b.DeleteCommand(r.PathValue("device"), r.PathValue("name")); err != nil {
		writeBackendError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeBackendError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, bridge.ErrNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, bridge.ErrInvalid):
		writeError(w, http.StatusBadRequest, err)
	case errors.Is(err, bridge.ErrReadOnly):
		writeError(w, http.StatusForbidden, err)
	case errors.Is(err, bridge.ErrExists), errors.Is(err, bridge.ErrBusy):
		writeError(w, http.StatusConflict, err)
	default:
		logger.Error("API request failed", "error", err)
		writeError(w, http.StatusBadGateway, err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// staticHandler serves the built UI with an index.html fallback for client
// side routes. Hashed assets are cached; index.html never.
func staticHandler(ui fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ui == nil {
			http.Error(w, "UI not built (run `make ui`)", http.StatusServiceUnavailable)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if st, err := fs.Stat(ui, name); err != nil || st.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}
		f, err := ui.Open(name)
		if err != nil {
			http.Error(w, "UI not built (run `make ui`)", http.StatusServiceUnavailable)
			return
		}
		defer func() { _ = f.Close() }()
		st, _ := f.Stat()
		rs, ok := f.(io.ReadSeeker)
		if !ok {
			http.Error(w, "unsupported file system", http.StatusInternalServerError)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, name, st.ModTime(), rs)
	})
}
