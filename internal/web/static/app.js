(() => {
  const nodeWidth = 172
  const nodeCenterY = 42
  let connectionSource = null
  let dragging = null

  const workbench = () => document.querySelector('#workbench')
  const canvas = () => document.querySelector('#flow-canvas')
  const status = () => document.querySelector('#interaction-status')
  const hint = () => document.querySelector('#canvas-hint')

  function edgePath(source, target) {
    const startX = source.offsetLeft + nodeWidth
    const startY = source.offsetTop + nodeCenterY
    const endX = target.offsetLeft
    const endY = target.offsetTop + nodeCenterY
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
    document.body.classList.remove('is-connecting')
    document.querySelectorAll('.connecting-source').forEach((node) => node.classList.remove('connecting-source'))
    const draft = document.querySelector('#draft-edge')
    if (draft) draft.setAttribute('d', '')
    if (status()) status().textContent = message
    if (hint()) hint().textContent = 'Select an output port to start wiring'
  }

  function beginConnection(button) {
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
    const root = canvas()
    const draft = document.querySelector('#draft-edge')
    if (!root || !draft) return
    const bounds = root.getBoundingClientRect()
    const source = connectionSource.node
    const startX = source.offsetLeft + nodeWidth
    const startY = source.offsetTop + nodeCenterY
    const endX = event.clientX - bounds.left + root.scrollLeft
    const endY = event.clientY - bounds.top + root.scrollTop
    const bend = Math.max(54, Math.abs(endX - startX) * 0.45)
    draft.setAttribute('d', `M ${startX} ${startY} C ${startX + bend} ${startY}, ${endX - bend} ${endY}, ${endX} ${endY}`)
  }

  function startDrag(event, node) {
    if (event.button !== 0 || event.target.closest('.port, input')) return
    const root = canvas()
    dragging = {
      node,
      root,
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

  function moveDrag(event) {
    if (!dragging || event.pointerId !== dragging.pointerId) return
    const dx = event.clientX - dragging.startX
    const dy = event.clientY - dragging.startY
    if (Math.abs(dx) + Math.abs(dy) > 4) dragging.moved = true
    const maxX = dragging.root.scrollWidth - nodeWidth - 20
    const maxY = dragging.root.scrollHeight - 90
    const left = Math.max(20, Math.min(maxX, dragging.left + dx))
    const top = Math.max(20, Math.min(maxY, dragging.top + dy))
    dragging.node.style.left = `${Math.round(left)}px`
    dragging.node.style.top = `${Math.round(top)}px`
    redrawEdges()
  }

  function endDrag(event) {
    if (!dragging || event.pointerId !== dragging.pointerId) return
    const current = dragging
    current.node.classList.remove('dragging')
    current.node.releasePointerCapture(event.pointerId)
    dragging = null
    if (!current.moved) {
      selectBlock(current.node)
      return
    }
    htmx.ajax('PATCH', `/blocks/${current.node.dataset.blockId}/position`, {
      swap: 'none',
      values: {
        x: current.node.offsetLeft,
        y: current.node.offsetTop
      }
    })
    if (status()) status().textContent = 'Block position saved'
  }

  function selectBlock(node) {
    const root = workbench()
    if (!root || !node) return
    htmx.ajax('GET', `/flows/${root.dataset.flowId}/workbench?selected=${node.dataset.blockId}`, {
      target: '#workbench',
      swap: 'outerHTML'
    })
  }

  function fitView() {
    const root = canvas()
    if (!root) return
    root.scrollTo({ left: 0, top: 0, behavior: 'smooth' })
    if (status()) status().textContent = 'Flowsheet centered'
  }

  document.addEventListener('pointerdown', (event) => {
    const output = event.target.closest('[data-output-port]')
    if (output) {
      beginConnection(output)
      return
    }
    const input = event.target.closest('[data-input-port]')
    if (input) {
      finishConnection(input)
      return
    }
    const node = event.target.closest('.block-card')
    if (node) startDrag(event, node)
  })
  document.addEventListener('pointermove', (event) => {
    moveDrag(event)
    drawDraft(event)
  })
  document.addEventListener('pointerup', endDrag)
  document.addEventListener('pointercancel', endDrag)
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
  document.addEventListener('htmx:afterSwap', redrawEdges)
  window.addEventListener('resize', redrawEdges)
  redrawEdges()
})()
