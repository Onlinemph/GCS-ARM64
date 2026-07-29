// Command gcsweb serves GURPS Character Sheet (.gcs) files as mobile-friendly
// web pages.
//
// It is a thin server around the upstream GCS model packages: sheets are
// loaded with gurps.NewEntityFromFile, fully recalculated by the upstream
// rules engine, and rendered through upstream's own export-template pipeline.
// No GCS source is forked or patched.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	library := flag.String("library", "samples", "directory scanned for .gcs files")
	maxUpload := flag.Int64("max-upload", 8<<20, "maximum upload size in bytes")
	flag.Parse()

	srv, err := newServer(*library, *maxUpload)
	if err != nil {
		log.Fatalf("startup: %v", err)
	}
	defer srv.close()

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("gcsweb listening on %s (library: %s)", *addr, *library)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	fmt.Println()
	log.Println("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
