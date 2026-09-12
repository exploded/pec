# pec

Measures the periodic error of a German equatorial mount from PHD2 guide logs, fits a
harmonic correction curve, and compares it with the PEC table stored in the Bisque TCS.
Built for a Paramount ME (worm period about 150 s, 1250-entry table, 1 tick = 0.1125″).

It is a local, offline tool with a browser UI. It reads files and writes files. It never
talks to the mount.

## Run

```
go build -o pec.exe ./cmd/pec
pec.exe
```

Open http://127.0.0.1:8990/. Running `pec.exe` with no arguments (or double-clicking it) starts
the server; `pec.exe serve` is the same thing spelled out. Flags: `-addr`, `-db` (history database, default `pec.db`),
`-data` (uploads directory, default `./data`), `-tz` (zone the PHD2 logs were written in,
default the machine's local zone).

## What it does

- **Analyse log**: upload a PHD2 guide log, or pick one from the PHD2 folder on this PC (`-phd2`, default Documents/PHD2), pick a session, get the fitted worm period with its
  uncertainty, the amplitude and phase of each harmonic, the drift rate, and the RMS before and
  after removing the fitted curve, with charts: the samples folded at the worm period with the
  fitted curve, the residual over time, the harmonic amplitudes, and the period scan. Warns when
  PHD2 was guiding (which suppresses the signal), when the run is shorter than three worm cycles,
  and when the cadence is too coarse for the requested harmonics.
- **TCS table**: upload the table copied from the Bisque TCS window and get the same harmonic
  breakdown in the same units, with the table and its reconstruction drawn together.
- **Runs**: every analysis is saved to `pec.db` with the target, altitude, pier side, period,
  harmonics and residual, plus your note of whether mount PEC was on. Each run page has a
  self-contained HTML report to download.
- **Verify**: pick a PEC-off run and a PEC-on run. The after run is re-fitted at the before
  run's period and the fundamental amplitudes compared, with a blunt verdict: helping, no
  difference, worse, inverted (every harmonic doubled) or half a cycle out of phase (even
  harmonics cancelled).

- **Anchor**: type the PEC index the Bisque TCS window is showing and press the button; the
  server stamps the time. That ties the mount's index to the clock, with a stated timing
  uncertainty (a reaction time, about 2 s or 5° of phase).
- **Fit**: write a paste-ready table. It refuses without a phase reference. Two modes: smooth a
  table the TCS recorded itself (already in the mount's phase, no anchor needed), or fit a
  guide-log run with the period pinned and the phase set from an anchor. Every fit shows the
  quantisation error from rounding to whole ticks and, in anchor mode, a phase-error budget
  (anchor timing and period uncertainty combined). Invert negates the table for when the sign
  convention turns out to be backwards. Saved fits have the table text to copy, a download,
  and a `.meta.json` with the full provenance (source file and hash, session, anchor, period and
  its source, harmonics, sign convention, warnings) so the fit can be rebuilt later.

- **Tonight**: the measuring-night checklist, ticks kept in the browser.
- **Target**: where to point for a run. Enter the site once (or read it from the NINA profile on the
  same PC) and the page names a bright star near the celestial equator that is one to two hours east
  of the meridian right now, with its hour angle, altitude and distance from the Moon, so it can be
  typed into TheSkyX's Find box. A time field plans ahead.

- **Capture** (milestone 4): upload a passive USB capture of the TheSkyX-to-mount link
  (Wireshark with USBPcap; nothing is sent to the mount). pec decodes the MKS 4000 protocol,
  fits the HA encoder rate while tracking, and reads the PEC index TheSkyX polls while the TCS
  window's Periodic Error Correction tab is showing. That gives the worm period to a few
  thousandths of a second and an anchor good to a tenth of a second, saved as an anchor that
  Fit uses automatically. The tracking status word doubles as proof that tracking never stopped.

Nothing is ever sent to the mount. The table is pasted by hand into the TCS window.

## Method

One joint least-squares fit per session: an offset per dither segment, a drift polynomial,
and K harmonics of the worm period. The period comes from a least-squares periodogram over
140–160 s with those nuisance parameters included. Householder QR, plain Go, no numeric
dependencies.

## Test

```
go test ./...
```

The synthetic round-trip test generates a known three-harmonic curve with drift and noise
and requires the fit to recover amplitudes within 2 % and phases within 2°. The real TCS
table in `testdata/` must reproduce its reference harmonic amplitudes.
