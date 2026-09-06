package main

import (
	"github.com/fallais/cargo/cmd"
)

// version is stamped by the release build:
//
//	go build -ldflags "-X main.version=$(git describe --tags)"
var version = "dev"

func main() {
	cmd.SetVersion(version)
	cmd.Execute()
}
