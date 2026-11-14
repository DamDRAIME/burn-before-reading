// Tiny DOM helpers. Everything is built with createElement and text nodes,
// never innerHTML, so message content can't inject markup.

const SVG_NS = 'http://www.w3.org/2000/svg';

function setProps(el, props) {
  if (!props) return;
  for (const [key, value] of Object.entries(props)) {
    if (value == null || value === false) continue;
    if (key === 'class') {
      el.setAttribute('class', value);
    } else if (key === 'style' && typeof value === 'object') {
      for (const [prop, v] of Object.entries(value)) {
        if (v == null) continue;
        if (prop.startsWith('--')) el.style.setProperty(prop, v);
        else el.style[prop] = v;
      }
    } else if (key.startsWith('on') && typeof value === 'function') {
      el.addEventListener(key.slice(2).toLowerCase(), value);
    } else if (key === 'value' && 'value' in el) {
      el.value = value;
    } else if (value === true) {
      el.setAttribute(key, '');
    } else {
      el.setAttribute(key, String(value));
    }
  }
}

function appendChildren(el, children) {
  for (const child of children.flat(Infinity)) {
    if (child == null || child === false || child === true) continue;
    el.append(child instanceof Node ? child : String(child));
  }
}

/** h('button', {class: 'btn', onClick}, 'Label') */
export function h(tag, props, ...children) {
  const el = document.createElement(tag);
  setProps(el, props);
  appendChildren(el, children);
  return el;
}

/**
 * Replaces el's children, skipping null/false like h() does.
 * (Native replaceChildren would print "null" and "false" as text.)
 */
export function fill(el, ...children) {
  el.replaceChildren();
  appendChildren(el, children);
  return el;
}

export function s(tag, props, ...children) {
  const el = document.createElementNS(SVG_NS, tag);
  setProps(el, props);
  appendChildren(el, children);
  return el;
}

/** Concentric rings used as decoration across the pages. */
export function rings(radii, { viewBox = 100, className = '' } = {}) {
  const c = viewBox / 2;
  return s('svg', { class: `rings ${className}`, viewBox: `0 0 ${viewBox} ${viewBox}`, 'aria-hidden': 'true', fill: 'none' },
    radii.map((r) => s('circle', { cx: c, cy: c, r })));
}

/** A status shape: filled dot, half dot, ring, grey square or triangle. */
export function shape(kind, label) {
  return h('span', label ? { class: `shape shape--${kind}`, role: 'img', 'aria-label': label } : { class: `shape shape--${kind}`, 'aria-hidden': 'true' });
}
