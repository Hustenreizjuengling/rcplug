# Third-party notices

rcplug's programs use only the Go standard library and ship no third-party code. What
it builds on is listed here.

## Duo state-tree fixture

`internal/rcsim/fixture/duo-fw1.7.4-full-sync.frame` is a sanitized capture
from a RØDECaster Duo, taken unchanged from
[rodecaster-protocol](https://github.com/Yeradon/rodecaster-protocol)
(`tests/fixtures/duo/fw-1.7.4`), © 2026 Yeradon, MIT License. The full licence
text is in `internal/rcsim/fixture/NOTICE`.

## Protocol knowledge

The wire format of the desk's control port is the JUCE `ValueTreeSynchroniser`
format. rcplug's encoder and decoder (`internal/rcproto`) are an independent
implementation written from the format's description and from captures; no
JUCE code is included.

Facts about the protocol, the device and its property names were also learned
from these public projects and write-ups. No code was copied from them:

- [rodecaster-protocol](https://github.com/Yeradon/rodecaster-protocol) by Yeradon (see above)
- [companion-module-rode-rodecaster2](https://github.com/gcdkgrnvle/companion-module-rode-rodecaster2)
- [rodey](https://github.com/seanheiney/rodey)
- [rodecasterpro2-linux](https://github.com/cristalcorp/rodecasterpro2-linux)
- [rc2http](https://github.com/phihag/rc2http) by phihag (front-panel codes, device layout)
- the [hhh.hn](https://hhh.hn/) blog posts on the RØDECaster Duo

## Trademarks

RØDE, RØDECaster and RØDECaster Duo are trademarks of Freedman Electronics
Pty Ltd. rcplug is not affiliated with, endorsed by or supported by RØDE.
They are used here only to describe what rcplug works with.
