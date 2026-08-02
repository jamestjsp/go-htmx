// =====================================================================
// Sheet geometry and derived route state. Persisted values are always
// read from the server-rendered canvas. Computed paths survive a whole
// workbench swap only while the flow, layout, and topology signatures match.
// =====================================================================
import { canvas } from './dom.js'
import {
  routeIntersectsRect,
  routeOrthogonal,
  routePath,
  routeSegments
} from './orthogonal-routing.js'

const DEFAULT_GEOMETRY = { width: 6000, height: 4000, grid: 20, blockWidth: 172, blockHeight: 84 }
const ROUTE_CLEARANCE = 16
const routeCache = new Map()
let cachedSignature = null

// The server owns the sheet constants; the client reads them off the
// canvas so the grid, the snap step, and the bounds cannot drift apart.
export function geometry(root = canvas()) {
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

function routingContext(root) {
  const { width, height, blockWidth, blockHeight } = geometry(root)
  const blocksByID = new Map()
  const obstaclesByBlock = new Map()
  const blockRecords = [...root.querySelectorAll('.block-card')].map((block) => {
    const blockID = String(block.dataset.blockId)
    const obstacle = {
      left: block.offsetLeft,
      top: block.offsetTop,
      right: block.offsetLeft + blockWidth,
      bottom: block.offsetTop + blockHeight
    }
    blocksByID.set(blockID, block)
    obstaclesByBlock.set(blockID, obstacle)
    return { blockID, obstacle }
  })
  blockRecords.sort((a, b) => a.blockID.localeCompare(b.blockID, undefined, { numeric: true }))
  const edgeElementsByID = new Map()
  root.querySelectorAll('[data-edge-id]').forEach((element) => {
    const edgeID = String(element.dataset.edgeId)
    const elements = edgeElementsByID.get(edgeID)
    if (elements) elements.push(element)
    else edgeElementsByID.set(edgeID, [element])
  })
  return {
    obstacles: blockRecords.map(({ obstacle }) => obstacle),
    obstaclesByBlock,
    blocksByID,
    edgeElementsByID,
    bounds: { left: 0, top: 0, right: width, bottom: height },
    blockWidth,
    blockHeight,
    layoutSignature: [
      root.dataset.flowId || '',
      width,
      height,
      blockWidth,
      blockHeight,
      ...blockRecords.flatMap(({ blockID, obstacle }) => [
        blockID,
        obstacle.left,
        obstacle.top,
        obstacle.right,
        obstacle.bottom
      ])
    ].join(':')
  }
}

function endpoint(block, center, output, context) {
  const offset = Number(center) || context.blockHeight / 2
  return {
    x: block.offsetLeft + (output ? context.blockWidth : 0),
    y: block.offsetTop + offset
  }
}

function computeRoute(start, end, occupied, context) {
  const points = routeOrthogonal({
    start,
    end,
    occupied,
    obstacles: context.obstacles,
    bounds: context.bounds
  })
  return { path: routePath(points), segments: routeSegments(points) }
}

export function signalPath(start, end) {
  const root = canvas()
  if (!root) return ''
  return computeRoute(start, end, [], routingContext(root)).path
}

export function routeAffectedByBlocks(route, movedBlockIDs, movedRects) {
  if (!route?.segments) return true
  if (movedBlockIDs.has(String(route.sourceID)) || movedBlockIDs.has(String(route.targetID))) return true
  return movedRects.some((rect) => routeIntersectsRect(route.segments, rect, ROUTE_CLEARANCE))
}

function setConnectionPath(context, edgeID, path) {
  const elements = context.edgeElementsByID.get(edgeID) || []
  elements.forEach((element) => {
    element.setAttribute('d', path)
  })
}

export function routeCacheReusable(previous, current, liveEdgeIDs, cachedRoutes) {
  if (!previous ||
      previous.layout !== current.layout ||
      previous.topology !== current.topology) return false
  return liveEdgeIDs.every((edgeID) => cachedRoutes.has(edgeID))
}

export function redrawEdges(blockIDs = null) {
  const root = canvas()
  if (!root) return
  const context = routingContext(root)
  const edges = [...root.querySelectorAll('.signal-line[data-edge-source]')]
  const edgeRecords = edges.map((edge) => ({
    edge,
    edgeID: String(edge.dataset.edgeId),
    topology: [
      edge.dataset.edgeId,
      edge.dataset.edgeSource,
      edge.dataset.edgeSourcePort,
      edge.dataset.edgeSourceCenter,
      edge.dataset.edgeTarget,
      edge.dataset.edgeTargetPort,
      edge.dataset.edgeTargetCenter
    ].join(':')
  })).sort((a, b) => a.edgeID.localeCompare(b.edgeID, undefined, { numeric: true }))
  const signature = {
    layout: context.layoutSignature,
    topology: edgeRecords.map(({ topology }) => topology).join('|')
  }
  const liveEdgeIDs = edgeRecords.map(({ edgeID }) => edgeID)
  const liveEdgeSet = new Set(liveEdgeIDs)
  const movedBlockIDs = Array.isArray(blockIDs) ? new Set(blockIDs.map(String)) : null
  const movedRects = movedBlockIDs === null
    ? []
    : [...movedBlockIDs].map((id) => context.obstaclesByBlock.get(id)).filter(Boolean)
  const reuseFullCache = movedBlockIDs === null &&
    routeCacheReusable(cachedSignature, signature, liveEdgeIDs, routeCache)
  const occupied = []
  edgeRecords.forEach(({ edge, edgeID }) => {
    const source = context.blocksByID.get(String(edge.dataset.edgeSource))
    const target = context.blocksByID.get(String(edge.dataset.edgeTarget))
    if (!source || !target) return
    const cached = routeCache.get(edgeID)
    const affected = !reuseFullCache && (movedBlockIDs === null ||
      routeAffectedByBlocks(cached, movedBlockIDs, movedRects)
    )
    let route = cached
    if (affected) {
      const start = endpoint(source, edge.dataset.edgeSourceCenter, true, context)
      const end = endpoint(target, edge.dataset.edgeTargetCenter, false, context)
      route = {
        ...computeRoute(start, end, occupied, context),
        sourceID: edge.dataset.edgeSource,
        targetID: edge.dataset.edgeTarget
      }
      routeCache.set(edgeID, route)
    }
    setConnectionPath(context, edgeID, route.path)
    occupied.push(...route.segments)
  })
  if (movedBlockIDs === null) {
    for (const edgeID of routeCache.keys()) {
      if (!liveEdgeSet.has(edgeID)) routeCache.delete(edgeID)
    }
    cachedSignature = signature
  }
}

export function createRedrawScheduler(redraw, requestFrame, cancelFrame) {
  let frame = null
  let token = 0
  let full = false
  const blockIDs = new Set()

  const deliver = () => {
    const selection = full ? null : [...blockIDs]
    full = false
    blockIDs.clear()
    redraw(selection)
  }
  const schedule = (changedBlockIDs = null) => {
    if (changedBlockIDs === null) full = true
    else changedBlockIDs.forEach((id) => blockIDs.add(String(id)))
    if (frame !== null) return
    const scheduledToken = ++token
    frame = requestFrame(() => {
      if (scheduledToken !== token) return
      frame = null
      deliver()
    })
  }
  const flush = (forceFull = false) => {
    if (forceFull) full = true
    if (frame !== null) {
      cancelFrame(frame)
      frame = null
      token += 1
    }
    if (full || blockIDs.size) deliver()
  }
  return { schedule, flush }
}

const redrawScheduler = createRedrawScheduler(
  redrawEdges,
  (callback) => window.requestAnimationFrame(callback),
  (frame) => window.cancelAnimationFrame(frame)
)

export const scheduleRedrawEdges = (blockIDs) => redrawScheduler.schedule(blockIDs)
export const flushRedrawEdges = () => redrawScheduler.flush(true)
