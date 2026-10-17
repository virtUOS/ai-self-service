// Command seed runs an app instance of its own against the dev stack, fills it
// with users, profiles, keys and usage, and keeps it running until Ctrl-C:
//
//	make seed
//
// It starts from scratch each time. What the previous run created in LiteLLM
// is deleted first, and its directory is replaced. The developer's own .env,
// database and server are left alone.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"

	"github.com/virtuos/keyosk/internal/devseed"
)

func main() {
	dir := flag.String("dir", "dev/.seed", "directory for the instance's binary, database and log")
	addr := flag.String("addr", "127.0.0.1:8090", "address the instance listens on")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *dir, *addr); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dir, addr string) error {
	if err := forgetPreviousRun(ctx, dir); err != nil {
		return err
	}
	inst, err := devseed.NewInstance(dir, addr)
	if err != nil {
		return err
	}
	defer inst.Stop()

	m, err := devseed.Seed(ctx, inst, os.Stdout)
	if err != nil {
		return err
	}
	if err := m.Write(manifestPath(dir)); err != nil {
		return err
	}

	fmt.Printf("\nSeeded. Open %s and sign in as one of these:\n\n", m.URL)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  SUBJECT\tPROFILE\tSHOWS")
	for _, u := range m.Users {
		fmt.Fprintf(w, "  %s\t%s\t%s\n", u.Sub, u.Profile, u.Shows)
	}
	w.Flush()
	fmt.Printf("\nMail goes to http://localhost:8025. Server log: %s\nCtrl-C stops the server.\n", inst.LogPath())

	<-ctx.Done()
	fmt.Println("\nStopping.")
	return nil
}

func manifestPath(dir string) string { return filepath.Join(dir, "seed.json") }

// forgetPreviousRun deletes what the previous run created in LiteLLM and then
// its directory. A run whose manifest is gone can only be cleaned up by
// resetting the stack; its users never collide with a new run's, whose prefix
// is new.
func forgetPreviousRun(ctx context.Context, dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	m, err := devseed.ReadManifest(manifestPath(dir))
	if err == nil {
		err = devseed.Cleanup(ctx, filepath.Join(dir, "seed.db"), m.Prefix)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "seed: could not remove the previous run from LiteLLM, going on: %v\n", err)
	}
	return os.RemoveAll(dir)
}
