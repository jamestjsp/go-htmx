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

	"github.com/jamestjsp/process-lab/internal/studio"
	"github.com/jamestjsp/process-lab/internal/web"
)

func main() {
	var (
		address = flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
		dbPath  = flag.String("db", "processlab.db", "SQLite database path")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	service, err := studio.Open(ctx, *dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer service.Close()

	app, err := web.New(service)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr:              *address,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	fmt.Printf("Process Lab is running at http://%s\n", *address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
