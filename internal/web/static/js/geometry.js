// =====================================================================
// Sheet geometry and derived route state. Persisted values are always
// read from the server-rendered canvas. Computed paths survive a whole
// workbench swap only while the flow, layout, and topology signatures match.
// =====================================================================
import { canvas } from './dom.js'
import {
  createObstacleIndex,
  createSegmentIndex,
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
    const left = Number.parseFloat(block.style.left) || 0
    const top = Number.parseFloat(block.style.top) || 0
    const obstacle = {
      left,
      top,
      right: left + blockWidth,
      bottom: top + blockHeight
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
  const obstacles = blockRecords.map(({ obstacle }) => obstacle)
  return {
    obstacles,
    obstacleIndex: createObstacleIndex(obstacles),
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
  const left = Number.parseFloat(block.style.left) || 0
  const top = Number.parseFloat(block.style.top) || 0
  return {
    x: left + (output ? context.blockWidth : 0),
    y: top + offset
  }
}

function computeRoute(start, end, occupied, context) {
  const points = routeOrthogonal({
    start,
    end,
    occupied,
    obstacles: context.obstacles,
    obstacleIndex: context.obstacleIndex,
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

export function syncBlockRouteEndpoints(blockID, root = canvas()) {
  if (!root) return false
  const id = String(blockID)
  const block = [...root.querySelectorAll('.block-card')]
    .find((node) => String(node.dataset.blockId) === id)
  if (!block) return false
  const ports = {
    source: new Map([...block.querySelectorAll('[data-output-port]')]
      .map((node) => [String(node.dataset.outputPort), String(node.dataset.portCenter)])),
    target: new Map([...block.querySelectorAll('[data-input-port]')]
      .map((node) => [String(node.dataset.inputPort), String(node.dataset.portCenter)]))
  }
  let changed = false
  root.querySelectorAll('[data-edge-id]').forEach((edge) => {
    for (const role of ['source', 'target']) {
      if (String(edge.dataset[`edge${role[0].toUpperCase()}${role.slice(1)}`]) !== id) continue
      const port = String(edge.dataset[`edge${role[0].toUpperCase()}${role.slice(1)}Port`])
      const center = ports[role].get(port)
      const centerKey = `edge${role[0].toUpperCase()}${role.slice(1)}Center`
      if (center !== undefined && edge.dataset[centerKey] !== center) {
        edge.dataset[centerKey] = center
        changed = true
      }
    }
  })
  return changed
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

function prepareRedraw(blockIDs) {
  const root = canvas()
  if (!root) return null
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
  return {
    context,
    edgeRecords,
    signature,
    liveEdgeSet,
    movedBlockIDs,
    movedRects,
    reuseFullCache,
    occupied: createSegmentIndex()
  }
}

function processEdge(state, { edge, edgeID }) {
  const source = state.context.blocksByID.get(String(edge.dataset.edgeSource))
  const target = state.context.blocksByID.get(String(edge.dataset.edgeTarget))
  if (!source || !target) return
  const cached = routeCache.get(edgeID)
  const affected = !state.reuseFullCache && (state.movedBlockIDs === null ||
    routeAffectedByBlocks(cached, state.movedBlockIDs, state.movedRects)
  )
  let route = cached
  if (affected) {
    const start = endpoint(source, edge.dataset.edgeSourceCenter, true, state.context)
    const end = endpoint(target, edge.dataset.edgeTargetCenter, false, state.context)
    route = {
      ...computeRoute(start, end, state.occupied, state.context),
      sourceID: edge.dataset.edgeSource,
      targetID: edge.dataset.edgeTarget
    }
    routeCache.set(edgeID, route)
  }
  setConnectionPath(state.context, edgeID, route.path)
  state.occupied.addAll(route.segments)
}

function finishRedraw(state) {
  if (state.movedBlockIDs !== null) return
  for (const edgeID of routeCache.keys()) {
    if (!state.liveEdgeSet.has(edgeID)) routeCache.delete(edgeID)
  }
  cachedSignature = state.signature
}

export function createFrameChunker(requestFrame, now) {
  let generation = 0
  return {
    run(items, process, finish, frameBudget = 8) {
      const token = ++generation
      let index = 0
      const runChunk = () => {
        if (token !== generation) return
        const deadline = now() + frameBudget
        do {
          process(items[index])
          index += 1
        } while (index < items.length && now() < deadline)
        if (index < items.length) requestFrame(runChunk)
        else finish()
      }
      if (items.length) requestFrame(runChunk)
      else finish()
    },
    cancel() {
      generation += 1
    }
  }
}

const authoritativeChunks = createFrameChunker(
  (callback) => window.requestAnimationFrame(callback),
  () => performance.now()
)

export function redrawEdges(blockIDs = null) {
  authoritativeChunks.cancel()
  const state = prepareRedraw(blockIDs)
  if (!state) return
  state.edgeRecords.forEach((edge) => processEdge(state, edge))
  finishRedraw(state)
}

export function scheduleAuthoritativeRedraw(frameBudget = 8) {
  const state = prepareRedraw(null)
  if (!state) return
  authoritativeChunks.run(
    state.edgeRecords,
    (edge) => processEdge(state, edge),
    () => {
      finishRedraw(state)
      document.dispatchEvent(new Event('processlab:routesSettled'))
    },
    frameBudget
  )
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
export const flushRedrawEdges = () => {
  redrawScheduler.flush(false)
  scheduleAuthoritativeRedraw()
}
