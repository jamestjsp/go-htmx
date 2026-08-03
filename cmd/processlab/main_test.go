package main

import (
	"bytes"
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
