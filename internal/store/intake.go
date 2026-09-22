package store

// Durable intake publication, retries, and filesystem flush operations live here.
// Packet-content hashing, reading, and verification do not.

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"datum/internal/model"
)

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
