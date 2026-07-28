# Structured parameter values

Process Lab parses vectors, matrices, and channel names into validated values
at the catalog boundary. Compiler code and controlsys realizations receive
already-validated shapes; handlers and templates do not parse these values
again.

## Syntax

- A vector is a comma-, semicolon-, or whitespace-separated row. Values keep
  their entered order. Polynomial vectors use descending powers.
- A matrix separates columns with commas or whitespace and rows with newlines
  or semicolons. Every row must have the same number of columns.
- A channel-name list separates names with commas, semicolons, or newlines.
  Names are trimmed, nonempty, case-sensitive, and unique.

Editors render the validated row and column count beside a field. Formatting
uses Go's shortest round-trippable `float64` representation, so applying an
unchanged form preserves every value exactly.

## Invariants

`VectorValue`, `MatrixValue`, and `ChannelNames` can only be created through
validated constructors, parsers, or JSON decoding. They reject empty values,
non-finite numbers, invalid dimensions, ragged rows, empty names, and duplicate
names. Accessors return copies so callers cannot invalidate a value after
construction.

Matrix JSON stores explicit `rows`, `columns`, and row-major `values`. Channel
names store a JSON array. Existing numerator and denominator arrays retain
their original JSON representation and are decoded unchanged; their catalog
field now delegates to the shared vector parser.

Unresolved questions: none
