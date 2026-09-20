-- Metrics samples belong to one exact Docker runtime. Provider IDs alone do
-- not distinguish multiple contexts/backends owned by the same provider.
--
-- Existing samples predate runtime-scope persistence. Keep them with an empty
-- context so they remain unclaimed. Scoped readers never expose them; global
-- retention keeps the blank context as a separate quarantine bucket and may
-- age it out. Repository writes require a non-empty runtime scope.

ALTER TABLE metrics_samples
    ADD COLUMN context_name TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS idx_metrics_container_time;
DROP INDEX IF EXISTS idx_metrics_project_time;
DROP INDEX IF EXISTS idx_metrics_res_time;

CREATE INDEX idx_metrics_container_time ON metrics_samples(
    provider_id,
    context_name,
    container_id,
    sampled_at
);
CREATE INDEX idx_metrics_project_time ON metrics_samples(
    provider_id,
    context_name,
    project_id,
    sampled_at
);
CREATE INDEX idx_metrics_res_time ON metrics_samples(
    provider_id,
    context_name,
    resolution,
    sampled_at
);
