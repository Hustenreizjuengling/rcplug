// Explorer: the complete state tree, searchable, live.
import { h, $, $$, fmtValue } from '../dom.js';
import { t } from '../i18n.js';
import { store, nodeAt, famIndex } from '../store.js';
import { propsTable } from './parts.js';

let selected = [];
let query = '';

function subTree(n, path, ord) {
  const text = n.t + (ord !== null ? ' #' + ord : '');
  if (!n.c.length) return h('a', { class: 'nd', href: '#explorer', 'data-path': path.join('.') }, text, ' ', h('span', { class: 'cnt' }, t('explorer.props', { n: Object.keys(n.p).length })));
  const d = h('details', null, h('summary', null, text, ' ', h('span', { class: 'cnt' }, t('explorer.children', { n: n.c.length }))));
  d.addEventListener('toggle', () => { if (d.open && !d._built) { d._built = true; d.append(buildTree(n, path)); } });
  return d;
}

function buildTree(n, path) {
  const frag = h('div');
  frag.append(h('a', { class: 'nd', href: '#explorer', 'data-path': path.join('.') }, path.length ? n.t : t('explorer.root', { t: n.t }), ' ', h('span', { class: 'cnt' }, t('explorer.props', { n: Object.keys(n.p).length }))));
  for (let i = 0; i < n.c.length;) {
    let j = i + 1;
    while (j < n.c.length && n.c[j].t === n.c[i].t) j++;
    if (j - i > 1) {
      const d = h('details', null, h('summary', null, n.c[i].t, ' ', h('span', { class: 'cnt' }, '×' + (j - i))));
      for (let k = i; k < j; k++) d.append(subTree(n.c[k], [...path, k], k - i));
      frag.append(d);
    } else frag.append(subTree(n.c[i], [...path, i], null));
    i = j;
  }
  return frag;
}

export const explorer = {
  id: 'explorer',
  title: 'tab.explorer',
  render(scope) {
    const tree = h('nav', { class: 'etree', 'aria-label': t('explorer.tree') });
    const search = h('input', { type: 'search', placeholder: t('explorer.search'), value: query, 'aria-label': t('explorer.search'), style: 'width:100%;margin-bottom:8px' });
    const results = h('div', { 'aria-live': 'polite' });
    tree.append(search, results, buildTree(store.root, []));
    const props = h('div', { class: 'eprops' });
    let propScope = null;
    scope.onDispose(() => propScope && propScope.dispose());
    const show = (path) => {
      selected = path;
      $$('.nd.sel', tree).forEach((e) => e.classList.remove('sel'));
      const a = $(`.nd[data-path="${path.join('.')}"]`, tree);
      if (a) a.classList.add('sel');
      const n = nodeAt(path);
      if (propScope) propScope.dispose();
      propScope = scope.child();
      if (!n) { props.replaceChildren(h('p', { class: 'muted' }, t('explorer.notFound'))); return; }
      const fam = Object.keys(store.L.runs).find((k) => !k.includes('/') && famIndex(k, path) >= 0);
      const names = Object.keys(n.p);
      props.replaceChildren(
        h('h3', { style: 'margin:0 0 4px' }, n.t, fam ? ' #' + famIndex(fam, path) : ''),
        h('div', { class: 'muted small mono', style: 'margin-bottom:10px' }, t('explorer.pathInfo', { path: path.join(', '), props: names.length, children: n.c.length })),
        names.length ? propsTable(propScope, path, names) : h('p', { class: 'muted' }, path.length ? t('explorer.noProps') : t('explorer.rootHint', { n: n.c.length })));
    };
    tree.addEventListener('click', (e) => {
      const a = e.target.closest('.nd');
      if (a) { e.preventDefault(); show(a.dataset.path === '' ? [] : a.dataset.path.split('.').map(Number)); }
    });
    const doSearch = () => {
      query = search.value.trim();
      results.replaceChildren();
      if (query.length < 2) return;
      const q = query.toLowerCase();
      const hits = [];
      const walk = (n, path) => {
        for (const [k, v] of Object.entries(n.p)) {
          if (hits.length >= 200) return;
          if (k.toLowerCase().includes(q) || (typeof v === 'string' && v.toLowerCase().includes(q)) || n.t.toLowerCase() === q) hits.push([path, n.t, k, v]);
        }
        n.c.forEach((c, i) => walk(c, [...path, i]));
      };
      walk(store.root, []);
      results.append(h('div', { class: 'small muted', style: 'margin:4px 0' }, t('explorer.hits', { n: hits.length + (hits.length >= 200 ? '+' : '') })));
      for (const [path, type, k, v] of hits) results.append(h('a', { class: 'nd', href: '#explorer', 'data-path': path.join('.') }, h('span', { class: 'cnt' }, type + ' '), k, h('span', { class: 'cnt' }, ' = ' + String(fmtValue(v)).slice(0, 30))));
    };
    let timer = 0;
    search.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(doSearch, 250); });
    if (query) doSearch();
    scope.timeout(() => show(selected.length && nodeAt(selected) ? selected : []), 0);
    return [h('div', { class: 'explorer' }, tree, props), h('p', { class: 'muted small' }, t('explorer.note'))];
  },
};
