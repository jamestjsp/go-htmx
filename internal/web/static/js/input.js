// =====================================================================
// Every binding the canvas layer answers, in one file.
//
// The bindings live together rather than in the modules they call
// because what they share is not state but precedence. A pointerdown on
// a block may be a pan, a wire, a drag or a marquee depending only on
// which claim is honoured first, and that order is a single sequence of
// early returns here — split across four listeners it would become an
// accident of registration order instead of a decision.
//
// The keyboard is here for the same reason: Escape means close the
// shortcut sheet, or clear the selection, and the two are told apart by
// one early return.
// =====================================================================
import { geometry, redrawEdges } from './geometry.js'
import {
  beginPan, currentZoom, endPan, fitView, holdSpace, movePan,
  panBy, releaseSpace, resetZoom, trackCursorReadout, zoomAround, zoomByStep
} from './viewport.js'
import {
  beginMarquee, deleteSelection, duplicateSelection, endMarquee, fitSelection,
  moveMarquee, selectAll, selectBlock, selectionSize, setSelection
} from './selection.js'
import { endDrag, moveDrag, nudgeSelection, startDrag } from './dragging.js'
import {
  beginConnection, cancelConnection, drawDraft, endWiring, finishConnection, moveWiring
} from './wiring.js'
import {
  closeShortcutSheet, isShortcutBackdrop, isShortcutSheetOpen, openShortcutSheet
} from './shortcuts.js'

function typingInAField(event) {
  const node = event.target
  return node instanceof HTMLElement && node.closest('input, textarea, select, [contenteditable="true"]')
}

// Something nearer the keyboard than a global shortcut has the key: a
// text field the user is typing into, or an open context menu.
//
// The menu half fixes a defect older than menu.js. The menu navigates
// itself with the arrow keys, and the nudge binding below also fires on
// them; the nudge saves the new positions, the save swaps the workbench,
// and the swap closes the menu. Arrow navigation therefore only ever
// worked on a menu raised over empty sheet, which clears the selection
// first and so leaves the nudge with nothing to move.
//
// A guard here rather than stopPropagation in menu.js. Every listener
// involved is on document, so stopping propagation would have to be
// stopImmediatePropagation, which only reaches listeners registered
// after menu.js's — it would silence these bindings by an accident of
// script order, and silence them wholesale rather than saying which ones
// and why. The guard is also read the same way for every region that
// registers a menu, not just the canvas.
function keyboardIsClaimed(event) {
  return typingInAField(event) || window.ProcessLab.menu.ownsKey(event)
}

// The nudge answers a bare arrow and a Shift + arrow, and nothing else.
// Ctrl/Cmd + Shift + arrow is the tab strip's reorder chord, and both
// bindings sit on document: without this the strip and the selection
// would both move on one keypress, which is the sort of collision the
// keyboardIsClaimed guard cannot see, because neither party is a text
// field or a menu.
function plainArrow(event) {
  return !event.ctrlKey && !event.metaKey
}

document.addEventListener('wheel', (event) => {
  if (!event.target.closest('#flow-canvas')) return
  event.preventDefault()
  if (event.ctrlKey || event.metaKey) {
    // Trackpad pinch arrives here as ctrl+wheel; exp keeps the steps
    // proportional so zooming feels the same at 25% and 400%.
    zoomAround(currentZoom() * Math.exp(-event.deltaY * 0.01), event.clientX, event.clientY)
    return
  }
  panBy(-event.deltaX, -event.deltaY)
}, { passive: false })

document.addEventListener('click', (event) => {
  if (event.target.closest('[data-zoom-in]')) zoomByStep(1.2)
  else if (event.target.closest('[data-zoom-out]')) zoomByStep(1 / 1.2)
  else if (event.target.closest('[data-zoom-reset]')) resetZoom()
})

document.addEventListener('keydown', (event) => {
  if (event.key === ' ' && !keyboardIsClaimed(event) && holdSpace()) {
    if (event.target.closest('#flow-canvas')) event.preventDefault()
    return
  }
  if (keyboardIsClaimed(event)) return
  if ((event.metaKey || event.ctrlKey) && (event.key === '=' || event.key === '+')) {
    event.preventDefault()
    zoomByStep(1.2)
  } else if ((event.metaKey || event.ctrlKey) && event.key === '-') {
    event.preventDefault()
    zoomByStep(1 / 1.2)
  } else if ((event.metaKey || event.ctrlKey) && event.key === '0') {
    event.preventDefault()
    resetZoom()
  } else if (event.shiftKey && (event.key === '!' || event.code === 'Digit1')) {
    event.preventDefault()
    fitView()
  }
})

document.addEventListener('keyup', (event) => {
  if (event.key === ' ') releaseSpace()
})

document.addEventListener('click', (event) => {
  if (event.target.closest('[data-shortcuts-close]')) closeShortcutSheet()
  else if (isShortcutBackdrop(event.target)) closeShortcutSheet()
  else if (event.target.closest('[data-shortcuts-open]')) openShortcutSheet()
})

// Every binding below is guarded by keyboardIsClaimed first. Without it,
// typing a block name would delete the selection on Backspace and
// duplicate it on "d", which is the most destructive thing this file
// could get wrong.
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape' && isShortcutSheetOpen()) {
    event.preventDefault()
    closeShortcutSheet()
    return
  }
  // The guard comes before every binding, deliberately.
  if (keyboardIsClaimed(event)) return
  // The dock resizer owns the arrow keys while it has focus.
  if (event.target.closest('[data-dock-resizer], [role="separator"]')) return
  const { grid } = geometry()
  const step = event.shiftKey ? grid * 5 : grid

  if (event.key === 'Delete' || event.key === 'Backspace') {
    if (!selectionSize()) return
    event.preventDefault()
    deleteSelection()
  } else if ((event.metaKey || event.ctrlKey) && (event.key === 'a' || event.key === 'A')) {
    event.preventDefault()
    selectAll()
  } else if ((event.metaKey || event.ctrlKey) && (event.key === 'd' || event.key === 'D')) {
    event.preventDefault()
    duplicateSelection()
  } else if (event.key === '?') {
    event.preventDefault()
    openShortcutSheet()
  } else if (event.key === 'Escape') {
    if (selectionSize()) setSelection([])
  } else if (!plainArrow(event)) {
    /* a modified arrow belongs to someone else; see plainArrow */
  } else if (event.key === 'ArrowLeft') {
    event.preventDefault()
    nudgeSelection(-step, 0)
  } else if (event.key === 'ArrowRight') {
    event.preventDefault()
    nudgeSelection(step, 0)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    nudgeSelection(0, -step)
  } else if (event.key === 'ArrowDown') {
    event.preventDefault()
    nudgeSelection(0, step)
  }
})

window.addEventListener('blur', releaseSpace)

document.addEventListener('pointerdown', (event) => {
  // Panning claims the gesture first: space-drag and middle-drag must
  // win over starting a block drag or a wire.
  if (beginPan(event)) return
  const output = event.target.closest('[data-output-port]')
  if (output) {
    beginConnection(output, event)
    return
  }
  const input = event.target.closest('[data-input-port]')
  if (input) {
    finishConnection(input)
    return
  }
  const node = event.target.closest('.block-card')
  if (node) {
    startDrag(event, node)
    return
  }
  beginMarquee(event)
})
document.addEventListener('pointermove', (event) => {
  movePan(event)
  moveMarquee(event)
  moveWiring(event)
  moveDrag(event)
  drawDraft(event)
  trackCursorReadout(event)
})
document.addEventListener('pointerup', (event) => {
  endPan(event)
  endMarquee(event)
  endWiring(event)
  endDrag(event)
})
document.addEventListener('pointercancel', (event) => {
  endPan(event)
  endMarquee(event)
  endWiring(event)
  endDrag(event)
})
document.addEventListener('click', (event) => {
  if (event.target.closest('[data-selection-fit]')) fitSelection()
  else if (event.target.closest('[data-selection-delete]')) deleteSelection()
})
document.addEventListener('click', (event) => {
  if (event.target.closest('[data-fit-view]')) fitView()
  if (event.target.closest('[data-dismiss-error]')) event.target.closest('.error-banner')?.remove()
  if (event.detail === 0 && event.target.closest('.block-body')) selectBlock(event.target.closest('.block-card'))
})
document.addEventListener('keydown', (event) => {
  // No typingInAField here: Cmd+Enter is meant to run the model from
  // inside the inspector too. The menu still gets its Escape to itself,
  // so dismissing a menu raised mid-wire does not also drop the wire.
  if (window.ProcessLab.menu.ownsKey(event)) return
  if (event.key === 'Escape') cancelConnection()
  if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
    event.preventDefault()
    document.querySelector('#run-form')?.requestSubmit()
  }
})
window.addEventListener('resize', redrawEdges)
