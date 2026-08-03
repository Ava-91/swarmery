-- 0049: board task labels — free-form marks on a card, JSON array of lowercase
-- strings ('["jira-ticket"]'). Mirrors projects.tags (0015): storage is a TEXT
-- column holding compact JSON, not a join table — the board reads every card in
-- one query and a second table would buy nothing but a join.
--
-- Existing cards get '[]' and render exactly as before.

ALTER TABLE tasks ADD COLUMN labels TEXT NOT NULL DEFAULT '[]';
