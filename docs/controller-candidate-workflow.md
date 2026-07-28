# Controller candidate workflow

Process Lab keeps controller design separate from model mutation.

1. Assign explicit named plant, controller, measurement, control, and analysis-point roles.
2. Generate a PID/PID2, bounded tuning, state-feedback, estimator, or LQG candidate.
3. Review the current and candidate closed loops on the same time and frequency grids.
4. Apply the candidate as one complete, validated controller-block replacement.
5. Undo the application while its model revision and named-role assignment remain current.

Candidates record the source model revision, a normalized named-role snapshot and
fingerprint, the algorithm, structured goals, warnings, and numerical evidence.
Candidate generation and review do not alter persisted blocks.

Application is refused if the flowsheet model, selected roles, target block kind,
or wired port dimensions changed. The UI keeps authoritative candidate edits on
the server behind an opaque identifier; matrices and gains are never accepted
back from hidden browser fields.

Undo is deliberately narrow and truthful. A successful apply returns the exact
previous controller parameters as a one-use undo candidate. Undo is refused
after any intervening model or role change rather than overwriting later work.
The general activity log remains audit history, not model version history.

PID2 time comparisons use the complete reference-to-measurement loop so setpoint
and derivative weights are visible. State-feedback and observer-regulator
candidates normalize their authored signed control law before robustness
analysis, matching the external-negative-feedback convention used by
`controlsys`.
