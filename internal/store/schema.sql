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

-- A PEC index reading with the wall-clock instant it was seen. Typed anchors
-- (milestone 3) have one reading and a timing sigma of about a reaction
-- time; watch anchors (milestone 4) carry the index-rate fit as well.
CREATE TABLE IF NOT EXISTS anchors (
    id             INTEGER PRIMARY KEY,
    created_at     TEXT NOT NULL,             -- RFC3339
    pec_index      INTEGER NOT NULL,          -- 0..entries-1
    at             TEXT NOT NULL,             -- RFC3339 with offset, server clock
    sigma_s        REAL NOT NULL DEFAULT 2,   -- timing uncertainty, seconds
    source         TEXT NOT NULL DEFAULT 'typed',  -- 'typed' | 'uia' | 'pixels'
    period_s       REAL NOT NULL DEFAULT 0,   -- from an index-rate fit; 0 = none
    period_sigma_s REAL NOT NULL DEFAULT 0,
    readings       INTEGER NOT NULL DEFAULT 1,
    note           TEXT NOT NULL DEFAULT ''
);

-- A generated table. The table text and provenance are stored in full so
-- a fit outlives the run or anchor it came from.
CREATE TABLE IF NOT EXISTS fits (
    id            INTEGER PRIMARY KEY,
    created_at    TEXT NOT NULL,              -- RFC3339
    mode          TEXT NOT NULL,              -- 'tcs' | 'index'
    run_id        INTEGER REFERENCES runs(id) ON DELETE SET NULL,
    anchor_id     INTEGER REFERENCES anchors(id) ON DELETE SET NULL,
    file_sha256   TEXT NOT NULL REFERENCES files(sha256),
    source_name   TEXT NOT NULL,
    phase_ref     TEXT NOT NULL,              -- human-readable
    period_s      REAL NOT NULL,
    harmonics     INTEGER NOT NULL,
    inverted      INTEGER NOT NULL,
    amp1_arcsec   REAL NOT NULL,
    p2p_ticks     INTEGER NOT NULL,
    quant_rms     REAL NOT NULL,
    phase_err_deg REAL NOT NULL DEFAULT 0,
    table_text    TEXT NOT NULL,
    meta_json     TEXT NOT NULL,
    warnings_json TEXT NOT NULL DEFAULT '[]',
    tool_version  TEXT NOT NULL DEFAULT '',
    notes         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS fits_created ON fits(created_at DESC);

-- A decoded USB capture of the TheSkyX <-> MKS 4000 link: the encoder
-- rate gives the worm period, the PEC index readings give the anchor. The
-- anchor itself lives in anchors (source 'capture'); this row keeps the
-- numbers behind it.
CREATE TABLE IF NOT EXISTS captures (
    id               INTEGER PRIMARY KEY,
    created_at       TEXT NOT NULL,
    anchor_id        INTEGER REFERENCES anchors(id) ON DELETE SET NULL,
    file_sha256      TEXT NOT NULL REFERENCES files(sha256),
    source_name      TEXT NOT NULL,
    started_at       TEXT NOT NULL,         -- RFC3339, capture clock
    ended_at         TEXT NOT NULL,
    track_from       TEXT NOT NULL DEFAULT '',
    track_to         TEXT NOT NULL DEFAULT '',
    frames           INTEGER NOT NULL,
    encoder_readings INTEGER NOT NULL,
    encoder_rate     REAL NOT NULL,         -- counts/s
    encoder_rate_sig REAL NOT NULL,
    encoder_rms      REAL NOT NULL,
    counts_per_turn  REAL NOT NULL,
    period_s         REAL NOT NULL,
    period_sigma_s   REAL NOT NULL,
    index_readings   INTEGER NOT NULL,
    index_offset     REAL NOT NULL DEFAULT 0,
    index_spread     REAL NOT NULL DEFAULT 0,
    warnings_json    TEXT NOT NULL DEFAULT '[]',
    notes            TEXT NOT NULL DEFAULT ''
);
