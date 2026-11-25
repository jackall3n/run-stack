// Command run-stack runs a checkout as its own local stack. See README.md.
package main

import (
	"os"

	"github.com/jackall3n/run-stack/internal/cli"
)

// version is replaced by the release build.
var version = "dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], version))
}
