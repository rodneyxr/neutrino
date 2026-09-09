// Command prototype serves the throwaway Neutrino TUI redesign prototype.
package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
)

//go:embed index.html
var content embed.FS

func main() {
	site, err := fs.Sub(content, ".")
	if err != nil {
		log.Fatal(err)
	}

	const address = "127.0.0.1:4173"
	log.Printf("PROTOTYPE: http://%s/?variant=A", address)
	log.Fatal(http.ListenAndServe(address, http.FileServer(http.FS(site))))
}
