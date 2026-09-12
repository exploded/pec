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
