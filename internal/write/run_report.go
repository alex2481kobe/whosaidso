package write

// Producer-report decoding, observed facts, output validation, and sealing live here.
// Intent freezing and process launch do not.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"datum/internal/model"
	"datum/internal/store"
)

// ProducerReport is the version 1 JSON artifact written to DATUM_RUN_REPORT.
// DATUM_RUN_DIR is a fresh directory for this invocation's output files.
// Example: {"version":1,"config_effective":{"samples":{"type":"number",
// "number":8}},"conditions_observed":{},"outputs":[{"path":"result.json",
// "media_type":"application/json"}]}.
//
// Maps contain model.Scalar values. Omitted maps are unknown. Omitted keys in a
// supplied map are explicitly unknown for declared knobs and conditions. Visual
// uses model.VisualObservation availability objects. Omitted visual fields,
// including nested availability fields, become unknown. A known limits object
// still requires the model's still_limitations text. No value is copied from intent.
// Outputs are regular files relative to DATUM_RUN_DIR, without symlinks or '..'.
// Large visual payloads use content pins to those files. Reports are limited to
// 1 MiB and 256 outputs. Invalid reports remain artifacts but supply no facts.
// Isolation is always unknown because this observer does not enforce isolation.
type ProducerReport struct {
	Version            uint16                   `json:"version"`
	ConfigEffective    map[string]model.Scalar  `json:"config_effective,omitempty"`
	ConditionsObserved map[string]model.Scalar  `json:"conditions_observed,omitempty"`
	Visual             *model.VisualObservation `json:"visual,omitempty"`
	Outputs            []RunOutput              `json:"outputs,omitempty"`
}

type RunOutput struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
}

func runSeal(ctx context.Context, project store.Project, request RunRequest, envelope model.InvocationEnvelope, dir string, stdout, stderr *os.File, streamErr error) (model.PacketRef, model.InvocationEnvelope, error) {
	files := []*os.File{stdout, stderr}
	outputs := []RunOutput{{Path: "stdout", MediaType: "application/octet-stream"}, {Path: "stderr", MediaType: "application/octet-stream"}}
	readers := []io.Reader{}
	for _, f := range files {
		info, err := f.Stat()
		if err != nil {
			return model.PacketRef{}, envelope, err
		}
		readers = append(readers, io.NewSectionReader(f, 0, info.Size()))
	}
	report, raw, reportErr := runReadReport(dir)
	if raw != nil {
		readers = append(readers, bytes.NewReader(raw))
		outputs = append(outputs, RunOutput{Path: "producer.json", MediaType: "application/json"})
	}
	if report != nil && reportErr == nil {
		candidate := envelope
		candidate.ConfigEffective = runObservedMap(report.ConfigEffective, request.Instrument.ConfigSurface)
		declared := make([]string, 0, len(envelope.ConditionsDeclared))
		for key := range envelope.ConditionsDeclared {
			declared = append(declared, key)
		}
		candidate.ConditionsObserved = runObservedMap(report.ConditionsObserved, declared)
		if report.Visual != nil {
			runVisualUnknowns(report.Visual)
			candidate.Visual = runKnown(*report.Visual)
		}
		reportErr = model.ValidateInvocationConfig(candidate, request.Instrument)
		if reportErr == nil {
			seen := map[string]bool{"stdout": true, "stderr": true, "producer.json": true}
			for _, output := range report.Outputs {
				if seen[output.Path] || model.Blank(output.MediaType) {
					reportErr = fmt.Errorf("run: duplicate output or missing media type")
					break
				}
				seen[output.Path] = true
				f, err := runOutputFile(dir, output.Path)
				if err != nil {
					reportErr = err
					break
				}
				defer f.Close()
				info, err := f.Stat()
				if err != nil {
					reportErr = err
					break
				}
				readers = append(readers, io.NewSectionReader(f, 0, info.Size()))
				outputs = append(outputs, output)
			}
		}
		if reportErr == nil {
			envelope = candidate
		}
	}
	if reportErr != nil {
		envelope.ConfigEffective.Reason = "producer report was invalid"
		envelope.ConditionsObserved.Reason = "producer report was invalid"
		envelope.Visual.Reason = "producer report was invalid"
	}
	ref, err := store.WriteIntake(ctx, project, store.IntakeRequest{
		Author: request.Author, Blobs: readers,
		BuildEvents: func(blobs []store.CapturedBlob) ([]model.Event, error) {
			refs := make([]model.ArtifactRef, len(blobs))
			for i, blob := range blobs {
				rel, err := filepath.Rel(project.Root, filepath.Join(dir, filepath.FromSlash(outputs[i].Path)))
				if err != nil {
					return nil, err
				}
				refs[i] = model.ArtifactRef{Kind: "content", Content: &model.ContentPin{SHA256: blob.SHA256, Length: blob.Length, MediaType: outputs[i].MediaType, Locators: []model.Locator{{Path: filepath.ToSlash(rel)}}}, Selector: model.Selector{Kind: "whole"}}
			}
			envelope.OutputRefs = runKnown(refs)
			if streamErr != nil {
				envelope.OutputRefs = model.Availability[[]model.ArtifactRef]{State: model.Unknown, Reason: "output capture was incomplete, captured bytes remain in this packet's blobs"}
			}
			event, err := model.EncodeEvent(&model.InvocationSeal{StartRef: model.InvocationRef{Project: project.ID, InvocationID: envelope.InvocationID}, Envelope: envelope})
			return []model.Event{event}, err
		},
	})
	return ref, envelope, errors.Join(reportErr, err)
}

func runObservedMap(reported map[string]model.Scalar, declared []string) model.Availability[map[string]model.Availability[model.Scalar]] {
	if reported == nil {
		return runUnknown[map[string]model.Availability[model.Scalar]]()
	}
	values := make(map[string]model.Availability[model.Scalar], len(reported)+len(declared))
	for _, name := range declared {
		values[name] = runUnknown[model.Scalar]()
	}
	for name, value := range reported {
		values[name] = runKnown(value)
	}
	return runKnown(values)
}

func runOutputFile(dir, name string) (*os.File, error) {
	if name == "" || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return nil, fmt.Errorf("run: output path must be relative")
	}
	parts := strings.Split(name, "/")
	current := dir
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("run: output path must not traverse directories")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || i < len(parts)-1 && !info.IsDir() || i == len(parts)-1 && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("run: output must be a regular file without symlinks")
		}
	}
	return os.Open(current)
}

func runReadReport(dir string) (*ProducerReport, []byte, error) {
	f, err := runOutputFile(dir, "producer.json")
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, runReportBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > runReportBytes {
		return nil, nil, fmt.Errorf("run: producer report exceeds 1 MiB")
	}
	shape := json.NewDecoder(bytes.NewReader(raw))
	shape.UseNumber()
	if err := runUniqueJSON(shape, 0, "$"); err != nil {
		return nil, raw, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var report ProducerReport
	if err := decoder.Decode(&report); err != nil {
		return nil, raw, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, raw, fmt.Errorf("run: producer report must be one JSON object")
	}
	if report.Version != 1 || len(report.Outputs) > 256 {
		return nil, raw, fmt.Errorf("run: unsupported report version or too many outputs")
	}
	return &report, raw, nil
}

// Last-key-wins decoding can turn contradictory producer facts into a seemingly
// unambiguous observation, so duplicates and case aliases are refused at every
// depth before typed decoding, with the model decoder's fault codes and paths.
func runUniqueJSON(decoder *json.Decoder, depth int, path string) error {
	if depth > 64 {
		return fmt.Errorf("run: producer report nesting exceeds 64 levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for i := 0; decoder.More(); i++ {
		childPath := fmt.Sprintf("%s[%d]", path, i)
		if delim == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return &model.Fault{Code: "invalid-json", Path: path, EventIndex: -1, Detail: "object key is not a string"}
			}
			childPath = path + "." + name
			if seen[name] {
				return &model.Fault{Code: "invalid-json", Path: childPath, EventIndex: -1, Detail: "duplicate object key"}
			}
			for previous := range seen {
				if strings.EqualFold(previous, name) {
					return &model.Fault{Code: "invalid-field", Path: childPath, EventIndex: -1, Detail: "unknown field, or a case variant of a known one"}
				}
			}
			seen[name] = true
		}
		if err := runUniqueJSON(decoder, depth+1, childPath); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func runMissing[T any](field *model.Availability[T]) {
	if field.State == "" && field.Value == nil && field.Reason == "" {
		*field = runUnknown[T]()
	}
}

func runVisualUnknowns(v *model.VisualObservation) {
	runMissing(&v.Framing)
	runMissing(&v.Transforms)
	runMissing(&v.Clip)
	runMissing(&v.Time)
	runMissing(&v.Seed)
	runMissing(&v.Backend)
	runMissing(&v.Appearance)
	runMissing(&v.Limits)
	if f := v.Framing.Value; f != nil {
		runMissing(&f.Subject)
		runMissing(&f.ProjectedBounds)
		runMissing(&f.CameraPosition)
		runMissing(&f.CameraTarget)
		runMissing(&f.Projection)
		runMissing(&f.Viewport)
		runMissing(&f.DPR)
	}
	if a := v.Appearance.Value; a != nil {
		runMissing(&a.Lights)
		runMissing(&a.Exposure)
		runMissing(&a.ColourSpace)
		runMissing(&a.Materials)
		runMissing(&a.DiagnosticOverrides)
	}
	if l := v.Limits.Value; l != nil {
		runMissing(&l.Occlusion)
		runMissing(&l.UnviewedSurfaces)
		runMissing(&l.UntestedBackends)
	}
}
