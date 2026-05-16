package main

import (
	"io"
	"log"
	"os"

	"github.com/btwiuse/hush"
)

func main() {
	log.SetOutput(io.Discard)
	exitCode := hush.Run()
	os.Exit(exitCode)
}
