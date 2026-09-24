package store

// Intake locations, blob capture, request digests, the source-durability rule,
// and packet-content verification live here.
// Publication sequencing, retry acknowledgement, and filesystem flush operations do not.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"whosaidso/internal/model"
)

// CapturedBlob describes bytes copied into a packet, never their original path.
type CapturedBlob struct {
	SHA256 model.Digest `json:"sha256"`
	Length uint64       `json:"length"`
}

// IntakeDir is runtime-only: a portable, injective encoding keeps logical ids
// containing slashes or Unicode distinct without making them filesystem paths.
func IntakeDir(project Project) (string, error) {
	encoded, err := encodeProjectID(project.ID)
	if err != nil {
		return "", err
	}
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "intake", encoded), nil
}

// encodeProjectID names a project inside the WhoSaidSo home: its intake, staging
// and registry binding all use this one name.
func encodeProjectID(id model.ProjectID) (string, error) {
	if id == "" || !utf8.ValidString(string(id)) || strings.ContainsRune(string(id), 0) {
		return "", storeFault("invalid-field", "project.id", "expected a nonempty UTF-8 project id without NUL")
	}
	// Lowercase hex, not base64. base64 IS injective over byte strings, which is
	// a true measurement of the wrong property: the inbox is a filesystem PATH,
	// and macOS APFS folds case, so "datum/aaa" and "datum/aaG" encode to names
	// differing only in case and become ONE directory. Both projects' packets
	// land together and then neither can read its own. Found by lane E.
	//
	// Hex has one case, so two different ids cannot fold onto each other.
	return hex.EncodeToString([]byte(id)), nil
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

// requireSourceBytes is the capture-durability rule: a packet is acknowledged
// only when its own blob inventory holds every source.intake's exact original
// bytes. Events are otherwise opaque to storage; a source.intake is decoded
// just far enough to read that reference, and one that cannot be decoded is
// refused, because an unreadable reference cannot be shown to be saved.
func requireSourceBytes(events []model.Event, captured []CapturedBlob) error {
	have := make(map[CapturedBlob]bool, len(captured))
	for _, blob := range captured {
		have[blob] = true
	}
	for i, raw := range events {
		if raw.Type != "source.intake" {
			continue
		}
		event, err := model.DecodeEvent(raw)
		if err != nil {
			return err
		}
		source, ok := event.(*model.SourceIntake)
		if !ok || !have[CapturedBlob{SHA256: source.OriginalDigest, Length: source.Length}] {
			return storeFault("source-not-captured", fmt.Sprintf("request.events[%d].source_ref", i),
				"the packet's blobs do not hold the original source bytes, so capture cannot save them durably")
		}
	}
	return nil
}

// VerifiedPacket is one intake packet as a single read verified it: the decoded
// packet, the digest of the exact packet.json bytes that decoded to it, and the
// blob inventory whose bytes were hashed against their names.
//
// It is a snapshot of that read, not a standing fact about the inbox. A caller
// may use it for the rest of the operation that read it, never across commands.
type VerifiedPacket struct {
	Packet model.Packet
	Ref    model.PacketRef
	Blobs  []CapturedBlob
}

// ReadIntake returns verified complete packets. Nil ids selects the currently
// visible packets in command-id order; a non-nil empty slice selects none.
// Temporary siblings are never candidates, even if they contain valid JSON.
func ReadIntake(project Project, ids []model.ID) ([]model.Packet, error) {
	verified, err := ReadVerifiedIntake(project, ids)
	if err != nil {
		return nil, err
	}
	packets := make([]model.Packet, len(verified))
	for i, v := range verified {
		packets[i] = v.Packet
	}
	return packets, nil
}

// ReadVerifiedIntake is ReadIntake keeping what verification already computed,
// so an admission never reopens or rehashes a packet it has just verified.
func ReadVerifiedIntake(project Project, ids []model.ID) ([]VerifiedPacket, error) {
	inbox, ids, err := intakeIDs(project, ids)
	if err != nil {
		return nil, err
	}
	packets := make([]VerifiedPacket, 0, len(ids))
	for _, id := range ids {
		if !model.ValidID(id) {
			return nil, storeFault("invalid-field", "intake.command_id", "not a ULID")
		}
		p, ref, blobs, err := readIntakePacket(project, filepath.Join(inbox, string(id)))
		if err != nil {
			return nil, err
		}
		packets = append(packets, VerifiedPacket{Packet: p, Ref: ref, Blobs: blobs})
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
		// Reads never mutate published packets. Permission drift is recoverable
		// without deleting, rewriting or recapturing their immutable bytes.
		mode := "0600"
		if directory {
			mode = "0700"
		}
		quotedPath := "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
		return storeFault("insecure-permissions", path,
			"intake requires owner-only access; restore permissions with chmod "+mode+" "+quotedPath+
				"; then retry the identical read or write; do not delete or recapture the packet")
	}
	return nil
}
