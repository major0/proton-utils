package account

import (
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

// genProductUsedSpace generates a random proton.ProductUsedSpace with
// arbitrary uint64 field values.
func genProductUsedSpace(t *rapid.T) proton.ProductUsedSpace {
	return proton.ProductUsedSpace{
		Calendar: rapid.Uint64().Draw(t, "calendar"),
		Contact:  rapid.Uint64().Draw(t, "contact"),
		Drive:    rapid.Uint64().Draw(t, "drive"),
		Mail:     rapid.Uint64().Draw(t, "mail"),
		Pass:     rapid.Uint64().Draw(t, "pass"),
	}
}

// genUser generates a random proton.User with arbitrary field values.
// Keys are left at zero value because the User wrapper does not expose
// them — only the fields surfaced by accessor methods are varied.
func genUser(t *rapid.T) proton.User {
	return proton.User{
		ID:               rapid.String().Draw(t, "id"),
		Name:             rapid.String().Draw(t, "name"),
		DisplayName:      rapid.String().Draw(t, "displayName"),
		Email:            rapid.String().Draw(t, "email"),
		UsedSpace:        rapid.Uint64().Draw(t, "usedSpace"),
		MaxSpace:         rapid.Uint64().Draw(t, "maxSpace"),
		ProductUsedSpace: genProductUsedSpace(t),
	}
}

// TestUserWrapper_FieldPreservation_Property verifies that for any valid
// proton.User value, wrapping it in account.User via newUser() and
// accessing each field via the corresponding accessor method returns the
// same value as the original proton.User field.
//
// **Validates: Requirements 4.5**
func TestUserWrapper_FieldPreservation_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		raw := genUser(t)
		u := newUser(raw)

		if got, want := u.ID(), raw.ID; got != want {
			t.Fatalf("ID(): got %q, want %q", got, want)
		}
		if got, want := u.Name(), raw.Name; got != want {
			t.Fatalf("Name(): got %q, want %q", got, want)
		}
		if got, want := u.DisplayName(), raw.DisplayName; got != want {
			t.Fatalf("DisplayName(): got %q, want %q", got, want)
		}
		if got, want := u.Email(), raw.Email; got != want {
			t.Fatalf("Email(): got %q, want %q", got, want)
		}
		if got, want := u.UsedSpace(), safeInt64(raw.UsedSpace); got != want {
			t.Fatalf("UsedSpace(): got %d, want %d", got, want)
		}
		if got, want := u.MaxSpace(), safeInt64(raw.MaxSpace); got != want {
			t.Fatalf("MaxSpace(): got %d, want %d", got, want)
		}
		if got, want := u.MailUsedSpace(), safeInt64(raw.ProductUsedSpace.Mail); got != want {
			t.Fatalf("MailUsedSpace(): got %d, want %d", got, want)
		}
		if got, want := u.DriveUsedSpace(), safeInt64(raw.ProductUsedSpace.Drive); got != want {
			t.Fatalf("DriveUsedSpace(): got %d, want %d", got, want)
		}
		if got, want := u.CalendarUsedSpace(), safeInt64(raw.ProductUsedSpace.Calendar); got != want {
			t.Fatalf("CalendarUsedSpace(): got %d, want %d", got, want)
		}
		if got, want := u.PassUsedSpace(), safeInt64(raw.ProductUsedSpace.Pass); got != want {
			t.Fatalf("PassUsedSpace(): got %d, want %d", got, want)
		}
		if got, want := u.ContactUsedSpace(), safeInt64(raw.ProductUsedSpace.Contact); got != want {
			t.Fatalf("ContactUsedSpace(): got %d, want %d", got, want)
		}
	})
}
