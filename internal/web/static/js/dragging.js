// =====================================================================
// Moving blocks: the pointer drag, the alignment guides it raises, the
// keyboard nudge, and the one request that persists whatever moved.
// =====================================================================
import { setStatus, sheet, workbench } from './dom.js'
import {
  flushRedrawEdges,
  geometry,
  redrawEdges,
  scheduleAuthoritativeRedraw,
  scheduleRedrawEdges
} from './geometry.js'
import { currentZoom } from './viewport.js'
import { isSelected, selectBlock, selectedNodes, setSelection, toggleSelection } from './selection.js'

let dragging = null

export function startDrag(event, node) {
  if (event.button !== 0 || event.target.closest('.port, input')) return
  const id = node.dataset.blockId
  if (event.shiftKey || event.metaKey || event.ctrlKey) {
    toggleSelection(id)
    return
  }
  // Dragging an unselected block selects it alone; dragging one that is
  // already part of a selection moves the whole selection.
  if (!isSelected(id)) setSelection([id])
  dragging = {
    node,
    group: selectedNodes().map((member) => ({
      node: member, left: member.offsetLeft, top: member.offsetTop
    })),
    pointerId: event.pointerId,
    startX: event.clientX,
    startY: event.clientY,
    left: node.offsetLeft,
    top: node.offsetTop,
    moved: false
  }
  node.setPointerCapture(event.pointerId)
  node.classList.add('dragging')
  event.preventDefault()
}

// Snapping. The grid is the default resting place; an edge or centre
// shared with a neighbour wins over it, because visual alignment is what
// the user is actually after.
//
// Alt suspends alignment only, never the grid. The server snaps every
// stored position, so a client that placed a block off-grid would have
// it jump on the next reload; keeping the grid unconditional is what
// makes the rendered and the persisted position the same thing.
const GUIDE_THRESHOLD = 5
const GUIDE_RANGE = 2000
let guideLayer = null

function clearGuides() {
  if (guideLayer) guideLayer.remove()
  guideLayer = null
}

function showGuides(x, y) {
  const layer = sheet()
  if (!layer) return
  if (x === null && y === null) {
    clearGuides()
    return
  }
  if (!guideLayer || guideLayer.parentNode !== layer) {
    clearGuides()
    guideLayer = document.createElement('div')
    guideLayer.dataset.alignGuides = ''
    guideLayer.style.cssText = 'position:absolute;inset:0;pointer-events:none;z-index:20'
    layer.appendChild(guideLayer)
  }
  const { width, height } = geometry()
  // Counter-scale so the guide stays a hairline at any zoom.
  const hairline = 1 / currentZoom()
  guideLayer.innerHTML = ''
  const add = (css) => {
    const line = document.createElement('i')
    line.style.cssText = `position:absolute;background:#35b39c;${css}`
    guideLayer.appendChild(line)
  }
  if (x !== null) add(`left:${x}px;top:0;width:${hairline}px;height:${height}px`)
  if (y !== null) add(`top:${y}px;left:0;height:${hairline}px;width:${width}px`)
}

function alignDrag(node, left, top, useAlignment) {
  const { blockWidth, blockHeight, grid } = geometry()
  const layer = sheet()
  let bestX = Infinity
  let bestY = Infinity
  let snapLeft = null
  let snapTop = null
  let guideX = null
  let guideY = null

  if (layer && useAlignment) {
    layer.querySelectorAll('.block-card').forEach((other) => {
      if (other === node) return
      const otherLeft = other.offsetLeft
      const otherTop = other.offsetTop
      // Only neighbours can plausibly align; skip the rest of the sheet.
      if (Math.abs(otherLeft - left) > GUIDE_RANGE || Math.abs(otherTop - top) > GUIDE_RANGE) return
      const edgesX = [otherLeft, otherLeft + blockWidth / 2, otherLeft + blockWidth]
      const edgesY = [otherTop, otherTop + blockHeight / 2, otherTop + blockHeight]
      const mineX = [left, left + blockWidth / 2, left + blockWidth]
      const mineY = [top, top + blockHeight / 2, top + blockHeight]
      // A block is 172x84 while the grid is 20, so centre- and far-edge
      // alignments land between intersections. The server would snap
      // those back and the block would jump on reload, so only accept an
      // alignment that is itself on the grid.
      const onGrid = (value) => Math.abs(value - Math.round(value / grid) * grid) < 0.001
      mineX.forEach((mine) => edgesX.forEach((edge) => {
        const delta = Math.abs(mine - edge)
        const candidate = left + (edge - mine)
        if (delta < bestX && onGrid(candidate)) {
          bestX = delta
          snapLeft = candidate
          guideX = edge
        }
      }))
      mineY.forEach((mine) => edgesY.forEach((edge) => {
        const delta = Math.abs(mine - edge)
        const candidate = top + (edge - mine)
        if (delta < bestY && onGrid(candidate)) {
          bestY = delta
          snapTop = candidate
          guideY = edge
        }
      }))
    })
  }

  const alignedX = bestX <= GUIDE_THRESHOLD
  const alignedY = bestY <= GUIDE_THRESHOLD
  return {
    left: alignedX ? snapLeft : Math.round(left / grid) * grid,
    top: alignedY ? snapTop : Math.round(top / grid) * grid,
    guideX: alignedX ? guideX : null,
    guideY: alignedY ? guideY : null
  }
}

export function moveDrag(event) {
  if (!dragging || event.pointerId !== dragging.pointerId) return
  // Pointer travel is in screen pixels; block positions are in sheet
  // units, so every delta divides by the zoom or the block outruns the
  // cursor as soon as you leave 100%.
  const zoom = currentZoom()
  const dx = (event.clientX - dragging.startX) / zoom
  const dy = (event.clientY - dragging.startY) / zoom
  if (Math.abs(dx) + Math.abs(dy) > 4 / zoom) dragging.moved = true
  const { width, height, blockWidth, blockHeight } = geometry()
  let left = dragging.left + dx
  let top = dragging.top + dy

  // Snap in sheet space, never screen space, or the step would change
  // with zoom.
  const aligned = alignDrag(dragging.node, left, top, !event.altKey)
  left = aligned.left
  top = aligned.top
  showGuides(aligned.guideX, aligned.guideY)

  left = Math.max(0, Math.min(width - blockWidth, left))
  top = Math.max(0, Math.min(height - blockHeight, top))

  // Everything else in the selection follows by the same delta, so the
  // arrangement's relative spacing is preserved exactly.
  const deltaX = left - dragging.left
  const deltaY = top - dragging.top
  dragging.group.forEach((member) => {
    const memberLeft = member.node === dragging.node
      ? left
      : Math.max(0, Math.min(width - blockWidth, member.left + deltaX))
    const memberTop = member.node === dragging.node
      ? top
      : Math.max(0, Math.min(height - blockHeight, member.top + deltaY))
    member.node.style.left = `${Math.round(memberLeft)}px`
    member.node.style.top = `${Math.round(memberTop)}px`
  })
  if (dragging.moved) {
    scheduleRedrawEdges(dragging.group.map((member) => member.node.dataset.blockId))
  }
}

export function endDrag(event) {
  if (!dragging || event.pointerId !== dragging.pointerId) return
  const current = dragging
  current.node.classList.remove('dragging')
  current.node.releasePointerCapture(event.pointerId)
  dragging = null
  clearGuides()
  if (!current.moved) {
    selectBlock(current.node)
    return
  }
  flushRedrawEdges()
  savePositions(current.group)
}

export function nudgeSelection(stepX, stepY) {
  const nodes = selectedNodes()
  if (!nodes.length) return
  const { width, height, blockWidth, blockHeight } = geometry()
  const group = nodes.map((node) => ({ node, left: node.offsetLeft, top: node.offsetTop }))
  group.forEach((member) => {
    member.node.style.left = `${Math.max(0, Math.min(width - blockWidth, member.left + stepX))}px`
    member.node.style.top = `${Math.max(0, Math.min(height - blockHeight, member.top + stepY))}px`
  })
  redrawEdges(nodes.map((node) => node.dataset.blockId))
  scheduleAuthoritativeRedraw()
  savePositions(group)
}

// One request for the whole arrangement: N separate PATCHes would be
// slower and could leave a half-moved selection behind.
function savePositions(group) {
  const root = workbench()
  if (!root || !group.length) return
  if (group.length === 1) {
    htmx.ajax('PATCH', `/blocks/${group[0].node.dataset.blockId}/position`, {
      swap: 'none',
      values: { x: group[0].node.offsetLeft, y: group[0].node.offsetTop }
    })
    setStatus('Block position saved')
    return
  }
  const values = { id: [], x: [], y: [] }
  group.forEach((member) => {
    values.id.push(member.node.dataset.blockId)
    values.x.push(String(member.node.offsetLeft))
    values.y.push(String(member.node.offsetTop))
  })
  htmx.ajax('PATCH', `/flows/${root.dataset.flowId}/blocks/positions`, {
    swap: 'none',
    values
  })
  setStatus(`${group.length} block positions saved`)
}
