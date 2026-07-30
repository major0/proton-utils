package drive

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

// TestNoPlaintextMetadataAtRest_Property is the security-critical guarantee
// that mode and all POSIX metadata exist ONLY inside the encrypted, armored
// XAttr blob — never as plaintext in the request payload that is sent to (and
// persisted by) the API. It mirrors the commit path: build a *RevisionXAttr
// via the package's own setPosixXAttr, then encrypt it exactly as
// commitRevisionFromTokens does through proton.UpdateRevisionReq.SetEncXAttrString.
//
// The property established: for any recognizable mode plus arbitrary sibling
// sections written by other Proton clients, the resulting UpdateRevisionReq
//
//   - carries the metadata solely on the PGP-armored XAttr field,
//   - leaks no plaintext marker (POSIX key, Mode key, the mode's plaintext
//     value fragment, or a sibling's distinctive content) into that field or
//     any other request field, and
//   - yields those exact values back only via decryption — proving the data is
//     present but encrypted.
//
// Every negative marker embeds a byte that is absent from the base64 armor
// alphabet (a double quote or an underscore), so a match can never occur by
// coincidence inside the ciphertext body — the assertions are deterministic
// rather than probabilistic. The bare decimal of the mode is intentionally
// NOT searched on its own: base64 armor contains digits, so a naked 3–4 digit
// substring would collide by chance. The exact plaintext carrier `"Mode":<n>`
// (quote + colon, neither a base64 byte) is searched instead — the cleanest
// deterministic form of the same guarantee.
//
// **Property 4: No plaintext metadata at rest**
// **Validates: Requirements 7.1, 7.2, 7.3, 7.5**
func TestNoPlaintextMetadataAtRest_Property(t *testing.T) {
	// Generate the keyring once (expensive RSA keygen) — reused across
	// property iterations, matching the other XAttr property tests.
	kr := xattrTestKeyRing(t)

	// siblingSentinel is a distinctive value for an unmodeled sibling section.
	// The underscores guarantee it can never appear in the base64 armor body,
	// so its absence check is deterministic.
	const siblingSentinel = "SIBLING_SENTINEL_MEDIA_do_not_leak_at_rest"

	rapid.Check(t, func(t *rapid.T) {
		// Arbitrary unmodeled sibling sections written by other Proton clients.
		// Drop any stray "Common" (owned by the typed field, not a sibling).
		siblings := genSiblingSections(t)
		delete(siblings, "Common")
		// Inject one sibling with a distinctive, collision-free value so we can
		// assert sibling content is also never exposed in plaintext.
		siblings["Media"] = json.RawMessage(`{"tag":"` + siblingSentinel + `"}`)

		// A non-zero, recognizable Unix mode (lower 12 bits).
		mode := rapid.Uint32Range(1, 0o7777).Draw(t, "mode")

		// Build the RevisionXAttr exactly as the write path does: a fresh
		// Common, inherited sibling sections, and the mode under POSIX via the
		// package's own setPosixXAttr helper.
		x := &proton.RevisionXAttr{
			Common: proton.RevisionXAttrCommon{
				ModificationTime: "2024-06-01T12:30:00+0000",
				Size:             rapid.Int64Range(0, 1<<40).Draw(t, "size"),
				BlockSizes:       []int64{1, 2, 3},
			},
			Extra: make(map[string]json.RawMessage, len(siblings)),
		}
		for k, v := range siblings {
			x.Extra[k] = v
		}
		setPosixXAttr(x, PosixXAttr{Mode: mode})

		// Sanity: the PLAINTEXT marshaling really does carry every marker we
		// claim is hidden. Without this, the negative assertions below would be
		// vacuously true if the fixture stopped embedding the metadata.
		plain, err := json.Marshal(x)
		if err != nil {
			t.Fatalf("marshal plaintext xattr: %v", err)
		}
		modeFragment := fmt.Sprintf(`"Mode":%d`, mode)
		plainStr := string(plain)
		for _, want := range []string{`"POSIX"`, modeFragment, siblingSentinel} {
			if !strings.Contains(plainStr, want) {
				t.Fatalf("fixture plaintext missing %q — negative checks would be vacuous", want)
			}
		}

		// Encrypt exactly as commitRevisionFromTokens does.
		var req proton.UpdateRevisionReq
		if err := req.SetEncXAttrString(kr, kr, x); err != nil {
			t.Fatalf("SetEncXAttrString: %v", err)
		}

		// The metadata's only carrier is the XAttr field, and it is PGP-armored.
		if req.XAttr == "" {
			t.Fatal("req.XAttr is empty after SetEncXAttrString")
		}
		if !strings.HasPrefix(req.XAttr, "-----BEGIN PGP MESSAGE-----") {
			t.Fatalf("req.XAttr is not PGP-armored: %.48q", req.XAttr)
		}

		// Collision-free plaintext markers: the POSIX key, the Mode key, the
		// mode's exact plaintext value fragment, the sibling sentinel, and every
		// sibling key in its quoted plaintext form. Each contains a double quote
		// or underscore, neither of which is a base64 byte, so none can appear
		// inside the armored ciphertext by chance.
		markers := []string{`"POSIX"`, `"Mode"`, modeFragment, siblingSentinel}
		for k := range siblings {
			markers = append(markers, `"`+k+`"`)
		}

		// No marker leaks into ANY field of the request payload. Only the
		// encrypted XAttr carries the metadata, and there it must be ciphertext.
		fields := map[string]string{
			"XAttr":             req.XAttr,
			"ManifestSignature": req.ManifestSignature,
			"SignatureAddress":  req.SignatureAddress,
		}
		for name, val := range fields {
			for _, m := range markers {
				if strings.Contains(val, m) {
					t.Fatalf("plaintext marker %q leaked into UpdateRevisionReq.%s", m, name)
				}
			}
		}

		// The plaintext is recoverable ONLY via decryption: decrypting req.XAttr
		// with the keyring yields the mode and sibling content back, proving the
		// data is present but encrypted at rest.
		rev := proton.RevisionMetadata{XAttr: req.XAttr}
		dec, err := rev.GetDecXAttrString(kr, kr)
		if err != nil {
			t.Fatalf("GetDecXAttrString: %v", err)
		}
		if dec == nil {
			t.Fatal("GetDecXAttrString returned nil for a non-empty XAttr")
			return
		}
		gotPosix := posixFromXAttr(dec)
		if gotPosix == nil {
			t.Fatal("decrypted XAttr carries no POSIX section")
			return
		}
		if gotPosix.Mode != mode {
			t.Fatalf("decrypted POSIX mode = %d, want %d", gotPosix.Mode, mode)
		}
		media, ok := dec.Extra["Media"]
		if !ok {
			t.Fatal("decrypted XAttr lost the Media sibling section")
		}
		if !strings.Contains(string(media), siblingSentinel) {
			t.Fatalf("decrypted Media section missing sentinel: %s", media)
		}
	})
}
