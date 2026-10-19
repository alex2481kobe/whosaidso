// Viewer requirement warnings are checked through the command's stderr and
// authenticated HTTP reads. Browser rendering is outside these fixtures.
package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/store"
)

type viewerLaunch chan string

func (w viewerLaunch) Write(p []byte) (int, error) {
	w <- strings.TrimSpace(string(p))
	return len(p), nil
}

func TestViewerWarnsOncePerRegisteredProject(t *testing.T) {
	for _, first := range []string{"/api/projects", "/api/view?project=test/second&view=todo", "/api/gallery?project=test/second"} {
		t.Run(first, func(t *testing.T) {
			requiresBuild(t, "v0.1.7")
			root, _ := cliFixture(t)
			for _, id := range []string{"test/second", "test/third"} {
				home := t.TempDir()
				body := fmt.Sprintf("id = '%s'\nledger = '.whosaidso/events'\nrequires = 'v0.1.8'\n", id)
				if err := os.WriteFile(filepath.Join(home, "whosaidso.toml"), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Bind(context.Background(), home, home); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			launch := make(viewerLaunch, 1)
			var stderr bytes.Buffer
			done := make(chan struct{})
			var code int
			go func() {
				defer close(done)
				code = whosaidso(ctx, []string{"ui", "--no-open"}, root, strings.NewReader(""), launch, &stderr, os.Getenv)
			}()
			t.Cleanup(func() { cancel(); <-done })
			var address string
			select {
			case address = <-launch:
			case <-done:
				t.Fatalf("viewer exited before launch: exit %d stderr %q", code, stderr.String())
			case <-ctx.Done():
				t.Fatal("viewer did not launch")
			}
			session := uiSession(t, address)
			for _, path := range []string{first, "/api/projects", "/api/view?project=test/second&view=todo", "/api/gallery?project=test/second", "/api/projects"} {
				if status, body := uiGet(t, address, session, path); status != http.StatusOK {
					t.Fatalf("%s: %d %s", path, status, body)
				}
			}
			var readers sync.WaitGroup
			for range 8 {
				readers.Add(1)
				go func() {
					defer readers.Done()
					if status, body := uiGet(t, address, session, "/api/projects"); status != http.StatusOK {
						t.Errorf("concurrent registry read: %d %s", status, body)
					}
				}()
			}
			readers.Wait()
			cancel()
			<-done
			if code != 0 || strings.Count(stderr.String(), "warning:") != 2 {
				t.Fatalf("want one warning for each newer project: exit %d stderr %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "v0.1.7") || !strings.Contains(stderr.String(), "v0.1.8") {
				t.Fatalf("warning lacks running or required version: %q", stderr.String())
			}
			for _, id := range []string{"test/second", "test/third"} {
				if strings.Count(stderr.String(), id) != 1 {
					t.Errorf("want one warning naming %s: %q", id, stderr.String())
				}
			}
		})
	}
}
