//go:build !profile

package cli

// RegisterProfileFlag is a no-op when built without the profile tag.
func RegisterProfileFlag() {}

// StartProfile is a no-op when built without the profile tag.
func StartProfile() func() { return func() {} }
