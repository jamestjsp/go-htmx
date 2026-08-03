package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

const (
	generatedStart = "<!-- generated:cli:start -->"
	generatedEnd   = "<!-- generated:cli:end -->"
)

type helpDocument struct {
	Name      string         `json:"name"`
	Summary   string         `json:"summary"`
	Flags     []helpFlag     `json:"flags"`
	Arguments []helpArgument `json:"arguments"`
	Commands  []helpDocument `json:"commands"`
}

type helpFlag struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Default     string `json:"default"`
	Description string `json:"description"`
}

type helpArgument struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type blockEntry struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

type blockSchema struct {
	Kind       string        `json:"kind"`
	Label      string        `json:"label"`
	Parameters []schemaField `json:"parameters"`
	Inputs     []schemaPort  `json:"inputs"`
	Outputs    []schemaPort  `json:"outputs"`
}

type schemaField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Default  string `json:"default"`
	Optional bool   `json:"optional"`
	Options  []struct {
		Value string `json:"value"`
	} `json:"options"`
}

type schemaPort struct {
	Width    int      `json:"width"`
	Channels []string `json:"channels"`
}

func main() {
	binary := flag.String("binary", "./processlab", "processlab binary used for command help")
	server := flag.String("server", "http://127.0.0.1:8080", "Process Lab server used for block catalog data")
	documentPath := flag.String("document", "docs/cli.md", "CLI reference to update or check")
	check := flag.Bool("check", false, "check that the generated section is current")
	flag.Parse()

	generated, err := generate(*binary, *server)
	if err != nil {
		fatal(err)
	}
	document, err := os.ReadFile(*documentPath)
	if err != nil {
		fatal(fmt.Errorf("read %s: %w", *documentPath, err))
	}
	updated, err := replaceGeneratedSection(string(document), generated)
	if err != nil {
		fatal(err)
	}
	if *check {
		if updated != string(document) {
			fatal(errors.New("generated CLI reference is stale; run cmd/processlab-docs"))
		}
		return
	}
	if err := os.WriteFile(*documentPath, []byte(updated), 0o644); err != nil {
		fatal(fmt.Errorf("write %s: %w", *documentPath, err))
	}
}

func generate(binary, server string) (string, error) {
	help, err := commandHelp(binary)
	if err != nil {
		return "", err
	}
	entries, err := getJSON[[]blockEntry](server + "/api/v1/blocks")
	if err != nil {
		return "", fmt.Errorf("load block catalog: %w", err)
	}
	var output strings.Builder
	output.WriteString("## Generated command and block reference\n\n")
	output.WriteString("The sections below are generated from `processlab help --json`, each command's help output, and the live block catalog API.\n\n")
	output.WriteString("### Commands\n\n")
	output.WriteString("| Command | Summary |\n| --- | --- |\n")
	for _, command := range help.Commands {
		fmt.Fprintf(&output, "| `processlab %s` | %s |\n", command.Name, escapeTable(command.Summary))
	}
	output.WriteString("\n")
	for _, command := range help.Commands {
		text, err := commandHelpText(binary, command.Name)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&output, "#### `processlab %s --help`\n\n```text\n%s\n```\n\n", command.Name, strings.TrimSpace(text))
	}
	output.WriteString("### Block catalog\n\n")
	output.WriteString("| Kind | Label | Category | Description | Parameters |\n| --- | --- | --- | --- | --- |\n")
	for _, entry := range entries {
		schema, err := getJSON[blockSchema](server + "/api/v1/blocks/" + entry.Kind)
		if err != nil {
			return "", fmt.Errorf("load %s schema: %w", entry.Kind, err)
		}
		parameters := make([]string, len(schema.Parameters))
		for index, field := range schema.Parameters {
			parameters[index] = field.Name
		}
		fmt.Fprintf(&output, "| `%s` | %s | %s | %s | %s |\n",
			entry.Kind, escapeTable(entry.Label), escapeTable(entry.Category),
			escapeTable(entry.Description), escapeTable(strings.Join(parameters, ", ")),
		)
	}
	return output.String(), nil
}

func commandHelp(binary string) (helpDocument, error) {
	command := exec.Command(binary, "help", "--json")
	output, err := command.Output()
	if err != nil {
		return helpDocument{}, fmt.Errorf("run %s help --json: %w", binary, err)
	}
	var help helpDocument
	if err := json.Unmarshal(output, &help); err != nil {
		return helpDocument{}, fmt.Errorf("decode command help: %w", err)
	}
	return help, nil
}

func commandHelpText(binary, name string) (string, error) {
	command := exec.Command(binary, name, "--help")
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("run %s %s --help: %w", binary, name, err)
	}
	return string(output), nil
}

func getJSON[T any](endpoint string) (T, error) {
	var value T
	response, err := http.Get(endpoint)
	if err != nil {
		return value, err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return value, fmt.Errorf("GET %s returned HTTP %d: %s", endpoint, response.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return value, err
	}
	return value, nil
}

func replaceGeneratedSection(document, generated string) (string, error) {
	start := strings.Index(document, generatedStart)
	end := strings.Index(document, generatedEnd)
	if start < 0 || end < 0 || end < start {
		return "", fmt.Errorf("%s and %s markers are required", generatedStart, generatedEnd)
	}
	end += len(generatedEnd)
	return document[:start] + generatedStart + "\n" + generated + generatedEnd + document[end:], nil
}

func escapeTable(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "|", "\\|"), "\n", " ")
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
