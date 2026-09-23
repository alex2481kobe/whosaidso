// This file constructs untimed, disposable intake fixture bytes. Public store
// verification checks the result; real capture and admission are timed elsewhere.
package benchmarks

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"datum/internal/model"
	"datum/internal/store"
)

// Bulk setup deliberately omits fsyncs. Replaying ten thousand historical
// admissions or syncing their many directories would dominate this experiment's
// runtime. Request digests follow the published packet format and are checked
// by ReadVerifiedIntake in fixture.append. No timed command takes this path.
func (f *fixture) bulkCapture(blobs [][]byte, events []model.Event) model.PacketRef {
	id := f.id()
	inbox, err := store.IntakeDir(f.Project)
	must(f.t, err)
	dir := filepath.Join(inbox, string(id))
	must(f.t, os.MkdirAll(filepath.Join(dir, "blobs"), 0700))
	unique := map[model.Digest]store.CapturedBlob{}
	for _, blob := range blobs {
		digest := model.HashBytes(blob)
		unique[digest] = store.CapturedBlob{SHA256: digest, Length: uint64(len(blob))}
		put(f.t, filepath.Join(dir, "blobs", string(digest)), blob)
	}
	inventory := make([]store.CapturedBlob, 0, len(unique))
	for _, blob := range unique {
		inventory = append(inventory, blob)
	}
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].SHA256 < inventory[j].SHA256 })
	identity, err := model.Encode(struct {
		Project model.ProjectID      `json:"project"`
		Author  model.Actor          `json:"author"`
		Events  []model.Event        `json:"events"`
		Blobs   []store.CapturedBlob `json:"blobs"`
	}{f.Project.ID, author, events, inventory})
	must(f.t, err)
	packet := model.Packet{Version: model.WireVersion, Project: f.Project.ID, CommandID: id, RequestDigest: model.HashBytes(identity), Author: author, CapturedAt: time.Now().UTC(), Events: events}
	data, err := model.Encode(packet)
	must(f.t, err)
	put(f.t, filepath.Join(dir, "packet.json"), data)
	return model.PacketRef{CommandID: id, Digest: model.HashBytes(data)}
}
