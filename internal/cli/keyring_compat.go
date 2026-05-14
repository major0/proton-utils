package cli

import "github.com/major0/proton-utils/internal/keyring"

// appName is kept for backward compatibility with existing tests.
const appName = "proton-utils"

// keyringService is re-exported from internal/keyring for backward compatibility
// with existing tests.
const keyringService = keyring.KeyringService
