# pec

Measures the periodic error of a German equatorial mount from PHD2 guide logs, fits a
harmonic correction curve, and compares it with the PEC table stored in the Bisque TCS.
Built for a Paramount ME (worm period about 150 s, 1250-entry table, 1 tick = 0.1125″).

It is a local, offline tool with a browser UI. It reads files and writes files. It never
talks to the mount.

## Run

```
go build -o pec.exe ./cmd/pec
pec.exe serve
```

Open http://127.0.0.1:8990/. Flags: `-addr`, `-db` (history database, default `pec.db`),
`-data` (uploads directory, default `./data`), `-tz` (zone the PHD2 logs were written in,
default the machine's local zone).

## What it does today (milestones 1 and 2)

- **Analyse log**: upload a PHD2 guide log, pick a session, get the fitted worm period with its
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

Coming: a paste-ready table writer with phase reference (milestone 3), and a screen watcher
that reads the PEC index off the TCS window to sync the phase (milestone 4).

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
