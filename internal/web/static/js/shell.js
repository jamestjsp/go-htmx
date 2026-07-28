// =====================================================================
// Shell state: rail collapse and simulation dock height.
//
// This is per-user view state, so it lives in localStorage and is
// expressed as data attributes on #workbench that CSS reads for all
// sizing. HTMX replaces #workbench wholesale on nearly every action,
// which throws those attributes away, so applyShellState() is the one
// place that rebuilds them and it runs on load and after every swap —
// registered as a step of the re-apply pass in main.js.
//
// The chrome bindings stay in this file rather than in input.js: they
// act on the rails and the dock, never on the sheet, so they cannot
// collide with a canvas gesture and there is nothing to order them
// against. The one place the two layers meet is the arrow keys, and the
// canvas keymap defers to the resizer explicitly.
// =====================================================================
import { workbench } from './dom.js'

const SHELL_KEYS = {
  left: 'processlab:rail-left',
  right: 'processlab:rail-right',
  dock: 'processlab:dock-height'
}
const COMPACT_QUERY = '(max-width: 1099.98px)'
const STACKED_QUERY = '(max-width: 860px)'
const DEFAULT_DOCK_HEIGHT = 240
const FALLBACK_DOCK_HEADER = 58
const MIN_CANVAS_HEIGHT = 150
const railOverride = { left: false, right: false }
let lastDockHeight = DEFAULT_DOCK_HEIGHT
let dockDrag = null

const stage = () => document.querySelector('.main-stage')
const dock = () => document.querySelector('#simulation-results')
const matches = (query) => window.matchMedia(query).matches

function readShell(key) {
  try {
    return window.localStorage.getItem(key)
  } catch (error) {
    return null
  }
}

function writeShell(key, value) {
  try {
    window.localStorage.setItem(key, value)
  } catch (error) {
    /* storage disabled; the shell still works, it just will not persist */
  }
}

function dockHeaderHeight() {
  const root = workbench()
  if (!root) return FALLBACK_DOCK_HEADER
  const parsed = Number.parseFloat(getComputedStyle(root).getPropertyValue('--dock-header'))
  return Number.isFinite(parsed) && parsed > 0 ? parsed : FALLBACK_DOCK_HEADER
}

function dockLimits() {
  const min = dockHeaderHeight()
  const box = stage()
  const room = box ? box.getBoundingClientRect().height - MIN_CANVAS_HEIGHT : window.innerHeight
  return { min, max: Math.max(min, Math.min(window.innerHeight * 0.7, room)) }
}

function storedDockHeight() {
  const parsed = Number.parseFloat(readShell(SHELL_KEYS.dock))
  return Number.isFinite(parsed) ? parsed : DEFAULT_DOCK_HEIGHT
}

function railIsCollapsed(side) {
  if (matches(STACKED_QUERY)) return false
  if (matches(COMPACT_QUERY) && !railOverride[side]) return true
  return readShell(SHELL_KEYS[side]) === 'collapsed'
}

function railChevron(side, collapsed) {
  if (side === 'left') return collapsed ? '»' : '«'
  return collapsed ? '«' : '»'
}

function describeToggle(button, collapsed, subject, chevron) {
  const action = collapsed ? 'Expand' : 'Collapse'
  // Buttons that carry a visible label keep it inside the accessible
  // name so speech control still matches what the user can read.
  const visible = button.querySelector('.shell-toggle-label')
  const name = visible ? `${action} ${visible.textContent.trim()}` : `${action} the ${subject}`
  button.setAttribute('aria-expanded', collapsed ? 'false' : 'true')
  button.setAttribute('title', name)
  if (!visible) button.setAttribute('aria-label', name)
  const glyph = button.querySelector('.rail-chevron')
  if (glyph) glyph.textContent = chevron
}

function applyRailState(side, collapsed) {
  const root = workbench()
  if (!root) return
  root.dataset[side === 'left' ? 'railLeft' : 'railRight'] = collapsed ? 'collapsed' : 'expanded'
  const subject = side === 'left' ? 'block library' : 'inspector'
  document.querySelectorAll(`[data-rail-toggle="${side}"]`).forEach((button) => {
    describeToggle(button, collapsed, subject, railChevron(side, collapsed))
  })
}

function applyDockState(height, collapsed, limits) {
  const root = workbench()
  if (!root) return
  root.dataset.dock = collapsed ? 'collapsed' : 'expanded'
  root.style.setProperty('--dock-height', `${Math.round(height)}px`)
  document.querySelectorAll('[data-dock-toggle]').forEach((button) => {
    describeToggle(button, collapsed, 'simulation dock', collapsed ? '▴' : '▾')
  })
  const handle = document.querySelector('[data-dock-resizer]')
  if (!handle) return
  const span = Math.max(1, limits.max - limits.min)
  handle.setAttribute('aria-valuenow', String(Math.round(((height - limits.min) / span) * 100)))
}

function resolveDockHeight(requested) {
  const limits = dockLimits()
  let height = Math.min(limits.max, Math.max(limits.min, requested))
  const collapsed = height <= limits.min + 4
  if (collapsed) height = limits.min
  else lastDockHeight = height
  return { height, collapsed, limits }
}

export function applyShellState() {
  const root = workbench()
  if (!root) return
  applyRailState('left', railIsCollapsed('left'))
  applyRailState('right', railIsCollapsed('right'))
  const resolved = resolveDockHeight(storedDockHeight())
  applyDockState(resolved.height, resolved.collapsed, resolved.limits)
}

function toggleRail(side) {
  const root = workbench()
  if (!root) return
  const collapsed = root.dataset[side === 'left' ? 'railLeft' : 'railRight'] === 'collapsed'
  railOverride[side] = true
  writeShell(SHELL_KEYS[side], collapsed ? 'expanded' : 'collapsed')
  applyShellState()
}

function setDockHeight(requested, persist = true) {
  const resolved = resolveDockHeight(requested)
  applyDockState(resolved.height, resolved.collapsed, resolved.limits)
  if (persist) writeShell(SHELL_KEYS.dock, String(Math.round(resolved.height)))
}

function toggleDock() {
  const root = workbench()
  if (!root) return
  if (root.dataset.dock === 'collapsed') setDockHeight(lastDockHeight || DEFAULT_DOCK_HEIGHT)
  else setDockHeight(0)
}

function currentDockHeight() {
  const root = workbench()
  if (!root) return DEFAULT_DOCK_HEIGHT
  const parsed = Number.parseFloat(getComputedStyle(root).getPropertyValue('--dock-height'))
  return Number.isFinite(parsed) ? parsed : DEFAULT_DOCK_HEIGHT
}

function startDockDrag(event, handle) {
  if (event.button !== 0) return
  const panel = dock()
  if (!panel) return
  dockDrag = { pointerId: event.pointerId, bottom: panel.getBoundingClientRect().bottom, handle }
  handle.setPointerCapture(event.pointerId)
  document.body.classList.add('is-resizing-dock')
  event.preventDefault()
}

function moveDockDrag(event) {
  if (!dockDrag || event.pointerId !== dockDrag.pointerId) return
  setDockHeight(dockDrag.bottom - event.clientY, false)
}

function endDockDrag(event) {
  if (!dockDrag || event.pointerId !== dockDrag.pointerId) return
  dockDrag.handle.releasePointerCapture(event.pointerId)
  dockDrag = null
  document.body.classList.remove('is-resizing-dock')
  writeShell(SHELL_KEYS.dock, String(Math.round(currentDockHeight())))
}

function resizeDockByKeyboard(event) {
  if (!event.target.closest('[data-dock-resizer]')) return
  const step = event.shiftKey ? 64 : 16
  if (event.key === 'ArrowUp') setDockHeight(currentDockHeight() + step)
  else if (event.key === 'ArrowDown') setDockHeight(currentDockHeight() - step)
  else if (event.key === 'Home') setDockHeight(dockLimits().max)
  else if (event.key === 'End') setDockHeight(0)
  else if (event.key === 'Enter' || event.key === ' ') toggleDock()
  else return
  event.preventDefault()
}

export function initShell() {
  document.addEventListener('click', (event) => {
    const rail = event.target.closest('[data-rail-toggle]')
    if (rail) {
      toggleRail(rail.dataset.railToggle)
      return
    }
    if (event.target.closest('[data-dock-toggle]')) toggleDock()
  })
  document.addEventListener('pointerdown', (event) => {
    const handle = event.target.closest('[data-dock-resizer]')
    if (handle) startDockDrag(event, handle)
  })
  document.addEventListener('pointermove', moveDockDrag)
  document.addEventListener('pointerup', endDockDrag)
  document.addEventListener('pointercancel', endDockDrag)
  document.addEventListener('keydown', resizeDockByKeyboard)
  // A history restore that misses htmx's cache re-fetches the page, so the
  // markup arrives with the server's default rail and dock attributes; the
  // re-apply pass covers that case and the cached one alike.
  document.addEventListener('DOMContentLoaded', applyShellState)
  window.addEventListener('resize', applyShellState)

  const compactViewport = window.matchMedia(COMPACT_QUERY)
  const onViewportChange = (event) => {
    if (!event.matches) {
      railOverride.left = false
      railOverride.right = false
    }
    applyShellState()
  }
  if (compactViewport.addEventListener) compactViewport.addEventListener('change', onViewportChange)
  else if (compactViewport.addListener) compactViewport.addListener(onViewportChange)

  applyShellState()
}
