# Control model roles

Process Lab keeps diagram wiring and control-design intent separate. A
flowsheet can simulate without any role specification. Controller-design
workflows begin by assigning one versioned `ControlRoleSpec`.

## Explicit subsystem ownership

The specification names every block in two disjoint subsystems:

- the plant;
- the controller.

Sources and sinks are not subsystem blocks. Boundary channels replace them
while a design model is compiled. This makes the model independent of the
current test signal and display blocks, and avoids guessing ownership from
graph reachability.

Plant boundaries are ordered as:

```text
[exogenous inputs; control inputs] -> [performance outputs; measurement outputs]
```

That ordering is the generalized-plant partition expected by controlsys
synthesis operations. The physical plant is the `control -> measurement`
selection. The controller is explicitly `measurement -> control`.

## Durable named channels

A `NamedChannelRef` carries:

- block ID;
- input or output direction;
- port index;
- channel name.

The name is authoritative. A build resolves its current channel index, so a
consistent MIMO port reorder survives. Renaming or removing the channel makes
the assignment stale and returns a repairable error naming the block, port,
and channel.

Every assigned input boundary must cover its complete vector port. Partial
vector breaks are rejected because a drawn wire connects the whole ordered
vector. A Selector, Permutation, Mux, or Demux block should make a partial or
reordered boundary explicit on the diagram.

## Analysis points

An analysis point has a unique name, a controlsys-supported location
(`plant_input` or `plant_output`), and an ordered channel pairing:

- controller control output to plant control input; or
- plant measurement output to controller measurement input.

Pairs must match the complete ordered boundary and an actual drawn connection.
They are design metadata only. Normal compilation and simulation retain every
wire.

## One build operation

`Studio.BuildControlModels` loads and resolves the specification once, then
returns:

- the physical plant;
- the fixed controller;
- the generalized plant;
- the estimator plant;
- a `controlsys.GeneralizedClosedLoop`;
- open- and closed-loop systems for each analysis point.

The operation owns subsystem compilation, named channel selection, dimension
checks, time-domain/sample-time checks, generalized-loop assembly, and
controlsys error translation. Callers do not reproduce Series, Feedback, or
selection steps.

## Persistence and lifecycle

SQLite stores one JSON specification per flow in `control_model_specs`. The
payload has an explicit version and no legacy topology is inferred.

- A full flowsheet duplicate remaps every block reference and retains the
  design contract.
- Duplicating selected blocks does not copy roles, matching its intentionally
  unwired behavior.
- Deleting an unrelated block retains the contract.
- Deleting any referenced block removes the whole contract atomically, rather
  than leaving a plausible partial plant or controller.
- Flow and project deletion cascade through the flow-owned row.
