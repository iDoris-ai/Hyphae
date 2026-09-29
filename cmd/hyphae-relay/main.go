package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/iDoris-ai/hyphae/internal/relay"
)

func main() {
	listen := flag.String("listen", "127.0.0.1", "IP address to bind")
	dataDir := flag.String("data-dir", filepath.Join("build", "relay-data"), "private directory for persistent relay data")
	port := flag.Int("port", 3334, "TCP port")
	public := flag.Bool("public", false, "allow listening on a non-loopback address")
	flag.Parse()

	cfg, err := relay.ParseConfig(*listen, *dataDir, *port, *public)
	if err != nil {
		log.Fatal(err)
	}
	handler, closeStore, err := relay.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer closeStore()

	srv := &http.Server{Addr: cfg.Address, Handler: handler}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		if err := srv.Close(); err != nil {
			log.Printf("stop relay: %v", err)
		}
	}()
	fmt.Printf("Hyphae relay listening on ws://%s\n", cfg.Address)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
