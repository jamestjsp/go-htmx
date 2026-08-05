---
name: flowsheet-building
description: Build, simulate, analyze, and control Process Lab flowsheets through the processlab CLI.
---

# Process Lab flowsheet building

Use this skill when an agent needs to turn a dynamic-system idea into a
reproducible Process Lab flowsheet, run evidence-producing experiments, or
prototype a controller. The CLI is an HTTP client: start `processlab serve`
once, keep the server address in `PROCESSLAB_ADDR`, and never add a `--db`
flag to a client command. Read [`docs/cli.md`](../../../../docs/cli.md) for
the transport, streams, exit codes, and generated help. Discover current
commands with `processlab help --json` and current block fields with
`processlab block help <kind>`.

The server may also be open in the GUI. Treat model replacement and deletion
as shared-workspace operations: inspect first, prefer `flow apply --dry-run`,
and use `--force` only when the user explicitly intends the destructive
operation.

## Bootstrap

For a reproducible CLI session, build one client binary and run one isolated
server database. Keep the server address in `PROCESSLAB_ADDR`; client commands
must never receive `--db`.

```bash
work_dir="$(mktemp -d /tmp/processlab-skill.XXXXXX)"
cli="$work_dir/processlab"
go build -o "$cli" ./cmd/processlab

server_addr=http://127.0.0.1:18080
"$cli" serve --addr 127.0.0.1:18080 --db "$work_dir/processlab.db" \
  >"$work_dir/server.log" 2>&1 &
server_pid=$!
export PROCESSLAB_ADDR="$server_addr"
trap 'kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; rm -rf "$work_dir"' EXIT

until curl -fsS "$PROCESSLAB_ADDR/" >/dev/null; do
  kill -0 "$server_pid" 2>/dev/null || {
    sed -n '1,120p' "$work_dir/server.log"
    exit 1
  }
  sleep 0.1
done

"$cli" help --json >/dev/null
"$cli" block add --help
"$cli" block set --help
```

Use a different loopback port when another server owns `18080`. The two
general block-help commands above are intentionally server-independent; a
kind-specific help request still needs the running catalog API.

## Workflow

1. Start with a named project and flowsheet. `project list` and `flow list`
   establish whether the user already has work in progress. Create a project
   with `project create "Name"`, then its sheet with `flow create --project
   <project-id> "Loop"` when starting from an empty workspace.

2. Build the graph declaratively. Use `flow dump --flow <flow-id>` as the
   current document, edit the JSON by block name, and preview the replacement:

   ```bash
   processlab flow dump --flow <flow-id> > flow.json
   processlab flow apply --flow <flow-id> --dry-run < flow.json
   processlab flow apply --flow <flow-id> < flow.json
   ```

   Use `block help <kind>` to learn a block's parameters and ports; do not
   duplicate that catalog here. Use `wire list --flow <flow-id>` and
   `block list --flow <flow-id>` to verify the applied graph. Keep source and
   sink blocks in the diagram for experiments, but do not assign them to the
   plant or controller roles.

3. Run a baseline. `sim run --flow <flow-id> --duration <seconds>
   --sample-time <seconds>` writes a tabular series to stdout. Save it when
   the comparison matters, and use `sim show --flow <flow-id> --json` or
   `export --flow <flow-id>` when the complete persisted record is needed.
   A model edit makes the stored run stale; rerun before interpreting it.

4. Assign control roles before controller work. The plant and controller
   blocks must be disjoint, and the diagram must contain both controller
   output → plant input and plant measurement output → controller input
   connections. Then run:

   ```bash
   processlab roles set --flow <flow-id> --plant <plant-block-id> --controller <controller-block-id>
   processlab roles show --flow <flow-id> --json
   ```

   The role fingerprint and version are evidence. A later model or role edit
   makes design candidates stale rather than silently changing their source.
   For a PID2 controller, connect the reference source to its reference input
   and the plant measurement to its measurement input; `roles set` records
   both named boundaries.

5. Discover analysis channels, then analyze the loop. Use `analyze channels
   --flow <flow-id> --json` to get the named `block:port:channel` references.
   Choose the plant input and measurement output deliberately and pass them to
   the relevant `analyze` operation. For robustness, `analyze loop` reports
   named sensitivity and complementary-sensitivity models; compare current
   and candidate results on the same frequency grid. See
   [`docs/loop-sensitivity.md`](../../../../docs/loop-sensitivity.md).

6. Choose the design method from the evidence. Use PID/PID2 design for a SISO
   role pair with a meaningful crossover target. Use bounded `controller tune`
   when several authored parameters or time-domain goals must be searched.
   Use state feedback, estimator, or observer/regulator commands when the
   plant is a named state-space model. Use a parameter `sweep` when the
   question is sensitivity or trade-space exploration rather than selecting
   one controller. Candidate generation is read-only.

7. Review before applying. A controller design command returns an opaque
   candidate id. Run `controller review --flow <flow-id> <candidate-id> --json`
   and inspect source revision, named roles, margins, frequency evidence,
   time responses, warnings, and failed goals. A candidate is not a result
   until its evidence supports the intended operating region. Delayed or
   unstable cases may have reviewable frequency evidence but a missing step
   response; treat that warning as part of the decision.

8. Apply once, then verify. `controller apply <candidate-id> --flow
   <flow-id>` performs the domain's atomic replacement. Rerun the same
   simulation and loop analysis used for the baseline, then compare the saved
   outputs and margins. If the result is wrong and no intervening model or
   role edit occurred, `controller undo <candidate-id> --flow <flow-id>` is
   the narrow one-use reversal. Do not use an old candidate after a stale
   refusal; regenerate it from the current model.

## Simulink control-system walkthrough

The repository's R2026a compatibility fixtures are the reference for supported
Simulink semantics and their provenance. Start with
`internal/studio/testdata/simulink/r2026a/pid_parallel_form.json`: it exercises
the parallel PID/PID2 subset with `P=2`, `I=0.5`, `D=0.25`, `N=20`, setpoint
weight `b=0.7`, derivative weight `c=0.3`, and explicit sample time `0.1`.
Use `block help pid2` for the live field names, then build the named
`Reference -> Controller -> Plant -> Output` graph with `flow apply`.

The evidence-producing sequence is:

```bash
"$cli" project create "Simulink PID example" --json
"$cli" flow list --project <project-id> --json
"$cli" flow apply --flow <flow-id> --dry-run < pid-loop.json
"$cli" flow apply --flow <flow-id> < pid-loop.json
"$cli" sim run --flow <flow-id> --duration 5 --sample-time 0.1 --json > baseline.json
"$cli" analyze channels --flow <flow-id> --json
"$cli" analyze loop --flow <flow-id> --input <input:block:channel> \
  --output <output:block:channel> --points 16 --json
"$cli" roles set --flow <flow-id> --plant <plant-id> --controller <controller-id> --json
candidate=$("$cli" controller pid --flow <flow-id> --type PI \
  --crossover 1 --phase-margin 55 --review-horizon 5 --json | jq -r .id)
"$cli" controller review --flow <flow-id> "$candidate" --json
"$cli" controller apply --flow <flow-id> "$candidate" --json
"$cli" sim run --flow <flow-id> --duration 5 --sample-time 0.1 --json > after.json
"$cli" analyze loop --flow <flow-id> --input <input:block:channel> \
  --output <output:block:channel> --points 16 --json
```

For a model-only check, use `state_space_and_transport_history.json`. A scalar
State-Space case with `A=-1`, zero input, `C=1`, `D=0`, and initial state `2`
should end near `2*e^-1` after one second. These are analytic compatibility
oracles, not MATLAB or Simulink output. Include `sample_time: 0.1` in the
declarative State-Space payload even when `time_domain` is `continuous`; the
live validation path expects that field to be explicit. The complete CLI
acceptance oracle is
`go test ./cmd/processlab -run '^TestFlowsheetBuildingSkillBuildsAndImprovesClosedLoop$' -count=1`.

## Failure handling

- Exit `2` means the workflow invocation or local input is wrong. Stop and
  correct the command; do not retry the same request or reinterpret an empty
  result as a domain answer.
- Exit `1` means the server received a valid request and refused it. Preserve
  the message. It usually names the repair: missing roles, a non-equilibrium
  point, incompatible dimensions, stale candidate, or a failed control
  condition.
- Exit `3` means no server answered. Check `PROCESSLAB_ADDR`, then start
  `processlab serve`; do not open the SQLite file from a client process.
- A stale simulation or candidate is evidence about revision, not permission
  to force an apply. Rebuild or rerun against the current model.
- For a human editing the same server in the browser, re-list and re-dump
  before destructive changes. `flow apply --dry-run` is the safe reconciliation
  check; deletion and `--force` require explicit user intent.

## Scope boundaries

This skill teaches workflow judgment, not a second catalog. It does not list
block parameters, flag tables, or numerical defaults. The live CLI help and
block schemas are authoritative. It also does not turn a nonlinear definition
into a flowsheet block: use the dedicated nonlinear register, linearize, and
EKF commands, and keep those results separate from normal LTI simulation.
