CREATE TABLE forgotten_projects (
    provider_id TEXT NOT NULL,
    context_name TEXT NOT NULL,
    name TEXT NOT NULL,
    project_id TEXT NOT NULL,
    forgotten_at DATETIME NOT NULL,
    PRIMARY KEY (provider_id, context_name, name)
);

CREATE INDEX idx_forgotten_projects_id ON forgotten_projects(project_id);
