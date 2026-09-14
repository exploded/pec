package web

import (
	"context"
	"strconv"
	"time"

	"github.com/exploded/pec/internal/phd2live"
)

// livePosition tells the PHD2 recorder where the mount points when a run
// starts: the mount itself through NINA when it answers, else the star the
// Point page last slewed to (within six hours), else nothing, with a
// warning that the cos(Dec) scaling will assume the equator.
func (s *Server) livePosition(ctx context.Context) phd2live.Position {
	if p, ok := s.ninaPosition(ctx); ok {
		return p
	}
	name := s.st.Setting(ctx, settingTargetName)
	ra, err1 := strconv.ParseFloat(s.st.Setting(ctx, settingTargetRA), 64)
	dec, err2 := strconv.ParseFloat(s.st.Setting(ctx, settingTargetDec), 64)
	at, err3 := time.Parse(time.RFC3339, s.st.Setting(ctx, settingTargetAt))
	if err1 == nil && err2 == nil && err3 == nil && time.Since(at) < 6*time.Hour {
		return phd2live.Position{RAHours: ra, DecDeg: dec, Source: "target", Target: name,
			Warning: "position taken from the last slew on the Point page (" + name + "), not from the mount"}
	}
	return phd2live.Position{Source: "none", Warning: "declination unknown: the cos(Dec) scaling assumes the equator; slew from the Point page or connect NINA"}
}

// liveFinished files a finished recording like an uploaded log.
func (s *Server) liveFinished(ctx context.Context, f phd2live.Finished) error {
	loc := s.loc(ctx)
	at := f.Begins
	if at.IsZero() {
		at = time.Now()
	}
	name := "PHD2 live " + at.In(loc).Format("2006-01-02 15:04")
	return s.st.EnsureFile(ctx, f.SHA, "phd2live", name, f.Size)
}
