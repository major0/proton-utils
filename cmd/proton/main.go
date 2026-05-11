// Package main is the entry point for the proton-cli application.
package main

import (
	_ "github.com/major0/proton-cli/cmd/account"
	cli "github.com/major0/proton-cli/internal/cli"

	// _ "github.com/major0/proton-cli/cmd/calendar"
	_ "github.com/major0/proton-cli/cmd/config"
	_ "github.com/major0/proton-cli/cmd/drive"
	_ "github.com/major0/proton-cli/cmd/drive/share"
	_ "github.com/major0/proton-cli/cmd/lumo"
	// _ "github.com/major0/proton-cli/cmd/wallet"
)

func main() {
	cli.Execute()
}
