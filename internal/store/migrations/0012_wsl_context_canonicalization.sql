-- Windows WSL runtime identities were historically persisted with the distro's
-- display casing (for example, "wsl:Ubuntu"). Runtime binding now uses a
-- canonical lower-case identity ("wsl:ubuntu"). Normalize the durable scope so
-- existing projects and their related data remain owned by the same backend.
--
-- Only the managed Windows WSL provider is eligible. Empty/foreign contexts
-- remain quarantined. The versioned cairn_wsl_context_v1() function applies
-- the same Unicode-aware trim/lower contract as the runtime provider. Project
-- ownership collisions remain isolated; eligible tombstone/ignore aliases
-- collapse deterministically while malformed scoped rows stay quarantined.
-- Derived caches are reconstructible and are
-- discarded for every noncanonical WSL spelling so the active runtime can
-- rebuild them safely.

-- If multiple case spellings published update generations for one project,
-- neither generation can be proven authoritative. Capture those scopes before
-- changing any context, demote every current check, and remove every head. The
-- next complete update check will publish a fresh canonical generation.
CREATE TEMP TABLE _wsl_ambiguous_update_scopes (
    provider_id TEXT NOT NULL,
    context_name TEXT NOT NULL,
    project_id TEXT NOT NULL,
    PRIMARY KEY (provider_id, context_name, project_id)
);

INSERT INTO _wsl_ambiguous_update_scopes (provider_id, context_name, project_id)
SELECT provider_id, cairn_wsl_context_v1(context_name), project_id
FROM (
    SELECT provider_id, context_name, COALESCE(project_id, '') AS project_id
    FROM image_update_checks
    UNION
    SELECT provider_id, context_name, project_id
    FROM image_update_check_runs
    UNION
    SELECT provider_id, context_name, project_id
    FROM image_update_check_heads
) AS generations
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
GROUP BY provider_id, cairn_wsl_context_v1(context_name), project_id
HAVING COUNT(DISTINCT context_name) > 1;

UPDATE projects
SET context_name = cairn_wsl_context_v1(context_name)
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name)
    AND NOT EXISTS (
        SELECT 1
        FROM projects AS other
        WHERE other.id <> projects.id
            AND other.provider_id = projects.provider_id
            AND cairn_wsl_context_v1(other.context_name) = cairn_wsl_context_v1(projects.context_name)
            AND lower(trim(other.name)) = lower(trim(projects.name))
    );

WITH ranked AS (
    SELECT rowid,
        ROW_NUMBER() OVER (
            PARTITION BY provider_id, cairn_wsl_context_v1(context_name), name
            ORDER BY forgotten_at DESC, rowid DESC
        ) AS canonical_rank
    FROM forgotten_projects
    WHERE provider_id = 'windows_wsl_ubuntu'
        AND cairn_wsl_context_v1(context_name) IS NOT NULL
)
DELETE FROM forgotten_projects
WHERE rowid IN (
    SELECT rowid FROM ranked WHERE canonical_rank > 1
);

UPDATE forgotten_projects
SET context_name = cairn_wsl_context_v1(context_name)
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name);

DELETE FROM containers_cache
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name);

DELETE FROM images_cache
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name);

DELETE FROM volumes_cache
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name);

DELETE FROM networks_cache
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name);

UPDATE metrics_samples
SET context_name = cairn_wsl_context_v1(context_name)
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name)
    AND (
        COALESCE(project_id, '') = ''
        OR EXISTS (
            SELECT 1
            FROM projects AS owner
            WHERE owner.id = metrics_samples.project_id
                AND owner.provider_id = metrics_samples.provider_id
                AND owner.context_name = cairn_wsl_context_v1(metrics_samples.context_name)
        )
    )
    AND (
        COALESCE(service_id, '') = ''
        OR (
            COALESCE(project_id, '') <> ''
            AND EXISTS (
                SELECT 1
                FROM services
                WHERE services.id = metrics_samples.service_id
                    AND services.project_id = metrics_samples.project_id
            )
        )
    );

UPDATE image_lineage
SET context_name = cairn_wsl_context_v1(context_name)
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name)
    AND EXISTS (
        SELECT 1
        FROM projects AS owner
        WHERE owner.id = image_lineage.project_id
            AND owner.provider_id = image_lineage.provider_id
            AND owner.context_name = cairn_wsl_context_v1(image_lineage.context_name)
    )
    AND (
        COALESCE(service_id, '') = ''
        OR EXISTS (
            SELECT 1
            FROM services
            WHERE services.id = image_lineage.service_id
                AND services.project_id = image_lineage.project_id
        )
    );

-- Runs must move before checks and heads so their logical generation links can
-- be verified in the canonical scope. Rows with invalid project/service
-- ownership remain quarantined under their original spelling.
UPDATE image_update_check_runs
SET context_name = cairn_wsl_context_v1(context_name)
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name)
    AND EXISTS (
        SELECT 1
        FROM projects AS owner
        WHERE owner.id = image_update_check_runs.project_id
            AND owner.provider_id = image_update_check_runs.provider_id
            AND owner.context_name = cairn_wsl_context_v1(image_update_check_runs.context_name)
    )
    AND (
        service_id = ''
        OR EXISTS (
            SELECT 1
            FROM services
            WHERE services.id = image_update_check_runs.service_id
                AND services.project_id = image_update_check_runs.project_id
        )
    );

UPDATE image_update_checks
SET is_current = 0
WHERE EXISTS (
    SELECT 1
    FROM _wsl_ambiguous_update_scopes AS ambiguous
    WHERE ambiguous.provider_id = image_update_checks.provider_id
        AND ambiguous.context_name = cairn_wsl_context_v1(image_update_checks.context_name)
        AND ambiguous.project_id = COALESCE(image_update_checks.project_id, '')
);

DELETE FROM image_update_check_heads
WHERE EXISTS (
    SELECT 1
    FROM _wsl_ambiguous_update_scopes AS ambiguous
    WHERE ambiguous.provider_id = image_update_check_heads.provider_id
        AND ambiguous.context_name = cairn_wsl_context_v1(image_update_check_heads.context_name)
        AND ambiguous.project_id = image_update_check_heads.project_id
);

UPDATE image_update_checks
SET context_name = cairn_wsl_context_v1(context_name)
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name)
    AND (
        COALESCE(project_id, '') = ''
        OR EXISTS (
            SELECT 1
            FROM projects AS owner
            WHERE owner.id = image_update_checks.project_id
                AND owner.provider_id = image_update_checks.provider_id
                AND owner.context_name = cairn_wsl_context_v1(image_update_checks.context_name)
        )
    )
    AND (
        COALESCE(service_id, '') = ''
        OR (
            COALESCE(project_id, '') <> ''
            AND EXISTS (
                SELECT 1
                FROM services
                WHERE services.id = image_update_checks.service_id
                    AND services.project_id = image_update_checks.project_id
            )
        )
    )
    AND (
        lineage_id IS NULL
        OR EXISTS (
            SELECT 1
            FROM image_lineage AS lineage
            WHERE lineage.id = image_update_checks.lineage_id
                AND lineage.provider_id = image_update_checks.provider_id
                AND lineage.context_name = cairn_wsl_context_v1(image_update_checks.context_name)
                AND lineage.project_id = image_update_checks.project_id
                AND COALESCE(lineage.service_id, '') = COALESCE(image_update_checks.service_id, '')
        )
    )
    AND (
        base_image_ref_id IS NULL
        OR EXISTS (
            SELECT 1
            FROM base_image_refs AS base_ref
            JOIN image_lineage AS lineage ON lineage.id = base_ref.lineage_id
            WHERE base_ref.id = image_update_checks.base_image_ref_id
                AND image_update_checks.lineage_id = lineage.id
                AND lineage.provider_id = image_update_checks.provider_id
                AND lineage.context_name = cairn_wsl_context_v1(image_update_checks.context_name)
                AND lineage.project_id = image_update_checks.project_id
                AND COALESCE(lineage.service_id, '') = COALESCE(image_update_checks.service_id, '')
        )
    )
    AND (
        generation_id = 0
        OR EXISTS (
            SELECT 1
            FROM image_update_check_runs AS generation
            WHERE generation.id = image_update_checks.generation_id
                AND generation.provider_id = image_update_checks.provider_id
                AND generation.context_name = cairn_wsl_context_v1(image_update_checks.context_name)
                AND generation.project_id = image_update_checks.project_id
                AND (
                    generation.service_id = ''
                    OR generation.service_id = COALESCE(image_update_checks.service_id, '')
                )
        )
    );

WITH ranked AS (
    SELECT id,
        ROW_NUMBER() OVER (
            PARTITION BY provider_id,
                cairn_wsl_context_v1(context_name),
                image_ref,
                update_kind,
                COALESCE(base_image_ref, ''),
                COALESCE(project_id, ''),
                COALESCE(service_id, '')
            ORDER BY created_at DESC, id DESC
        ) AS canonical_rank
    FROM ignored_updates
    WHERE provider_id = 'windows_wsl_ubuntu'
        AND cairn_wsl_context_v1(context_name) IS NOT NULL
        AND (
            COALESCE(project_id, '') = ''
            OR EXISTS (
                SELECT 1
                FROM projects AS owner
                WHERE owner.id = ignored_updates.project_id
                    AND owner.provider_id = ignored_updates.provider_id
                    AND owner.context_name = cairn_wsl_context_v1(ignored_updates.context_name)
            )
        )
        AND (
            COALESCE(service_id, '') = ''
            OR (
                COALESCE(project_id, '') <> ''
                AND EXISTS (
                    SELECT 1
                    FROM services
                    WHERE services.id = ignored_updates.service_id
                        AND services.project_id = ignored_updates.project_id
                )
            )
        )
)
DELETE FROM ignored_updates
WHERE id IN (
    SELECT id FROM ranked WHERE canonical_rank > 1
);

UPDATE ignored_updates
SET context_name = cairn_wsl_context_v1(context_name)
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name)
    AND (
        COALESCE(project_id, '') = ''
        OR EXISTS (
            SELECT 1
            FROM projects AS owner
            WHERE owner.id = ignored_updates.project_id
                AND owner.provider_id = ignored_updates.provider_id
                AND owner.context_name = cairn_wsl_context_v1(ignored_updates.context_name)
        )
    )
    AND (
        COALESCE(service_id, '') = ''
        OR (
            COALESCE(project_id, '') <> ''
            AND EXISTS (
                SELECT 1
                FROM services
                WHERE services.id = ignored_updates.service_id
                    AND services.project_id = ignored_updates.project_id
            )
        )
    );

UPDATE update_history
SET context_name = cairn_wsl_context_v1(context_name)
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name)
    AND (
        COALESCE(project_id, '') = ''
        OR EXISTS (
            SELECT 1
            FROM projects AS owner
            WHERE owner.id = update_history.project_id
                AND owner.provider_id = update_history.provider_id
                AND owner.context_name = cairn_wsl_context_v1(update_history.context_name)
        )
    )
    AND (
        COALESCE(service_id, '') = ''
        OR (
            COALESCE(project_id, '') <> ''
            AND EXISTS (
                SELECT 1
                FROM services
                WHERE services.id = update_history.service_id
                    AND services.project_id = update_history.project_id
            )
        )
    );

UPDATE image_update_check_heads
SET context_name = cairn_wsl_context_v1(context_name)
WHERE provider_id = 'windows_wsl_ubuntu'
    AND cairn_wsl_context_v1(context_name) IS NOT NULL
    AND context_name <> cairn_wsl_context_v1(context_name)
    AND NOT EXISTS (
        SELECT 1
        FROM image_update_check_heads AS other
        WHERE other.rowid <> image_update_check_heads.rowid
            AND other.provider_id = image_update_check_heads.provider_id
            AND cairn_wsl_context_v1(other.context_name) = cairn_wsl_context_v1(image_update_check_heads.context_name)
            AND other.project_id = image_update_check_heads.project_id
            AND other.service_id = image_update_check_heads.service_id
    )
    AND EXISTS (
        SELECT 1
        FROM projects AS owner
        WHERE owner.id = image_update_check_heads.project_id
            AND owner.provider_id = image_update_check_heads.provider_id
            AND owner.context_name = cairn_wsl_context_v1(image_update_check_heads.context_name)
    )
    AND (
        service_id = ''
        OR EXISTS (
            SELECT 1
            FROM services
            WHERE services.id = image_update_check_heads.service_id
                AND services.project_id = image_update_check_heads.project_id
        )
    )
    AND EXISTS (
        SELECT 1
        FROM image_update_check_runs AS last_run
        WHERE last_run.id = image_update_check_heads.last_run_id
            AND last_run.provider_id = image_update_check_heads.provider_id
            AND last_run.context_name = cairn_wsl_context_v1(image_update_check_heads.context_name)
            AND last_run.project_id = image_update_check_heads.project_id
            AND last_run.service_id = image_update_check_heads.service_id
    );

DROP TABLE _wsl_ambiguous_update_scopes;
