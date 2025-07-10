package main

import (
	"fmt"
	"log"
	"os"

	"github.com/inngest/inngestgo"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatalf("Usage: %s <version>", os.Args[0])
	}
	version := os.Args[1]
	if version == "" {
		log.Fatalf("Version is not set")
	}

	codeVersion := "v" + inngestgo.SDKVersion
	fmt.Println("Release version:", version)
	fmt.Println("Code version:", codeVersion)

	if version != codeVersion {
		log.Fatalf("Version is not set to the correct version")
	}

	fmt.Println("Version matches value in code")
}
