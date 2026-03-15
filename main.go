package main

import (
	"fmt"
	"os"

	"github.com/acm-gaming/beammp-deploy/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
