# Physical assembly spike

Status: evidence spike only. No production block, port schema, persistence, or
UI change is proposed by this document.

## Recommendation

Defer a production physical-assembly mode. If physical models need to enter
Process Lab before the gaps below are closed, accept them only through an
explicit analysis-only import/conversion workflow that clearly says time
simulation is unavailable. Do not present a physical connection as an
ordinary signal wire, and do not invent a Process Lab port API around the
current `controlsys` names.

The assembly algebra is useful and its frequency-domain answers are correct
for the tested regular pencils. It is not yet an end-to-end product surface:
connected assemblies are singular descriptor systems, Process Lab has no
descriptor time simulator, connected delays are rejected, and the library
does not expose a domain-neutral across/through variable contract.

## What was tested

`internal/studio/physical_assembly_spike_test.go` builds two independent
families of multi-component models using the pinned `controlsys v1.2.0` API.
Each component is the hand-solvable linear one-port model

```text
x_dot_i = -a_i x_i + u_i + f_i
y_i     = x_i
```

where `y` is the connected across value, `f` is the connected through value,
and `u` is an external load. Joining all ports imposes

```text
x_1 = x_2 = ... = x_n
f_1 + f_2 + ... + f_n = 0
```

Adding the differential equations gives the independent signal-flow oracle

```text
Y_i(s) / U_j(s) = 1 / (n*s + sum(a_i))
finite pole     = -sum(a_i) / n
```

This derivation does not use the matrices produced by `AssemblePhysical`.

### Explicit two-component node

Both components bind their node port explicitly to input channel 1 and output
channel 1. With `a = [1, 2]`, the expected transfer from either external load
to either position is

```text
H_ij(s) = 1 / (2*s + 3)
pole    = -1.5
H_ij(j) = 0.2307692308 - 0.1538461538j
```

The test independently constructs and compares the full descriptor equations.
For state order `[x1, x2, f1, f2]`, they are

```text
[1 0 0 0] [x1_dot]   [-1  0  1  0] [x1]   [1 0] [u1]
[0 1 0 0] [x2_dot] = [ 0 -2  0  1] [x2] + [0 1] [u2]
[0 0 0 0] [f1_dot]   [-1  1  0  0] [f1]   [0 0]
[0 0 0 0] [f2_dot]   [ 0  0  1  1] [f2]   [0 0]
```

`Poles` returns the one finite generalized pole and `FreqResponse` matches
`1/(2s+3)` for every input/output pair at four frequencies.

### Implicit three-component node

The first component has two physical ports:

- `external`, explicitly bound to channel 0;
- `node`, implicitly bound with no `Input` or `Output` indices.

The other two components bind their node channels explicitly. With
`a = [1, 2, 3]`, the hand-reduced oracle is

```text
H_ij(s) = 1 / (3*s + 6)
pole    = -2
H_ij(j) = 0.1333333333 - 0.0666666667j
```

The assembly is built twice, with `[external, node]` and `[node, external]`
declaration order on the mixed component. Both full descriptor realizations
and all frequency responses are identical.

That result exposes the actual implicit-binding rule:

1. all explicit bindings reserve their channels in a first pass;
2. implicit ports then claim the lowest unused channels in implicit-port
   declaration order.

Moving one implicit port across an explicit sibling therefore does not change
its binding. Ordering multiple implicit siblings can change which unused
channels they claim. This is deterministic, but it is hidden positional
meaning that should not be serialized as a user-facing physical model.

## Concept and diagram mismatch

Process Lab signal connections and physical connections answer different
questions.

| Concern | Signal-flow sheet | Physical assembly |
| --- | --- | --- |
| Edge meaning | Directed output-to-input value | Undirected membership in a physical node |
| Port causality | Output produces; input consumes | Port contributes both an across value and a through value |
| Connection law | Value propagation | Across equality plus signed through conservation |
| Fan-in | Requires an explicit Sum or routing rule | A multi-port node is the conservation equation |
| Ground | Usually a constant-zero signal | A boundary constraint with a reaction through variable |
| Validation | Width, direction, sample time, algebraic loop | Kind, dimension, sign convention, node solvability, descriptor regularity |

A physical editor therefore needs a separate connection gesture and visual
grammar: undirected nodes, junctions, ground/reference symbols, and visible
across/through sign conventions. Reusing the directed Process Lab arrow would
misstate the equations.

The current library vocabulary is also insufficient for a product schema.
`PhysicalPortDisplacement` and `PhysicalPortEffort` are compatibility tags.
Both kinds are assembled by treating mapped outputs as across quantities and
mapped inputs as through quantities; the kind does not define different
equations or expose named across/through variables. The names are mechanical,
not domain-neutral enough for electrical, thermal, fluid, and process
networks.

## Solvability boundary

The two spike pencils are regular. Their algebraic constraints reduce to one
differential degree of freedom, and `sE-A` is invertible except at the single
finite pole. The generalized-pole and frequency-response implementations can
therefore solve them correctly.

That does not establish general physical-network solvability.
`AssemblePhysical` validates component dimensions, port compatibility,
duplicate edges, and basic node topology, then constructs a descriptor model.
It does not establish a user-facing contract for:

- regularity of the assembled pencil;
- consistent initial conditions;
- redundant or contradictory constraints;
- structural index and hidden constraint differentiation;
- singular direct-feedthrough combinations;
- useful diagnostics that identify the responsible node or component.

Those failures need diagram-level messages before a physical sheet can ship.

## Descriptor and delay limits

Every connected spike has a singular `E`: component states have differential
rows and the introduced through variables have algebraic rows.

- `System.Poles` supports the generalized pencil and excludes infinite poles.
- `System.FreqResponse` correctly evaluates `C(sE-A)^-1B + D` for these
  delay-free assemblies.
- `System.ToExplicit` returns `ErrDescriptorSingular`; this assembly cannot be
  converted by multiplying through by `E^-1`.
- `System.Simulate` is discrete-only, and its discrete path explicitly rejects
  descriptor systems.
- Process Lab's continuous run path calls `controlsys.Lsim`.
  In the pinned version, `Lsim` calls `DiscretizeZOH`, whose standard
  state-space path does not preserve `E`. The spike verifies that this path
  does not produce the one-pole discrete equivalent. It is not a descriptor
  simulation path.
- `AssemblePhysical` rejects any connected component with delay metadata using
  `ErrDescriptorUnsupported`. The persistent spike test covers this boundary.

Frequency analysis alone is not enough for Process Lab's simulation-first
workflow. Silently running the existing `Lsim` path would produce a different
model, while silently eliminating constraints would require a new,
well-specified reduction with consistent-initial-condition behavior.

## Product options considered

### Separate physical sheet

This is the right eventual interaction model, but not yet implementable with
honest execution semantics. It should wait for the exit criteria below.

### Import or conversion

This is the only defensible near-term exposure. An imported assembly could be
read-only and analysis-only, with its descriptor equations and frequency
response visible. Conversion to a signal-flow model should be offered only
when a future constraint-elimination routine proves an equivalent explicit
realization; `ToExplicit` cannot do that for the singular assemblies tested
here.

### Add physical blocks to the existing sheet

Reject. Directed signal wires and physical conservation nodes cannot share the
same port and connection semantics without ambiguity.

### Defer

Recommended. The current capability is a sound library-level analysis
primitive, not a complete Process Lab mode.

## Exit criteria for reconsideration

Revisit a physical sheet only when all of the following exist:

1. an explicit, domain-neutral across/through port vocabulary with orientation
   and sign conventions;
2. persisted explicit channel bindings, without declaration-order inference;
3. regular-pencil and consistent-initial-condition diagnostics attributed to
   diagram nodes;
4. descriptor-aware time simulation with independent trajectory or residual
   checks;
5. a declared descriptor-plus-delay policy;
6. distinct physical-node diagram and validation rules;
7. an explicit conversion contract for assemblies that can be reduced to
   ordinary signal flow.

Until then, no downstream implementation tasks should be opened from this
spike.

Unresolved questions: none
