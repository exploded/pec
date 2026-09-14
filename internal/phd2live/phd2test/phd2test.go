// Package phd2test is a fake PHD2 event server for tests: it greets each
// client with the lines PHD2 sends on connect, answers JSON-RPC requests
// from a table, and lets the test push events.
package phd2test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// Server is one fake PHD2.
type Server struct {
	ln net.Listener
	t  testing.TB

	mu       sync.Mutex
	conns    []net.Conn
	hello    []string
	results  map[string]any
	requests []string
	accepted chan struct{}
}

// New starts a server on a free loopback port. By default it greets with a
// Version and a Looping AppState and answers the four questions pec asks.
func New(t testing.TB) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{ln: ln, t: t, accepted: make(chan struct{}, 16), results: map[string]any{
		"get_pixel_scale":          1.43,
		"get_exposure":             2000,
		"get_guide_output_enabled": true,
		"get_profile":              map[string]any{"id": 1, "name": "AT12IN"},
	}}
	s.hello = []string{
		Event("Version", 1000, map[string]any{"PHDVersion": "2.6.14", "PHDSubver": "dev1", "MsgVersion": 1}),
		Event("AppState", 1000, map[string]any{"State": "Looping"}),
	}
	t.Cleanup(s.Close)
	go s.accept()
	return s
}

// Addr is host:port.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// SetHello replaces the lines sent to a new client.
func (s *Server) SetHello(lines ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hello = lines
}

// SetResult sets the JSON-RPC answer for a method.
func (s *Server) SetResult(method string, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[method] = v
}

// Requests lists the RPC methods clients have asked so far.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// WaitConn waits for a client to connect.
func (s *Server) WaitConn(d time.Duration) bool {
	select {
	case <-s.accepted:
		return true
	case <-time.After(d):
		return false
	}
}

// Send writes one event line to every client.
func (s *Server) Send(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_, _ = c.Write([]byte(line + "\r\n"))
	}
}

// CloseConns drops every client, as a PHD2 restart would.
func (s *Server) CloseConns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		c.Close()
	}
	s.conns = nil
}

// Close stops the server.
func (s *Server) Close() {
	s.ln.Close()
	s.CloseConns()
}

func (s *Server) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, c)
		hello := append([]string(nil), s.hello...)
		s.mu.Unlock()
		for _, l := range hello {
			_, _ = c.Write([]byte(l + "\r\n"))
		}
		select {
		case s.accepted <- struct{}{}:
		default:
		}
		go s.serve(c)
	}
}

func (s *Server) serve(c net.Conn) {
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		var req struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.Method == "" {
			continue
		}
		s.mu.Lock()
		s.requests = append(s.requests, req.Method)
		v, ok := s.results[req.Method]
		s.mu.Unlock()
		var reply []byte
		if ok {
			b, _ := json.Marshal(v)
			reply = []byte(fmt.Sprintf(`{"jsonrpc":"2.0","result":%s,"id":%d}`+"\r\n", b, req.ID))
		} else {
			reply = []byte(fmt.Sprintf(`{"jsonrpc":"2.0","error":{"code":1,"message":"unknown method %s"},"id":%d}`+"\r\n", req.Method, req.ID))
		}
		_, _ = c.Write(reply)
	}
}

// Event builds one event line.
func Event(name string, ts float64, fields map[string]any) string {
	m := map[string]any{"Event": name, "Timestamp": ts, "Host": "test", "Inst": 1}
	for k, v := range fields {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// GuideStep builds a GuideStep at Time t with the given raw RA distance
// and, when pulseMS > 0, an RA correction.
func GuideStep(ts float64, frame int, t, ra float64, pulseMS int) string {
	f := map[string]any{
		"Frame": frame, "Time": t, "Mount": "On Camera", "dx": ra, "dy": 0.02,
		"RADistanceRaw": ra, "DECDistanceRaw": 0.02, "RADistanceGuide": 0.0, "DECDistanceGuide": 0.0,
		"StarMass": 8000.0, "SNR": 40.0, "HFD": 2.5, "AvgDist": 0.1,
	}
	if pulseMS > 0 {
		f["RADuration"] = pulseMS
		f["RADirection"] = "West"
	}
	return Event("GuideStep", ts, f)
}
