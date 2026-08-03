package main

import (
	"context"
	"encoding/json"
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
	defaultServer  = "http://127.0.0.1:8080"
	defaultAddr    = "127.0.0.1:8080"
	defaultDB      = "processlab.db"
	defaultTimeout = 5 * time.Minute
)

type globalOptions struct {
	server        string
	json          bool
	timeout       time.Duration
	commandValues map[string]func() any
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

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	return e.err.Error()
}

func (e *exitError) Unwrap() error {
	return e.err
}

func (e *exitError) ExitCode() int {
	return e.code
}

type commandFlag struct {
	name         string
	typeName     string
	defaultValue string
	usage        string
	register     func(*flag.FlagSet)
	value        func() any
}

type commandArgument struct {
	name        string
	description string
	required    bool
}

func commandBoolFlag(name, usage string, value *bool) commandFlag {
	return commandFlag{
		name: name, typeName: "bool", defaultValue: "false", usage: usage,
		register: func(set *flag.FlagSet) { set.BoolVar(value, name, false, usage) },
		value:    func() any { return *value },
	}
}

func commandInt64Flag(name, typeName string, defaultValue int64, usage string, value *int64) commandFlag {
	return commandFlag{
		name: name, typeName: typeName, defaultValue: fmt.Sprint(defaultValue), usage: usage,
		register: func(set *flag.FlagSet) { set.Int64Var(value, name, defaultValue, usage) },
		value:    func() any { return *value },
	}
}

func commandIntFlag(name, typeName string, defaultValue int, usage string, value *int) commandFlag {
	return commandFlag{
		name: name, typeName: typeName, defaultValue: fmt.Sprint(defaultValue), usage: usage,
		register: func(set *flag.FlagSet) { set.IntVar(value, name, defaultValue, usage) },
		value:    func() any { return *value },
	}
}

func commandFloat64Flag(name, typeName string, defaultValue float64, usage string, value *float64) commandFlag {
	return commandFlag{
		name: name, typeName: typeName, defaultValue: fmt.Sprint(defaultValue), usage: usage,
		register: func(set *flag.FlagSet) { set.Float64Var(value, name, defaultValue, usage) },
		value:    func() any { return *value },
	}
}

func commandStringFlag(name, typeName, defaultValue, usage string, value *string) commandFlag {
	return commandFlag{
		name: name, typeName: typeName, defaultValue: defaultValue, usage: usage,
		register: func(set *flag.FlagSet) { set.StringVar(value, name, defaultValue, usage) },
		value:    func() any { return *value },
	}
}

func documentedBoolFlag(name, usage string) commandFlag {
	var value bool
	return commandBoolFlag(name, usage, &value)
}

func documentedInt64Flag(name, typeName string, defaultValue int64, usage string) commandFlag {
	var value int64
	return commandInt64Flag(name, typeName, defaultValue, usage, &value)
}

func documentedIntFlag(name, typeName string, defaultValue int, usage string) commandFlag {
	var value int
	return commandIntFlag(name, typeName, defaultValue, usage, &value)
}

func documentedFloat64Flag(name, typeName string, defaultValue float64, usage string) commandFlag {
	var value float64
	return commandFloat64Flag(name, typeName, defaultValue, usage, &value)
}

func documentedStringFlag(name, typeName, defaultValue, usage string) commandFlag {
	var value string
	return commandStringFlag(name, typeName, defaultValue, usage, &value)
}

func newCommand(name, summary string, flags []commandFlag, arguments []commandArgument, run func(context.Context, globalOptions, []string, io.Writer, io.Writer) error) *command {
	return &command{name: name, summary: summary, flags: flags, arguments: arguments, run: run}
}

func newVariadicCommand(name, summary string, flags []commandFlag, arguments []commandArgument, run func(context.Context, globalOptions, []string, io.Writer, io.Writer) error) *command {
	command := newCommand(name, summary, flags, arguments, run)
	command.variadic = true
	return command
}

type command struct {
	name      string
	path      string
	summary   string
	flags     []commandFlag
	arguments []commandArgument
	freeform  bool
	variadic  bool
	children  []*command
	help      func(context.Context, globalOptions, []string, io.Writer, io.Writer) error
	run       func(context.Context, globalOptions, []string, io.Writer, io.Writer) error
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
	if c.help != nil && hasHelpFlag(args) {
		return c.help(ctx, options, removeHelpFlags(args), stdout, stderr)
	}
	set := flag.NewFlagSet(c.path, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	help := false
	set.BoolVar(&help, "help", false, "show command help")
	for _, specification := range c.flags {
		specification.register(set)
	}
	commandValues := make(map[string]func() any, len(c.flags))
	for _, specification := range c.flags {
		if specification.value != nil {
			commandValues[specification.name] = specification.value
		}
	}
	parseArgs := moveCommandFlags(args, c.valueFlagNames(), c.boolFlagNames())
	if err := set.Parse(parseArgs); err != nil {
		return usagef("%s: %v", c.path, err)
	}
	if help {
		printCommandHelp(stdout, c)
		return nil
	}
	parsedArgs := set.Args()
	if len(c.children) > 0 && !c.freeform {
		if len(parsedArgs) == 0 {
			return usagef("%s: choose %s", c.path, joinCommandNames(c.children))
		}
		child := c.child(parsedArgs[0])
		if child == nil {
			return usagef("%s: unknown command %q; choose %s", c.path, parsedArgs[0], joinCommandNames(c.children))
		}
		return child.execute(ctx, options, parsedArgs[1:], stdout, stderr)
	}
	if !c.freeform {
		if !c.variadic && len(parsedArgs) > len(c.arguments) {
			return usagef("%s: unexpected argument %q", c.path, parsedArgs[len(c.arguments)])
		}
		for index, argument := range c.arguments {
			if argument.required && (index >= len(parsedArgs) || parsedArgs[index] == "") {
				return usagef("%s: argument %q is required", c.path, argument.name)
			}
		}
	}
	if c.run == nil {
		return usagef("%s: command has no implementation", c.path)
	}
	options.commandValues = commandValues
	return c.run(ctx, options, parsedArgs, stdout, stderr)
}

func (options globalOptions) commandValue(name string) (any, bool) {
	read, ok := options.commandValues[name]
	if !ok {
		return nil, false
	}
	return read(), true
}

func (options globalOptions) commandBool(name string) bool {
	value, ok := options.commandValue(name)
	if !ok {
		return false
	}
	result, _ := value.(bool)
	return result
}

func (options globalOptions) commandString(name string) string {
	value, ok := options.commandValue(name)
	if !ok {
		return ""
	}
	result, _ := value.(string)
	return result
}

func (options globalOptions) commandInt64(name string) int64 {
	value, ok := options.commandValue(name)
	if !ok {
		return 0
	}
	result, _ := value.(int64)
	return result
}

func (options globalOptions) commandInt(name string) int {
	value, ok := options.commandValue(name)
	if !ok {
		return 0
	}
	result, _ := value.(int)
	return result
}

func (options globalOptions) commandFloat64(name string) float64 {
	value, ok := options.commandValue(name)
	if !ok {
		return 0
	}
	result, _ := value.(float64)
	return result
}

func (options globalOptions) commandStrings(name string) []string {
	value, ok := options.commandValue(name)
	if !ok {
		return nil
	}
	result, _ := value.([]string)
	return result
}

func (c *command) valueFlagNames() []string {
	result := make([]string, 0, len(c.flags)*2)
	for _, specification := range c.flags {
		if specification.typeName == "bool" {
			continue
		}
		result = append(result, "--"+specification.name, "-"+specification.name)
	}
	return result
}

func (c *command) boolFlagNames() []string {
	result := make([]string, 0, len(c.flags)*2+3)
	if len(c.children) == 0 {
		result = append(result, "--help", "-help", "-h")
	}
	for _, specification := range c.flags {
		if specification.typeName == "bool" {
			result = append(result, "--"+specification.name, "-"+specification.name)
		}
	}
	return result
}

func hasHelpFlag(args []string) bool {
	for _, argument := range args {
		if argument == "--help" || argument == "-help" || argument == "-h" {
			return true
		}
	}
	return false
}

func removeHelpFlags(args []string) []string {
	result := make([]string, 0, len(args))
	for _, argument := range args {
		if argument != "--help" && argument != "-help" && argument != "-h" {
			result = append(result, argument)
		}
	}
	return result
}

func joinCommandNames(commands []*command) string {
	names := make([]string, len(commands))
	for index, child := range commands {
		names[index] = child.name
	}
	return strings.Join(names, ", ")
}

func assignCommandPaths(root *command) {
	var visit func(*command, string)
	visit = func(current *command, path string) {
		current.path = path
		for _, child := range current.children {
			visit(child, path+" "+child.name)
		}
	}
	visit(root, root.name)
}

func commandTree() *command {
	root := &command{
		name:    "processlab",
		summary: "Process Lab command-line interface",
		flags: []commandFlag{
			{name: "server", typeName: "url", defaultValue: defaultServer, usage: "Process Lab server URL (env: PROCESSLAB_ADDR)"},
			{name: "json", typeName: "bool", defaultValue: "false", usage: "write machine-readable output"},
			{name: "timeout", typeName: "duration", defaultValue: defaultTimeout.String(), usage: "maximum time for one server request"},
			{name: "help", typeName: "bool", defaultValue: "false", usage: "show help"},
		},
	}
	helpOptions := struct{ json bool }{}
	root.children = []*command{
		{
			name:    "help",
			summary: "Show command help",
			flags: []commandFlag{
				{
					name:         "json",
					typeName:     "bool",
					defaultValue: "false",
					usage:        "write machine-readable help",
					register: func(set *flag.FlagSet) {
						set.BoolVar(&helpOptions.json, "json", false, "write machine-readable help")
					},
				},
			},
			arguments: []commandArgument{{name: "command", description: "command to describe"}},
			run: func(_ context.Context, options globalOptions, args []string, stdout io.Writer, _ io.Writer) error {
				target := root
				if len(args) == 1 {
					target = root.child(args[0])
					if target == nil {
						return usagef("processlab help: unknown command %q", args[0])
					}
				}
				if helpOptions.json || options.json {
					return writeJSONHelp(stdout, target)
				}
				if target == root {
					printRootHelp(stdout, root)
				} else {
					printCommandHelp(stdout, target)
				}
				return nil
			},
		},
		newServeCommand(),
		newProjectCommand(),
		newFlowCommand(),
		newBlockCommand(),
		newWireCommand(),
		newSimulationCommand(),
		newAnalysisCommand(),
		newRolesCommand(),
		newSweepCommand(),
		newControllerCommand(),
		newIdentCommand(),
		newStudyCommand(),
		newNonlinearCommand(),
		newExportCommand(),
		newLogCommand(),
	}
	assignCommandPaths(root)
	return root
}

func parseGlobalOptions(args []string) (globalOptions, []string, bool, error) {
	options := globalOptions{server: os.Getenv("PROCESSLAB_ADDR"), timeout: defaultTimeout}
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
		case argument == "--timeout" || argument == "-timeout":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
				return options, nil, false, usagef("processlab: flag %s needs an argument", argument)
			}
			index++
			parsed, err := time.ParseDuration(args[index])
			if err != nil || parsed <= 0 {
				return options, nil, false, usagef("processlab: invalid timeout %q", args[index])
			}
			options.timeout = parsed
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
		case strings.HasPrefix(argument, "--timeout=") || strings.HasPrefix(argument, "-timeout="):
			value := argument[strings.IndexByte(argument, '=')+1:]
			parsed, err := time.ParseDuration(value)
			if err != nil || parsed <= 0 {
				return options, nil, false, usagef("processlab: invalid timeout %q", value)
			}
			options.timeout = parsed
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
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		fmt.Fprintln(stderr, err)
		return exit.ExitCode()
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
	fmt.Fprintf(w, "Usage: %s", c.path)
	if len(c.children) > 0 {
		fmt.Fprint(w, " <command>")
	}
	if len(c.flags) > 0 {
		fmt.Fprint(w, " [flags]")
	}
	for _, argument := range c.arguments {
		if argument.required {
			fmt.Fprintf(w, " <%s>", argument.name)
		} else {
			fmt.Fprintf(w, " [<%s>]", argument.name)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w)
	fmt.Fprintln(w, c.summary)
	if len(c.children) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Commands:")
		for _, child := range c.children {
			fmt.Fprintf(w, "  %-12s %s\n", child.name, child.summary)
		}
	}
	if len(c.arguments) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Arguments:")
		for _, argument := range c.arguments {
			required := "optional"
			if argument.required {
				required = "required"
			}
			fmt.Fprintf(w, "  %-12s %s (%s)\n", argument.name, argument.description, required)
		}
	}
	if len(c.flags) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Flags:")
		for _, specification := range c.flags {
			fmt.Fprintf(w, "  --%-10s <%s> %s (default %s)\n", specification.name, specification.typeName, specification.usage, specification.defaultValue)
		}
	}
}

type commandHelpJSON struct {
	Name      string             `json:"name"`
	Summary   string             `json:"summary"`
	Flags     []flagHelpJSON     `json:"flags"`
	Arguments []argumentHelpJSON `json:"arguments"`
	Commands  []commandHelpJSON  `json:"commands"`
}

type flagHelpJSON struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Default     string `json:"default"`
	Description string `json:"description"`
}

type argumentHelpJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

func writeJSONHelp(w io.Writer, c *command) error {
	if err := json.NewEncoder(w).Encode(commandHelp(c)); err != nil {
		return fmt.Errorf("write help: %w", err)
	}
	return nil
}

func commandHelp(c *command) commandHelpJSON {
	result := commandHelpJSON{
		Name:      c.name,
		Summary:   c.summary,
		Flags:     make([]flagHelpJSON, 0, len(c.flags)),
		Arguments: make([]argumentHelpJSON, 0, len(c.arguments)),
		Commands:  make([]commandHelpJSON, 0, len(c.children)),
	}
	for _, specification := range c.flags {
		result.Flags = append(result.Flags, flagHelpJSON{
			Name:        specification.name,
			Type:        specification.typeName,
			Default:     specification.defaultValue,
			Description: specification.usage,
		})
	}
	for _, argument := range c.arguments {
		result.Arguments = append(result.Arguments, argumentHelpJSON{
			Name:        argument.name,
			Description: argument.description,
			Required:    argument.required,
		})
	}
	for _, child := range c.children {
		result.Commands = append(result.Commands, commandHelp(child))
	}
	return result
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
		run: func(ctx context.Context, _ globalOptions, _ []string, stdout io.Writer, stderr io.Writer) error {
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
