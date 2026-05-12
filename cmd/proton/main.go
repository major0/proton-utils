// Package main is the entry point for the proton-cli application.
package main

import (
	cli "github.com/major0/proton-cli/internal/cli"
	_ "github.com/major0/proton-cli/internal/cli/account"

	// _ "github.com/major0/proton-cli/cmd/calendar"
	_ "github.com/major0/proton-cli/internal/cli/config"
	_ "github.com/major0/proton-cli/internal/cli/drive"
	_ "github.com/major0/proton-cli/internal/cli/drive/share"
	_ "github.com/major0/proton-cli/internal/cli/lumo"
	// _ "github.com/major0/proton-cli/cmd/wallet"
)

func main() {
	cli.Execute()
}
