# Simulink block expansion

## Research boundary

The core Simulink library groups commonly used blocks into Sources, Continuous,
Math Operations, Signal Routing, Sinks, and Ports & Subsystems. For a
continuous-time transfer-function workbench, the most useful common subset is:

- Step, Constant, and Sine Wave sources;
- Gain and signed Sum math blocks;
- Integrator, Transfer Function, PID Controller, and Transport Delay dynamics;
- Scope and Spectrum Analyzer sinks.

Spectrum Analyzer is a DSP System Toolbox sink rather than a core Simulink
block, but it is included to provide a frequency-domain signal-processing
workflow using Gonum's Hann window and real FFT.

State-Space is also common, but a useful editor needs matrix-aware fields and
MIMO port semantics. Unit Delay and discrete filters need an explicit
sample-time policy. Product, Saturation, Switch, Relay, and logic blocks are
nonlinear or hybrid. Those blocks must not be silently approximated by the
continuous LTI compiler.

## Design

One catalog in `internal/studio` owns each block's identity, category, display
metadata, ports, parameter defaults, editor field schema, parsing, validation,
and compact summary. The HTTP and template layers consume that catalog instead
of repeating block-kind switches.

Block parameters are persisted as version-tolerant JSON. Startup adds the JSON
column to existing SQLite databases and falls back to the original scalar
columns when reading legacy rows.

The compiler realizes each dynamic block as a named `controlsys.System`:

- Gain and Sum use static gain matrices;
- First-order lag and Integrator use continuous state-space realizations;
- Transfer Function uses `controlsys.TransferFunc.StateSpace`;
- PID uses `controlsys.NewPID(...).System`;
- Transport Delay uses `controlsys.PadeDelay`.

Each Step, Constant, or Sine Wave source becomes its own external input channel.
The simulation request creates the waveform matrix, named connections compose
the local systems, and every Scope selects a system output. Signed Sum inputs
follow connection-ID order; one sign broadcasts to every input, while a longer
sign string must match the number of connected inputs.

After the time response, Spectrum Analyzer sinks compute a Hann-windowed,
one-sided amplitude spectrum with Gonum DSP. They report the dominant frequency
and render a separate server-generated SVG frequency plot.

## Acceptance

- Existing databases and seeded behavior remain valid.
- All catalog blocks can be added, edited, persisted, connected, and rendered
  through HTMX.
- New dynamic blocks have independent numerical checks against analytic
  responses or direct control-system identities.
- Invalid improper transfer functions, PID derivative filters, delays, source
  parameters, and sum signs return domain validation errors.
- Formatting, vet, race tests, build, and browser interaction checks pass.

Unresolved questions: none
