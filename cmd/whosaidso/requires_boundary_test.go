// Version refusals are exercised through storage, without command dispatch.
// These fixtures do not cover commands absent from this checkout.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func TestRequiresStoreCapture(t *testing.T) {
	requiresBuild(t, "v0.1.7")
	t.Setenv("WHOSAIDSO_HOME", t.TempDir())
	root, input := cliFixture(t)
	requiresConfig(t, root, "v0.1.7")
	p, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	var events []model.Event
	if err := json.Unmarshal(input, &events); err != nil {
		t.Fatal(err)
	}
	request := store.IntakeRequest{Author: model.Actor{ID: "worker"}, Events: events}
	if _, err := store.WriteIntake(context.Background(), p, request); err != nil {
		t.Fatalf("good control: %v", err)
	}
	requiresConfig(t, root, "v0.1.8")
	p, err = store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	request.CommandID = cliID(888)
	_, err = store.WriteIntake(context.Background(), p, request)
	requireVersionRefusal(t, err)
	inbox, err := store.IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inbox, string(request.CommandID))); !os.IsNotExist(err) {
		t.Fatalf("refused packet published: %v", err)
	}
}

func requireVersionRefusal(t *testing.T, err error) {
	t.Helper()
	var fault *model.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("want store refusal, got %v", err)
	}
	for _, want := range []string{"v0.1.7", "v0.1.8", "go install github.com/alex2481kobe/whosaidso/cmd/whosaidso@v0.1.8"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %s", err, want)
		}
	}
}

func TestRequiresAdmissionRereadsConfig(t *testing.T) {
	requiresBuild(t, "v0.1.7")
	t.Setenv("WHOSAIDSO_HOME", t.TempDir())
	root, input := cliFixture(t)
	requiresConfig(t, root, "v0.1.7")
	if _, stderr, code := cliRun(t, root, input, "worker", "capture", "--admit", "--reason", "control"); code != 0 {
		t.Fatalf("good control: %s", stderr)
	}
	p, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	// The command retains its opened Project while another edit raises requires.
	requiresConfig(t, root, "v0.1.8")
	called := false
	_, err = store.Transact(context.Background(), p, cliID(889), model.HashBytes([]byte("request")), func([]model.Bundle) (model.Bundle, error) {
		called = true
		return model.Bundle{}, errors.New("proposal reached after requires was raised")
	})
	requireVersionRefusal(t, err)
	if called {
		t.Fatal("proposal ran after requires was raised")
	}
}

func TestRequiresDiscoveryWarnsOnceWithJSON(t *testing.T) {
	t.Setenv("WHOSAIDSO_HOME", t.TempDir())
	for _, version := range []string{"v0.1.7", "(devel)"} {
		t.Run(version, func(t *testing.T) {
			requiresBuild(t, version)
			root, input := cliFixture(t)
			if _, stderr, code := cliRun(t, root, input, "worker", "capture", "--admit", "--reason", "control"); code != 0 {
				t.Fatal(stderr)
			}
			requiresConfig(t, root, "v0.1.8")
			for _, args := range [][]string{{"home", "--json"}, {"gallery", "list", "--json"}, {"show", "--json"}, {"template", "task.amend", "--from", string(cliID(1))}} {
				out, stderr, code := cliRun(t, root, nil, "", args...)
				if code != 0 || !json.Valid([]byte(out)) || strings.Count(stderr, "warning:") != 1 || !strings.Contains(stderr, "v0.1.8") {
					t.Errorf("%v: exit=%d JSON=%v stderr=%q", args, code, json.Valid([]byte(out)), stderr)
				}
			}
		})
	}
}

// Stop the viewer as soon as it prints the launch URL; no browser is opened.
type cancelOnWrite struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelOnWrite) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.cancel()
	return n, err
}

func TestRequiresViewerWarnsOnce(t *testing.T) {
	t.Setenv("WHOSAIDSO_HOME", t.TempDir())
	for _, version := range []string{"v0.1.7", "(devel)"} {
		t.Run(version, func(t *testing.T) {
			requiresBuild(t, version)
			root, _ := cliFixture(t)
			requiresConfig(t, root, "v0.1.8")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := &cancelOnWrite{cancel: cancel}
			var stderr bytes.Buffer
			code := whosaidso(ctx, []string{"ui", "--no-open"}, root, strings.NewReader(""), out, &stderr, os.Getenv)
			if code != 0 || !strings.HasPrefix(out.String(), "http://127.0.0.1:") || strings.Count(stderr.String(), "warning:") != 1 {
				t.Fatalf("exit %d stdout %q stderr %q", code, out.String(), stderr.String())
			}
		})
	}
}
