package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/dullkingsman/dpg-lsp/internal/server"
	"github.com/dullkingsman/dpg-lsp/internal/version"
)

func main() {
	tcpAddr := flag.String("tcp", "", "listen on TCP address instead of stdio (e.g. :7777)")
	flag.Bool("stdio", true, "use stdio transport (default)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("dpg-lsp %s (%s)\n", version.Version, version.Commit)
		return
	}

	var err error
	if *tcpAddr != "" {
		err = server.RunTCP(*tcpAddr)
	} else {
		err = server.RunStdio()
	}
	if err != nil {
		log.Fatalf("dpg-lsp: %v", err)
	}
}
