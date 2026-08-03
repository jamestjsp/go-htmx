package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/jamestjsp/process-lab/internal/studio"
)

type rolePortClient struct {
	Width    int      `json:"width"`
	Channels []string `json:"channels"`
}

type roleBlockClient struct {
	ID      int64            `json:"id"`
	FlowID  int64            `json:"flowId"`
	Name    string           `json:"name"`
	Inputs  []rolePortClient `json:"inputs"`
	Outputs []rolePortClient `json:"outputs"`
}

type roleConnectionClient struct {
	SourceID       int64    `json:"sourceId"`
	SourceName     string   `json:"sourceName"`
	SourcePort     int      `json:"sourcePort"`
	SourceWidth    int      `json:"sourceWidth"`
	SourceChannels []string `json:"sourceChannels"`
	TargetID       int64    `json:"targetId"`
	TargetName     string   `json:"targetName"`
	TargetPort     int      `json:"targetPort"`
	TargetWidth    int      `json:"targetWidth"`
	TargetChannels []string `json:"targetChannels"`
}

type rolesOutput struct {
	Version     int                    `json:"version"`
	Fingerprint string                 `json:"fingerprint"`
	Spec        studio.ControlRoleSpec `json:"spec"`
	Blocks      []roleBlockSummary     `json:"blocks,omitempty"`
}

type roleBlockSummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func newRolesCommand() *command {
	return &command{
		name:      "roles",
		summary:   "Assign and inspect control model roles",
		freeform:  true,
		arguments: []commandArgument{{name: "subcommand", description: "role operation"}},
		run: func(ctx context.Context, options globalOptions, args []string, stdout io.Writer, stderr io.Writer) error {
			return runRoles(ctx, options, args, stdout, stderr)
		},
	}
}

func runRoles(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usagef("processlab roles: choose show or set")
	}
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	switch args[0] {
	case "show":
		return runRolesShow(ctx, client, args[1:], options, stdout)
	case "set":
		return runRolesSet(ctx, client, args[1:], options, stdout)
	default:
		return usagef("processlab roles: unknown operation %q; choose show or set", args[0])
	}
}

func runRolesShow(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--flow", "-flow"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("roles show", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab roles show --flow <id> [--json]")
			return nil
		}
		return usagef("processlab roles show: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab roles show: --flow is required")
	}
	if set.NArg() != 0 {
		return usagef("processlab roles show: unexpected argument %q", set.Arg(0))
	}
	spec, err := getControlRoles(ctx, client, flowID)
	if err != nil {
		if isClearedRolesError(err) {
			return printRoles(stdout, jsonOutput, rolesResult(studio.ControlRoleSpec{Version: 1}, nil))
		}
		return err
	}
	blocks, err := getRoleBlocks(ctx, client, flowID)
	if err != nil {
		return err
	}
	return printRoles(stdout, jsonOutput, rolesResult(spec, blocks))
}

func runRolesSet(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--flow", "-flow", "--plant", "-plant", "--controller", "-controller"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("roles set", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	var plantText, controllerText string
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	set.StringVar(&plantText, "plant", "", "plant block id or comma-separated block ids")
	set.StringVar(&controllerText, "controller", "", "controller block id or comma-separated block ids")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab roles set --flow <id> --plant <blockID[,blockID...]> --controller <blockID[,blockID...]> [--json]")
			return nil
		}
		return usagef("processlab roles set: %v", err)
	}
	if flowID <= 0 || plantText == "" || controllerText == "" {
		return usagef("processlab roles set: --flow, --plant, and --controller are required")
	}
	if set.NArg() != 0 {
		return usagef("processlab roles set: unexpected argument %q", set.Arg(0))
	}
	plantIDs, err := parseRoleIDs(plantText)
	if err != nil {
		return usagef("processlab roles set: invalid --plant: %v", err)
	}
	controllerIDs, err := parseRoleIDs(controllerText)
	if err != nil {
		return usagef("processlab roles set: invalid --controller: %v", err)
	}
	blocks, err := getRoleBlocks(ctx, client, flowID)
	if err != nil {
		return err
	}
	connections, err := getRoleConnections(ctx, client, flowID)
	if err != nil {
		return err
	}
	spec, err := inferControlRoleSpec(plantIDs, controllerIDs, blocks, connections)
	if err != nil {
		return err
	}
	if err := putControlRoles(ctx, client, flowID, spec); err != nil {
		return err
	}
	return printRoles(stdout, jsonOutput, rolesResult(spec, blocks))
}

func getControlRoles(ctx context.Context, client *apiClient, flowID int64) (studio.ControlRoleSpec, error) {
	var spec studio.ControlRoleSpec
	err := client.requestRoot(ctx, http.MethodGet, "/flows/"+strconv.FormatInt(flowID, 10)+"/control-roles", nil, &spec)
	return spec, err
}

func putControlRoles(ctx context.Context, client *apiClient, flowID int64, spec studio.ControlRoleSpec) error {
	return client.requestRoot(ctx, http.MethodPut, "/flows/"+strconv.FormatInt(flowID, 10)+"/control-roles", spec, &spec)
}

func getRoleBlocks(ctx context.Context, client *apiClient, flowID int64) ([]roleBlockClient, error) {
	var blocks []roleBlockClient
	err := client.request(ctx, http.MethodGet, "/flows/"+strconv.FormatInt(flowID, 10)+"/blocks", nil, &blocks)
	return blocks, err
}

func getRoleConnections(ctx context.Context, client *apiClient, flowID int64) ([]roleConnectionClient, error) {
	var connections []roleConnectionClient
	err := client.request(ctx, http.MethodGet, "/flows/"+strconv.FormatInt(flowID, 10)+"/connections", nil, &connections)
	return connections, err
}

func parseRoleIDs(value string) ([]int64, error) {
	parts := strings.Split(value, ",")
	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("block ids must be positive integers")
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("block %d is repeated", id)
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func inferControlRoleSpec(plantIDs, controllerIDs []int64, blocks []roleBlockClient, connections []roleConnectionClient) (studio.ControlRoleSpec, error) {
	blockByID := make(map[int64]roleBlockClient, len(blocks))
	for _, block := range blocks {
		blockByID[block.ID] = block
	}
	plantSet := make(map[int64]struct{}, len(plantIDs))
	controllerSet := make(map[int64]struct{}, len(controllerIDs))
	for _, id := range plantIDs {
		if _, ok := blockByID[id]; !ok {
			return studio.ControlRoleSpec{}, fmt.Errorf("block %d is not in the selected flowsheet", id)
		}
		plantSet[id] = struct{}{}
	}
	for _, id := range controllerIDs {
		if _, ok := blockByID[id]; !ok {
			return studio.ControlRoleSpec{}, fmt.Errorf("block %d is not in the selected flowsheet", id)
		}
		if _, overlap := plantSet[id]; overlap {
			return studio.ControlRoleSpec{}, fmt.Errorf("block %d cannot be both plant and controller", id)
		}
		controllerSet[id] = struct{}{}
	}

	var actuator, sensor, reference *roleConnectionClient
	for index := range connections {
		connection := &connections[index]
		_, sourcePlant := plantSet[connection.SourceID]
		_, sourceController := controllerSet[connection.SourceID]
		_, targetPlant := plantSet[connection.TargetID]
		_, targetController := controllerSet[connection.TargetID]
		if sourceController && targetPlant && actuator == nil {
			actuator = connection
		}
		if sourcePlant && targetController && sensor == nil {
			sensor = connection
		}
		if targetController && !sourcePlant && !sourceController && reference == nil {
			reference = connection
		}
	}
	if actuator == nil || sensor == nil {
		return studio.ControlRoleSpec{}, fmt.Errorf("could not infer control roles; connect controller output to plant input and plant output to controller input first")
	}
	plantInput := namedRoleRefs(actuator.TargetID, studio.ChannelInput, actuator.TargetPort, actuator.TargetChannels)
	controllerOutput := namedRoleRefs(actuator.SourceID, studio.ChannelOutput, actuator.SourcePort, actuator.SourceChannels)
	plantOutput := namedRoleRefs(sensor.SourceID, studio.ChannelOutput, sensor.SourcePort, sensor.SourceChannels)
	controllerInput := namedRoleRefs(sensor.TargetID, studio.ChannelInput, sensor.TargetPort, sensor.TargetChannels)
	var controllerReference []studio.NamedChannelRef
	if reference != nil {
		controllerReference = namedRoleRefs(
			reference.TargetID, studio.ChannelInput, reference.TargetPort, reference.TargetChannels,
		)
	}
	if len(plantInput) == 0 || len(controllerOutput) == 0 || len(plantOutput) == 0 || len(controllerInput) == 0 {
		return studio.ControlRoleSpec{}, fmt.Errorf("could not infer named control channels from the selected connections")
	}
	if len(plantInput) != len(controllerOutput) || len(plantOutput) != len(controllerInput) {
		return studio.ControlRoleSpec{}, fmt.Errorf("selected control connections have mismatched channel widths")
	}
	return studio.ControlRoleSpec{
		Version: 1,
		Plant: studio.PlantRole{
			Blocks: plantIDs, ControlInputs: plantInput, MeasurementOutputs: plantOutput,
		},
		Controller: studio.ControllerRole{
			Blocks: controllerIDs, FeedbackConvention: studio.FeedbackExternalNegative,
			ReferenceInputs:   controllerReference,
			MeasurementInputs: controllerInput, ControlOutputs: controllerOutput,
		},
		AnalysisPoints: []studio.AnalysisPointRole{
			{Name: "actuator", Location: studio.AnalysisPointPlantInput, Pairs: rolePairs(controllerOutput, plantInput)},
			{Name: "sensor", Location: studio.AnalysisPointPlantOutput, Pairs: rolePairs(plantOutput, controllerInput)},
		},
	}, nil
}

func namedRoleRefs(blockID int64, direction studio.ChannelDirection, port int, channels []string) []studio.NamedChannelRef {
	refs := make([]studio.NamedChannelRef, len(channels))
	for index, name := range channels {
		refs[index] = studio.NamedChannelRef{BlockID: blockID, Direction: direction, Port: port, ChannelName: name}
	}
	return refs
}

func rolePairs(outputs, inputs []studio.NamedChannelRef) []studio.LoopBreakPair {
	pairs := make([]studio.LoopBreakPair, len(outputs))
	for index := range outputs {
		pairs[index] = studio.LoopBreakPair{Output: outputs[index], Input: inputs[index]}
	}
	return pairs
}

func rolesResult(spec studio.ControlRoleSpec, blocks []roleBlockClient) rolesOutput {
	result := rolesOutput{Version: spec.Version, Fingerprint: controlRolesFingerprint(spec), Spec: spec}
	for _, block := range blocks {
		if containsRoleBlock(spec, block.ID) {
			result.Blocks = append(result.Blocks, roleBlockSummary{ID: block.ID, Name: block.Name})
		}
	}
	sort.Slice(result.Blocks, func(i, j int) bool { return result.Blocks[i].ID < result.Blocks[j].ID })
	return result
}

func containsRoleBlock(spec studio.ControlRoleSpec, blockID int64) bool {
	for _, id := range append(append([]int64{}, spec.Plant.Blocks...), spec.Controller.Blocks...) {
		if id == blockID {
			return true
		}
	}
	return false
}

func controlRolesFingerprint(spec studio.ControlRoleSpec) string {
	copySpec := spec
	copySpec.Plant.Blocks = append([]int64(nil), spec.Plant.Blocks...)
	copySpec.Controller.Blocks = append([]int64(nil), spec.Controller.Blocks...)
	sort.Slice(copySpec.Plant.Blocks, func(i, j int) bool { return copySpec.Plant.Blocks[i] < copySpec.Plant.Blocks[j] })
	sort.Slice(copySpec.Controller.Blocks, func(i, j int) bool { return copySpec.Controller.Blocks[i] < copySpec.Controller.Blocks[j] })
	if copySpec.Controller.FeedbackConvention == "" {
		copySpec.Controller.FeedbackConvention = studio.FeedbackExternalNegative
	}
	encoded, _ := json.Marshal(copySpec)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func printRoles(w io.Writer, jsonOutput bool, result rolesOutput) error {
	if jsonOutput {
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return writeRawJSON(w, encoded)
	}
	fmt.Fprintf(w, "version %d\nfingerprint %s\n", result.Version, result.Fingerprint)
	if len(result.Blocks) == 0 {
		fmt.Fprintln(w, "roles: cleared")
		return nil
	}
	fmt.Fprintln(w, "plant:")
	for _, id := range result.Spec.Plant.Blocks {
		fmt.Fprintf(w, "  %d\t%s\n", id, roleBlockName(result.Blocks, id))
	}
	fmt.Fprintln(w, "controller:")
	for _, id := range result.Spec.Controller.Blocks {
		fmt.Fprintf(w, "  %d\t%s\n", id, roleBlockName(result.Blocks, id))
	}
	return nil
}

func roleBlockName(blocks []roleBlockSummary, id int64) string {
	for _, block := range blocks {
		if block.ID == id {
			return block.Name
		}
	}
	return "<missing>"
}

func isClearedRolesError(err error) bool {
	return strings.Contains(err.Error(), "assign plant and controller roles before building control models")
}
