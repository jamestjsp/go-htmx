# Process Lab CLI

The CLI is a client of a running Process Lab server. It never opens the
SQLite database; `processlab serve` is the process that owns `--db` and the
domain connection described in [`internal/studio/store.go:101`](../internal/studio/store.go#L101).

## Start the server

```bash
processlab serve
```

The server prints its address and serves the browser application and `/api/v1`
JSON API. Keep it running in one terminal, then use the client commands from
another. The client resolves the server in this order:

1. `--server <url>`;
2. `PROCESSLAB_ADDR`;
3. `http://127.0.0.1:8080`.

The global `--timeout` controls one request and defaults to five minutes. It is
available before the command, for example:

```bash
processlab --server http://127.0.0.1:8080 --timeout 30s project list
```

## Output and exit codes

Successful data goes to stdout. Diagnostics go to stderr. `--json` writes the
complete machine-readable response for commands that return records; it can be
global or command-local. The client does not print progress or warnings into a
JSON response.

- `0` — command completed successfully;
- `1` — the server or domain refused a valid request;
- `2` — local usage, flag, file, JSON, or TSV input error;
- `3` — the server could not be reached.

Examples below assume the seeded server is running and that the selected
flowsheet is `1`. Each is an executable command; use `processlab help --json`
or the generated reference below to discover flags without contacting a
server.

## Top-level command examples

| Command | Example |
| --- | --- |
| `help` | `processlab help --json` |
| `serve` | `processlab serve --help` |
| `project` | `processlab project list` |
| `flow` | `processlab flow list` |
| `block` | `processlab block list --flow 1` |
| `wire` | `processlab wire list --flow 1` |
| `sim` | `processlab sim run --flow 1 --duration 1 --sample-time 0.1` |
| `analyze` | `processlab analyze channels --flow 1` |
| `roles` | `processlab roles show --flow 1` |
| `sweep` | `processlab sweep run --help` |
| `controller` | `processlab controller pid --help` |
| `ident` | `processlab ident estimate --help` |
| `study` | `processlab study show --help` |
| `nonlinear` | `processlab nonlinear register --help` |
| `export` | `processlab export --flow 1` |
| `log` | `processlab log --flow 1` |

Use a command's `--json` form for scripts. For example, these commands return
the project register and a simulation result without presentation formatting:

```bash
processlab project list --json
processlab sim show --flow 1 --json
```

The nonlinear workflows use persisted expression definitions. Register JSON on
stdin, pass a JSON linearization request file, or feed the EKF the same
time-plus-series TSV shape emitted by `sim run`:

```bash
processlab nonlinear register < definition.json
processlab nonlinear linearize --definition models/decay@1 --operating-point origin.json
processlab nonlinear ekf --definition models/decay@1 < samples.tsv
```

See [`nonlinear-workflows.md`](nonlinear-workflows.md) for expression,
linearization, and EKF semantics.

<!-- generated:cli:start -->
## Generated command and block reference

The sections below are generated from `processlab help --json`, each command's help output, and the live block catalog API.

### Commands

| Command | Summary |
| --- | --- |
| `processlab help` | Show command help |
| `processlab serve` | Start the Process Lab web application |
| `processlab project` | List and manage Process Lab projects |
| `processlab flow` | List and manage flowsheets |
| `processlab block` | Discover, add, and configure library blocks |
| `processlab wire` | Connect and disconnect flowsheet signals |
| `processlab sim` | Run and inspect flowsheet simulations |
| `processlab analyze` | Discover channels and run control analyses |
| `processlab roles` | Assign and inspect control model roles |
| `processlab sweep` | Run catalog-backed parameter sweeps |
| `processlab controller` | Design, tune, and review controller candidates |
| `processlab ident` | Estimate models from measured data |
| `processlab study` | Inspect compiled model provenance |
| `processlab nonlinear` | Register, linearize, and estimate nonlinear models |
| `processlab export` | Export complete flowsheet results |
| `processlab log` | Show recent flowsheet activity |

#### `processlab help --help`

```text
Usage: processlab help [flags] [<command>]

Show command help

Arguments:
  command      command to describe (optional)

Flags:
  --json       <bool> write machine-readable help (default false)
```

#### `processlab serve --help`

```text
Usage: processlab serve [flags]

Start the Process Lab web application

Flags:
  --addr       <address> HTTP listen address (default 127.0.0.1:8080)
  --db         <path> SQLite database path (default processlab.db)
```

#### `processlab project --help`

```text
Usage: processlab project <command>

List and manage Process Lab projects

Commands:
  list         List projects
  show         Show a project
  create       Create a project
  rename       Rename a project
  delete       Delete a project
```

#### `processlab flow --help`

```text
Usage: processlab flow <command>

List and manage flowsheets

Commands:
  list         List flowsheets
  dump         Dump a declarative flowsheet document
  apply        Apply a declarative flowsheet document
  show         Show a flowsheet
  create       Create a flowsheet
  rename       Rename a flowsheet
  duplicate    Duplicate a flowsheet
  delete       Delete a flowsheet
  reorder      Reorder a project's flowsheets
```

#### `processlab block --help`

```text
Usage: processlab block <command>

Discover, add, and configure library blocks

Commands:
  list         List library or flowsheet blocks
  show         Show a block
  add          Add a catalog block
  set          Update a block
  mv           Move blocks
  rm           Delete blocks
  cp           Duplicate blocks
  help         Show catalog block help
```

#### `processlab wire --help`

```text
Usage: processlab wire <command>

Connect and disconnect flowsheet signals

Commands:
  list         List signal connections
  connect      Connect two signal endpoints
  rm           Remove signal connections
```

#### `processlab sim --help`

```text
Usage: processlab sim <command>

Run and inspect flowsheet simulations

Commands:
  run          Run a flowsheet simulation
  show         Show the latest simulation
```

#### `processlab analyze --help`

```text
Usage: processlab analyze <command>

Discover channels and run control analyses

Commands:
  channels     List selectable analysis channels
  dynamics     Run a dynamics analysis
  frequency    Run a frequency analysis
  loop         Run a loop analysis
  show         Show cached analyses
```

#### `processlab roles --help`

```text
Usage: processlab roles <command>

Assign and inspect control model roles

Commands:
  show         Show assigned control roles
  set          Assign control model roles
```

#### `processlab sweep --help`

```text
Usage: processlab sweep <command>

Run catalog-backed parameter sweeps

Commands:
  run          Run catalog-backed parameter sweeps
```

#### `processlab controller --help`

```text
Usage: processlab controller <command>

Design, tune, and review controller candidates

Commands:
  pid          Design a PID controller
  state        Design state-space controllers
  robust       Design a robust controller
  tune         Tune a controller
  review       Review a controller candidate
  apply        Apply a controller candidate
  undo         Undo a controller candidate
```

#### `processlab ident --help`

```text
Usage: processlab ident <command>

Estimate models from measured data

Commands:
  estimate     Estimate a frequency-response model
  era          Estimate a state-space model with ERA
```

#### `processlab study --help`

```text
Usage: processlab study <command>

Inspect compiled model provenance

Commands:
  show         Show compiled model provenance
```

#### `processlab nonlinear --help`

```text
Usage: processlab nonlinear <command>

Register, linearize, and estimate nonlinear models

Commands:
  register     Register a persisted nonlinear definition
  linearize    Linearize a nonlinear definition
  ekf          Estimate a nonlinear model with an EKF
```

#### `processlab export --help`

```text
Usage: processlab export [flags]

Export complete flowsheet results

Flags:
  --flow       <id> flowsheet id (default 0)
  --json       <bool> write machine-readable output (default false)
```

#### `processlab log --help`

```text
Usage: processlab log [flags]

Show recent flowsheet activity

Flags:
  --flow       <id> flowsheet id (default 0)
  --limit      <count> maximum number of events (default 8)
  --json       <bool> write machine-readable output (default false)
```

### Block catalog

| Kind | Label | Category | Description | Parameters |
| --- | --- | --- | --- | --- |
| `source` | Step | Sources | Initial-to-final step | amplitude, initial_value, step_time |
| `constant` | Constant | Sources | Constant signal | value |
| `vector_constant` | Vector Constant | Sources | Named constant vector | vector, output_names |
| `sine` | Sine Wave | Sources | Biased sinusoid | amplitude, bias, frequency, phase |
| `gain` | Gain | Math | Scale a signal | gain |
| `matrix_gain` | Matrix Gain | Math | Named vector gain y = Du | d, input_names, output_names |
| `mux` | Mux | Routing | Assemble named scalar channels | output_names |
| `demux` | Demux | Routing | Decompose a named vector | input_names |
| `selector` | Selector | Routing | Select a named channel subset | input_names, output_names |
| `permutation` | Permutation | Routing | Reorder named vector channels | input_names, output_names |
| `sum` | Sum | Math | Signed signal sum | signs, signal_width |
| `vector_sum` | Vector Sum | Math | Signed sum of named vectors | signs, input_names, output_names |
| `lag` | First-order Lag | Continuous | 1 / (τs + 1) | time_constant |
| `integrator` | Integrator | Continuous | Continuous 1 / s | initial_condition |
| `transfer` | Transfer Function | Continuous | Proper SISO model | numerator, denominator |
| `pid` | PID Controller | Control | Parallel-form PID with filtered derivative | proportional, integral, derivative, filter_coefficient, time_domain, sample_time |
| `pid2` | 2-DOF PID Controller | Control | Parallel-form 2-DOF PID with filtered derivative | proportional, integral, derivative, filter_coefficient, setpoint_weight, derivative_weight, time_domain, sample_time |
| `delay` | Transport Delay | Continuous | Exact delay with explicit Padé and Thiran approximations | delay, initial_output, delay_mode, approximation, sample_time_mode, sample_time |
| `state_space` | State-Space | Models | Named continuous or discrete MIMO model | a, b, c, d, initial_state, input_names, output_names, state_names, time_domain, sample_time |
| `mimo_transfer` | MIMO Transfer Function | Models | Named transfer matrix with row denominators and pairwise delays | transfer_numerators, transfer_denominators, transfer_delays, input_names, output_names, time_domain, sample_time |
| `zpk` | Zero-Pole-Gain | Models | Named MIMO zero-pole-gain model | zeros, poles, d, input_names, output_names, time_domain, sample_time |
| `frd` | Frequency Response Data | Models | Named complex MIMO samples for frequency-domain workflows | input_names, output_names, frequencies, frequency_response, frequency_unit, response_unit, time_domain, sample_time |
| `unit_delay` | Unit Delay | Discrete | Exact one-sample memory | initial_condition, signal_width, sample_time_mode, sample_time |
| `discrete_transfer` | Discrete Transfer Function | Discrete | Proper SISO model in z | numerator, denominator, sample_time_mode, sample_time |
| `discrete_state_space` | Discrete State-Space | Discrete | Named x[k+1]=Ax+Bu, y=Cx+Du | a, b, c, d, initial_state, input_names, output_names, state_names, sample_time_mode, sample_time |
| `discretized_transfer` | Discretized Transfer | Discrete | Explicit continuous-to-discrete conversion | numerator, denominator, conversion_method, sample_time_mode, sample_time |
| `scope` | Scope | Sinks | Plot a signal |  |
| `vector_scope` | Vector Scope | Sinks | Plot named vector channels | input_names |
| `spectrum` | Spectrum Analyzer | Sinks | Hann-windowed FFT |  |
<!-- generated:cli:end -->
