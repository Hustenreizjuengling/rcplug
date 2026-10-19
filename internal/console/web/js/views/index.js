// The view registry: the navigation is generated from this list. A view is
// {id, title (translation key), render(scope, rerender) → Node[]}; add one
// here to get a new entry. tools holds what most people never need (shown
// last and quieter).
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

// Their icons in ui/icons.svg (sdk/ui).
export const icons = { mixer: 'mixer', routing: 'routing', inputs: 'mic', effects: 'effects', outputs: 'headphones', pads: 'pads', recorder: 'record', system: 'chip', explorer: 'tree', audit: 'list' };
