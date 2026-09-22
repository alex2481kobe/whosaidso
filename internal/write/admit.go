// Package write joins immutable capture to canonical state through one admission gate.
package write

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"datum/internal/evidence"
	"datum/internal/model"
	"datum/internal/reduce"
	"datum/internal/store"
)

// AdmitRequest has its own command identity because one admission can carry many packets.
type AdmitRequest struct {
	CommandID model.ID
	PacketIDs []model.ID
	Admitter  model.Actor
	Outcome   string
	Reason    string
}

// Admit is the only canonical writer in this package. Capture never calls it implicitly.
func Admit(ctx context.Context, project store.Project, request AdmitRequest) (model.Bundle, error) {
	ids, err := admissionIDs(request)
	if err != nil {
		return model.Bundle{}, err
	}
	// Only request identity is read early. Every authoritative check is repeated
	// after Transact selects the prefix, including the exact packet bytes.
	_, refs, err := admissionPackets(project, ids)
	if err != nil {
		return model.Bundle{}, err
	}
	data, err := model.Encode(struct {
		Project model.ProjectID   `json:"project"`
		Packets []model.PacketRef `json:"packets"`
		Actor   model.Actor       `json:"actor"`
		Outcome string            `json:"outcome"`
		Reason  string            `json:"reason"`
	}{project.ID, refs, request.Admitter, request.Outcome, request.Reason})
	if err != nil {
		return model.Bundle{}, err
	}
	digest := model.HashBytes(data)
	return store.Transact(ctx, project, request.CommandID, digest, func(prefix []model.Bundle) (model.Bundle, error) {
		snapshot, err := reduce.Replay(prefix)
		if err != nil {
			return model.Bundle{}, err
		}
		packets, lockedRefs, err := admissionPackets(project, ids)
		if err != nil {
			return model.Bundle{}, err
		}
		for i, ref := range lockedRefs {
			if ref != refs[i] {
				return model.Bundle{}, admissionFault("conflict", "packets", "packet bytes changed while waiting for admission")
			}
			if prior, ok := snapshot.Review(reduce.ReviewKey{Project: project.ID, CommandID: ref.CommandID}); ok {
				return model.Bundle{}, admissionFault("conflict", "packets", fmt.Sprintf("packet %s already has disposition %s", ref.CommandID, prior.Outcome))
			}
		}
		proposal := model.Bundle{Admitter: request.Admitter, Packets: lockedRefs, Events: []model.Event{}}
		if request.Outcome == "accepted" {
			packets, err = gatePackets(project.ID, snapshot, packets)
			if err != nil {
				return model.Bundle{}, err
			}
			for _, packet := range packets {
				proposal.Events = append(proposal.Events, packet.Events...)
			}
		}
		selfAdmission, reason := admissionDetails(request, packets)
		review, err := model.EncodeEvent(&model.ReviewAdmit{
			Packets: lockedRefs, Outcome: request.Outcome, Actor: request.Admitter,
			Reason: reason, SelfAdmission: selfAdmission,
		})
		if err != nil {
			return model.Bundle{}, err
		}
		proposal.Events = append(proposal.Events, review)
		if err := gateProposal(snapshot, project.ID, request.CommandID, digest, proposal); err != nil {
			return model.Bundle{}, err
		}
		if request.Outcome == "accepted" {
			if err := materializeAdmission(ctx, project, packets); err != nil {
				return model.Bundle{}, err
			}
		}
		// The store alone seals the envelope. The validation copy never becomes
		// the proposal, so a tail selected outside the store cannot be published.
		return proposal, nil
	})
}

func admissionIDs(r AdmitRequest) ([]model.ID, error) {
	if !model.ValidID(r.CommandID) {
		return nil, admissionFault("invalid-field", "command_id", "admission needs its own ULID")
	}
	if err := model.ValidateSchema(r.Admitter); err != nil {
		return nil, err
	}
	if r.Outcome != "accepted" && r.Outcome != "rejected" && r.Outcome != "correction-requested" {
		return nil, admissionFault("invalid-field", "outcome", "expected accepted, rejected or correction-requested")
	}
	if model.Blank(r.Reason) || len(r.PacketIDs) == 0 {
		return nil, admissionFault("invalid-field", "request", "admission needs a reason and a nonempty packet set")
	}
	seen := map[model.ID]bool{}
	ids := make([]model.ID, 0, len(r.PacketIDs))
	for _, id := range r.PacketIDs {
		if !model.ValidID(id) || id == r.CommandID {
			return nil, admissionFault("invalid-field", "packet_ids", "packet ids must be ULIDs distinct from the admission id")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func admissionPackets(project store.Project, ids []model.ID) ([]model.Packet, []model.PacketRef, error) {
	packets, err := store.ReadIntake(project, ids)
	if err != nil {
		return nil, nil, err
	}
	inbox, err := store.IntakeDir(project)
	if err != nil {
		return nil, nil, err
	}
	refs := make([]model.PacketRef, len(packets))
	for i, packet := range packets {
		path := filepath.Join(inbox, string(packet.CommandID), "packet.json")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		decoded, err := model.DecodePacket(data)
		if err != nil {
			return nil, nil, err
		}
		before, err := model.Encode(packet)
		if err != nil {
			return nil, nil, err
		}
		after, err := model.Encode(decoded)
		if err != nil {
			return nil, nil, err
		}
		if !bytes.Equal(before, after) {
			return nil, nil, admissionFault("conflict", path, "packet changed during verification")
		}
		// Hash stored bytes, not a reconstructed packet with different whitespace.
		refs[i] = model.PacketRef{CommandID: packet.CommandID, Digest: model.HashBytes(data)}
	}
	return packets, refs, nil
}

// admissionDetails renders the readable suffix from the same facts we persist.
func admissionDetails(r AdmitRequest, packets []model.Packet) (map[model.ID]model.SelfAdmissionState, string) {
	states := make(map[model.ID]model.SelfAdmissionState, len(packets))
	var reason strings.Builder
	reason.WriteString(r.Reason)
	for _, p := range packets {
		self := model.SelfAdmissionUnknown
		if model.SameActor(p.Author, r.Admitter) {
			self = model.SelfAdmissionTrue
		} else if !model.Blank(p.Author.ID) && !model.Blank(r.Admitter.ID) {
			self = model.SelfAdmissionFalse
		}
		states[p.CommandID] = self
		author := "unknown: " + p.Author.UnknownReason
		if !model.Blank(p.Author.ID) {
			author = p.Author.ID
		}
		fmt.Fprintf(&reason, "\nPacket %s author %s. Self-admitted: %s.", p.CommandID, strconv.Quote(author), self)
	}
	return states, reason.String()
}

func materializeAdmission(ctx context.Context, project store.Project, packets []model.Packet) error {
	inbox, err := store.IntakeDir(project)
	if err != nil {
		return err
	}
	resolver := evidence.NewResolver(project.Root)
	for _, packet := range packets {
		for _, raw := range packet.Events {
			event, err := model.DecodeEvent(raw)
			if err != nil {
				return err
			}
			for _, ref := range admissionArtifacts(event) {
				if err := ctx.Err(); err != nil {
					return err
				}
				if ref.Content != nil {
					for _, candidate := range packets {
						path := filepath.Join(inbox, string(candidate.CommandID), "blobs", string(ref.Content.SHA256))
						blob, err := admissionBlob(path)
						if os.IsNotExist(err) {
							continue
						}
						if err != nil {
							return err
						}
						if model.HashBytes(blob) != ref.Content.SHA256 || uint64(len(blob)) != ref.Content.Length {
							return admissionFault("conflict", path, "intake bytes disagree with the accepted content pin")
						}
						if err := preserveAdmissionBlob(project.Root, blob); err != nil {
							return err
						}
						break
					}
				}
				resolved, err := resolver.Resolve(ctx, ref)
				if err != nil {
					return fmt.Errorf("accepted support is unavailable or invalid: %w", err)
				}
				// A working locator can disappear with its producer. Keep its verified
				// bytes without adding our location to the authored event.
				if resolved.Origin == evidence.OriginLocator {
					if err := preserveAdmissionBlob(project.Root, resolved.Bytes); err != nil {
						return err
					}
				}
				reading, err := evidence.Select(resolved, ref.Selector)
				if err != nil {
					return err
				}
				if reading.Kind == evidence.ReadingAbsent {
					return admissionFault("unavailable", "artifact.selector", reading.Reason)
				}
				if source, ok := event.(*model.SourceIntake); ok && (resolved.SHA256 != source.OriginalDigest || resolved.Length != source.Length) {
					return admissionFault("conflict", "source_ref", "resolved source differs from the captured original")
				}
			}
		}
	}
	return nil
}

func admissionBlob(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, admissionFault("invalid-field", path, "artifact must be a regular file, never a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, evidence.DefaultMaxBytes+1))
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > evidence.DefaultMaxBytes {
		return nil, admissionFault("unavailable", path, "artifact exceeds the resolver byte limit")
	}
	return data, nil
}

func preserveAdmissionBlob(root string, data []byte) error {
	if !filepath.IsAbs(root) {
		return admissionFault("invalid-field", "project.root", "artifact publication needs an absolute project root")
	}
	dir := root
	for _, part := range strings.Split(evidence.DefaultArtifactDir, "/") {
		parent := dir
		dir = filepath.Join(dir, part)
		if err := os.Mkdir(dir, 0755); err != nil && !os.IsExist(err) {
			return err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return admissionFault("invalid-field", dir, "artifact directories cannot be symlinks")
		}
		if err := syncAdmissionPath(parent); err != nil {
			return err
		}
	}
	final := filepath.Join(dir, string(model.HashBytes(data)))
	if prior, err := admissionBlob(final); err == nil {
		if !bytes.Equal(prior, data) {
			return admissionFault("conflict", final, "existing artifact bytes do not match their name")
		}
		if err := syncAdmissionPath(final); err != nil {
			return err
		}
		return syncAdmissionPath(dir)
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(dir, ".admit-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	// Link publishes without overwriting an immutable artifact created elsewhere.
	if err := os.Link(f.Name(), final); err != nil {
		return err
	}
	verified, err := admissionBlob(final)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, verified) {
		return admissionFault("conflict", final, "published artifact failed byte verification")
	}
	return syncAdmissionPath(dir)
}

func syncAdmissionPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func admissionFault(code, path, detail string) error {
	return &model.Fault{Code: code, EventIndex: -1, Path: path, Detail: detail}
}
