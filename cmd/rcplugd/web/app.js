// rcplug's dashboard: the side bar with the device and its pages, the
// plugins' own pages (framed), the plugin list with the catalog, and the
// System pages (overview, processes, updates, access, audio and events,
// help). Plain JavaScript, no build step; the look is ui/rcplug.css.
'use strict';
const T = {
  en: {
    connecting: 'connecting…', offline: 'The device does not answer', cpu: 'CPU', load: 'load', ramFree: 'RAM free', up: 'up for',
    storage: 'Storage', storageFree: 'Storage free',
    plugins: 'Plugins', system: 'System', rescan: 'Rescan', rescanHint: 'Read the plugins folder again', skipped: 'Skipped plugin folders:',
    none: 'No plugins installed yet.', noneHint: 'Install one from the catalog below.', open: 'Open', newTab: 'Open in a new tab',
    stop: 'Stop', restart: 'Restart', start: 'Start', log: 'Log', restarts: 'restarts: {n}', startpage: 'start page', audio: 'audio',
    remove: 'Remove', rmQ: 'Remove “{name}” from the device? It stops, and its files are deleted.', rmPurge: 'Also delete its data, settings and log',
    rmGo: 'Remove', rmCancel: 'Cancel',
    pluginsLead: 'What runs on this RØDECaster. Plugins with pages appear in the side bar.', pages: 'Pages', manage: 'Manage',
    catTitle: 'More plugins', catLoad: 'Show the catalog', catReload: 'Look again', catLoading: 'Loading…',
    catHint: 'The catalog lists plugins you can install from here. Showing it asks GitHub for the latest signed releases of the catalogs below.',
    catOff: 'Updates are switched off on this device, and with them the catalog.', catEmpty: 'The catalogs offer no plugins.',
    catOfficial: 'official', catFrom: 'from {repo}', catInstalled: 'installed ({v})', catInstall: 'Install',
    catConfirm: 'Install “{name}” {v} from {repo}?\n\nPlugins run with full rights on the device: install only what you trust.',
    catConfirmPerms: 'It declares:', catConfirmNoPerms: 'It declares no permissions: it only shows its own pages.',
    catDone: 'Installed: it is in the list above now.', catFailed: 'Installing failed: {err}',
    catSources: 'Catalogs', catSourcesHint: 'The official catalog holds the plugins published with rcplug. Add other catalogs only from people you trust, since their plugins run with full rights on the device. A catalog is a GitHub repository with signed releases; you need the public key it is signed with.',
    catSrcRelease: 'release {v}', catSrcPlugins: 'plugins: {n}', catSrcKey: 'key {id}', catRepo: 'GitHub repository (owner/name)', catKey: 'Public key (base64)',
    catAdd: 'Add', catRemoveSrc: 'Remove', catRemoveSrcQ: 'Remove the catalog {repo}? Plugins installed from it stay.',
    perms: 'Permissions:', 'perm_desk:write': 'changes the desk', perm_audio: 'hears the desk', perm_network: 'network', perm_device: 'device', perm_screen: 'takes over the screen',
    'permHint_desk:write': 'May change the mixer, the recorder and the pads through the console, without the PIN and whatever control level the console has; write protection stops it.',
    permHint_audio: 'May listen to the desk\'s inputs and outputs through the shared audio taps.',
    permHint_network: 'Talks to other machines on the network or the internet.',
    permHint_device: 'Uses the device\'s hardware or files outside its own directory.',
    permHint_screen: 'Shows its own pages on the device\'s screen while it runs: RØDE\'s screen is stopped meanwhile and comes back when the plugin stops. It never starts by itself.',
    unknown: 'unknown plugin.json fields: {f}', notFound: 'Plugin not found.', isState: 'The plugin is {state}', autoRestart: 'It is restarted automatically…',
    failed: 'Action failed: {err}', emptyLog: '(empty)', noLog: '(no log)', footer: 'rcplug – add-ons for the RØDECaster, without new firmware.',
    st_running: 'running', st_starting: 'starting', st_backoff: 'restarting…', st_stopped: 'stopped',
    sysTitle: 'System', sysDesc: 'The device and rcplug at a glance.', hostname: 'Hostname', arch: 'Architecture', temp: 'CPU temperature',
    loadAvg: 'Load (1/5/15 min)', memory: 'Memory', disk: 'Storage (rcplug)', devUp: 'Device up for', hostUp: 'rcplug up for',
    version: 'rcplug version', clock: 'Device time', memUsed: '{used} of {total} MB used', diskFree: '{free} MB free of {total} MB',
    sysServices: 'RØDE services', svcTitle: 'RØDE services', svcDesc: 'Some of RØDE\'s built-in services can be switched off if you never use them. A switched-off service does not start any more, also after a restart. No file of RØDE\'s is changed: a firmware update or removing rcplug switches everything on again.',
    svcOn: 'on', svcOff: 'off', svcNotRunning: 'on, not running', svcUnavailable: 'not available', svcTurnOff: 'Switch off', svcTurnOn: 'Switch on',
    svcCost: 'Now: CPU {cpu} %, own RAM {ram} MB', svcPorts: 'Open ports: {list}', svcOutside: 'Connections to the internet: {n}', svcNoPorts: 'No open ports',
    svcWithout: 'Without it: {what}', svcConfirmOff: 'Switch {name} off?\n\nWithout it: {what}\n\nYou can switch it on again here at any time.',
    svcWhy_unknown_version: 'This firmware has another version of it than rcplug knows, so rcplug leaves it on.', svcWhy_not_present: 'Not on this device.',
    svcWhy_unreadable: 'Its starter cannot be read.', svcWhy_not_applied: 'Switched off, takes effect at the next start of the device.',
    svc_callme: 'CallMe', svc_callme_what: 'RØDE\'s remote calls over the internet (SIP/Opus): CallMe calls, hosting and Quick Dial.', svc_callme_off: 'no CallMe calls, no hosting, no Quick Dial; the CallMe channels stay silent.',
    svc_rpcbind: 'rpcbind', svc_rpcbind_what: 'A helper for network file systems (NFS). The Duo does not use it, but it listens on port 111.', svc_rpcbind_off: 'nothing you would notice.',
    svc_modemmanager: 'ModemManager', svc_modemmanager_what: 'Manages mobile network modems. The Duo has none.', svc_modemmanager_off: 'nothing you would notice.',
    svcScreen: 'The device\'s screen is with the plugin {name}; RØDE\'s screen comes back when it stops.',
    sysOverview: 'Overview', sysProcs: 'Processes', sysUpdates: 'Updates', sysAccess: 'Access', sysAudio: 'Audio & events', sysHelp: 'Help',
    sysDevice: 'Device', statLoad: 'Load', statMem: 'Memory used', statDisk: 'Storage free', statUp: 'Device up for', statTemp: 'CPU temperature',
    disclaimer: 'rcplug is unofficial software, not affiliated with or endorsed by RØDE Microphones. RØDE and RØDECaster are trademarks of their owner. Use at your own risk.',
    diagTitle: 'Diagnostics', diagText: 'If something does not work: download this bundle (logs, versions, device state; passwords and keys masked) and attach it to a bug report. Review it before posting it publicly.',
    diagButton: 'Download diagnostics', diagView: 'Look at it in the browser', diagViewHint: 'Opens the file on the toolkit\'s page, read only in your browser: problems, resources over time, and what to review before sharing',
    updTitle: 'Updates', updDesc: 'rcplug and plugins update from signed releases on GitHub. Nothing is downloaded until you check, nothing is installed until you click. Settings and plugin data are kept.',
    updOff: 'Updates are switched off on this device (RCPLUG_UPDATES=off or --updates=false).', updCheck: 'Check for updates', updChecking: 'checking…',
    updNever: 'Not checked yet.', updChecked: 'Last checked: {at}', updCurrent: 'up to date', updAvail: 'update available',
    updInstall: 'Install {v}', updNotes: 'Release notes', updRelease: 'Release page', updNoTargets: 'Nothing to check.',
    updConfirmHost: 'Update rcplug to {v}?\n\nrcplug and all plugins restart (about half a minute). The RØDECaster itself and its audio keep running. If the new version does not start, the previous one comes back automatically.',
    updConfirmPlugin: 'Update the plugin “{name}” to {v}?\n\nThe plugin restarts; its settings and data are kept.',
    unTitle: 'Remove rcplug', unDesc: 'Stops rcplug and every plugin and removes them from the device, with all settings and plugin data. The RØDECaster itself keeps working as before. To install rcplug again you need the toolkit on a computer.',
    unWord: 'remove', unWordHint: 'type “remove”', unTypeWord: 'Type “{w}” first.', unButton: 'Remove rcplug',
    unConfirm: 'Really remove rcplug and all plugin data from the device?', unDone: 'rcplug is being removed', unDoneText: 'In a few seconds rcplug and its plugins are gone from the device. This page no longer works.',
    procTitle: 'Processes', procDesc: 'What runs on the device, the busiest first.',
    procExplain: 'CPU is the share of the whole device over the last seconds, so the processes add up to the total. Own RAM is memory only the process holds; together it is about the “programs” part of the memory. Shared RAM (program files, shared memory) counts in every process that uses it and does not add up. Tags mark rcplug and its plugins, and RØDE\'s own programs.',
    procWhat: 'What the numbers mean',
    procByCPU: 'by CPU', procByMem: 'by RAM', procName: 'Process', procOwn: 'own RAM', procShared: 'shared', procThreads: 'Threads', procNone: 'Not available.',
    procOwnHint: 'Memory only this process holds (RssAnon)', procSharedHint: 'Program files and shared memory this process maps (RssFile + RssShmem), also counted in every other process that maps them',
    procCPUNow: 'CPU {total} % of the device', procCPUParts: 'programs {user} % · kernel {system} % · interrupts {irq} % · waiting for storage {io} % · {cores} cores',
    procKernel: 'kernel threads ({n})',
    procFoot: '{n} processes (without kernel threads). Their own RAM together: {own} MB; the kernel counts {programs} MB for programs. The first CPU values appear after a few seconds.',
    memParts: 'programs {p} · shared memory {s} · kernel {k} · file cache {c} · free {f} MB',
    memPartsHint: 'Programs: the processes\' own memory. Shared memory: shared memory segments and tmpfs (/tmp). Kernel: memory the kernel keeps for itself. File cache: copies of files, freed when programs need the memory, so it does not count as used.',
    tapTitle: 'Audio taps',
    tapDesc: 'One bridge from the desk\'s audio graph (JACK) to the plugins that need sound, such as Sonar. It runs only while a plugin listens and costs about 1 % of the device\'s CPU.',
    tapRunning: 'Running: {n} channel(s) in use', tapIdle: 'Ready; the bridge starts when a plugin asks for audio.', tapProblems: '{n} dropout(s) reported',
    tapNoJack: 'JACK is not reachable here: {err}', tapPorts: '{n} ports can be tapped.',
    evTitle: 'Events', evDesc: 'What the desk, the host and the plugins report on the event bus, live (the last 30).', evNone: 'No events yet.',
    accTitle: 'Access', accDesc: 'An optional password for the dashboard and every plugin page. Without one, everyone on the network can use them.',
    accOn: 'Protected', accOff: 'No password', accCurrent: 'Current password', accNew: 'New password', accRepeat: 'Repeat',
    accSet: 'Set password', accChange: 'Change password', accRemove: 'Remove password', accLogout: 'Log out', accMismatch: 'The two new passwords differ.',
    accRemoveConfirm: 'Remove the password? Everyone on the network can then use the dashboard again.',
    accNote: 'At least 6 characters. Plugins on the device itself are not asked for it. Forgotten? “Forgot the password?” on the login page removes it with four presses of Listen 1 at the device.',
    updAuto: 'Look for updates every day (only asks GitHub; nothing is installed without your click)', updBadgeNav: '{n} update(s) available',
    updPhaseDownload: 'Downloading (slowly on purpose, so the USB audio is not disturbed)', updPhaseUnpack: 'Unpacking', updAmount: '{done} of {total} MB · {pct} %', updEta: 'about {s} s left',
    updRunning: 'Installing {name} {v}…', updRestarting: 'rcplug {v} is being installed and restarts now. This page reloads by itself when it is back.',
    updDone: '{name} {v} installed.', updFailed: 'The update failed: {err}', updLog: 'Details',
    updLastOk: 'Last rcplug update{v}: successful ({at}).', updLastFail: 'Last rcplug update{v} failed ({at}); the previous version was kept or restored.',
    updKeys: 'Trusted signing keys: {ids}', updBadge: 'update {v}', updPlugin: 'Update',
    days: '{d} d {h} h', hours: '{h} h {m} min', mins: '{m} min', switchLang: 'Deutsch', menu: 'Menu', closeMenu: 'Close the menu',
    settings: 'Settings', optSave: 'Save', optSaveRestart: 'Save and restart', optSaved: 'Saved.', optSavedRestart: 'Saved; the plugin restarts.',
    optDefault: 'default: {v}', optReset: 'use the default', optYes: 'on', optNo: 'off', optHint: 'Changes take effect when the plugin starts.',
  },
  de: {
    connecting: 'verbinde…', offline: 'Das Gerät antwortet nicht', cpu: 'CPU', load: 'Last', ramFree: 'RAM frei', up: 'an seit',
    storage: 'Speicher', storageFree: 'Speicher frei',
    plugins: 'Plugins', system: 'System', rescan: 'Neu einlesen', rescanHint: 'Plugin-Ordner neu einlesen', skipped: 'Übersprungene Plugin-Ordner:',
    none: 'Noch keine Plugins installiert.', noneHint: 'Installiere eines aus dem Katalog unten.', open: 'Öffnen', newTab: 'In neuem Tab öffnen',
    stop: 'Stoppen', restart: 'Neu starten', start: 'Starten', log: 'Log', restarts: 'Neustarts: {n}', startpage: 'Startseite', audio: 'Audio',
    remove: 'Entfernen', rmQ: '„{name}“ vom Gerät entfernen? Es wird gestoppt, und seine Dateien werden gelöscht.', rmPurge: 'Auch seine Daten, Einstellungen und das Log löschen',
    rmGo: 'Entfernen', rmCancel: 'Abbrechen',
    pluginsLead: 'Was auf diesem RØDECaster läuft. Plugins mit eigenen Seiten erscheinen in der Seitenleiste.', pages: 'Seiten', manage: 'Verwalten',
    catTitle: 'Weitere Plugins', catLoad: 'Katalog anzeigen', catReload: 'Neu laden', catLoading: 'Lädt…',
    catHint: 'Der Katalog zeigt Plugins, die du hier installieren kannst. Zum Anzeigen fragt das Gerät GitHub nach den neuesten signierten Releases der Kataloge unten.',
    catOff: 'Updates sind auf diesem Gerät abgeschaltet und damit auch der Katalog.', catEmpty: 'Die Kataloge bieten keine Plugins an.',
    catOfficial: 'offiziell', catFrom: 'aus {repo}', catInstalled: 'installiert ({v})', catInstall: 'Installieren',
    catConfirm: '„{name}“ {v} aus {repo} installieren?\n\nPlugins laufen mit allen Rechten auf dem Gerät: Installiere nur, was du vertraust.',
    catConfirmPerms: 'Es gibt an:', catConfirmNoPerms: 'Es gibt keine Berechtigungen an: Es zeigt nur seine eigenen Seiten.',
    catDone: 'Installiert: Es steht jetzt oben in der Liste.', catFailed: 'Die Installation ist fehlgeschlagen: {err}',
    catSources: 'Kataloge', catSourcesHint: 'Der offizielle Katalog enthält die Plugins, die mit rcplug veröffentlicht werden. Füge andere Kataloge nur von Leuten hinzu, denen du vertraust, denn ihre Plugins laufen mit allen Rechten auf dem Gerät. Ein Katalog ist ein GitHub-Repository mit signierten Releases; du brauchst den öffentlichen Schlüssel, mit dem es signiert wird.',
    catSrcRelease: 'Release {v}', catSrcPlugins: 'Plugins: {n}', catSrcKey: 'Schlüssel {id}', catRepo: 'GitHub-Repository (owner/name)', catKey: 'Öffentlicher Schlüssel (base64)',
    catAdd: 'Hinzufügen', catRemoveSrc: 'Entfernen', catRemoveSrcQ: 'Den Katalog {repo} entfernen? Daraus installierte Plugins bleiben.',
    perms: 'Berechtigungen:', 'perm_desk:write': 'ändert das Pult', perm_audio: 'hört das Pult', perm_network: 'Netzwerk', perm_device: 'Gerät', perm_screen: 'übernimmt den Bildschirm',
    'permHint_desk:write': 'Darf über die Konsole Mixer, Recorder und Pads ändern, ohne PIN und unabhängig von der Steuerungsstufe der Konsole; der Schreibschutz hält es auf.',
    permHint_audio: 'Darf die Ein- und Ausgänge des Pults über die gemeinsamen Audio-Abgriffe hören.',
    permHint_network: 'Spricht mit anderen Rechnern im Netzwerk oder im Internet.',
    permHint_device: 'Nutzt die Hardware des Geräts oder Dateien außerhalb seines eigenen Verzeichnisses.',
    permHint_screen: 'Zeigt eigene Seiten auf dem Bildschirm des Geräts, solange es läuft: RØDEs Bildschirm ist so lange aus und kommt zurück, wenn das Plugin stoppt. Startet nie von selbst.',
    unknown: 'unbekannte plugin.json-Felder: {f}', notFound: 'Plugin nicht gefunden.', isState: 'Das Plugin ist gerade {state}', autoRestart: 'Es wird automatisch neu gestartet…',
    failed: 'Aktion fehlgeschlagen: {err}', emptyLog: '(leer)', noLog: '(kein Log)', footer: 'rcplug – Funktionen für den RØDECaster ohne neue Firmware.',
    st_running: 'läuft', st_starting: 'startet', st_backoff: 'Neustart…', st_stopped: 'gestoppt',
    sysTitle: 'System', sysDesc: 'Das Gerät und rcplug auf einen Blick.', hostname: 'Hostname', arch: 'Architektur', temp: 'CPU-Temperatur',
    loadAvg: 'Last (1/5/15 min)', memory: 'Arbeitsspeicher', disk: 'Speicherplatz (rcplug)', devUp: 'Gerät läuft seit', hostUp: 'rcplug läuft seit',
    version: 'rcplug-Version', clock: 'Uhrzeit (Gerät)', memUsed: '{used} / {total} MB belegt', diskFree: '{free} MB frei von {total} MB',
    sysServices: 'RØDE-Dienste', svcTitle: 'RØDE-Dienste', svcDesc: 'Einige eingebaute Dienste von RØDE lassen sich ausschalten, wenn du sie nie benutzt. Ein ausgeschalteter Dienst startet nicht mehr, auch nicht nach einem Neustart. Keine Datei von RØDE wird verändert: Ein Firmware-Update oder das Entfernen von rcplug schaltet alles wieder ein.',
    svcOn: 'an', svcOff: 'aus', svcNotRunning: 'an, läuft nicht', svcUnavailable: 'nicht verfügbar', svcTurnOff: 'Ausschalten', svcTurnOn: 'Einschalten',
    svcCost: 'Gerade: CPU {cpu} %, eigener RAM {ram} MB', svcPorts: 'Offene Ports: {list}', svcOutside: 'Verbindungen ins Internet: {n}', svcNoPorts: 'Keine offenen Ports',
    svcWithout: 'Ohne ihn: {what}', svcConfirmOff: '{name} ausschalten?\n\nOhne ihn: {what}\n\nDu kannst ihn hier jederzeit wieder einschalten.',
    svcWhy_unknown_version: 'Diese Firmware hat eine andere Fassung davon, als rcplug kennt – deshalb bleibt er an.', svcWhy_not_present: 'Gibt es auf diesem Gerät nicht.',
    svcWhy_unreadable: 'Sein Start-Skript lässt sich nicht lesen.', svcWhy_not_applied: 'Ausgeschaltet, wirkt ab dem nächsten Start des Geräts.',
    svc_callme: 'CallMe', svc_callme_what: 'RØDEs Fernanrufe übers Internet (SIP/Opus): CallMe-Anrufe, Hosting und Quick Dial.', svc_callme_off: 'keine CallMe-Anrufe, kein Hosting, kein Quick Dial; die CallMe-Kanäle bleiben stumm.',
    svc_rpcbind: 'rpcbind', svc_rpcbind_what: 'Ein Hilfsdienst für Netzwerk-Dateisysteme (NFS). Der Duo nutzt ihn nicht, aber er lauscht auf Port 111.', svc_rpcbind_off: 'nichts, was du merken würdest.',
    svc_modemmanager: 'ModemManager', svc_modemmanager_what: 'Verwaltet Mobilfunk-Modems. Der Duo hat keins.', svc_modemmanager_off: 'nichts, was du merken würdest.',
    svcScreen: 'Der Bildschirm des Geräts ist beim Plugin {name}; RØDEs Bildschirm kommt zurück, wenn es stoppt.',
    sysOverview: 'Übersicht', sysProcs: 'Prozesse', sysUpdates: 'Updates', sysAccess: 'Zugang', sysAudio: 'Audio & Ereignisse', sysHelp: 'Hilfe',
    sysDevice: 'Gerät', statLoad: 'Last', statMem: 'Arbeitsspeicher belegt', statDisk: 'Speicherplatz frei', statUp: 'Gerät läuft seit', statTemp: 'CPU-Temperatur',
    disclaimer: 'rcplug ist inoffizielle Software und steht in keiner Verbindung zu RØDE Microphones. RØDE und RØDECaster sind Marken ihres Inhabers. Nutzung auf eigene Gefahr.',
    diagTitle: 'Diagnose', diagText: 'Wenn etwas nicht funktioniert: dieses Paket herunterladen (Logs, Versionen, Gerätezustand; Passwörter und Schlüssel maskiert) und an einen Fehlerbericht hängen. Vor dem öffentlichen Teilen bitte durchsehen.',
    diagButton: 'Diagnose herunterladen', diagView: 'Im Browser ansehen', diagViewHint: 'Öffnet die Datei auf der Seite des Toolkits, nur in deinem Browser gelesen: Probleme, Ressourcen über die Zeit und was du vor dem Teilen prüfen solltest',
    updTitle: 'Updates', updDesc: 'rcplug und Plugins aktualisieren sich aus signierten Releases auf GitHub. Heruntergeladen wird erst nach „Nach Updates suchen“, installiert erst nach deinem Klick. Einstellungen und Plugin-Daten bleiben erhalten.',
    updOff: 'Updates sind auf diesem Gerät abgeschaltet (RCPLUG_UPDATES=off oder --updates=false).', updCheck: 'Nach Updates suchen', updChecking: 'suche…',
    updNever: 'Noch nicht gesucht.', updChecked: 'Zuletzt gesucht: {at}', updCurrent: 'aktuell', updAvail: 'Update verfügbar',
    updInstall: '{v} installieren', updNotes: 'Versionshinweise', updRelease: 'Release-Seite', updNoTargets: 'Nichts zu prüfen.',
    updConfirmHost: 'rcplug auf {v} aktualisieren?\n\nrcplug und alle Plugins starten neu (etwa eine halbe Minute). Der RØDECaster selbst und sein Audio laufen weiter. Startet die neue Version nicht, kommt automatisch die vorherige zurück.',
    updConfirmPlugin: 'Das Plugin „{name}“ auf {v} aktualisieren?\n\nDas Plugin startet neu; seine Einstellungen und Daten bleiben erhalten.',
    unTitle: 'rcplug entfernen', unDesc: 'Hält rcplug und alle Plugins an und entfernt sie vom Gerät, mit allen Einstellungen und Plugin-Daten. Der RØDECaster selbst arbeitet weiter wie vorher. Zum erneuten Installieren brauchst du das Toolkit auf einem Computer.',
    unWord: 'entfernen', unWordHint: '„entfernen“ eintippen', unTypeWord: 'Bitte zuerst „{w}“ eintippen.', unButton: 'rcplug entfernen',
    unConfirm: 'rcplug und alle Plugin-Daten wirklich vom Gerät entfernen?', unDone: 'rcplug wird entfernt', unDoneText: 'In ein paar Sekunden sind rcplug und seine Plugins vom Gerät verschwunden. Diese Seite funktioniert dann nicht mehr.',
    procTitle: 'Prozesse', procDesc: 'Was auf dem Gerät läuft, das Lebhafteste zuerst.',
    procExplain: 'CPU ist der Anteil am ganzen Gerät über die letzten Sekunden, die Prozesse ergeben zusammen also die Gesamtzahl. „RAM eigen“ ist Arbeitsspeicher, den nur dieser Prozess hält; zusammen ergibt das ungefähr den Posten „Programme“ beim Arbeitsspeicher. Geteilter Speicher (Programmdateien, gemeinsamer Speicher) zählt bei jedem Prozess mit, der ihn nutzt, und lässt sich nicht addieren. Markiert sind rcplug und seine Plugins sowie RØDEs eigene Programme.',
    procWhat: 'Was die Zahlen bedeuten',
    procByCPU: 'nach CPU', procByMem: 'nach RAM', procName: 'Prozess', procOwn: 'RAM eigen', procShared: 'geteilt', procThreads: 'Threads', procNone: 'Nicht verfügbar.',
    procOwnHint: 'Arbeitsspeicher, den nur dieser Prozess hält (RssAnon)', procSharedHint: 'Programmdateien und gemeinsamer Speicher, die dieser Prozess eingeblendet hat (RssFile + RssShmem); zählt auch bei jedem anderen Prozess mit, der sie nutzt',
    procCPUNow: 'CPU {total} % des Geräts', procCPUParts: 'Programme {user} % · Kernel {system} % · Interrupts {irq} % · Warten auf Speicher {io} % · {cores} Kerne',
    procKernel: 'Kernel-Threads ({n})',
    procFoot: '{n} Prozesse (ohne Kernel-Threads). Ihr eigener Arbeitsspeicher zusammen: {own} MB; der Kernel zählt {programs} MB für Programme. Die ersten CPU-Werte erscheinen nach ein paar Sekunden.',
    memParts: 'Programme {p} · gemeinsamer Speicher {s} · Kernel {k} · Datei-Cache {c} · frei {f} MB',
    memPartsHint: 'Programme: der eigene Speicher der Prozesse. Gemeinsamer Speicher: Shared-Memory-Segmente und tmpfs (/tmp). Kernel: Speicher, den der Kernel für sich hält. Datei-Cache: Kopien von Dateien, die freigegeben werden, sobald Programme den Speicher brauchen; er zählt deshalb nicht als belegt.',
    tapTitle: 'Audio-Abgriffe',
    tapDesc: 'Eine Brücke vom Audio-Graph des Pults (JACK) zu den Plugins, die Ton brauchen, etwa Sonar. Sie läuft nur, solange ein Plugin zuhört, und kostet etwa 1 % der CPU des Geräts.',
    tapRunning: 'Läuft: {n} Kanal/Kanäle belegt', tapIdle: 'Bereit; die Brücke startet, sobald ein Plugin Ton anfordert.', tapProblems: '{n} Aussetzer gemeldet',
    tapNoJack: 'JACK ist hier nicht erreichbar: {err}', tapPorts: '{n} Ports können abgegriffen werden.',
    evTitle: 'Ereignisse', evDesc: 'Was Pult, Host und Plugins auf dem Ereignis-Bus melden, live (die letzten 30).', evNone: 'Noch keine Ereignisse.',
    accTitle: 'Zugang', accDesc: 'Ein optionales Passwort für das Dashboard und alle Plugin-Seiten. Ohne Passwort kann jeder im Netzwerk sie benutzen.',
    accOn: 'Geschützt', accOff: 'Kein Passwort', accCurrent: 'Aktuelles Passwort', accNew: 'Neues Passwort', accRepeat: 'Wiederholen',
    accSet: 'Passwort setzen', accChange: 'Passwort ändern', accRemove: 'Passwort entfernen', accLogout: 'Abmelden', accMismatch: 'Die beiden neuen Passwörter sind verschieden.',
    accRemoveConfirm: 'Passwort entfernen? Dann kann wieder jeder im Netzwerk das Dashboard benutzen.',
    accNote: 'Mindestens 6 Zeichen. Plugins auf dem Gerät selbst werden nicht gefragt. Vergessen? „Passwort vergessen?“ auf der Anmeldeseite entfernt es mit viermal Listen 1 am Gerät.',
    updAuto: 'Täglich nach Updates suchen (fragt nur bei GitHub nach; installiert wird erst nach deinem Klick)', updBadgeNav: '{n} Update(s) verfügbar',
    updPhaseDownload: 'Herunterladen (absichtlich langsam, damit der USB-Ton nicht gestört wird)', updPhaseUnpack: 'Entpacken', updAmount: '{done} von {total} MB · {pct} %', updEta: 'noch etwa {s} s',
    updRunning: '{name} {v} wird installiert…', updRestarting: 'rcplug {v} wird installiert und startet jetzt neu. Die Seite lädt sich von selbst neu, sobald es wieder da ist.',
    updDone: '{name} {v} installiert.', updFailed: 'Das Update ist fehlgeschlagen: {err}', updLog: 'Details',
    updLastOk: 'Letztes rcplug-Update{v}: erfolgreich ({at}).', updLastFail: 'Letztes rcplug-Update{v} fehlgeschlagen ({at}); die vorherige Version blieb bzw. wurde wiederhergestellt.',
    updKeys: 'Vertrauenswürdige Signaturschlüssel: {ids}', updBadge: 'Update {v}', updPlugin: 'Aktualisieren',
    days: '{d} T {h} h', hours: '{h} h {m} min', mins: '{m} min', switchLang: 'English', menu: 'Menü', closeMenu: 'Menü schließen',
    settings: 'Einstellungen', optSave: 'Speichern', optSaveRestart: 'Speichern und neu starten', optSaved: 'Gespeichert.', optSavedRestart: 'Gespeichert; das Plugin startet neu.',
    optDefault: 'Standard: {v}', optReset: 'Standard verwenden', optYes: 'an', optNo: 'aus', optHint: 'Änderungen wirken, wenn das Plugin startet.',
  },
};
let lang = 'en';
try { lang = localStorage.getItem('rcplug-lang') || ((navigator.language || '').toLowerCase().startsWith('de') ? 'de' : 'en'); } catch (e) { /* ignore */ }
if (!T[lang]) lang = 'en';
document.documentElement.lang = lang;
const t = (k, p) => (T[lang][k] ?? T.en[k] ?? k).replace(/\{(\w+)\}/g, (m, n) => (p && n in p ? p[n] : m));

const $ = (s, r = document) => r.querySelector(s);
const dec = (s) => { try { return decodeURIComponent(s); } catch (e) { return s; } };
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const ico = (id, cls = '') => `<svg class="i ${cls}" aria-hidden="true"><use href="ui/icons.svg#${id}"/></svg>`;
// A session that ran out (or a password set meanwhile) leads to the login.
function toLogin(r) {
  if (r.status === 401) { location.href = '/login'; return true; }
  return false;
}
async function jget(u) { const r = await fetch(u, { cache: 'no-store' }); if (toLogin(r)) throw new Error('login'); if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); }
async function jpost(u, body) {
  const init = { method: 'POST', headers: { 'X-Rcplug': '1' } };
  if (body !== undefined) { init.headers['Content-Type'] = 'application/json'; init.body = JSON.stringify(body); }
  const r = await fetch(u, init);
  if (toLogin(r)) throw new Error('login');
  if (!r.ok) {
    const text = (await r.text()).trim();
    let msg = text;
    try { msg = JSON.parse(text).error || text; } catch (e) { /* plain text */ }
    throw new Error(msg || 'HTTP ' + r.status);
  }
  return r.json();
}
function toast(msg, bad) {
  const el = document.createElement('div');
  el.className = 'toast' + (bad ? ' bad' : '');
  el.textContent = msg;
  $('#toasts').append(el);
  setTimeout(() => el.remove(), 4200);
}

let plugins = [], invalid = [], sys = null, route = '', loaded = false, pageSig = '';
let upd = null, updSig = '', updChecking = false, bootVersion = null;
const openLogs = {};
const openOpts = {}; // plugin → the values being edited (kept across re-renders)
const openRemove = {}; // plugin → {purge} while its remove box is open
let cat = null, catSig = '', catLoading = false, catSourcesOpen = false, navSig = '';
const catDraft = { repo: '', key: '' }; // what is typed into the source form survives its rebuilds

function fmtUptime(s) {
  const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60);
  return d ? t('days', { d, h }) : h ? t('hours', { h, m }) : t('mins', { m });
}
const mb = (kb) => Math.round(kb / 1024);
// Megabytes as "512 MB" or, from a gigabyte on, "1.5 GB".
const mbLabel = (n) => (n >= 1024 ? `${(n / 1024).toFixed(1)} GB` : `${Math.round(n)} MB`);

async function refresh() {
  try {
    sys = await jget('api/system');
    // After an update the new rcplug may serve a new dashboard.
    if (bootVersion === null) bootVersion = sys.version;
    else if (sys.version !== bootVersion) { location.reload(); return; }
    document.body.classList.remove('offline');
  } catch (e) {
    sys = null;
    document.body.classList.add('offline'); // the plugins' states are unknown now
  }
  renderHealth();
  try { const d = await jget('api/plugins'); plugins = d.plugins || []; invalid = d.invalid || []; loaded = true; } catch (e) { /* keep last */ }
  await loadUpdates(); // local; also for the update badges in the side bar
  renderNav();
  if (!route) go();
  else if (route === 'plugins') {
    await loadCatalog();
    if (route !== 'plugins') return; // moved on meanwhile
    renderPlugins(); renderCatalog(); updateProgress();
  }
  else if (route.startsWith('system')) renderSystem();
  else if (route.startsWith('p/')) checkFrame();
}

// ---- the side bar
function renderHealth() {
  const el = $('#health');
  $('#mdot').className = 'dot ' + (sys ? 'ok' : 'danger');
  if (!sys) { el.innerHTML = `<div class="off">${ico('alert', 'i-sm')}${esc(t('offline'))}</div>`; return; }
  $('#host').textContent = sys.host || 'RØDECaster';
  $('#ver').textContent = 'rcplug ' + sys.version;
  const rows = [];
  if (sys.memTotalKB) {
    const used = 1 - sys.memAvailableKB / sys.memTotalKB;
    rows.push(`<div class="row" title="${esc(t('ramFree'))}: ${esc(mb(sys.memAvailableKB))} MB"><span>RAM</span><b>${esc((used * 100).toFixed(0))} %</b><div class="bar${used > 0.85 ? ' warn' : ''}"><i style="width:${(used * 100).toFixed(0)}%"></i></div></div>`);
  }
  if (sys.diskTotalMB > 0) {
    const used = 1 - sys.diskFreeMB / sys.diskTotalMB;
    rows.push(`<div class="row" title="${esc(t('storageFree'))}: ${esc(mbLabel(sys.diskFreeMB))}"><span>${esc(t('storage'))}</span><b>${esc((used * 100).toFixed(0))} %</b><div class="bar${used > 0.85 ? ' warn' : ''}"><i style="width:${(used * 100).toFixed(0)}%"></i></div></div>`);
  }
  const bits = [];
  if (sys.cpuTempC !== undefined) bits.push(`${esc(t('cpu'))} <b>${esc(sys.cpuTempC.toFixed(0))} °C</b>`);
  if (sys.load) bits.push(`${esc(t('load'))} <b>${esc(sys.load[0])}</b>`);
  if (bits.length) rows.push(`<div>${bits.join(' · ')}</div>`);
  if (sys.systemUptimeSec !== undefined) rows.push(`<div>${esc(t('up'))} <b>${esc(fmtUptime(sys.systemUptimeSec))}</b></div>`);
  el.innerHTML = rows.join('');
}

const uiPlugins = () => plugins.filter((p) => p.hasUI);
// Localised manifest texts (plugin.json "translations"), English otherwise.
const tr = (p, k) => (p.translations && p.translations[lang] && p.translations[lang][k]) || p[k] || '';
// Rcplug's own plugins get an icon from the set; others show their glyph.
const ICONS = { console: 'mixer', sonar: 'radar', backup: 'archive' };
const glyph = (p) => (ICONS[p.name] ? ico(ICONS[p.name]) : `<span aria-hidden="true">${esc(p.icon || '•')}</span>`);
const stateDot = (st) => ({ running: 'ok', starting: 'info', backoff: 'warn' }[st] || '');

function defaultRoute() {
  const ui = uiPlugins();
  const primary = ui.find((p) => p.primary && p.enabled) || ui.find((p) => p.enabled);
  return primary ? 'p/' + primary.name : 'plugins';
}

// A plugin route is p/<name>, optionally followed by a path or query inside
// the plugin's UI (p/sonar?strip=physical1), e.g. from a console action.
function frameRoute() {
  const rest = route.slice(2);
  const i = rest.search(/[/?]/);
  const name = dec(i < 0 ? rest : rest.slice(0, i));
  let sub = i < 0 ? '' : rest.slice(i).replace(/^\//, '');
  // Stay inside the plugin: no "." or ".." segment, also not percent-encoded
  // (browsers resolve %2e%2e like ..), and no backslashes.
  const path = sub.split(/[?#]/)[0];
  if (path.split('/').some((seg) => { const d = dec(seg); return d === '.' || d === '..' || d.includes('/') || d.includes('\\'); })) sub = '';
  return { name, sub };
}
const currentPlugin = () => (route.startsWith('p/') ? frameRoute().name : null);

function renderNav() {
  const cur = currentPlugin();
  const link = (href, on, inner) => `<a href="${href}"${on ? ' aria-current="page"' : ''}>${inner}</a>`;
  const items = [];
  const ui = uiPlugins();
  if (ui.length) items.push(`<div class="label">${esc(t('pages'))}</div>`);
  for (const p of ui) items.push(link('#/p/' + encodeURIComponent(p.name), cur === p.name, `<span class="glyph">${glyph(p)}</span><span class="name">${esc(tr(p, 'title'))}</span><span class="dot ${stateDot(p.state)}" title="${esc(t('st_' + (p.state || 'stopped')))}"></span>`));
  items.push(`<div class="label">${esc(t('manage'))}</div>`);
  const pu = (upd && upd.targets || []).filter((x) => x.available && x.target !== 'rcplug').length;
  const hu = (upd && upd.targets || []).some((x) => x.available && x.target === 'rcplug');
  items.push(link('#/plugins', route === 'plugins', `<span class="glyph">${ico('puzzle')}</span><span class="name">${esc(t('plugins'))}</span>${pu ? `<span class="badge" title="${esc(t('updBadgeNav', { n: pu }))}">${pu}</span>` : ''}`));
  items.push(link('#/system', route.startsWith('system'), `<span class="glyph">${ico('gear')}</span><span class="name">${esc(t('system'))}</span>${hu ? `<span class="badge" title="${esc(t('updBadgeNav', { n: 1 }))}">1</span>` : ''}`));
  const html = items.join('');
  if (html !== navSig) { navSig = html; $('#nav').innerHTML = html; }
  const p = cur && plugins.find((x) => x.name === cur);
  $('#mtitle').textContent = p ? tr(p, 'title') : route === 'plugins' ? t('plugins') : route.startsWith('system') ? t('system') : 'rcplug';
}

// On a phone the side bar slides in over the page; closed, it is out of
// reach for the keyboard and screen readers too.
const phone = matchMedia('(max-width: 860px)');
function setNav(open) {
  document.body.classList.toggle('nav-open', open);
  $('#scrim').hidden = !open;
  $('#sidebar').inert = phone.matches && !open;
  $('#navOpen').setAttribute('aria-expanded', String(open));
  if (open) ($('#nav a[aria-current]') || $('#navClose')).focus();
}
phone.addEventListener('change', () => setNav(false));
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && document.body.classList.contains('nav-open')) { setNav(false); $('#navOpen').focus(); }
});

function go() {
  route = location.hash.replace(/^#\/?/, '');
  if (!route) {
    if (!loaded) return; // refresh() calls go() again once the plugin list arrived
    route = defaultRoute();
    history.replaceState(null, '', '#/' + route);
  }
  setNav(false);
  renderNav();
  pageSig = '';
  if (route !== 'system/audio') stopEvents();
  if (route.startsWith('p/')) { const f = frameRoute(); renderFrame(f.name, f.sub); }
  else if (route.startsWith('system')) renderSystem();
  else renderPlugins();
  const at = route;
  if ((route.startsWith('system') || route === 'plugins') && !upd) {
    loadUpdates().then(() => {
      if (route !== at) return; // moved on meanwhile
      if (route.startsWith('system')) renderSystem(); else renderPlugins();
    });
  }
}
window.addEventListener('hashchange', go);

// ---- embedded plugin UI
let frameFor = null;
function renderFrame(name, sub = '') {
  const p = plugins.find((x) => x.name === name);
  const view = $('#view');
  if (!p) { view.innerHTML = `<div class="empty">${ico('alert')}<p>${esc(t('notFound'))}</p></div>`; frameFor = null; return; }
  if (!p.running) {
    frameFor = null;
    view.innerHTML = `<div class="page"><div class="card"><div class="card-head"><span class="tile">${glyph(p)}</span><h2>${esc(tr(p, 'title'))}</h2>
      <span class="pill ${stateDot(p.state) || 'plain'}" style="margin-left:auto">${esc(t('st_' + (p.state || 'stopped')))}</span></div>
      <p class="muted">${esc(tr(p, 'description'))}</p>
      <p>${esc(t('isState', { state: t('st_' + p.state) }))}${p.lastErr ? ' (' + esc(p.lastErr) + ')' : ''}.</p>
      ${p.enabled ? `<p class="muted">${esc(t('autoRestart'))}</p>` : `<button class="btn primary" id="startIt">${ico('play', 'i-sm')}${esc(t('start'))}</button>`}<p class="error" id="err"></p></div></div>`;
    const b = $('#startIt');
    if (b) b.onclick = () => act(p.name, 'enable', b);
    return;
  }
  if (frameFor === name + '|' + sub && $('#view iframe')) return;
  frameFor = name + '|' + sub;
  view.innerHTML = `<iframe src="plugins/${encodeURIComponent(name)}/${esc(sub)}" title="${esc(tr(p, 'title'))}" allow="fullscreen"></iframe>`;
}
let stoppedSig = '';
function checkFrame() {
  const { name, sub } = frameRoute();
  const p = plugins.find((x) => x.name === name);
  if (!p || (frameFor === name + '|' + sub) !== !!p.running) { stoppedSig = ''; renderFrame(name, sub); return; }
  // The page shown instead of a stopped plugin follows its state.
  if (!p.running) {
    const sig = JSON.stringify([p.state, p.enabled, p.lastErr, lang]);
    if (sig !== stoppedSig) { stoppedSig = sig; renderFrame(name, sub); }
  }
}

async function act(name, action, btn) {
  if (btn) btn.disabled = true;
  try { await jpost(`api/plugins/${encodeURIComponent(name)}/${action}`); }
  catch (e) { const el = $('#err') || $(`.plugin[data-name="${CSS.escape(name)}"] .error`); if (el) el.textContent = t('failed', { err: e.message }); }
  setTimeout(() => { if (btn) btn.disabled = false; refresh(); }, 400);
}

// ---- plugins (rebuilt only when something changed, so focus, open logs
// and scroll positions survive the periodic refresh)
function removeBox(p) {
  return `<div class="banner danger">${ico('trash')}<div class="stack" style="--gap:10px"><div>${esc(t('rmQ', { name: tr(p, 'title') || p.name }))}</div>
    <label class="check small"><input type="checkbox" data-purge${openRemove[p.name].purge ? ' checked' : ''}> ${esc(t('rmPurge'))}</label>
    <div class="cluster"><button class="btn danger sm" data-act="rm-go">${esc(t('rmGo'))}</button><button class="btn ghost sm" data-act="rm-cancel">${esc(t('rmCancel'))}</button></div></div></div>`;
}
async function removePlugin(name, btn, errEl) {
  btn.disabled = true;
  try {
    await jpost(`api/plugins/${encodeURIComponent(name)}/remove`, { confirm: name, purge: !!(openRemove[name] && openRemove[name].purge) });
    delete openRemove[name]; delete openOpts[name]; delete openLogs[name];
  } catch (e) { errEl.textContent = t('failed', { err: e.message }); btn.disabled = false; return; }
  await refresh();
}

// What a plugin declares it does (plugin.json "permissions").
function permsRow(p) {
  const perms = p.permissions || [];
  if (!perms.length) return '';
  return `<div class="perms">${perms.map((x) => `<span class="tag perm${x === 'desk:write' ? ' strong' : ''}" title="${esc(t('permHint_' + x))}">${esc(t('perm_' + x))}</span>`).join('')}</div>`;
}

function pluginCard(p) {
  const st = p.state || 'stopped';
  const u = pluginUpdate(p.name);
  const nm = encodeURIComponent(p.name);
  const tags = `<span class="tag">v${esc(p.version || '?')}</span>${u ? `<span class="tag info">${esc(t('updBadge', { v: u.latest }))}</span>` : ''}`
    + `${p.needsAudio && !(p.permissions || []).includes('audio') ? `<span class="tag">${esc(t('audio'))}</span>` : ''}${p.primary ? `<span class="tag">${esc(t('startpage'))}</span>` : ''}`;
  const main = p.hasUI ? `<a class="btn primary sm" href="#/p/${nm}">${esc(t('open'))}</a><a class="btn ghost icon sm" href="plugins/${nm}/" target="_blank" rel="noopener" title="${esc(t('newTab'))}" aria-label="${esc(t('newTab'))}">${ico('external', 'i-sm')}</a>` : '';
  const run = p.enabled
    ? `<button class="btn ghost sm" data-act="restart">${ico('restart', 'i-sm')}${esc(t('restart'))}</button><button class="btn ghost sm" data-act="disable">${ico('stop', 'i-sm')}${esc(t('stop'))}</button>`
    : `<button class="btn sm" data-act="enable">${ico('play', 'i-sm')}${esc(t('start'))}</button>`;
  return `<div class="card plugin" data-name="${esc(p.name)}"><span class="tile">${glyph(p)}</span>
    <div class="head"><h3>${esc(tr(p, 'title'))}</h3>${tags}<span class="pill state ${stateDot(st) || 'plain'}">${esc(t('st_' + st))}${p.pid ? ' · PID ' + esc(p.pid) : ''}</span></div>
    <p class="desc">${esc(tr(p, 'description'))}</p>
    <div>${permsRow(p)}${p.unknownFields ? `<p class="error">${esc(t('unknown', { f: p.unknownFields.join(', ') }))}</p>` : ''}
      ${p.restarts ? `<span class="dim tiny">${esc(t('restarts', { n: p.restarts }))}</span> ` : ''}${p.lastErr && st !== 'running' ? `<span class="warn-text small">${esc(p.lastErr)}</span>` : ''}</div>
    <div class="foot">${main}${u ? `<button class="btn sm" data-act="update"${jobActive() ? ' disabled' : ''}>${ico('download', 'i-sm')}${esc(t('updPlugin'))}</button>` : ''}<span class="spacer"></span>
      ${p.options && p.options.length ? `<button class="btn ghost sm" data-act="settings" aria-expanded="${!!openOpts[p.name]}">${ico('sliders', 'i-sm')}${esc(t('settings'))}</button>` : ''}
      <button class="btn ghost sm" data-act="logs" aria-expanded="${!!openLogs[p.name]}">${ico('list', 'i-sm')}${esc(t('log'))}</button>
      ${run}${p.removable ? `<button class="btn ghost icon sm" data-act="remove" aria-expanded="${!!openRemove[p.name]}" title="${esc(t('remove'))}" aria-label="${esc(t('remove'))}">${ico('trash', 'i-sm')}</button>` : ''}</div>
    <div class="extra">${u || (upd && upd.job && upd.job.target === 'plugin:' + p.name) ? `<div data-progress="plugin:${esc(p.name)}"></div>` : ''}<p class="error"></p>${openRemove[p.name] ? removeBox(p) : ''}<div class="opts" ${openOpts[p.name] ? '' : 'hidden'}></div><pre class="logs" ${openLogs[p.name] ? '' : 'hidden'}></pre></div></div>`;
}

function renderPlugins() {
  frameFor = null;
  const sig = JSON.stringify([plugins, invalid, lang, upd && upd.targets, upd && upd.job && upd.job.state]);
  if (sig === pageSig && $('#view .page')) { refreshLogs(); return; }
  pageSig = sig;
  let html = `<div class="page"><div class="page-head"><div><h1>${esc(t('plugins'))}</h1><p class="lead">${esc(t('pluginsLead'))}</p></div>
    <div class="actions"><button class="btn" id="rescan" title="${esc(t('rescanHint'))}">${ico('refresh', 'i-sm')}${esc(t('rescan'))}</button></div></div>`;
  if (invalid.length) html += `<div class="banner warn">${ico('alert')}<div><b>${esc(t('skipped'))}</b><br>${invalid.map(esc).join('<br>')}</div></div>`;
  html += '<div class="stack">';
  if (!plugins.length) html += `<div class="card empty">${ico('puzzle')}<p>${esc(t('none'))}</p><p class="dim small">${esc(t('noneHint'))}</p></div>`;
  for (const p of plugins) html += pluginCard(p);
  html += `</div><div id="catalog" class="section catalog"></div><p class="footnote">${esc(t('footer'))}</p></div>`;
  $('#view').innerHTML = html;
  catSig = ''; // the catalog's container is new
  $('#rescan').onclick = async (e) => {
    const b = e.currentTarget;
    b.disabled = true;
    try { await jpost('api/reload'); } catch (x) { toast(t('failed', { err: x.message }), true); }
    b.disabled = false;
    refresh();
  };
  document.querySelectorAll('#view .plugin[data-name]').forEach((c) => {
    const name = c.dataset.name, logsEl = $('.logs', c);
    const p = plugins.find((x) => x.name === name);
    if (openOpts[name]) renderOptions(p, $('.opts', c));
    const purge = $('[data-purge]', c);
    if (purge) purge.onchange = () => { openRemove[name].purge = purge.checked; };
    c.querySelectorAll('[data-act]').forEach((b) => b.addEventListener('click', () => {
      const a = b.dataset.act;
      if (a === 'settings') { toggleOptions(p, $('.opts', c), b); return; }
      if (a === 'logs') {
        openLogs[name] = !openLogs[name];
        logsEl.hidden = !openLogs[name];
        b.setAttribute('aria-expanded', String(!!openLogs[name]));
        if (openLogs[name]) loadLogs(name, logsEl);
        return;
      }
      if (a === 'update') { installUpdate('plugin:' + name, b); return; }
      if (a === 'remove') {
        if (openRemove[name]) delete openRemove[name]; else openRemove[name] = { purge: false };
        pageSig = ''; renderPlugins(); renderCatalog(); updateProgress();
        return;
      }
      if (a === 'rm-cancel') { delete openRemove[name]; pageSig = ''; renderPlugins(); renderCatalog(); updateProgress(); return; }
      if (a === 'rm-go') { removePlugin(name, b, $('.error', c)); return; }
      act(name, a, b);
    }));
  });
  refreshLogs();
  renderCatalog(); // into the new container, not only at the next poll
}

// ---- plugin options (manifest "options", stored by the host in
// config/<plugin>.json; see docs/plugin-api.md#options)
const trOpt = (p, o, k) => (p.translations && p.translations[lang] && p.translations[lang].options && p.translations[lang].options[o.id] && p.translations[lang].options[o.id][k]) || o[k] || '';
const trChoice = (p, o, v) => {
  const c = (o.choices || []).find((x) => x.value === v);
  const l = p.translations && p.translations[lang] && p.translations[lang].options && p.translations[lang].options[o.id];
  return (l && l.choices && l.choices[v]) || (c ? c.title : String(v));
};
async function toggleOptions(p, el, btn) {
  if (openOpts[p.name]) { delete openOpts[p.name]; el.hidden = true; btn.setAttribute('aria-expanded', 'false'); return; }
  try {
    const d = await jget(`api/plugins/${encodeURIComponent(p.name)}/options`);
    openOpts[p.name] = { values: { ...(d.values || {}) } };
  } catch (e) { el.hidden = false; el.textContent = t('failed', { err: e.message }); return; }
  btn.setAttribute('aria-expanded', 'true');
  renderOptions(p, el);
}
function renderOptions(p, el) {
  const st = openOpts[p.name];
  el.hidden = false;
  el.replaceChildren();
  const make = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text !== undefined) e.textContent = text; return e; };
  const show = (o, v) => (o.type === 'bool' ? t(v ? 'optYes' : 'optNo') : o.type === 'choice' ? trChoice(p, o, v) : String(v));
  for (const o of p.options) {
    const set = Object.prototype.hasOwnProperty.call(st.values, o.id) && st.values[o.id] !== null;
    const cur = set ? st.values[o.id] : o.default;
    const label = make('label', 'field');
    const title = make('span', '', trOpt(p, o, 'title'));
    let input;
    if (o.type === 'bool') {
      input = make('input', 'switch');
      input.type = 'checkbox';
      input.checked = !!cur;
      input.onchange = () => { st.values[o.id] = input.checked; };
      const row = make('span', 'check');
      row.append(input, make('span', '', trOpt(p, o, 'title')));
      label.append(row);
    } else {
      if (o.type === 'choice') {
        input = make('select', 'select');
        for (const c of o.choices) input.append(new Option(trChoice(p, o, c.value), c.value));
        input.value = cur ?? '';
      } else {
        input = make('input', 'input');
        input.type = o.type === 'number' ? 'number' : 'text';
        if (o.type === 'number') { input.step = 'any'; if (o.min != null) input.min = o.min; if (o.max != null) input.max = o.max; }
        if (o.maxLength) input.maxLength = o.maxLength;
        input.value = cur ?? '';
      }
      input.onchange = () => { st.values[o.id] = o.type === 'number' ? Number(input.value) : input.value; };
      label.append(title, input);
    }
    label.append(make('span', 'odesc', [trOpt(p, o, 'description'), o.default !== undefined ? t('optDefault', { v: show(o, o.default) }) : ''].filter(Boolean).join(' · ')));
    if (set) {
      const reset = make('button', 'link', t('optReset'));
      reset.type = 'button';
      reset.style.justifySelf = 'start';
      reset.onclick = (e) => { e.preventDefault(); st.values[o.id] = null; renderOptions(p, el); };
      label.append(reset);
    }
    el.append(label);
  }
  const msg = make('span', 'dim small', st.msg || t('optHint'));
  const save = make('button', 'btn primary sm', t(p.enabled ? 'optSaveRestart' : 'optSave'));
  save.type = 'button';
  save.onclick = async () => {
    save.disabled = true;
    try {
      const r = await jpost(`api/plugins/${encodeURIComponent(p.name)}/options`, { values: st.values });
      const d = await jget(`api/plugins/${encodeURIComponent(p.name)}/options`);
      st.values = { ...(d.values || {}) };
      st.msg = t(r.restarted ? 'optSavedRestart' : 'optSaved');
      renderOptions(p, el);
      setTimeout(refresh, 600);
    } catch (e) { msg.textContent = t('failed', { err: e.message }); msg.className = 'error'; save.disabled = false; }
  };
  const row = make('div', 'cluster');
  row.append(save, msg);
  el.append(row);
}

function refreshLogs() {
  for (const [name, open] of Object.entries(openLogs)) {
    const el = $(`.plugin[data-name="${CSS.escape(name)}"] .logs`);
    if (open && el) loadLogs(name, el);
  }
}
async function loadLogs(name, el) {
  const atBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - 4;
  try {
    const d = await jget(`api/plugins/${encodeURIComponent(name)}/logs`);
    el.textContent = (d.lines || []).join('\n') || t('emptyLog');
    if (atBottom) el.scrollTop = el.scrollHeight;
  } catch (e) { el.textContent = t('noLog'); }
}

// ---- the plugin catalog (docs/updates.md#plugin-catalog)
async function loadCatalog() { try { cat = await jget('api/catalog'); } catch (e) { /* keep the last state */ } }
const trCat = (e, k) => (e.translations && e.translations[lang] && e.translations[lang][k]) || e[k] || '';
const fmtSize = (n) => (n >= 1048576 ? (n / 1048576).toLocaleString(lang, { maximumFractionDigits: 1 }) + ' MB' : Math.max(1, Math.round(n / 1024)) + ' KB');
const httpsURL = (u) => (/^https:\/\//.test(u || '') ? u : '');

function renderCatalog() {
  const el = $('#catalog');
  if (!el) return;
  const sig = JSON.stringify([cat, catLoading, lang, upd && upd.job, plugins.map((p) => p.name + '@' + p.version)]);
  if (sig === catSig) return;
  catSig = sig;
  const loadLabel = catLoading ? t('catLoading') : cat && cat.checkedAt ? t('catReload') : t('catLoad');
  let h = `<div class="section-head"><h2>${esc(t('catTitle'))}</h2><span class="spacer"></span>`
    + `${cat && cat.enabled ? `<button class="btn sm" id="catLoad"${catLoading ? ' disabled' : ''}>${ico('refresh', 'i-sm')}${esc(loadLabel)}</button>` : ''}</div>`;
  if (!cat || !cat.enabled) { el.innerHTML = h + `<p class="muted">${esc(cat ? t('catOff') : t('offline'))}</p>`; return; }
  if (!cat.checkedAt) h += `<p class="muted">${esc(t('catHint'))}</p>`;
  else {
    for (const s of cat.sources.filter((x) => x.error)) h += `<div class="banner danger">${ico('alert')}<div>${esc(s.github)}: ${esc(s.error)}</div></div>`;
    if (!cat.plugins.length) h += `<div class="empty">${esc(t('catEmpty'))}</div>`;
    const j = upd && upd.job, busy = jobActive();
    h += '<div class="grid" style="--min:300px">';
    cat.plugins.forEach((e, i) => {
      const target = 'install:' + e.name, mine = j && j.target === target;
      const page = httpsURL(e.page);
      h += `<div class="card" data-cat="${i}">
        <div class="head"><span class="tile">${ICONS[e.name] ? ico(ICONS[e.name]) : esc(e.icon || '•')}</span><div><h3>${esc(trCat(e, 'title') || e.name)}</h3>
          <div class="meta"><span class="tag">v${esc(e.version)}</span>${e.official ? `<span class="tag ok">${esc(t('catOfficial'))}</span>` : `<span class="tag">${esc(t('catFrom', { repo: e.repo }))}</span>`}</div></div></div>
        <p class="desc">${esc(trCat(e, 'description'))}</p>${permsRow(e)}
        <div class="cluster">${e.installed ? `<span class="pill ok">${esc(t('catInstalled', { v: e.installed }))}</span>` : `<button class="btn primary sm" data-install${busy ? ' disabled' : ''}>${ico('download', 'i-sm')}${esc(t('catInstall'))}</button>`}
          <span class="dim small">${esc(fmtSize(e.size))}</span><span class="spacer"></span>${page ? `<a class="small" href="${esc(page)}" target="_blank" rel="noopener">${esc(t('updRelease'))} ${ico('external', 'i-sm')}</a>` : ''}</div>
        <div data-progress="${esc(target)}"></div>`
        + (mine && j.state === 'error' ? `<p class="error">${esc(t('catFailed', { err: j.error || '?' }))}</p>` : '')
        + (mine && j.state === 'done' && e.installed ? `<p class="ok-text small">${esc(t('catDone'))}</p>` : '') + '</div>';
    });
    h += '</div>';
  }
  h += `<details id="catSources" class="card" style="margin-top:16px"${catSourcesOpen ? ' open' : ''}><summary>${esc(t('catSources'))}</summary>
    <p class="hint">${esc(t('catSourcesHint'))}</p><div style="margin-top:8px">`;
  for (const s of cat.sources) {
    const bits = [s.official ? t('catOfficial') : '', s.release ? t('catSrcRelease', { v: s.release }) : '', cat.checkedAt && !s.error ? t('catSrcPlugins', { n: s.plugins }) : '', s.keyId ? t('catSrcKey', { id: s.keyId }) : ''].filter(Boolean);
    h += `<div class="src"><b>${esc(s.github)}</b><span class="dim small">${esc(bits.join(' · '))}</span><span class="spacer"></span>`
      + `${s.official ? '' : `<button class="btn ghost sm" data-src-remove="${esc(s.github)}">${esc(t('catRemoveSrc'))}</button>`}${s.error ? `<p class="error" style="flex-basis:100%">${esc(s.error)}</p>` : ''}</div>`;
  }
  h += `</div><div class="cluster" style="margin-top:12px"><input class="input" id="catRepo" placeholder="${esc(t('catRepo'))}" aria-label="${esc(t('catRepo'))}" style="flex:1 1 200px" autocomplete="off">`
    + `<input class="input" id="catKey" placeholder="${esc(t('catKey'))}" aria-label="${esc(t('catKey'))}" style="flex:2 1 260px" autocomplete="off"><button class="btn" id="catAdd">${ico('plus', 'i-sm')}${esc(t('catAdd'))}</button></div><p class="error" id="catSrcErr"></p></details>`;
  el.innerHTML = h;
  for (const [id, k] of [['#catRepo', 'repo'], ['#catKey', 'key']]) {
    const input = $(id);
    input.value = catDraft[k];
    input.oninput = () => { catDraft[k] = input.value; };
  }
  if ($('#catLoad')) $('#catLoad').onclick = refreshCatalog;
  $('#catSources').ontoggle = (e) => { catSourcesOpen = e.target.open; };
  el.querySelectorAll('[data-install]').forEach((b) => (b.onclick = () => installFromCatalog(cat.plugins[+b.closest('[data-cat]').dataset.cat], b)));
  el.querySelectorAll('[data-src-remove]').forEach((b) => (b.onclick = () => changeSource({ github: b.dataset.srcRemove, remove: true }, b)));
  $('#catAdd').onclick = (e) => changeSource({ github: $('#catRepo').value, publicKey: $('#catKey').value }, e.currentTarget);
}

async function refreshCatalog() {
  catLoading = true;
  renderCatalog();
  try { cat = await jget('api/catalog?refresh=1'); } catch (e) { toast(t('failed', { err: e.message }), true); }
  catLoading = false;
  renderCatalog();
  updateProgress();
}

async function changeSource(body, btn) {
  if (body.remove && !confirm(t('catRemoveSrcQ', { repo: body.github }))) return;
  btn.disabled = true;
  catLoading = true;
  try {
    cat = await jpost('api/catalog/sources', body);
    if (!body.remove) { catDraft.repo = ''; catDraft.key = ''; }
    catLoading = false;
    renderCatalog();
  } catch (e) {
    catLoading = false;
    btn.disabled = false;
    renderCatalog();
    $('#catSrcErr').textContent = t('failed', { err: e.message });
  }
  updateProgress();
}

async function installFromCatalog(e, btn) {
  const perms = (e.permissions || []).map((x) => '• ' + t('perm_' + x) + ': ' + t('permHint_' + x)).join('\n');
  const q = t('catConfirm', { name: trCat(e, 'title') || e.name, v: e.version, repo: e.repo }) + '\n\n' + (perms ? t('catConfirmPerms') + '\n' + perms : t('catConfirmNoPerms'));
  if (!confirm(q)) return;
  btn.disabled = true;
  try { cat = await jpost('api/catalog/install', { repo: e.repo, plugin: e.name }); await loadUpdates(); }
  catch (x) { toast(t('failed', { err: x.message }), true); btn.disabled = false; }
  renderCatalog();
  updateProgress();
}

// ---- the System pages: #/system[/procs|updates|access|audio|help]
const SYS_TABS = [['', 'sysOverview'], ['procs', 'sysProcs'], ['services', 'sysServices'], ['updates', 'sysUpdates'], ['access', 'sysAccess'], ['audio', 'sysAudio'], ['help', 'sysHelp']];
let sysTabShown = null;
function renderSystem() {
  frameFor = null;
  const tab = route.split('/')[1] || '';
  if (sysTabShown !== tab || !$('#sysBody')) {
    sysTabShown = tab;
    updSig = '';
    const tabs = SYS_TABS.map(([id, k]) => {
      const n = id === 'updates' && upd ? (upd.targets || []).filter((x) => x.available).length : 0;
      return `<a href="#/system${id ? '/' + id : ''}"${tab === id ? ' aria-current="page"' : ''}>${esc(t(k))}${n ? ` <span class="badge">${n}</span>` : ''}</a>`;
    }).join('');
    $('#view').innerHTML = `<div class="page"><div class="page-head"><div><h1>${esc(t('system'))}</h1><p class="lead">${esc(t('sysDesc'))}</p></div></div>
      <nav class="tabs" style="margin-bottom:22px">${tabs}</nav><div id="sysBody"></div></div>`;
    const body = $('#sysBody');
    if (tab === '') body.innerHTML = '<div id="sysStats" class="stats"></div><div class="card section" id="sysCard"></div>';
    else if (tab === 'procs') {
      body.innerHTML = `<div class="card" id="procCard"><div class="card-head"><div><h2>${esc(t('procTitle'))}</h2><p class="lead" style="margin:2px 0 0">${esc(t('procDesc'))}</p></div>
        <div class="actions"><div class="seg" role="radiogroup"><button type="button" role="radio" data-sort="cpu">${esc(t('procByCPU'))}</button><button type="button" role="radio" data-sort="mem">${esc(t('procByMem'))}</button></div></div></div>
        <div id="procList"></div><details style="margin-top:14px"><summary>${esc(t('procWhat'))}</summary><p class="hint">${esc(t('procExplain'))}</p></details></div>`;
      bindProcSort();
    } else if (tab === 'services') {
      body.innerHTML = `<div class="stack" style="--gap:14px"><p class="lead" style="margin:0">${esc(t('svcDesc'))}</p><div id="svcScreen"></div><div id="svcList" class="stack" style="--gap:14px"></div></div>`;
    } else if (tab === 'updates') body.innerHTML = '<div class="card" id="updCard"></div>';
    else if (tab === 'access') { body.innerHTML = '<div class="card" id="accCard"></div>'; renderAccess(); }
    else if (tab === 'audio') {
      body.innerHTML = `<div class="stack" style="--gap:16px"><div class="card" id="tapCard"></div><div class="card"><div class="card-head"><h2>${esc(t('evTitle'))}</h2></div><p class="lead">${esc(t('evDesc'))}</p><div id="evList" class="evlist"></div></div></div>`;
      startEvents();
    } else if (tab === 'help') {
      body.innerHTML = `<div class="stack" style="--gap:16px"><div class="card"><div class="card-head"><h2>${esc(t('diagTitle'))}</h2></div><p class="lead">${esc(t('diagText'))}</p>
          <div class="cluster"><a class="btn primary" href="api/diagnostics" download>${ico('download', 'i-sm')}${esc(t('diagButton'))}</a>
          <a class="btn ghost" href="https://hustenreizjuengling.github.io/rcplug-toolkit/diagnostics.html" target="_blank" rel="noopener" title="${esc(t('diagViewHint'))}">${esc(t('diagView'))} ${ico('external', 'i-sm')}</a></div></div>
        <div class="card danger"><div class="card-head"><h2>${esc(t('unTitle'))}</h2></div><p class="lead">${esc(t('unDesc'))}</p>
          <div class="cluster"><input class="input" type="text" id="unWord" placeholder="${esc(t('unWordHint'))}" aria-label="${esc(t('unWordHint'))}" autocomplete="off" style="max-width:220px">
          <button class="btn danger" id="unGo">${ico('trash', 'i-sm')}${esc(t('unButton'))}</button><span class="error" id="unErr"></span></div></div>
        <p class="footnote">${esc(t('disclaimer'))}</p></div>`;
      $('#unGo').onclick = async () => {
        if ($('#unWord').value.trim().toLowerCase() !== t('unWord').toLowerCase()) { $('#unErr').textContent = t('unTypeWord', { w: t('unWord') }); return; }
        if (!confirm(t('unConfirm'))) return;
        try { await jpost('api/uninstall', { confirm: 'uninstall' }); document.body.innerHTML = `<main class="page"><div class="card"><h2>${esc(t('unDone'))}</h2><p>${esc(t('unDoneText'))}</p></div></main>`; }
        catch (e) { $('#unErr').textContent = t('failed', { err: e.message }); }
      };
    }
  }
  if (tab === '') renderOverview();
  else if (tab === 'procs') loadProcs();
  else if (tab === 'services') loadServices();
  else if (tab === 'updates') renderUpdates();
  else if (tab === 'audio') renderTaps();
}

function renderOverview() {
  const st = $('#sysStats'), card = $('#sysCard');
  if (!st) return;
  if (!sys) { st.innerHTML = `<div class="card empty">${ico('alert')}<p>${esc(t('offline'))}</p></div>`; card.hidden = true; return; }
  card.hidden = false;
  const stat = (label, value, bar, cls) => `<div class="card"><div class="stat"><span class="label">${esc(label)}</span><span class="value">${value}</span></div>${bar !== undefined ? `<div class="bar${cls ? ' ' + cls : ''}"><i style="width:${bar.toFixed(0)}%"></i></div>` : ''}</div>`;
  const tiles = [];
  if (sys.memTotalKB) {
    const used = 100 * (1 - sys.memAvailableKB / sys.memTotalKB);
    tiles.push(stat(t('statMem'), `${esc(mb(sys.memTotalKB - sys.memAvailableKB))}<small>/ ${esc(mb(sys.memTotalKB))} MB</small>`, used, used > 85 ? 'warn' : ''));
  }
  if (sys.diskTotalMB) {
    const used = 100 * (1 - sys.diskFreeMB / sys.diskTotalMB);
    tiles.push(stat(t('statDisk'), `${esc(sys.diskFreeMB)}<small>MB</small>`, used, used > 90 ? 'warn' : ''));
  }
  if (sys.load) tiles.push(stat(t('statLoad'), `${esc(sys.load[0])}<small>${esc(sys.load.slice(1).join(' · '))}</small>`));
  if (sys.cpuTempC !== undefined) tiles.push(stat(t('statTemp'), `${esc(sys.cpuTempC.toFixed(0))}<small>°C</small>`));
  if (sys.systemUptimeSec !== undefined) tiles.push(stat(t('statUp'), esc(fmtUptime(sys.systemUptimeSec))));
  st.innerHTML = tiles.join('');
  const mbr = (kb) => Math.round(kb / 1024);
  const parts = sys.memory ? `<div class="hint" title="${esc(t('memPartsHint'))}">${esc(t('memParts', { p: mbr(sys.memory.programsKB), s: mbr(sys.memory.sharedKB), k: mbr(sys.memory.kernelKB), c: mbr(sys.memory.cacheKB), f: mbr(sys.memory.freeKB) }))}</div>` : '';
  const row = (k, v) => `<dt>${esc(t(k))}</dt><dd>${v}</dd>`;
  card.innerHTML = `<div class="card-head"><h2>${esc(t('sysDevice'))}</h2></div><dl class="kv">
    ${row('hostname', esc(sys.host))}${row('arch', esc(sys.arch + '/' + sys.os))}
    ${row('memory', sys.memTotalKB ? esc(t('memUsed', { used: mbr(sys.memTotalKB - sys.memAvailableKB), total: mbr(sys.memTotalKB) })) + parts : '—')}
    ${row('disk', sys.diskTotalMB ? esc(t('diskFree', { free: sys.diskFreeMB, total: sys.diskTotalMB })) : '—')}
    ${row('temp', sys.cpuTempC !== undefined ? esc(sys.cpuTempC.toFixed(1)) + ' °C' : '—')}${row('loadAvg', sys.load ? esc(sys.load.join(' / ')) : '—')}
    ${row('devUp', sys.systemUptimeSec !== undefined ? esc(fmtUptime(sys.systemUptimeSec)) : '—')}${row('hostUp', esc(fmtUptime(sys.uptimeSec)))}
    ${row('version', esc(sys.version))}${row('clock', esc(new Date(sys.time).toLocaleString(lang)))}
  </dl>`;
}

// ---- processes (refreshed with the page, every 4 s)
let procSort = 'cpu';
function bindProcSort() {
  document.querySelectorAll('#procCard [data-sort]').forEach((b) => {
    b.setAttribute('aria-checked', String(b.dataset.sort === procSort));
    b.onclick = () => { procSort = b.dataset.sort; bindProcSort(); loadProcs(); };
  });
}
async function loadProcs() {
  const el = $('#procList');
  if (!el) return;
  let d;
  try { d = await jget('api/processes?limit=15&sort=' + procSort); } catch (e) { el.innerHTML = `<p class="muted">${esc(t('procNone'))}</p>`; return; }
  const fmb = (kb) => (kb / 1024).toLocaleString(lang, { maximumFractionDigits: 1 });
  const pc = (x) => (x || 0).toLocaleString(lang, { minimumFractionDigits: 1, maximumFractionDigits: 1 });
  const cores = d.cores || 1;
  const owner = (p) => (p.owner === 'rcplug' ? `<span class="tag ok">${esc(p.plugin === 'rcplugd' ? 'rcplug' : p.plugin)}</span>` : p.owner === 'rode' ? '<span class="tag">RØDE</span>' : '');
  const kt = d.kernelThreads || { count: 0, cpu: 0 };
  const cpuLine = d.cpu ? `<div class="procs-top"><b>${esc(t('procCPUNow', { total: pc(d.cpu.total) }))}</b><span class="dim small">${esc(t('procCPUParts', { user: pc(d.cpu.user), system: pc(d.cpu.system), irq: pc(d.cpu.irq), io: pc(d.cpu.iowait), cores }))}</span></div>` : '';
  el.innerHTML = cpuLine + `<div class="procs-wrap"><table class="table"><thead><tr><th>${esc(t('procName'))}</th><th>PID</th><th class="num">CPU</th><th class="num" title="${esc(t('procOwnHint'))}">${esc(t('procOwn'))}</th><th class="num" title="${esc(t('procSharedHint'))}">${esc(t('procShared'))}</th><th class="num">${esc(t('procThreads'))}</th></tr></thead><tbody>`
    + d.processes.map((p) => `<tr title="${esc(p.command || p.name)}"><td>${esc(p.name)} ${owner(p)}</td><td class="dim num">${esc(p.pid)}</td><td class="num">${esc(pc(p.cpu / cores))} %</td><td class="num">${esc(fmb(p.ownKB || 0))} MB</td><td class="num dim">${esc(fmb(p.sharedKB || 0))} MB</td><td class="num dim">${esc(p.threads)}</td></tr>`).join('')
    + (kt.count ? `<tr class="dim"><td>${esc(t('procKernel', { n: kt.count }))}</td><td></td><td class="num">${esc(pc(kt.cpu / cores))} %</td><td class="num">–</td><td class="num">–</td><td></td></tr>` : '')
    + `</tbody></table></div><p class="hint">${esc(t('procFoot', { n: d.count, own: fmb(d.ownKB || 0), programs: fmb((d.memory && d.memory.programsKB) || 0) }))}</p>`;
}

// ---- RØDE's services: switched off and on (internal/host/services.go)
let svcBusy = '';
async function loadServices() {
  const el = $('#svcList');
  if (!el || svcBusy) return;
  let d;
  try { d = await jget('api/services'); } catch (e) { el.innerHTML = `<p class="muted">${esc(t('failed', { err: e.message }))}</p>`; return; }
  $('#svcScreen').innerHTML = d.screen ? `<div class="banner info">${ico('info')}<div>${esc(t('svcScreen', { name: d.screen }))}</div></div>` : '';
  const pc = (x) => (x || 0).toLocaleString(lang, { minimumFractionDigits: 1, maximumFractionDigits: 1 });
  const cores = d.cores || 1;
  el.innerHTML = d.services.map((x) => {
    const procs = x.procs || [], running = procs.length > 0;
    const cpu = procs.reduce((a, p) => a + (p.cpu || 0), 0) / cores, ram = procs.reduce((a, p) => a + (p.ownKB || 0), 0) / 1024;
    const pill = x.state === 'off' ? `<span class="pill plain">${esc(t('svcOff'))}</span>`
      : x.state === 'unavailable' ? `<span class="pill warn">${esc(t('svcUnavailable'))}</span>`
      : running ? `<span class="pill ok">${esc(t('svcOn'))}</span>` : `<span class="pill plain">${esc(t('svcNotRunning'))}</span>`;
    const facts = [];
    if (running) facts.push(t('svcCost', { cpu: pc(cpu), ram: ram.toLocaleString(lang, { maximumFractionDigits: 1 }) }));
    if (running) facts.push((x.listening || []).length ? t('svcPorts', { list: x.listening.join(', ') }) : t('svcNoPorts'));
    if (x.outside) facts.push(t('svcOutside', { n: x.outside }));
    const btn = x.state === 'unavailable' ? ''
      : x.state === 'off' ? `<button class="btn primary sm" data-svc="${esc(x.id)}" data-on="1">${esc(t('svcTurnOn'))}</button>`
      : `<button class="btn sm" data-svc="${esc(x.id)}" data-on="0">${ico('power', 'i-sm')}${esc(t('svcTurnOff'))}</button>`;
    return `<div class="card svc"><div class="card-head"><h2>${esc(t('svc_' + x.id))}</h2>${pill}<span class="spacer"></span>${btn}</div>
      <p class="lead" style="margin:0 0 6px">${esc(t('svc_' + x.id + '_what'))}</p>
      <p class="small">${esc(t('svcWithout', { what: t('svc_' + x.id + '_off') }))}</p>
      ${facts.length ? `<p class="hint">${esc(facts.join(' · '))}</p>` : ''}
      ${x.reason ? `<p class="${x.state === 'unavailable' ? 'warn-text' : 'hint'} small">${esc(t('svcWhy_' + x.reason))}</p>` : ''}<p class="error small"></p></div>`;
  }).join('');
  el.querySelectorAll('[data-svc]').forEach((b) => (b.onclick = async () => {
    const id = b.dataset.svc, on = b.dataset.on === '1';
    if (!on && !confirm(t('svcConfirmOff', { name: t('svc_' + id), what: t('svc_' + id + '_off') }))) return;
    b.disabled = true;
    svcBusy = id;
    try { await jpost('api/services/' + encodeURIComponent(id), { on }); }
    catch (e) { const err = b.closest('.svc').querySelector('.error'); if (err) err.textContent = t('failed', { err: e.message }); }
    finally { svcBusy = ''; }
    loadServices();
  }));
}

// ---- shared audio taps
async function renderTaps() {
  const card = $('#tapCard');
  if (!card) return;
  let d;
  try { d = await jget('api/audio'); } catch (e) { card.hidden = true; return; }
  card.hidden = false;
  const st = d.status || {};
  let h = `<div class="card-head"><h2>${esc(t('tapTitle'))}</h2><span class="pill ${st.running ? 'ok live' : 'plain'}" style="margin-left:auto">${esc(st.running ? t('tapRunning', { n: (st.channels || []).length }) : t('st_stopped'))}</span></div><p class="lead">${esc(t('tapDesc'))}</p>`;
  if (!st.running) h += `<p>${esc(t('tapIdle'))}</p>`;
  if (st.problems) h += `<p class="warn-text small">${esc(t('tapProblems', { n: st.problems }))}</p>`;
  if (st.error) h += `<p class="error">${esc(st.error)}</p>`;
  for (const s of st.streams || []) h += `<div class="ev"><b>${esc(s.plugin)}</b> <code>${esc((s.ports || []).join(', '))}</code></div>`;
  if (d.portsError) h += `<p class="hint">${esc(t('tapNoJack', { err: d.portsError }))}</p>`;
  else h += `<p class="hint">${esc(t('tapPorts', { n: (d.ports || []).length }))}</p>`;
  card.innerHTML = h;
}

// ---- the event bus, live on the audio page
let evSource = null;
const evKept = [];
function startEvents() {
  stopEvents();
  evKept.length = 0;
  renderEvents();
  try {
    evSource = new EventSource('api/events/stream?since=0');
    evSource.addEventListener('rcplug', (m) => {
      try { evKept.unshift(JSON.parse(m.data)); } catch (e) { return; }
      evKept.splice(30);
      renderEvents();
    });
  } catch (e) { /* no event bus (older host) */ }
}
function stopEvents() { if (evSource) { evSource.close(); evSource = null; } }
function renderEvents() {
  const el = $('#evList');
  if (!el) return;
  if (!evKept.length) { el.innerHTML = `<p class="muted">${esc(t('evNone'))}</p>`; return; }
  el.innerHTML = evKept.map((e) => `<div class="ev"><span class="dim num">${esc(new Date(e.time).toLocaleTimeString(lang))}</span><b>${esc(e.type)}</b><span class="tag">${esc(e.source)}</span><code>${esc(e.data ? JSON.stringify(e.data) : '')}</code></div>`).join('');
}

// ---- dashboard access (optional password)
async function renderAccess() {
  const card = $('#accCard');
  if (!card) return;
  let st;
  try { st = await jget('api/access'); } catch (e) { return; }
  const fld = (id, ph, ac) => `<input class="input" type="password" id="${id}" placeholder="${esc(ph)}" aria-label="${esc(ph)}" autocomplete="${ac}" style="max-width:240px">`;
  card.innerHTML = `<div class="card-head"><h2>${esc(t('accTitle'))}</h2><span class="pill ${st.protected ? 'ok' : 'warn'}" style="margin-left:auto">${ico('lock', 'i-sm')}<span>${esc(t(st.protected ? 'accOn' : 'accOff'))}</span></span></div>
    <p class="lead">${esc(t('accDesc'))}</p>
    <div class="cluster" style="margin:6px 0 12px">${st.protected ? fld('accCur', t('accCurrent'), 'current-password') : ''}${fld('accNew', t('accNew'), 'new-password')}${fld('accNew2', t('accRepeat'), 'new-password')}
      <button class="btn primary" id="accSave">${esc(t(st.protected ? 'accChange' : 'accSet'))}</button></div>
    <div class="cluster">${st.protected ? `<button class="btn" id="accRemove">${esc(t('accRemove'))}</button>` : ''}${st.protected && st.loggedIn ? `<button class="btn ghost" id="accLogout">${esc(t('accLogout'))}</button>` : ''}
      <span class="error" id="accErr"></span></div>
    <p class="hint">${esc(t('accNote'))}</p>`;
  const cur = () => (st.protected ? $('#accCur').value : '');
  const fail = (e) => { $('#accErr').textContent = t('failed', { err: e.message }); };
  $('#accSave').onclick = async () => {
    const a = $('#accNew').value, b = $('#accNew2').value;
    if (a !== b) { $('#accErr').textContent = t('accMismatch'); return; }
    try { await jpost('api/access/password', { current: cur(), new: a }); renderAccess(); } catch (e) { fail(e); }
  };
  if ($('#accRemove')) $('#accRemove').onclick = async () => {
    if (!confirm(t('accRemoveConfirm'))) return;
    try { await jpost('api/access/password', { current: cur(), new: '' }); renderAccess(); } catch (e) { fail(e); }
  };
  if ($('#accLogout')) $('#accLogout').onclick = async () => {
    try { await jpost('api/logout'); } catch (e) { /* the login follows anyway */ }
    location.href = '/login';
  };
}

// ---- updates
async function loadUpdates() { try { upd = await jget('api/updates'); } catch (e) { /* keep the last state */ } }
const jobActive = () => !!(upd && upd.job && (upd.job.state === 'running' || upd.job.state === 'restarting'));
const pluginUpdate = (name) => (upd && upd.targets || []).find((x) => x.target === 'plugin:' + name && x.available);
const fmtTime = (s) => { const d = new Date(s); return isNaN(d) ? String(s) : d.toLocaleString(lang); };
const logBlock = (lines, open) => (lines && lines.length ? `<details${open ? ' open' : ''} style="margin-top:8px"><summary>${esc(t('updLog'))}</summary><pre class="logs" style="margin-top:8px">${esc(lines.join('\n'))}</pre></details>` : '');

// progressHTML shows how far a running update's download or unpacking is.
function progressHTML(j) {
  if (!j || j.state !== 'running' || (j.phase !== 'download' && j.phase !== 'unpack')) return '';
  const pct = j.total ? Math.min(100, Math.round((j.done / j.total) * 100)) : 0;
  const mbf = (n) => ((n || 0) / 1048576).toLocaleString(lang, { minimumFractionDigits: 1, maximumFractionDigits: 1 });
  const eta = j.eta ? ' · ' + t('updEta', { s: j.eta }) : '';
  return `<div class="progress"><div class="spread"><span>${esc(t(j.phase === 'download' ? 'updPhaseDownload' : 'updPhaseUnpack'))}</span>`
    + `<span class="dim">${esc(t('updAmount', { done: mbf(j.done), total: mbf(j.total), pct }))}${esc(eta)}</span></div>`
    + `<div class="bar" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${pct}"><i style="width:${pct}%"></i></div></div>`;
}
// updateProgress refreshes the progress on plugin cards, which are rebuilt
// only when something else changes.
function updateProgress() {
  document.querySelectorAll('[data-progress]').forEach((el) => {
    const j = upd && upd.job;
    el.innerHTML = j && j.target === el.dataset.progress ? progressHTML(j) : '';
  });
}

function renderUpdates() {
  const card = $('#updCard');
  if (!card) return;
  const sig = JSON.stringify([upd, updChecking, lang]);
  if (sig === updSig) return;
  updSig = sig;
  let h = `<div class="card-head"><h2>${esc(t('updTitle'))}</h2></div><p class="lead">${esc(t('updDesc'))}</p>`;
  if (!upd) { card.innerHTML = h + `<p class="muted">${esc(t('offline'))}</p>`; return; }
  if (!upd.enabled) { card.innerHTML = h + `<div class="banner">${ico('info')}<div>${esc(t('updOff'))}</div></div>`; return; }
  const busy = jobActive();
  h += `<div class="cluster"><button class="btn primary" id="updCheck"${updChecking || busy ? ' disabled' : ''}>${ico('refresh', 'i-sm')}${esc(updChecking ? t('updChecking') : t('updCheck'))}</button>
    <span class="dim small">${esc(upd.checkedAt ? t('updChecked', { at: fmtTime(upd.checkedAt) }) : t('updNever'))}</span></div>`;
  h += `<label class="check small" style="margin-top:14px"><input type="checkbox" class="switch" id="updAuto"${upd.autoCheck ? ' checked' : ''}> ${esc(t('updAuto'))}</label>`;
  if (upd.error) h += `<p class="error">${esc(upd.error)}</p>`;
  const j = upd.job;
  if (j) {
    const name = j.target === 'rcplug' ? 'rcplug' : j.target.replace(/^(plugin|install):/, '');
    const msg = j.state === 'restarting' || (j.state === 'running' && j.target === 'rcplug' && (!j.phase || j.phase === 'install')) ? t('updRestarting', { v: j.version })
      : j.state === 'running' ? t('updRunning', { name, v: j.version })
      : j.state === 'done' ? t('updDone', { name, v: j.version }) : t('updFailed', { err: j.error || '?' });
    const kind = j.state === 'error' ? 'danger' : j.state === 'done' ? 'ok' : 'info';
    h += `<div class="banner ${kind}" style="margin:16px 0 0">${ico(kind === 'danger' ? 'alert' : kind === 'ok' ? 'check' : 'download')}<div style="flex:1">${esc(msg)}${progressHTML(j)}${logBlock(j.log, j.state === 'error')}</div></div>`;
  }
  if (upd.last && !(j && j.target === 'rcplug' && j.state !== 'error')) {
    const v = upd.last.from && upd.last.to ? ` (${upd.last.from} → ${upd.last.to})` : '';
    h += `<p class="${upd.last.ok ? 'ok-text' : 'error'} small" style="margin:14px 0 0">${esc(t(upd.last.ok ? 'updLastOk' : 'updLastFail', { v, at: fmtTime(upd.last.at) }))}</p>${logBlock(upd.last.log, false)}`;
  }
  if (upd.checkedAt) {
    h += '<div style="margin-top:12px">';
    if (!(upd.targets || []).length) h += `<p class="muted">${esc(t('updNoTargets'))}</p>`;
    for (const x of upd.targets || []) {
      const state = x.error ? `<span class="error">${esc(x.error)}</span>`
        : x.available ? `<span class="pill info">${esc(t('updAvail'))}</span>` : `<span class="pill ok">${esc(t('updCurrent'))}</span>`;
      h += `<div class="upd"><span class="name">${esc(x.name)}</span><span class="dim num">${esc(x.current || '?')}${x.latest && x.latest !== x.current ? ' → ' + esc(x.latest) : ''}</span>${state}
        <span class="spacer"></span>${httpsURL(x.url) ? `<a class="small" href="${esc(httpsURL(x.url))}" target="_blank" rel="noopener">${esc(t('updRelease'))} ${ico('external', 'i-sm')}</a>` : ''}
        ${x.available ? `<button class="btn primary sm" data-target="${esc(x.target)}"${busy ? ' disabled' : ''}>${esc(t('updInstall', { v: x.latest }))}</button>` : ''}
        ${x.available && x.notes ? `<details class="notes"><summary>${esc(t('updNotes'))}</summary><pre class="logs" style="margin-top:8px">${esc(x.notes)}</pre></details>` : ''}</div>`;
    }
    h += '</div>';
  }
  if (upd.keyIds && upd.keyIds.length) h += `<p class="hint">${esc(t('updKeys', { ids: upd.keyIds.join(', ') }))}</p>`;
  card.innerHTML = h;
  $('#updCheck').onclick = checkUpdates;
  $('#updAuto').onchange = async (e) => {
    try { upd = await jpost('api/updates/auto?on=' + e.target.checked); } catch (x) { toast(t('failed', { err: x.message }), true); }
    renderUpdates();
  };
  card.querySelectorAll('[data-target]').forEach((b) => (b.onclick = () => installUpdate(b.dataset.target, b)));
}

async function checkUpdates() {
  updChecking = true;
  renderUpdates();
  try { upd = await jpost('api/updates/check'); } catch (e) { if (upd) upd = { ...upd, error: t('failed', { err: e.message }) }; }
  updChecking = false;
  if (route.startsWith('system')) { renderUpdates(); renderNav(); } else renderPlugins();
}

async function installUpdate(target, btn) {
  const x = (upd && upd.targets || []).find((y) => y.target === target);
  if (!x) return;
  const q = target === 'rcplug' ? t('updConfirmHost', { v: x.latest }) : t('updConfirmPlugin', { name: x.name, v: x.latest });
  if (!confirm(q)) return;
  if (btn) btn.disabled = true;
  try { upd = await jpost('api/updates/install?target=' + encodeURIComponent(target)); }
  catch (e) { toast(t('failed', { err: e.message }), true); }
  await loadUpdates();
  if (route.startsWith('system')) renderUpdates(); else renderPlugins();
}

$('#lang span').textContent = t('switchLang');
$('#lang').addEventListener('click', () => {
  try { localStorage.setItem('rcplug-lang', lang === 'de' ? 'en' : 'de'); } catch (e) { /* ignore */ }
  location.reload();
});
$('#navOpen').setAttribute('aria-label', t('menu'));
$('#navClose').setAttribute('aria-label', t('closeMenu'));
$('#navOpen').onclick = () => setNav(true);
$('#navClose').onclick = () => setNav(false);
$('#scrim').onclick = () => setNav(false);
setNav(false);
$('#boot').textContent = t('connecting');
// Every 4 s; every second while an update is being installed, so its
// progress (and rcplug's restart) shows without delay.
(async function loop() {
  try { await refresh(); } catch (e) { console.error(e); } // a broken render must not end the polling
  finally { setTimeout(loop, jobActive() ? 1000 : 4000); }
})();
