// The view registry: the navigation is generated from this list. A view is
// {id, title (translation key), render(scope, rerender) → Node[]}; add one
// here to get a new entry. tools holds what most people never need (the
// side bar folds it away).
import { mixer } from './mixer.js';
import { routing } from './routing.js';
import { inputs } from './inputs.js';
import { outputsView } from './outputs.js';
import { effects } from './effects.js';
import { pads } from './pads.js';
import { recorder } from './recorder.js';
import { system } from './system.js';
import { explorer } from './explorer.js';
import { audit } from './audit.js';

export const views = [mixer, routing, inputs, effects, outputsView, pads, recorder, system, explorer, audit];
export const tools = new Set(['explorer', 'audit']);

// Icons for the side bar (24×24, stroked; a few hundred bytes in all).
export const icons = {
  mixer: 'M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6',
  routing: 'M3 9h18M3 15h18M9 3v18M15 3v18',
  inputs: 'M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3zM19 10v2a7 7 0 0 1-14 0v-2M12 19v3',
  effects: 'M15 4V2M15 16v-2M8 9h2M20 9h2M17.8 11.8 19 13M17.8 6.2 19 5M3 21l9-9M12.2 6.2 11 5',
  outputs: 'M3 18v-6a9 9 0 0 1 18 0v6M21 19a2 2 0 0 1-2 2h-1a2 2 0 0 1-2-2v-3a2 2 0 0 1 2-2h3zM3 19a2 2 0 0 0 2 2h1a2 2 0 0 0 2-2v-3a2 2 0 0 0-2-2H3z',
  pads: 'M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z',
  recorder: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z',
  system: 'M5 5h14v14H5zM9 9h6v6H9zM9 2v3M15 2v3M9 19v3M15 19v3M19 9h3M19 15h3M2 9h3M2 15h3',
  explorer: 'M3 3h6v6H3zM15 15h6v6h-6zM6 9v6a3 3 0 0 0 3 3h6',
  audit: 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01',
};
