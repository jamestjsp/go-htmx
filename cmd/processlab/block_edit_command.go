package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type blockMoveRequestClient struct {
	BlockID int64 `json:"blockId"`
	X       int   `json:"x"`
	Y       int   `json:"y"`
}

type blockBatchClient struct {
	FlowID int64               `json:"flowId"`
	Blocks []blockRecordClient `json:"blocks"`
}

func requestBlock(ctx context.Context, client *apiClient, blockID int64) (blockRecordClient, json.RawMessage, error) {
	var raw json.RawMessage
	path := "/blocks/" + strconv.FormatInt(blockID, 10)
	if err := client.request(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return blockRecordClient{}, nil, err
	}
	var block blockRecordClient
	if err := json.Unmarshal(raw, &block); err != nil {
		return blockRecordClient{}, nil, fmt.Errorf("decode block: %w", err)
	}
	return block, raw, nil
}

func runBlockShow(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	if len(args) != 1 {
		return usagef("processlab block show: expected a block id")
	}
	blockID, err := commandID(args[0], "block id")
	if err != nil {
		return err
	}
	block, raw, err := requestBlock(ctx, client, blockID)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\n", block.ID, block.Name, block.Kind, block.Summary)
	keys := make([]string, 0, len(block.ParameterValues))
	for name := range block.ParameterValues {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		fmt.Fprintf(stdout, "  --%s=%s\n", parameterFlagName(name), block.ParameterValues[name])
	}
	return nil
}

func runBlockSet(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usagef("processlab block set: expected a block id")
	}
	blockID, err := commandID(args[0], "block id")
	if err != nil {
		return err
	}
	block, _, err := requestBlock(ctx, client, blockID)
	if err != nil {
		return err
	}
	schema, err := requestBlockSchema(ctx, client, block.Kind)
	if err != nil {
		return err
	}
	set := flag.NewFlagSet("block set", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json || options.commandBool("json")
	name := block.Name
	if requestedName := options.commandString("name"); requestedName != "" {
		name = requestedName
	}
	values := make(map[string]*string, len(schema.Parameters))
	for _, field := range schema.Parameters {
		value, ok := block.ParameterValues[field.Name]
		if !ok {
			value = field.Default
		}
		values[field.Name] = &value
		set.StringVar(&value, parameterFlagName(field.Name), value, field.Label)
	}
	if hasHelpFlag(args[1:]) {
		printBlockSchemaHelp(stdout, schema)
		return nil
	}
	if err := set.Parse(args[1:]); err != nil {
		return usagef("processlab block set: %v", err)
	}
	if set.NArg() != 0 {
		return usagef("processlab block set: unexpected argument %q", set.Arg(0))
	}
	parameters := make(map[string]string, len(values))
	for field, value := range values {
		parameters[field] = *value
	}
	input := struct {
		Name       string            `json:"name"`
		Parameters map[string]string `json:"parameters"`
	}{Name: name, Parameters: parameters}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodPut, "/blocks/"+strconv.FormatInt(blockID, 10), input, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var updated blockRecordClient
	if err := json.Unmarshal(raw, &updated); err != nil {
		return fmt.Errorf("decode updated block: %w", err)
	}
	fmt.Fprintln(stdout, updated.ID)
	return nil
}

func runBlockMove(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	if flowID <= 0 {
		return usagef("processlab block mv: --flow is required")
	}
	if len(args) == 0 {
		return usagef("processlab block mv: expected blockID:x,y values")
	}
	moves := make([]blockMoveRequestClient, 0, len(args))
	for _, argument := range args {
		move, err := parseBlockMove(argument)
		if err != nil {
			return err
		}
		moves = append(moves, move)
	}
	return runBlockBatchAction(ctx, client, http.MethodPatch, "/flows/"+strconv.FormatInt(flowID, 10)+"/blocks/positions", struct {
		Moves []blockMoveRequestClient `json:"moves"`
	}{Moves: moves}, jsonOutput, stdout, fmt.Sprintf("moved %d blocks", len(moves)))
}

func runBlockDelete(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	return runBlockIDsAction(ctx, client, args, options, stdout, "rm", http.MethodDelete, "deleted")
}

func runBlockDuplicate(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	return runBlockIDsAction(ctx, client, args, options, stdout, "cp", http.MethodPost, "duplicated")
}

func runBlockIDsAction(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer, commandName, method, verb string) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	if flowID <= 0 {
		return usagef("processlab block %s: --flow is required", commandName)
	}
	if len(args) == 0 {
		return usagef("processlab block %s: expected at least one block id", commandName)
	}
	ids := make([]int64, len(args))
	for index, argument := range args {
		id, err := commandID(argument, "block id")
		if err != nil {
			return err
		}
		ids[index] = id
	}
	path := "/flows/" + strconv.FormatInt(flowID, 10) + "/blocks"
	if commandName == "cp" {
		path += "/duplicate"
	}
	return runBlockBatchAction(ctx, client, method, path, struct {
		BlockIDs []int64 `json:"blockIds"`
	}{BlockIDs: ids}, jsonOutput, stdout, fmt.Sprintf("%s %d blocks", verb, len(ids)))
}

func runBlockBatchAction(ctx context.Context, client *apiClient, method, path string, input any, jsonOutput bool, stdout io.Writer, text string) error {
	var raw json.RawMessage
	if err := client.request(ctx, method, path, input, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	fmt.Fprintln(stdout, text)
	return nil
}

func parseBlockMove(value string) (blockMoveRequestClient, error) {
	blockPart, positionPart, ok := strings.Cut(value, ":")
	if !ok {
		return blockMoveRequestClient{}, usagef("block move %q must use blockID:x,y", value)
	}
	blockID, err := commandID(blockPart, "block id")
	if err != nil {
		return blockMoveRequestClient{}, err
	}
	xPart, yPart, ok := strings.Cut(positionPart, ",")
	if !ok || strings.Contains(yPart, ",") {
		return blockMoveRequestClient{}, usagef("block move %q must use blockID:x,y", value)
	}
	x, errX := strconv.Atoi(xPart)
	y, errY := strconv.Atoi(yPart)
	if errX != nil || errY != nil {
		return blockMoveRequestClient{}, usagef("block move %q must use integer coordinates", value)
	}
	return blockMoveRequestClient{BlockID: blockID, X: x, Y: y}, nil
}
