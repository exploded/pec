package phd2live

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/exploded/pec/internal/phd2"
	"github.com/exploded/pec/internal/phd2live/phd2test"
)

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newClient(t *testing.T, fake *phd2test.Server, pos func(ctx context.Context) Position) (*Client, string, chan Finished) {
	t.Helper()
	dir := t.TempDir()
	finished := make(chan Finished, 4)
	c := New(Options{
		LiveDir: filepath.Join(dir, "live"), FilesDir: filepath.Join(dir, "files"),
		Position:  pos,
		Finished:  func(ctx context.Context, f Finished) error { finished <- f; return nil },
		Reconnect: 50 * time.Millisecond,
	})
	t.Cleanup(func() { c.Close() })
	if fake != nil {
		c.SetAddr(fake.Addr())
	}
	c.Start()
	return c, dir, finished
}

func TestClientRecordsRun(t *testing.T) {
	fake := phd2test.New(t)
	posCalled := false
	c, dir, finished := newClient(t, fake, func(ctx context.Context) Position {
		posCalled = true
		return Position{RAHours: 22.1, DecDeg: -0.3, Source: "nina", Target: "Sadalmelik"}
	})
	if !fake.WaitConn(2 * time.Second) {
		t.Fatal("client did not connect")
	}
	waitFor(t, "connected state", 2*time.Second, func() bool { return c.State().Connected && c.State().Version == "2.6.14dev1" })

	base := 2000.0
	fake.Send(phd2test.Event("StartGuiding", base, nil))
	waitFor(t, "recording", 2*time.Second, func() bool { return c.State().Recording != nil })
	waitFor(t, "session info", 3*time.Second, func() bool { return c.State().Recording.InfoDone })
	frame := 0
	send := func(n int, pulse int) {
		for i := 0; i < n; i++ {
			frame++
			tt := float64(frame) * 2.5
			fake.Send(phd2test.GuideStep(base+tt, frame, tt, 0.4*math.Sin(tt/24), pulse))
		}
	}
	send(3, 100)
	fake.Send(phd2test.Event("GuideParamChange", base+8, map[string]any{"Name": "MountGuidingEnabled", "Value": false}))
	send(20, 0)
	waitFor(t, "frames", 2*time.Second, func() bool { return c.State().Recording != nil && c.State().Recording.Frames == 23 })
	st := c.State()
	if st.Recording.OutputEnabled || st.Recording.OutputDisabledAfter != 3 || st.Recording.MidRun {
		t.Errorf("recording state %+v", *st.Recording)
	}
	if st.Recording.Info.PixelScale != 1.43 || st.Recording.Info.ExposureMS != 2000 || st.Recording.Info.Profile != "AT12IN" || st.Recording.Info.PositionSource != "nina" {
		t.Errorf("info %+v (err %q)", st.Recording.Info, st.Recording.InfoErr)
	}
	if sess, err := c.Session(time.UTC); err != nil || sess == nil || len(sess.Samples) != 23 || !sess.Live {
		t.Errorf("live session: %v %+v", err, sess)
	}

	fake.Send(phd2test.Event("GuidingStopped", base+70, nil))
	var f Finished
	select {
	case f = <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("recording was not filed")
	}
	if f.Frames != 23 || f.MidRun || f.Reason != "" || f.Begins.Unix() != int64(base) {
		t.Errorf("finished %+v", f)
	}
	data, err := os.ReadFile(filepath.Join(dir, "files", f.SHA))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != f.SHA || int64(len(data)) != f.Size {
		t.Error("sha or size does not match the file")
	}
	l, err := phd2.ParseEvents(strings.NewReader(string(data)), "rec", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	s := l.Sessions[0]
	g := s.Guiding()
	if len(s.Samples) != 23 || !g.IsGA || g.DisabledAfter != 2 || s.PixelScale != 1.43 || s.Header["Target"] != "Sadalmelik" || s.Ends.IsZero() {
		t.Errorf("parsed session: samples %d guiding %+v scale %v header %v ends %v", len(s.Samples), g, s.PixelScale, s.Header, s.Ends)
	}
	if !posCalled {
		t.Error("position hook not called")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "live", "*")); len(left) != 0 {
		t.Errorf("live dir not empty: %v", left)
	}
	if st := c.State(); st.Recording != nil || len(st.Finished) != 1 || st.AppState != "Stopped" {
		t.Errorf("state after stop %+v", st)
	}
	reqs := fake.Requests()
	if len(reqs) != 4 {
		t.Errorf("rpc requests %v", reqs)
	}
}

func TestClientMidRunAndDisconnect(t *testing.T) {
	fake := phd2test.New(t)
	fake.SetHello(
		phd2test.Event("Version", 3000, map[string]any{"PHDVersion": "2.6.14", "PHDSubver": ""}),
		phd2test.Event("StartGuiding", 3000, nil),
		phd2test.Event("AppState", 3000, map[string]any{"State": "Guiding"}),
	)
	c, _, finished := newClient(t, fake, nil)
	if !fake.WaitConn(2 * time.Second) {
		t.Fatal("no connection")
	}
	waitFor(t, "mid-run recording", 2*time.Second, func() bool { r := c.State().Recording; return r != nil && r.MidRun })
	waitFor(t, "session info", 3*time.Second, func() bool { return c.State().Recording.InfoDone })
	for i := 1; i <= 3; i++ {
		tt := 200 + float64(i)*2.5
		fake.Send(phd2test.GuideStep(3000+tt, 100+i, tt, 0.1, 0))
	}
	waitFor(t, "frames", 2*time.Second, func() bool { r := c.State().Recording; return r != nil && r.Frames == 3 })
	fake.CloseConns()
	var f Finished
	select {
	case f = <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("recording was not filed on disconnect")
	}
	if !f.MidRun || f.Frames != 3 || !strings.Contains(f.Reason, "disconnected") || f.Begins.Unix() != 3000 {
		t.Errorf("finished %+v", f)
	}
	if !fake.WaitConn(3 * time.Second) {
		t.Fatal("client did not reconnect")
	}
	waitFor(t, "reconnected", 2*time.Second, func() bool { return c.State().Connected })
	// The catch-up StartGuiding after a reconnect opens a fresh mid-run recording.
	waitFor(t, "second recording", 2*time.Second, func() bool { r := c.State().Recording; return r != nil && r.MidRun })
	if st := c.State(); st.Recording.InfoDone && st.Recording.Info.PositionSource != "none" {
		t.Errorf("no position hook should give source none: %+v", st.Recording.Info)
	}
}

func TestClientRefusedHint(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	c, _, _ := newClient(t, nil, nil)
	c.SetAddr(addr)
	waitFor(t, "refused hint", 3*time.Second, func() bool {
		st := c.State()
		return strings.Contains(st.Hint, "Enable Server") && strings.Contains(st.Err, addr)
	})
	start := time.Now()
	c.Close()
	if time.Since(start) > 2*time.Second {
		t.Error("Close took too long")
	}
	if st := c.State(); st.Connected {
		t.Error("connected after close")
	}
}

func TestClientOffWithoutAddr(t *testing.T) {
	fake := phd2test.New(t)
	c, _, _ := newClient(t, nil, nil)
	if fake.WaitConn(200 * time.Millisecond) {
		t.Fatal("client dialled without an address")
	}
	if st := c.State(); st.Addr != "" || st.Connected || st.Err != "" {
		t.Errorf("state %+v", st)
	}
	c.SetAddr(fake.Addr())
	if !fake.WaitConn(2 * time.Second) {
		t.Fatal("SetAddr did not connect")
	}
	waitFor(t, "connected", 2*time.Second, func() bool { return c.State().Connected })
	c.SetAddr("")
	waitFor(t, "disconnected", 2*time.Second, func() bool { st := c.State(); return !st.Connected && st.Addr == "" })
	if fake.WaitConn(300 * time.Millisecond) {
		t.Error("reconnected after the feed was turned off")
	}
}

func TestSweepFilesLeftovers(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live")
	os.MkdirAll(live, 0o755)
	data, _ := os.ReadFile("../../testdata/phd2_events_excerpt.jsonl")
	os.WriteFile(filepath.Join(live, "old.jsonl"), data, 0o644)
	os.WriteFile(filepath.Join(live, "empty.jsonl"), []byte(phd2test.Event("StartGuiding", 1, nil)+"\n"), 0o644)
	finished := make(chan Finished, 4)
	c := New(Options{LiveDir: live, FilesDir: filepath.Join(dir, "files"), Finished: func(ctx context.Context, f Finished) error { finished <- f; return nil }})
	c.Start()
	defer c.Close()
	select {
	case f := <-finished:
		if f.Frames != 15 || !strings.Contains(f.Reason, "not running") {
			t.Errorf("swept %+v", f)
		}
		if _, err := os.Stat(f.Path); err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("leftover recording not filed")
	}
	if left, _ := filepath.Glob(filepath.Join(live, "*")); len(left) != 0 {
		t.Errorf("live dir not empty: %v", left)
	}
}
