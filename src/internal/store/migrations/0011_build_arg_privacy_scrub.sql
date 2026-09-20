-- Marker recorded only after Migrate has scrubbed live rows, rebuilt legacy
-- free pages, and truncated the WAL before creating the schema backup.
SELECT 1;
