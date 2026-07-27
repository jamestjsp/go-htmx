(() => {
  const DEFAULT_GEOMETRY = { width: 6000, height: 4000, grid: 20, blockWidth: 172, blockHeight: 84 }
  const MIN_ZOOM = 0.25
  const MAX_ZOOM = 4
  let connectionSource = null
  let dragging = null
  let wiring = null

  const workbench = () => document.querySelector('#workbench')
  const canvas = () => document.querySelector('#flow-canvas')
  const sheet = () => document.querySelector('#sheet')
  const status = () => document.querySelector('#interaction-status')
  const hint = () => document.querySelector('#canvas-hint')

  // The server owns the sheet constants; the client reads them off the
  // canvas so the grid, the snap step, and the bounds cannot drift apart.
  function geometry() {
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

  function edgePath(source, target) {
    const { blockWidth, blockHeight } = geometry()
    const startX = source.offsetLeft + blockWidth
    const startY = source.offsetTop + blockHeight / 2
    const endX = target.offsetLeft
    const endY = target.offsetTop + blockHeight / 2
    const bend = Math.max(54, Math.abs(endX - startX) * 0.45)
    return `M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`
  }

  function redrawEdges() {
    const root = canvas()
    if (!root) return
    root.querySelectorAll('[data-edge-source]').forEach((path) => {
      const source = root.querySelector(`[data-block-id="${path.dataset.edgeSource}"]`)
      const target = root.querySelector(`[data-block-id="${path.dataset.edgeTarget}"]`)
      if (source && target) path.setAttribute('d', edgePath(source, target))
    })
  }

  function cancelConnection(message = 'Wire cancelled') {
    connectionSource = null
    wiring = null
    document.querySelectorAll('.hover-target, .hover-refused').forEach((node) => {
      node.classList.remove('hover-target', 'hover-refused')
    })
    document.body.classList.remove('is-connecting')
    document.querySelectorAll('.connecting-source').forEach((node) => node.classList.remove('connecting-source'))
    const draft = document.querySelector('#draft-edge')
    if (draft) draft.setAttribute('d', '')
    if (status()) status().textContent = message
    if (hint()) hint().textContent = 'Select an output port to start wiring'
  }

  function beginConnection(button, event) {
    connectionSource = {
      id: button.dataset.outputPort,
      name: button.dataset.outputName,
      node: button.closest('.block-card')
    }
    document.body.classList.add('is-connecting')
    document.querySelectorAll('.connecting-source').forEach((node) => node.classList.remove('connecting-source'))
    connectionSource.node.classList.add('connecting-source')
    if (status()) status().textContent = `Wiring from ${connectionSource.name}; choose an input`
    if (hint()) hint().textContent = `Connecting from ${connectionSource.name}`

    // Drag is the primary gesture. The pointer is captured on the canvas so
    // the draft edge keeps tracking even when it leaves the port, and the
    // target under the cursor is found geometrically.
    if (!event) return
    wiring = { pointerId: event.pointerId, startX: event.clientX, startY: event.clientY, moved: false }
    const root = canvas()
    if (root) root.setPointerCapture(event.pointerId)
  }

  function portUnderPointer(event) {
    const element = document.elementFromPoint(event.clientX, event.clientY)
    return element ? element.closest('[data-input-port]') : null
  }

  // The server is the authority on what may connect; this only decides
  // whether to show the target as inviting or as refused.
  function targetIsValid(port) {
    if (!port || !connectionSource) return false
    return port.closest('.block-card') !== connectionSource.node
  }

  function highlightTarget(port) {
    document.querySelectorAll('.hover-target, .hover-refused').forEach((node) => {
      node.classList.remove('hover-target', 'hover-refused')
    })
    if (!port) return
    port.classList.add(targetIsValid(port) ? 'hover-target' : 'hover-refused')
  }

  function moveWiring(event) {
    if (!wiring || event.pointerId !== wiring.pointerId) return
    if (Math.abs(event.clientX - wiring.startX) + Math.abs(event.clientY - wiring.startY) > 5) {
      wiring.moved = true
    }
    highlightTarget(portUnderPointer(event))
  }

  function endWiring(event) {
    if (!wiring || event.pointerId !== wiring.pointerId) return
    const root = canvas()
    if (root && root.hasPointerCapture(event.pointerId)) root.releasePointerCapture(event.pointerId)
    const dragged = wiring.moved
    wiring = null
    highlightTarget(null)
    const port = portUnderPointer(event)
    if (port && targetIsValid(port)) {
      finishConnection(port)
      return
    }
    if (port && !targetIsValid(port)) {
      cancelConnection('A block cannot wire to itself')
      return
    }
    // A press with no travel leaves the sticky click-then-click mode
    // armed, so both gestures coexist and the keyboard path still works.
    if (dragged) cancelConnection('Wire cancelled')
  }

  function finishConnection(button) {
    if (!connectionSource) {
      if (status()) status().textContent = 'Choose an output port first'
      return
    }
    const root = workbench()
    htmx.ajax('POST', `/flows/${root.dataset.flowId}/connections`, {
      target: '#workbench',
      swap: 'outerHTML',
      values: {
        source_id: connectionSource.id,
        target_id: button.dataset.inputPort
      }
    })
    cancelConnection('Saving signal connection…')
  }

  function drawDraft(event) {
    if (!connectionSource) return
    const draft = document.querySelector('#draft-edge')
    if (!draft) return
    const { blockWidth, blockHeight } = geometry()
    const source = connectionSource.node
    const startX = source.offsetLeft + blockWidth
    const startY = source.offsetTop + blockHeight / 2
    const { x: endX, y: endY } = screenToSheet(event.clientX, event.clientY)
    const bend = Math.max(54, Math.abs(endX - startX) * 0.45)
    draft.setAttribute('d', `M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`)
  }

  // =================================================================
  // Selection.
  //
  // Multi-selection is client state on purpose: the server keeps its
  // single `selected` parameter for the inspector, so the HTMX contract is
  // untouched and a marquee drag costs no round trips. A swap replaces
  // every block element, so the set is re-applied afterwards and ids that
  // no longer exist are dropped.
  // =================================================================
  const selection = new Set()
  let marquee = null

  function blockNodes() {
    const layer = sheet()
    return layer ? Array.from(layer.querySelectorAll('.block-card')) : []
  }

  function selectedNodes() {
    return blockNodes().filter((node) => selection.has(node.dataset.blockId))
  }

  function applySelection() {
    const nodes = blockNodes()
    if (!nodes.length) return
    const present = new Set(nodes.map((node) => node.dataset.blockId))
    selection.forEach((id) => {
      if (!present.has(id)) selection.delete(id)
    })
    // With nothing selected, defer to whatever the server marked, so a
    // swap does not silently drop the inspector's highlight.
    if (!selection.size) {
      const root = workbench()
      const serverSelected = root && root.dataset.selectedId
      if (serverSelected) selection.add(serverSelected)
    }
    nodes.forEach((node) => {
      node.classList.toggle('selected', selection.has(node.dataset.blockId))
    })
    renderSelectionBar()
    updateSelectionReadout()
  }

  function setSelection(ids) {
    selection.clear()
    ids.forEach((id) => selection.add(id))
    applySelection()
  }

  function toggleSelection(id) {
    if (selection.has(id)) selection.delete(id)
    else selection.add(id)
    applySelection()
    if (status()) status().textContent = `${selection.size} block${selection.size === 1 ? '' : 's'} selected`
  }

  function selectionBounds() {
    const nodes = selectedNodes()
    if (!nodes.length) return null
    let minX = Infinity
    let minY = Infinity
    let maxX = -Infinity
    let maxY = -Infinity
    nodes.forEach((node) => {
      minX = Math.min(minX, node.offsetLeft)
      minY = Math.min(minY, node.offsetTop)
      maxX = Math.max(maxX, node.offsetLeft + node.offsetWidth)
      maxY = Math.max(maxY, node.offsetTop + node.offsetHeight)
    })
    return { minX, minY, maxX, maxY }
  }

  function renderSelectionBar() {
    const root = canvas()
    if (!root) return
    let bar = root.querySelector('[data-selection-bar]')
    if (selection.size < 2) {
      if (bar) bar.remove()
      return
    }
    if (!bar) {
      bar = document.createElement('div')
      bar.dataset.selectionBar = ''
      bar.style.cssText = [
        'position:absolute', 'left:50%', 'bottom:14px', 'transform:translateX(-50%)',
        'z-index:25', 'display:flex', 'align-items:center', 'gap:10px',
        'padding:7px 9px 7px 13px', 'border-radius:8px',
        'background:var(--housing,#16201e)', 'color:var(--ink-inverse,#e8efec)',
        'font-size:12px', 'box-shadow:0 10px 26px rgb(10 20 18 / 34%)'
      ].join(';')
      bar.innerHTML =
        '<span data-selection-count></span>' +
        '<button type="button" data-selection-fit>Fit</button>' +
        '<button type="button" data-selection-delete>Delete</button>'
      bar.querySelectorAll('button').forEach((button) => {
        button.style.cssText = [
          'padding:5px 10px', 'border:1px solid var(--housing-line-strong,#3c4f4a)',
          'border-radius:5px', 'background:var(--housing-raised,#1f2c29)',
          'color:inherit', 'cursor:pointer', 'font-size:11px', 'font-weight:650'
        ].join(';')
      })
      root.appendChild(bar)
    }
    bar.querySelector('[data-selection-count]').textContent = `${selection.size} blocks selected`
  }

  function fitSelection() {
    const bounds = selectionBounds()
    if (bounds) fitTo(bounds, `Fitted to ${selection.size} selected blocks`)
  }

  function deleteSelection() {
    const root = workbench()
    if (!root || !selection.size) return
    const ids = Array.from(selection)
    if (ids.length > 1 && !window.confirm(`Delete ${ids.length} blocks and their signal wires?`)) return
    const query = ids.map((id) => `id=${encodeURIComponent(id)}`).join('&')
    htmx.ajax('DELETE', `/flows/${root.dataset.flowId}/blocks?${query}`, {
      target: '#workbench',
      swap: 'outerHTML'
    })
    selection.clear()
    if (status()) status().textContent = `Deleted ${ids.length} block${ids.length === 1 ? '' : 's'}`
  }

  function marqueeRect(start, current) {
    return {
      minX: Math.min(start.x, current.x),
      minY: Math.min(start.y, current.y),
      maxX: Math.max(start.x, current.x),
      maxY: Math.max(start.y, current.y)
    }
  }

  function blocksWithin(rect) {
    const { blockWidth, blockHeight } = geometry()
    return blockNodes().filter((node) => {
      const left = node.offsetLeft
      const top = node.offsetTop
      return left < rect.maxX && left + blockWidth > rect.minX &&
        top < rect.maxY && top + blockHeight > rect.minY
    })
  }

  function beginMarquee(event) {
    const root = canvas()
    if (!root || event.button !== 0) return false
    if (!event.target.closest('#flow-canvas')) return false
    if (event.target.closest('.block-card, .port, [data-selection-bar], .canvas-legend')) return false
    marquee = {
      pointerId: event.pointerId,
      start: screenToSheet(event.clientX, event.clientY),
      base: event.shiftKey ? Array.from(selection) : [],
      moved: false,
      element: null,
      current: null
    }
    if (!event.shiftKey) setSelection([])
    root.setPointerCapture(event.pointerId)
    event.preventDefault()
    return true
  }

  function moveMarquee(event) {
    if (!marquee || event.pointerId !== marquee.pointerId) return
    const layer = sheet()
    if (!layer) return
    marquee.current = screenToSheet(event.clientX, event.clientY)
    marquee.moved = true
    const rect = marqueeRect(marquee.start, marquee.current)
    if (!marquee.element || marquee.element.parentNode !== layer) {
      marquee.element = document.createElement('div')
      marquee.element.dataset.marquee = ''
      layer.appendChild(marquee.element)
    }
    const hairline = 1 / viewport.zoom
    marquee.element.style.cssText = [
      'position:absolute', `left:${rect.minX}px`, `top:${rect.minY}px`,
      `width:${rect.maxX - rect.minX}px`, `height:${rect.maxY - rect.minY}px`,
      `border:${hairline}px solid var(--probe-deep,#0d6156)`,
      'background:var(--probe-glow,rgb(53 179 156 / 20%))',
      'pointer-events:none', 'z-index:19'
    ].join(';')
    // Live feedback: highlight as the band sweeps, not only on release.
    setSelection(marquee.base.concat(blocksWithin(rect).map((node) => node.dataset.blockId)))
  }

  function endMarquee(event) {
    if (!marquee || event.pointerId !== marquee.pointerId) return
    const root = canvas()
    if (root) root.releasePointerCapture(event.pointerId)
    if (marquee.element) marquee.element.remove()
    const { moved, start, current, base } = marquee
    marquee = null
    if (!moved || !current) {
      setSelection(base)
      return
    }
    const ids = blocksWithin(marqueeRect(start, current)).map((node) => node.dataset.blockId)
    setSelection(base.concat(ids))
    if (status()) {
      status().textContent = selection.size
        ? `${selection.size} block${selection.size === 1 ? '' : 's'} selected`
        : 'Selection cleared'
    }
  }

  function startDrag(event, node) {
    if (event.button !== 0 || event.target.closest('.port, input')) return
    const id = node.dataset.blockId
    if (event.shiftKey || event.metaKey || event.ctrlKey) {
      toggleSelection(id)
      return
    }
    // Dragging an unselected block selects it alone; dragging one that is
    // already part of a selection moves the whole selection.
    if (!selection.has(id)) setSelection([id])
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
    const hairline = 1 / viewport.zoom
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

  function moveDrag(event) {
    if (!dragging || event.pointerId !== dragging.pointerId) return
    // Pointer travel is in screen pixels; block positions are in sheet
    // units, so every delta divides by the zoom or the block outruns the
    // cursor as soon as you leave 100%.
    const dx = (event.clientX - dragging.startX) / viewport.zoom
    const dy = (event.clientY - dragging.startY) / viewport.zoom
    if (Math.abs(dx) + Math.abs(dy) > 4 / viewport.zoom) dragging.moved = true
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
    redrawEdges()
  }

  function endDrag(event) {
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
    savePositions(current.group)
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
      if (status()) status().textContent = 'Block position saved'
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
    if (status()) status().textContent = `${group.length} block positions saved`
  }

  function selectBlock(node) {
    const root = workbench()
    if (!root || !node) return
    setSelection([node.dataset.blockId])
    htmx.ajax('GET', `/flows/${root.dataset.flowId}/workbench?selected=${node.dataset.blockId}`, {
      target: '#workbench',
      swap: 'outerHTML'
    })
  }

  // =================================================================
  // Viewport: the sheet is a fixed world that the user pans and zooms a
  // window across. #sheet carries the transform; the canvas clips it.
  //
  // Every interaction converts pointer coordinates through screenToSheet.
  // Reading offsetLeft/scrollLeft directly, as this file used to, silently
  // breaks the moment zoom leaves 100%.
  // =================================================================
  const viewport = { x: 0, y: 0, zoom: 1 }
  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
  let panning = null
  let spaceHeld = false
  let animateTimer = 0

  function viewportKey() {
    const root = workbench()
    return `processlab:viewport:${root ? root.dataset.flowId : 'default'}`
  }

  function screenToSheet(clientX, clientY) {
    const root = canvas()
    if (!root) return { x: 0, y: 0 }
    const bounds = root.getBoundingClientRect()
    return {
      x: (clientX - bounds.left - viewport.x) / viewport.zoom,
      y: (clientY - bounds.top - viewport.y) / viewport.zoom
    }
  }

  function applyViewport(animate = false) {
    const root = canvas()
    const layer = sheet()
    if (!root || !layer) return
    const { grid } = geometry()
    root.style.setProperty('--pan-x', `${viewport.x}px`)
    root.style.setProperty('--pan-y', `${viewport.y}px`)
    root.style.setProperty('--zoom', String(viewport.zoom))
    root.style.setProperty('--grid-fine', `${grid * viewport.zoom}px`)
    root.style.setProperty('--grid-coarse', `${grid * 4 * viewport.zoom}px`)
    root.dataset.zoomBand = viewport.zoom < 0.6 ? 'coarse' : 'normal'
    if (animate && !reducedMotion.matches) {
      root.dataset.animate = 'true'
      window.clearTimeout(animateTimer)
      animateTimer = window.setTimeout(() => delete root.dataset.animate, 200)
    }
    const readout = document.querySelector('#zoom-readout')
    if (readout) readout.textContent = `${Math.round(viewport.zoom * 100)}%`
    setReadout('#readout-zoom', `${Math.round(viewport.zoom * 100)}%`)
    saveReadoutGrid()
    saveViewport()
  }

  // ---- readout rail -------------------------------------------------
  // The rail is server-rendered markup with stable ids; JS only fills in
  // the values that change as the pointer and the selection move.
  function setReadout(selector, text) {
    const node = document.querySelector(selector)
    if (node && node.textContent !== text) node.textContent = text
  }

  function saveReadoutGrid() {
    setReadout('#readout-grid', String(geometry().grid))
  }

  function trackCursorReadout(event) {
    if (!event.target.closest('#flow-canvas')) return
    const point = screenToSheet(event.clientX, event.clientY)
    setReadout('#readout-cursor-x', String(Math.round(point.x)).padStart(4, '0'))
    setReadout('#readout-cursor-y', String(Math.round(point.y)).padStart(4, '0'))
  }

  function updateSelectionReadout() {
    const node = document.querySelector('#readout-selection')
    if (!node) return
    node.textContent = String(selection.size)
    node.dataset.count = String(selection.size)
  }

  function saveViewport() {
    try {
      window.localStorage.setItem(viewportKey(), JSON.stringify(viewport))
    } catch (error) {
      /* storage disabled; the viewport still works, it just will not persist */
    }
  }

  function loadViewport() {
    try {
      const stored = JSON.parse(window.localStorage.getItem(viewportKey()) || 'null')
      if (!stored || !Number.isFinite(stored.zoom)) return false
      viewport.x = Number(stored.x) || 0
      viewport.y = Number(stored.y) || 0
      viewport.zoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, stored.zoom))
      return true
    } catch (error) {
      return false
    }
  }

  // Zoom about a screen point, keeping whatever sheet coordinate sits
  // under that point pinned there.
  function zoomAround(nextZoom, clientX, clientY, animate = false) {
    const root = canvas()
    if (!root) return
    const bounds = root.getBoundingClientRect()
    const px = clientX - bounds.left
    const py = clientY - bounds.top
    const zoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, nextZoom))
    const anchorX = (px - viewport.x) / viewport.zoom
    const anchorY = (py - viewport.y) / viewport.zoom
    viewport.zoom = zoom
    viewport.x = px - anchorX * zoom
    viewport.y = py - anchorY * zoom
    applyViewport(animate)
  }

  function zoomByStep(factor) {
    const root = canvas()
    if (!root) return
    const bounds = root.getBoundingClientRect()
    zoomAround(viewport.zoom * factor, bounds.left + bounds.width / 2, bounds.top + bounds.height / 2, true)
  }

  function resetZoom() {
    const root = canvas()
    if (!root) return
    const bounds = root.getBoundingClientRect()
    zoomAround(1, bounds.left + bounds.width / 2, bounds.top + bounds.height / 2, true)
    if (status()) status().textContent = 'Zoom reset to 100%'
  }

  function contentBounds() {
    const layer = sheet()
    if (!layer) return null
    const blocks = Array.from(layer.querySelectorAll('.block-card'))
    if (!blocks.length) return null
    let minX = Infinity
    let minY = Infinity
    let maxX = -Infinity
    let maxY = -Infinity
    blocks.forEach((node) => {
      minX = Math.min(minX, node.offsetLeft)
      minY = Math.min(minY, node.offsetTop)
      maxX = Math.max(maxX, node.offsetLeft + node.offsetWidth)
      maxY = Math.max(maxY, node.offsetTop + node.offsetHeight)
    })
    return { minX, minY, maxX, maxY }
  }

  function fitTo(bounds, message) {
    const root = canvas()
    if (!root || !bounds) return
    const box = root.getBoundingClientRect()
    const { blockWidth } = geometry()
    const pad = blockWidth * 0.5
    const width = bounds.maxX - bounds.minX + pad * 2
    const height = bounds.maxY - bounds.minY + pad * 2
    // Never zoom past 100% to fill the window; a two-block sheet blown up
    // to 400% is disorienting, not helpful.
    const zoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM,
      Math.min(1, Math.min(box.width / width, box.height / height))))
    viewport.zoom = zoom
    viewport.x = (box.width - width * zoom) / 2 - (bounds.minX - pad) * zoom
    viewport.y = (box.height - height * zoom) / 2 - (bounds.minY - pad) * zoom
    applyViewport(true)
    if (message && status()) status().textContent = message
  }

  function fitView() {
    const bounds = contentBounds()
    if (!bounds) {
      viewport.x = 0
      viewport.y = 0
      viewport.zoom = 1
      applyViewport(true)
      if (status()) status().textContent = 'Empty sheet; view reset'
      return
    }
    fitTo(bounds, 'Flowsheet fitted to the window')
  }

  function beginPan(event) {
    const root = canvas()
    if (!root || !event.target.closest('#flow-canvas')) return false
    const wantsPan = event.button === 1 || (event.button === 0 && spaceHeld)
    if (!wantsPan) return false
    panning = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      originX: viewport.x,
      originY: viewport.y
    }
    root.setPointerCapture(event.pointerId)
    root.classList.add('is-panning')
    event.preventDefault()
    return true
  }

  function movePan(event) {
    if (!panning || event.pointerId !== panning.pointerId) return
    viewport.x = panning.originX + (event.clientX - panning.startX)
    viewport.y = panning.originY + (event.clientY - panning.startY)
    applyViewport()
  }

  function endPan(event) {
    if (!panning || event.pointerId !== panning.pointerId) return
    const root = canvas()
    if (root) {
      root.releasePointerCapture(event.pointerId)
      root.classList.remove('is-panning')
    }
    panning = null
  }

  function setPanCursor() {
    const root = canvas()
    if (root) root.classList.toggle('can-pan', spaceHeld)
  }

  function typingInAField(event) {
    const node = event.target
    return node instanceof HTMLElement && node.closest('input, textarea, select, [contenteditable="true"]')
  }

  document.addEventListener('wheel', (event) => {
    if (!event.target.closest('#flow-canvas')) return
    event.preventDefault()
    if (event.ctrlKey || event.metaKey) {
      // Trackpad pinch arrives here as ctrl+wheel; exp keeps the steps
      // proportional so zooming feels the same at 25% and 400%.
      zoomAround(viewport.zoom * Math.exp(-event.deltaY * 0.01), event.clientX, event.clientY)
      return
    }
    viewport.x -= event.deltaX
    viewport.y -= event.deltaY
    applyViewport()
  }, { passive: false })

  document.addEventListener('click', (event) => {
    if (event.target.closest('[data-zoom-in]')) zoomByStep(1.2)
    else if (event.target.closest('[data-zoom-out]')) zoomByStep(1 / 1.2)
    else if (event.target.closest('[data-zoom-reset]')) resetZoom()
  })

  document.addEventListener('keydown', (event) => {
    if (event.key === ' ' && !typingInAField(event) && !spaceHeld) {
      spaceHeld = true
      setPanCursor()
      if (event.target.closest('#flow-canvas')) event.preventDefault()
      return
    }
    if (typingInAField(event)) return
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
    if (event.key === ' ') {
      spaceHeld = false
      setPanCursor()
    }
  })

  // =================================================================
  // Keyboard.
  //
  // Every binding below is guarded by typingInAField first. Without it,
  // typing a block name would delete the selection on Backspace and
  // duplicate it on "d", which is the most destructive thing this file
  // could get wrong.
  // =================================================================
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
    ]]
  ]

  let shortcutSheet = null
  let shortcutOpener = null

  function closeShortcutSheet() {
    if (!shortcutSheet) return
    shortcutSheet.remove()
    shortcutSheet = null
    if (shortcutOpener && document.contains(shortcutOpener)) shortcutOpener.focus()
    else if (canvas()) canvas().focus()
    shortcutOpener = null
  }

  function openShortcutSheet() {
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

  // =================================================================
  // Canvas context menu.
  //
  // menu.js owns construction, placement, dismissal, and the keyboard;
  // this file only says where the menu applies and what is in it. The
  // native menu is suppressed only over the sheet; over the rails and
  // the dock the browser's own menu stays available, where it is useful.
  // =================================================================
  function menuItems(node, point) {
    if (node) {
      const plural = selection.size > 1 ? ` ${selection.size} blocks` : ''
      return [
        { label: 'Rename', run: () => focusInspectorName(node) },
        { label: `Duplicate${plural}`, run: duplicateSelection },
        { label: 'Disconnect all wires', run: () => disconnectBlock(node) },
        { label: `Fit to${plural || ' this block'}`, run: fitSelection },
        { label: `Delete${plural}`, run: deleteSelection, danger: true }
      ]
    }
    return [
      { label: 'Add block here', submenu: paletteChoices(point) },
      { label: 'Select all', run: selectAll },
      { label: 'Fit to contents', run: fitView },
      { label: 'Reset zoom', run: resetZoom }
    ]
  }

  // Read the block catalogue off the palette rather than duplicating it,
  // so a new block kind on the server appears here with no client change.
  function paletteChoices(point) {
    const { grid } = geometry()
    return Array.from(document.querySelectorAll('.palette-list form')).map((form) => {
      const kind = form.querySelector('[name="kind"]').value
      const label = form.querySelector('strong').textContent
      return {
        label,
        run: () => {
          const root = workbench()
          if (!root) return
          htmx.ajax('POST', `/flows/${root.dataset.flowId}/blocks`, {
            target: '#workbench',
            swap: 'outerHTML',
            values: {
              kind,
              x: String(Math.round(point.x / grid) * grid),
              y: String(Math.round(point.y / grid) * grid)
            }
          })
        }
      }
    })
  }

  function focusInspectorName(node) {
    selectBlock(node)
    // The inspector arrives with the swap, so wait for it before focusing.
    const focusWhenReady = () => {
      const field = document.querySelector('.property-form input[name="name"]')
      if (field) {
        field.focus()
        field.select()
      }
      document.removeEventListener('htmx:afterSettle', focusWhenReady)
    }
    document.addEventListener('htmx:afterSettle', focusWhenReady)
  }

  function disconnectBlock(node) {
    htmx.ajax('DELETE', `/blocks/${node.dataset.blockId}/connections`, {
      target: '#workbench',
      swap: 'outerHTML'
    })
    if (status()) status().textContent = 'Signal wires removed'
  }

  ProcessLab.menu.register({
    selector: '#flow-canvas',
    restoreFocus: () => { if (canvas()) canvas().focus() },
    items: (event) => {
      const node = event.target.closest('.block-card')
      // Right-clicking outside the current selection re-targets it; inside
      // it, the existing selection and its plural actions are kept.
      if (node && !selection.has(node.dataset.blockId)) setSelection([node.dataset.blockId])
      if (!node) setSelection([])
      return menuItems(node, screenToSheet(event.clientX, event.clientY))
    }
  })

  function selectAll() {
    const ids = blockNodes().map((node) => node.dataset.blockId)
    setSelection(ids)
    if (status()) status().textContent = `${ids.length} blocks selected`
  }

  function nudgeSelection(stepX, stepY) {
    const nodes = selectedNodes()
    if (!nodes.length) return
    const { width, height, blockWidth, blockHeight } = geometry()
    const group = nodes.map((node) => ({ node, left: node.offsetLeft, top: node.offsetTop }))
    group.forEach((member) => {
      member.node.style.left = `${Math.max(0, Math.min(width - blockWidth, member.left + stepX))}px`
      member.node.style.top = `${Math.max(0, Math.min(height - blockHeight, member.top + stepY))}px`
    })
    redrawEdges()
    savePositions(group)
  }

  function duplicateSelection() {
    const root = workbench()
    if (!root || !selection.size) return
    htmx.ajax('POST', `/flows/${root.dataset.flowId}/blocks/duplicate`, {
      target: '#workbench',
      swap: 'outerHTML',
      values: { id: Array.from(selection) }
    })
    if (status()) status().textContent = `Duplicated ${selection.size} block${selection.size === 1 ? '' : 's'}`
  }

  document.addEventListener('click', (event) => {
    if (event.target.closest('[data-shortcuts-close]')) closeShortcutSheet()
    else if (shortcutSheet && event.target === shortcutSheet) closeShortcutSheet()
    else if (event.target.closest('[data-shortcuts-open]')) openShortcutSheet()
  })

  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && shortcutSheet) {
      event.preventDefault()
      closeShortcutSheet()
      return
    }
    // The guard comes before every binding, deliberately.
    if (typingInAField(event)) return
    // The dock resizer owns the arrow keys while it has focus.
    if (event.target.closest('[data-dock-resizer], [role="separator"]')) return
    const { grid } = geometry()
    const step = event.shiftKey ? grid * 5 : grid

    if (event.key === 'Delete' || event.key === 'Backspace') {
      if (!selection.size) return
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
      if (selection.size) setSelection([])
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

  window.addEventListener('blur', () => {
    spaceHeld = false
    setPanCursor()
  })

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
    if (event.key === 'Escape') cancelConnection()
    if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
      event.preventDefault()
      document.querySelector('#run-form')?.requestSubmit()
    }
  })
  document.addEventListener('htmx:beforeSwap', () => {
    if (connectionSource) cancelConnection('Workbench updated')
  })
  // A swap replaces #sheet, so the transform has to be re-stamped or the
  // view snaps back to the origin on every edit.
  //
  // Both events are needed: at afterSwap the replacement node is not yet
  // the one querySelector returns, so styling there alone writes to a node
  // htmx is about to discard. afterSettle is what actually sticks.
  const restoreViewport = () => {
    applyViewport()
    applySelection()
    redrawEdges()
  }
  document.addEventListener('htmx:afterSwap', restoreViewport)
  document.addEventListener('htmx:afterSettle', restoreViewport)
  window.addEventListener('resize', redrawEdges)

  function initViewport() {
    if (!loadViewport()) {
      applyViewport()
      fitView()
      return
    }
    applyViewport()
  }

  initViewport()
  applySelection()
  redrawEdges()

  // =================================================================
  // Shell state: rail collapse and simulation dock height.
  //
  // This is per-user view state, so it lives in localStorage and is
  // expressed as data attributes on #workbench that CSS reads for all
  // sizing. HTMX replaces #workbench wholesale on nearly every action,
  // which throws those attributes away, so applyShellState() is the one
  // place that rebuilds them and it runs on load and after every swap.
  // =================================================================
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

  function applyShellState() {
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
  document.addEventListener('htmx:afterSwap', applyShellState)
  document.addEventListener('htmx:afterSettle', applyShellState)
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
})()
