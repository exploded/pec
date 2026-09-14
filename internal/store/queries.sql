-- name: UpsertFile :exec
INSERT INTO files (sha256, kind, name, size, uploaded_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(sha256) DO UPDATE SET name = excluded.name;

-- name: GetFile :one
SELECT * FROM files WHERE sha256 = ?;

-- name: InsertRun :execresult
INSERT INTO runs (
    created_at, kind, file_sha256, source_name, session_index, session_begins, equipment,
    ra_hours, dec_deg, hour_angle, alt_deg, pier_side, pixel_scale, exposure_ms,
    sample_count, cadence_s, span_s, cycles, drift_arcsec_min,
    period_s, period_sigma_s, period_fixed, harmonics_json, amp1_arcsec, phase1_deg,
    periodic_rms, residual_rms, peak_to_peak, guiding_active, pec_on, ra_sign,
    options_json, warnings_json, tool_version, notes
) VALUES (
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?
);

-- name: GetRun :one
SELECT * FROM runs WHERE id = ?;

-- name: ListRuns :many
SELECT * FROM runs ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: ListAnalyseRuns :many
SELECT * FROM runs WHERE kind = 'analyse' ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: DeleteRun :exec
DELETE FROM runs WHERE id = ?;

-- name: UpdateRunNotes :exec
UPDATE runs SET notes = ?, pec_on = ? WHERE id = ?;

-- name: InsertAnchor :execresult
INSERT INTO anchors (created_at, pec_index, at, sigma_s, source, period_s, period_sigma_s, readings, note)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAnchor :one
SELECT * FROM anchors WHERE id = ?;

-- name: ListAnchors :many
SELECT * FROM anchors ORDER BY at DESC, id DESC LIMIT ?;

-- name: DeleteAnchor :exec
DELETE FROM anchors WHERE id = ?;

-- name: InsertFit :execresult
INSERT INTO fits (
    created_at, mode, run_id, anchor_id, file_sha256, source_name, phase_ref,
    period_s, harmonics, inverted, amp1_arcsec, p2p_ticks, quant_rms, phase_err_deg,
    table_text, meta_json, warnings_json, tool_version, notes
) VALUES (
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?
);

-- name: GetFit :one
SELECT * FROM fits WHERE id = ?;

-- name: ListFits :many
SELECT * FROM fits ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: DeleteFit :exec
DELETE FROM fits WHERE id = ?;

-- name: UpdateFitNotes :exec
UPDATE fits SET notes = ? WHERE id = ?;

-- name: ListTableRuns :many
SELECT * FROM runs WHERE kind = 'table' ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: InsertCapture :execresult
INSERT INTO captures (
    created_at, anchor_id, file_sha256, source_name, started_at, ended_at, track_from, track_to,
    frames, encoder_readings, encoder_rate, encoder_rate_sig, encoder_rms, counts_per_turn,
    period_s, period_sigma_s, index_readings, index_offset, index_spread, warnings_json, notes
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?
);

-- name: GetCapture :one
SELECT * FROM captures WHERE id = ?;

-- name: ListCaptures :many
SELECT * FROM captures ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: DeleteCapture :exec
DELETE FROM captures WHERE id = ?;

-- name: GetSetting :one
SELECT value FROM settings WHERE key = ?;

-- name: SetSetting :exec
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value;

-- name: ListLiveFiles :many
SELECT f.*, (SELECT COUNT(*) FROM runs r WHERE r.file_sha256 = f.sha256) AS run_count
FROM files f
WHERE f.kind = 'phd2live'
ORDER BY f.uploaded_at DESC, f.sha256
LIMIT ?;

-- name: ListUnsavedLiveFiles :many
SELECT * FROM files f
WHERE f.kind = 'phd2live'
  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.file_sha256 = f.sha256)
ORDER BY f.uploaded_at DESC, f.sha256
LIMIT ?;

-- name: DeleteUnreferencedFile :execresult
DELETE FROM files
WHERE sha256 = ? AND kind = 'phd2live'
  AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.file_sha256 = files.sha256)
  AND NOT EXISTS (SELECT 1 FROM fits x WHERE x.file_sha256 = files.sha256);
