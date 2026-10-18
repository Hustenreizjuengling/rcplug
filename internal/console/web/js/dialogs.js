// Modal dialogs (queued, so concurrent requests never clobber each other) and
// the side drawer used for details. A drawer has its own scope, disposed when
// it closes or when the tree is reloaded.
import { $, h } from './dom.js';
import { t } from './i18n.js';
import { on } from './store.js';
import { createScope } from './scope.js';

let queue = Promise.resolve();

// confirmDialog resolves true if the user pressed OK. Enter confirms.
export function confirmDialog(content, okText, onOpen) {
  const run = () => new Promise((resolve) => {
    const d = $('#dlg');
    $('#dlgBody').replaceChildren(typeof content === 'string' ? h('p', null, content) : content);
    $('#dlgOk').textContent = okText || t('ok');
    $('#dlgCancel').textContent = t('cancel');
    d.returnValue = '';
    d.addEventListener('close', () => resolve(d.returnValue === 'ok'), { once: true });
    d.showModal();
    if (onOpen) onOpen();
  });
  const p = queue.then(run, run);
  queue = p.catch(() => {});
  return p;
}

let pinPrompt = null;
// askPin asks once even if several requests need the PIN at the same time.
export function askPin() {
  if (pinPrompt) return pinPrompt;
  const input = h('input', { type: 'password', autocomplete: 'current-password', 'aria-label': t('pin.label'), placeholder: t('pin.label') });
  pinPrompt = confirmDialog(h('div', null, h('p', null, t('pin.prompt')), input), t('pin.send'), () => input.focus())
    .then((ok) => (ok ? input.value : ''))
    .finally(() => { pinPrompt = null; });
  return pinPrompt;
}

let drawerScope = null;
let drawerBuild = null;

// openDrawer(title, scope => Node) shows details; build is re-run when the
// view asks for a re-render.
export function openDrawer(title, build) {
  closeDrawerScope();
  drawerBuild = build;
  $('#drawerTitle').textContent = title;
  renderDrawer();
  const d = $('#drawer');
  if (!d.open) d.showModal();
}

function renderDrawer() {
  closeDrawerScope();
  drawerScope = createScope(renderDrawer);
  $('#drawerBody').replaceChildren(drawerBuild(drawerScope));
}

function closeDrawerScope() {
  if (drawerScope) { drawerScope.dispose(); drawerScope = null; }
}

export function closeDrawer() {
  const d = $('#drawer');
  if (d.open) d.close();
}

export function initDialogs() {
  $('#drawerClose').addEventListener('click', closeDrawer);
  $('#drawer').addEventListener('close', () => { closeDrawerScope(); drawerBuild = null; });
  $('#dlgCancel').addEventListener('click', () => $('#dlg').close('cancel'));
  // Paths shown in a drawer are only valid for the tree they were built for;
  // within the same epoch the drawer is rebuilt so it shows the new values
  // (a toggle computes its next value from the tree).
  on('reloaded', ({ epochChanged }) => {
    if (epochChanged) closeDrawer();
    else if (drawerBuild && $('#drawer').open) renderDrawer();
  });
}
