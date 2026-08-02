import assert from 'node:assert/strict'
import test from 'node:test'

import {
  createRedrawScheduler,
  routeAffectedByBlocks,
  routeCacheReusable
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
