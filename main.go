package main

import (
	"embed"
	"log"

	"github.com/RCooLeR/Cairn/internal/shell"
)

//go:embed all:frontend/dist assets/cairn-icon.png
var assets embed.FS

func main() {
	if err := shell.Run(assets); err != nil {
		log.Fatal(err)
	}
}
