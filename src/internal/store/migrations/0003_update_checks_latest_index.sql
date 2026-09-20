CREATE INDEX IF NOT EXISTS idx_checks_latest ON image_update_checks(
    COALESCE(project_id, ''),
    provider_id,
    COALESCE(service_id, ''),
    COALESCE(container_id, ''),
    kind,
    image_ref,
    COALESCE(base_image_ref, ''),
    id
);
