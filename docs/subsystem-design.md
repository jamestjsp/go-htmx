# Hierarchical subsystems

How a flowsheet gets to live inside a block — the structure `README.md`
currently rules out in one sentence: "Hierarchical subflowsheet or subsystem
blocks inside a flowsheet are not yet supported." This is a decision document.
It picks one data model, one navigation model, and one compilation strategy,
and it names the tables, columns, functions, routes and refusal messages the
work would add. No implementation code is written here.

Read this before touching `internal/studio/store.go`'s migrations,
`lifecycle.go`, `workspace.go`, or `simulate.go`'s `compileFlow`.

It depends on the port work that landed in `52e35f1`: connections carry
`source_port` and `target_port`, and a kind's port count is derived from its
definition and its parameters in one place. Child Inport and Outport blocks
becoming the parent block's ports is the whole reason that task was sequenced
first.

## What is already true

Verified against the code, not assumed. Each of these decides something below.

**Block ids are unique across the whole database.** `blocks.id` is
`INTEGER PRIMARY KEY` on one table, so a block in a child sheet cannot collide
with a block in its parent. This is what makes the compiler's existing signal
names sufficient for a flattened graph (see *Compilation*).

**Nothing moves a block between flowsheets, and nothing moves a flowsheet
between projects.** `MoveBlocks` and `MoveBlock` write `x` and `y` only, and
the `Studio` surface has no reparenting operation of any kind. This is what
makes the containment relation a tree by construction rather than by a check.

**`ON DELETE CASCADE` already carries every deletion.** `DeleteProject` and
`DeleteFlow` both say, in their own comments, that deleting children by hand
would be a second and divergent statement of what a container holds. Foreign
keys are on for every connection via the DSN, and SQLite fires cascades
recursively, so a chain `flows → blocks → flows → blocks` needs no new code.

**`flowSelect` is the one query authority for every flowsheet list.** The tab
strip (`projectFlows`) and the register (`flowsByProject`) both read it, which
is what stops the amber dot in one from contradicting the other. Anything that
must not appear in either list has to be excluded there, once.

**Five queries assume every flow is a top-level flow.** `CurrentWorkspace`,
`ProjectWorkspace`, `Current`, `DeleteFlow`'s survivor count, and
`ensureFlowPositions` all order or count `flows` rows with no notion that one
might live inside a block. Each is named in *Data model* below; missing any one
of them is how a subsystem sheet ends up in the tab strip or lets a project be
emptied of every sheet a user can open.

**A refusal is an explicit domain message.** `checkWiredInputPorts` is the
precedent this design leans on hardest: shrinking a Sum's signs past a wired
port is refused, naming the port, rather than dropping the wire. Every port
rule below is the same rule wearing a different hat.

## Data model

### The recommendation

**The child flowsheet carries the edge.** One nullable column on `flows`:

```sql
ALTER TABLE flows ADD COLUMN parent_block_id INTEGER
    REFERENCES blocks(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX IF NOT EXISTS flows_parent_block_id_idx
    ON flows(parent_block_id);
```

`parent_block_id IS NULL` is the definition of a top-level sheet. A non-null
value names the subsystem block the sheet lives inside.

Three things follow from putting the edge on this end, and they are the whole
argument:

- **The cascade runs the right way.** Deleting the block deletes the sheet,
  and the sheet's own cascade takes its blocks, connections, events and runs —
  recursively into grandchildren, because a cascaded delete of a `blocks` row
  fires the `flows` cascade beneath it. Nothing hand-deletes anything, which
  is the discipline `DeleteProject` and `DeleteFlow` already state.
- **"One block, one sheet" is a schema constraint, not a convention.** The
  unique index refuses a second sheet on one block. SQLite treats NULLs in a
  unique index as distinct, so every top-level sheet still coexists happily,
  and no table rebuild is needed — unlike the ports migration, which had to
  rebuild `connections` to change a UNIQUE clause.
- **It is the shape `project_id` already has.** A row names its owner; the
  owner's deletion cascades. `ensureProjects` adds `project_id` by
  `ALTER TABLE ... ADD COLUMN ... REFERENCES ... ON DELETE CASCADE`, which is
  the same statement shape, already proven against this driver. (SQLite
  permits `ADD COLUMN` with a `REFERENCES` clause only when the default is
  NULL, which is exactly this column.)

### What was rejected

**`blocks.child_flow_id` — the block pointing at its sheet.** It is the same
edge stored on the other end, and it is worse in three specific ways. The
foreign key would cascade backwards: `ON DELETE CASCADE` on that column means
deleting the *sheet* deletes the *block*, so `DeleteFlow` on a child sheet
would silently erase a block on the parent's canvas. Deleting the block would
leave the sheet orphaned, requiring a hand-written delete — a second statement
of what a subsystem contains. And nothing in the schema would stop two blocks
naming one sheet, which is a library link arriving by accident (see *Out of
scope*) and which quietly breaks the compiler's instance identity.

**A separate `subsystems` join table.** Correct, and pure overhead: the
relation is 1:1 and total on child sheets, so a table would add a join to
every query for a fact one nullable column states.

### Which project a child sheet belongs to

**The same project as the sheet that contains it, stored in the same
`project_id` column, NOT NULL as it is today.** The alternative — leaving
`project_id` NULL for child sheets and deriving it by climbing — would make
`Register`'s two-query accounting into a recursive one and would put a second
authority on "which project does this row belong to".

The invariant holds by construction: `project_id` is written once, at
creation, from the parent sheet's own row, and nothing ever moves a flow
between projects. Deleting a project reaches a child sheet twice over — once
through `project_id`, once through its parent block — and both paths delete
the same row, so the redundancy costs nothing.

### The five queries that must learn about child sheets

Every list the UI draws still has one query authority; each of these gains one
predicate and no second copy of the rule.

| Site | Change | What breaks without it |
| --- | --- | --- |
| `flowSelect` (`workspace.go`) | Rename to `topLevelFlowSelect` and fold `WHERE flows.parent_block_id IS NULL` into it; the two callers append with `AND` | A subsystem sheet appears as a tab and as a register chip |
| `CurrentWorkspace`, `ProjectWorkspace` | `AND flows.parent_block_id IS NULL` | The application opens on a subsystem sheet, since a child's `position` is 0 and its id can be lower than a later top-level sheet's |
| `Current` (`studio.go`) | Same | `renderFailure`'s fallback renders a subsystem sheet after an error |
| `DeleteFlow`'s survivor count | `AND parent_block_id IS NULL` | A project can be emptied of every sheet the tab strip can draw, leaving the strip blank and `ProjectWorkspace` with nothing to open — the exact failure the "last flowsheet" refusal exists to prevent |
| `ensureFlowPositions` and `hasInvalidFlowPositions` | Both restricted to `parent_block_id IS NULL` | A child sheet at position 0 makes every project's positions look non-dense, so the repair runs on every `Open` and renumbers subsystem sheets into the tab strip |

Child flows are created with `position = 0` and it is never read.

`ReorderFlows` needs no new message: it builds `belongs` from the project's
flowsheets, which is now the top-level ones, so a child id lands on the
existing "the new order must list each of this project's N flowsheets exactly
once" refusal.

### The child sheet's name

**The subsystem block names the sheet. The `flows.name` column of a child row
is written as the empty string and is never read.**

The column is `NOT NULL`, so something has to go in it. A maintained copy of
the block's name would be a second authority that goes stale the moment a
rename path is added that does not know about it; an empty string is honest
about which row is the authority, and it is unreachable from the interface
because every list is filtered to `parent_block_id IS NULL` and the trail
query (*Navigation*) reads the parent block's name for exactly these rows.

`RenameFlow` refuses a child sheet: **"a subsystem sheet is renamed by
renaming its block"**.

### Cascade rules

| Operation | What happens |
| --- | --- |
| Delete a subsystem block | FK cascade removes its child sheet, that sheet's blocks and connections, and recursively every subsystem beneath it. No hand-written delete. |
| Delete a top-level sheet | Its blocks cascade, which cascades its subsystem sheets, to any depth. |
| Delete a project | Unchanged: flows → blocks → child flows → … |
| `DeleteFlow` on a child sheet | Refused: **"a subsystem sheet is deleted with its block"**. The landing-sheet query it would otherwise run is tab-strip order, which a child sheet has no place in. |
| `DisconnectBlock` on a subsystem block | Unchanged. Wires are removed; the child sheet is untouched. |

The canvas confirmation for deleting a subsystem block names it and says
everything inside it goes too. It does not count the subtree: the count needs
a recursive query on every render, and "and everything inside it" is the
sentence that actually matters.

### Duplication

Duplicating anything that contains a subsystem block **deep-copies the
subtree**. Sharing would be a library link, which is out of scope and which
would break the compiler's instance identity.

`copyBlocks` and `copyConnections` (`lifecycle.go`) become the leaves of one
recursive unit:

```go
// copyFlowContents copies one flowsheet's blocks and wires into another, and
// recursively copies the child sheet of every subsystem block it copied, so a
// duplicate simulates identically to its source at every level.
func copyFlowContents(ctx context.Context, tx *sql.Tx, sourceFlowID, targetFlowID int64) error
```

Both existing callers change:

- **`DuplicateFlow`** calls `copyFlowContents` instead of the two copy
  functions in sequence. Its documented behaviour is unchanged — run history
  is not copied, at any level, and only the copy's own sheet gets the
  "Duplicated from …" event.
- **`DuplicateBlocks`** (the in-sheet selection copy) gains the same recursion
  for any subsystem block in the selection. Its rule that wires *between* the
  originals are not copied is unchanged and applies only to the parent sheet's
  wires: the wiring *inside* a duplicated subsystem is part of what the block
  is, not a relationship between two selected blocks, so it is copied. That
  distinction should be written into `DuplicateBlocks`'s comment, because it
  reads like a contradiction otherwise.

`DuplicateFlow` on a child sheet is refused: **"a subsystem sheet is
duplicated with its block"**.

### What stops a cycle

A subsystem containing itself, directly or transitively, is **impossible by
construction rather than prevented by a check**:

1. Each block belongs to exactly one flow (`blocks.flow_id`, NOT NULL).
2. Each flow has at most one parent block (`UNIQUE(parent_block_id)`).
3. The only operation that creates the edge — adding a subsystem block —
   creates a **fresh, empty** child flow in the same transaction. There is no
   operation anywhere that points an existing flow at a different block, and
   no operation that moves a block to another flow.

Those three make the containment relation a forest. A cycle would need an
existing sheet to be re-attached under one of its own descendants, and nothing
can re-attach a sheet at all.

That is worth writing down rather than replacing with a runtime check, because
the check would be dead code that hides the fact that the property is
structural. What must be written down instead is the condition under which it
stops holding: **the day a subsystem block can point at a sheet that already
exists** — a library link, a model reference, a "paste as reference" — the
argument fails at step 3, and the operation that creates such an edge must
walk `flows.parent_block_id → blocks.flow_id` upward from the target and
refuse when it reaches the sheet being edited. That is the check to write
then, and not before.

**Depth is bounded anyway**, for cost rather than correctness:

```go
// maxSubsystemDepth bounds how deeply subsystems nest. It bounds the
// recursive cascade, the flattening walk, and the length of a qualified block
// name, and it is the one place the limit is stated.
const maxSubsystemDepth = 8
```

Adding a subsystem block on a sheet already at depth 8 is refused:
**"subsystems may not nest more than 8 levels deep"**. The depth of the
current sheet comes from the same recursive walk the trail query uses.

## Port surface

### Three new kinds

| Kind | Role on its own sheet | What it is |
| --- | --- | --- |
| `BlockSubsystem` (`"subsystem"`) | One input port per child Inport, one output port per child Outport | The block that owns a sheet |
| `BlockInport` (`"inport"`) | No input, one output | One input terminal of the sheet's owning block |
| `BlockOutport` (`"outport"`) | One input, no output | One output terminal of the sheet's owning block |

Inport and Outport are legal **only on a subsystem's own sheet**. A top-level
sheet has no parent block to give terminals to, so adding one there is
refused: **"Inport blocks belong inside a subsystem"**. The rule is one field
on the definition, read by `AddBlock` (which enforces it) and by the palette
(which omits the two kinds on a top-level sheet), in the same way `HasInput()`
is read by both the wiring rules and the canvas:

```go
// insideSubsystem is true for the two kinds that only mean something on a
// subsystem's own sheet: an Inport and an Outport are the owning block's
// terminals, and a top-level sheet has no owning block.
insideSubsystem bool
```

### The subsystem's definition entry

The brief for this spike asks what one definition entry would have to say. It
says less than any other kind, which is the point:

```go
BlockSubsystem: {
    BlockDefinition: BlockDefinition{
        Kind: BlockSubsystem, Label: "Subsystem", Category: "Ports & Subsystems",
        Description: "A flowsheet inside a block", Glyph: "▣", Tag: "STRUCTURE",
    },
    // No editable fields. A subsystem's ports are its child sheet's Inport and
    // Outport blocks, so there is nothing here for the inspector to offer
    // beyond the name every block has.
    Parameters: nil,
    variadic:    true,
    inputPorts:  func(p Parameters) int { return len(p.Inports) },
    outputPorts: func(p Parameters) int { return len(p.Outports) },
    // realize is nil and must stay nil. A subsystem has no realization of its
    // own: flattening removes it before compileFlow sees a block list, so a
    // unit-gain default would never be reached and stating one would suggest
    // it could be.
    created: createSubsystemSheet,
    summary: func(p Parameters) string {
        return fmt.Sprintf("%d in · %d out", len(p.Inports), len(p.Outports))
    },
},
```

Two new fields on `blockDefinition`, and one widening of an existing
derivation.

**`Parameters.Inports []string` and `Parameters.Outports []string`** are the
subsystem's port list, in port order, holding the names of the child's Inport
and Outport blocks. This is the same move Sum's `Signs` already makes: the
port list is a parameter, so `inputPortCount(parameters)` stays a pure
function of the definition and the block's own parameters and the catalog
never touches the database. Nothing edits these fields through the inspector;
they are written by exactly one function (below). `cloneParameters` must copy
both slices alongside `Numerator` and `Denominator`.

**`outputPorts func(Parameters) int`** is the counterpart of `inputPorts`, and
`outputPortCount` becomes parameter-aware:

```go
func (d blockDefinition) outputPortCount(parameters Parameters) int {
    if d.role == roleSink {
        return 0
    }
    if d.outputPorts != nil {
        return d.outputPorts(parameters)
    }
    return 1
}
```

`Block.OutputPortCount()` passes `b.Parameters`; every other caller is
unchanged, and every existing kind keeps `outputPorts` nil and therefore its
current answer.
`TestEveryVariadicKindDerivesItsInputPortsFromParameters` gains its output
counterpart, so a kind that varies its outputs without saying how is caught
the same way.

**`checkWiredOutputPorts`** mirrors `checkWiredInputPorts`: an edit that would
take away an output port a wire is sitting on is refused, naming the port.
Note the asymmetry — an input port holds one wire, an output port fans out —
so the message counts: **"Reactor has 2 wires on output port 1; disconnect
them first"**.

**`created func(ctx context.Context, tx *sql.Tx, block Block) error`** runs
inside `AddBlock`'s transaction for kinds whose block is not the whole of what
exists. Subsystem is the only one: it inserts the child flow with the parent's
`project_id`, `parent_block_id = block.ID`, `name = ''`, `position = 0`. Nil
for every other kind. This keeps the catalog the single authority for what a
kind *is*, rather than adding a `if kind == BlockSubsystem` to `AddBlock`.

### One rule has to move

`compileFlow`'s arity walk refuses a variadic block with no inputs:

```go
case arityVariadic:
    if len(inputs) == 0 {
        return compiledFlow{}, invalid("%s needs at least one input", block.Name)
    }
```

That is Sum's rule, not variadic's. A subsystem with no Inports is
legitimate — a self-contained sheet with its own Step source is the obvious
case — so the check moves into Sum's own `checkInputs` hook, which already
exists and already carries Sum's other cross-field rule. The message text is
unchanged and no test asserts it, so this is a pure relocation.

### How child ports become parent ports

One function owns the parent's port surface:

```go
// syncSubsystemPorts restates a subsystem block's port list from its child
// sheet's Inport and Outport blocks, which are the only authority for it. It
// runs inside the transaction of every child-sheet edit that can change that
// set, so a port the canvas draws is always a port the child sheet has.
func syncSubsystemPorts(ctx context.Context, tx *sql.Tx, childFlowID int64) error
```

Called by `AddBlock`, `DeleteBlock`, `DeleteBlocks`, `UpdateBlock` and
`DuplicateBlocks` when the edited flow has a parent block. It does four
things, in order:

1. Reads the child's Inport blocks and Outport blocks, **ordered by
   `blocks.id`**. That order is the port order. There is no port-number
   parameter in the first release; see *Out of scope*.
2. Refuses duplicate names within either list: **"this subsystem already has
   an input named Feed"**. Names are how the diff in step 3 matches old ports
   to new ones, and they are what the canvas draws beside each pip, so they
   have to be unique. The rule is enforced here rather than in
   `validateBlockUpdate`, because it is a property of the sheet, not of one
   block.
3. Diffs the new list against the parent block's stored `Inports` / `Outports`
   by name, and applies the wire rules below.
4. Writes the parent block's parameters and stamps the parent sheet's
   `model_updated_at` through `touchModel`, which climbs (see *Staleness*).

### What happens to existing wires

| Child edit | Parent's port list | Parent's wires |
| --- | --- | --- |
| **Add** an Inport | Appended. Every existing index keeps its meaning. | Untouched. The new port is unwired, so the parent sheet now refuses to simulate until it is wired — a truthful refusal naming the port, and the parent's tab dot goes amber. |
| **Rename** an Inport | The entry's text changes; indices do not move. | Untouched. The pip's label changes and the inspector's connection list follows. |
| **Remove** an Inport whose parent port carries a wire | — | **Refused**: "Feed carries a wire on Reactor; disconnect it on the parent sheet first". The child edit rolls back with it, so the Inport is not deleted either. |
| **Remove** an unwired Inport | The entry is dropped; every higher port index shifts down one. | Wires on the higher ports are renumbered down with the ports they name, in the same transaction. |
| The same three, for Outports | As above | As above, except that an output port may carry several wires and the refusal counts them. |

The renumbering in the last row is not a silent repair, and the distinction is
worth stating precisely: **the index moves, the identity does not.** A wire
drawn to the port called "Setpoint" is still on "Setpoint" afterwards; only
the integer that addresses it changed, because the list it indexes got
shorter. Nothing else in the system could have kept that wire on the right
signal. The refusal in the row above is what guarantees no wire is ever
*dropped* — the destructive case is the one that gets refused, exactly as
`checkWiredInputPorts` refuses shrinking a wired Sum.

The parent sheet's event log records the change on the parent —
"Removed input Feed from Reactor" — because that is the sheet whose model
changed shape.

## Navigation

### Breadcrumb descent, not tabs

**A subsystem sheet never appears in the tab strip and never appears in the
register.** The tab strip lists a project's top-level sheets, is reorderable
by drag and by keyboard, and is the only place `flows.position` means
anything. A subsystem sheet has no independent existence to order, and a strip
that grew a tab every time someone opened a subsystem would stop being a list
of the project's drawings. The register's "N sheets" keeps meaning "N tabs"
for the same reason.

Descent is a **breadcrumb**, rendered above the canvas:

```
Reactor temperature loop  ›  Reactor  ›  Inner loop
```

The first crumb is the top-level sheet's name; every later crumb is the name
of the subsystem block that owns the sheet below it. Each crumb is an anchor
with the same three attributes a tab carries — `href` to the canonical URL,
`hx-get` to the fragment URL, `hx-target="#workbench"`, `hx-push-url` to the
canonical one — so a crumb behaves exactly as a tab does, with or without
JavaScript.

### The canonical URL

**`/projects/{projectID}/flows/{childFlowID}` — the URL shape that already
exists, with no new route.** A child sheet's flow id is its identity, as a
top-level sheet's is. `Workspace(projectID, flowID)` already joins `flows` to
`projects`, so a child sheet of another project is already a 404.
`GET /flows/{flowID}/workbench` serves its fragment unchanged.

The alternative — a path-style URL such as
`/projects/2/flows/5/subsystems/17` — was rejected. It requires a new route
and a new resolution path for a lookup the flow id already answers, it changes
whenever the tree changes, and the breadcrumb it appears to encode is better
computed from the row than parsed from the address.

**Back** therefore behaves exactly as it does between tabs today: descending
and ascending each push a canonical URL, so Back walks the sheets you visited
in the order you visited them, across levels and across the tab strip alike.
The fragment URL is never pushed, for the reason `docs/workbench-ergonomics.md`
already gives — it renders a bare `<main>` with no stylesheet when reloaded or
shared.

Two things fall out of reusing the flow id, and both are free:
`processlab:viewport:<flowID>` gives every subsystem sheet its own pan and
zoom, and `data-flow-id` on `#workbench` changes on descent, so the
per-sheet viewport logic in `js/viewport.js` needs no change at all.

### The trail is one query

```go
// Trail is the open sheet's ancestry, root first, ending with the sheet
// itself. It is the one authority for a sheet's displayed name: a top-level
// sheet is named by its own row, a subsystem sheet by the block that owns it.
type Crumb struct {
    FlowID int64
    Label  string
    Href   string
}
```

`Workspace` gains `Trail []Crumb`, filled by one recursive query:

```sql
WITH RECURSIVE trail(flow_id, parent_block_id, depth) AS (
    SELECT id, parent_block_id, 0 FROM flows WHERE id = ?
    UNION ALL
    SELECT owner.id, owner.parent_block_id, trail.depth + 1
    FROM trail
    JOIN blocks ON blocks.id = trail.parent_block_id
    JOIN flows owner ON owner.id = blocks.flow_id
)
SELECT trail.flow_id, COALESCE(parent.name, flows.name)
FROM trail
JOIN flows ON flows.id = trail.flow_id
LEFT JOIN blocks parent ON parent.id = trail.parent_block_id
ORDER BY trail.depth DESC
```

The `COALESCE` is where the empty `flows.name` of a child row is answered: a
child sheet always has a parent block, so the left join always supplies a
name, and the top-level row is the only one that falls through to `flows.name`.
The same walk answers the depth check `AddBlock` needs, so there is one
recursive query shape in the codebase, not two.

The tab strip's `Active` flag then compares against `Trail[0].FlowID` rather
than the open flow's id, so descending into a subsystem leaves its root sheet's
tab lit rather than lighting none.

### Gestures

| Gesture | Action | Rationale |
| --- | --- | --- |
| Double-click a subsystem block | Open its sheet | Simulink's gesture, and the canvas's only free double-click |
| Right-click a subsystem block → **Open subsystem** | Same | The keyboard-reachable path, through the shared `menu.js` |
| Click a breadcrumb | Open that ancestor | Same contract as a tab |
| `Cmd`/`Ctrl` + `↑` | Ascend one level | Bare arrows nudge and `Ctrl`+`Shift`+arrows move tabs; this pair is free |
| `Cmd`/`Ctrl` + `↓` | Descend into the selected subsystem | — |

Both chords are guarded by `typingInAField` and by `ProcessLab.menu.ownsKey`,
for the reasons `docs/workbench-ergonomics.md` gives at length. They do
nothing at the root and nothing without a subsystem selected, rather than
wrapping or guessing.

The client needs the child sheet's id to descend without a round trip.
`snapshot`'s block query supplies it with one left join and no extra query:

```sql
SELECT blocks.id, blocks.flow_id, blocks.kind, blocks.name, blocks.x, blocks.y,
       blocks.parameters_json, COALESCE(child.id, 0)
FROM blocks
LEFT JOIN flows child ON child.parent_block_id = blocks.id
WHERE blocks.flow_id = ? ORDER BY blocks.id
```

`Block` gains `ChildFlowID int64`, zero for every kind but Subsystem, and the
block card carries it as a `data-` attribute.

## Compilation

### Flatten before compiling

`compileFlow` does not learn about subsystems. A new pass in the studio turns
a sheet and everything beneath it into one block list and one connection list,
and `compileFlow` compiles that exactly as it compiles a sheet today:

```go
// expandFlow flattens a flowsheet and every subsystem beneath it into the one
// graph the compiler sees: no Subsystem, Inport or Outport block survives it.
// A sheet with no subsystem blocks expands to its own blocks and wires,
// unchanged, so the linear path is the degenerate case of the flattening
// rather than a branch beside it.
func (s *Studio) expandFlow(ctx context.Context, flowID int64) ([]Block, []Connection, error)
```

`Run` calls it in place of reading `snapshot.Blocks` and
`snapshot.Connections`, and passes the result to `simulate` unchanged. The
property in that comment is the one to protect: **for every sheet that exists
today, `expandFlow` returns exactly what the snapshot holds**, so every
numeric assertion in `simulate_test.go`, and the seeded model's 1.590114,
are reached by the same arithmetic in the same order.

Cost is three queries whatever the depth, in the shape `Register` already
uses: one recursive query for the subtree's flow ids, one for every block in
them, one for every connection in them.

### The splice

Depth-first, so a subsystem is always spliced into its parent after its own
children are already flat. For each subsystem block `S` on the sheet:

1. **Inputs.** For each input port `i` of `S`, let `I` be the child's `i`-th
   Inport block and `X.p → S.i` the parent wire into that port. For every
   child wire `I.0 → Y.q`, emit `X.p → Y.q`. Then drop `I` and both original
   wires.
2. **Outputs.** For each output port `o` of `S`, let `O` be the child's `o`-th
   Outport block and `W.p → O.0` the child wire into it. For every parent wire
   `S.o → Z.r`, emit `W.p → Z.r`. Then drop `O` and both original wires.
3. Drop `S`.

Inputs are spliced before outputs so that an Inport wired straight through to
an Outport composes: step 1 leaves `X.p → O.0`, and step 2 reads that as the
wire into `O` and produces `X.p → Z.r`.

Inport and Outport blocks are removed rather than realized as unit gains.
Either is numerically exact — a unit gain is what every source and sink
already realizes — but removing them keeps the composed system as small as the
sheet the user drew and keeps `ConnectByName` free of an algebraic hop per
port.

Two boundary cases are refusals, because the alternative is a message naming a
block the user cannot see on the sheet they are looking at:

- **An input port with no wire.** `compileFlow` would otherwise report
  "Valve gain is not connected" about a block inside the subsystem. Refused at
  the boundary instead: **"Reactor has no signal on input port 1
  (Setpoint)"**. Port indices are zero-based here, as they are in every
  existing port message.
- **An Outport with nothing driving it inside the child.** Refused:
  **"Temperature is not driven inside Reactor"**.

Two boundary cases are deliberately not refusals. An Inport that nothing
inside the child consumes drops the parent's wire — a signal into a dead end
is legal today and stays legal. An output port with no parent wire drops the
child's wire into the Outport, for the same reason.

Flattening is bounded:

```go
// maxExpandedBlocks bounds the graph one Run may compile. Flattening is the
// first operation whose cost is set by rows the user is not looking at, so it
// states a limit rather than discovering one.
const maxExpandedBlocks = 1024
```

Exceeding it is refused: **"this flowsheet expands to more than 1024
blocks"**.

### Namespacing, and why the existing names already suffice

The compiler names signals `block_%d_source`, `block_%d_input` (and
`block_%d_input_%d` once `VJKI5N` lands) and `block_%d_output`, all keyed on
the block id. In a flattened graph those names are already unique, and the
reason is a short theorem rather than a hope:

1. `blocks.id` is `INTEGER PRIMARY KEY` on one table, so ids are unique across
   every flowsheet in the database.
2. The containment relation is a tree, and each subsystem block owns exactly
   one child sheet (`UNIQUE(parent_block_id)`), so flattening visits each
   block row exactly once. No row is ever instantiated twice.
3. Therefore the flattened list holds distinct ids, and every name derived
   from one is distinct.

**Depending on that silently would be fragile, so the design makes the
dependence explicit.** `expandFlow` states the invariant in its comment, and a
test — `TestExpandedGraphHasDistinctSignalNames`, over a three-level
fixture — asserts it rather than trusting it.

Step 2 is the one that would fail first. **The day a subsystem sheet can be
instantiated more than once** — a library link, a model reference — the block
id stops being the instance identity, and the fix is named now so nobody has
to rediscover it: flattening already carries each block's *path*, the list of
subsystem block ids from the root, for the naming below; a
`signalPrefix(path)` prepended in the three name functions is the whole
change. It is not built now because nothing can produce a second instance.

### Names the user reads

Flattening rewrites each expanded block's `Name` to its qualified instance
name — `"Reactor / Inner loop / Temperature"` — leaving its `ID` alone. That
single move carries the path into everything without touching `compileFlow`:

- Every refusal `compileFlow` already produces names the sheet the block is
  on: "Reactor / Valve gain is not connected".
- A Scope inside a subsystem produces a `Series` named
  "Reactor / Temperature", so the parent's trend legend is unambiguous and a
  subsystem's internals are observable without descending.
- `Series.BlockID` still names a real row. `newChartView` reads only
  `Series.Name`, so nothing looks the id up against the open sheet's blocks.

The 48-character block-name limit applies to stored names, not to the composed
string; `maxSubsystemDepth` bounds its length.

### Cycles across levels

**Every cycle that could span levels is already refused at `Connect` time, on
one sheet, by the check that exists.**

`Connect` runs `pathExists` over the edited sheet's own connections, treating
a subsystem block as a single vertex. That is enough, and here is why: take
any cycle in the flattened graph and project each of its blocks onto the
topmost subsystem block containing it, or onto itself if it is on the sheet
being run. An edge internal to a child projects to a self-loop and drops out;
an edge crossing a subsystem boundary projects to an edge incident to that
subsystem block. What remains is a closed walk on one sheet — the deepest
sheet that contains the whole cycle — and that sheet's own `Connect` check
refused it when it was drawn.

The check is **conservative**, and that is worth saying out loud: because a
subsystem is one vertex, a feedback path through a subsystem is refused even
when it would be acyclic after flattening (`S.out0 → G → S.in1`, where output
0 does not in fact depend on input 1). That costs nothing today — cycles are
refused wholesale, as `README.md` states — and it is the same conservatism the
engine roadmap's T12 spike will have to revisit for feedback in general.

`compileFlow`'s own cycle rejection stays as the backstop it already is: it
catches a graph assembled from rows an older version left behind, which is why
"a connection references a missing block" exists too. Its message is unchanged;
`TestCompileRejectsCycle` asserts only that it contains "cycle".

### Staleness

A subsystem sheet is never run on its own — `Run` refuses it: **"a subsystem
sheet is simulated from the sheet that contains it"**. Its Inports have no
driver, and a run that treated them as silent sources would answer a question
nobody asked.

But an edit inside a subsystem changes what its ancestors simulate, so
`touchModel` climbs:

```sql
WITH RECURSIVE ancestors(flow_id) AS (
    SELECT ?
    UNION ALL
    SELECT blocks.flow_id
    FROM ancestors
    JOIN flows ON flows.id = ancestors.flow_id
    JOIN blocks ON blocks.id = flows.parent_block_id
)
UPDATE flows SET updated_at = ?, model_updated_at = ?
WHERE id IN (SELECT flow_id FROM ancestors)
```

That is the whole change, and it belongs in `touchModel` precisely because
`touchModel` is already the one place that says which operations count as a
model edit — the boundary `flowSelect` reads to light the amber dot. Without
it, editing a gain three levels down would leave the root sheet's tab claiming
its last run is current.

**`touchLayout` does not climb.** Dragging a block inside a subsystem changes
nothing any simulation depends on, at any level.

Events stay on the sheet they happened on. The parent's feed shows what
happened on the parent — "Added Reactor", "Removed input Feed from Reactor" —
not every keystroke three levels down.

## Every refusal this design adds

Collected so the wording is decided once. All are `*studio.ValidationError`,
in the existing voice: lower case, no trailing period, naming the subject and
the way out.

| Message | Raised by |
| --- | --- |
| `a subsystem sheet is deleted with its block` | `DeleteFlow` |
| `a subsystem sheet is renamed by renaming its block` | `RenameFlow` |
| `a subsystem sheet is duplicated with its block` | `DuplicateFlow` |
| `a subsystem sheet is simulated from the sheet that contains it` | `Run` |
| `subsystems may not nest more than 8 levels deep` | `AddBlock` |
| `Inport blocks belong inside a subsystem` | `AddBlock` |
| `this subsystem already has an input named Feed` | `syncSubsystemPorts` |
| `this subsystem already has an output named Temperature` | `syncSubsystemPorts` |
| `Feed carries a wire on Reactor; disconnect it on the parent sheet first` | `syncSubsystemPorts` |
| `Reactor has 2 wires on output port 1; disconnect them first` | `checkWiredOutputPorts` |
| `Reactor has no signal on input port 1 (Setpoint)` | `expandFlow` |
| `Temperature is not driven inside Reactor` | `expandFlow` |
| `this flowsheet expands to more than 1024 blocks` | `expandFlow` |

## Out of a first subsystem release

Named explicitly, with the reason, because each of these is a thing someone
will reasonably ask for and none of them is needed to make a flowsheet live
inside a block.

- **Masked parameters.** A subsystem exposing its own inspector fields that
  drive parameters of blocks inside it. This is a second parameter system —
  binding, validation, and a mask editor — on top of a feature that does not
  exist yet. Nothing below depends on it.
- **Library links and model references.** Two blocks sharing one child sheet,
  or a sheet stored once and instantiated many times. This is the single
  largest exclusion, and it is load-bearing: it is what keeps the containment
  relation a tree by construction (*What stops a cycle*) and what keeps the
  block id valid as the compiler's instance identity (*Namespacing*). Adding
  it later means writing the ancestor check and the `signalPrefix(path)`
  qualifier, both named above.
- **Port reordering.** Port order is the child's Inport blocks in `blocks.id`
  order. To reorder, delete and re-add. Reordering arrives as a `PortNumber`
  parameter validated by `syncSubsystemPorts`, which is where the sibling
  blocks are already in hand; it is excluded now because it is a second way to
  say what the sheet already says.
- **Create subsystem from selection.** Wrapping N selected blocks, moving them
  to a new sheet, and synthesising the Inports and Outports their crossing
  wires imply. It is the gesture that makes subsystems pleasant, and it is a
  rewiring algorithm with its own edge cases that has no meaning until the
  model underneath it exists.
- **Expand a subsystem back into its parent.** The inverse gesture, blocked on
  the same reasoning.
- **Root-level Inport and Outport.** A top-level sheet exposing external
  terminals. Source blocks are this application's external channels, and a
  second mechanism for the same thing would need a story for how the two
  compose.
- **Simulating a subsystem sheet on its own.** Refused, above. It needs
  per-port test inputs, which is a feature of its own.
- **Atomic, enabled, triggered, and for-each subsystems**, per-subsystem
  execution order, and per-subsystem sample times. All of them are engine
  semantics, and the engine roadmap has not reached even one nonlinear block.
- **Bus and vector signals.** Every signal stays scalar, as the engine roadmap
  already commits.
- **Goto/From tags**, cross-sheet copy and paste, moving a sheet between
  projects.
- **Showing subsystems in the register**, or a project-wide sheet tree. The
  register lists top-level sheets, and "N sheets" keeps meaning "N tabs".

## Task list, dependency ordered

Ready to file. Ergo ids are not assigned here.

| Task | Depends on | Deliverable |
| --- | --- | --- |
| **H1** Containment column and the five top-level queries | — | `flows.parent_block_id`, the unique index, `ensureSubsystemFlows` ordered after `ensureProjects` and **before** `ensureFlowPositions`; `flowSelect` → `topLevelFlowSelect`; the filters in `CurrentWorkspace`, `ProjectWorkspace`, `Current`, `DeleteFlow`'s survivor count, and both position queries. No new block kinds — a database with no subsystems must behave identically, and a legacy database must migrate with its tab order untouched. Test: the existing pre-projects fixture opens and re-opens unchanged |
| **H2** Parameter-derived output ports | — (the landed port work) | `outputPorts` hook, `outputPortCount(parameters)`, `checkWiredOutputPorts`, the output half of the variadic-kinds guard test, and the relocation of "needs at least one input" from `compileFlow`'s variadic case into Sum's `checkInputs`. Every existing kind's answers unchanged. Runs in parallel with H1 |
| **H3** The three kinds and the `created` hook | H1, H2 | `BlockSubsystem`, `BlockInport`, `BlockOutport`; `Parameters.Inports`/`Outports` and their clone; `insideSubsystem` read by `AddBlock` and the palette; `maxSubsystemDepth` and its refusal; `created` creating the child sheet in `AddBlock`'s transaction; `Block.ChildFlowID` from `snapshot`'s left join |
| **H4** `syncSubsystemPorts` | H3 | The one authority for the parent's port surface: the id-ordered read, the unique-name refusal, the add/rename/remove diff, the orphan refusal, the renumbering of surviving wires, and `touchModel` climbing to every ancestor. Tests for each row of the wire table |
| **H5** Cascade and deep duplication | H3 | `copyFlowContents` recursive, used by `DuplicateFlow` and `DuplicateBlocks`; the three lifecycle refusals on child sheets; a cascade test at `maxSubsystemDepth` proving one `DELETE` removes the whole subtree |
| **H6** `expandFlow` | H4, H5, and ideally `VJKI5N` | The flattening pass, its three queries, the splice, the two boundary refusals, `maxExpandedBlocks`, qualified names, and `Run` refusing a child sheet. Tests: a subsystem-free sheet expands to itself and every existing numeric assertion holds; a two-level sheet matches the same model drawn flat to 1e-12; distinct signal names over a three-level fixture |
| **H7** Navigation | H1, H3 | `Workspace.Trail` and the recursive trail query; the breadcrumb partial; `Active` by `Trail[0]`; the descent and ascent gestures in `js/contextmenu.js`, `js/input.js` and `js/shortcuts.js`; no new route |
| **H8** Subsystem ports on the canvas | H4, and `KE3PPL` | N labelled input pips and M labelled output pips on a subsystem card, drawn from the same derivation the wiring rules read; the inspector's connection list naming ports by their Inport names |
| **H9** Verification and documentation | H6, H7, H8 | A CDP pass in the style of `docs/workbench-ergonomics.md`'s record — build a subsystem, wire it, descend and ascend, Back across levels, delete a wired Inport and read the refusal, simulate and compare against the flat equivalent, restart and reopen. Replace the "not yet supported" paragraph in `README.md`; add the descent gestures to `docs/workbench-ergonomics.md` |

`H1` and `H2` are independent of each other and of everything already in
flight. `H6` should follow `VJKI5N` (compile wiring by port) so that flattening
produces port-qualified names rather than order-dependent ones; it is not
strictly blocked, but landing it first would mean writing the splice against a
naming scheme that is about to change.

## What dependent tasks must know

- **The edge lives on the child.** `flows.parent_block_id`, never
  `blocks.child_flow_id`. Anything that reads "which sheet does this block
  own" reads it through that column.
- **`parent_block_id IS NULL` is the definition of a top-level sheet**, and it
  belongs in `topLevelFlowSelect` rather than in each caller, for the same
  reason the amber dot's condition does.
- **The catalog stays pure.** A subsystem's port counts come from
  `Parameters.Inports` / `Outports`, written by `syncSubsystemPorts`, so
  `inputPortCount(parameters)` never learns about the database.
- **The child sheet's Inport and Outport blocks are the only authority for the
  parent's ports.** The parameters are a restatement kept current by one
  function; nothing else writes them and no editor field offers them.
- **A destructive port change is refused, a shifted index is not.** Removing a
  wired port is refused naming the port; removing an unwired one renumbers the
  wires above it, because the wire stays on the port it was drawn to.
- **The containment relation is a tree by construction, not by a check.** The
  argument depends on there being no way to attach an existing sheet to a
  block. Any task that adds one owns the ancestor check.
- **`expandFlow` is the whole of the hierarchy as far as the compiler is
  concerned.** `compileFlow` must not learn what a subsystem is, and a sheet
  with no subsystem blocks must reach `simulate` with exactly the lists the
  snapshot holds.
- **Block ids are the instance identity, and that is only true while a sheet
  can be instantiated once.** Anything that changes that must prepend
  `signalPrefix(path)`.
- **Editing a subsystem makes every ancestor stale.** `touchModel` climbs;
  `touchLayout` does not.
- **A subsystem sheet is not a tab, not a register chip, and not runnable.**
  Three refusals and one query predicate say so; none of them may be softened
  without deciding what the tab strip is a list of.

Unresolved questions: whether the parent-level cycle check's conservatism
(refusing a feedback path through a subsystem that would flatten acyclically)
ever bites in practice, which the engine roadmap's T12 feedback spike will
have to answer anyway; whether a subsystem's trend traces should be
selectable per level rather than all appearing on the root's chart; and
whether "create subsystem from selection" should be scheduled immediately
after H9, since without it every subsystem must be built block by block on an
empty sheet.
