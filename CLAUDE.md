# pec

Periodic-error analysis and PEC curve fitting for a Paramount ME (MKS 4000, Bisque TCS).
Reads PHD2 guide logs and TCS PEC tables, fits a harmonic curve, and (from milestone 3)
writes a paste-ready TCS table. **It never talks to the mount.** No TheSkyX scripting to
write, no serial, no network beyond the local UI.

The brief is `.local/PEC_TOOL_PROMPT.md` (gitignored, authoritative on algorithms and formats).

## Skills

- `go-htmx-skill` for anything in `internal/web` (htmx **4**: `hx-disable`, `hx-status:422`,
  `:inherited`; never htmx 2 spellings).
- `sqlc-sqlite` once `internal/store` exists (milestone 2).
- `google-style` for prose.

## Stack

Go 1.27, stdlib only plus `modernc.org/sqlite` (milestone 2). No other Go dependencies
without asking. htmx 4.0.0 is vendored at `internal/web/static/js/htmx.min.js`.
Everything is `go:embed`-ed; the deployable is `pec.exe`.

## Build / run

```
build.bat                 vet + test + build pec.exe
pec.exe serve             http://127.0.0.1:8990/  (-addr, -data ./data, -tz Local)
pec.exe version           VCS revision from the Go toolchain (no ldflags)
```

James runs his own instance; to test a change, run a second instance on another port
(`-addr 127.0.0.1:8999 -data <scratch>`) and stop it afterwards.

## Layout

```
cmd/pec/            subcommand dispatch (flag.NewFlagSet per command)
internal/phd2/      PHD2 guide-log parser: sessions, samples, INFO events, DROP rows
internal/tcs/       TCS PEC table read/write, ticks <-> arcsec, quantisation
internal/pe/        the numerics: segmentation, Householder QR, joint LS fit, periodogram, DFT
internal/analysis/  joins parsers to the fitter; the policy layer (which rows count, warnings)
internal/web/       http server, handlers, embedded templates and static files
testdata/           real guide-log excerpt (4 sessions) and the real TCS table
```

## Numerics (verified in tests)

- One joint least-squares design matrix: per-segment offsets | drift polynomial | K harmonics.
  No detrend-then-fold; that leaks drift into the fundamental on short runs.
- Periodogram scans 140-160 s minimising RSS **with the full harmonic count**; scanning with the
  fundamental alone biases the period because the unmodelled harmonics dominate the residual.
- Phase convention lives on `pe.Harmonic` only: `phi = atan2(B, A)`, harmonic k peaks at
  `t0 + phi*P/(360k)`. `pe.DFT` uses the same convention so tables and measurements compare.
- Amplitude `sqrt(A^2+B^2)` with `A=(2/N) sum v cos` reproduces the brief's reference table:
  k=1 6.067 ticks, k=2 3.202, k=3 1.716; RMS about zero 5.011 ticks. `TestDFTReferenceTable`.
- The synthetic round-trip suite (`internal/pe/fit_test.go`) is the gate: 2 % amplitude and
  2 degrees phase at low noise.
- `phd2.RASign` is the only RA sign in the code base.

## Gotchas

- `//go:embed all:templates`: fragment templates start with `_` and a plain directory embed
  silently skips them.
- The TCS table on disk is LF-terminated; `tcs.Write` emits `%4d\t%d\n` to stay byte-identical.
- PHD2 header keys repeat across lines (`Dec` appears in "Norm rates" before the target line);
  extract fields from the current line's pairs, not the first-wins map.
- Never commit `.local/`, `data/`, `*.db*`.
