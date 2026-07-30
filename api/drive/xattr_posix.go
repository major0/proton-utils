package drive

import (
	"encoding/json"

	"github.com/ProtonMail/go-proton-api"
)

// posixXAttrKey is the single top-level XAttr section key that proton-utils
// owns. All proton-utils POSIX filesystem metadata lives under this key so it
// never collides with sections written by other Proton clients (Media,
// Camera, Location).
const posixXAttrKey = "POSIX"

// PosixXAttr is the typed shape of the "POSIX" top-level XAttr section — a
// generic namespace for POSIX filesystem metadata that any application
// interoperating with these files may read and write. It is serialized into
// the RevisionXAttr Extra bag under posixXAttrKey so it never collides with
// sections written by other Proton clients. All fields are omitempty: a file
// with no POSIX metadata produces no "POSIX" section at all. This is the
// extension point for future POSIX metadata.
type PosixXAttr struct {
	Mode uint32 `json:"Mode,omitempty"` // Unix permission bits (lower 12: 0o7777)
}

// posixFromXAttr extracts the POSIX section from a decoded RevisionXAttr,
// reading only Extra[posixXAttrKey]. It returns nil (without error) when x is
// nil, x.Extra is nil, the POSIX section is absent, or the section is
// malformed JSON — callers fall back to default permissions in those cases.
func posixFromXAttr(x *proton.RevisionXAttr) *PosixXAttr {
	if x == nil || x.Extra == nil {
		return nil
	}
	raw, ok := x.Extra[posixXAttrKey]
	if !ok {
		return nil
	}
	var pfs PosixXAttr
	if err := json.Unmarshal(raw, &pfs); err != nil {
		return nil
	}
	return &pfs
}

// setPosixXAttr marshals pfs and stores it into x.Extra[posixXAttrKey],
// leaving every sibling Extra section unchanged. When pfs marshals to the
// empty object "{}" (all fields omitempty-elided), it returns without adding,
// modifying, or clearing the POSIX key — section-level omitempty. Otherwise it
// allocates x.Extra when nil and replaces only the POSIX key.
func setPosixXAttr(x *proton.RevisionXAttr, pfs PosixXAttr) {
	raw, err := json.Marshal(pfs)
	if err != nil {
		return
	}
	if string(raw) == "{}" {
		return // section-level omitempty: do not create or clear the key
	}
	if x.Extra == nil {
		x.Extra = make(map[string]json.RawMessage, 1)
	}
	x.Extra[posixXAttrKey] = raw
}
