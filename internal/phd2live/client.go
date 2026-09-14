// Package phd2live keeps a connection to PHD2's event server and records
// each guiding run as it happens. The recording is the raw event stream,
// one JSON line per event as PHD2 sent it, plus one line of pec's own with
// what PHD2 answered about the run (pixel scale, exposure, guide output)
// and where the mount was pointing. When the run stops the file is hashed
// and filed like an uploaded guide log, and internal/phd2 parses it back
// into the same Session the log parser produces.
//
// pec only listens. The only requests it sends are four read-only JSON-RPC
// calls at the start of a run; it never starts, stops, pauses or dithers.
package phd2live

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/exploded/pec/internal/phd2"
)

// DefaultAddr is where PHD2 listens by default (Tools > Enable Server).
const DefaultAddr = "127.0.0.1:4400"

const (
	reconnectDelay = 5 * time.Second
	dialTimeout    = 3 * time.Second
	rpcTimeout     = 3 * time.Second
	infoWait       = 3 * time.Second // finalise waits this long for the SessionInfo line
)

// Options wires the client to the rest of pec.
type Options struct {
	LiveDir  string // recordings in progress
	FilesDir string // finished recordings, by sha256, next to uploaded files
	// Position, if set, is asked where the mount points when a run starts.
	Position func(ctx context.Context) Position
	// Finished, if set, registers a finished recording (the files table).
	Finished func(ctx context.Context, f Finished) error
	Logger   *slog.Logger
	// Reconnect overrides the retry interval (tests shorten it).
	Reconnect time.Duration
}

// Position is where the mount pointed when a run started.
type Position struct {
	RAHours, DecDeg float64 // J2000
	Source          string  // nina | target | none
	Target          string  // the star's name, when known
	Warning         string  // why the position is a guess, when it is
}

// Finished describes a recording that has been filed.
type Finished struct {
	SHA    string
	Path   string
	Size   int64
	Begins time.Time // first frame's guiding start; zero if unknown
	Ends   time.Time // last event
	Frames int
	MidRun bool
	Reason string // "" for a normal GuidingStopped
}

// Recording is the run in progress, as far as pec has seen it.
type Recording struct {
	Path      string
	StartedAt time.Time // when pec opened the file
	Begins    time.Time // the run's own start, from the first GuideStep; zero until then
	LastAt    time.Time // last event
	Frames    int
	Drops     int
	// OutputEnabled is PHD2's guide output as last reported; true until told
	// otherwise. OutputKnown says whether anything reported it.
	OutputEnabled bool
	OutputKnown   bool
	// OutputDisabledAfter is the number of frames received before guide
	// output went off; -1 while it has not.
	OutputDisabledAfter int
	Info                phd2.SessionInfo // once InfoDone
	InfoDone            bool
	InfoErr             string // which questions PHD2 did not answer
	MidRun              bool   // pec connected while the run was already going
}

// State is a snapshot for the UI.
type State struct {
	Addr      string // "" = live feed off
	Connected bool
	Err       string // last dial or read error; "" while connected
	Hint      string // what to check when the connection is refused
	Since     time.Time
	Version   string // PHD2's version, from its first event
	AppState  string // Stopped | Selected | Calibrating | Guiding | LostLock | Paused | Looping
	Recording *Recording
	Finished  []Finished // this process, newest first
}

type recording struct {
	Recording
	f        *os.File
	w        *bufio.Writer
	infoDone chan struct{}
}

type rpcReply struct {
	result json.RawMessage
	err    error
}

// Client is the long-lived connection. Zero value is not usable: use New.
type Client struct {
	opt   Options
	log   *slog.Logger
	delay time.Duration

	mu     sync.Mutex
	st     State
	addr   string
	conn   net.Conn
	rec    *recording
	rpcID  int
	rpc    map[int]chan rpcReply
	closed bool

	kick    chan struct{}
	done    chan struct{}
	started bool
	wg      sync.WaitGroup
}

// New prepares a client; nothing is dialled until Start and SetAddr.
func New(opt Options) *Client {
	if opt.Logger == nil {
		opt.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	c := &Client{opt: opt, log: opt.Logger, delay: opt.Reconnect, rpc: map[int]chan rpcReply{}, kick: make(chan struct{}, 1), done: make(chan struct{})}
	if c.delay <= 0 {
		c.delay = reconnectDelay
	}
	return c
}

// Start launches the connection loop, after filing any recording a crash
// left behind. Idempotent.
func (c *Client) Start() {
	c.mu.Lock()
	if c.started || c.closed {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.mu.Unlock()
	c.sweep()
	c.wg.Add(1)
	go c.run()
}

// SetAddr points the client at a PHD2 server; "" turns the feed off. A
// change drops the current connection, which files any run in progress.
func (c *Client) SetAddr(addr string) {
	c.mu.Lock()
	if addr == c.addr {
		c.mu.Unlock()
		return
	}
	c.addr = addr
	c.st.Addr = addr
	c.st.Err, c.st.Hint = "", ""
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// State returns a snapshot.
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.st
	st.Finished = append([]Finished(nil), c.st.Finished...)
	if c.rec != nil {
		r := c.rec.Recording
		st.Recording = &r
	}
	return st
}

// Session parses the run in progress, or returns nil when there is none.
func (c *Client) Session(loc *time.Location) (*phd2.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rec == nil {
		return nil, nil
	}
	if err := c.rec.w.Flush(); err != nil {
		return nil, err
	}
	f, err := os.Open(c.rec.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	l, err := phd2.ParseEvents(f, filepath.Base(c.rec.Path), loc)
	if err != nil {
		return nil, err
	}
	if len(l.Sessions) == 0 {
		return nil, nil
	}
	return l.Sessions[len(l.Sessions)-1], nil
}

// Close stops the loop and files any run in progress.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.done)
	if c.conn != nil {
		c.conn.Close()
	}
	started := c.started
	c.mu.Unlock()
	if started {
		waited := make(chan struct{})
		go func() { c.wg.Wait(); close(waited) }()
		select {
		case <-waited:
		case <-time.After(3 * time.Second):
			c.log.Warn("phd2live: connection loop did not stop in time")
		}
	}
	c.finalise("pec stopped")
	return nil
}

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// wait sleeps until the delay passes or SetAddr kicks; false on Close.
func (c *Client) wait(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-c.done:
		return false
	case <-c.kick:
		return true
	case <-t.C:
		return true
	}
}

func (c *Client) run() {
	defer c.wg.Done()
	for {
		c.mu.Lock()
		addr := c.addr
		c.mu.Unlock()
		if addr == "" {
			if !c.wait(time.Hour) {
				return
			}
			continue
		}
		conn, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err != nil {
			c.mu.Lock()
			if c.addr == addr {
				c.st.Err = "PHD2 is not answering on " + addr + ": " + shortErr(err)
				c.st.Since = time.Now()
				if refused(err) {
					c.st.Hint = "check Tools > Enable Server in PHD2"
				}
			}
			c.mu.Unlock()
			if !c.wait(c.delay) {
				return
			}
			continue
		}
		c.mu.Lock()
		if c.closed || c.addr != addr {
			c.mu.Unlock()
			conn.Close()
			if c.isClosed() {
				return
			}
			continue
		}
		c.conn = conn
		c.st.Connected, c.st.Err, c.st.Hint, c.st.Since = true, "", "", time.Now()
		c.mu.Unlock()
		c.log.Info("phd2live: connected", "addr", addr)

		err = c.readLoop(conn)
		conn.Close()

		c.mu.Lock()
		c.conn = nil
		c.st.Connected = false
		c.st.Since = time.Now()
		c.st.AppState = ""
		reason := "PHD2 disconnected"
		if c.closed {
			reason = "pec stopped"
		} else if c.addr != addr {
			reason = "PHD2 server address changed"
		} else {
			c.st.Err = "PHD2 disconnected: " + shortErr(err)
		}
		for id, ch := range c.rpc {
			delete(c.rpc, id)
			ch <- rpcReply{err: errors.New("disconnected")}
		}
		c.mu.Unlock()
		c.finalise(reason)
		if c.isClosed() {
			return
		}
		if !c.wait(c.delay) {
			return
		}
	}
}

// readLoop reads lines until the connection drops.
func (c *Client) readLoop(conn net.Conn) error {
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	catchup := true // until the first AppState, events describe the state before pec connected
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var hdr struct {
			Event     string
			Timestamp float64
			ID        *int            `json:"id"`
			Result    json.RawMessage `json:"result"`
			Error     json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(line, &hdr); err != nil {
			continue
		}
		if hdr.Event == "" && hdr.ID != nil {
			c.deliver(*hdr.ID, hdr.Result, hdr.Error)
			continue
		}
		if c.handleEvent(line, hdr.Event, hdr.Timestamp, &catchup) {
			c.finalise("")
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

// handleEvent updates the state and appends the line to the recording.
// It reports whether the run just ended.
func (c *Client) handleEvent(line []byte, name string, ts float64, catchup *bool) (ended bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	switch name {
	case "Version":
		var v struct{ PHDVersion, PHDSubver string }
		_ = json.Unmarshal(line, &v)
		c.st.Version = v.PHDVersion + v.PHDSubver
	case "AppState":
		var v struct{ State string }
		_ = json.Unmarshal(line, &v)
		c.st.AppState = v.State
		if v.State == "Guiding" && c.rec == nil {
			c.openRecording(true)
		}
		*catchup = false
	case "StartGuiding":
		c.st.AppState = "Guiding"
		if c.rec == nil {
			c.openRecording(*catchup)
		}
	case "GuideStep":
		c.st.AppState = "Guiding"
		if c.rec == nil {
			c.openRecording(true)
		}
		var v struct{ Time float64 }
		_ = json.Unmarshal(line, &v)
		c.rec.Frames++
		if c.rec.Begins.IsZero() {
			c.rec.Begins = unixTime(ts - v.Time)
		}
	case "StarLost":
		c.st.AppState = "LostLock"
		if c.rec != nil {
			c.rec.Drops++
		}
	case "GuideParamChange":
		var v struct {
			Name  string
			Value json.RawMessage
		}
		_ = json.Unmarshal(line, &v)
		if v.Name == "MountGuidingEnabled" && c.rec != nil {
			on := string(bytes.TrimSpace(v.Value)) != "false"
			c.rec.OutputEnabled, c.rec.OutputKnown = on, true
			if !on && c.rec.OutputDisabledAfter < 0 {
				c.rec.OutputDisabledAfter = c.rec.Frames
			}
		}
	case "GuidingStopped":
		c.st.AppState = "Stopped"
		ended = c.rec != nil
	case "LoopingExposures":
		if c.rec == nil {
			c.st.AppState = "Looping"
		}
	case "LoopingExposuresStopped":
		c.st.AppState = "Stopped"
	case "Paused":
		c.st.AppState = "Paused"
	case "Resumed":
		c.st.AppState = "Guiding"
	case "StartCalibration":
		c.st.AppState = "Calibrating"
	case "StarSelected":
		if c.rec == nil {
			c.st.AppState = "Selected"
		}
	}
	if c.rec != nil {
		c.rec.LastAt = now
		if _, err := c.rec.w.Write(line); err == nil {
			_ = c.rec.w.WriteByte('\n')
		}
		_ = c.rec.w.Flush()
	}
	return ended
}

// openRecording starts a file for a new run. Called with mu held.
func (c *Client) openRecording(midRun bool) {
	if err := os.MkdirAll(c.opt.LiveDir, 0o755); err != nil {
		c.log.Error("phd2live: live dir", "err", err)
		return
	}
	now := time.Now()
	path := filepath.Join(c.opt.LiveDir, now.Format("20060102-150405")+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		c.log.Error("phd2live: open recording", "err", err)
		return
	}
	rec := &recording{f: f, w: bufio.NewWriter(f), infoDone: make(chan struct{})}
	rec.Path, rec.StartedAt, rec.OutputEnabled, rec.OutputDisabledAfter, rec.MidRun = path, now, true, -1, midRun
	c.rec = rec
	c.log.Info("phd2live: recording", "path", path, "midRun", midRun)
	go c.fetchInfo(rec)
}

// fetchInfo asks PHD2 about the run and appends the SessionInfo line.
func (c *Client) fetchInfo(rec *recording) {
	defer close(rec.infoDone)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	info := phd2.SessionInfo{Event: phd2.SessionInfoEvent, Timestamp: float64(time.Now().UnixNano()) / 1e9, MidRun: rec.MidRun, PositionSource: "none"}
	var errs []string
	if v, err := c.callFloat(ctx, "get_pixel_scale"); err == nil {
		info.PixelScale = v
	} else {
		errs = append(errs, "get_pixel_scale: "+err.Error())
	}
	if v, err := c.callFloat(ctx, "get_exposure"); err == nil {
		info.ExposureMS = int(v)
	} else {
		errs = append(errs, "get_exposure: "+err.Error())
	}
	if raw, err := c.call(ctx, "get_guide_output_enabled"); err == nil {
		var on bool
		if json.Unmarshal(raw, &on) == nil {
			info.GuideOutputEnabled = &on
		}
	} else {
		errs = append(errs, "get_guide_output_enabled: "+err.Error())
	}
	if raw, err := c.call(ctx, "get_profile"); err == nil {
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(raw, &p)
		info.Profile = p.Name
	}
	if c.opt.Position != nil {
		pos := c.opt.Position(ctx)
		info.RAHours, info.DecDeg, info.Target, info.Warning = pos.RAHours, pos.DecDeg, pos.Target, pos.Warning
		if pos.Source != "" {
			info.PositionSource = pos.Source
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rec != rec {
		return
	}
	if b, err := json.Marshal(info); err == nil {
		_, _ = rec.w.Write(b)
		_ = rec.w.WriteByte('\n')
		_ = rec.w.Flush()
	}
	rec.Info, rec.InfoDone, rec.InfoErr = info, true, strings.Join(errs, "; ")
	if info.GuideOutputEnabled != nil && !*info.GuideOutputEnabled && !rec.OutputKnown {
		rec.OutputEnabled, rec.OutputKnown, rec.OutputDisabledAfter = false, true, 0
	}
}

// call sends one JSON-RPC request and waits for its reply.
func (c *Client) call(ctx context.Context, method string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	c.mu.Lock()
	conn := c.conn
	if conn == nil {
		c.mu.Unlock()
		return nil, errors.New("not connected")
	}
	c.rpcID++
	id := c.rpcID
	ch := make(chan rpcReply, 1)
	c.rpc[id] = ch
	c.mu.Unlock()
	unregister := func() {
		c.mu.Lock()
		delete(c.rpc, id)
		c.mu.Unlock()
	}
	req := fmt.Sprintf("{\"method\":%q,\"id\":%d}\r\n", method, id)
	_ = conn.SetWriteDeadline(time.Now().Add(rpcTimeout))
	_, err := conn.Write([]byte(req))
	_ = conn.SetWriteDeadline(time.Time{})
	if err != nil {
		unregister()
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		unregister()
		return nil, ctx.Err()
	}
}

func (c *Client) callFloat(ctx context.Context, method string) (float64, error) {
	raw, err := c.call(ctx, method)
	if err != nil {
		return 0, err
	}
	var v *float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, fmt.Errorf("%s answered %s", method, raw)
	}
	if v == nil {
		return 0, fmt.Errorf("%s is not set in PHD2", method)
	}
	return *v, nil
}

func (c *Client) deliver(id int, result, rpcErr json.RawMessage) {
	c.mu.Lock()
	ch := c.rpc[id]
	delete(c.rpc, id)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	if len(rpcErr) > 0 && string(rpcErr) != "null" {
		var e struct{ Message string }
		_ = json.Unmarshal(rpcErr, &e)
		if e.Message == "" {
			e.Message = string(rpcErr)
		}
		ch <- rpcReply{err: errors.New(e.Message)}
		return
	}
	ch <- rpcReply{result: result}
}

// finalise closes the recording in progress and files it. Safe to call
// when there is none.
func (c *Client) finalise(reason string) {
	c.mu.Lock()
	rec := c.rec
	c.mu.Unlock()
	if rec == nil {
		return
	}
	// Let the SessionInfo line land: the RPCs answer within milliseconds.
	select {
	case <-rec.infoDone:
	case <-time.After(infoWait):
	}
	c.mu.Lock()
	if c.rec != rec {
		c.mu.Unlock()
		return
	}
	c.rec = nil
	_ = rec.w.Flush()
	_ = rec.f.Close()
	c.mu.Unlock()

	f, err := c.file(rec.Path, rec.Frames, rec.Begins, rec.LastAt, rec.MidRun, reason)
	if err != nil {
		c.log.Error("phd2live: filing recording", "path", rec.Path, "err", err)
		return
	}
	if f == nil {
		return
	}
	c.mu.Lock()
	c.st.Finished = append([]Finished{*f}, c.st.Finished...)
	if len(c.st.Finished) > 10 {
		c.st.Finished = c.st.Finished[:10]
	}
	c.mu.Unlock()
}

// file hashes a closed recording into FilesDir and registers it. A
// recording with no frames is deleted and nil is returned.
func (c *Client) file(path string, frames int, begins, ends time.Time, midRun bool, reason string) (*Finished, error) {
	if frames == 0 {
		return nil, os.Remove(path)
	}
	sha, size, err := hashFile(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(c.opt.FilesDir, 0o755); err != nil {
		return nil, err
	}
	dst := filepath.Join(c.opt.FilesDir, sha)
	if _, err := os.Stat(dst); err == nil {
		_ = os.Remove(path)
	} else if err := os.Rename(path, dst); err != nil {
		return nil, err
	}
	f := Finished{SHA: sha, Path: dst, Size: size, Begins: begins, Ends: ends, Frames: frames, MidRun: midRun, Reason: reason}
	if c.opt.Finished != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := c.opt.Finished(ctx, f); err != nil {
			c.log.Error("phd2live: registering recording", "sha", sha, "err", err)
		}
	}
	c.log.Info("phd2live: filed recording", "sha", sha, "frames", frames, "reason", reason)
	return &f, nil
}

// sweep files recordings a previous process left in LiveDir.
func (c *Client) sweep() {
	paths, _ := filepath.Glob(filepath.Join(c.opt.LiveDir, "*.jsonl"))
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		l, err := phd2.ParseEvents(f, filepath.Base(p), time.UTC)
		f.Close()
		frames := 0
		var begins, ends time.Time
		midRun := false
		if err == nil {
			for _, s := range l.Sessions {
				frames += len(s.Samples)
				if begins.IsZero() {
					begins = s.Begins
				}
				if n := len(s.Samples); n > 0 {
					ends = s.Samples[n-1].At
				}
				midRun = midRun || s.Header["Joined"] != ""
			}
		}
		if _, err := c.file(p, frames, begins, ends, midRun, "pec was not running when the run ended"); err != nil {
			c.log.Error("phd2live: sweeping", "path", p, "err", err)
		}
	}
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func unixTime(ts float64) time.Time {
	sec := int64(ts)
	return time.Unix(sec, int64((ts-float64(sec))*1e9))
}

func refused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "refused")
}

// shortErr drops the "dial tcp 127.0.0.1:4400:" prefix Go adds.
func shortErr(err error) string {
	if err == nil {
		return "connection closed"
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		err = op.Err
	}
	return strings.TrimRight(err.Error(), ".")
}
