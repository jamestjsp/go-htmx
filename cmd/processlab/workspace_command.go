package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type projectClientRecord struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	FlowCount int    `json:"flowCount"`
}

type flowClientRecord struct {
	ID             int64  `json:"id"`
	ProjectID      int64  `json:"projectId"`
	ProjectName    string `json:"projectName"`
	Name           string `json:"name"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	ModelUpdatedAt string `json:"modelUpdatedAt"`
	NeedsRun       bool   `json:"needsRun"`
	BlockCount     int    `json:"blockCount"`
}

type workspaceClientRecord struct {
	Project    projectClientRecord `json:"project"`
	Flows      []flowClientRecord  `json:"flows"`
	Snapshot   flowClientRecord    `json:"snapshot"`
	BlockCount int                 `json:"blockCount"`
}

type workspaceNameRequest struct {
	Name string `json:"name"`
}

type workspaceDeleteRequest struct {
	Force bool `json:"force"`
}

type flowReorderRequest struct {
	FlowIDs []int64 `json:"flowIds"`
}

func newProjectCommand() *command {
	var jsonOutput, force bool
	return &command{
		name:    "project",
		summary: "List and manage Process Lab projects",
		children: []*command{
			{
				name: "list", summary: "List projects",
				flags: []commandFlag{{name: "json", typeName: "bool", defaultValue: "false", usage: "write machine-readable output", register: func(set *flag.FlagSet) {
					set.BoolVar(&jsonOutput, "json", false, "write machine-readable output")
				}}},
				run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
					client, err := newAPIClient(options.server, options.timeout)
					if err != nil {
						return err
					}
					return runProjectList(ctx, client, options, jsonOutput || options.json, stdout)
				},
			},
			{
				name: "show", summary: "Show a project", arguments: []commandArgument{{name: "project id", description: "project identifier", required: true}},
				flags: []commandFlag{{name: "json", typeName: "bool", defaultValue: "false", usage: "write machine-readable output", register: func(set *flag.FlagSet) {
					set.BoolVar(&jsonOutput, "json", false, "write machine-readable output")
				}}},
				run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
					client, err := newAPIClient(options.server, options.timeout)
					if err != nil {
						return err
					}
					return runProjectShow(ctx, client, args, options, jsonOutput || options.json, stdout)
				},
			},
			{
				name: "create", summary: "Create a project", arguments: []commandArgument{{name: "name", description: "project name", required: true}},
				flags: []commandFlag{{name: "json", typeName: "bool", defaultValue: "false", usage: "write machine-readable output", register: func(set *flag.FlagSet) {
					set.BoolVar(&jsonOutput, "json", false, "write machine-readable output")
				}}},
				run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
					client, err := newAPIClient(options.server, options.timeout)
					if err != nil {
						return err
					}
					return runProjectCreate(ctx, client, args, options, jsonOutput || options.json, stdout)
				},
			},
			{
				name: "rename", summary: "Rename a project", arguments: []commandArgument{{name: "project id", description: "project identifier", required: true}, {name: "name", description: "new project name", required: true}},
				flags: []commandFlag{{name: "json", typeName: "bool", defaultValue: "false", usage: "write machine-readable output", register: func(set *flag.FlagSet) {
					set.BoolVar(&jsonOutput, "json", false, "write machine-readable output")
				}}},
				run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
					client, err := newAPIClient(options.server, options.timeout)
					if err != nil {
						return err
					}
					return runProjectRename(ctx, client, args, options, jsonOutput || options.json, stdout)
				},
			},
			{
				name: "delete", summary: "Delete a project", arguments: []commandArgument{{name: "project id", description: "project identifier", required: true}},
				flags: []commandFlag{
					{name: "json", typeName: "bool", defaultValue: "false", usage: "write machine-readable output", register: func(set *flag.FlagSet) { set.BoolVar(&jsonOutput, "json", false, "write machine-readable output") }},
					{name: "force", typeName: "bool", defaultValue: "false", usage: "confirm cascading deletion", register: func(set *flag.FlagSet) { set.BoolVar(&force, "force", false, "confirm cascading deletion") }},
				},
				run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
					client, err := newAPIClient(options.server, options.timeout)
					if err != nil {
						return err
					}
					return runProjectDelete(ctx, client, args, options, jsonOutput || options.json, force, stdout)
				},
			},
		},
	}
}

func newFlowCommand() *command {
	var jsonOutput, dryRun, force bool
	var projectID int64
	clientRun := func(ctx context.Context, options globalOptions, action func(*apiClient) error) error {
		client, err := newAPIClient(options.server, options.timeout)
		if err != nil {
			return err
		}
		return action(client)
	}
	return &command{
		name: "flow", summary: "List and manage flowsheets", children: []*command{
			{name: "list", summary: "List flowsheets", flags: []commandFlag{commandInt64Flag("project", "id", 0, "filter by project id", &projectID), commandBoolFlag("json", "write machine-readable output", &jsonOutput)}, run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return clientRun(ctx, options, func(client *apiClient) error {
					return runFlowList(ctx, client, options, projectID, jsonOutput || options.json, stdout)
				})
			}},
			{name: "dump", summary: "Dump a declarative flowsheet document", flags: []commandFlag{commandInt64Flag("flow", "id", 0, "flowsheet id", &projectID), commandBoolFlag("json", "write machine-readable output", &jsonOutput)}, run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return clientRun(ctx, options, func(client *apiClient) error {
					return runFlowDump(ctx, client, options, projectID, jsonOutput || options.json, stdout)
				})
			}},
			{name: "apply", summary: "Apply a declarative flowsheet document", flags: []commandFlag{commandInt64Flag("flow", "id", 0, "flowsheet id", &projectID), commandBoolFlag("json", "write machine-readable output", &jsonOutput), commandBoolFlag("dry-run", "show reconciliation without changing the flowsheet", &dryRun)}, run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return clientRun(ctx, options, func(client *apiClient) error {
					return runFlowApply(ctx, client, options, projectID, jsonOutput || options.json, dryRun, stdout)
				})
			}},
			{name: "show", summary: "Show a flowsheet", arguments: []commandArgument{{name: "flow id", description: "flowsheet identifier", required: true}}, flags: []commandFlag{commandBoolFlag("json", "write machine-readable output", &jsonOutput)}, run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return clientRun(ctx, options, func(client *apiClient) error {
					return runFlowShow(ctx, client, args, options, jsonOutput || options.json, stdout)
				})
			}},
			{name: "create", summary: "Create a flowsheet", arguments: []commandArgument{{name: "name", description: "flowsheet name", required: true}}, flags: []commandFlag{commandInt64Flag("project", "id", 0, "project id", &projectID), commandBoolFlag("json", "write machine-readable output", &jsonOutput)}, run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return clientRun(ctx, options, func(client *apiClient) error {
					return runFlowCreate(ctx, client, args, options, projectID, jsonOutput || options.json, stdout)
				})
			}},
			{name: "rename", summary: "Rename a flowsheet", arguments: []commandArgument{{name: "flow id", description: "flowsheet identifier", required: true}, {name: "name", description: "new flowsheet name", required: true}}, flags: []commandFlag{commandBoolFlag("json", "write machine-readable output", &jsonOutput)}, run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return clientRun(ctx, options, func(client *apiClient) error {
					return runFlowRename(ctx, client, args, options, jsonOutput || options.json, stdout)
				})
			}},
			{name: "duplicate", summary: "Duplicate a flowsheet", arguments: []commandArgument{{name: "flow id", description: "flowsheet identifier", required: true}}, flags: []commandFlag{commandBoolFlag("json", "write machine-readable output", &jsonOutput)}, run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return clientRun(ctx, options, func(client *apiClient) error {
					return runFlowDuplicate(ctx, client, args, options, jsonOutput || options.json, stdout)
				})
			}},
			{name: "delete", summary: "Delete a flowsheet", arguments: []commandArgument{{name: "flow id", description: "flowsheet identifier", required: true}}, flags: []commandFlag{commandBoolFlag("json", "write machine-readable output", &jsonOutput), commandBoolFlag("force", "confirm deletion of blocks in the flowsheet", &force)}, run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return clientRun(ctx, options, func(client *apiClient) error {
					return runFlowDelete(ctx, client, args, options, jsonOutput || options.json, force, stdout)
				})
			}},
			{name: "reorder", summary: "Reorder a project's flowsheets", arguments: []commandArgument{{name: "flow id", description: "flowsheet identifier", required: true}}, variadic: true, flags: []commandFlag{commandInt64Flag("project", "id", 0, "project id", &projectID), commandBoolFlag("json", "write machine-readable output", &jsonOutput)}, run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return clientRun(ctx, options, func(client *apiClient) error {
					return runFlowReorder(ctx, client, args, options, projectID, jsonOutput || options.json, stdout)
				})
			}},
		},
	}
}
func runProjectList(ctx context.Context, client *apiClient, options globalOptions, jsonOutput bool, stdout io.Writer) error {
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodGet, "/projects", nil, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var projects []projectClientRecord
	if err := json.Unmarshal(raw, &projects); err != nil {
		return fmt.Errorf("decode projects: %w", err)
	}
	for _, project := range projects {
		fmt.Fprintf(stdout, "%d\t%s\t%d flows\n", project.ID, project.Name, project.FlowCount)
	}
	return nil
}

func runProjectShow(ctx context.Context, client *apiClient, args []string, options globalOptions, jsonOutput bool, stdout io.Writer) error {
	projectID, err := commandID(args[0], "project id")
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodGet, "/projects/"+strconv.FormatInt(projectID, 10), nil, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var workspace workspaceClientRecord
	if err := json.Unmarshal(raw, &workspace); err != nil {
		return fmt.Errorf("decode project: %w", err)
	}
	fmt.Fprintf(stdout, "%d\t%s\t%d flows\n", workspace.Project.ID, workspace.Project.Name, len(workspace.Flows))
	return nil
}

func runProjectCreate(ctx context.Context, client *apiClient, args []string, options globalOptions, jsonOutput bool, stdout io.Writer) error {
	return runWorkspaceAction(ctx, client, http.MethodPost, "/projects", workspaceNameRequest{Name: args[0]}, jsonOutput, stdout, "project")
}

func runProjectRename(ctx context.Context, client *apiClient, args []string, options globalOptions, jsonOutput bool, stdout io.Writer) error {
	projectID, err := commandID(args[0], "project id")
	if err != nil {
		return err
	}
	return runWorkspaceAction(ctx, client, http.MethodPut, "/projects/"+strconv.FormatInt(projectID, 10)+"/name", workspaceNameRequest{Name: args[1]}, jsonOutput, stdout, "project")
}

func runProjectDelete(ctx context.Context, client *apiClient, args []string, options globalOptions, jsonOutput, force bool, stdout io.Writer) error {
	projectID, err := commandID(args[0], "project id")
	if err != nil {
		return err
	}
	var input any
	if force {
		input = workspaceDeleteRequest{Force: true}
	}
	return runWorkspaceAction(ctx, client, http.MethodDelete, "/projects/"+strconv.FormatInt(projectID, 10), input, jsonOutput, stdout, "project")
}

func runFlowList(ctx context.Context, client *apiClient, options globalOptions, projectID int64, jsonOutput bool, stdout io.Writer) error {
	path := "/flows"
	if projectID != 0 {
		if projectID < 0 {
			return usagef("project id must be positive")
		}
		path += "?project=" + strconv.FormatInt(projectID, 10)
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var flows []flowClientRecord
	if err := json.Unmarshal(raw, &flows); err != nil {
		return fmt.Errorf("decode flows: %w", err)
	}
	for _, flow := range flows {
		stale := ""
		if flow.NeedsRun {
			stale = " stale"
		}
		fmt.Fprintf(stdout, "%d\t%s\t%s\t%s%s\n", flow.ID, flow.ProjectName, flow.Name, "", stale)
	}
	return nil
}

func runFlowShow(ctx context.Context, client *apiClient, args []string, options globalOptions, jsonOutput bool, stdout io.Writer) error {
	flowID, err := commandID(args[0], "flowsheet id")
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodGet, "/flows/"+strconv.FormatInt(flowID, 10), nil, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var workspace workspaceClientRecord
	if err := json.Unmarshal(raw, &workspace); err != nil {
		return fmt.Errorf("decode flowsheet: %w", err)
	}
	printFlow(stdout, workspace.Snapshot)
	return nil
}

func runFlowCreate(ctx context.Context, client *apiClient, args []string, options globalOptions, projectID int64, jsonOutput bool, stdout io.Writer) error {
	if projectID <= 0 {
		return usagef("processlab flow create: --project is required")
	}
	return runWorkspaceAction(ctx, client, http.MethodPost, "/projects/"+strconv.FormatInt(projectID, 10)+"/flows", workspaceNameRequest{Name: args[0]}, jsonOutput, stdout, "flow")
}

func runFlowRename(ctx context.Context, client *apiClient, args []string, options globalOptions, jsonOutput bool, stdout io.Writer) error {
	flowID, err := commandID(args[0], "flowsheet id")
	if err != nil {
		return err
	}
	return runWorkspaceAction(ctx, client, http.MethodPut, "/flows/"+strconv.FormatInt(flowID, 10)+"/name", workspaceNameRequest{Name: args[1]}, jsonOutput, stdout, "flow")
}

func runFlowDuplicate(ctx context.Context, client *apiClient, args []string, options globalOptions, jsonOutput bool, stdout io.Writer) error {
	flowID, err := commandID(args[0], "flowsheet id")
	if err != nil {
		return err
	}
	return runWorkspaceAction(ctx, client, http.MethodPost, "/flows/"+strconv.FormatInt(flowID, 10)+"/duplicate", nil, jsonOutput, stdout, "flow")
}

func runFlowDelete(ctx context.Context, client *apiClient, args []string, options globalOptions, jsonOutput, force bool, stdout io.Writer) error {
	flowID, err := commandID(args[0], "flowsheet id")
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodGet, "/flows/"+strconv.FormatInt(flowID, 10), nil, &raw); err != nil {
		return err
	}
	var before workspaceClientRecord
	if err := json.Unmarshal(raw, &before); err != nil {
		return fmt.Errorf("decode flowsheet: %w", err)
	}
	if before.BlockCount > 0 && !force {
		return fmt.Errorf("flowsheet %d contains %d blocks; use --force to delete it", flowID, before.BlockCount)
	}
	var input any
	if force {
		input = workspaceDeleteRequest{Force: true}
	}
	return runWorkspaceAction(ctx, client, http.MethodDelete, "/flows/"+strconv.FormatInt(flowID, 10), input, jsonOutput, stdout, "flow")
}

func runFlowReorder(ctx context.Context, client *apiClient, args []string, options globalOptions, projectID int64, jsonOutput bool, stdout io.Writer) error {
	if projectID <= 0 {
		return usagef("processlab flow reorder: --project is required")
	}
	if len(args) == 0 {
		return usagef("processlab flow reorder: expected at least one flowsheet id")
	}
	flowIDs := make([]int64, len(args))
	for index, argument := range args {
		id, err := commandID(argument, "flowsheet id")
		if err != nil {
			return err
		}
		flowIDs[index] = id
	}
	return runWorkspaceAction(ctx, client, http.MethodPatch, "/projects/"+strconv.FormatInt(projectID, 10)+"/flows/order", flowReorderRequest{FlowIDs: flowIDs}, jsonOutput, stdout, "flow")
}

func runWorkspaceAction(ctx context.Context, client *apiClient, method, path string, input any, jsonOutput bool, stdout io.Writer, noun string) error {
	var raw json.RawMessage
	if err := client.request(ctx, method, path, input, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var workspace workspaceClientRecord
	if err := json.Unmarshal(raw, &workspace); err != nil {
		return fmt.Errorf("decode %s action: %w", noun, err)
	}
	if noun == "project" {
		fmt.Fprintln(stdout, workspace.Project.ID)
	} else {
		fmt.Fprintln(stdout, workspace.Snapshot.ID)
	}
	return nil
}

func printFlow(w io.Writer, flow flowClientRecord) {
	stale := ""
	if flow.NeedsRun {
		stale = " stale"
	}
	fmt.Fprintf(w, "%d\t%s%s\n", flow.ID, flow.Name, stale)
}

func commandID(value, label string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, usagef("%s must be a positive integer", label)
	}
	return id, nil
}

func moveCommandFlags(args, valueFlags, boolFlags []string) []string {
	valueNames := make(map[string]bool, len(valueFlags))
	for _, name := range valueFlags {
		valueNames[name] = true
	}
	boolNames := make(map[string]bool, len(boolFlags))
	for _, name := range boolFlags {
		boolNames[name] = true
	}
	flags := make([]string, 0, len(args))
	arguments := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if boolNames[argument] {
			flags = append(flags, argument)
			continue
		}
		isValueFlag := valueNames[argument]
		for name := range valueNames {
			if strings.HasPrefix(argument, name+"=") {
				isValueFlag = true
			}
		}
		if isValueFlag {
			flags = append(flags, argument)
			if !strings.Contains(argument, "=") && index+1 < len(args) {
				flags = append(flags, args[index+1])
				index++
			}
			continue
		}
		arguments = append(arguments, argument)
	}
	return append(flags, arguments...)
}
