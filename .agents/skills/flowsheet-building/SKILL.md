---
name: flowsheet-building
description: Use when building, simulating, analyzing, or controlling a Process Lab flowsheet through the processlab CLI, including MIMO plants, transport delays, control roles, and controller candidate design.
---

# Process Lab flowsheet building

The CLI is an HTTP client. Start `processlab serve` once, keep its address in
`PROCESSLAB_ADDR`, and never pass `--db` to a client command. `processlab help
--json` and `processlab block help <kind>` are the authoritative reference;
this skill carries only workflow judgment. See [`docs/cli.md`](../../../../docs/cli.md).

A human may hold the same server open in the GUI. Inspect before replacing,
prefer `flow apply --dry-run`, and use deletion and `--force` only on explicit
intent.

## Bootstrap

```bash
work_dir="$(mktemp -d /tmp/processlab-skill.XXXXXX)"
cli="$work_dir/processlab"
go build -o "$cli" ./cmd/processlab
"$cli" serve --addr 127.0.0.1:18080 --db "$work_dir/processlab.db" \
  >"$work_dir/server.log" 2>&1 &
server_pid=$!
export PROCESSLAB_ADDR=http://127.0.0.1:18080
trap 'kill "$server_pid" 2>/dev/null; rm -rf "$work_dir"' EXIT
until curl -fso /dev/null "$PROCESSLAB_ADDR/"; do
  kill -0 "$server_pid" 2>/dev/null || { cat "$work_dir/server.log"; exit 1; }
  sleep 0.1
done
```

Keep `server.log`: it holds the only actionable text for some refusals. Use
another loopback port when 18080 is taken.

## Workflow

1. `project create`, then `flow list --project <id>` — a new project already
   owns one flowsheet.
2. Build declaratively: `flow dump` → edit the JSON by block name →
   `flow apply --dry-run` → `flow apply`. Verify with `block list` and
   `wire list`. Keep sources and sinks in the diagram, out of the roles.
3. Baseline with `sim run --duration <s> --sample-time <s>`. Any model edit
   makes a stored run stale; rerun before comparing.
4. `roles set --plant <ids> --controller <ids>`, then read `roles show --json`
   back and confirm the inferred boundary covers the loop you meant. The
   fingerprint is evidence; a later edit makes candidates stale.
5. `analyze channels`, then `analyze loop`. Compare current and candidate on
   one frequency grid — see [`docs/loop-sensitivity.md`](../../../../docs/loop-sensitivity.md).
6. Design from the evidence: PID for a SISO role pair with a meaningful
   crossover target; `controller tune` for a bounded search over authored
   parameters; state, estimator, and observer commands for a named
   state-space plant; `sweep` when the question is sensitivity rather than
   selecting one controller. Candidate generation is read-only.
7. `controller review --json` before applying. Read source revision, margins,
   warnings, and failed goals. Frequency evidence with a missing step response
   is normal for delayed or indeterminate loops: part of the decision, not a
   blocker.
8. `controller apply`, then rerun the same simulation and loop analysis used
   for the baseline. `controller undo` reverses once, only if nothing else
   changed.

## Traps

| Symptom | Cause and repair |
|---|---|
| Help shows `--transfer-numerators` | Help prints flag names and no ports at all. On a placed block, `block show <id> --json` is the bridge: `parameterValues` gives the exact snake_case keys `flow apply` expects (`transfer_numerators`), and `inputs`/`outputs` give port widths and channel names. |
| `is not exposed by the compiled model` | `analyze loop --input` takes an exogenous channel from the `Inputs:` list, not a plant input. Outputs may be any listed output. |
| `requires one explicit controller block` | Design commands need exactly one controller block. `roles set` accepts several, succeeds, and silently infers only the first loop's boundary. |
| `needs one scalar input for each of its N output channels` | The plant role must compile as a closed subsystem. A mux shared by two loops cannot sit inside it, so a decentralized MIMO diagram admits no role set that allows PID design. Design each loop on its own SISO flowsheet, copy the gains back with `block set`, and verify on the coupled model. |
| `The operation could not be completed.` | A refusal that named no repair; the cause is in the server log. Domain refusals now carry their reason, so treat this message as a bug worth reporting rather than something to work around. |
| Review grid on a delayed loop | The review step is coarsened automatically to divide every transport delay the loop carries, so an exact `delay` no longer needs horizon arithmetic or a Padé substitution. `--base-step` sets the control-model build step, not the review grid. |
| `flag provided but not defined: -flow` | `block set <id>` and `block show <id>` take a positional block id and no `--flow`. |

## Exit codes

`0` success. `1` domain refusal — preserve the message; when it names no
repair, read the server log. `2` local usage error — correct the command, do
not retry it. `3` no server — check `PROCESSLAB_ADDR`; never open the SQLite
file from a client.

The acceptance oracle is `go test ./cmd/processlab -run
'^TestFlowsheetBuildingSkillBuildsAndImprovesClosedLoop$' -count=1`. Simulink
subset semantics and their MathWorks provenance live in
`internal/studio/testdata/simulink/r2026a/`. This skill does not turn a
nonlinear definition into a flowsheet block: use the `nonlinear` commands and
keep those results separate from LTI simulation.
