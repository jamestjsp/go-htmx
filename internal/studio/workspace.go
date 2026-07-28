package studio

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Studio) CurrentWorkspace(ctx context.Context) (Workspace, error) {
	var projectID, flowID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT projects.id, flows.id
		FROM projects
		JOIN flows ON flows.project_id = projects.id
		ORDER BY projects.id, flows.position, flows.id
		LIMIT 1`,
	).Scan(&projectID, &flowID)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("load current workspace: %w", err)
	}
	return s.Workspace(ctx, projectID, flowID)
}

func (s *Studio) ProjectWorkspace(ctx context.Context, projectID int64) (Workspace, error) {
	var flowID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id
		FROM flows
		WHERE project_id = ?
		ORDER BY position, id
		LIMIT 1`,
		projectID,
	).Scan(&flowID)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("open project workspace: %w", err)
	}
	return s.Workspace(ctx, projectID, flowID)
}

func (s *Studio) Workspace(ctx context.Context, projectID, flowID int64) (Workspace, error) {
	var workspace Workspace
	var created, updated string
	err := s.db.QueryRowContext(ctx, `
		SELECT projects.id, projects.name, projects.created_at, projects.updated_at
		FROM projects
		JOIN flows ON flows.project_id = projects.id
		WHERE projects.id = ? AND flows.id = ?`,
		projectID, flowID,
	).Scan(
		&workspace.Project.ID, &workspace.Project.Name, &created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("load workspace project: %w", err)
	}
	workspace.Project.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	workspace.Project.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)

	workspace.Projects, err = s.projects(ctx)
	if err != nil {
		return Workspace{}, err
	}
	workspace.Flows, err = s.projectFlows(ctx, projectID)
	if err != nil {
		return Workspace{}, err
	}
	workspace.Snapshot, err = s.snapshot(ctx, flowID)
	if err != nil {
		return Workspace{}, err
	}
	workspace.Analysis = s.analysisWorkspace(workspace.Snapshot)
	return workspace, nil
}

func (s *Studio) projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, created_at, updated_at
		FROM projects
		ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var project Project
		var created, updated string
		if err := rows.Scan(
			&project.ID, &project.Name, &created, &updated,
		); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		project.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		project.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read projects: %w", err)
	}
	return projects, nil
}

// flowSelect is the single definition of a flowsheet row for every list the
// interface draws — the tab strip and the register both read it, so the amber
// dot in one cannot contradict the amber dot in the other. NeedsRun compares
// the two RFC3339Nano timestamps as raw text, exactly as `snapshot` does when
// it picks the run behind the chart; converting either side to a datetime here
// is how the dot and the dock would start to disagree.
const flowSelect = `
	SELECT
		flows.id, flows.project_id, flows.name,
		flows.created_at, flows.updated_at, flows.model_updated_at,
		NOT EXISTS (
			SELECT 1 FROM simulation_runs
			WHERE simulation_runs.flow_id = flows.id
				AND simulation_runs.created_at >= flows.model_updated_at
		) AS needs_run
	FROM flows`

// projectFlows lists one project's tab strip.
func (s *Studio) projectFlows(ctx context.Context, projectID int64) ([]Flow, error) {
	rows, err := s.db.QueryContext(ctx, flowSelect+`
		WHERE flows.project_id = ?
		ORDER BY flows.position, flows.id`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list project flows: %w", err)
	}
	return scanFlows(rows)
}

func scanFlows(rows *sql.Rows) ([]Flow, error) {
	defer rows.Close()
	var flows []Flow
	for rows.Next() {
		var flow Flow
		var created, updated, modelUpdated string
		if err := rows.Scan(
			&flow.ID, &flow.ProjectID, &flow.Name, &created, &updated, &modelUpdated,
			&flow.NeedsRun,
		); err != nil {
			return nil, fmt.Errorf("scan flow: %w", err)
		}
		flow.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		flow.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		flow.ModelUpdatedAt, _ = time.Parse(time.RFC3339Nano, modelUpdated)
		flows = append(flows, flow)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read flows: %w", err)
	}
	return flows, nil
}

// Register is the projects home. Every project arrives with the flowsheets its
// row expands to reveal, so expansion costs no request, and the sheet count is
// the length of that same list rather than a separate tally that could
// disagree with what the row shows.
type Register struct {
	Projects []RegisterEntry
}

// RegisterEntry is one project's row in the register.
type RegisterEntry struct {
	Project Project
	// Flows is the project's tab strip, in the order the workbench shows it,
	// so a chip in the register links straight to the sheet it names.
	Flows []Flow
	// EditedAt is the last time anything in the project moved: its own name,
	// or any of its flowsheets. A block edit touches only the flowsheet, so a
	// row reporting the project's own timestamp would sit still while the
	// user worked.
	EditedAt time.Time
}

// FlowCount is how many flowsheets the project holds.
func (e RegisterEntry) FlowCount() int {
	return len(e.Flows)
}

// Register reads the projects home in two queries whatever the number of
// projects: one for the projects, one for every flowsheet grouped by project.
// An empty database is an empty register, not an error.
func (s *Studio) Register(ctx context.Context) (Register, error) {
	projects, err := s.projects(ctx)
	if err != nil {
		return Register{}, err
	}
	grouped, err := s.flowsByProject(ctx)
	if err != nil {
		return Register{}, err
	}
	register := Register{Projects: make([]RegisterEntry, 0, len(projects))}
	for _, project := range projects {
		entry := RegisterEntry{
			Project:  project,
			Flows:    grouped[project.ID],
			EditedAt: project.UpdatedAt,
		}
		for _, flow := range entry.Flows {
			if flow.UpdatedAt.After(entry.EditedAt) {
				entry.EditedAt = flow.UpdatedAt
			}
		}
		register.Projects = append(register.Projects, entry)
	}
	return register, nil
}

// flowsByProject reads every project's tab strip in one query, so the register
// asks the database once rather than once per project.
func (s *Studio) flowsByProject(ctx context.Context) (map[int64][]Flow, error) {
	rows, err := s.db.QueryContext(ctx, flowSelect+`
		ORDER BY flows.project_id, flows.position, flows.id`)
	if err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	flows, err := scanFlows(rows)
	if err != nil {
		return nil, err
	}
	grouped := make(map[int64][]Flow)
	for _, flow := range flows {
		grouped[flow.ProjectID] = append(grouped[flow.ProjectID], flow)
	}
	return grouped, nil
}
