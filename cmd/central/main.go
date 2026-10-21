package main

import (
	"os"

	"github.com/JoaoVitorSantDrade/central/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout))
}
