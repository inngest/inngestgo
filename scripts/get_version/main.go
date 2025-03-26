package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/inngest/inngestgo"
)

func main() {
	v := inngestgo.SDKVersion
	if strings.HasPrefix(v, "v") {
		// We add the 'v' prefix in the tag elsewhere, so we don't want it in
		// the version const.
		log.Fatal("Version should not start with 'v'")
	}

	_, err := version.NewVersion(v)
	if err != nil {
		log.Fatalf("Version is not a valid semantic version: %s", err)
	}
	fmt.Print(v)
}
