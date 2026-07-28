-- Phase doc-status marker: an executor announces "this phase is being worked on
-- right now" by writing a `Status: In progress` line in the phase doc's header
-- block (directly under the H1). The wsingest scanner parses it into this
-- column; the Plans UI shows the phase as in-progress before the first
-- acceptance-criteria checkbox is ticked. NULL = no recognizable marker.
-- Values: pending | in_progress | done (normalized by the scanner).
ALTER TABLE epic_phases ADD COLUMN doc_status TEXT;
