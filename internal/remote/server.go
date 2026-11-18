// Package remote carries core.API over HTTP: the server side runs inside the
// «клиент = сервер» host (or headless with --serve), the client side is used
// by the other workplaces.
package remote

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/Serge-Nook/aegis/internal/core"
)

const (
	DefaultPort   = 8080
	DiscoveryPort = 8089
	maxBody       = 256 << 20
	tokenTTL      = 30 * 24 * time.Hour
	maxFails      = 5
	lockTime      = time.Minute
)

type Info struct {
	App        string
	Version    string
	Name       string
	Kind       string
	NeedsSetup bool
}

type Credentials struct {
	Login    string
	FullName string
	Password string
}

type LoginReply struct {
	Token string
	User  core.User
}

type errorReply struct {
	Code string
	Msg  string
}

type token struct {
	uid  int64
	seen  time.Time
	stamp string
}

type attempts struct {
	n     int
	until time.Time
}

type Server struct {
	svc     *core.Service
	name    string
	version string
	mu      sync.Mutex
	tokens  map[string]*token
	fails   map[string]*attempts
	api     reflect.Type
}

func NewServer(svc *core.Service, name, version string) *Server {
	return &Server{svc: svc, name: name, version: version, tokens: map[string]*token{}, fails: map[string]*attempts{}, api: reflect.TypeOf((*core.API)(nil)).Elem()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", s.info)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("POST /api/call/{method}", s.call)
	return mux
}

// ListenAndServe serves the API, answers LAN discovery and checks deadlines
// until the listener fails.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

func (s *Server) Serve(ln net.Listener) error {
	port := DefaultPort
	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		port = a.Port
	}
	go func() {
		if err := Announce(s.name, port); err != nil {
			log.Printf("discovery: %v", err)
		}
	}()
	go func() {
		for {
			if err := s.svc.CheckDeadlines(); err != nil {
				log.Printf("deadlines: %v", err)
			}
			time.Sleep(5 * time.Minute)
		}
	}()
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var ce *core.Error
	status := http.StatusInternalServerError
	rep := errorReply{Code: core.CodeInternal, Msg: err.Error()}
	if errors.As(err, &ce) {
		rep = errorReply{Code: ce.Code, Msg: ce.Msg}
		switch ce.Code {
		case core.CodeAuth:
			status = http.StatusUnauthorized
		case core.CodeForbidden:
			status = http.StatusForbidden
		case core.CodeNotFound:
			status = http.StatusNotFound
		case core.CodeTooLarge:
			status = http.StatusRequestEntityTooLarge
		default:
			status = http.StatusBadRequest
		}
	} else {
		log.Printf("api: %v", err)
	}
	writeJSON(w, status, rep)
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	need, err := s.svc.NeedsSetup()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, Info{App: "aegis", Version: s.version, Name: s.name, Kind: s.svc.Kind(), NeedsSetup: need})
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody)).Decode(v); err != nil {
		return &core.Error{Code: core.CodeInvalid, Msg: "bad request"}
	}
	return nil
}

// setup is allowed only from the host itself: otherwise anyone on the network
// could claim the administrator account of a fresh database.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		writeErr(w, core.ErrForbidden)
		return
	}
	var c Credentials
	if err := decode(r, &c); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.svc.CreateFirstAdmin(c.Login, c.FullName, c.Password); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, struct{}{})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var c Credentials
	if err := decode(r, &c); err != nil {
		writeErr(w, err)
		return
	}
	key := strings.ToLower(strings.TrimSpace(c.Login))
	s.mu.Lock()
	a := s.fails[key]
	locked := a != nil && time.Now().Before(a.until)
	s.mu.Unlock()
	if locked {
		writeErr(w, &core.Error{Code: core.CodeAuth, Msg: "too many attempts"})
		return
	}
	ss, err := s.svc.Login(c.Login, c.Password)
	if err != nil {
		if core.CodeOf(err) == core.CodeAuth {
			s.loginFailed(key)
		}
		writeErr(w, err)
		return
	}
	s.mu.Lock()
	delete(s.fails, key)
	s.mu.Unlock()
	stamp, err := s.svc.AuthStamp(ss.UserID())
	if err != nil {
		writeErr(w, err)
		return
	}
	u, err := ss.Me()
	if err != nil {
		writeErr(w, err)
		return
	}
	b := make([]byte, 24)
	rand.Read(b)
	tk := hex.EncodeToString(b)
	s.mu.Lock()
	now := time.Now()
	for k, t := range s.tokens {
		if now.Sub(t.seen) > tokenTTL {
			delete(s.tokens, k)
		}
	}
	s.tokens[tk] = &token{uid: ss.UserID(), seen: now, stamp: stamp}
	s.mu.Unlock()
	writeJSON(w, 200, LoginReply{Token: tk, User: *u})
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	delete(s.tokens, bearer(r))
	s.mu.Unlock()
	writeJSON(w, 200, struct{}{})
}

// loginFailed locks a login for lockTime after maxFails wrong passwords in a row.
func (s *Server) loginFailed(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.fails[key]
	if a == nil {
		if len(s.fails) > 10000 {
			clear(s.fails)
		}
		a = &attempts{}
		s.fails[key] = a
	}
	a.n++
	if a.n >= maxFails {
		a.n = 0
		a.until = time.Now().Add(lockTime)
	}
}

// session resolves the bearer token. A token stops working once its user's
// credentials change (password change or reset, deactivation, restore).
func (s *Server) session(r *http.Request) *core.Session {
	tk := bearer(r)
	s.mu.Lock()
	t := s.tokens[tk]
	if t == nil || time.Since(t.seen) > tokenTTL {
		s.mu.Unlock()
		return nil
	}
	t.seen = time.Now()
	uid, stamp := t.uid, t.stamp
	s.mu.Unlock()
	if st, err := s.svc.AuthStamp(uid); err != nil || st != stamp {
		s.mu.Lock()
		delete(s.tokens, tk)
		s.mu.Unlock()
		return nil
	}
	return s.svc.SessionFor(uid)
}

// restamp keeps the caller's own token valid after he changed his password.
func (s *Server) restamp(r *http.Request, uid int64) {
	st, err := s.svc.AuthStamp(uid)
	if err != nil {
		return
	}
	s.mu.Lock()
	if t := s.tokens[bearer(r)]; t != nil && t.uid == uid {
		t.stamp = st
	}
	s.mu.Unlock()
}

var errType = reflect.TypeOf((*error)(nil)).Elem()

func (s *Server) call(w http.ResponseWriter, r *http.Request) {
	ss := s.session(r)
	if ss == nil {
		writeErr(w, core.ErrAuth)
		return
	}
	name := r.PathValue("method")
	if _, ok := s.api.MethodByName(name); !ok {
		writeErr(w, core.ErrNotFound)
		return
	}
	m := reflect.ValueOf(ss).MethodByName(name)
	var raw []json.RawMessage
	if err := decode(r, &raw); err != nil {
		writeErr(w, err)
		return
	}
	mt := m.Type()
	if len(raw) != mt.NumIn() {
		writeErr(w, &core.Error{Code: core.CodeInvalid, Msg: "arguments"})
		return
	}
	args := make([]reflect.Value, len(raw))
	for i := range raw {
		p := reflect.New(mt.In(i))
		if err := json.Unmarshal(raw[i], p.Interface()); err != nil {
			writeErr(w, &core.Error{Code: core.CodeInvalid, Msg: "arguments"})
			return
		}
		args[i] = p.Elem()
	}
	out := m.Call(args)
	if e := out[len(out)-1]; mt.Out(len(out)-1) == errType && !e.IsNil() {
		writeErr(w, e.Interface().(error))
		return
	}
	if name == "ChangePassword" || name == "SaveUser" {
		s.restamp(r, ss.UserID())
	}
	var res any
	if len(out) == 2 {
		res = out[0].Interface()
	}
	writeJSON(w, 200, res)
}
