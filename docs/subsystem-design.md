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
with a block in its parent. That is half of why the compiler's signal names
need no prefix in a flattened graph; the other half is that since `663759f`
every name is built from one block's id and one of that block's own ports. See
*Namespacing*, which is stated against `simulate.go` at HEAD and would have
been false a commit earlier.

**Nothing moves a block between flowsheets, and nothing moves a flowsheet
between projects.** `MoveBlocks` and `MoveBlock` write `x` and `y` only, and
the `Studio` surface has no reparenting operation of any kind. This is what
makes the containment relation a tree by construction rather than by a check.

**`ON DELETE CASCADE` already carries every deletion.** `DeleteProject` and
`DeleteFlow` both say, in their own comments, that deleting children by hand
would be a second and divergent statement of what a container holds. Foreign
keys are on for every connection via the DSN, and SQLite fires cascades
recursively, so a chain `flows → blocks → flows → blocks` needs no new code.

**`flowSelect` is the one query authority for every flowsheet *list*, and only
for lists.** The tab strip (`projectFlows`) and the register (`flowsByProject`)
both read it, which is what stops the amber dot in one from contradicting the
other. But four other operations reach `flows` through raw `SELECT`s of their
own — `ReorderFlows`, both of `DeleteFlow`'s landing-sheet queries, and
`generatedFlowName` — so excluding child sheets from `flowSelect` excludes them
from the two lists and from nothing else.

**Nine queries in four files assume every flow is a top-level flow.** They are
enumerated in *Data model* below and the enumeration is exhaustive: it is every
statement in the repository that reads `flows` without going through
`flowSelect`, plus `flowSelect` itself. Missing one is not cosmetic — three of
them break a working gesture outright, and one bricks the migration of every
legacy database.

**A refusal is an explicit domain message, and a doomed control is absent
rather than present.** `checkWiredInputPorts` is the precedent this design
leans on hardest: shrinking a Sum's signs past a wired port is refused, naming
the port, rather than dropping the wire. `registerRowView.CanDelete` is the
other: the domain's refusal to delete the last project is carried into the view
so the control never appears doomed. Every port rule and every chrome decision
below is one of those two rules wearing a different hat.

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

### The nine queries that must learn about child sheets

This is every statement in the repository that reads `flows`. Renaming
`flowSelect` covers two of them and **nothing else** — the rest are raw
`SELECT`s that do not go through it, which is the trap this table exists to
close. Each row gains one predicate and no second copy of the rule.

| # | Site | Change | What breaks without it |
| --- | --- | --- | --- |
| 1 | `flowSelect` (`workspace.go:118`) | Rename to `topLevelFlowSelect` and fold `WHERE flows.parent_block_id IS NULL` into it. `projectFlows` then converts its own `WHERE` to `AND`; **`flowsByProject` appends only `ORDER BY` and needs no change at all** | A subsystem sheet appears as a tab and as a register chip |
| 2 | `CurrentWorkspace`, `ProjectWorkspace` (`workspace.go:13, 32`) | `AND flows.parent_block_id IS NULL` | The application opens on a subsystem sheet: a child's `position` is 0, and its id can be lower than a later top-level sheet's |
| 3 | `Current` (`studio.go:19`) | Same | `renderFailure`'s fallback renders a subsystem sheet after an error |
| 4 | `DeleteFlow`'s survivor count (`lifecycle.go:403`) | `AND parent_block_id IS NULL` | A project can be emptied of every sheet the tab strip can draw, leaving the strip blank and `ProjectWorkspace` with nothing to open — the exact failure the "last flowsheet" refusal exists to prevent |
| 5 | **`DeleteFlow`'s two landing-sheet queries** (`lifecycle.go:413-426`) | `AND parent_block_id IS NULL` in both | **Verified failure**: with A(pos 0, id 1), B(pos 1, id 2) and B's own child C(id 3), deleting B selects `landingID = 3`. C sorts ahead of A because `ORDER BY position DESC, id DESC` prefers the higher id at position 0, and a child's id is normally higher than its parent's. C is then destroyed by the cascade, so `s.Workspace(ctx, projectID, 3)` at `lifecycle.go:452` returns `ErrNotFound` — **the user gets a failure page after a successful delete.** Even when the child survives, the app opens on a subsystem sheet |
| 6 | **`ReorderFlows`'s `belongs` query** (`lifecycle.go:480`) | `AND parent_block_id IS NULL` | **Verified failure**: this is a raw `SELECT id FROM flows WHERE project_id = ?`, not `flowSelect`, so renaming `flowSelect` leaves it untouched. Two top-level sheets plus one child give `len(belongs) = 3` while the strip sends 2 ids, and the guard at `lifecycle.go:501` refuses **every** reorder with "the new order must list each of this project's 3 flowsheets exactly once". Drag-to-reorder stops working entirely |
| 7 | `generatedFlowName` (`lifecycle.go:549`) | `AND parent_block_id IS NULL` | Nothing today — a child's name is the empty string and the candidates are "Flowsheet N", so they cannot collide. It is filtered so this enumeration is complete and so the claim does not depend on the empty-name decision holding forever |
| 8 | `hasInvalidFlowPositions` (`store.go:570`) | `WHERE flows.parent_block_id IS NULL` before the `GROUP BY` | A child sheet at position 0 makes every project's positions look non-dense, so the repair below runs on every `Open` |
| 9 | `ensureFlowPositions`'s repair (`store.go:549-560`) | **Both the inner `ROW_NUMBER()` source and the outer `UPDATE`.** See below | Subsystem sheets are renumbered into the tab strip — or, with the obvious partial fix, every legacy database fails to open |

**Row 9 is the one that can be read two ways, and one reading bricks every
legacy database.** The repair is an `UPDATE` whose value is a correlated
subquery over a `ROW_NUMBER()` window. Filtering only the inner `FROM flows` —
the literal minimal edit — leaves child rows with no matching `ordered` row, so
the subquery yields NULL against a `NOT NULL` column. Verified in a scratch
database: `NOT NULL constraint failed: flows.position`. The outer statement
needs its own predicate too:

```sql
UPDATE flows SET position = (
    SELECT ordered.position
    FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY project_id
            ORDER BY position, name COLLATE NOCASE, id
        ) - 1 AS position
        FROM flows WHERE parent_block_id IS NULL   -- inner
    ) AS ordered
    WHERE ordered.id = flows.id
)
WHERE parent_block_id IS NULL;                     -- outer, and not optional
```

Verified with that form: a hand-made order (Beta at 1, Alpha at 0) is left
alone, child rows stay at 0, and the density check returns 0 rather than 1.

Child flows are created with `position = 0` and it is never read.

`ReorderFlows` needs no new *message* once row 6 lands — a child id is simply
not in `belongs`, so it falls to the existing "must list each of this project's
N flowsheets exactly once" refusal, now counting the right N.

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

| Kind | `role` | Ports on its own sheet | What it is |
| --- | --- | --- | --- |
| `BlockSubsystem` (`"subsystem"`) | `roleDynamic` (the zero value) | One input port per child Inport, one output port per child Outport | The block that owns a sheet |
| `BlockInport` (`"inport"`) | `roleSource` | No input, one output | One input terminal of the sheet's owning block |
| `BlockOutport` (`"outport"`) | `roleSink` | One input, no output | One output terminal of the sheet's owning block |

**`role` is a field an implementer must set deliberately, because it governs
more than the port shape.** It decides `compileFlow`'s presence checks
(`simulate.go:144-149`) and `sourceValue`'s waveform dispatch
(`simulate.go:325-331`), which returns 0 in silence for a `roleSource` kind
with no `waveform` hook. The values above are chosen for what they do on the
child sheet itself, where these blocks are wired: `roleSource` gives Inport
`arityNone`, so `Connect` refuses a wire *into* an Inport, and `roleSink` gives
Outport `HasOutput() == false`, so `Connect` refuses a wire *out of* an
Outport. Both are exactly right.

Neither ever reaches the compiler — `Run` refuses a subsystem sheet and
`expandFlow` erases both kinds before `compileFlow` sees a block list — so
Inport's absent `waveform` is unreachable rather than silently zero. That is a
load-bearing coincidence, so it gets a guard rather than a comment: a test over
`blockDefinitions` asserting **every `roleSource` kind either sets `waveform`
or sets `insideSubsystem`**, in the shape of the variadic-ports guard that
already exists.

A subsystem block is `roleDynamic`, so `HasOutput()` is true even for one with
no Outports yet. `Connect` then refuses the wire one check later, on
`hasOutputPort`, with "Reactor has no output port 0" — the right message, one
step further down than a reader might expect.

Inport and Outport are legal **only on a subsystem's own sheet**. A top-level
sheet has no parent block to give terminals to, so adding one there is
refused: **"Inport blocks belong inside a subsystem"**. The rule is one field
on the definition, read by `AddBlock` and by the palette, in the same way
`HasInput()` is read by both the wiring rules and the canvas:

```go
// insideSubsystem is true for the two kinds that only mean something on a
// subsystem's own sheet: an Inport and an Outport are the owning block's
// terminals, and a top-level sheet has no owning block.
insideSubsystem bool
```

**Reading it from the palette is an exported-API change, not a template
tweak.** The palette's only source is `studio.BlockLibrary()`
(`view.go:233`), which takes no arguments and has no flow context;
`newWorkbenchView` holds the workspace but `BlockLibrary` cannot see it. Per-
sheet omission therefore means changing an exported signature and its one call
site — `BlockLibrary(insideSubsystem bool)`, answered from the same field
`AddBlock` enforces. H3 carries that as a named deliverable. Doing it any other
way (filtering in the view against a locally spelled kind list) would put a
second authority on where a kind may be placed.

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

**`created`** runs inside `AddBlock`'s transaction for kinds whose block is not
the whole of what exists. Subsystem is the only one: it inserts the child flow.
Nil for every other kind. This keeps the catalog the single authority for what
a kind *is*, rather than adding an `if kind == BlockSubsystem` to `AddBlock`.

The obvious signature — `func(ctx, tx, block Block) error` — does not fit, and
the mismatch is worth spelling out because it costs `AddBlock` one column.
`AddBlock` never assembles a `Block` value: it inserts columns and keeps the
`LastInsertId`. And its existence check reads `SELECT 1 FROM flows WHERE id = ?`
(`studio.go:43`), so it does not have the `project_id` the child row needs. So
the hook takes what the insert actually produces, and the existence check
selects one more column into the same `ErrNotFound` branch:

```go
// created runs inside AddBlock's transaction for a kind whose block owns rows
// outside the blocks table. It receives the ids the insert has just produced
// rather than a Block value, because AddBlock never assembles one.
created func(ctx context.Context, tx *sql.Tx, placed placedBlock) error

type placedBlock struct {
    BlockID   int64
    FlowID    int64
    ProjectID int64  // from AddBlock's existence check, widened to
    Name      string // SELECT project_id FROM flows WHERE id = ?
}
```

Subsystem's hook inserts `project_id` from `placed.ProjectID`,
`parent_block_id = placed.BlockID`, `name = ''`, `position = 0`.

### One rule moves, and it is hygiene rather than a prerequisite

`compileFlow`'s arity walk refuses a variadic block with no inputs
(`simulate.go:197-200`):

```go
case arityVariadic:
    if len(inputs) == 0 {
        return compiledFlow{}, invalid("%s needs at least one input", block.Name)
    }
```

That is Sum's rule, not variadic's: "at least one" is a statement about signs,
and any future variadic kind inherits it today without being asked. It moves
into Sum's own `checkInputs` hook, which already exists and already carries
Sum's other cross-field rule. The message text is unchanged and no test asserts
it, so the relocation is pure.

**It is not, however, needed for subsystems, and an earlier draft of this
document claimed it was.** A subsystem with no Inports is legitimate — a
self-contained sheet with its own Step source — but that case never meets this
check, because `expandFlow`'s splice step 3 drops every subsystem block before
`compileFlow` sees a block list. Nothing in *Compilation* depends on the
relocation. H2 carries it as optional layering hygiene that may be dropped
without affecting any other task, and it is flagged here so an implementer
reconciling the two sections does not stall looking for the case.

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

### What a subsystem sheet's chrome shows

`Run` refuses a subsystem sheet, so the workbench must not offer to run one.
Two things in the current shell would otherwise contradict that, and both are
part of this navigation question rather than separate polish:

- **The run form is unconditional** (`workbench.html:203`), so a subsystem
  sheet would open with a live "Run model" button whose only possible answer is
  a refusal.
- **`Flow.NeedsRun` is permanently true** on a subsystem sheet.
  `snapshot` sets it from `snapshot.LastRun == nil` (`store.go:854`), and no
  `simulation_runs` row can ever exist for a sheet that cannot be run, so the
  dock's staleness state would be stuck amber forever and would mean nothing.

`workbenchView` therefore gains one flag, `CanRun`, true exactly when the open
sheet is top-level — `len(Trail) == 1`. It gates the run form, the staleness
banner, and the `Cmd`/`Ctrl` + `Enter` binding in `shortcuts.js`, which must be
inert here or the keyboard would reach a refusal the button no longer offers.
In their place the dock shows one line naming the sheet that runs this one,
linking to `Trail[0]`: *Simulated from **Reactor temperature loop***.

`Flow.NeedsRun` keeps its meaning and its query; it is simply not read on a
subsystem sheet, and nothing else reads it there — the tab strip never shows
one.

Carrying a domain refusal into the view as a flag is the established pattern
here, not a duplicated rule: `registerRowView.CanDelete` does exactly this for
the last-project refusal, and `view.go:39-41` gives the reason — "the control
is absent rather than present and doomed."

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

### Namespacing, and why the existing names suffice — but only since `663759f`

**This section is stated against the naming scheme at HEAD, and it would have
been false against the one this document was first drafted on.** That is worth
recording, because the difference is the whole argument.

Before `663759f` (`VJKI5N`, compile wiring by port) a variadic block's inbound
signal was `block_%d_input_from_%d` — built from **two different blocks' ids**,
the target and the source. The splice rewrites the source id of every wire
crossing a subsystem boundary, so it could collapse two names into one: a
subsystem `S` whose two Outports are both driven by one internal gain `G`,
wired `S.0 → Sum.0` and `S.1 → Sum.1`, splices to two wires that both spell
`block_Sum_input_from_G`. Two `controlsys.Connection` entries with an identical
`To`, and one signal vanishing into the other's place — the same defect
`663759f`'s own message describes for a fan-out into two Sum ports, where it
"came out halved".

At HEAD (`simulate.go:333-347`) the names are:

```go
sourceSignalName(id)        = fmt.Sprintf("block_%d_source", id)
inputSignalName(id, port)   = fmt.Sprintf("block_%d_input_%d", id, port)
outputSignalName(id, port)  = fmt.Sprintf("block_%d_output_%d", id, port)
```

Every name is now built from **one block's id and one of that block's own
ports**. That is what makes the theorem true, and it needs two parts rather
than the one an earlier draft gave.

**Part 1 — every name is distinct.** A system's names all carry its own block
id as a prefix, so names from different blocks cannot collide; within one
block, the ports are distinct integers because `wiredInputPorts`
(`simulate.go:266-284`) refuses a repeat. So it reduces to distinct block ids
in the flattened list, which holds because (a) `blocks.id` is
`INTEGER PRIMARY KEY` on one table, so ids are unique across every flowsheet,
and (b) the containment relation is a tree with `UNIQUE(parent_block_id)`, so
flattening visits each row exactly once and no row is ever instantiated twice.

**Part 2 — no two connections share a `To`.** This is the part the splice could
have broken and does not, for a reason that should be stated as an invariant of
the splice rather than checked afterwards: **both splice steps rewrite only the
*source* end of a wire, never the target end.** Step 1 replaces `I.0 → Y.q`
with `X.p → Y.q`; step 2 replaces `S.o → Z.r` with `W.p → Z.r`. The
`(target, targetPort)` pair is carried over untouched in both. So every input
port in the flattened graph carries exactly the number of wires it carried on
its own sheet — one, because each sheet's `Connect` enforces one wire per input
port.

The reviewer's collision case is the test to write, because at HEAD it is not a
collision but a fan-out: `S.0 → Sum.0` and `S.1 → Sum.1` with one internal `G`
driving both Outports splice to `G.0 → Sum.0` and `G.0 → Sum.1` — the same
`From`, two different `To`s, which is what `ConnectByName` handles and what
`663759f` exists to make work. `TestFlatteningFansOneSubsystemOutputIntoTwoPortsOfOneSum`
asserts the Sum receives the signal **twice** rather than once.

**Depending on any of this silently would be fragile, so the design makes the
dependence explicit.** `expandFlow` states both parts in its comment, and
`TestExpandedGraphHasDistinctSignalNames`, over a three-level fixture, asserts
part 1 rather than trusting it.

Part 1(b) is what fails first. **The day a subsystem sheet can be instantiated
more than once** — a library link, a model reference — the block id stops being
the instance identity, and the fix is named now so nobody has to rediscover it:
flattening already carries each block's *path*, the list of subsystem block ids
from the root, for the naming below; a `signalPrefix(path)` prepended in the
three name functions is the whole change. It is not built now because nothing
can produce a second instance.

**H6 is therefore hard-blocked on `VJKI5N`, not merely sequenced after it.**
That task has landed, so the block is already satisfied; the dependency is
recorded because a splice written against the old two-id naming would have been
silently wrong rather than obviously broken.

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
| **H1** Containment column and the nine top-level queries | — | `flows.parent_block_id`, the unique index, `ensureSubsystemFlows` ordered after `ensureProjects` and **before** `ensureFlowPositions`; then **all nine rows of the query table**, not just the `flowSelect` rename — rows 5 and 6 (`DeleteFlow`'s landing queries, `ReorderFlows`'s `belongs`) each break a working gesture on their own, and row 9 needs the predicate on *both* the inner `ROW_NUMBER()` source and the outer `UPDATE` or every legacy database fails to open. No new block kinds — a database with no subsystems must behave identically. Tests: the existing pre-projects fixture opens and re-opens unchanged; with a child sheet present, a reorder is accepted and deleting the sheet that owns it lands on the correct neighbour |
| **H2** Parameter-derived output ports | — (the landed port work) | `outputPorts` hook, `outputPortCount(parameters)`, `checkWiredOutputPorts`, and the output half of the variadic-kinds guard test. Every existing kind's answers unchanged. Runs in parallel with H1. **Optional, droppable:** relocating "needs at least one input" into Sum's `checkInputs` is layering hygiene — no subsystem ever reaches that check, because `expandFlow` drops subsystem blocks first |
| **H3** The three kinds and the `created` hook | H1, H2 | `BlockSubsystem`, `BlockInport`, `BlockOutport` with their `role` values and the guard test that every `roleSource` kind sets `waveform` or `insideSubsystem`; `Parameters.Inports`/`Outports` and their clone; `maxSubsystemDepth` and its refusal; `created` plus the `placedBlock` value and the widening of `AddBlock`'s existence check to `SELECT project_id`; `Block.ChildFlowID` from `snapshot`'s left join. **Named API change:** `studio.BlockLibrary()` gains a parameter and its one call site at `view.go:233` changes, because the palette cannot otherwise omit Inport and Outport on a top-level sheet |
| **H4** `syncSubsystemPorts` | H3 | The one authority for the parent's port surface: the id-ordered read, the unique-name refusal, the add/rename/remove diff, the orphan refusal, the renumbering of surviving wires, and `touchModel` climbing to every ancestor. Tests for each row of the wire table |
| **H5** Cascade and deep duplication | H3 | `copyFlowContents` recursive, used by `DuplicateFlow` and `DuplicateBlocks`; the three lifecycle refusals on child sheets; a cascade test at `maxSubsystemDepth` proving one `DELETE` removes the whole subtree |
| **H6** `expandFlow` | H4, H5, and **`VJKI5N` (hard — landed as `663759f`)** | The flattening pass, its three queries, the splice, the two boundary refusals, `maxExpandedBlocks`, qualified names, and `Run` refusing a child sheet. Tests: a subsystem-free sheet expands to itself and every existing numeric assertion holds; a two-level sheet matches the same model drawn flat to 1e-12; distinct signal names over a three-level fixture; and `TestFlatteningFansOneSubsystemOutputIntoTwoPortsOfOneSum` — one internal gain driving two Outports wired into two ports of one Sum, asserting the signal arrives **twice**. That last one is the case a splice written against the pre-`663759f` naming would have halved |
| **H7** Navigation | H1, H3 | `Workspace.Trail` and the recursive trail query; the breadcrumb partial; `Active` by `Trail[0]`; the descent and ascent gestures in `js/contextmenu.js`, `js/input.js` and `js/shortcuts.js`; `workbenchView.CanRun` gating the run form, the staleness banner and the `Cmd`/`Ctrl` + `Enter` binding, with the "Simulated from …" line in their place; no new route |
| **H8** Subsystem ports on the canvas | H4, and `KE3PPL` | N labelled input pips and M labelled output pips on a subsystem card, drawn from the same derivation the wiring rules read; the inspector's connection list naming ports by their Inport names |
| **H9** Verification and documentation | H6, H7, H8 | A CDP pass in the style of `docs/workbench-ergonomics.md`'s record — build a subsystem, wire it, descend and ascend, Back across levels, delete a wired Inport and read the refusal, simulate and compare against the flat equivalent, restart and reopen. Replace the "not yet supported" paragraph in `README.md`; add the descent gestures to `docs/workbench-ergonomics.md` |

`H1` and `H2` are independent of each other and of everything already in
flight. **`H6` is hard-blocked on `VJKI5N`** — port-qualified signal names are
what make the namespacing theorem true, and a splice written against the old
two-id naming would have been silently wrong rather than obviously broken. That
task landed as `663759f`, so the block is satisfied; it is recorded because the
reason is not recoverable from the code once both are in.

**H1 is the largest and least glamorous task in this list, and it is the one
most likely to be under-scoped.** It is nine queries in four files, three of
which break a working gesture and one of which stops legacy databases opening.
Anything that treats it as "add a column and rename `flowSelect`" will ship all
four defects.

## What dependent tasks must know

- **The edge lives on the child.** `flows.parent_block_id`, never
  `blocks.child_flow_id`. Anything that reads "which sheet does this block
  own" reads it through that column.
- **`parent_block_id IS NULL` is the definition of a top-level sheet**, and
  `topLevelFlowSelect` states it for the two *lists* — not for the eight other
  places that read `flows`. Adding a new query over `flows` means deciding
  which of the two it is; the nine-row table is the enumeration to check
  against.
- **`flowSelect` is not the only path to `flows`.** `ReorderFlows`,
  `DeleteFlow`'s landing queries and `generatedFlowName` each hold a raw
  `SELECT` of their own. An earlier draft of this document assumed otherwise
  and was wrong about `ReorderFlows` in a way that would have disabled
  drag-to-reorder entirely.
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
- **The splice rewrites only the source end of a wire.** That is what keeps one
  wire per input port after flattening, which is half the namespacing theorem.
  A future splice that touches a target port owns a new argument for why no two
  connections share a `To`.
- **A signal name is built from one block's id and one of its own ports.**
  Before `663759f` a variadic input name was built from two blocks' ids, and
  the splice could collapse two such names into one. Anything that reintroduces
  a name spanning two blocks reintroduces that collision.
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
