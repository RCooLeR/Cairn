DELETE FROM ignored_updates
WHERE id NOT IN (
    SELECT MAX(id)
    FROM ignored_updates
    GROUP BY provider_id,
        image_ref,
        update_kind,
        COALESCE(base_image_ref, ''),
        COALESCE(project_id, ''),
        COALESCE(service_id, '')
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ignored_updates_unique ON ignored_updates(
    provider_id,
    image_ref,
    update_kind,
    COALESCE(base_image_ref, ''),
    COALESCE(project_id, ''),
    COALESCE(service_id, '')
);
