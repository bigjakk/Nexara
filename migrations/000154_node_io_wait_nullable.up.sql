-- Automatic in-place upgrade — no manual steps, no operator action required.
-- Lets nodes.io_wait be NULL, meaning "not reported": the collector writes NULL
-- when the node status call failed, or on a fresh pveproxy worker's first answer,
-- whose wait is 0 because it has no earlier /proc/stat reading to measure from.
-- Until now both were stored as 0, which the UI could not tell from a real 0%.
-- Only the constraint goes; no row changes, and existing values stay as they are
-- until the next collector pass rewrites them.

ALTER TABLE nodes ALTER COLUMN io_wait DROP NOT NULL;
