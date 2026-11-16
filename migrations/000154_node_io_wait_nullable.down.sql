-- A NULL (not reported) reads as 0 again, as it did before 000154.
UPDATE nodes SET io_wait = 0 WHERE io_wait IS NULL;
ALTER TABLE nodes ALTER COLUMN io_wait SET NOT NULL;
