-- pec history. CREATE IF NOT EXISTS throughout: applied on every open.

-- Uploaded guide logs and TCS tables, deduplicated by content. The bytes
-- live at <data>/files/<sha256>; runs are re-fitted from them on demand.
CREATE TABLE IF NOT EXISTS files (
    sha256      TEXT PRIMARY KEY,
    kind        TEXT NOT NULL,             -- 'phd2' | 'tcs'
    name        TEXT NOT NULL,
    size        INTEGER NOT NULL,
    uploaded_at TEXT NOT NULL              -- RFC3339
);

-- One row per analysis: a fitted guide-log session or a decomposed TCS table.
CREATE TABLE IF NOT EXISTS runs (
    id               INTEGER PRIMARY KEY,
    created_at       TEXT NOT NULL,        -- RFC3339
    kind             TEXT NOT NULL,        -- 'analyse' | 'table'
    file_sha256      TEXT NOT NULL REFERENCES files(sha256),
    source_name      TEXT NOT NULL,
    session_index    INTEGER NOT NULL DEFAULT 0,
    session_begins   TEXT NOT NULL DEFAULT '',   -- RFC3339 with offset; '' for tables
    equipment        TEXT NOT NULL DEFAULT '',
    ra_hours         REAL NOT NULL DEFAULT 0,
    dec_deg          REAL NOT NULL DEFAULT 0,
    hour_angle       REAL NOT NULL DEFAULT 0,
    alt_deg          REAL NOT NULL DEFAULT 0,
    pier_side        TEXT NOT NULL DEFAULT '',
    pixel_scale      REAL NOT NULL DEFAULT 0,
    exposure_ms      INTEGER NOT NULL DEFAULT 0,
    sample_count     INTEGER NOT NULL,
    cadence_s        REAL NOT NULL DEFAULT 0,
    span_s           REAL NOT NULL DEFAULT 0,
    cycles           REAL NOT NULL DEFAULT 0,
    drift_arcsec_min REAL NOT NULL DEFAULT 0,
    period_s         REAL NOT NULL,
    period_sigma_s   REAL NOT NULL DEFAULT 0,  -- 0 = fixed or unknown
    period_fixed     INTEGER NOT NULL DEFAULT 0,
    harmonics_json   TEXT NOT NULL,        -- [{"k","a","b","amp","phase_deg","sigma"}] arcsec
    amp1_arcsec      REAL NOT NULL,
    phase1_deg       REAL NOT NULL,
    periodic_rms     REAL NOT NULL,
    residual_rms     REAL NOT NULL DEFAULT 0,
    peak_to_peak     REAL NOT NULL,
    guiding_active   INTEGER NOT NULL DEFAULT 0,
    pec_on           INTEGER,              -- NULL unknown, 0 off, 1 on (user-supplied)
    ra_sign          REAL NOT NULL DEFAULT 1,
    options_json     TEXT NOT NULL DEFAULT '{}',
    warnings_json    TEXT NOT NULL DEFAULT '[]',
    tool_version     TEXT NOT NULL DEFAULT '',
    notes            TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS runs_created ON runs(created_at DESC);
