// =====================================================================
// The workbench entry point: it names the modules and the order the
// canvas is rebuilt in, and does nothing else.
//
// Loaded as type="module", which is deferred by definition. Deferred
// classic scripts and module scripts share one execution list ordered by
// document position, so menu.js still runs before this file — its
// ProcessLab.menu namespace is there for contextmenu.js to register
// against — and tabs.js still runs after it, layering its second menu
// region and its Ctrl/Cmd + Shift + arrow chord over these bindings.
//
// The flowsheet tab strip is not here. It began in app.js because the
// agent that wrote it did not own page.html and had nowhere else to put
// it; it now lives in tabs.js, next to the rename, drag and reorder
// gestures that share its geometry.
// =====================================================================
import { redrawEdges } from './geometry.js'
import { initViewport, reapplyViewport } from './viewport.js'
import { applySelection } from './selection.js'
import { applyShellState, initShell } from './shell.js'
import { cancelConnection, hasConnectionSource } from './wiring.js'
import { onBeforeSwap, onReapply } from './reapply.js'
import './contextmenu.js'
import './input.js'

// A wire in flight refers to a block element the swap is about to
// destroy, so it is dropped before the markup goes rather than after.
onBeforeSwap(() => {
  if (hasConnectionSource()) cancelConnection('Workbench updated')
})
document.addEventListener('htmx:historyRestore', () => {
  if (hasConnectionSource()) cancelConnection('Workbench restored')
})

// The order is the order the canvas is rebuilt in, and it matters: the
// viewport first, because it decides the flowsheet on screen and every
// measurement downstream reads through its transform; then the selection,
// which drops ids the swap removed; then the wires between whatever is
// left; then the shell around all of it.
onReapply(reapplyViewport)
onReapply(applySelection)
onReapply(redrawEdges)
onReapply(applyShellState)

// The same order on first load, and the shell last for the same reason
// it is last above: initViewport() fits a sheet that has never been
// opened to the canvas, and restoring a stored dock height changes the
// size of the canvas it would measure.
initViewport()
applySelection()
redrawEdges()
initShell()
