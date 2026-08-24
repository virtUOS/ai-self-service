// Help tooltips.
//
// Positioned in the top layer via the popover API rather than as a CSS
// ::after: the profiles table lives inside an overflow-x scroll container,
// which clips absolutely-positioned children no matter how they are anchored.
(function () {
  let popover = null;

  function ensurePopover() {
    if (popover) return popover;
    popover = document.createElement('div');
    popover.className = 'help-popover';
    popover.popover = 'manual';
    document.body.appendChild(popover);
    return popover;
  }

  function show(trigger) {
    const text = trigger.getAttribute('data-help');
    if (!text) return;

    const el = ensurePopover();
    el.textContent = text;
    el.showPopover();

    // Measure after showing, then keep the box inside the viewport.
    const t = trigger.getBoundingClientRect();
    const p = el.getBoundingClientRect();
    const gap = 8;

    let left = t.left + t.width / 2 - p.width / 2;
    left = Math.max(gap, Math.min(left, window.innerWidth - p.width - gap));

    // Prefer above; flip below when there is not room.
    let top = t.top - p.height - gap;
    if (top < gap) top = t.bottom + gap;

    el.style.left = left + 'px';
    el.style.top = top + 'px';
  }

  function hide() {
    if (popover && popover.matches(':popover-open')) popover.hidePopover();
  }

  function bind(el) {
    el.addEventListener('mouseenter', () => show(el));
    el.addEventListener('mouseleave', hide);
    el.addEventListener('focus', () => show(el));
    el.addEventListener('blur', hide);
    // Touch has no hover; tapping toggles.
    el.addEventListener('click', (e) => {
      e.preventDefault();
      if (popover && popover.matches(':popover-open')) hide();
      else show(el);
    });
  }

  document.querySelectorAll('.help').forEach(bind);
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') hide(); });
  window.addEventListener('scroll', hide, true);
})();
