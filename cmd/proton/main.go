// Package main is the entry point for the proton-cli application.
package main

import (
	cli "github.com/major0/proton-utils/internal/cli"
	_ "github.com/major0/proton-utils/internal/cli/account"

	// _ "github.com/major0/proton-utils/cmd/calendar"
	_ "github.com/major0/proton-utils/internal/cli/config"
	_ "github.com/major0/proton-utils/internal/cli/drive"
	_ "github.com/major0/proton-utils/internal/cli/drive/share"
	_ "github.com/major0/proton-utils/internal/cli/lumo"
	// _ "github.com/major0/proton-utils/cmd/wallet"
)

func main() {
	cli.Execute()
}
