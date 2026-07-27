// Command leanctl is the LeanSignal command-line client.
//
// It authenticates with a personal access token and acts with the token
// owner's identity and role against one tenant's lean-api.
package main

import (
	"os"

	"github.com/leansignal/lean-cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
