package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type blockLibraryEntryClient struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Tag         string `json:"tag"`
	HasInput    bool   `json:"hasInput"`
	HasOutput   bool   `json:"hasOutput"`
}

type blockParameterOptionClient struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type blockParameterSchemaClient struct {
	Name        string                       `json:"name"`
	Label       string                       `json:"label"`
	Type        string                       `json:"type"`
	Default     string                       `json:"default"`
	Options     []blockParameterOptionClient `json:"options"`
	Step        string                       `json:"step"`
	Minimum     *float64                     `json:"minimum"`
	Maximum     *float64                     `json:"maximum"`
	Unit        string                       `json:"unit"`
	Placeholder string                       `json:"placeholder"`
	Help        string                       `json:"help"`
	Optional    bool                         `json:"optional"`
	ActiveWhen  []string                     `json:"activeWhen"`
}

type blockPortSchemaClient struct {
	Width    int      `json:"width"`
	Channels []string `json:"channels"`
}

type blockSchemaClient struct {
	Kind        string                       `json:"kind"`
	Label       string                       `json:"label"`
	Category    string                       `json:"category"`
	Description string                       `json:"description"`
	Glyph       string                       `json:"glyph"`
	Tag         string                       `json:"tag"`
	Parameters  []blockParameterSchemaClient `json:"parameters"`
	Inputs      []blockPortSchemaClient      `json:"inputs"`
	Outputs     []blockPortSchemaClient      `json:"outputs"`
}

type blockRecordClient struct {
	ID              int64              `json:"id"`
	FlowID          int64              `json:"flowId"`
	Kind            string             `json:"kind"`
	Name            string             `json:"name"`
	Position        struct{ X, Y int } `json:"position"`
	Parameters      map[string]any     `json:"parameters"`
	ParameterValues map[string]string  `json:"parameterValues"`
	Summary         string             `json:"summary"`
}

func newBlockCommand() *command {
	return &command{
		name: "block", summary: "Discover, add, and configure library blocks", children: []*command{
			newCommand("list", "List library or flowsheet blocks", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedBoolFlag("json", "write machine-readable output")}, nil, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runBlockList(ctx, client, args, options, stdout)
			}),
			newCommand("show", "Show a block", []commandFlag{documentedBoolFlag("json", "write machine-readable output")}, []commandArgument{{name: "block id", description: "block identifier", required: true}}, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runBlockShow(ctx, client, args, options, stdout)
			}),
			{
				name: "add", summary: "Add a catalog block", freeform: true,
				flags: []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedIntFlag("x", "pixels", 90, "horizontal position"), documentedIntFlag("y", "pixels", 120, "vertical position"), documentedBoolFlag("json", "write machine-readable output")},
				help: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
					client, err := newAPIClient(options.server, options.timeout)
					if err != nil {
						return err
					}
					return runBlockAdd(ctx, client, append(args, "--help"), options, stdout)
				},
				run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
					client, err := newAPIClient(options.server, options.timeout)
					if err != nil {
						return err
					}
					return runBlockAdd(ctx, client, args, options, stdout)
				},
			},
			{
				name: "set", summary: "Update a block", freeform: true,
				flags: []commandFlag{documentedStringFlag("name", "string", "", "block name"), documentedBoolFlag("json", "write machine-readable output")},
				help: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
					client, err := newAPIClient(options.server, options.timeout)
					if err != nil {
						return err
					}
					return runBlockSet(ctx, client, append(args, "--help"), options, stdout)
				},
				run: func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
					client, err := newAPIClient(options.server, options.timeout)
					if err != nil {
						return err
					}
					return runBlockSet(ctx, client, args, options, stdout)
				},
			},
			newVariadicCommand("mv", "Move blocks", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedBoolFlag("json", "write machine-readable output")}, []commandArgument{{name: "blockID:x,y", description: "block position", required: true}}, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runBlockMove(ctx, client, args, options, stdout)
			}),
			newVariadicCommand("rm", "Delete blocks", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedBoolFlag("json", "write machine-readable output")}, []commandArgument{{name: "block id", description: "one or more block identifiers", required: true}}, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runBlockDelete(ctx, client, args, options, stdout)
			}),
			newVariadicCommand("cp", "Duplicate blocks", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedBoolFlag("json", "write machine-readable output")}, []commandArgument{{name: "block id", description: "one or more block identifiers", required: true}}, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runBlockDuplicate(ctx, client, args, options, stdout)
			}),
			newCommand("help", "Show catalog block help", nil, []commandArgument{{name: "kind", description: "block kind", required: true}}, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runBlockHelp(ctx, client, args, stdout)
			}),
		},
	}
}

func requestBlockLibrary(ctx context.Context, client *apiClient) ([]blockLibraryEntryClient, json.RawMessage, error) {
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodGet, "/blocks", nil, &raw); err != nil {
		return nil, nil, err
	}
	var entries []blockLibraryEntryClient
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, nil, fmt.Errorf("decode block library: %w", err)
	}
	return entries, raw, nil
}

func requestBlockSchema(ctx context.Context, client *apiClient, kind string) (blockSchemaClient, error) {
	var schema blockSchemaClient
	path := "/blocks/" + url.PathEscape(kind)
	if err := client.request(ctx, http.MethodGet, path, nil, &schema); err != nil {
		return blockSchemaClient{}, err
	}
	return schema, nil
}

func runBlockList(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	if len(args) != 0 {
		return usagef("processlab block list: unexpected argument %q", args[0])
	}
	var raw json.RawMessage
	var entries []blockLibraryEntryClient
	if flowID != 0 {
		if flowID < 0 {
			return usagef("flow id must be positive")
		}
		if err := client.request(ctx, http.MethodGet, "/flows/"+strconv.FormatInt(flowID, 10)+"/blocks", nil, &raw); err != nil {
			return err
		}
	} else {
		var err error
		entries, raw, err = requestBlockLibrary(ctx, client)
		if err != nil {
			return err
		}
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	if flowID != 0 {
		var blocks []blockRecordClient
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return fmt.Errorf("decode blocks: %w", err)
		}
		for _, block := range blocks {
			fmt.Fprintf(stdout, "%d\t%s\t%s\n", block.ID, block.Name, block.Summary)
		}
		return nil
	}
	printBlockLibrary(stdout, entries)
	return nil
}

func runBlockHelp(ctx context.Context, client *apiClient, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return usagef("processlab block help: expected a block kind")
	}
	entries, _, err := requestBlockLibrary(ctx, client)
	if err != nil {
		return err
	}
	if !knownBlockKind(entries, args[0]) {
		return unknownBlockKindError(args[0], entries)
	}
	schema, err := requestBlockSchema(ctx, client, args[0])
	if err != nil {
		return err
	}
	printBlockSchemaHelp(stdout, schema)
	return nil
}

func runBlockAdd(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		entries, _, err := requestBlockLibrary(ctx, client)
		if err != nil {
			return err
		}
		if len(args) == 1 && (args[0] == "--help" || args[0] == "-help" || args[0] == "-h") {
			printBlockAddHelp(stdout, entries)
			return nil
		}
		return usagef("processlab block add: expected a block kind")
	}
	kind := strings.ToLower(args[0])
	entries, _, err := requestBlockLibrary(ctx, client)
	if err != nil {
		return err
	}
	if !knownBlockKind(entries, kind) {
		return unknownBlockKindError(kind, entries)
	}
	schema, err := requestBlockSchema(ctx, client, kind)
	if err != nil {
		return err
	}
	set := flag.NewFlagSet("block add "+kind, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	flowID := options.commandInt64("flow")
	x := options.commandInt("x")
	y := options.commandInt("y")
	jsonOutput := options.json || options.commandBool("json")
	values := make(map[string]*string, len(schema.Parameters))
	for _, field := range schema.Parameters {
		value := field.Default
		values[field.Name] = &value
		set.StringVar(&value, parameterFlagName(field.Name), value, field.Label)
	}
	if hasHelpFlag(args[1:]) {
		printBlockSchemaHelp(stdout, schema)
		return nil
	}
	if err := set.Parse(args[1:]); err != nil {
		return usagef("processlab block add %s: %v", kind, err)
	}
	if set.NArg() != 0 {
		return usagef("processlab block add %s: unexpected argument %q", kind, set.Arg(0))
	}
	if flowID == 0 {
		return usagef("processlab block add %s: --flow is required", kind)
	}
	parameters := make(map[string]string, len(values))
	for name, value := range values {
		if *value != "" {
			parameters[name] = *value
		}
	}
	input := struct {
		Kind       string            `json:"kind"`
		X          int               `json:"x"`
		Y          int               `json:"y"`
		Parameters map[string]string `json:"parameters"`
	}{Kind: kind, X: x, Y: y, Parameters: parameters}
	var raw json.RawMessage
	path := "/flows/" + strconv.FormatInt(flowID, 10) + "/blocks"
	if err := client.request(ctx, http.MethodPost, path, input, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var record blockRecordClient
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("decode created block: %w", err)
	}
	fmt.Fprintln(stdout, record.ID)
	return nil
}

func parameterFlagName(name string) string {
	return strings.ReplaceAll(name, "_", "-")
}

func knownBlockKind(entries []blockLibraryEntryClient, kind string) bool {
	for _, entry := range entries {
		if entry.Kind == strings.ToLower(kind) {
			return true
		}
	}
	return false
}

func unknownBlockKindError(kind string, entries []blockLibraryEntryClient) error {
	valid := make([]string, 0, len(entries))
	for _, entry := range entries {
		valid = append(valid, entry.Kind)
	}
	sort.Strings(valid)
	return usagef("processlab block: unknown kind %q; valid kinds: %s", kind, strings.Join(valid, ", "))
}

func printBlockLibrary(w io.Writer, entries []blockLibraryEntryClient) {
	groups := make(map[string][]blockLibraryEntryClient)
	order := make([]string, 0)
	for _, entry := range entries {
		if _, exists := groups[entry.Category]; !exists {
			order = append(order, entry.Category)
		}
		groups[entry.Category] = append(groups[entry.Category], entry)
	}
	for _, category := range order {
		fmt.Fprintf(w, "%s:\n", category)
		for _, entry := range groups[category] {
			fmt.Fprintf(w, "  %-20s %s\n", entry.Kind, entry.Label)
		}
	}
}

func printBlockAddHelp(w io.Writer, entries []blockLibraryEntryClient) {
	fmt.Fprintln(w, "Usage: processlab block add <kind> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Add a block from the running server's catalog.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Valid kinds:")
	printBlockLibrary(w, entries)
}

func printBlockSchemaHelp(w io.Writer, schema blockSchemaClient) {
	fmt.Fprintf(w, "Usage: processlab block add %s [flags]\n\n", schema.Kind)
	fmt.Fprintf(w, "%s — %s\n", schema.Label, schema.Description)
	fmt.Fprintln(w)
	for _, field := range schema.Parameters {
		fmt.Fprintf(w, "  --%-20s <%s> default %s", parameterFlagName(field.Name), field.Type, field.Default)
		if field.Minimum != nil || field.Maximum != nil {
			fmt.Fprint(w, ", range ")
			if field.Minimum != nil {
				fmt.Fprintf(w, "%g", *field.Minimum)
			} else {
				fmt.Fprint(w, "-∞")
			}
			fmt.Fprint(w, "..")
			if field.Maximum != nil {
				fmt.Fprintf(w, "%g", *field.Maximum)
			} else {
				fmt.Fprint(w, "+∞")
			}
			if field.Unit != "" {
				fmt.Fprintf(w, " %s", field.Unit)
			}
		}
		fmt.Fprintln(w)
		if len(field.Options) > 0 {
			fmt.Fprint(w, "    options: ")
			for index, option := range field.Options {
				if index > 0 {
					fmt.Fprint(w, ", ")
				}
				fmt.Fprintf(w, "%s (%s)", option.Value, option.Label)
			}
			fmt.Fprintln(w)
		}
		if len(field.ActiveWhen) > 0 {
			conditions := make([]string, len(field.ActiveWhen))
			for index, dependency := range field.ActiveWhen {
				conditions[index] = "--" + parameterFlagName(dependency)
			}
			fmt.Fprintf(w, "    applies when %s is active\n", strings.Join(conditions, " and "))
		}
		if field.Help != "" {
			fmt.Fprintf(w, "    %s\n", field.Help)
		}
	}
}

func writeRawJSON(w io.Writer, raw json.RawMessage) error {
	if _, err := w.Write(raw); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}
