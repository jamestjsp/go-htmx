package main

import (
	"context"
	"encoding/json"
	"errors"
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
	return &command{
		name:      "project",
		summary:   "List and manage Process Lab projects",
		freeform:  true,
		arguments: []commandArgument{{name: "subcommand", description: "project operation"}},
		run: func(ctx context.Context, options globalOptions, args []string, stdout io.Writer, stderr io.Writer) error {
			return runProject(ctx, options, args, stdout, stderr)
		},
	}
}

func newFlowCommand() *command {
	return &command{
		name:      "flow",
		summary:   "List and manage flowsheets",
		freeform:  true,
		arguments: []commandArgument{{name: "subcommand", description: "flowsheet operation"}},
		run: func(ctx context.Context, options globalOptions, args []string, stdout io.Writer, stderr io.Writer) error {
			return runFlow(ctx, options, args, stdout, stderr)
		},
	}
}

func runProject(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usagef("processlab project: choose list, show, create, rename, or delete")
	}
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		return runProjectList(ctx, client, args[1:], options, stdout)
	case "show":
		return runProjectShow(ctx, client, args[1:], options, stdout)
	case "create":
		return runProjectCreate(ctx, client, args[1:], options, stdout)
	case "rename":
		return runProjectRename(ctx, client, args[1:], options, stdout)
	case "delete":
		return runProjectDelete(ctx, client, args[1:], options, stdout)
	default:
		return usagef("processlab project: unknown operation %q; choose list, show, create, rename, or delete", args[0])
	}
}

func runProjectList(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, nil, []string{"--json", "-json"})
	set := flag.NewFlagSet("project list", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab project list [--json]")
			return nil
		}
		return usagef("processlab project list: %v", err)
	}
	if set.NArg() != 0 {
		return usagef("processlab project list: unexpected argument %q", set.Arg(0))
	}
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

func runProjectShow(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, nil, []string{"--json", "-json"})
	set := flag.NewFlagSet("project show", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	if err := set.Parse(args); err != nil {
		return usagef("processlab project show: %v", err)
	}
	if set.NArg() != 1 {
		return usagef("processlab project show: expected a project id")
	}
	projectID, err := commandID(set.Arg(0), "project id")
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

func runProjectCreate(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, nil, []string{"--json", "-json"})
	set := flag.NewFlagSet("project create", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	if err := set.Parse(args); err != nil {
		return usagef("processlab project create: %v", err)
	}
	if set.NArg() != 1 {
		return usagef("processlab project create: expected a project name")
	}
	return runWorkspaceAction(ctx, client, http.MethodPost, "/projects", workspaceNameRequest{Name: set.Arg(0)}, jsonOutput, stdout, "project")
}

func runProjectRename(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, nil, []string{"--json", "-json"})
	set := flag.NewFlagSet("project rename", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	if err := set.Parse(args); err != nil {
		return usagef("processlab project rename: %v", err)
	}
	if set.NArg() != 2 {
		return usagef("processlab project rename: expected a project id and name")
	}
	projectID, err := commandID(set.Arg(0), "project id")
	if err != nil {
		return err
	}
	return runWorkspaceAction(ctx, client, http.MethodPut, "/projects/"+strconv.FormatInt(projectID, 10)+"/name", workspaceNameRequest{Name: set.Arg(1)}, jsonOutput, stdout, "project")
}

func runProjectDelete(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, nil, []string{"--json", "-json", "--force", "-force"})
	set := flag.NewFlagSet("project delete", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	force := false
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.BoolVar(&force, "force", false, "confirm cascading deletion")
	if err := set.Parse(args); err != nil {
		return usagef("processlab project delete: %v", err)
	}
	if set.NArg() != 1 {
		return usagef("processlab project delete: expected a project id")
	}
	projectID, err := commandID(set.Arg(0), "project id")
	if err != nil {
		return err
	}
	var input any
	if force {
		input = workspaceDeleteRequest{Force: true}
	}
	return runWorkspaceAction(ctx, client, http.MethodDelete, "/projects/"+strconv.FormatInt(projectID, 10), input, jsonOutput, stdout, "project")
}

func runFlow(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usagef("processlab flow: choose list, show, create, rename, duplicate, delete, or reorder")
	}
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		return runFlowList(ctx, client, args[1:], options, stdout)
	case "show":
		return runFlowShow(ctx, client, args[1:], options, stdout)
	case "create":
		return runFlowCreate(ctx, client, args[1:], options, stdout)
	case "rename":
		return runFlowRename(ctx, client, args[1:], options, stdout)
	case "duplicate":
		return runFlowDuplicate(ctx, client, args[1:], options, stdout)
	case "delete":
		return runFlowDelete(ctx, client, args[1:], options, stdout)
	case "reorder":
		return runFlowReorder(ctx, client, args[1:], options, stdout)
	default:
		return usagef("processlab flow: unknown operation %q; choose list, show, create, rename, duplicate, delete, or reorder", args[0])
	}
}

func runFlowList(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--project", "-project"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("flow list", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var projectID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&projectID, "project", 0, "filter by project id")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab flow list [--project <id>] [--json]")
			return nil
		}
		return usagef("processlab flow list: %v", err)
	}
	if set.NArg() != 0 {
		return usagef("processlab flow list: unexpected argument %q", set.Arg(0))
	}
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

func runFlowShow(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, nil, []string{"--json", "-json"})
	set := flag.NewFlagSet("flow show", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	if err := set.Parse(args); err != nil {
		return usagef("processlab flow show: %v", err)
	}
	if set.NArg() != 1 {
		return usagef("processlab flow show: expected a flowsheet id")
	}
	flowID, err := commandID(set.Arg(0), "flowsheet id")
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

func runFlowCreate(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--project", "-project"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("flow create", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var projectID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&projectID, "project", 0, "project id")
	if err := set.Parse(args); err != nil {
		return usagef("processlab flow create: %v", err)
	}
	if projectID <= 0 {
		return usagef("processlab flow create: --project is required")
	}
	if set.NArg() != 1 {
		return usagef("processlab flow create: expected a flowsheet name")
	}
	return runWorkspaceAction(ctx, client, http.MethodPost, "/projects/"+strconv.FormatInt(projectID, 10)+"/flows", workspaceNameRequest{Name: set.Arg(0)}, jsonOutput, stdout, "flow")
}

func runFlowRename(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, nil, []string{"--json", "-json"})
	set := flag.NewFlagSet("flow rename", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	if err := set.Parse(args); err != nil {
		return usagef("processlab flow rename: %v", err)
	}
	if set.NArg() != 2 {
		return usagef("processlab flow rename: expected a flowsheet id and name")
	}
	flowID, err := commandID(set.Arg(0), "flowsheet id")
	if err != nil {
		return err
	}
	return runWorkspaceAction(ctx, client, http.MethodPut, "/flows/"+strconv.FormatInt(flowID, 10)+"/name", workspaceNameRequest{Name: set.Arg(1)}, jsonOutput, stdout, "flow")
}

func runFlowDuplicate(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, nil, []string{"--json", "-json"})
	set := flag.NewFlagSet("flow duplicate", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	if err := set.Parse(args); err != nil {
		return usagef("processlab flow duplicate: %v", err)
	}
	if set.NArg() != 1 {
		return usagef("processlab flow duplicate: expected a flowsheet id")
	}
	flowID, err := commandID(set.Arg(0), "flowsheet id")
	if err != nil {
		return err
	}
	return runWorkspaceAction(ctx, client, http.MethodPost, "/flows/"+strconv.FormatInt(flowID, 10)+"/duplicate", nil, jsonOutput, stdout, "flow")
}

func runFlowDelete(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, nil, []string{"--json", "-json", "--force", "-force"})
	set := flag.NewFlagSet("flow delete", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	force := false
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.BoolVar(&force, "force", false, "confirm deletion of blocks in the flowsheet")
	if err := set.Parse(args); err != nil {
		return usagef("processlab flow delete: %v", err)
	}
	if set.NArg() != 1 {
		return usagef("processlab flow delete: expected a flowsheet id")
	}
	flowID, err := commandID(set.Arg(0), "flowsheet id")
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

func runFlowReorder(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--project", "-project"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("flow reorder", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var projectID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&projectID, "project", 0, "project id")
	if err := set.Parse(args); err != nil {
		return usagef("processlab flow reorder: %v", err)
	}
	if projectID <= 0 {
		return usagef("processlab flow reorder: --project is required")
	}
	if set.NArg() == 0 {
		return usagef("processlab flow reorder: expected at least one flowsheet id")
	}
	flowIDs := make([]int64, set.NArg())
	for index, argument := range set.Args() {
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
