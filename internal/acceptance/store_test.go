// Lane E's independent U03 attacks on root discovery and durable, lock-free
// intake. Every test redirects the user home to a temporary directory first, so
// nothing here can reach the real ~/.datum.
//
// Out of scope on purpose: the ledger publisher and its lock (U04), and whether
// a captured packet is admissible (U08). This file only asks whether capture
// keeps its two promises: one inbox per declared project, and nothing readable
// as complete that is not complete.
package acceptance_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"datum/internal/model"
	"datum/internal/store"
)

// stHome redirects the user home and proves the redirection took effect before
// any test writes a byte. A test that silently kept the real home would publish
// packets into the owner's live inbox.
func stHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := os.UserHomeDir()
	if err != nil || got != home {
		t.Fatalf("home redirection failed, refusing to touch the real inbox: %q, %v", got, err)
	}
	return home
}

func stWriteConfig(t *testing.T, root, id, ledger string) {
	t.Helper()
	body := "id = '" + id + "'\nledger = '" + ledger + "'\n"
	if err := os.WriteFile(filepath.Join(root, "datum.toml"), []byte(body), 0600); err != nil {
		t.Fatalf("writing datum.toml: %v", err)
	}
}

func stDiscover(t *testing.T, id, ledger string) store.Project {
	t.Helper()
	root := t.TempDir()
	stWriteConfig(t, root, id, ledger)
	p, err := store.Discover(root)
	if err != nil {
		t.Fatalf("discovering the project declared as %q: %v", id, err)
	}
	return p
}

func stInbox(t *testing.T, p store.Project) string {
	t.Helper()
	dir, err := store.IntakeDir(p)
	if err != nil {
		t.Fatalf("no inbox for project %q: %v", p.ID, err)
	}
	return dir
}

// stRequest builds one capture whose single source event is derived from the
// captured bytes, which is the real producer path rather than a shortcut.
func stRequest(id model.ID, body string, source int) store.IntakeRequest {
	return store.IntakeRequest{
		CommandID: id,
		Author:    model.Actor{ID: "lane-e"},
		Blobs:     []io.Reader{strings.NewReader(body)},
		BuildEvents: func(blobs []store.CapturedBlob) ([]model.Event, error) {
			if len(blobs) != 1 {
				return nil, fmt.Errorf("expected one captured blob, got %d", len(blobs))
			}
			return []model.Event{mustEncode(&model.SourceIntake{
				SourceID:       recID(source),
				OriginalDigest: blobs[0].SHA256,
				Length:         blobs[0].Length,
				SourceRef: model.ArtifactRef{
					Kind: "content",
					Content: &model.ContentPin{
						SHA256: blobs[0].SHA256, Length: blobs[0].Length,
						MediaType: "text/markdown", Locators: []model.Locator{},
					},
					Selector: model.Selector{Kind: "whole"},
				},
				Speaker:   model.Actor{ID: "owner"},
				Order:     0,
				Referents: []model.RecordRef{},
			})}, nil
		},
	}
}

func mustEncode(payload model.TypedEvent) model.Event {
	e, err := model.EncodeEvent(payload)
	if err != nil {
		panic("lane E fixture is invalid: " + err.Error())
	}
	return e
}

func stCode(err error) string {
	var f *model.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

// stEntries lists what a reader would actually see in an inbox.
func stEntries(t *testing.T, inbox string) []string {
	t.Helper()
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("reading inbox %s: %v", inbox, err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// ---- control --------------------------------------------------------------

func TestIntakeCapturesOnePacketWithItsBlobsAndReadsItBackVerified(t *testing.T) {
	home := stHome(t)
	project := stDiscover(t, "datum/acceptance", ".datum/events")

	if want := filepath.Join(project.Root, ".datum", "events"); project.Ledger != want {
		t.Fatalf("relative ledger resolved to %q, want %q", project.Ledger, want)
	}
	ref, err := store.WriteIntake(context.Background(), project, stRequest("", "the owner said the blind spot is required", 1))
	if err != nil {
		t.Fatalf("control: a valid capture must succeed: %v", err)
	}
	if !model.ValidID(ref.CommandID) || !model.ValidDigest(ref.Digest) {
		t.Fatalf("capture returned an unusable reference: %+v", ref)
	}

	inbox := stInbox(t, project)
	if !strings.HasPrefix(inbox, filepath.Join(home, ".datum", "intake")+string(filepath.Separator)) {
		t.Fatalf("inbox %s is outside the redirected home", inbox)
	}
	dir := filepath.Join(inbox, string(ref.CommandID))
	entries := stEntries(t, dir)
	if len(entries) != 2 || entries[0] != "blobs" || entries[1] != "packet.json" {
		t.Fatalf("a published packet holds %v, want exactly blobs/ and packet.json", entries)
	}
	blobs := stEntries(t, filepath.Join(dir, "blobs"))
	if len(blobs) != 1 || !model.ValidDigest(model.Digest(blobs[0])) {
		t.Fatalf("blobs/ holds %v, want one raw sha-256 name", blobs)
	}

	packets, err := store.ReadIntake(project, nil)
	if err != nil {
		t.Fatalf("control: reading back the packet just written: %v", err)
	}
	if len(packets) != 1 || packets[0].CommandID != ref.CommandID || packets[0].Project != project.ID {
		t.Fatalf("read back %d packets: %+v", len(packets), packets)
	}
	if len(packets[0].Events) != 1 || packets[0].Events[0].Type != "source.intake" {
		t.Fatalf("captured events are %+v, want one source.intake", packets[0].Events)
	}
}

// ---- config ---------------------------------------------------------------

func TestConfigNearestRootWins(t *testing.T) {
	stHome(t)
	outer := t.TempDir()
	stWriteConfig(t, outer, "datum/outer", ".datum/events")
	inner := filepath.Join(outer, "lane", "worktree")
	if err := os.MkdirAll(inner, 0700); err != nil {
		t.Fatal(err)
	}

	// Control: with no nearer config the outer one wins, and with one it loses.
	p, err := store.Discover(inner)
	if err != nil || p.ID != "datum/outer" {
		t.Fatalf("control: the outer config must win from a nested directory: %+v, %v", p, err)
	}
	stWriteConfig(t, inner, "datum/inner", ".datum/events")
	p, err = store.Discover(inner)
	if err != nil || p.ID != "datum/inner" {
		t.Fatalf("control: the nearest config must win: %+v, %v", p, err)
	}
	if want := filepath.Join(inner, ".datum", "events"); p.Ledger != want {
		t.Fatalf("the nearest config resolved its ledger to %q, want %q", p.Ledger, want)
	}

}

// TestConfigLedgerLeavingItsRootContradictsTheCommittedLedgerRule is an open
// question, not a claim that lane B slipped. internal/store/config_test.go
// asserts this behaviour on purpose, for a relative parent hop and for an
// absolute path alike, so the two readings of the contract have to be settled
// by the owner rather than by either lane.
//
// The reading this test encodes: DATUM-CONTRACT.md:676 says every stored path is
// relative to the datum root, and the storage table puts the ledger in "the
// project's working tree, committed with the project". A ledger at /tmp or above
// the root is in no working tree, so it cannot be committed with the project and
// a rearranged project directory stops finding it.
func TestConfigLedgerLeavingItsRootContradictsTheCommittedLedgerRule(t *testing.T) {
	stHome(t)
	// Control: an ordinary relative ledger resolves under its own root.
	inside := stDiscover(t, "datum/inside", ".datum/events")
	if rel, err := filepath.Rel(inside.Root, inside.Ledger); err != nil || rel != filepath.Join(".datum", "events") {
		t.Fatalf("control: a relative ledger must land under its root, got %q, %v", rel, err)
	}

	escapes := []struct {
		name   string
		ledger string
	}{
		{"a parent hop", "../events"},
		{"several parent hops", "../../../../../../events"},
		{"a hop hidden mid-path", ".datum/../../events"},
		{"an absolute path", filepath.Join(t.TempDir(), "elsewhere", "events")},
		{"an absolute path in a system directory", "/tmp/datum-escaped-ledger"},
	}
	for _, e := range escapes {
		t.Run(e.name, func(t *testing.T) {
			root := t.TempDir()
			stWriteConfig(t, root, "datum/escape", e.ledger)
			p, err := store.Discover(root)
			if err != nil {
				return
			}
			rel, relErr := filepath.Rel(p.Root, p.Ledger)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Errorf("ledger %q declared as %q resolves outside its own root %q, "+
					"so it lives in no working tree and cannot be committed with the project. "+
					"lane B asserts this behaviour deliberately, so this needs an owner ruling",
					p.Ledger, e.ledger, p.Root)
			}
		})
	}
}

func TestConfigRefusesAnIdentityOrLedgerThatRendersAsNothing(t *testing.T) {
	stHome(t)
	// Control: a real identity and a real ledger are accepted.
	if p := stDiscover(t, "datum/acceptance", ".datum/events"); p.ID != "datum/acceptance" {
		t.Fatalf("control: %+v", p)
	}

	for _, blank := range recBlanks {
		for _, key := range []string{"id", "ledger"} {
			t.Run(key+"/"+blank.name, func(t *testing.T) {
				root := t.TempDir()
				id, ledger := "datum/acceptance", ".datum/events"
				if key == "id" {
					id = blank.text
				} else {
					ledger = blank.text
				}
				stWriteConfig(t, root, id, ledger)
				p, err := store.Discover(root)
				if err != nil {
					return
				}
				became := fmt.Sprintf("the identity %q", p.ID)
				if key != "id" {
					became = fmt.Sprintf("the ledger %q", p.Ledger)
				}
				t.Errorf("datum.toml declaring %s as %s (%q) was accepted. The project now has %s, "+
					"which is the same defect as a non-empty rule that a single space satisfies",
					key, blank.name, blank.text, became)
			})
		}
	}
}

func TestConfigRefusesKeysThatOnlyLookLikeTheTwoItAccepts(t *testing.T) {
	stHome(t)
	cases := []struct{ name, body string }{
		{"a capitalised id", "ID = 'datum/x'\nledger = '.datum/events'\n"},
		{"a capitalised ledger", "id = 'datum/x'\nLedger = '.datum/events'\n"},
		{"an all caps pair", "ID = 'datum/x'\nLEDGER = '.datum/events'\n"},
		{"a key inside a table header", "[project]\nid = 'datum/x'\nledger = '.datum/events'\n"},
		{"a duplicate id", "id = 'datum/x'\nid = 'datum/y'\nledger = '.datum/events'\n"},
		{"a duplicate id spelled through a quoted key", "id = 'datum/x'\n\"id\" = 'datum/y'\nledger = '.datum/events'\n"},
		{"a multi-line basic string", "id = \"\"\"datum/x\"\"\"\nledger = '.datum/events'\n"},
		{"a multi-line literal string", "id = '''datum/x'''\nledger = '.datum/events'\n"},
		{"trailing content after the value", "id = 'datum/x' and more\nledger = '.datum/events'\n"},
		{"a third key", "id = 'datum/x'\nledger = '.datum/events'\nactor = 'lane-e'\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "datum.toml"), []byte(
				"id = 'datum/control'\nledger = '.datum/events'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Discover(root); err != nil {
				t.Fatalf("control config must be accepted: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, "datum.toml"), []byte(c.body), 0600); err != nil {
				t.Fatal(err)
			}
			p, err := store.Discover(root)
			if err == nil {
				t.Errorf("a datum.toml with %s was accepted as project %q with ledger %q", c.name, p.ID, p.Ledger)
				return
			}
			if !strings.HasPrefix(stCode(err), "config-") {
				t.Errorf("a datum.toml with %s was refused with code %q, want a specific config diagnostic: %v",
					c.name, stCode(err), err)
			}
		})
	}
}

// ---- one inbox per declared project ---------------------------------------

func TestIntakeDistinctProjectIdsNeverShareAnInbox(t *testing.T) {
	stHome(t)

	// Control: two plainly different ids get two inboxes, and each reads back
	// only its own packet even though one Git checkout could hold both.
	alpha := stDiscover(t, "datum/alpha", ".datum/events")
	beta := stDiscover(t, "datum/beta", ".datum/events")
	if stInbox(t, alpha) == stInbox(t, beta) {
		t.Fatalf("control: two obviously different ids already share an inbox")
	}
	if _, err := store.WriteIntake(context.Background(), alpha, stRequest("", "alpha source", 1)); err != nil {
		t.Fatalf("control capture for alpha: %v", err)
	}
	if _, err := store.WriteIntake(context.Background(), beta, stRequest("", "beta source", 2)); err != nil {
		t.Fatalf("control capture for beta: %v", err)
	}
	for _, p := range []store.Project{alpha, beta} {
		packets, err := store.ReadIntake(p, nil)
		if err != nil || len(packets) != 1 || packets[0].Project != p.ID {
			t.Fatalf("control: project %q read back %d packets: %v", p.ID, len(packets), err)
		}
	}

	t.Run("ids whose inbox names differ only in case", func(t *testing.T) {
		// Base64 is injective over byte strings. The inbox is a filesystem PATH,
		// and macOS compares path components without regard to case, so the
		// injective property is a true fact about the wrong object. This pair
		// was found by exhaustive search over short ids.
		one := stDiscover(t, "datum/aaa", ".datum/events")
		two := stDiscover(t, "datum/aaG", ".datum/events")
		dirOne, dirTwo := stInbox(t, one), stInbox(t, two)
		// The original form of this attack asserted the two names differ only in
		// case, which was the precondition for the collision. The encoding is
		// single-case now, so that precondition is structurally unreachable and
		// asserting it would fail for the right reason in a confusing way. The
		// attack itself is unchanged and stronger: capture as both projects, then
		// require each to read back only its own.
		if strings.EqualFold(filepath.Base(dirOne), filepath.Base(dirTwo)) {
			t.Fatalf("two distinct declared ids share one inbox once the filesystem folds case: %s and %s",
				filepath.Base(dirOne), filepath.Base(dirTwo))
		}
		if _, err := store.WriteIntake(context.Background(), one, stRequest("", "source for datum/aaa", 3)); err != nil {
			t.Fatalf("capture for datum/aaa: %v", err)
		}
		if _, err := store.WriteIntake(context.Background(), two, stRequest("", "source for datum/aaG", 4)); err != nil {
			t.Fatalf("capture for datum/aaG: %v", err)
		}
		for _, p := range []store.Project{one, two} {
			packets, err := store.ReadIntake(p, nil)
			if err != nil {
				t.Errorf("project %q can no longer read its own inbox: %v. "+
					"its packets and those of the other project landed in one directory", p.ID, err)
				continue
			}
			if len(packets) != 1 {
				t.Errorf("project %q sees %d packets in its inbox, want only its own one", p.ID, len(packets))
			}
			for _, packet := range packets {
				if packet.Project != p.ID {
					t.Errorf("project %q sees a packet belonging to %q", p.ID, packet.Project)
				}
			}
		}
	})

	t.Run("ids built out of path syntax stay one harmless directory component", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Join(home, ".datum", "intake")
		hostile := []struct{ name, id string }{
			{"a slash", "datum/lane/a"},
			{"a parent hop", "../../etc"},
			{"a bare parent hop", ".."},
			{"a current directory", "."},
			{"a leading dash", "-rf"},
			{"a newline", "datum/a\nledger"},
			{"a tab and a quote", "datum/\ta\"b"},
			{"an absolute path", "/etc/passwd"},
			{"a windows separator", `datum\lane`},
		}
		for _, h := range hostile {
			t.Run(h.name, func(t *testing.T) {
				p := store.Project{ID: model.ProjectID(h.id), Root: t.TempDir(), Ledger: filepath.Join(t.TempDir(), "events")}
				dir, err := store.IntakeDir(p)
				if err != nil {
					t.Fatalf("a declared id containing %s has no inbox: %v", h.name, err)
				}
				component := strings.TrimPrefix(dir, base+string(filepath.Separator))
				if component == dir {
					t.Fatalf("inbox %s is not under %s", dir, base)
				}
				if strings.ContainsAny(component, `/\`+"\n\x00") || component == "." || component == ".." || strings.HasPrefix(component, "-") {
					t.Errorf("the declared id %q produced the inbox component %q, which is path syntax rather than a name", h.id, component)
				}
				// Assert the property, not the encoding. What matters is that the
				// component is one harmless directory name and that it is stable
				// under case folding, since the filesystem is what decides whether
				// two names are one directory.
				if component != strings.ToLower(component) && component != strings.ToUpper(component) {
					t.Errorf("inbox component for %q is %q, which mixes case and can fold onto another id", h.id, component)
				}
				if _, err := store.WriteIntake(context.Background(), p, stRequest("", "source for "+h.id, 5)); err != nil {
					t.Errorf("a declared id containing %s cannot capture: %v", h.name, err)
				}
			})
		}
	})
}

func TestIntakeCapturedSourceOutlivesTheWorktreeItCameFrom(t *testing.T) {
	stHome(t)
	project := stDiscover(t, "datum/lane-worktree", ".datum/events")
	ref, err := store.WriteIntake(context.Background(), project, stRequest("", "the lane's only copy of this source", 6))
	if err != nil {
		t.Fatalf("control capture: %v", err)
	}
	if _, err := store.ReadIntake(project, []model.ID{ref.CommandID}); err != nil {
		t.Fatalf("control read: %v", err)
	}

	if err := os.RemoveAll(project.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Discover(project.Root); err == nil {
		t.Error("a deleted worktree still discovers a project")
	}
	packets, err := store.ReadIntake(project, []model.ID{ref.CommandID})
	if err != nil {
		t.Fatalf("captured source did not survive removal of the worktree it came from: %v", err)
	}
	if len(packets) != 1 || packets[0].CommandID != ref.CommandID {
		t.Fatalf("read back %d packets after the worktree was deleted", len(packets))
	}
}

// ---- durability -----------------------------------------------------------

func TestIntakeFiveConcurrentWritersPublishWithoutALockOrAPartialPacket(t *testing.T) {
	stHome(t)
	project := stDiscover(t, "datum/five-writers", ".datum/events")
	if _, err := store.WriteIntake(context.Background(), project, stRequest("", "first source", 1)); err != nil {
		t.Fatalf("control capture: %v", err)
	}

	const writers = 5
	start := make(chan struct{})
	refs := make([]model.PacketRef, writers)
	errs := make([]error, writers)
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			packets, err := store.ReadIntake(project, nil)
			if err != nil {
				t.Errorf("a concurrent reader saw an inbox it could not account for: %v", err)
				return
			}
			for _, p := range packets {
				if p.Project != project.ID || len(p.Events) == 0 {
					t.Errorf("a concurrent reader returned an incomplete packet: %+v", p)
					return
				}
			}
		}
	}()

	var writing sync.WaitGroup
	for i := 0; i < writers; i++ {
		writing.Add(1)
		go func(i int) {
			defer writing.Done()
			<-start
			refs[i], errs[i] = store.WriteIntake(context.Background(), project,
				stRequest("", fmt.Sprintf("source number %d written at the same moment as four others", i), 10+i))
		}(i)
	}
	close(start)
	writing.Wait()
	close(stop)
	readers.Wait()

	seen := map[model.ID]bool{}
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent writer %d failed, so capture is serialised by something: %v", i, err)
			continue
		}
		if seen[refs[i].CommandID] {
			t.Errorf("concurrent writer %d reused command id %s", i, refs[i].CommandID)
		}
		seen[refs[i].CommandID] = true
	}
	packets, err := store.ReadIntake(project, nil)
	if err != nil {
		t.Fatalf("reading the inbox after five concurrent writers: %v", err)
	}
	if len(packets) != writers+1 {
		t.Fatalf("the inbox holds %d packets after one plus five writes", len(packets))
	}
}

func TestIntakeRetryWithDifferentBytesUnderOneCommandIdIsAConflict(t *testing.T) {
	stHome(t)
	project := stDiscover(t, "datum/retry", ".datum/events")
	id := recID(100)

	first, err := store.WriteIntake(context.Background(), project, stRequest(id, "the original source", 1))
	if err != nil {
		t.Fatalf("control: the first capture under a chosen id must succeed: %v", err)
	}

	t.Run("an identical retry returns the existing packet", func(t *testing.T) {
		again, err := store.WriteIntake(context.Background(), project, stRequest(id, "the original source", 1))
		if err != nil {
			t.Fatalf("control: an identical retry must return the existing packet: %v", err)
		}
		if again != first {
			t.Errorf("retry returned %+v, want the original %+v", again, first)
		}
	})

	changed := []struct {
		name    string
		request store.IntakeRequest
	}{
		{"different source bytes", stRequest(id, "a quietly edited source", 1)},
		{"a different authored event", stRequest(id, "the original source", 2)},
		{"a different author", func() store.IntakeRequest {
			r := stRequest(id, "the original source", 1)
			r.Author = model.Actor{ID: "someone-else"}
			return r
		}()},
		{"an extra blob", func() store.IntakeRequest {
			r := stRequest(id, "the original source", 1)
			r.Blobs = append(r.Blobs, strings.NewReader("a second source nobody mentioned"))
			r.BuildEvents = func(blobs []store.CapturedBlob) ([]model.Event, error) {
				return stRequest(id, "the original source", 1).BuildEvents(blobs[:1])
			}
			return r
		}()},
	}
	for _, c := range changed {
		t.Run(c.name, func(t *testing.T) {
			ref, err := store.WriteIntake(context.Background(), project, c.request)
			if err == nil {
				t.Fatalf("retrying command %s with %s overwrote or accepted it and returned %+v", id, c.name, ref)
			}
			if stCode(err) != "conflict" {
				t.Errorf("retrying command %s with %s was refused with code %q, want conflict: %v",
					id, c.name, stCode(err), err)
			}
		})
	}

	packets, err := store.ReadIntake(project, []model.ID{id})
	if err != nil {
		t.Fatalf("the original packet is no longer readable after the conflicting retries: %v", err)
	}
	if len(packets) != 1 || packets[0].RequestDigest == "" {
		t.Fatalf("read back %+v", packets)
	}
}

func TestIntakeBlobNameMustEqualItsOwnContentHash(t *testing.T) {
	tamper := []struct {
		name string
		do   func(t *testing.T, blobDir string, name string)
	}{
		{"the bytes behind a valid name are replaced", func(t *testing.T, blobDir, name string) {
			stOverwrite(t, filepath.Join(blobDir, name), "different bytes under the same identity")
		}},
		{"a blob is renamed to another valid digest", func(t *testing.T, blobDir, name string) {
			other := string(recDigest('a'))
			if other == name {
				other = string(recDigest('b'))
			}
			if err := os.Rename(filepath.Join(blobDir, name), filepath.Join(blobDir, other)); err != nil {
				t.Fatal(err)
			}
		}},
		{"a blob is named something that is not a digest at all", func(t *testing.T, blobDir, name string) {
			if err := os.Rename(filepath.Join(blobDir, name), filepath.Join(blobDir, "source.md")); err != nil {
				t.Fatal(err)
			}
		}},
		{"an extra blob appears that no event mentions", func(t *testing.T, blobDir, name string) {
			extra := "planted bytes"
			if err := os.WriteFile(filepath.Join(blobDir, string(model.HashBytes([]byte(extra)))), []byte(extra), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"the only blob is removed", func(t *testing.T, blobDir, name string) {
			if err := os.Remove(filepath.Join(blobDir, name)); err != nil {
				t.Fatal(err)
			}
		}},
		{"one byte is appended to a blob", func(t *testing.T, blobDir, name string) {
			f, err := os.OpenFile(filepath.Join(blobDir, name), os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString("."); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}},
	}
	for _, c := range tamper {
		t.Run(c.name, func(t *testing.T) {
			stHome(t)
			project := stDiscover(t, "datum/blobs", ".datum/events")
			ref, err := store.WriteIntake(context.Background(), project, stRequest("", "the source bytes as captured", 1))
			if err != nil {
				t.Fatalf("control capture: %v", err)
			}
			if _, err := store.ReadIntake(project, []model.ID{ref.CommandID}); err != nil {
				t.Fatalf("control read before tampering: %v", err)
			}
			blobDir := filepath.Join(stInbox(t, project), string(ref.CommandID), "blobs")
			names := stEntries(t, blobDir)
			if len(names) != 1 {
				t.Fatalf("control packet holds %d blobs, want one", len(names))
			}
			c.do(t, blobDir, names[0])

			if _, err := store.ReadIntake(project, []model.ID{ref.CommandID}); err == nil {
				t.Errorf("a packet was returned as complete after %s", c.name)
			} else if code := stCode(err); code != "intake-corrupt" {
				t.Errorf("after %s the read failed with code %q, want intake-corrupt: %v", c.name, code, err)
			}
		})
	}
}

func stOverwrite(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// stCancelOnEOF hands over its bytes and then cancels the capture, which puts
// the interruption between the durable blob and the published packet.
type stCancelOnEOF struct {
	data   string
	cancel context.CancelFunc
}

func (r *stCancelOnEOF) Read(p []byte) (int, error) {
	if r.data == "" {
		r.cancel()
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestIntakeInterruptedPublishIsNeverVisibleAsACompletePacket(t *testing.T) {
	stHome(t)
	project := stDiscover(t, "datum/interrupted", ".datum/events")
	good, err := store.WriteIntake(context.Background(), project, stRequest("", "a source that was fully captured", 1))
	if err != nil {
		t.Fatalf("control capture: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := stRequest(recID(101), "a source captured just before the lights went out", 2)
	request.Blobs = []io.Reader{&stCancelOnEOF{data: "a source captured just before the lights went out", cancel: cancel}}
	if _, err := store.WriteIntake(ctx, project, request); err == nil {
		t.Fatal("a capture interrupted between the blob and the packet reported success")
	}

	packets, err := store.ReadIntake(project, nil)
	if err != nil {
		t.Fatalf("the inbox became unreadable after an interrupted capture: %v", err)
	}
	if len(packets) != 1 || packets[0].CommandID != good.CommandID {
		t.Fatalf("the inbox lists %d packets after one success and one interruption: %+v", len(packets), packets)
	}
	if _, err := store.ReadIntake(project, []model.ID{recID(101)}); err == nil {
		t.Error("the interrupted command id is readable as a complete packet")
	}

	t.Run("a staging directory left behind by a crash is never a packet", func(t *testing.T) {
		inbox := stInbox(t, project)
		leftover := filepath.Join(inbox, string(recID(102))+"-1234567890.tmp")
		if err := os.Mkdir(leftover, 0700); err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(inbox, string(good.CommandID))
		stCopyTree(t, source, leftover)

		packets, err := store.ReadIntake(project, nil)
		if err != nil {
			t.Fatalf("a leftover staging directory made the inbox unreadable: %v", err)
		}
		if len(packets) != 1 {
			t.Errorf("a leftover staging directory was returned as a packet: %d packets visible", len(packets))
		}
		if _, err := store.ReadIntake(project, []model.ID{recID(102)}); err == nil {
			t.Error("a leftover staging directory is readable under its own command id")
		}
	})
}

func stCopyTree(t *testing.T, from, to string) {
	t.Helper()
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		src, dst := filepath.Join(from, e.Name()), filepath.Join(to, e.Name())
		if e.IsDir() {
			if err := os.Mkdir(dst, 0700); err != nil {
				t.Fatal(err)
			}
			stCopyTree(t, src, dst)
			continue
		}
		body, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
