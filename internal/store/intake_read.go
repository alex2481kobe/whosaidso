package store

// The intake inventory a read walks, and the cheap check a read gives a packet
// the ledger has already reviewed. Full packet verification (layout, strict
// decode, every blob hashed, request digest) lives in intake_contents.go and is
// what admission and every presented packet get; publication does not belong here.

import (
	"os"
	"path/filepath"
	"strings"

	"datum/internal/model"
)

// IntakeIDs lists the currently visible published packets in command-id order,
// without reading any of them. Temporary siblings are never candidates; any
// other entry that is not a packet directory is intake-corrupt.
func IntakeIDs(project Project) ([]model.ID, error) {
	_, ids, err := intakeIDs(project, nil)
	return ids, err
}

// intakeIDs resolves the inbox and the ids a read covers: ids itself when it is
// non-nil, otherwise every published packet. The inbox's ancestors must be real
// owner-only directories; a missing inbox lists as empty only when no ids were named.
func intakeIDs(project Project, ids []model.ID) (string, []model.ID, error) {
	inbox, err := IntakeDir(project)
	if err != nil {
		return "", nil, err
	}
	for _, dir := range []string{filepath.Dir(filepath.Dir(inbox)), filepath.Dir(inbox), inbox} {
		if _, err := os.Lstat(dir); os.IsNotExist(err) && ids == nil {
			return inbox, []model.ID{}, nil
		}
		if err := checkIntakePath(dir, true); err != nil {
			return "", nil, err
		}
	}
	if ids != nil {
		return inbox, ids, nil
	}
	entries, err := os.ReadDir(inbox)
	if os.IsNotExist(err) {
		return inbox, []model.ID{}, nil
	}
	if err != nil {
		return "", nil, storeFault("io", inbox, err.Error())
	}
	ids = []model.ID{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}
		if !model.ValidID(model.ID(entry.Name())) || !entry.IsDir() {
			return "", nil, storeFault("intake-corrupt", filepath.Join(inbox, entry.Name()), "unexpected published intake entry")
		}
		ids = append(ids, model.ID(entry.Name()))
	}
	return inbox, ids, nil
}

// ReviewedPacketDigest is what a read checks of a packet a ledger review
// already names: its directory and packet.json are real owner-only entries, and
// the result is the digest of packet.json's exact bytes, for the caller to
// compare with the digest the review recorded. It does not decode the packet,
// hash its blobs or recompute its request digest. Admission did all of that
// under the lock before the review was written, and the ledger, not this
// machine's intake, is the authority for what was admitted. So a tampered
// packet.json fails every read, while a tampered blob of a reviewed packet is
// seen only by a full read (ReadVerifiedIntake). Never present a packet on the
// strength of this check alone.
func ReviewedPacketDigest(project Project, id model.ID) (model.Digest, error) {
	if !model.ValidID(id) {
		return "", storeFault("invalid-field", "intake.command_id", "not a ULID")
	}
	inbox, err := IntakeDir(project)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(inbox, string(id))
	if err := checkIntakePath(dir, true); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "packet.json")
	if err := checkIntakePath(path, false); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", storeFault("io", path, err.Error())
	}
	return model.HashBytes(data), nil
}
