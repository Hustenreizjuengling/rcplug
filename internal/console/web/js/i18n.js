// Translations. The UI language follows the browser (German if it prefers
// German, English otherwise) and can be switched in the header; the choice is
// shared with the rcplug shell (same localStorage key).
import de from './i18n/de.js';
import en from './i18n/en.js';

const dicts = { de, en };
const KEY = 'rcplug-lang';

function detect() {
  try {
    const saved = localStorage.getItem(KEY);
    if (saved && dicts[saved]) return saved;
  } catch (e) { /* storage blocked */ }
  return (navigator.language || 'en').toLowerCase().startsWith('de') ? 'de' : 'en';
}

let lang = detect();
document.documentElement.lang = lang;

export const getLang = () => lang;

export function setLang(l) {
  if (!dicts[l]) return;
  try { localStorage.setItem(KEY, l); } catch (e) { /* ignore */ }
  location.reload();
}

// t(key, {name: 'x'}) looks the key up in the active dictionary, falls back
// to English, then to the key itself; {name} placeholders are substituted.
export function t(key, params) {
  let s = dicts[lang][key] ?? dicts.en[key] ?? key;
  if (params) s = s.replace(/\{(\w+)\}/g, (m, k) => (k in params ? params[k] : m));
  return s;
}

// label('src', 'combo1', 'Mic 1') translates an entity id, falling back to the
// server's English label.
export function label(kind, id, fallback) {
  return dicts[lang][kind + '.' + id] ?? fallback ?? id;
}

// errorText turns an API error into a sentence in the UI language. Codes whose
// server message carries details (which property, which range) keep them via
// the {msg} placeholder.
export function errorText(err) {
  const msg = (err && err.message) || String(err);
  if (err && err.code && dicts[lang]['err.' + err.code]) return t('err.' + err.code, { msg });
  return msg;
}
