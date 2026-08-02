import assert from 'node:assert/strict'
import test from 'node:test'

import {
  createFrameChunker,
  createRedrawScheduler,
  routeAffectedByBlocks,
  routeCacheReusable,
  syncBlockRouteEndpoints
} from './geometry.js'

test('coalesces drag redraws into one animation frame', () => {
  const frames = []
  const redraws = []
  const scheduler = createRedrawScheduler(
    (blockIDs) => redraws.push(blockIDs),
    (callback) => {
      frames.push(callback)
      return frames.length
    },
    () => {}
  )

  scheduler.schedule(['12'])
  scheduler.schedule(['12', '18'])

  assert.equal(frames.length, 1)
  assert.deepEqual(redraws, [])
  frames[0]()
  assert.deepEqual(redraws, [['12', '18']])
})

test('a final flush replaces a queued partial redraw with one full redraw', () => {
  const frames = []
  const cancelled = []
  const redraws = []
  const scheduler = createRedrawScheduler(
    (blockIDs) => redraws.push(blockIDs),
    (callback) => {
      frames.push(callback)
      return 41
    },
    (frame) => cancelled.push(frame)
  )

  scheduler.schedule(['12'])
  scheduler.flush(true)

  assert.deepEqual(cancelled, [41])
  assert.deepEqual(redraws, [null])
  frames[0]()
  assert.deepEqual(redraws, [null])
})

test('partial redraws include connected routes and routes obstructed by the moved block', () => {
  const moved = new Set(['12'])
  const movedRects = [{ left: 180, top: 80, right: 260, bottom: 160 }]

  assert.equal(routeAffectedByBlocks({
    sourceID: '12',
    targetID: '20',
    segments: []
  }, moved, movedRects), true)
  assert.equal(routeAffectedByBlocks({
    sourceID: '1',
    targetID: '2',
    segments: [{ a: { x: 100, y: 120 }, b: { x: 300, y: 120 } }]
  }, moved, movedRects), true)
  assert.equal(routeAffectedByBlocks({
    sourceID: '1',
    targetID: '2',
    segments: [{ a: { x: 100, y: 40 }, b: { x: 300, y: 40 } }]
  }, moved, movedRects), false)
})

test('full redraw cache requires matching layout, topology, and every live edge', () => {
  const signature = { layout: 'flow:1:block:1:20:40', topology: '8:1:2' }
  const routes = new Map([['8', { path: 'M 1 2' }], ['9', { path: 'M 3 4' }]])

  assert.equal(routeCacheReusable(signature, { ...signature }, ['8', '9'], routes), true)
  assert.equal(routeCacheReusable(null, signature, ['8'], routes), false)
  assert.equal(routeCacheReusable(signature, { ...signature, layout: 'flow:2' }, ['8'], routes), false)
  assert.equal(routeCacheReusable(signature, { ...signature, topology: '10:1:2' }, ['8'], routes), false)
  assert.equal(routeCacheReusable(signature, { ...signature }, ['8', '10'], routes), false)
})

test('synchronizes only changed route centers after a bounded card update', () => {
  const input = { dataset: { inputPort: '1', portCenter: '62' } }
  const output = { dataset: { outputPort: '0', portCenter: '42' } }
  const block = {
    dataset: { blockId: '12' },
    querySelectorAll(selector) {
      if (selector === '[data-output-port]') return [output]
      if (selector === '[data-input-port]') return [input]
      return []
    }
  }
  const sourceEdge = {
    dataset: {
      edgeId: '7', edgeSource: '12', edgeSourcePort: '0', edgeSourceCenter: '41',
      edgeTarget: '20', edgeTargetPort: '0', edgeTargetCenter: '42'
    }
  }
  const targetEdge = {
    dataset: {
      edgeId: '8', edgeSource: '20', edgeSourcePort: '0', edgeSourceCenter: '42',
      edgeTarget: '12', edgeTargetPort: '1', edgeTargetCenter: '61'
    }
  }
  const root = {
    querySelectorAll(selector) {
      if (selector === '.block-card') return [block]
      if (selector === '[data-edge-id]') return [sourceEdge, targetEdge]
      return []
    }
  }

  assert.equal(syncBlockRouteEndpoints('12', root), true)
  assert.equal(sourceEdge.dataset.edgeSourceCenter, '42')
  assert.equal(targetEdge.dataset.edgeTargetCenter, '62')
  assert.equal(syncBlockRouteEndpoints('12', root), false)
})

test('authoritative redraw work is frame-bounded and cancelable', () => {
  const frames = []
  const processed = []
  let clock = 0
  const chunker = createFrameChunker(
    (callback) => frames.push(callback),
    () => {
      clock += 3
      return clock
    }
  )

  chunker.run([1, 2, 3, 4, 5], (item) => processed.push(item), () => processed.push('done'), 5)
  frames.shift()()
  assert.deepEqual(processed, [1, 2])
  assert.equal(frames.length, 1)
  chunker.cancel()
  frames.shift()()
  assert.deepEqual(processed, [1, 2])
})
