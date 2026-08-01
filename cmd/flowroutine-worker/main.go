package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"flowroutine/internal/distributed"
)

func main() {
	var (
		listen        = flag.String("listen", "127.0.0.1:9443", "worker HTTPS listen address")
		workerID      = flag.String("worker-id", defaultWorkerID(), "stable worker identity")
		certificate   = flag.String("tls-cert", "", "worker TLS certificate PEM")
		privateKey    = flag.String("tls-key", "", "worker TLS private key PEM")
		coordinatorCA = flag.String("client-ca", "", "CA PEM used to authenticate coordinators")
	)
	flag.Parse()
	if *certificate == "" || *privateKey == "" || *coordinatorCA == "" {
		log.Fatal("-tls-cert, -tls-key, and -client-ca are required")
	}

	handler, err := distributed.NewWorkerServer(*workerID)
	if err != nil {
		log.Fatal(err)
	}
	tlsConfig, err := distributed.LoadServerTLSConfig(*certificate, *privateKey, *coordinatorCA)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("FlowRoutine worker %s listening on https://%s", *workerID, *listen)
		serverErrors <- server.ListenAndServeTLS("", "")
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case signal := <-stop:
		log.Printf("received %s; stopping worker", signal)
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
		return
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		log.Printf("worker shutdown failed: %v", err)
	}
}

func defaultWorkerID() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return "flowroutine-worker"
	}
	return hostname
}
