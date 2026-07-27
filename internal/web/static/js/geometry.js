// =====================================================================
// Sheet geometry. No state of its own: every value is read back off the
// server-rendered canvas, so this is the one module the rest can depend
// on without inheriting a dependency on anything mutable.
// =====================================================================
import { canvas } from './dom.js'

const DEFAULT_GEOMETRY = { width: 6000, height: 4000, grid: 20, blockWidth: 172, blockHeight: 84 }

// The server owns the sheet constants; the client reads them off the
// canvas so the grid, the snap step, and the bounds cannot drift apart.
export function geometry() {
  const root = canvas()
  if (!root) return DEFAULT_GEOMETRY
  const read = (name, fallback) => Number(root.dataset[name]) || fallback
  return {
    width: read('sheetWidth', DEFAULT_GEOMETRY.width),
    height: read('sheetHeight', DEFAULT_GEOMETRY.height),
    grid: read('sheetGrid', DEFAULT_GEOMETRY.grid),
    blockWidth: read('blockWidth', DEFAULT_GEOMETRY.blockWidth),
    blockHeight: read('blockHeight', DEFAULT_GEOMETRY.blockHeight)
  }
}

// The twin of edgePath() in view.go. The server draws every wire on load
// and this redraws them during a drag, so the two curves have to be the
// same curve; changing the bend here alone would make a wire jump the
// moment it is touched.
export function edgePath(source, target) {
  const { blockWidth, blockHeight } = geometry()
  const startX = source.offsetLeft + blockWidth
  const startY = source.offsetTop + blockHeight / 2
  const endX = target.offsetLeft
  const endY = target.offsetTop + blockHeight / 2
  const bend = Math.max(54, Math.abs(endX - startX) * 0.45)
  return `M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`
}

export function redrawEdges() {
  const root = canvas()
  if (!root) return
  root.querySelectorAll('[data-edge-source]').forEach((path) => {
    const source = root.querySelector(`[data-block-id="${path.dataset.edgeSource}"]`)
    const target = root.querySelector(`[data-block-id="${path.dataset.edgeTarget}"]`)
    if (source && target) path.setAttribute('d', edgePath(source, target))
  })
}
