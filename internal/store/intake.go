package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"datum/internal/model"
)

// CapturedBlob describes bytes copied into a packet, never their original path.
type CapturedBlob struct {
	SHA256 model.Digest `json:"sha256"`
	Length uint64       `json:"length"`
}

// IntakeRequest separates authored facts from storage's clock, ids and hashes.
// An empty CommandID mints a ULID; callers retain the returned id for retries.
// Blobs are consumed from their current positions, without closing the readers.
// Events or BuildEvents supplies the events, never both. BuildEvents receives
// blob identities in input order so source events can use hashes computed here.
// On retry callers supply fresh readers and the same authored facts.
type IntakeRequest struct {
	CommandID   model.ID
	Author      model.Actor
	Events      []model.Event
	Blobs       []io.Reader
	BuildEvents func([]CapturedBlob) ([]model.Event, error)
}

// IntakeDir is runtime-only: a portable, injective encoding keeps logical ids
// containing slashes or Unicode distinct without making them filesystem paths.
func IntakeDir(project Project) (string, error) {
	if project.ID == "" || !utf8.ValidString(string(project.ID)) || strings.ContainsRune(string(project.ID), 0) {
		return "", storeFault("invalid-field", "project.id", "expected a nonempty UTF-8 project id without NUL")
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", storeFault("io", "home", "an absolute user home directory is required")
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(project.ID))
	return filepath.Join(home, ".datum", "intake", encoded), nil
}

// intakeIO is a narrow durability boundary for failure/crash tests. There is no
// global hook or lock: each writer owns its staging directory and test boundary.
type intakeIO struct {
	sync   func(*os.File) error
	rename func(string, string) error
}

func systemIntakeIO() intakeIO {
	return intakeIO{sync: (*os.File).Sync, rename: os.Rename}
}

// WriteIntake acknowledges only after blob bytes, packet bytes, their directory
// entries and publication have been flushed. Publication is one directory
// rename; another writer can never expose this writer's unfinished staging tree.
func WriteIntake(ctx context.Context, project Project, request IntakeRequest) (model.PacketRef, error) {
	return writeIntake(ctx, project, request, systemIntakeIO())
}

func writeIntake(ctx context.Context, project Project, request IntakeRequest, disk intakeIO) (model.PacketRef, error) {
	var zero model.PacketRef
	// os.Rename's atomic-directory guarantee is platform dependent. Do not
	// acknowledge durable capture on a platform this publisher cannot support.
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return zero, storeFault("unsupported-platform", "intake", "durable intake publication currently supports Linux and macOS")
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	inbox, err := IntakeDir(project)
	if err != nil {
		return zero, err
	}
	at := time.Now().UTC()
	id := request.CommandID
	if id == "" {
		id, err = model.NewID(at, rand.Reader)
		if err != nil {
			return zero, err
		}
	} else if !model.ValidID(id) {
		return zero, storeFault("invalid-field", "request.command_id", "not a ULID")
	}
	if request.BuildEvents != nil && len(request.Events) != 0 {
		return zero, storeFault("invalid-field", "request.events", "supply Events or BuildEvents, not both")
	}
	// Sync even existing ancestors. A concurrent creator may have made one
	// visible without yet flushing its name into its own parent directory.
	for _, dir := range []string{filepath.Dir(filepath.Dir(inbox)), filepath.Dir(inbox), inbox} {
		if err := intakeMkdir(dir); err != nil {
			return zero, err
		}
		if err := syncIntakePath(dir, disk); err != nil {
			return zero, err
		}
		if err := syncIntakePath(filepath.Dir(dir), disk); err != nil {
			return zero, err
		}
	}
	tmp, err := os.MkdirTemp(inbox, string(id)+"-*.tmp")
	if err != nil {
		return zero, storeFault("io", inbox, err.Error())
	}
	// Only this writer's unpublished temporary is ours to remove. A crash may
	// leave it behind; readers ignore it and never clean another writer's temp.
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0700); err != nil {
		return zero, storeFault("io", tmp, err.Error())
	}
	blobDir := filepath.Join(tmp, "blobs")
	if err := intakeMkdir(blobDir); err != nil {
		return zero, err
	}
	captured := make([]CapturedBlob, 0, len(request.Blobs))
	for i, reader := range request.Blobs {
		if reader == nil {
			return zero, storeFault("invalid-field", fmt.Sprintf("request.blobs[%d]", i), "nil stream")
		}
		blob, err := captureBlob(ctx, blobDir, reader, disk)
		if err != nil {
			return zero, err
		}
		captured = append(captured, blob)
	}
	events := request.Events
	if request.BuildEvents != nil {
		// The callback may retain or mutate its slice; inventory remains ours.
		events, err = request.BuildEvents(append([]CapturedBlob(nil), captured...))
		if err != nil {
			return zero, err
		}
	}
	p := model.Packet{Version: model.WireVersion, Project: project.ID, CommandID: id, Author: request.Author, CapturedAt: at, Events: events}
	p.RequestDigest, err = intakeRequestDigest(p, captured)
	if err != nil {
		return zero, storeFault("invalid-field", "request", err.Error())
	}
	data, err := model.Encode(p)
	if err != nil {
		return zero, err
	}
	if _, err := model.DecodePacket(data); err != nil {
		return zero, err
	}
	if err := syncIntakePath(blobDir, disk); err != nil {
		return zero, err
	}
	if err := writeIntakeFile(filepath.Join(tmp, "packet.json"), data, disk); err != nil {
		return zero, err
	}
	if err := syncIntakePath(tmp, disk); err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	final := filepath.Join(inbox, string(id))
	if _, err := os.Lstat(final); err == nil {
		return retryIntake(project, final, p.RequestDigest, disk)
	} else if !os.IsNotExist(err) {
		return zero, storeFault("io", final, err.Error())
	}
	// Valid published directories are nonempty, so POSIX rename cannot replace
	// one. Concurrent identical ids race here, then verify the winner's bytes.
	if err := disk.rename(tmp, final); err != nil {
		if _, statErr := os.Lstat(final); statErr == nil {
			return retryIntake(project, final, p.RequestDigest, disk)
		}
		return zero, storeFault("io", final, err.Error())
	}
	if err := syncIntakePath(inbox, disk); err != nil {
		return zero, uncertainIntake(id, final, err)
	}
	return model.PacketRef{CommandID: id, Digest: model.HashBytes(data)}, nil
}

func captureBlob(ctx context.Context, dir string, reader io.Reader, disk intakeIO) (CapturedBlob, error) {
	var zero CapturedBlob
	f, err := os.CreateTemp(dir, "blob-*.tmp")
	if err != nil {
		return zero, storeFault("io", dir, err.Error())
	}
	path := f.Name()
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return zero, storeFault("io", path, err.Error())
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), intakeReader{ctx: ctx, reader: reader})
	if copyErr == nil {
		copyErr = disk.sync(f)
	}
	closeErr := f.Close()
	if copyErr != nil {
		return zero, storeFault("io", path, copyErr.Error())
	}
	if closeErr != nil {
		return zero, storeFault("io", path, closeErr.Error())
	}
	blob := CapturedBlob{SHA256: model.Digest(hex.EncodeToString(h.Sum(nil))), Length: uint64(n)}
	// Duplicate bytes in one request share an identity and one immutable file.
	if err := os.Rename(path, filepath.Join(dir, string(blob.SHA256))); err != nil {
		return zero, storeFault("io", path, err.Error())
	}
	return blob, nil
}

type intakeReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r intakeReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// intakeRequestDigest includes the inventory in the frozen envelope's existing
// request digest. Missing, extra or altered blobs then fail verification without
// a second manifest or a change to model.Packet. Stream order and duplication
// are clerical; authored event order remains significant.
func intakeRequestDigest(p model.Packet, blobs []CapturedBlob) (model.Digest, error) {
	unique := make(map[model.Digest]CapturedBlob, len(blobs))
	for _, blob := range blobs {
		unique[blob.SHA256] = blob
	}
	inventory := make([]CapturedBlob, 0, len(unique))
	for _, blob := range unique {
		inventory = append(inventory, blob)
	}
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].SHA256 < inventory[j].SHA256 })
	data, err := model.Encode(struct {
		Project model.ProjectID `json:"project"`
		Author  model.Actor     `json:"author"`
		Events  []model.Event   `json:"events"`
		Blobs   []CapturedBlob  `json:"blobs"`
	}{p.Project, p.Author, p.Events, inventory})
	if err != nil {
		return "", err
	}
	return model.HashBytes(data), nil
}

// ReadIntake returns verified complete packets. Nil ids selects the currently
// visible packets in command-id order; a non-nil empty slice selects none.
// Temporary siblings are never candidates, even if they contain valid JSON.
func ReadIntake(project Project, ids []model.ID) ([]model.Packet, error) {
	inbox, err := IntakeDir(project)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{filepath.Dir(filepath.Dir(inbox)), filepath.Dir(inbox), inbox} {
		if _, err := os.Lstat(dir); os.IsNotExist(err) && ids == nil {
			return []model.Packet{}, nil
		}
		if err := checkIntakePath(dir, true); err != nil {
			return nil, err
		}
	}
	if ids == nil {
		entries, err := os.ReadDir(inbox)
		if os.IsNotExist(err) {
			return []model.Packet{}, nil
		}
		if err != nil {
			return nil, storeFault("io", inbox, err.Error())
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".tmp") {
				continue
			}
			if !model.ValidID(model.ID(entry.Name())) || !entry.IsDir() {
				return nil, storeFault("intake-corrupt", filepath.Join(inbox, entry.Name()), "unexpected published intake entry")
			}
			ids = append(ids, model.ID(entry.Name()))
		}
	}
	packets := make([]model.Packet, 0, len(ids))
	for _, id := range ids {
		if !model.ValidID(id) {
			return nil, storeFault("invalid-field", "intake.command_id", "not a ULID")
		}
		p, _, _, err := readIntakePacket(project, filepath.Join(inbox, string(id)))
		if err != nil {
			return nil, err
		}
		packets = append(packets, p)
	}
	return packets, nil
}

func readIntakePacket(project Project, dir string) (model.Packet, model.PacketRef, []CapturedBlob, error) {
	var p model.Packet
	var ref model.PacketRef
	if err := checkIntakePath(dir, true); err != nil {
		return p, ref, nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return p, ref, nil, storeFault("io", dir, err.Error())
	}
	if len(entries) != 2 || entries[0].Name() != "blobs" || entries[1].Name() != "packet.json" {
		return p, ref, nil, storeFault("intake-corrupt", dir, "a packet must contain only packet.json and blobs/")
	}
	path := filepath.Join(dir, "packet.json")
	if err := checkIntakePath(path, false); err != nil {
		return p, ref, nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return p, ref, nil, storeFault("io", path, err.Error())
	}
	p, err = model.DecodePacket(data)
	if err != nil {
		return p, ref, nil, storeFault("intake-corrupt", path, err.Error())
	}
	if p.Project != project.ID || string(p.CommandID) != filepath.Base(dir) || p.CapturedAt.IsZero() {
		return p, ref, nil, storeFault("intake-corrupt", path, "packet project, command id or capture time disagrees with its location")
	}
	blobDir := filepath.Join(dir, "blobs")
	if err := checkIntakePath(blobDir, true); err != nil {
		return p, ref, nil, err
	}
	entries, err = os.ReadDir(blobDir)
	if err != nil {
		return p, ref, nil, storeFault("io", blobDir, err.Error())
	}
	blobs := make([]CapturedBlob, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(blobDir, entry.Name())
		if !model.ValidDigest(model.Digest(entry.Name())) {
			return p, ref, nil, storeFault("intake-corrupt", path, "blob names must be raw SHA-256 identities")
		}
		if err := checkIntakePath(path, false); err != nil {
			return p, ref, nil, err
		}
		f, err := os.Open(path)
		if err != nil {
			return p, ref, nil, storeFault("io", path, err.Error())
		}
		h := sha256.New()
		n, readErr := io.Copy(h, f)
		closeErr := f.Close()
		if readErr != nil || closeErr != nil {
			return p, ref, nil, storeFault("io", path, fmt.Sprintf("read: %v; close: %v", readErr, closeErr))
		}
		if hex.EncodeToString(h.Sum(nil)) != entry.Name() {
			return p, ref, nil, storeFault("intake-corrupt", path, "blob bytes do not match their identity")
		}
		blobs = append(blobs, CapturedBlob{SHA256: model.Digest(entry.Name()), Length: uint64(n)})
	}
	digest, err := intakeRequestDigest(p, blobs)
	if err != nil {
		return p, ref, nil, err
	}
	if digest != p.RequestDigest {
		return p, ref, nil, storeFault("intake-corrupt", dir, "request digest does not match author, events and complete blob inventory")
	}
	return p, model.PacketRef{CommandID: p.CommandID, Digest: model.HashBytes(data)}, blobs, nil
}

func retryIntake(project Project, dir string, digest model.Digest, disk intakeIO) (model.PacketRef, error) {
	p, ref, blobs, err := readIntakePacket(project, dir)
	if err != nil {
		return model.PacketRef{}, err
	}
	if p.RequestDigest != digest {
		return model.PacketRef{}, storeFault("conflict", dir, "command id already captured a different request")
	}
	// A prior writer may have lost its acknowledgement immediately after rename.
	// Reflush the existing publication before promising durability on a retry.
	paths := make([]string, 0, len(blobs)+4)
	for _, blob := range blobs {
		paths = append(paths, filepath.Join(dir, "blobs", string(blob.SHA256)))
	}
	paths = append(paths, filepath.Join(dir, "packet.json"), filepath.Join(dir, "blobs"), dir, filepath.Dir(dir))
	for _, path := range paths {
		if err := syncIntakePath(path, disk); err != nil {
			return model.PacketRef{}, uncertainIntake(p.CommandID, dir, err)
		}
	}
	return ref, nil
}

func uncertainIntake(id model.ID, path string, err error) error {
	return storeFault("uncertain-ack", path, fmt.Sprintf("command %s is published but durability was not confirmed; retry the identical command: %v", id, err))
}

func intakeMkdir(path string) error {
	if err := os.Mkdir(path, 0700); err != nil {
		if !os.IsExist(err) {
			return storeFault("io", path, err.Error())
		}
	} else if err := os.Chmod(path, 0700); err != nil {
		return storeFault("io", path, err.Error())
	}
	return checkIntakePath(path, true)
}

func checkIntakePath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return storeFault("intake-not-found", path, "packet or required content is absent")
	}
	if err != nil {
		return storeFault("io", path, err.Error())
	}
	if info.Mode()&os.ModeSymlink != 0 || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return storeFault("intake-corrupt", path, "expected a real directory or regular file, never a symlink")
	}
	if info.Mode().Perm()&0077 != 0 {
		return storeFault("insecure-permissions", path, "intake requires owner-only access")
	}
	return nil
}

func syncIntakePath(path string, disk intakeIO) error {
	f, err := os.Open(path)
	if err != nil {
		return storeFault("io", path, err.Error())
	}
	syncErr := disk.sync(f)
	closeErr := f.Close()
	if syncErr != nil {
		return storeFault("io", path, syncErr.Error())
	}
	if closeErr != nil {
		return storeFault("io", path, closeErr.Error())
	}
	return nil
}

func writeIntakeFile(path string, data []byte, disk intakeIO) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return storeFault("io", path, err.Error())
	}
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = disk.sync(f)
	}
	closeErr := f.Close()
	if err != nil {
		return storeFault("io", path, err.Error())
	}
	if closeErr != nil {
		return storeFault("io", path, closeErr.Error())
	}
	return nil
}
