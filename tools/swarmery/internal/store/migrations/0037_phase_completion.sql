-- Phase completion report: the `## Completion Report` section an executor
-- appends to the phase doc when the phase's last checkbox is ticked (what
-- shipped, commits, verification results, deviations). Extracted verbatim by
-- the wsingest scanner; the Plans UI opens it in a summary modal on done
-- phases. NULL when the doc has no such section (or it is empty).
ALTER TABLE epic_phases ADD COLUMN completion_report TEXT;
