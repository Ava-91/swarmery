-- Phase doc last-modified timestamp: the mtime of the phase doc captured at
-- scan time. Since every executor edit (checkbox tick, Status flip) changes the
-- doc content and re-triggers the scan, this is a liveness signal — the Plans
-- UI shows "active Nm ago" on an in-progress phase and flags a stall when the
-- doc has been silent too long. NULL for unresolved docs.
ALTER TABLE epic_phases ADD COLUMN doc_updated_at TEXT;
