// =====================================================================
// The keyboard reference sheet.
//
// The table is the documentation for the bindings in input.js; the two
// are kept side by side deliberately, because a shortcut that works and
// is not listed here is a shortcut nobody finds.
// =====================================================================
import { canvas } from './dom.js'

const SHORTCUTS = [
  ['Canvas', [
    ['Drag empty canvas', 'Select blocks with a marquee'],
    ['Shift + drag', 'Add to the selection'],
    ['Space + drag, or middle-drag', 'Pan the sheet'],
    ['Wheel', 'Pan'],
    ['Cmd/Ctrl + wheel, or pinch', 'Zoom about the pointer'],
    ['Cmd/Ctrl + = or −', 'Zoom in or out'],
    ['Cmd/Ctrl + 0', 'Reset to 100%'],
    ['Shift + 1', 'Fit the flowsheet to the window']
  ]],
  ['Blocks', [
    ['Drag a block', 'Move it; it snaps to the grid'],
    ['Alt + drag', 'Suspend alignment magnetism'],
    ['Shift or Cmd + click', 'Add or remove one block'],
    ['Cmd/Ctrl + A', 'Select every block'],
    ['Arrow keys', 'Nudge one grid step'],
    ['Shift + arrow keys', 'Nudge five grid steps'],
    ['Cmd/Ctrl + D', 'Duplicate; wires between blocks are not copied'],
    ['Delete or Backspace', 'Delete the selection']
  ]],
  ['Signals', [
    ['Click an output, then an input', 'Wire a signal'],
    ['Esc', 'Cancel wiring, or clear the selection']
  ]],
  ['Model', [
    ['Cmd/Ctrl + Enter', 'Run the simulation'],
    ['?', 'Show this sheet']
  ]],
  ['Sheets', [
    ['Double-click a tab', 'Rename it in place'],
    ['Enter, or Esc', 'Commit the name, or put it back'],
    ['Right-click a tab', 'Rename, duplicate or delete the sheet'],
    ['Drag a tab', 'Move it along the strip'],
    ['Ctrl/Cmd + Shift + ← or →', 'Move the open sheet one place'],
    ['+', 'Add a sheet and name it']
  ]]
]

let shortcutSheet = null
let shortcutOpener = null

export const isShortcutSheetOpen = () => shortcutSheet !== null
export const isShortcutBackdrop = (target) => shortcutSheet !== null && target === shortcutSheet

// The canvas is a div, so focus() only lands once it is a focus target;
// without this the fallback below silently drops focus on <body>.
// tabindex="-1" for the same reason menu.js uses it: the canvas is
// clicked or returned to, never tabbed to.
function focusCanvas() {
  const root = canvas()
  if (!root) return
  if (!root.hasAttribute('tabindex')) root.tabIndex = -1
  root.focus()
}

export function closeShortcutSheet() {
  if (!shortcutSheet) return
  shortcutSheet.remove()
  shortcutSheet = null
  // "?" is usually pressed with nothing focused, so the opener is <body>,
  // which document.contains() happily accepts — focusing it is the same
  // as focusing nothing. Only a real element counts as somewhere to
  // return to; otherwise the sheet hands the keyboard to the canvas.
  const hasOpener = shortcutOpener && shortcutOpener !== document.body && document.contains(shortcutOpener)
  if (hasOpener) shortcutOpener.focus()
  else focusCanvas()
  shortcutOpener = null
}

export function openShortcutSheet() {
  if (shortcutSheet) {
    closeShortcutSheet()
    return
  }
  shortcutOpener = document.activeElement
  shortcutSheet = document.createElement('div')
  shortcutSheet.setAttribute('role', 'dialog')
  shortcutSheet.setAttribute('aria-modal', 'true')
  shortcutSheet.setAttribute('aria-label', 'Keyboard shortcuts')
  shortcutSheet.tabIndex = -1
  shortcutSheet.style.cssText = [
    'position:fixed', 'inset:0', 'z-index:200', 'display:grid', 'place-items:center',
    'padding:24px', 'background:rgb(8 14 13 / 58%)'
  ].join(';')

  const panel = document.createElement('div')
  panel.style.cssText = [
    'max-width:760px', 'max-height:82vh', 'overflow:auto', 'padding:22px 24px',
    'border:1px solid var(--housing-line-strong,#3c4f4a)', 'border-radius:10px',
    'background:var(--housing,#16201e)', 'color:var(--ink-inverse,#e8efec)',
    'box-shadow:0 26px 60px rgb(6 12 11 / 46%)'
  ].join(';')

  const columns = SHORTCUTS.map(([group, rows]) => `
    <section style="min-width:0">
      <h3 style="margin:0 0 9px;font-size:11px;font-weight:800;letter-spacing:.14em;text-transform:uppercase;color:var(--probe,#35b39c)">${group}</h3>
      <dl style="margin:0;display:grid;gap:7px">
        ${rows.map(([keys, meaning]) => `
          <div style="display:grid;grid-template-columns:minmax(0,1fr);gap:2px">
            <dt style="font-family:var(--font-mono,ui-monospace);font-size:11px;color:var(--ink-inverse,#e8efec)">${keys}</dt>
            <dd style="margin:0;font-size:12px;color:var(--ink-inverse-muted,#93a8a2);line-height:1.4">${meaning}</dd>
          </div>`).join('')}
      </dl>
    </section>`).join('')

  panel.innerHTML = `
    <div style="display:flex;align-items:baseline;justify-content:space-between;gap:16px;margin-bottom:18px">
      <h2 style="margin:0;font-size:19px;font-weight:650">Keyboard shortcuts</h2>
      <button type="button" data-shortcuts-close
        style="padding:6px 11px;border:1px solid var(--housing-line-strong,#3c4f4a);border-radius:5px;background:var(--housing-raised,#1f2c29);color:inherit;cursor:pointer;font-size:11px">Close</button>
    </div>
    <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(230px,1fr));gap:22px">${columns}</div>`

  shortcutSheet.appendChild(panel)
  document.body.appendChild(shortcutSheet)
  shortcutSheet.focus()
}
