package account

import (
	"math"

	"github.com/ProtonMail/go-proton-api"
)

// User is an opaque wrapper around the Proton user profile.
// Consumers access fields via methods — they cannot construct
// or inspect the struct directly.
type User struct {
	raw proton.User
}

// newUser wraps a proton.User in an opaque User value.
func newUser(u proton.User) User { return User{raw: u} }

// safeInt64 converts a uint64 to int64, capping at math.MaxInt64.
func safeInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// ID returns the Proton user ID.
func (u User) ID() string { return u.raw.ID }

// Name returns the Proton username.
func (u User) Name() string { return u.raw.Name }

// DisplayName returns the user's display name.
func (u User) DisplayName() string { return u.raw.DisplayName }

// Email returns the user's primary email address.
func (u User) Email() string { return u.raw.Email }

// UsedSpace returns the total storage used in bytes.
func (u User) UsedSpace() int64 { return safeInt64(u.raw.UsedSpace) }

// MaxSpace returns the total storage quota in bytes.
func (u User) MaxSpace() int64 { return safeInt64(u.raw.MaxSpace) }

// MailUsedSpace returns storage used by Mail in bytes.
func (u User) MailUsedSpace() int64 { return safeInt64(u.raw.ProductUsedSpace.Mail) }

// DriveUsedSpace returns storage used by Drive in bytes.
func (u User) DriveUsedSpace() int64 { return safeInt64(u.raw.ProductUsedSpace.Drive) }

// CalendarUsedSpace returns storage used by Calendar in bytes.
func (u User) CalendarUsedSpace() int64 { return safeInt64(u.raw.ProductUsedSpace.Calendar) }

// PassUsedSpace returns storage used by Pass in bytes.
func (u User) PassUsedSpace() int64 { return safeInt64(u.raw.ProductUsedSpace.Pass) }

// ContactUsedSpace returns storage used by Contacts in bytes.
func (u User) ContactUsedSpace() int64 { return safeInt64(u.raw.ProductUsedSpace.Contact) }
