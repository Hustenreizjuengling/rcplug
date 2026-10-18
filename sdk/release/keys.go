package release

// BuiltinKeys are the public keys whose signatures rcplugd accepts for
// updates of rcplug itself and of plugins that name no key of their own.
// The matching private key signs releases in the release workflows of
// rcplug and rcplug-plugins (repository secret RCPLUG_SIGNING_KEY). To
// replace the key, add the new one here, release that version signed with
// the old key, and only then sign with the new one.
var BuiltinKeys = []string{
	"t0taYpU8zSZTLoPkCf6aoM4GQODUrrUoJPOJWVsTnUs=", // id efc94762, created 2026-10-18
}
