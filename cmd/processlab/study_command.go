package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func newStudyCommand() *command {
	return &command{
		name:      "study",
		summary:   "Inspect compiled model provenance",
		freeform:  true,
		arguments: []commandArgument{{name: "subcommand", description: "study operation"}},
		run: func(ctx context.Context, options globalOptions, args []string, stdout io.Writer, stderr io.Writer) error {
			return runStudy(ctx, options, args, stdout, stderr)
		},
	}
}

func runStudy(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usagef("processlab study: choose show")
	}
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	switch args[0] {
	case "show":
		return runStudyShow(ctx, client, args[1:], options, stdout)
	default:
		return usagef("processlab study: unknown operation %q; choose show", args[0])
	}
}

func runStudyShow(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--flow", "-flow", "--role", "-role"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("study show", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	var role string
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	set.StringVar(&role, "role", "plant", "model role: plant, controller, reference_controller, generalized, or estimator")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab study show --flow <id> [--role plant|controller] [--json]")
			return nil
		}
		return usagef("processlab study show: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab study show: --flow is required")
	}
	if set.NArg() != 0 {
		return usagef("processlab study show: unexpected argument %q", set.Arg(0))
	}
	path := "/flows/" + strconv.FormatInt(flowID, 10) + "/model-study?" + url.Values{"role": {role}}.Encode()
	var provenance studio.ModelStudyProvenance
	if err := client.request(ctx, http.MethodGet, path, nil, &provenance); err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(provenance)
	}
	printModelStudy(stdout, provenance)
	return nil
}

func printModelStudy(w io.Writer, provenance studio.ModelStudyProvenance) {
	fmt.Fprintf(w, "name: %s\nkind: %s\norder: %d\ninputs: %d\noutputs: %d\n", provenance.Name, provenance.Kind, provenance.Order, provenance.Inputs, provenance.Outputs)
	fmt.Fprintf(w, "input names: %s\noutput names: %s\nsample time: %g\n", strings.Join(provenance.InputNames, ", "), strings.Join(provenance.OutputNames, ", "), provenance.SampleTime)
	if provenance.Kind == studio.ModelStudyFRD {
		fmt.Fprintf(w, "frequency samples: %d\n", provenance.FrequencySamples)
	}
	if provenance.Stable == nil {
		fmt.Fprintln(w, "stability: unknown")
	} else {
		fmt.Fprintf(w, "stability: %t\n", *provenance.Stable)
	}
	if provenance.PoleIssue != "" {
		fmt.Fprintf(w, "pole issue: %s\n", provenance.PoleIssue)
	}
}
