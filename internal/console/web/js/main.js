// Entry point of the console UI.
import { initDialogs } from './dialogs.js';
import { initShell, renderHeader, render } from './shell.js';
import { connect, liveState } from './live.js';
import { reload, store } from './store.js';
import { onActionsChanged, startActions } from './plugins.js';

initDialogs();
initShell();
connect({ onStatus: renderHeader, onState: renderHeader });
reload();
onActionsChanged(render);
startActions();

// For debugging in the browser console and for UI tests.
window.__rcplug = { store, live: liveState };
