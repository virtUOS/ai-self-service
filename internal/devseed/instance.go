// Package devseed runs an app instance of its own against the dev stack and
// fills it with users, profiles, keys and usage, so the dashboard and the
// admin panel have something to show. `make seed` uses it for looking around,
// and the browser tests to have something to test.
//
// The instance is separate from the developer's own: it has its own directory,
// database and port, and names its users in LiteLLM with a prefix of its own,
// so it changes neither .env nor data.db nor anything already in the gateway.
package devseed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Instance is a server built from this checkout and run from Dir.
type Instance struct {
	// Dir holds the binary, .env, the database and the server's log.
	Dir string
	// Addr is where the server listens, such as 127.0.0.1:8090. 127.0.0.1
	// rather than localhost keeps its cookies apart from those of a server on
	// localhost:8080, since cookies are scoped by host and not by port.
	Addr string
	// Prefix names the instance's users in LiteLLM.
	Prefix string
	// Env overrides further variables, e.g. SMTP_HOST= to send no mail.
	Env map[string]string

	root string
	cmd  *exec.Cmd
	done chan error
	log  *os.File
}

// NewInstance prepares an instance in dir, which must not exist yet or be
// empty, and builds the server into it.
func NewInstance(dir, addr string) (*Instance, error) {
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	inst := &Instance{
		Dir:    dir,
		Addr:   addr,
		Prefix: fmt.Sprintf("seed-%d", time.Now().Unix()),
		Env:    map[string]string{},
		root:   root,
	}

	// The app reads .env from its working directory; .env.example is preset
	// for the dev stack.
	example, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), example, 0o600); err != nil {
		return nil, err
	}

	build := exec.Command("go", "build", "-o", inst.binary(), "./cmd/server")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build the server: %v\n%s", err, out)
	}
	return inst, nil
}

// URL is the instance's base URL.
func (i *Instance) URL() string { return "http://" + i.Addr }

// DBPath is the instance's database.
func (i *Instance) DBPath() string { return filepath.Join(i.Dir, "seed.db") }

// LogPath is where the server's output goes.
func (i *Instance) LogPath() string { return filepath.Join(i.Dir, "server.log") }

// MockUsersPath is the users file the OIDC mock serves.
func (i *Instance) MockUsersPath() string { return filepath.Join(i.root, "dev", "mock-users.json") }

func (i *Instance) binary() string { return filepath.Join(i.Dir, "keyosk") }

// Start runs the server and waits until it is ready.
func (i *Instance) Start(ctx context.Context) error {
	if i.cmd != nil {
		return errors.New("instance already running")
	}
	// A server left from an earlier run would answer the readiness check in
	// this one's place.
	l, err := net.Listen("tcp", i.Addr)
	if err != nil {
		return fmt.Errorf("%s is in use; is an earlier seed still running? %w", i.Addr, err)
	}
	l.Close()

	logFile, err := os.OpenFile(i.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	env := map[string]string{
		"LISTEN_ADDR":            i.Addr,
		"FRONTEND_URL":           i.URL(),
		"OIDC_REDIRECT_URL":      i.URL() + "/callback",
		"DB_PATH":                i.DBPath(),
		"LITELLM_USER_ID_PREFIX": i.Prefix,
		"PRIVACY_NOTICE_FILE_DE": filepath.Join(i.root, "dev", "seed", "privacy.de.html"),
		"PRIVACY_NOTICE_FILE_EN": filepath.Join(i.root, "dev", "seed", "privacy.en.html"),
	}
	for k, v := range i.Env {
		env[k] = v
	}
	cmd := exec.Command(i.binary())
	cmd.Dir = i.Dir
	// The last value of a variable wins, so these override the caller's
	// environment, and both override .env, which the app loads without
	// replacing what is set.
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("start the server: %w", err)
	}
	i.cmd, i.log, i.done = cmd, logFile, make(chan error, 1)
	go func() { i.done <- cmd.Wait() }()

	if err := i.waitReady(ctx); err != nil {
		_ = i.Stop()
		return err
	}
	return nil
}

func (i *Instance) waitReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		select {
		case err := <-i.done:
			i.done <- err
			return fmt.Errorf("the server exited (%v); see %s", err, i.LogPath())
		default:
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, i.URL()+"/readyz", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the server did not become ready; see %s", i.LogPath())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Stop ends the server and waits for it to exit.
func (i *Instance) Stop() error {
	if i.cmd == nil {
		return nil
	}
	defer func() {
		i.log.Close()
		i.cmd = nil
	}()
	_ = i.cmd.Process.Signal(os.Interrupt)
	select {
	case <-i.done:
	case <-time.After(10 * time.Second):
		_ = i.cmd.Process.Kill()
		<-i.done
	}
	return nil
}

// Restart stops and starts the server again. In between the in-memory caches
// empty, and the jobs that run at startup run again.
func (i *Instance) Restart(ctx context.Context) error {
	if err := i.Stop(); err != nil {
		return err
	}
	return i.Start(ctx)
}

// moduleRoot is the directory of go.mod, so the seed works from wherever it
// runs: the repository for `make seed`, a package directory for a test.
func moduleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", errors.New("not inside the keyosk module")
	}
	return filepath.Dir(gomod), nil
}
