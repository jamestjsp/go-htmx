#!/usr/bin/env node
// =====================================================================
// swap-scaling-bench.mjs — measures what a full #workbench swap costs as
// a flowsheet grows. It is the harness behind docs/swap-scaling.md.
//
//   Usage: node docs/swap-scaling-bench.mjs [options]
//
//     --sizes 50,150,400   block counts to build and measure (multiples of 10)
//     --port 8137          port for the Process Lab instance under test
//     --cdp-port 9233      port for the headless Chrome DevTools endpoint
//     --server-reps 30     HTTP samples per endpoint per size
//     --swap-reps 15       parameter-edit swaps per size per zoom
//     --load-reps 7        page loads per size per zoom
//     --out results.json   also write the raw samples as JSON
//     --keep               leave the scratch directory in place
//
// Needs the Go toolchain, Node 20 or newer for the global WebSocket, and
// Google Chrome at the path in CHROME below. It resolves the repository
// from its own location, so it runs from anywhere. A full run at the
// default settings takes roughly ten minutes, most of it building the
// 400-block fixture one HTTP request at a time. It prints markdown
// tables on stdout and progress on stderr.
//
// Nothing in the application is instrumented. The server figures come
// from a plain HTTP client on loopback with the body fully drained; the
// browser figures come from Chrome's own event timeline, read over CDP.
// Both methods and their confounds are stated in docs/swap-scaling.md.
//
// The harness builds its own binary, its own scratch database in a fresh
// temp directory, and its own Chrome profile, and removes all three on
// exit. It never touches processlab.db in the repository root.
// =====================================================================

import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { gzipSync } from 'node:zlib'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const REPO = path.resolve(HERE, '..')
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'

// The fixture is a repeating ten-block train, so every size is the same
// shape at a different length and the three rows of the table compare.
// Two branches per train (a fan-out into the variadic Sum, and a second
// tap into the spectrum sink) keep the connection count equal to the
// block count instead of one short, which is roughly what a real sheet
// looks like.
const TRAIN = ['sine', 'gain', 'lag', 'gain', 'sum', 'integrator', 'transfer', 'pid', 'scope', 'spectrum']
const TRAIN_WIRES = [[0, 1], [1, 2], [2, 3], [3, 4], [4, 5], [5, 6], [6, 7], [7, 8], [2, 4], [5, 9]]
const COLUMNS = 20
const ORIGIN = { x: 60, y: 80 }
const STEP = { x: 240, y: 120 } // block size plus a wire run, and a multiple of the 20px grid
// Mirrors the constants in internal/studio/model.go; used only to refuse a
// size the sheet cannot hold, which caps this layout at 640 blocks.
const SHEET = { width: 6000, height: 4000, blockHeight: 84 }

const options = parseArgs(process.argv.slice(2))
const base = `http://127.0.0.1:${options.port}`

main().catch((error) => {
  console.error(error)
  process.exitCode = 1
})

// ---- driver ---------------------------------------------------------

async function main() {
  const scratch = await fs.mkdtemp(path.join(os.tmpdir(), 'swap-scaling-'))
  const cleanup = []
  try {
    const binary = path.join(scratch, 'processlab')
    log(`building ${binary}`)
    await run('go', ['build', '-o', binary, './cmd/processlab'], { cwd: REPO })

    const server = spawn(binary, ['-addr', `127.0.0.1:${options.port}`, '-db', path.join(scratch, 'bench.db')], {
      cwd: scratch,
      stdio: ['ignore', 'pipe', 'pipe']
    })
    cleanup.push(() => server.kill('SIGKILL'))
    server.stderr.on('data', (chunk) => process.stderr.write(`[server] ${chunk}`))
    await waitFor(() => fetch(`${base}/`).then((r) => r.ok), 'server')
    log(`server up on ${base}`)

    const fixtures = []
    for (const size of options.sizes) {
      log(`building the ${size}-block fixture`)
      fixtures.push(await buildFixture(size))
    }

    const results = { generatedAt: new Date().toISOString(), sizes: [] }
    for (const fixture of fixtures) {
      log(`server timings for ${fixture.blocks} blocks`)
      results.sizes.push({ ...fixture, server: await benchServer(fixture), browser: {} })
    }

    const chrome = await startChrome(scratch)
    cleanup.push(chrome.kill)
    const session = await attach()
    cleanup.push(session.close)
    await session.send('Page.enable')
    await session.send('Runtime.enable')
    for (const entry of results.sizes) {
      for (const zoom of [1, 0.25]) {
        log(`browser timings for ${entry.blocks} blocks at ${Math.round(zoom * 100)}%`)
        entry.browser[zoom] = await benchBrowser(session, entry, zoom)
      }
    }

    report(results)
    if (options.out) {
      await fs.writeFile(options.out, JSON.stringify(results, null, 2))
      log(`raw samples written to ${options.out}`)
    }
  } finally {
    for (const step of cleanup.reverse()) {
      try { step() } catch { /* already gone */ }
    }
    if (options.keep) log(`scratch kept at ${scratch}`)
    else await fs.rm(scratch, { recursive: true, force: true })
  }
}

// ---- fixtures -------------------------------------------------------

// buildFixture drives the real HTTP API rather than writing rows, so the
// flowsheet it leaves behind is one the application could have produced
// and every domain rule (grid snap, arity, acyclicity) has been enforced.
async function buildFixture(blocks) {
  if (blocks % TRAIN.length !== 0) throw new Error(`size ${blocks} is not a multiple of ${TRAIN.length}`)
  // The sheet is finite. Past its capacity the server's openPosition would
  // quietly relocate blocks into its own lattice and the fixture would stop
  // being the grid this harness claims to measure, so refuse instead.
  const rows = Math.floor((SHEET.height - SHEET.blockHeight - ORIGIN.y) / STEP.y) + 1
  if (Math.ceil(blocks / COLUMNS) > rows) {
    throw new Error(`${blocks} blocks needs ${Math.ceil(blocks / COLUMNS)} rows; ` +
      `the ${SHEET.width}x${SHEET.height} sheet holds ${rows} at this pitch (${rows * COLUMNS} blocks)`)
  }
  const created = await fetch(`${base}/projects`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ name: `Scale ${blocks}` }),
    redirect: 'manual'
  })
  const location = created.headers.get('location')
  if (!location) throw new Error(`no redirect creating project: ${created.status}`)
  const [, projectID, flowID] = location.match(/^\/projects\/(\d+)\/flows\/(\d+)$/)

  const ids = []
  for (let index = 0; index < blocks; index += 1) {
    const kind = TRAIN[index % TRAIN.length]
    const slot = { x: ORIGIN.x + (index % COLUMNS) * STEP.x, y: ORIGIN.y + Math.floor(index / COLUMNS) * STEP.y }
    const response = await fetch(`${base}/flows/${flowID}/blocks`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({ kind, x: String(slot.x), y: String(slot.y) })
    })
    const html = await response.text()
    const match = html.match(/data-selected-id="(\d+)"/)
    if (!match) throw new Error(`block ${index} (${kind}) was not added: ${response.status}`)
    ids.push(match[1])
  }

  let wires = 0
  for (let train = 0; train < blocks / TRAIN.length; train += 1) {
    for (const [from, to] of TRAIN_WIRES) {
      const response = await fetch(`${base}/flows/${flowID}/connections`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: new URLSearchParams({
          source_id: ids[train * TRAIN.length + from],
          target_id: ids[train * TRAIN.length + to]
        })
      })
      const html = await response.text()
      if (/class="error-banner"/.test(html)) {
        throw new Error(`wire ${from}->${to} in train ${train} refused: ${html.match(/<p>([^<]*)<\/p>/)?.[1]}`)
      }
      wires += 1
    }
  }

  // The inspected block is a Gain: one numeric field, so the parameter
  // edit is the smallest legal mutation that still swaps everything.
  const probe = ids[1]
  const fragment = await fetch(`${base}/flows/${flowID}/workbench?selected=${probe}`).then((r) => r.text())
  return {
    blocks,
    connections: wires,
    projectID,
    flowID,
    probe,
    form: inspectorForm(fragment),
    blockIDs: ids
  }
}

// inspectorForm reads the inspector's own field set out of the rendered
// fragment. Every parameter is required by the domain, so a PUT has to
// send back what the form shows; hardcoding the list here would drift.
function inspectorForm(html) {
  const form = html.match(/<form class="property-form"[\s\S]*?<\/form>/)
  if (!form) throw new Error('no inspector form in the fragment')
  const fields = {}
  for (const input of form[0].matchAll(/<input\b[^>]*>/g)) {
    const name = input[0].match(/\bname="([^"]*)"/)
    const value = input[0].match(/\bvalue="([^"]*)"/)
    if (name) fields[name[1]] = value ? decodeEntities(value[1]) : ''
  }
  return fields
}

function decodeEntities(value) {
  return value
    .replace(/&#(\d+);/g, (_, code) => String.fromCodePoint(Number(code)))
    .replace(/&quot;/g, '"').replace(/&lt;/g, '<').replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
}

// ---- server timings -------------------------------------------------

// benchServer times the handler from a plain loopback client with the
// body fully drained, so the figure covers the whole response and not
// just the headers Go flushes on its first write. The static-asset row
// is the floor: loopback plus this client's own overhead.
async function benchServer(fixture) {
  const { flowID, probe, form } = fixture
  const fragmentURL = `${base}/flows/${flowID}/workbench?selected=${probe}`
  const pageURL = `${base}/projects/${fixture.projectID}/flows/${flowID}?selected=${probe}`

  let gain = Number(form.gain ?? 1)
  const editBody = () => {
    gain = Number((gain + 0.05).toFixed(2))
    return new URLSearchParams({ ...form, gain: String(gain) })
  }

  const cases = {
    floor: () => fetch(`${base}/assets/tokens.css`),
    getFragment: () => fetch(fragmentURL),
    getPage: () => fetch(pageURL),
    putBlock: () => fetch(`${base}/blocks/${probe}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: editBody()
    }),
    patchPosition: () => fetch(`${base}/blocks/${probe}/position`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({ x: '60', y: '80' })
    })
  }

  const timings = {}
  const bytes = {}
  for (const [name, call] of Object.entries(cases)) {
    for (let i = 0; i < 5; i += 1) await call().then((r) => r.arrayBuffer())
    const samples = []
    for (let i = 0; i < options.serverReps; i += 1) {
      const start = performance.now()
      const response = await call()
      const body = await response.arrayBuffer()
      samples.push(performance.now() - start)
      if (!response.ok && response.status !== 204) throw new Error(`${name}: ${response.status}`)
      bytes[name] = body.byteLength
    }
    timings[name] = summarise(samples)
  }

  const fragment = Buffer.from(await fetch(fragmentURL).then((r) => r.arrayBuffer()))
  return {
    timings,
    bytes,
    fragmentBytes: fragment.byteLength,
    fragmentGzipBytes: gzipSync(fragment).byteLength
  }
}

// ---- browser timings ------------------------------------------------

async function benchBrowser(session, fixture, zoom) {
  const url = `${base}/projects/${fixture.projectID}/flows/${fixture.flowID}?selected=${fixture.probe}`

  // The first navigation is the warm-up: it fills the HTTP cache (htmx
  // itself comes from a CDN) and gives an origin to seed the client
  // state against, so every measured load starts from the same place.
  await navigate(session, url)
  await evaluate(session, `(() => {
    localStorage.clear()
    localStorage.setItem('processlab:viewport:${fixture.flowID}', JSON.stringify({ x: 0, y: 0, zoom: ${zoom} }))
    localStorage.setItem('processlab:rail-left', 'expanded')
    localStorage.setItem('processlab:rail-right', 'expanded')
    localStorage.setItem('processlab:dock-height', '240')
    return true
  })()`)

  const loads = []
  for (let i = 0; i < options.loadReps; i += 1) {
    await navigate(session, url)
    // The paint entry can lag the load event by a frame, so wait for it
    // rather than recording a null and losing the column.
    await waitFor(() => evaluate(session, 'performance.getEntriesByName("first-contentful-paint").length > 0'), 'first paint', 5000)
      .catch(() => log('  first-contentful-paint never arrived for this load'))
    loads.push(await evaluate(session, `(() => {
      const nav = performance.getEntriesByType('navigation')[0]
      const paint = performance.getEntriesByName('first-contentful-paint')[0]
      const script = performance.getEntriesByType('resource').find((entry) => entry.name.includes('htmx'))
      return {
        responseEnd: nav.responseEnd,
        domInteractive: nav.domInteractive,
        domContentLoaded: nav.domContentLoadedEventEnd,
        load: nav.loadEventEnd,
        firstContentfulPaint: paint ? paint.startTime : null,
        htmxScript: script ? script.duration : null,
        htmxCached: script ? script.transferSize === 0 : null
      }
    })()`))
  }

  await navigate(session, url)
  await installProbe(session)
  const observed = await evaluate(session, `(() => {
    const box = document.querySelector('#flow-canvas').getBoundingClientRect()
    const cards = Array.from(document.querySelectorAll('.block-card'))
    return {
      blocks: cards.length,
      edges: document.querySelectorAll('[data-edge-source]').length,
      zoom: Number(getComputedStyle(document.querySelector('#flow-canvas')).getPropertyValue('--zoom')),
      visible: cards.filter((node) => {
        const r = node.getBoundingClientRect()
        return r.right > box.left && r.left < box.right && r.bottom > box.top && r.top < box.bottom
      }).length
    }
  })()`)
  if (Math.abs(observed.zoom - zoom) > 1e-6) {
    throw new Error(`zoom ${zoom} was not applied; the canvas reports ${observed.zoom}`)
  }
  if (observed.blocks !== fixture.blocks) {
    throw new Error(`expected ${fixture.blocks} block cards, the page has ${observed.blocks}`)
  }

  const edits = []
  for (let i = 0; i < options.swapReps; i += 1) {
    edits.push(await evaluate(session, 'window.__bench.parameterEdit()', true))
  }

  const moves = []
  for (let i = 0; i < options.swapReps; i += 1) {
    moves.push(await evaluate(session, `window.__bench.movePersist(${fixture.probe}, ${60 + (i % 2) * 20}, 80)`, true))
  }

  const redraws = []
  for (let i = 0; i < options.swapReps; i += 1) {
    redraws.push(await evaluate(session, 'window.__bench.redrawCost()'))
  }

  const drag = await benchDrag(session)

  // The profile runs last and on its own. A sampling profiler perturbs
  // what it measures, so it is never on while the numbers above are
  // being taken; it is here only to say which function the time is in.
  const profiles = {
    swap: await profile(session, () => evaluate(session, 'window.__bench.parameterEdit()', true), 5),
    drag: await profile(session, () => benchDrag(session), 1)
  }

  return { loads, observed, edits, moves, redraws, drag, profiles }
}

async function profile(session, action, repeats) {
  await session.send('Profiler.enable')
  await session.send('Profiler.setSamplingInterval', { interval: 100 })
  await session.send('Profiler.start')
  for (let i = 0; i < repeats; i += 1) await action()
  const { profile: raw } = await session.send('Profiler.stop')
  await session.send('Profiler.disable')

  const byNode = new Map(raw.nodes.map((node) => [node.id, node.callFrame]))
  const self = new Map()
  for (let i = 1; i < raw.samples.length; i += 1) {
    const frame = byNode.get(raw.samples[i])
    if (!frame) continue
    const key = `${frame.functionName || '(anonymous)'} · ${(frame.url || '').split('/').pop() || 'native'}`
    self.set(key, (self.get(key) ?? 0) + raw.timeDeltas[i] / 1000)
  }
  // (idle) is the htmx settle delay and the gaps between repeats; it is
  // wall clock the page spent waiting, not work anything could remove.
  return [...self.entries()]
    .filter(([name]) => !name.startsWith('(idle)'))
    .sort((a, b) => b[1] - a[1])
    .slice(0, 8)
    .map(([name, milliseconds]) => ({ name, milliseconds: Number(milliseconds.toFixed(1)) }))
}

// benchDrag drives a real mouse over CDP rather than synthesising
// PointerEvents, because startDrag() calls setPointerCapture and that
// refuses a pointer id the browser never issued.
async function benchDrag(session) {
  // The press has to land on the card body, and hit-testing decides that,
  // not arithmetic: .port::before is an invisible pad sized 22px/zoom, so
  // at 25% it covers the middle of a card and the press would arm a wire
  // instead of a drag. Candidate points are therefore checked against
  // elementFromPoint rather than assumed.
  const target = await evaluate(session, `(() => {
    const box = document.querySelector('#flow-canvas').getBoundingClientRect()
    for (const node of document.querySelectorAll('.block-card')) {
      const r = node.getBoundingClientRect()
      if (!(r.left > box.left + 4 && r.top > box.top + 4 &&
            r.right < box.right - 160 && r.bottom < box.bottom - 100)) continue
      for (const fx of [0.5, 0.62, 0.75, 0.38]) {
        for (const fy of [0.5, 0.78, 0.22]) {
          const x = Math.round(r.left + r.width * fx)
          const y = Math.round(r.top + r.height * fy)
          const hit = document.elementFromPoint(x, y)
          if (hit && hit.closest('.block-card') === node && !hit.closest('.port, input')) {
            return { x, y, id: node.dataset.blockId }
          }
        }
      }
    }
    return null
  })()`)
  if (!target) return { samples: [], live: 0, note: 'no block card offered a draggable point inside the canvas at this zoom' }

  await evaluate(session, 'window.__bench.armDrag()')
  const mouse = (type, x, y, buttons) => session.send('Input.dispatchMouseEvent', {
    type, x: Math.round(x), y: Math.round(y), button: 'left', buttons, clickCount: 1
  })
  await mouse('mousePressed', target.x, target.y, 1)
  for (let i = 1; i <= 40; i += 1) {
    await mouse('mouseMoved', target.x + i * 2, target.y + (i % 7), 1)
  }
  await mouse('mouseReleased', target.x + 80, target.y, 0)
  const drag = await evaluate(session, 'window.__bench.readDrag()')
  // Put the block back so repeated runs do not walk it across the sheet.
  await fetch(`${base}/blocks/${target.id}/position`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ x: '60', y: '80' })
  })
  return { ...drag, blockID: target.id }
}

// installProbe adds listeners only. Every application listener was
// registered when its module first evaluated, so a capture-phase
// listener added now still runs before them and a bubble-phase one
// still runs after — which is what brackets the re-apply pass without
// editing a line of it.
async function installProbe(session) {
  await evaluate(session, `(() => {
    const bench = {}
    const settle = (kick) => new Promise((resolve, reject) => {
      const t = {}
      const stamp = (key) => () => { if (t[key] === undefined) t[key] = performance.now() }
      const bind = []
      const add = (type, capture, key) => {
        const fn = stamp(key)
        document.addEventListener(type, fn, capture)
        bind.push([type, fn, capture])
      }
      add('htmx:beforeSwap', true, 'beforeSwap')
      add('htmx:afterSwap', true, 'swapStart')
      add('htmx:afterSwap', false, 'swapEnd')
      add('htmx:afterSettle', true, 'settleStart')
      const done = () => {
        t.settleEnd = performance.now()
        requestAnimationFrame(() => requestAnimationFrame(() => {
          t.frame = performance.now()
          bind.forEach(([type, fn, capture]) => document.removeEventListener(type, fn, capture))
          document.removeEventListener('htmx:afterSettle', done, false)
          clearTimeout(guard)
          resolve({
            request: t.beforeSwap - t.t0,
            swap: t.swapStart - t.beforeSwap,
            afterSwapReapply: t.swapEnd - t.swapStart,
            settleWait: t.settleStart - t.swapEnd,
            settleReapply: t.settleEnd - t.settleStart,
            total: t.settleEnd - t.t0,
            toFrame: t.frame - t.t0
          })
        }))
      }
      document.addEventListener('htmx:afterSettle', done, false)
      bind.push(['htmx:afterSettle', done, false])
      const guard = setTimeout(() => {
        bind.forEach(([type, fn, capture]) => document.removeEventListener(type, fn, capture))
        reject(new Error('swap did not settle within 30s'))
      }, 30000)
      t.t0 = performance.now()
      kick()
    })

    // The faithful parameter edit: the inspector's own form, submitted
    // the way a click on "Apply changes" submits it.
    //
    // stepUp() rather than an arbitrary increment. The field carries a
    // step, and requestSubmit() runs constraint validation first, so a
    // value off the step is refused by the browser and nothing at all
    // happens — a silent no-op that looks exactly like a hung swap.
    bench.parameterEdit = () => {
      const form = document.querySelector('form.property-form')
      const field = form.querySelector('input[type="number"]')
      field.stepUp()
      if (!form.checkValidity()) throw new Error('the inspector form is invalid; nothing would be submitted')
      return settle(() => form.requestSubmit())
    }

    // The block move. It answers 204 and swaps nothing, so there is no
    // settle to wait for: htmx:afterRequest is the end of the interaction.
    bench.movePersist = (id, x, y) => new Promise((resolve, reject) => {
      const t0 = performance.now()
      const done = () => {
        document.removeEventListener('htmx:afterRequest', done)
        clearTimeout(guard)
        resolve({ total: performance.now() - t0 })
      }
      const guard = setTimeout(() => {
        document.removeEventListener('htmx:afterRequest', done)
        reject(new Error('move did not answer within 30s'))
      }, 30000)
      document.addEventListener('htmx:afterRequest', done)
      htmx.ajax('PATCH', '/blocks/' + id + '/position', { swap: 'none', values: { x, y } })
    })

    // A drag frame: capture before input.js's pointermove handler, bubble
    // after it, so the difference is the whole canvas input layer.
    // redrawEdges is bound to window resize by input.js, so a synthetic
    // resize runs the real function synchronously and its cost is the
    // gap around dispatchEvent. shell.js listens to resize too, and its
    // applyShellState is in this figure; that part does not grow with
    // the block count, so the difference between two sizes is redraw.
    //
    // It also answers whether the redraw changes anything: the server
    // already emitted a d for every edge, from the same geometry, so
    // after a swap this should be rewriting each path to itself.
    bench.redrawCost = () => {
      const paths = Array.from(document.querySelectorAll('[data-edge-source]'))
      const numbers = (value) => (value.match(/-?\\d+(?:\\.\\d+)?/g) || []).map(Number)
      const before = paths.map((path) => numbers(path.getAttribute('d') || ''))
      const t0 = performance.now()
      window.dispatchEvent(new Event('resize'))
      const elapsed = performance.now() - t0
      const changed = paths.filter((path, index) => {
        const after = numbers(path.getAttribute('d') || '')
        const was = before[index]
        return was.length !== after.length || was.some((value, i) => Math.abs(value - after[i]) > 1e-6)
      }).length
      return { ms: elapsed, paths: paths.length, changed }
    }

    bench.armDrag = () => {
      bench._drag = []
      bench._live = 0
      bench._mark = 0
      document.addEventListener('pointermove', bench._before = () => { bench._mark = performance.now() }, true)
      document.addEventListener('pointermove', bench._after = () => {
        if (!bench._mark) return
        bench._drag.push(performance.now() - bench._mark)
        // A frame with no .dragging card is a drag that never started,
        // which would otherwise read as a very fast one.
        if (document.querySelector('.block-card.dragging')) bench._live += 1
      }, false)
    }
    bench.readDrag = () => {
      document.removeEventListener('pointermove', bench._before, true)
      document.removeEventListener('pointermove', bench._after, false)
      return { samples: bench._drag, live: bench._live }
    }

    window.__bench = bench
  })()`)
}

// ---- chrome + CDP ---------------------------------------------------

async function startChrome(scratch) {
  const child = spawn(CHROME, [
    '--headless=new',
    `--remote-debugging-port=${options.cdpPort}`,
    `--user-data-dir=${path.join(scratch, 'chrome')}`,
    '--window-size=1600,1000',
    '--force-device-scale-factor=1',
    '--hide-scrollbars',
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-background-timer-throttling',
    '--disable-backgrounding-occluded-windows',
    '--disable-renderer-backgrounding',
    '--disable-features=CalculateNativeWinOcclusion,Translate,MediaRouter',
    'about:blank'
  ], { stdio: ['ignore', 'ignore', 'ignore'] })
  const kill = () => child.kill('SIGKILL')
  await waitFor(() => fetch(`http://127.0.0.1:${options.cdpPort}/json/version`).then((r) => r.ok), 'chrome')
  return { kill }
}

async function attach() {
  const targets = await fetch(`http://127.0.0.1:${options.cdpPort}/json`).then((r) => r.json())
  const page = targets.find((entry) => entry.type === 'page')
  if (!page) throw new Error('no page target in Chrome')
  return connect(page.webSocketDebuggerUrl)
}

function connect(url) {
  const socket = new WebSocket(url)
  const pending = new Map()
  const waiters = new Map()
  let nextID = 0
  socket.addEventListener('message', (event) => {
    const message = JSON.parse(event.data)
    if (message.id !== undefined) {
      const slot = pending.get(message.id)
      if (!slot) return
      pending.delete(message.id)
      if (message.error) slot.reject(new Error(`${message.error.message} (${JSON.stringify(message.error.data ?? '')})`))
      else slot.resolve(message.result)
      return
    }
    const list = waiters.get(message.method)
    if (list) {
      waiters.delete(message.method)
      list.forEach((resolve) => resolve(message.params))
    }
  })
  const ready = new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true })
    socket.addEventListener('error', reject, { once: true })
  })
  return {
    ready,
    send(method, params = {}) {
      const id = (nextID += 1)
      return ready.then(() => new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject })
        socket.send(JSON.stringify({ id, method, params }))
      }))
    },
    event(method) {
      return new Promise((resolve) => {
        const list = waiters.get(method) ?? []
        list.push(resolve)
        waiters.set(method, list)
      })
    },
    close() { socket.close() }
  }
}

async function navigate(session, url) {
  const loaded = session.event('Page.loadEventFired')
  await session.send('Page.navigate', { url })
  await loaded
  // The canvas modules are deferred, so they have run by load; this just
  // confirms the fragment and htmx are both actually there.
  await waitFor(async () => evaluate(session, 'Boolean(window.htmx && document.querySelector("#flow-canvas"))'), 'workbench')
}

async function evaluate(session, expression, awaitPromise = false) {
  const result = await session.send('Runtime.evaluate', {
    expression,
    awaitPromise,
    returnByValue: true
  })
  if (result.exceptionDetails) {
    throw new Error(`evaluate failed: ${result.exceptionDetails.exception?.description ?? result.exceptionDetails.text}`)
  }
  return result.result.value
}

// ---- reporting ------------------------------------------------------

function summarise(samples) {
  const sorted = [...samples].sort((a, b) => a - b)
  return {
    n: sorted.length,
    median: percentile(sorted, 0.5),
    p25: percentile(sorted, 0.25),
    p75: percentile(sorted, 0.75),
    min: sorted[0],
    max: sorted[sorted.length - 1]
  }
}

function percentile(sorted, fraction) {
  if (!sorted.length) return null
  const index = Math.min(sorted.length - 1, Math.max(0, Math.round(fraction * (sorted.length - 1))))
  return sorted[index]
}

const ms = (value) => (value === null || value === undefined ? '—' : value.toFixed(1))
const band = (stat) => (stat.n ? `${ms(stat.median)} (${ms(stat.p25)}–${ms(stat.p75)})` : '—')

function report(results) {
  const out = []
  out.push('', '### Server (loopback client, body fully drained)', '')
  out.push('| blocks | wires | fragment | gzipped | GET workbench | PUT block | PATCH position | floor |')
  out.push('| --- | --- | --- | --- | --- | --- | --- | --- |')
  for (const size of results.sizes) {
    const t = size.server.timings
    out.push(`| ${size.blocks} | ${size.connections} | ${kb(size.server.fragmentBytes)} | ${kb(size.server.fragmentGzipBytes)} | ` +
      `${band(t.getFragment)} | ${band(t.putBlock)} | ${band(t.patchPosition)} | ${band(t.floor)} |`)
  }

  out.push('', '### Browser — initial page load', '')
  out.push('| blocks | zoom | on screen | FCP | DOMContentLoaded | load |')
  out.push('| --- | --- | --- | --- | --- | --- |')
  for (const size of results.sizes) {
    for (const zoom of [1, 0.25]) {
      const cell = size.browser[zoom]
      const fcp = summarise(cell.loads.map((l) => l.firstContentfulPaint).filter((v) => v !== null))
      const dcl = summarise(cell.loads.map((l) => l.domContentLoaded))
      const load = summarise(cell.loads.map((l) => l.load))
      out.push(`| ${size.blocks} | ${Math.round(zoom * 100)}% | ${cell.observed.visible} | ${band(fcp)} | ${band(dcl)} | ${band(load)} |`)
    }
  }

  out.push('', '### Browser — parameter edit (PUT /blocks/{id}, full swap)', '')
  out.push('| blocks | zoom | request | swap | afterSwap re-apply | htmx settle wait | settle re-apply | total | to first frame |')
  out.push('| --- | --- | --- | --- | --- | --- | --- | --- | --- |')
  for (const size of results.sizes) {
    for (const zoom of [1, 0.25]) {
      const edits = size.browser[zoom].edits
      const pick = (key) => summarise(edits.map((e) => e[key]))
      out.push(`| ${size.blocks} | ${Math.round(zoom * 100)}% | ${band(pick('request'))} | ${band(pick('swap'))} | ` +
        `${band(pick('afterSwapReapply'))} | ${band(pick('settleWait'))} | ${band(pick('settleReapply'))} | ` +
        `${band(pick('total'))} | ${band(pick('toFrame'))} |`)
    }
  }

  out.push('', '### Browser — block move (PATCH /blocks/{id}/position, no swap)', '')
  out.push('| blocks | zoom | round trip | drag frame handler | frames | live |')
  out.push('| --- | --- | --- | --- | --- | --- |')
  for (const size of results.sizes) {
    for (const zoom of [1, 0.25]) {
      const cell = size.browser[zoom]
      const move = summarise(cell.moves.map((m) => m.total))
      const drag = summarise(cell.drag.samples ?? [])
      out.push(`| ${size.blocks} | ${Math.round(zoom * 100)}% | ${band(move)} | ${band(drag)} | ${drag.n} | ${cell.drag.live ?? 0} |`)
    }
  }

  out.push('', '### redrawEdges alone, run through its own window-resize binding', '')
  out.push('| blocks | zoom | edge paths | one redraw | paths whose d actually changed |')
  out.push('| --- | --- | --- | --- | --- |')
  for (const size of results.sizes) {
    for (const zoom of [1, 0.25]) {
      const cell = size.browser[zoom].redraws
      out.push(`| ${size.blocks} | ${Math.round(zoom * 100)}% | ${cell[0].paths} | ` +
        `${band(summarise(cell.map((r) => r.ms)))} | ${Math.max(...cell.map((r) => r.changed))} |`)
    }
  }

  out.push('', '### Where the time goes (sampling profile, 100µs, taken separately from the timings above)', '')
  for (const size of results.sizes) {
    for (const [label, key] of [['5 parameter edits', 'swap'], ['one 40-frame drag', 'drag']]) {
      const top = size.browser[1].profiles[key].filter((entry) => entry.milliseconds >= 1)
      out.push(`- **${size.blocks} blocks, ${label} at 100%:** ` +
        (top.length ? top.map((e) => `\`${e.name}\` ${e.milliseconds}ms`).join(', ') : 'nothing above 1ms'))
    }
  }

  const cached = results.sizes.flatMap((size) => [1, 0.25].flatMap((zoom) => size.browser[zoom].loads))
  out.push('', `Times are milliseconds, median with the interquartile band in brackets. ` +
    `htmx came from the browser cache on ${cached.filter((l) => l.htmxCached).length} of ${cached.length} measured loads.`, '')
  console.log(out.join('\n'))
}

const kb = (bytes) => `${(bytes / 1024).toFixed(1)} KB`

// ---- plumbing -------------------------------------------------------

function parseArgs(argv) {
  const parsed = {
    sizes: [50, 150, 400],
    port: 8137,
    cdpPort: 9233,
    serverReps: 30,
    swapReps: 15,
    loadReps: 7,
    out: null,
    keep: false
  }
  for (let i = 0; i < argv.length; i += 1) {
    const flag = argv[i]
    const value = argv[i + 1]
    switch (flag) {
      case '--sizes': parsed.sizes = value.split(',').map(Number); i += 1; break
      case '--port': parsed.port = Number(value); i += 1; break
      case '--cdp-port': parsed.cdpPort = Number(value); i += 1; break
      case '--server-reps': parsed.serverReps = Number(value); i += 1; break
      case '--swap-reps': parsed.swapReps = Number(value); i += 1; break
      case '--load-reps': parsed.loadReps = Number(value); i += 1; break
      case '--out': parsed.out = path.resolve(value); i += 1; break
      case '--keep': parsed.keep = true; break
      default: throw new Error(`unknown option ${flag}`)
    }
  }
  return parsed
}

async function run(command, args, opts) {
  const child = spawn(command, args, { stdio: 'inherit', ...opts })
  const [code] = await once(child, 'exit')
  if (code !== 0) throw new Error(`${command} ${args.join(' ')} exited ${code}`)
}

async function waitFor(check, what, timeout = 30000) {
  const deadline = Date.now() + timeout
  for (;;) {
    try {
      if (await check()) return
    } catch { /* not up yet */ }
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`)
    await new Promise((resolve) => setTimeout(resolve, 100))
  }
}

function log(message) {
  process.stderr.write(`… ${message}\n`)
}
