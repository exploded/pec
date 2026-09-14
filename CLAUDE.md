# pec

Periodic-error analysis and PEC curve fitting for a Paramount ME (MKS 4000, Bisque TCS).
Records PHD2 Guiding Assistant runs live from PHD2's event server (or reads guide logs) and
TCS PEC tables, fits a harmonic curve, and writes a paste-ready TCS table with its provenance.
**The only mount command pec sends is the Point page slew, through the NINA Advanced API,
behind a confirmation dialog and equipment checks** (rule amended 2026-09-14 by James; the
brief's absolute ban is superseded on this one point). It never writes the PEC table, never
syncs, parks, homes or sets tracking, never scripts TheSkyX, never sends serial. The only other
command is a filter change through NINA. Everything else is read-only: PHD2's event stream,
NINA's equipment state, files.

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
build.bat                 vet + test + build pec.exe (go build in the root does the same build)
pec.exe                   serves http://127.0.0.1:8990/, opens the browser, keeps pec.db and data\ beside
                          the executable; a second start just opens the browser. No subcommands.
pec.exe -addr 127.0.0.1:8999 -db <scratch>\pec.db -data <scratch>\data
                          developer flags for a test instance (the only flags there are)
```

Everything a user can change is on the Settings page (PHD2 folder, NINA folder, log time
zone, site, PHD2 server, NINA API), stored in the `settings` table and read per request through
`s.phd2Dir(ctx)`, `s.ninaDir(ctx)`, `s.loc(ctx)`, `s.phd2Server(ctx)`, `s.ninaAPI(ctx)`
(`internal/web/settings.go`). `web.Options` holds the defaults; tests set `Options` and never
write the settings table. `Options.PHD2Server` and `Options.NINAAPI` are blank in tests so
nothing is dialled; `main.go` sets them to `phd2live.DefaultAddr` and `nina.DefaultBase`. The version in the footer comes from
`debug.ReadBuildInfo` (no ldflags, no version command).

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
main.go             flags -addr/-db/-data only, executable-dir defaults, port-busy check, opens the browser
internal/phd2/      PHD2 guide-log parser: sessions, samples, INFO events, DROP rows; events.go parses a
                    live recording (event-server JSON lines) into the same Session; LoadFile sniffs which
internal/phd2live/  the recorder: a goroutine on PHD2's event server writing each run to <data>/live and
                    filing it under <data>/files/<sha> when it stops (phd2test/ is the fake server for tests)
internal/nina/      NINA Advanced API client: mount, dome, safety, filter wheel and camera reads, the slew,
                    a filter change; sky.J2000ToDate/DateToJ2000 bridge the mount's JNOW and the catalogue
internal/tcs/       TCS PEC table read/write, ticks <-> arcsec, quantisation
internal/pe/        the numerics: segmentation, Householder QR, joint LS fit, periodogram, DFT
internal/mks/       MKS 4000 protocol from a USBPcap capture: pcap/pcapng reader, frame splitter,
                    request/reply pairing, encoder-rate and index analysis (watch.go), Synth for tests
internal/sky/       sidereal time, altitude, a low-precision Moon, bright equatorial stars, and the
                    NINA profile reader (JSON or XML) behind the Target page
internal/analysis/  joins parsers to the fitter; the policy layer (which rows count, warnings);
                    fit.go (table writer, both phase modes, phase-error budget), meta.go (provenance)
internal/report/    ReportData builders, Go-generated inline SVG (svg.go), report.css tokens,
                    body.tmpl (embedded in pages) and page.tmpl (standalone download)
internal/store/     schema.sql, queries.sql, open.go, store.go (save helpers), db/ (sqlc)
internal/web/       http server, handlers (handlers.go analyse/table, handlers_runs.go runs/verify,
                    handlers_fit.go anchor/fit/fits, handlers_target.go where to point, handlers_mount.go the
                    mount through NINA and the slew, handlers_tonight.go the Record page's live card and the
                    L filter, live.go the recorder hooks, handlers_start.go the step list with live status,
                    settings.go), embedded templates and static files
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

## Fit (table writer)

Refuses without a phase reference. `tcs` mode: DFT-smooth the TCS's own recording; phase and
units are the mount's, no PHD2 sign involved. `index` mode: pin the period and set
`FitOptions.PhaseOrigin = anchorOffset - index*P/N` so the fitted curve is in table phase by
construction; correction = -error, invert negates again. The anchor offset includes half the
exposure (centroid = exposure midpoint). Phase-error budget: anchor `360*sigma_t/P`, period
`360*farS/P*(sigma_P/P)` at the sample farthest from the anchor, quadrature sum; over 20° is
"do not paste". Sanitise an infinite period sigma to 0 (unknown) before it reaches JSON.
`pec_table.meta.json` carries everything needed to rebuild the fit from the source file alone
(analysis params, anchor, period and source), which is how `/fits/{id}` survives run and anchor
deletion (`ON DELETE SET NULL`).

## MKS 4000 protocol (inferred from one capture, 2026-09-12)

Frame: `64 00 axis seq len16 word16 data sum16 00 5a`, zeros doubled on the wire, sum16 over the
decoded bytes before it, len16 = decoded bytes from 64 through the checksum. Requests and replies
share the seq byte. Commands: 3 status word (0x1200 tracking, 0x0300 slewing, 0x0200 idle), 200
read u16 register, 210 read i32 register, 211 write i32. Axis 0 registers: 4 "Current Position",
10 "Current Encoder" (133.5 counts/s tracking = 16 counts per PEC index step, 20,000 per worm
turn), 16-bit 9 = PEC index (polled only while the TCS PEC tab is showing). The USB adapter
fragments replies into 1-3 byte packets, so the splitter reassembles. **Apply PEC is visible in the
encoder**: while the table plays back the encoder register reads minus the table (one count = one
tick), confirmed 2026-09-12 (fold amplitude 6.06 vs table 6.07 ticks, 180 degrees apart). `Analyse`
finds those minutes by residual RMS (`pecLoud`), fits the rate on the quiet ones, and folds the
PEC-on residual by index (`Result.Fold`); the report overlays it on the latest stored table. `mks.Synth` builds a
synthetic capture for tests; the real capture lives in `.local/cpature.pcapng` (gitignored) and
`TestRealCapture` uses it when present. pec never sends a frame: the encoder exists for tests only.

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
- Every guide-log amplitude is in RA-axis arcseconds: `analysis.sessionSamples` divides the PHD2
  sky error by cos(Dec) (PHD2 sees the axis error foreshortened; the table is in axis units).
  Verify ratios are unaffected; index-mode tables would otherwise be low by cos(Dec).

## Target page

`sky.PlanAt` classifies the star list against `sky.DefaultWindow` (HA -2 to -0.75 h, alt over 30,
30 degrees from a Moon over a quarter lit). The pick is the equator-nearest star with at least
`MinGoodFor` left. Site lat/lon live in the `settings` table (`site.lat`, `site.lon`); "Read from
NINA" parses the newest `.profile` in `%LOCALAPPDATA%\NINA\Profiles` (current NINA writes XML;
a 0, 0 site is rejected). The page also shows the mount through NINA and carries the slew (see
NINA below). Never scripts TheSkyX.

## PHD2 live

`phd2live.Client` dials `phd2.server` (default 127.0.0.1:4400; PHD2 Tools > Enable Server, on by
default in current builds), reconnects every 5 s, and records every event line of a run
verbatim to `<data>/live/<time>.jsonl`. A run opens on `StartGuiding` (PHD2 re-sends it to a
client that connects mid-run: `MidRun`), or on `AppState Guiding`/`GuideStep` with none open.
At the start pec asks four read-only RPCs (`get_pixel_scale`, `get_exposure`,
`get_guide_output_enabled`, `get_profile`) and the position hook (NINA mount/info in J2000,
else the last slew's `target.*` settings within 6 h, else "none" with a warning), and appends
its own `{"Event":"pec.SessionInfo",...}` line. `GuidingStopped`, a disconnect or `Close`
finalises: no frames means delete; else sha256, move to `<data>/files/<sha>`, `EnsureFile`
kind `phd2live`, name "PHD2 live 2026-09-14 21:05". `Start` sweeps leftovers from a crash.
`phd2.ParseEvents` turns the file into the same `Session` the log parser gives: `Begins` =
first `GuideStep` `Timestamp - Time` (absolute, so `tz` only affects display), `Offset` = PHD2's
`Time`, `StarLost` = drop, `GuidingDithered` = dither, `GuideParamChange
MountGuidingEnabled=false` = the Guiding Assistant turning output off. There is no GA event
and no GA result over the wire, so `Session.Live` makes `Guiding().IsGA` true whenever output
was off (`GuidingState.Disabled`; `DisabledAfter` is -1 when it preceded the first sample).
`phd2.LoadFile` sniffs the first byte (`{` = recording) and is the only loader the web layer
uses (`s.loadLog`). The Record page polls `/tonight/live` every 5 s: connection state, the run
(frames, unguided minutes, a quick `analysis.Session` fit once the measurement spans 4 min),
NINA's mount/filter/cooler with a Select L button, and finished recordings no run refers to,
each with Save as PEC off / on (posting the existing `/analyse`) and Discard
(`DeleteUnreferencedFile`). Recordings also appear on Runs (`/analyse/live`). `phd2test` is
the fake server; `TestTonightLiveFlow` drives a whole run through the pages.

## NINA

`nina.Client` wraps the Advanced API plugin (default `http://127.0.0.1:1888`, every route a GET
with query params, envelope `{Response, Error, StatusCode, Success}`; `Success == false` is an
`APIError`). Reads: `mount/info` (Connected, AtPark, TrackingEnabled, Slewing, RA hours, Dec
degrees, `Coordinates.Epoch` in the driver's native system, usually JNOW: `MountInfo.J2000`
precesses), `dome/info`, `safetymonitor/info`, `filterwheel/info`, `camera/info`. Commands:
`mount/slew?ra=<J2000 degrees>&dec=&waitForResult=true` (RA in degrees, epoch J2000 whatever
the mount reports) and `filterwheel/change-filter?filterId=`. The Point page's `mountStatus`
asks mount, dome and safety monitor in parallel within 1.5 s per poll so an absent NINA never
stalls a page. `POST /target/slew` refuses unless: confirm ticked, star known, site set, star
above 20 degrees now, NINA answering, mount connected and not parked and tracking and not
slewing, roof open if a dome device is connected, safe if a safety monitor is connected;
then slews (4 min budget), saves `target.name/ra/dec/at`, reads the pointing back and reports
the separation. Without a dome or safety device the card and the dialog say pec cannot check
the roof. The dialog lives outside the polled `#target-result` and closes on the `slewDone`
HX-Trigger event. Field names were taken from the plugin's spec and source; confirm them
against a live plugin when something reads oddly (`curl http://127.0.0.1:1888/v2/api/equipment/mount/info`).

## UI sequence

The nav is the workflow: Start · 1 Point (/target) · 2 Record (/tonight) · 3 Capture · 4 Runs
(/analyse, with the runs table) · 5 Verify · 6 Fit (with the TCS table upload card) · Settings.
`startData` derives each step's status from the list queries and picks the next step; the
"Check my PEC" / "Fit a new table" choice is browser-local (`localStorage` key `pec.mode`) and
only hides the capture/fit items. Record carries the live PHD2 card above the checklist; an
unsaved recording makes "Save the recorded run on Record" the next step on Start. /table and /anchor keep working but are reached from Fit and
Capture, not the nav. Nav keys: home, target, tonight, capture (anchor pages too), analyse (run
pages too), verify, fit (table pages too), settings.

## Gotchas

- `//go:embed all:templates`: fragment templates start with `_` and a plain directory embed
  silently skips them.
- The TCS table on disk is LF-terminated; `tcs.Write` emits `%4d\t%d\n` to stay byte-identical.
- PHD2 header keys repeat across lines (`Dec` appears in "Norm rates" before the target line);
  extract fields from the current line's pairs, not the first-wins map.
- Never commit `.local/`, `data/`, `*.db*`.
- Asset URLs carry `?v=<hash of embedded static files>` (`assetTag`); without it Chrome kept a
  stale `app.css` across rebuilds despite `Cache-Control: no-cache`.
- Defaults come from `os.Executable()`, so `go run .` would put `pec.db` in the temp build dir;
  always pass `-db`/`-data` when developing, and use port 8999 with scratch paths.
- html/template escapes apostrophes in status strings (`'` becomes `&#39;`); tests that grep
  page text must avoid them.
- Bash heredocs on this machine fail on non-ASCII (°, ±, ″); write such Go files with the Write tool.
- `srv.Close()` (the recorder) runs after `hs.Shutdown`'s 5 s budget and can take 3 s more; it
  finalises a run in progress, so never kill pec mid-run if the recording matters.
- Only finalised recordings live under `files/`; a file still being written stays in `live/`, so
  a sha is never taken over a growing file.
- `analysis.Session` on the live card runs every 5 s once the run spans 4 min: keep it cheap.
