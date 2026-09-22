package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"datum/internal/model"
)

func intakeProject(t *testing.T) Project {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	return Project{ID: "team/project", Root: root, Ledger: filepath.Join(root, "record/events")}
}

func commandID(n int) model.ID { return model.ID(fmt.Sprintf("%026d", n)) }

func requestFor(id model.ID, source string) IntakeRequest {
	return IntakeRequest{
		CommandID: id,
		Author:    model.Actor{ID: "lane-b"},
		Blobs:     []io.Reader{strings.NewReader(source)},
		BuildEvents: func(blobs []CapturedBlob) ([]model.Event, error) {
			data, err := json.Marshal(blobs[0])
			return []model.Event{{Type: "source.intake", Data: data}}, err
		},
	}
}

func capturedControl(t *testing.T, p Project, id model.ID, source string) model.PacketRef {
	t.Helper()
	ref, err := WriteIntake(context.Background(), p, requestFor(id, source))
	if err != nil {
		t.Fatalf("good capture control: %v", err)
	}
	packets, err := ReadIntake(p, []model.ID{ref.CommandID})
	if err != nil || len(packets) != 1 || packets[0].CommandID != ref.CommandID {
		t.Fatalf("good read control: %+v, %v", packets, err)
	}
	return ref
}

func packetDir(t *testing.T, p Project, id model.ID) string {
	t.Helper()
	inbox, err := IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(inbox, string(id))
}

func TestCaptureSurvivesMovedAndRemovedRoot(t *testing.T) {
	p := intakeProject(t)
	source := "raw source\r\n\x00\xffunchanged bytes\n"
	sourcePath := filepath.Join(p.Root, "source.bin")
	putFile(t, sourcePath, []byte(source))
	f, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	req := requestFor("", source)
	req.Blobs = []io.Reader{f}
	ref, err := WriteIntake(context.Background(), p, req)
	if closeErr := f.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || !model.ValidID(ref.CommandID) {
		t.Fatalf("capture: %+v, %v", ref, err)
	}
	dir := packetDir(t, p, ref.CommandID)
	data, err := os.ReadFile(filepath.Join(dir, "packet.json"))
	if err != nil || model.HashBytes(data) != ref.Digest || bytes.Contains(data, []byte(p.Root)) {
		t.Fatalf("packet digest or runtime path leak: %s, %v", data, err)
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(p.Root, moved); err != nil {
		t.Fatal(err)
	}
	p.Root = moved
	p.Ledger = filepath.Join(moved, "record/events")
	if _, err := ReadIntake(p, nil); err != nil {
		t.Fatalf("after move: %v", err)
	}
	if err := os.RemoveAll(moved); err != nil {
		t.Fatal(err)
	}
	packets, err := ReadIntake(p, nil)
	if err != nil || len(packets) != 1 {
		t.Fatalf("after deletion: %+v, %v", packets, err)
	}
	blob, err := os.ReadFile(filepath.Join(dir, "blobs", string(model.HashBytes([]byte(source)))))
	if err != nil || string(blob) != source {
		t.Fatalf("source lost: %q, %v", blob, err)
	}
	if _, err := os.Stat(p.Ledger); !os.IsNotExist(err) {
		t.Fatalf("capture unexpectedly needed a ledger: %v", err)
	}
}

func TestDistinctDeclaredProjectsSharingGit(t *testing.T) {
	p := intakeProject(t)
	common := filepath.Join(t.TempDir(), "common.git")
	mustMkdir(t, common)
	ids := []model.ProjectID{"a/b", "a_b", "a\\b", "../a", "a", "A", "é", "é"}
	seen := map[string]bool{}
	for i, id := range ids {
		root := t.TempDir()
		putFile(t, filepath.Join(root, ".git"), []byte("gitdir: "+common+"\n"))
		putFile(t, filepath.Join(root, "datum.toml"), []byte("id='"+string(id)+"'\nledger='record/events'"))
		project, err := Discover(root)
		if err != nil {
			t.Fatal(err)
		}
		dir, err := IntakeDir(project)
		if err != nil || seen[dir] || filepath.Base(dir) != base64.RawURLEncoding.EncodeToString([]byte(id)) {
			t.Fatalf("inbox identity collision: %s, %v", dir, err)
		}
		seen[dir] = true
		capturedControl(t, project, commandID(1), fmt.Sprintf("source %d", i))
		packets, err := ReadIntake(project, nil)
		if err != nil || len(packets) != 1 || packets[0].Project != id {
			t.Fatalf("cross-project intake: %+v, %v", packets, err)
		}
	}
	if packets, err := ReadIntake(p, nil); err != nil || len(packets) != 0 {
		t.Fatalf("uncreated inbox: %+v, %v", packets, err)
	}
}

type gatedReader struct {
	ready   chan<- struct{}
	release <-chan struct{}
	reader  io.Reader
	started bool
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if !r.started {
		r.started = true
		r.ready <- struct{}{}
		<-r.release
	}
	return r.reader.Read(p)
}

func TestFiveWritersNoLockAndNoPartialPackets(t *testing.T) {
	for _, sameID := range []bool{false, true} {
		t.Run(fmt.Sprintf("same-id=%v", sameID), func(t *testing.T) {
			p := intakeProject(t)
			ready := make(chan struct{}, 5)
			release := make(chan struct{})
			var unblock sync.Once
			defer unblock.Do(func() { close(release) })
			type result struct {
				ref model.PacketRef
				err error
			}
			results := make(chan result, 5)
			for i := 0; i < 5; i++ {
				go func(i int) {
					req := requestFor("", "parallel source")
					if sameID {
						req.CommandID = commandID(1)
					}
					req.Blobs = []io.Reader{&gatedReader{ready: ready, release: release, reader: strings.NewReader("parallel source")}}
					ref, err := WriteIntake(context.Background(), p, req)
					results <- result{ref, err}
				}(i)
			}
			for i := 0; i < 5; i++ {
				select {
				case <-ready:
				case <-time.After(10 * time.Second):
					t.Fatal("writers serialized or stalled before capture")
				}
			}
			if packets, err := ReadIntake(p, nil); err != nil || len(packets) != 0 {
				t.Fatalf("partial packets became visible: %+v, %v", packets, err)
			}
			unblock.Do(func() { close(release) })
			refs := map[model.PacketRef]bool{}
			for i := 0; i < 5; i++ {
				select {
				case result := <-results:
					if result.err != nil {
						t.Fatal(result.err)
					}
					refs[result.ref] = true
				case <-time.After(10 * time.Second):
					t.Fatal("writer did not finish")
				}
			}
			want := 5
			if sameID {
				want = 1
			}
			packets, err := ReadIntake(p, nil)
			if err != nil || len(packets) != want || len(refs) != want {
				t.Fatalf("writers: packets=%d refs=%d want=%d err=%v", len(packets), len(refs), want, err)
			}
			if err := filepath.WalkDir(filepath.Join(os.Getenv("HOME"), ".datum"), func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if strings.Contains(entry.Name(), "lock") || strings.HasSuffix(entry.Name(), ".tmp") {
					return fmt.Errorf("unexpected lock or leftover own temporary: %s", path)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRetryAndChangedRequestRefusals(t *testing.T) {
	mutations := map[string]func(*IntakeRequest){
		"bytes":  func(r *IntakeRequest) { r.Blobs = []io.Reader{strings.NewReader("changed bytes")} },
		"author": func(r *IntakeRequest) { r.Author.ID = "another-lane" },
		"events": func(r *IntakeRequest) {
			r.BuildEvents = nil
			r.Events = []model.Event{{Type: "source.intake", Data: json.RawMessage(`{"changed":true}`)}}
		},
		"extra blob": func(r *IntakeRequest) { r.Blobs = append(r.Blobs, strings.NewReader("extra")) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			p := intakeProject(t)
			ref := capturedControl(t, p, commandID(1), "original")
			before, err := os.ReadFile(filepath.Join(packetDir(t, p, ref.CommandID), "packet.json"))
			if err != nil {
				t.Fatal(err)
			}
			retry, err := WriteIntake(context.Background(), p, requestFor(ref.CommandID, "original"))
			if err != nil || retry != ref {
				t.Fatalf("identical retry: %+v != %+v, %v", retry, ref, err)
			}
			req := requestFor(ref.CommandID, "original")
			mutate(&req)
			_, err = WriteIntake(context.Background(), p, req)
			requireFault(t, err, "conflict")
			after, err := os.ReadFile(filepath.Join(packetDir(t, p, ref.CommandID), "packet.json"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("retry overwrote original: %v", err)
			}
		})
	}
}

func TestBlobInventoryIsAnIdentitySet(t *testing.T) {
	p := intakeProject(t)
	req := IntakeRequest{CommandID: commandID(1), Author: model.Actor{UnknownReason: "not supplied"}, Events: []model.Event{{Type: "source.intake", Data: json.RawMessage(`{"source":"known"}`)}}}
	req.Blobs = []io.Reader{strings.NewReader("a"), strings.NewReader("b"), strings.NewReader("a")}
	ref, err := WriteIntake(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	req.Blobs = []io.Reader{strings.NewReader("b"), strings.NewReader("a")}
	retry, err := WriteIntake(context.Background(), p, req)
	if err != nil || ref != retry {
		t.Fatalf("stream order or duplication changed identity: %+v, %v", retry, err)
	}
	if _, err := ReadIntake(p, nil); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerOnlyPermissions(t *testing.T) {
	p := intakeProject(t)
	capturedControl(t, p, commandID(1), "private source")
	if err := filepath.WalkDir(filepath.Join(os.Getenv("HOME"), ".datum"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		want := os.FileMode(0600)
		if entry.IsDir() {
			want = 0700
		}
		if info.Mode().Perm() != want {
			return fmt.Errorf("%s mode %o, want %o", path, info.Mode().Perm(), want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadRefusesIncompleteOrAlteredPackets(t *testing.T) {
	tests := []struct {
		name string
		code string
		edit func(*testing.T, string)
	}{
		{"missing blob", "intake-corrupt", func(t *testing.T, dir string) {
			mustRemove(t, filepath.Join(dir, "blobs", string(model.HashBytes([]byte("source")))))
		}},
		{"wrong bytes", "intake-corrupt", func(t *testing.T, dir string) {
			putFile(t, filepath.Join(dir, "blobs", string(model.HashBytes([]byte("source")))), []byte("changed"))
		}},
		{"extra blob", "intake-corrupt", func(t *testing.T, dir string) {
			putFile(t, filepath.Join(dir, "blobs", string(model.HashBytes([]byte("extra")))), []byte("extra"))
		}},
		{"path as identity", "intake-corrupt", func(t *testing.T, dir string) {
			putFile(t, filepath.Join(dir, "blobs", "source.txt"), []byte("source"))
		}},
		{"missing json", "intake-corrupt", func(t *testing.T, dir string) { mustRemove(t, filepath.Join(dir, "packet.json")) }},
		{"extra file", "intake-corrupt", func(t *testing.T, dir string) { putFile(t, filepath.Join(dir, "extra"), nil) }},
		{"malformed json", "intake-corrupt", func(t *testing.T, dir string) { putFile(t, filepath.Join(dir, "packet.json"), []byte("{")) }},
		{"permissive file", "insecure-permissions", func(t *testing.T, dir string) {
			if err := os.Chmod(filepath.Join(dir, "packet.json"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink blob", "intake-corrupt", func(t *testing.T, dir string) {
			path := filepath.Join(dir, "blobs", string(model.HashBytes([]byte("source"))))
			outside := filepath.Join(t.TempDir(), "source")
			putFile(t, outside, []byte("source"))
			mustRemove(t, path)
			if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong project", "intake-corrupt", func(t *testing.T, dir string) {
			path := filepath.Join(dir, "packet.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			putFile(t, path, bytes.ReplaceAll(data, []byte("team/project"), []byte("other/project")))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := intakeProject(t)
			ref := capturedControl(t, p, commandID(1), "source")
			tt.edit(t, packetDir(t, p, ref.CommandID))
			packets, err := ReadIntake(p, nil)
			requireFault(t, err, tt.code)
			if packets != nil {
				t.Fatalf("partial result on refusal: %+v", packets)
			}
		})
	}
}

func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestSyncFailureAtEveryBoundaryAndLostAckRetry(t *testing.T) {
	p := intakeProject(t)
	steps := 0
	disk := systemIntakeIO()
	disk.sync = func(f *os.File) error { steps++; return f.Sync() }
	if _, err := writeIntake(context.Background(), p, requestFor(commandID(1), "source"), disk); err != nil {
		t.Fatalf("good control: %v", err)
	}
	for failAt := 1; failAt <= steps; failAt++ {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			p := intakeProject(t)
			capturedControl(t, p, commandID(1), "control")
			calls := 0
			published := false
			disk := systemIntakeIO()
			disk.sync = func(f *os.File) error {
				calls++
				if calls == failAt {
					return errors.New("injected sync failure")
				}
				return f.Sync()
			}
			disk.rename = func(from, to string) error {
				if err := os.Rename(from, to); err != nil {
					return err
				}
				published = true
				return nil
			}
			ref, err := writeIntake(context.Background(), p, requestFor(commandID(2), "source"), disk)
			code := "io"
			if published {
				code = "uncertain-ack"
			}
			requireFault(t, err, code)
			if ref != (model.PacketRef{}) {
				t.Fatal("failed durability returned an acknowledgement")
			}
			packets, err := ReadIntake(p, nil)
			want := 1
			if published {
				want = 2
			}
			if err != nil || len(packets) != want {
				t.Fatalf("visible packets=%d want=%d err=%v", len(packets), want, err)
			}
			if published {
				path := filepath.Join(packetDir(t, p, commandID(2)), "packet.json")
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				retry := capturedControl(t, p, commandID(2), "source")
				if retry.Digest != model.HashBytes(before) {
					t.Fatal("lost-ack retry changed packet bytes")
				}
			}
		})
	}
}

func TestAcknowledgementRequiresFlushedTreeAndPublishedName(t *testing.T) {
	p := intakeProject(t)
	inbox, err := IntakeDir(p)
	if err != nil {
		t.Fatal(err)
	}
	// Check actual inode identities: the blob was synced under its temporary
	// name, but it must be that same file when the directory is published.
	type flush struct {
		info     os.FileInfo
		sequence int
	}
	flushed := []flush{}
	directoryFlushes := map[string]int{}
	sequence := 0
	published := false
	atFinalSync := make(chan struct{})
	release := make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	disk := systemIntakeIO()
	disk.sync = func(f *os.File) error {
		if published && f.Name() == inbox {
			close(atFinalSync)
			<-release
		}
		if err := f.Sync(); err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			return err
		}
		sequence++
		flushed = append(flushed, flush{info, sequence})
		if info.IsDir() {
			directoryFlushes[f.Name()] = sequence
		}
		return nil
	}
	disk.rename = func(from, to string) error {
		if err := filepath.WalkDir(from, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			childFlush := 0
			for _, synced := range flushed {
				if os.SameFile(info, synced.info) {
					childFlush = synced.sequence
				}
			}
			if childFlush == 0 {
				return fmt.Errorf("publication preceded flush of %s", path)
			}
			if path != from && directoryFlushes[filepath.Dir(path)] <= childFlush {
				return fmt.Errorf("containing directory flushed before its child %s", path)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, dir := range []string{inbox, filepath.Dir(inbox), filepath.Dir(filepath.Dir(inbox)), os.Getenv("HOME")} {
			if directoryFlushes[dir] == 0 {
				return fmt.Errorf("publication preceded ancestor flush: %s", dir)
			}
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		published = true
		return nil
	}
	type result struct {
		ref model.PacketRef
		err error
	}
	done := make(chan result, 1)
	go func() {
		ref, err := writeIntake(context.Background(), p, requestFor(commandID(1), "source"), disk)
		done <- result{ref, err}
	}()
	select {
	case <-atFinalSync:
	case result := <-done:
		t.Fatalf("writer returned before publication durability: %+v", result)
	case <-time.After(10 * time.Second):
		t.Fatal("writer stalled")
	}
	select {
	case result := <-done:
		t.Fatalf("acknowledged before inbox sync: %+v", result)
	default:
	}
	// Visibility is permitted before acknowledgement, but only for a complete
	// packet. Publication durability still has to finish before success returns.
	if packets, err := ReadIntake(p, nil); err != nil || len(packets) != 1 {
		t.Fatalf("published directory was partial: %+v, %v", packets, err)
	}
	unblock.Do(func() { close(release) })
	if result := <-done; result.err != nil || result.ref.CommandID != commandID(1) {
		t.Fatalf("capture after final sync: %+v", result)
	}
}

func TestCancellationBeforePublication(t *testing.T) {
	p := intakeProject(t)
	capturedControl(t, p, commandID(1), "control")
	ctx, cancel := context.WithCancel(context.Background())
	req := requestFor(commandID(2), "source")
	build := req.BuildEvents
	req.BuildEvents = func(blobs []CapturedBlob) ([]model.Event, error) {
		cancel()
		return build(blobs)
	}
	_, err := WriteIntake(ctx, p, req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled capture: %v", err)
	}
	if packets, err := ReadIntake(p, nil); err != nil || len(packets) != 1 {
		t.Fatalf("cancelled write published: %+v, %v", packets, err)
	}
}

type brokenReader struct{}

func (brokenReader) Read(p []byte) (int, error) {
	return copy(p, "partial"), errors.New("injected source failure")
}

func TestWriteRefusalsLeaveNoPublishedPacket(t *testing.T) {
	tests := []struct {
		name, code string
		change     func(*IntakeRequest, *intakeIO)
	}{
		{"bad id", "invalid-field", func(r *IntakeRequest, _ *intakeIO) { r.CommandID = "../escape" }},
		{"no actor", "invalid-field", func(r *IntakeRequest, _ *intakeIO) { r.Author = model.Actor{} }},
		{"nil reader", "invalid-field", func(r *IntakeRequest, _ *intakeIO) { r.Blobs = []io.Reader{nil} }},
		{"broken stream", "io", func(r *IntakeRequest, _ *intakeIO) { r.Blobs = []io.Reader{brokenReader{}} }},
		{"two event inputs", "invalid-field", func(r *IntakeRequest, _ *intakeIO) {
			r.Events = []model.Event{{Type: "source.intake", Data: json.RawMessage(`{}`)}}
		}},
		{"no events", "invalid-field", func(r *IntakeRequest, _ *intakeIO) { r.BuildEvents = nil }},
		{"malformed event JSON", "invalid-field", func(r *IntakeRequest, _ *intakeIO) {
			r.BuildEvents = nil
			r.Events = []model.Event{{Type: "source.intake", Data: json.RawMessage(`{`)}}
		}},
		{"rename failure", "io", func(_ *IntakeRequest, d *intakeIO) {
			d.rename = func(string, string) error { return errors.New("injected rename failure") }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := intakeProject(t)
			capturedControl(t, p, commandID(1), "control")
			req, disk := requestFor(commandID(2), "source"), systemIntakeIO()
			tt.change(&req, &disk)
			_, err := writeIntake(context.Background(), p, req, disk)
			requireFault(t, err, tt.code)
			packets, err := ReadIntake(p, nil)
			if err != nil || len(packets) != 1 {
				t.Fatalf("failed write published: %+v, %v", packets, err)
			}
		})
	}
}

func TestCrashRecovery(t *testing.T) {
	for _, point := range []string{"before-publish", "after-publish", "after-ack"} {
		t.Run(point, func(t *testing.T) {
			p := intakeProject(t)
			capturedControl(t, p, commandID(1), "control")
			cmd := exec.Command(os.Args[0], "-test.run=^TestIntakeCrashChild$")
			cmd.Env = append(os.Environ(), "DATUM_INTAKE_CRASH_TEST="+point)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("child did not crash: %s", output)
			} else {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 73 {
					t.Fatalf("child failed unexpectedly: %s, %v", output, err)
				}
			}
			packets, err := ReadIntake(p, nil)
			want := 2
			if point == "before-publish" {
				want = 1
			}
			if err != nil || len(packets) != want {
				t.Fatalf("restart: got %d want %d: %v", len(packets), want, err)
			}
			ref := capturedControl(t, p, commandID(2), "crash source")
			if !model.ValidDigest(ref.Digest) {
				t.Fatal("invalid retry reference")
			}
			if point == "before-publish" {
				inbox, err := IntakeDir(p)
				if err != nil {
					t.Fatal(err)
				}
				entries, err := os.ReadDir(inbox)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, entry := range entries {
					found = found || strings.HasSuffix(entry.Name(), ".tmp")
				}
				if !found {
					t.Fatal("reader or retry removed another producer's temporary")
				}
			}
		})
	}
}

func TestIntakeCrashChild(t *testing.T) {
	point := os.Getenv("DATUM_INTAKE_CRASH_TEST")
	if point == "" {
		return
	}
	p := Project{ID: "team/project"}
	disk := systemIntakeIO()
	disk.rename = func(from, to string) error {
		if point == "before-publish" {
			os.Exit(73)
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		if point == "after-publish" {
			os.Exit(73)
		}
		return nil
	}
	if _, err := writeIntake(context.Background(), p, requestFor(commandID(2), "crash source"), disk); err != nil {
		t.Fatal(err)
	}
	os.Exit(73)
}
