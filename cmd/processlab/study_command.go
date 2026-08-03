package main

import (
	"context"
	"encoding/json"
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
		name: "study", summary: "Inspect compiled model provenance", children: []*command{
			newCommand("show", "Show compiled model provenance", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedStringFlag("role", "string", "plant", "model role: plant, controller, reference_controller, generalized, or estimator"), documentedBoolFlag("json", "write machine-readable output")}, nil, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runStudyShow(ctx, client, args, options, stdout)
			}),
		},
	}
}

func runStudyShow(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	role := options.commandString("role")
	if role == "" {
		role = "plant"
	}
	if flowID <= 0 {
		return usagef("processlab study show: --flow is required")
	}
	if len(args) != 0 {
		return usagef("processlab study show: unexpected argument %q", args[0])
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
