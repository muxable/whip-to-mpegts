package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/muxable/whip-to-mpegts/pkg/whiptompegts"
)

func main() {
	// runs an example WHIP server that converts the first incoming stream to mpegts and writes it to stdout
	s := whiptompegts.NewServer(func(id string, data io.Reader) {
		// write id to stderr
		fmt.Fprintln(os.Stderr, "received stream", id)
		// write the mpegts stream to stdout
		io.Copy(os.Stdout, data)
	})

	http.HandleFunc("/", s.Handler)

	log.Printf("listening on :8080")

	log.Fatal(http.ListenAndServe(":8080", nil))
}
