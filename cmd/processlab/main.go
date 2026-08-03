package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jamestjsp/process-lab/internal/studio"
	"github.com/jamestjsp/process-lab/internal/web"
)

const (
	defaultServer = "http://127.0.0.1:8080"
	defaultAddr   = "127.0.0.1:8080"
	defaultDB     = "processlab.db"
)

type globalOptions struct {
	server string
	json   bool
}

type usageError struct {
	err error
}

func (e usageError) Error() string {
	return e.err.Error()
}

func (e usageError) Unwrap() error {
	return e.err
}

func usagef(format string, args ...any) error {
	return usageError{err: fmt.Errorf(format, args...)}
}

type commandFlag struct {
	name         string
	typeName     string
	defaultValue string
	usage        string
	register     func(*flag.FlagSet)
}

type command struct {
	name     string
	summary  string
	flags    []commandFlag
	children []*command
	run      func(context.Context, globalOptions, io.Writer, io.Writer) error
}

func (c *command) child(name string) *command {
	for _, child := range c.children {
		if child.name == name {
			return child
		}
	}
	return nil
}

func (c *command) execute(
	ctx context.Context,
	options globalOptions,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	set := flag.NewFlagSet(c.name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	for _, specification := range c.flags {
		specification.register(set)
	}
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandHelp(stdout, c)
			return nil
		}
		return usagef("processlab %s: %v", c.name, err)
	}
	if set.NArg() != 0 {
		return usagef("processlab %s: unexpected argument %q", c.name, set.Arg(0))
	}
	return c.run(ctx, options, stdout, stderr)
}

func commandTree() *command {
	return &command{
		name:    "processlab",
		summary: "Process Lab command-line interface",
		flags: []commandFlag{
			{name: "server", typeName: "url", defaultValue: defaultServer, usage: "Process Lab server URL (env: PROCESSLAB_ADDR)"},
			{name: "json", typeName: "bool", defaultValue: "false", usage: "write machine-readable output"},
			{name: "help", typeName: "bool", defaultValue: "false", usage: "show help"},
		},
		children: []*command{newServeCommand()},
	}
}

func parseGlobalOptions(args []string) (globalOptions, []string, bool, error) {
	options := globalOptions{server: os.Getenv("PROCESSLAB_ADDR")}
	if options.server == "" {
		options.server = defaultServer
	}
	help := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--" && index+1 < len(args):
			return options, args[index+1:], help, nil
		case argument == "--" || !strings.HasPrefix(argument, "-"):
			return options, args[index:], help, nil
		case argument == "--help" || argument == "-help" || argument == "-h":
			help = true
		case argument == "--json" || argument == "-json":
			options.json = true
		case argument == "--server" || argument == "-server":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
				return options, nil, false, usagef("processlab: flag %s needs an argument", argument)
			}
			index++
			options.server = args[index]
		case strings.HasPrefix(argument, "--server="):
			options.server = strings.TrimPrefix(argument, "--server=")
			if options.server == "" {
				return options, nil, false, usagef("processlab: flag --server needs an argument")
			}
		case strings.HasPrefix(argument, "-server="):
			options.server = strings.TrimPrefix(argument, "-server=")
			if options.server == "" {
				return options, nil, false, usagef("processlab: flag -server needs an argument")
			}
		default:
			return options, nil, false, usagef("processlab: unknown global flag %q", argument)
		}
	}
	return options, nil, help, nil
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	options, remaining, help, err := parseGlobalOptions(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	tree := commandTree()
	if len(remaining) == 0 {
		if help {
			printRootHelp(stdout, tree)
			return 0
		}
		fmt.Fprintln(stderr, "processlab: a command is required")
		printRootHelp(stderr, tree)
		return 2
	}
	selected := tree.child(remaining[0])
	if selected == nil {
		fmt.Fprintf(stderr, "processlab: unknown command %q\n", remaining[0])
		printRootHelp(stderr, tree)
		return 2
	}
	if help {
		printCommandHelp(stdout, selected)
		return 0
	}
	err = selected.execute(context.Background(), options, remaining[1:], stdout, stderr)
	if err == nil {
		return 0
	}
	var usage usageError
	if errors.As(err, &usage) {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printRootHelp(w io.Writer, root *command) {
	fmt.Fprintln(w, "Usage: processlab [global flags] <command> [command flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, root.summary)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, child := range root.children {
		fmt.Fprintf(w, "  %-12s %s\n", child.name, child.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global flags:")
	for _, specification := range root.flags {
		fmt.Fprintf(w, "  --%-10s %s (default %s)\n", specification.name, specification.usage, specification.defaultValue)
	}
}

func printCommandHelp(w io.Writer, c *command) {
	fmt.Fprintf(w, "Usage: processlab %s", c.name)
	if len(c.flags) > 0 {
		fmt.Fprint(w, " [flags]")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w)
	fmt.Fprintln(w, c.summary)
	if len(c.flags) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	for _, specification := range c.flags {
		fmt.Fprintf(w, "  --%-10s <%s> %s (default %s)\n", specification.name, specification.typeName, specification.usage, specification.defaultValue)
	}
}

type serveOptions struct {
	address string
	dbPath  string
}

func newServeCommand() *command {
	options := serveOptions{address: defaultAddr, dbPath: defaultDB}
	return &command{
		name:    "serve",
		summary: "Start the Process Lab web application",
		flags: []commandFlag{
			{
				name:         "addr",
				typeName:     "address",
				defaultValue: defaultAddr,
				usage:        "HTTP listen address",
				register: func(set *flag.FlagSet) {
					set.StringVar(&options.address, "addr", options.address, "HTTP listen address")
				},
			},
			{
				name:         "db",
				typeName:     "path",
				defaultValue: defaultDB,
				usage:        "SQLite database path",
				register: func(set *flag.FlagSet) {
					set.StringVar(&options.dbPath, "db", options.dbPath, "SQLite database path")
				},
			},
		},
		run: func(ctx context.Context, _ globalOptions, stdout io.Writer, stderr io.Writer) error {
			return serve(ctx, options, stdout, stderr)
		},
	}
}

func serve(ctx context.Context, options serveOptions, stdout io.Writer, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	service, err := studio.Open(ctx, options.dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer service.Close()

	app, err := web.New(service)
	if err != nil {
		return fmt.Errorf("create web application: %w", err)
	}
	server := &http.Server{
		Addr:              options.address,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	logger := log.New(stderr, "", log.LstdFlags)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Printf("shutdown: %v", err)
		}
	}()

	fmt.Fprintf(stdout, "Process Lab is running at http://%s\n", options.address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
