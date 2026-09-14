package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/exploded/pec/internal/analysis"
	"github.com/exploded/pec/internal/nina"
)

// --- Record: the live PHD2 card and the equipment line ------------------------

// measureMinutes is how much unguided measurement a run should have.
const measureMinutes = 8.0

// liveFile is a recording from PHD2's event server, filed like an upload.
type liveFile struct {
	SHA, Name, When, Size string
	Saved                 bool // a run refers to it
}

type liveFit struct {
	Period, Amp1, Cycles float64
	Err                  string
}

type liveRun struct {
	Since, Elapsed string
	Frames, Drops  int
	MidRun         bool
	OutputKnown    bool
	OutputOff      bool    // guide output disabled: a measurement
	MeasuredMin    float64 // minutes of unguided samples so far
	EightAt        string  // when measureMinutes will be reached; "" once passed
	Position       string
	InfoErr        string
	Fit            *liveFit // once the measurement spans four minutes
}

type liveCard struct {
	Configured, Connected              bool
	Addr, Err, Hint, Version, AppState string
	Recording                          *liveRun
}

type equipStatus struct {
	Configured, Reachable bool
	Err                   string
	Mount                 string
	MountOK               bool
	HasFilterWheel        bool
	Filter                string
	FilterIsL             bool
	Cooler                string
}

type tonightView struct {
	Live    liveCard
	Equip   *equipStatus
	Unsaved []liveFile
}

func (s *Server) tonightData(ctx context.Context) tonightView {
	return tonightView{Live: s.liveCard(ctx), Equip: s.equipStatus(ctx), Unsaved: s.unsavedLive(ctx)}
}

// liveCard describes the PHD2 connection and the run in progress, with a
// quick fit once there is enough of it.
func (s *Server) liveCard(ctx context.Context) liveCard {
	st := s.live.State()
	lc := liveCard{Configured: st.Addr != "", Connected: st.Connected, Addr: st.Addr, Err: st.Err, Hint: st.Hint, Version: st.Version, AppState: strings.ToLower(st.AppState)}
	if lc.AppState == "" {
		lc.AppState = "state unknown"
	}
	r := st.Recording
	if r == nil {
		return lc
	}
	loc, now := s.loc(ctx), time.Now()
	begins := r.Begins
	if begins.IsZero() {
		begins = r.StartedAt
	}
	lr := &liveRun{
		Since: begins.In(loc).Format("15:04:05"), Elapsed: fmtElapsed(now.Sub(begins)),
		Frames: r.Frames, Drops: r.Drops, MidRun: r.MidRun,
		OutputKnown: r.OutputKnown, OutputOff: r.OutputKnown && !r.OutputEnabled,
	}
	if r.InfoDone {
		lr.InfoErr = r.InfoErr
		switch r.Info.PositionSource {
		case "nina":
			lr.Position = fmt.Sprintf("Pointing from NINA: Dec %+.1f°", r.Info.DecDeg)
			if r.Info.Target != "" {
				lr.Position += " (" + r.Info.Target + ")"
			}
		default:
			lr.Position = r.Info.Warning
		}
	}
	if sess, err := s.live.Session(loc); err == nil && sess != nil {
		rows, _ := sess.MeasurementSamples()
		if len(rows) >= 2 {
			span := rows[len(rows)-1].Offset - rows[0].Offset
			lr.MeasuredMin = span / 60
			eight := sess.Begins.Add(time.Duration((rows[0].Offset + measureMinutes*60) * float64(time.Second)))
			if eight.After(now) {
				lr.EightAt = eight.In(loc).Format("15:04")
			}
			if span > 240 {
				res, err := analysis.Session(sess, analysis.DefaultParams())
				if err != nil {
					lr.Fit = &liveFit{Err: err.Error()}
				} else {
					lr.Fit = &liveFit{Period: res.Fit.Period, Amp1: res.Fit.Curve.Fundamental().Amp, Cycles: res.Fit.Cycles}
				}
			}
		}
	}
	lc.Recording = lr
	return lc
}

func fmtElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// equipStatus reads the mount, filter wheel and camera through NINA within
// one second, in parallel.
func (s *Server) equipStatus(ctx context.Context) *equipStatus {
	e := &equipStatus{}
	c := s.nina(ctx, time.Second)
	if c == nil {
		return e
	}
	e.Configured = true
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var (
		wg     sync.WaitGroup
		mi     nina.MountInfo
		miErr  error
		fw     nina.FilterWheel
		fwErr  error
		cam    nina.Camera
		camErr error
	)
	wg.Add(3)
	go func() { defer wg.Done(); mi, miErr = c.MountInfo(ctx) }()
	go func() { defer wg.Done(); fw, fwErr = c.FilterWheel(ctx) }()
	go func() { defer wg.Done(); cam, camErr = c.Camera(ctx) }()
	wg.Wait()
	if miErr != nil {
		e.Err = miErr.Error()
		return e
	}
	e.Reachable = true
	switch {
	case !mi.Connected:
		e.Mount = "not connected in NINA"
	case !mi.TrackingEnabled:
		e.Mount = "connected, not tracking"
	default:
		e.Mount, e.MountOK = "tracking ("+mi.TrackingMode+")", true
	}
	if fwErr == nil && fw.Connected {
		e.HasFilterWheel = true
		e.Filter = fw.SelectedFilter.Name
		if e.Filter == "" {
			e.Filter = "none selected"
		}
		e.FilterIsL = strings.HasPrefix(strings.ToLower(strings.TrimSpace(e.Filter)), "l")
	} else {
		e.Filter = "no filter wheel in NINA"
	}
	if camErr == nil && cam.Connected {
		state := "cooler off"
		if cam.CoolerOn {
			state = fmt.Sprintf("cooler on, set %.0f", cam.TemperatureSetPoint)
		}
		e.Cooler = fmt.Sprintf("%.1f °C, %s", cam.Temperature, state)
	} else {
		e.Cooler = "not connected in NINA"
	}
	return e
}

// unsavedLive lists recordings no run refers to yet.
func (s *Server) unsavedLive(ctx context.Context) []liveFile {
	rows, err := s.st.Q.ListUnsavedLiveFiles(ctx, 10)
	if err != nil {
		return nil
	}
	loc := s.loc(ctx)
	out := make([]liveFile, 0, len(rows))
	for _, f := range rows {
		lf := liveFile{SHA: f.Sha256, Name: f.Name, Size: fmt.Sprintf("%.0f KB", float64(f.Size)/1024)}
		if at, err := time.Parse(time.RFC3339, f.UploadedAt); err == nil {
			lf.When = at.In(loc).Format("15:04")
		}
		out = append(out, lf)
	}
	return out
}

// liveFiles lists recordings for the Runs page, saved or not.
func (s *Server) liveFiles(ctx context.Context, n int64) []liveFile {
	rows, err := s.st.Q.ListLiveFiles(ctx, n)
	if err != nil {
		return nil
	}
	loc := s.loc(ctx)
	out := make([]liveFile, 0, len(rows))
	for _, f := range rows {
		lf := liveFile{SHA: f.Sha256, Name: f.Name, Size: fmt.Sprintf("%.0f KB", float64(f.Size)/1024), Saved: f.RunCount > 0}
		if at, err := time.Parse(time.RFC3339, f.UploadedAt); err == nil {
			lf.When = at.In(loc).Format("2006-01-02 15:04")
		}
		out = append(out, lf)
	}
	return out
}

func (s *Server) tonightPage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, "tonight", "", pageData{Title: "Record", Nav: "tonight", Data: s.tonightData(r.Context())})
}

// tonightLive is the card on its own, polled every few seconds.
func (s *Server) tonightLive(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "tonight", "tonight/_phd2", pageData{Title: "Record", Nav: "tonight", Data: s.tonightData(r.Context())})
}

// tonightFilter selects the L filter through NINA: the one filter-wheel
// command pec sends.
func (s *Server) tonightFilter(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	base := pageData{Title: "Record", Nav: "tonight"}
	refuse := func(msg string) { s.problem(w, r, "tonight", base, msg) }
	c := s.nina(ctx, 5*time.Second)
	if c == nil {
		refuse("NINA is not set up: enter the Advanced API address under Settings")
		return
	}
	fw, err := c.FilterWheel(ctx)
	if err != nil {
		refuse(err.Error())
		return
	}
	if !fw.Connected {
		refuse("NINA has no filter wheel connected")
		return
	}
	f, ok := fw.FindFilter("L")
	if !ok {
		var names []string
		for _, x := range fw.AvailableFilters {
			names = append(names, x.Name)
		}
		refuse("no filter starting with L in NINA's list: " + strings.Join(names, ", "))
		return
	}
	if err := c.ChangeFilter(ctx, f.ID); err != nil {
		refuse("NINA did not change the filter: " + err.Error())
		return
	}
	s.log.Info("filter", "name", f.Name, "id", f.ID)
	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast":{"msg":"Filter %s selected"}}`, asciiOnly(f.Name)))
	s.render(w, http.StatusOK, "tonight", "tonight/_phd2", pageData{Title: "Record", Nav: "tonight", Data: s.tonightData(ctx)})
}

// tonightDiscard deletes a recording no run refers to.
func (s *Server) tonightDiscard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	base := pageData{Title: "Record", Nav: "tonight"}
	sha := r.FormValue("file")
	if !validSHA(sha) {
		s.problem(w, r, "tonight", base, "pick a recording")
		return
	}
	res, err := s.st.Q.DeleteUnreferencedFile(ctx, sha)
	if err != nil {
		s.fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.problem(w, r, "tonight", base, "that recording is saved as a run (delete the run first) or is not a recording")
		return
	}
	_ = os.Remove(s.filePath(sha))
	w.Header().Set("HX-Trigger", `{"showToast":{"msg":"Recording discarded"}}`)
	s.render(w, http.StatusOK, "tonight", "tonight/_phd2", pageData{Title: "Record", Nav: "tonight", Data: s.tonightData(ctx)})
}

// analyseLive lists the sessions of a recording, the long way round with
// the analysis settings.
func (s *Server) analyseLive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_ = r.ParseForm()
	sha := r.FormValue("file")
	base := pageData{Title: "Runs", Nav: "analyse", Data: s.analyseView(ctx)}
	if !validSHA(sha) {
		s.problem(w, r, "analyse", base, "pick a recording")
		return
	}
	f, err := s.st.Q.GetFile(ctx, sha)
	if err != nil || f.Kind != "phd2live" {
		s.problem(w, r, "analyse", base, "that is not a recording")
		return
	}
	s.analyseSessions(w, r, sha, f.Name)
}
