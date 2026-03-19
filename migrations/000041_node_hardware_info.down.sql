ALTER TABLE nodes
    DROP COLUMN IF EXISTS cpu_model,
    DROP COLUMN IF EXISTS cpu_cores,
    DROP COLUMN IF EXISTS cpu_sockets,
    DROP COLUMN IF EXISTS cpu_threads,
    DROP COLUMN IF EXISTS cpu_mhz,
    DROP COLUMN IF EXISTS kernel_version;
