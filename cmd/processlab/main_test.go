package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunRequiresCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("run() exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("bare invocation wrote %q to stdout", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Commands:") {
		t.Fatalf("bare invocation omitted command list: %s", stderr.String())
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--help) exit code = %d, want 0: %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("help wrote diagnostics: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "processlab") || !strings.Contains(stdout.String(), "serve") {
		t.Fatalf("help omitted command tree: %s", stdout.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"nosuchthing"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(nosuchthing) exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unknown command wrote %q to stdout", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown command "nosuchthing"`) {
		t.Fatalf("unknown command was not named: %s", stderr.String())
	}
}

func TestRunServeHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"serve", "--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(serve --help) exit code = %d, want 0: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "--addr") || !strings.Contains(stdout.String(), "--db") {
		t.Fatalf("serve help omitted flags: %s", stdout.String())
	}
}

func TestRunJSONHelpIncludesEveryCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(help --json) exit code = %d, want 0: %s", code, stderr.String())
	}
	var help commandHelpJSON
	if err := json.Unmarshal(stdout.Bytes(), &help); err != nil {
		t.Fatalf("decode JSON help: %v", err)
	}
	if help.Name != "processlab" || help.Summary == "" {
		t.Fatalf("root help = %#v", help)
	}

	seen := make(map[string]bool)
	var walk func(commandHelpJSON, string)
	walk = func(command commandHelpJSON, path string) {
		if command.Name == "" || command.Summary == "" {
			t.Errorf("command lacks name or summary: %#v", command)
		}
		if seen[path] {
			t.Errorf("command %q appears more than once", path)
		}
		seen[path] = true
		if len(command.Commands) == 0 && len(command.Flags) == 0 && len(command.Arguments) == 0 {
			t.Errorf("leaf command %q has no documented flags or arguments", path)
		}
		for _, child := range command.Commands {
			walk(child, path+" "+child.Name)
		}
	}
	walk(help, help.Name)
	for _, command := range []string{"processlab", "help", "serve", "project", "flow", "block", "wire", "sim", "analyze", "roles", "sweep", "controller", "ident", "study", "nonlinear", "export", "log"} {
		found := false
		for path := range seen {
			if path == command || strings.HasSuffix(path, " "+command) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("JSON help cannot reach command %q", command)
		}
	}
	if len(help.Commands) != 16 {
		t.Fatalf("root commands = %d, want 16: %#v", len(help.Commands), help.Commands)
	}
}

func TestRunJSONHelpSubtreeAndTextAgreement(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help", "serve", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(help serve --json) exit code = %d, want 0: %s", code, stderr.String())
	}
	var subtree commandHelpJSON
	if err := json.Unmarshal(stdout.Bytes(), &subtree); err != nil {
		t.Fatalf("decode JSON subtree: %v", err)
	}
	if subtree.Name != "serve" || len(subtree.Commands) != 0 {
		t.Fatalf("serve subtree = %#v", subtree)
	}

	root := commandTree()
	var text bytes.Buffer
	printRootHelp(&text, root)
	var printTree func(*command)
	printTree = func(command *command) {
		printCommandHelp(&text, command)
		for _, child := range command.children {
			printTree(child)
		}
	}
	for _, child := range root.children {
		printTree(child)
	}
	textHelp := text.String()
	var compare func(commandHelpJSON, string)
	compare = func(command commandHelpJSON, path string) {
		if !strings.Contains(textHelp, "Usage: "+path) {
			t.Errorf("text help omitted command %q", path)
		}
		for _, specification := range command.Flags {
			if !strings.Contains(textHelp, "--"+specification.Name) {
				t.Errorf("text help omitted flag %q", path+" --"+specification.Name)
			}
		}
		for _, child := range command.Commands {
			compare(child, path+" "+child.Name)
		}
	}
	compare(commandHelp(root), "processlab")
}

func TestRunRejectsMissingRequiredArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"flow", "create", "--project", "1"}, &stdout, &stderr); code != 2 {
		t.Fatalf("missing positional argument exit code = %d, want 2: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), `argument "name" is required`) {
		t.Fatalf("missing positional argument was not named: %s", stderr.String())
	}
}

func TestRunLeafHelp(t *testing.T) {
	root := commandTree()
	var visit func(*command, []string)
	visit = func(current *command, path []string) {
		if current.freeform {
			return
		}
		if len(current.children) == 0 {
			args := append(append([]string{}, path[1:]...), "--help")
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != 0 {
				t.Errorf("%s --help exit code = %d: %s", strings.Join(path, " "), code, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("%s --help wrote diagnostics: %s", strings.Join(path, " "), stderr.String())
			}
			if stdout.Len() == 0 {
				t.Errorf("%s --help wrote no output", strings.Join(path, " "))
			}
			return
		}
		for _, child := range current.children {
			visit(child, append(path, child.name))
		}
	}
	visit(root, []string{root.name})
}

func TestParseGlobalOptionsServerPrecedence(t *testing.T) {
	t.Setenv("PROCESSLAB_ADDR", "http://from-environment")

	options, remaining, help, err := parseGlobalOptions([]string{"--server", "http://from-flag", "serve"})
	if err != nil {
		t.Fatalf("parseGlobalOptions() error = %v", err)
	}
	if options.server != "http://from-flag" {
		t.Fatalf("server = %q, want flag value", options.server)
	}
	if len(remaining) != 1 || remaining[0] != "serve" {
		t.Fatalf("remaining = %#v, want [serve]", remaining)
	}
	if help {
		t.Fatal("help unexpectedly set")
	}

	options, _, _, err = parseGlobalOptions([]string{"serve"})
	if err != nil {
		t.Fatalf("parseGlobalOptions() error = %v", err)
	}
	if options.server != "http://from-environment" {
		t.Fatalf("server = %q, want environment value", options.server)
	}
}

func TestRunRejectsBadCommandFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"serve", "--unknown"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(serve --unknown) exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("bad flag was not reported: %s", stderr.String())
	}
}
