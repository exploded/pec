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

Go 1.27, stdlib plus `modernc.org/sqlite` (pure Go, never mattn). No other Go dependencies
without asking. htmx 4.0.0 is vendored at `internal/web/static/js/htmx.min.js`.
Everything is `go:embed`-ed (templates, CSS, schema); the deployable is `pec.exe`.

## Build / run

```
sqlc generate             after editing internal/store/queries.sql or schema.sql
build.bat                 vet + test + build pec.exe
pec.exe serve             http://127.0.0.1:8990/  (-addr, -db pec.db, -data ./data, -tz Local)
pec.exe version           VCS revision from the Go toolchain (no ldflags)
```

## sqlc

`sqlc.yaml` → `internal/store/db` (generated, never edit). Schema is `CREATE IF NOT EXISTS`
and applied on every `store.Open`; to change it, edit `schema.sql` and delete `pec.db` (no
migrations yet). `SetMaxOpenConns(1)` — never raise it. Inserts use `:execresult` +
`LastInsertId`. Runs store summary numbers only; pages **re-fit from the stored upload**
(`<data>/files/<sha256>`) so charts and Verify always come from the samples.

James runs his own instance; to test a change, run a second instance on another port
(`-addr 127.0.0.1:8999 -data <scratch>`) and stop it afterwards.

## Layout

```
cmd/pec/            subcommand dispatch (flag.NewFlagSet per command)
internal/phd2/      PHD2 guide-log parser: sessions, samples, INFO events, DROP rows
internal/tcs/       TCS PEC table read/write, ticks <-> arcsec, quantisation
internal/pe/        the numerics: segmentation, Householder QR, joint LS fit, periodogram, DFT
internal/analysis/  joins parsers to the fitter; the policy layer (which rows count, warnings)
internal/report/    ReportData builders, Go-generated inline SVG (svg.go), report.css tokens,
                    body.tmpl (embedded in pages) and page.tmpl (standalone download)
internal/store/     schema.sql, queries.sql, open.go, store.go (save helpers), db/ (sqlc)
internal/web/       http server, handlers (handlers.go analyse/table, handlers_runs.go runs/verify),
                    embedded templates and static files
testdata/           real guide-log excerpt (4 sessions) and the real TCS table
```

## Charts and report

Inline SVG generated in Go, no JS charting, following skyq's `design/CHARTS.md`: one y-axis
per chart, colour slots fixed by meaning (`--series-1` measured/before, `--series-2`
fitted/after, `--series-3` residual), markers only at low density, legend for 2+ series.
`report.css` holds the tokens for both the live UI (`/report.css`) and the standalone report
(inlined). Legend swatches use classes `.dot.s1/.s2/.s3` because html/template strips `var()`
from inline `style`. Verdict bands: `.verdict.good|warn|bad|info`.

Verify pins the after run to the before run's period and classifies on the fundamental ratio,
using the 2nd harmonic to tell "inverted" (everything doubled) from "half a cycle out" (even
harmonics cancelled). A guided run on either side gets a warning: PHD2 suppresses the signal
regardless of PEC, so only Guiding Assistant runs prove anything.

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
