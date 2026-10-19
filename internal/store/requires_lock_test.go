//go:build darwin || linux

// These fixtures verify that version policy is read after acquiring the lock.
// They do not synchronize config edits made after the lock has been acquired.
package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/buildinfo"
)

// Done is consulted when admission encounters an already-held lock, letting
// the test edit config after admission has started without relying on sleeps.
type lockWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *lockWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestRequiresRaisedWhileAdmissionWaitsForLock(t *testing.T) {
	p := ledgerProject(t)
	old := buildinfo.Version
	buildinfo.Version = func() string { return "v0.1.7" }
	t.Cleanup(func() { buildinfo.Version = old })
	config := filepath.Join(p.Root, "whosaidso.toml")
	putConfig := func(version string) {
		t.Helper()
		body := "id = 'team/project'\nledger = '.whosaidso/events'\nrequires = '" + version + "'\n"
		if err := os.WriteFile(config, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	putConfig("v0.1.7")
	var err error
	p, err = Discover(p.Root)
	if err != nil {
		t.Fatal(err)
	}
	admitControl(t, p, 1)
	lock, err := lockAcquire(filepath.Join(p.Ledger, lockName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if lock != nil {
			_ = lockRelease(lock)
		}
	}()
	deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx := &lockWaitContext{Context: deadline, waiting: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		_, err := Transact(ctx, p, admissionID(2), digestFor(2), proposeFor(2))
		result <- err
	}()
	select {
	case <-ctx.waiting:
	case <-deadline.Done():
		t.Fatal("admission never waited for the lock")
	}
	putConfig("v0.1.8")
	if err := lockRelease(lock); err != nil {
		t.Fatal(err)
	}
	lock = nil
	err = <-result
	if err == nil || !strings.Contains(err.Error(), "version-required") || !strings.Contains(err.Error(), "v0.1.8") {
		t.Fatalf("admission ignored requires raised while waiting: %v", err)
	}
	readControl(t, p, 1)
}
