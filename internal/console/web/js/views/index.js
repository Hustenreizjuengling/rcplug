// The view registry: the navigation is generated from this list. A view is
// {id, title (translation key), render(scope, rerender) → Node[]}; add one
// here to get a new tab.
import { mixer } from './mixer.js';
import { routing } from './routing.js';
import { inputs } from './inputs.js';
import { outputsView } from './outputs.js';
import { effects } from './effects.js';
import { pads } from './pads.js';
import { recorder } from './recorder.js';
import { system } from './system.js';
import { display } from './display.js';
import { explorer } from './explorer.js';
import { audit } from './audit.js';

export const views = [mixer, routing, inputs, outputsView, effects, pads, recorder, system, display, explorer, audit];
