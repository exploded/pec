# pec

Measures the periodic error of a German equatorial mount from PHD2 guide logs, fits a
harmonic correction curve, and compares it with the PEC table stored in the Bisque TCS.
Built for a Paramount ME (worm period about 150 s, 1250-entry table, 1 tick = 0.1125″).

It is a local, offline tool with a browser UI. It reads files and writes files. It never
talks to the mount.

## Run

```
go build
pec.exe
```

`pec.exe` serves http://127.0.0.1:8990/ and opens the browser. It keeps its database
(`pec.db`) and uploaded files (`data\`) beside the executable, so copy the one file wherever you
like and double-click it. Starting it a second time while it is running just opens the browser
to the running copy. There are no options on the command line; the Settings page holds the PHD2
guide-log folder, the NINA profiles folder, the time zone the logs were written in, and the
site.

## How it is used

The nav is the sequence. The Start page shows where you are in it and what to do next, and lets
you choose between checking the current PEC (steps 1, 2, 4, 5) and fitting a new table (all six).

1. **Point**: enter the site once (or read it from NINA) and the page names a bright star near
   the celestial equator one to two hours east of the meridian, to type into TheSkyX's Find box.
2. **Record**: the night checklist. Mount tracking, PHD2 Guiding Assistant runs with PEC off and
   on, and, for a new table, a passive USB capture (Wireshark with USBPcap) of the TheSkyX-to-mount
   link with the TCS window on its Periodic Error Correction tab.
3. **Capture**: upload the capture. pec decodes the MKS 4000 protocol, fits the HA encoder rate
   for the worm period to a few thousandths of a second, reads the PEC index TheSkyX polls for an
   anchor good to a tenth of a second, and, while Apply PEC was on, reads the correction the
   mount applied straight off the encoder and overlays it on the stored table. A typed index
   reading is the fallback.
4. **Runs**: analyse a Guiding Assistant session: worm period, harmonic amplitudes and phases,
   drift, and the RMS before and after removing the fitted curve, with charts. A run marked PEC
   on reports how much periodic error is left; PEC off, how much there is to correct. The mount's
   own table, copied out of the TCS window, is analysed in the same units.
5. **Verify**: pick a PEC-off run and a PEC-on run. The after run is re-fitted at the before
   run's period and the fundamental amplitudes compared, with a blunt verdict: helping, no
   difference, worse, inverted (every harmonic doubled) or half a cycle out of phase.
6. **Fit**: write the paste-ready table from a PEC-off run with the period pinned and the phase
   set by the anchor, or by smoothing a table the TCS recorded itself. Every fit shows the
   quantisation error and a phase-error budget, and saves a `.meta.json` with the full provenance
   so it can be rebuilt later. The table is pasted into the TCS window by hand.

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
