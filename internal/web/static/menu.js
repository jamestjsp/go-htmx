// =====================================================================
// Shared context menu.
//
// A caller registers a region: a selector saying where the browser's own
// menu is replaced, and a builder returning that region's items. Menu
// construction, edge flipping, dismissal, and keyboard navigation live
// here, so a second menu never needs a second copy of them.
//
// Loaded before the canvas modules and before tabs.js, each of which
// registers a region with it. A namespace on window rather than an ES
// module because tabs.js is a classic script and cannot import one; a
// deferred classic script and a module script share a single execution
// list ordered by document position, so the order of the tags in page.html
// is what guarantees this file runs first.
// =====================================================================
(() => {
  const regions = []
  let contextMenu = null
  let openRegion = null
  let openHost = null

  // A region is { selector, items, restoreFocus? }:
  //   selector     — the menu opens for right-clicks inside a match.
  //   items(event, host) — the items for this right-click, built fresh
  //                  each time. Returning an empty list opens nothing.
  //   restoreFocus(host) — optional. Escape hands focus back to the host
  //                  itself unless a region names a better landing place.
  function register(region) {
    regions.push(region)
  }

  function closeContextMenu() {
    if (!contextMenu) return
    contextMenu.remove()
    contextMenu = null
    openRegion = null
    openHost = null
  }

  // Escape returns focus to the region the menu was raised from, so the
  // keyboard is not stranded on <body>. Region hosts are ordinary
  // containers — #flow-canvas is a div — and a div only accepts focus once
  // it is a focus target, so make it one on the way. tabindex="-1" rather
  // than "0": the canvas is reached by clicking it or by leaving a menu or
  // the shortcut sheet, never by tabbing to it, and a positive value would
  // insert a stop in the tab order between the toolbar and the dock.
  //
  // A host that already takes focus is left alone. A flowsheet tab is an
  // <a href>, and stamping tabindex="-1" on it to hand focus back would
  // quietly drop that tab out of the tab order for good — the menu would
  // make the thing it was raised over unreachable by keyboard.
  const FOCUSABLE = 'a[href], button, input, select, textarea, [tabindex]'

  function restoreFocusTo(region, host) {
    if (region && region.restoreFocus) {
      region.restoreFocus(host)
      return
    }
    if (!host || !host.isConnected) return
    if (!host.matches(FOCUSABLE)) host.tabIndex = -1
    host.focus()
  }

  // Both candidates contain the event target, so one always contains the
  // other: the deepest match wins, whatever order regions registered in.
  function regionFor(target) {
    let best = null
    regions.forEach((region) => {
      const host = target.closest(region.selector)
      if (!host) return
      if (!best || best.host.contains(host)) best = { region, host }
    })
    return best
  }

  function buildMenu(items, x, y) {
    const menu = document.createElement('div')
    menu.dataset.contextMenu = ''
    menu.setAttribute('role', 'menu')
    menu.style.cssText = [
      'position:fixed', 'z-index:180', 'min-width:190px', 'max-height:60vh', 'overflow:auto',
      'padding:5px', 'border:1px solid var(--housing-line-strong,#3c4f4a)', 'border-radius:8px',
      'background:var(--housing,#16201e)', 'color:var(--ink-inverse,#e8efec)',
      'box-shadow:0 18px 40px rgb(6 12 11 / 44%)', 'font-size:12px'
    ].join(';')

    items.forEach((item) => {
      if (item.submenu) {
        const group = document.createElement('div')
        group.style.cssText = 'padding:5px 9px 3px;font-size:11px;font-weight:800;letter-spacing:.12em;text-transform:uppercase;color:var(--probe,#35b39c)'
        group.textContent = item.label
        menu.appendChild(group)
        item.submenu.forEach((choice) => menu.appendChild(menuButton(choice)))
        return
      }
      menu.appendChild(menuButton(item))
    })
    document.body.appendChild(menu)

    // Flip near a viewport edge so the menu is never clipped.
    const box = menu.getBoundingClientRect()
    const left = x + box.width > window.innerWidth - 8 ? Math.max(8, x - box.width) : x
    const top = y + box.height > window.innerHeight - 8 ? Math.max(8, y - box.height) : y
    menu.style.left = `${left}px`
    menu.style.top = `${top}px`
    return menu
  }

  function menuButton(item) {
    const button = document.createElement('button')
    button.type = 'button'
    button.setAttribute('role', 'menuitem')
    button.textContent = item.label
    button.style.cssText = [
      'display:block', 'width:100%', 'padding:7px 9px', 'border:0', 'border-radius:5px',
      'background:transparent', 'color:' + (item.danger ? 'var(--alarm,#ef7f6a)' : 'inherit'),
      'cursor:pointer', 'font-size:12px', 'text-align:left'
    ].join(';')
    button.addEventListener('mouseenter', () => button.focus())
    button.addEventListener('focus', () => { button.style.background = 'var(--housing-raised,#1f2c29)' })
    button.addEventListener('blur', () => { button.style.background = 'transparent' })
    button.addEventListener('click', () => {
      closeContextMenu()
      item.run()
    })
    return button
  }

  function openContextMenu(event) {
    const match = regionFor(event.target)
    if (!match) return
    event.preventDefault()
    closeContextMenu()
    const items = match.region.items(event, match.host) || []
    if (!items.length) return
    contextMenu = buildMenu(items, event.clientX, event.clientY)
    openRegion = match.region
    openHost = match.host
    const first = contextMenu.querySelector('button')
    if (first) first.focus()
  }

  document.addEventListener('contextmenu', openContextMenu)
  document.addEventListener('pointerdown', (event) => {
    if (contextMenu && !event.target.closest('[data-context-menu]')) closeContextMenu()
  }, true)
  document.addEventListener('htmx:beforeSwap', closeContextMenu)
  document.addEventListener('wheel', closeContextMenu, { passive: true })
  // An open menu owns the keyboard, the same way a focused text field
  // does: while one is up, a global shortcut elsewhere must not also act on
  // the key. Callers ask ownsKey() before running theirs.
  //
  // The question is asked about the event rather than about the menu alone
  // because the Escape that closes a menu must answer "yes" for the rest of
  // its dispatch — otherwise every handler downstream of this one sees a
  // menu that is already gone and treats the dismissal as a second Escape,
  // clearing the selection and cancelling any wire in progress.
  let claimedKey = null

  function ownsKey(event) {
    return Boolean(contextMenu) || claimedKey === event
  }

  document.addEventListener('keydown', (event) => {
    if (!contextMenu) return
    claimedKey = event
    const buttons = Array.from(contextMenu.querySelectorAll('button'))
    const index = buttons.indexOf(document.activeElement)
    if (event.key === 'Escape') {
      event.preventDefault()
      const region = openRegion
      const host = openHost
      closeContextMenu()
      restoreFocusTo(region, host)
    } else if (event.key === 'ArrowDown') {
      event.preventDefault()
      buttons[(index + 1) % buttons.length].focus()
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      buttons[(index - 1 + buttons.length) % buttons.length].focus()
    }
  })

  const namespace = window.ProcessLab || (window.ProcessLab = {})
  namespace.menu = { register, ownsKey }
})()
