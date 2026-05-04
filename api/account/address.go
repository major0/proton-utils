package account

import "github.com/ProtonMail/go-proton-api"

// Address is an opaque wrapper around a Proton email address.
// Consumers access fields via methods — they cannot construct
// or inspect the struct directly.
type Address struct {
	raw proton.Address
}

// newAddress wraps a proton.Address in an opaque Address value.
func newAddress(a proton.Address) Address { return Address{raw: a} }

// ID returns the Proton address ID.
func (a Address) ID() string { return a.raw.ID }

// Email returns the email address string.
func (a Address) Email() string { return a.raw.Email }

// Status returns the address status as an integer.
func (a Address) Status() int { return int(a.raw.Status) }

// Type returns the address type as an integer.
func (a Address) Type() int { return int(a.raw.Type) }

// Order returns the address display order.
func (a Address) Order() int { return a.raw.Order }
