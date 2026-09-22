package write

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"datum/internal/model"
	"datum/internal/store"
)

const (
	runTailBytes   = 16 << 10
	runReportBytes = 1 << 20
	runWaitDelay   = time.Second
)

// RunRequest carries intent only. Instrument is the spec at InstrumentRef so
// reported configuration names can be checked without taking an admission lock.
// Dir defaults to the project root. Env defaults to the inherited environment.
// Timeout zero leaves the execution deadline to ctx. Run never invokes a shell
// implicitly and never treats an invocation outcome as an attempt outcome.
type RunRequest struct {
	Author                  model.Actor
	AttemptID               model.ID
	InstrumentRef           model.RecordRef
	Instrument              model.InstrumentSpec
	CriterionRef            model.Availability[model.CriterionRef]
	ExecutionSourceIdentity model.ExecutionIdentity
	Argv                    []string
	InputRefs               []model.ArtifactRef
	ConfigRequested         map[string]model.Scalar
	ConditionsDeclared      map[string]model.Scalar
	Dir                     string
	Env                     []string
	Timeout                 time.Duration
}

// RunResult retains packet acknowledgements even when the process or seal fails.
// Tails are diagnostics only and never enter a packet. A zero SealPacket means
// the durable record has no acknowledged terminal observation.
type RunResult struct {
	Envelope    model.InvocationEnvelope
	StartPacket model.PacketRef
	SealPacket  model.PacketRef
	ArtifactDir string
	StdoutTail  []byte
	StderrTail  []byte
}

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

// Run publishes intent before even constructing the command. Once a child has
// been observed, cancellation must not erase that observation, so final capture
// gets a separate bounded context. A killed observer cannot execute this seal.
func Run(ctx context.Context, project store.Project, request RunRequest) (RunResult, error) {
	var result RunResult
	envelope, err := runIntent(project, request)
	if err != nil {
		return result, err
	}
	result.Envelope = envelope
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if request.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	dir := filepath.Join(project.Root, "record", "artifacts", "runs", string(envelope.InvocationID))
	result.ArtifactDir = dir
	if err := runMakeDir(project.Root, dir); err != nil {
		return result, err
	}
	stdout, err := runOpenStream(filepath.Join(dir, "stdout"))
	if err != nil {
		return result, err
	}
	defer stdout.file.Close()
	stderr, err := runOpenStream(filepath.Join(dir, "stderr"))
	if err != nil {
		return result, err
	}
	defer stderr.file.Close()
	outPipe, err := runNewPipe()
	if err != nil {
		return result, err
	}
	defer outPipe.close()
	errPipe, err := runNewPipe()
	if err != nil {
		return result, err
	}
	defer errPipe.close()
	start, err := model.EncodeEvent(&model.InvocationStart{Envelope: envelope})
	if err != nil {
		return result, err
	}
	result.StartPacket, err = store.WriteIntake(ctx, project, store.IntakeRequest{Author: request.Author, Events: []model.Event{start}})
	if err != nil {
		return result, err
	}

	cmd := exec.CommandContext(ctx, envelope.Argv[0], envelope.Argv[1:]...)
	cmd.Dir = request.Dir
	if cmd.Dir == "" {
		cmd.Dir = project.Root
	}
	cmd.Env = request.Env
	if cmd.Env == nil {
		cmd.Env = cmd.Environ()
	}
	// Replace inherited adapter paths so nested runs cannot overwrite a parent.
	filtered := make([]string, 0, len(cmd.Env)+2)
	for _, entry := range cmd.Env {
		if !strings.HasPrefix(entry, "DATUM_RUN_DIR=") && !strings.HasPrefix(entry, "DATUM_RUN_REPORT=") {
			filtered = append(filtered, entry)
		}
	}
	cmd.Env = append(filtered, "DATUM_RUN_DIR="+dir, "DATUM_RUN_REPORT="+filepath.Join(dir, "producer.json"))
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = outPipe.writer, errPipe.writer, runWaitDelay
	drained := make(chan error, 2)
	go func() { _, err := io.Copy(stdout, outPipe.reader); drained <- err }()
	go func() { _, err := io.Copy(stderr, errPipe.reader); drained <- err }()
	processErr := cmd.Start()
	_ = outPipe.writer.Close()
	_ = errPipe.writer.Close()
	if processErr != nil {
		diagnostic := processErr.Error()
		if len(diagnostic) > runTailBytes {
			diagnostic = diagnostic[:runTailBytes]
		}
		envelope.Outcome = runKnown(model.ProcessOutcome{Kind: "spawn-failed", Diagnostic: &diagnostic})
	} else {
		processErr = cmd.Wait()
		if cmd.ProcessState != nil {
			if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				signal := status.Signal().String()
				envelope.Outcome = runKnown(model.ProcessOutcome{Kind: "signal", Signal: &signal})
			} else if code := cmd.ProcessState.ExitCode(); code >= 0 {
				envelope.Outcome = runKnown(model.ProcessOutcome{Kind: "exit", ExitCode: &code})
			}
		}
	}
	envelope.ObservedAt = runKnown(time.Now().UTC())
	streamErr := runDrain(drained, outPipe, errPipe)
	result.StdoutTail, result.StderrTail = stdout.tail, stderr.tail
	result.Envelope = envelope
	// Sync before capture as well as inside WriteIntake. If this fails, no seal
	// claims durable outputs and the already published intent remains available.
	if err := errors.Join(stdout.file.Sync(), stderr.file.Sync()); err != nil {
		return result, errors.Join(processErr, streamErr, err)
	}
	sealCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	seal, sealedEnvelope, captureErr := runSeal(sealCtx, project, request, envelope, dir, stdout.file, stderr.file, streamErr)
	result.SealPacket, result.Envelope = seal, sealedEnvelope
	return result, errors.Join(processErr, streamErr, captureErr)
}

func runIntent(project store.Project, r RunRequest) (model.InvocationEnvelope, error) {
	e := model.InvocationEnvelope{}
	if !filepath.IsAbs(project.Root) || r.Timeout < 0 {
		return e, fmt.Errorf("run: absolute project root and nonnegative timeout required")
	}
	if r.ExecutionSourceIdentity.Project != project.ID {
		return e, fmt.Errorf("run: execution identity must name this project")
	}
	if len(r.Argv) == 0 || model.Blank(r.Argv[0]) {
		return e, fmt.Errorf("run: executable argv array required")
	}
	for _, arg := range r.Argv {
		if strings.ContainsRune(arg, 0) {
			return e, fmt.Errorf("run: argv contains NUL")
		}
	}
	now := time.Now().UTC()
	id, err := model.NewID(now, rand.Reader)
	if err != nil {
		return e, err
	}
	e = model.InvocationEnvelope{
		InvocationID: id, AttemptID: r.AttemptID, InstrumentRef: r.InstrumentRef,
		CriterionRef: r.CriterionRef, ExecutionSourceIdentity: r.ExecutionSourceIdentity,
		Argv: r.Argv, InputRefs: r.InputRefs,
		ConfigRequested: r.ConfigRequested, ConditionsDeclared: r.ConditionsDeclared, StartedAt: now,
		ConfigEffective:    runUnknown[map[string]model.Availability[model.Scalar]](),
		ConditionsObserved: runUnknown[map[string]model.Availability[model.Scalar]](),
		Isolation:          runUnknown[model.Isolation](), ObservedAt: runUnknown[time.Time](),
		Outcome: runUnknown[model.ProcessOutcome](), OutputRefs: runUnknown[[]model.ArtifactRef](),
		Visual: runUnknown[model.VisualObservation](),
	}
	if e.CriterionRef.State == "" {
		e.CriterionRef = runUnknown[model.CriterionRef]()
	}
	if e.InputRefs == nil {
		e.InputRefs = []model.ArtifactRef{}
	}
	if e.ConfigRequested == nil {
		e.ConfigRequested = map[string]model.Scalar{}
	}
	if e.ConditionsDeclared == nil {
		e.ConditionsDeclared = map[string]model.Scalar{}
	}
	if err := model.ValidateInvocationConfig(e, r.Instrument); err != nil {
		return e, err
	}
	// Freeze intent here, before publishing or launching: the start and seal
	// must own every mutable value even if the caller reuses its request.
	e.Argv = append([]string(nil), e.Argv...)
	e.InputRefs = runCopyArtifacts(e.InputRefs)
	e.ConfigRequested = runCopyScalars(e.ConfigRequested)
	e.ConditionsDeclared = runCopyScalars(e.ConditionsDeclared)
	e.CriterionRef.Value = runCopyValue(e.CriterionRef.Value)
	e.ExecutionSourceIdentity.MachineID.Value = runCopyValue(e.ExecutionSourceIdentity.MachineID.Value)
	e.ExecutionSourceIdentity.Head.Value = runCopyValue(e.ExecutionSourceIdentity.Head.Value)
	e.ExecutionSourceIdentity.Dirty.Value = runCopyValue(e.ExecutionSourceIdentity.Dirty.Value)
	e.ExecutionSourceIdentity.SourceRefs = runCopyArtifacts(e.ExecutionSourceIdentity.SourceRefs)
	return e, nil
}

// runCopyValue is only for pointers to values with no mutable members.
func runCopyValue[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func runCopyScalars(values map[string]model.Scalar) map[string]model.Scalar {
	if values == nil {
		return nil
	}
	out := make(map[string]model.Scalar, len(values))
	for key, value := range values {
		value.Number = runCopyValue(value.Number)
		value.String = runCopyValue(value.String)
		value.Bool = runCopyValue(value.Bool)
		out[key] = value
	}
	return out
}

func runCopyArtifacts(refs []model.ArtifactRef) []model.ArtifactRef {
	if refs == nil {
		return nil
	}
	out := make([]model.ArtifactRef, len(refs))
	for i, ref := range refs {
		ref.Git = runCopyValue(ref.Git)
		if ref.Content != nil {
			content := *ref.Content
			if content.Locators != nil {
				content.Locators = append([]model.Locator{}, content.Locators...)
			}
			ref.Content = &content
		}
		out[i] = ref
	}
	return out
}

func runKnown[T any](value T) model.Availability[T] {
	return model.Availability[T]{State: model.Known, Value: &value}
}
func runUnknown[T any]() model.Availability[T] {
	return model.Availability[T]{State: model.Unknown, Reason: "not observed by this runner or reported by the producer"}
}

type runStream struct {
	file *os.File
	tail []byte
}

func runOpenStream(path string) (*runStream, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	return &runStream{file: f}, nil
}

func (s *runStream) Write(p []byte) (int, error) {
	n, err := s.file.Write(p)
	p = p[:n]
	if len(p) >= runTailBytes {
		s.tail = append(s.tail[:0], p[len(p)-runTailBytes:]...)
	} else {
		if excess := len(s.tail) + len(p) - runTailBytes; excess > 0 {
			copy(s.tail, s.tail[excess:])
			s.tail = s.tail[:len(s.tail)-excess]
		}
		s.tail = append(s.tail, p...)
	}
	return n, err
}

type runPipe struct{ reader, writer *os.File }

func runNewPipe() (runPipe, error) {
	r, w, err := os.Pipe()
	return runPipe{reader: r, writer: w}, err
}

func (p runPipe) close() {
	_ = p.reader.Close()
	_ = p.writer.Close()
}

// exec.Wait prioritizes an exit error over its own pipe-drain timeout. Owning
// the drain keeps an incomplete stream distinguishable even for a failing child.
func runDrain(done <-chan error, stdout, stderr runPipe) error {
	timer := time.NewTimer(runWaitDelay)
	defer timer.Stop()
	var result error
	for n := 0; n < 2; n++ {
		select {
		case err := <-done:
			result = errors.Join(result, err)
		case <-timer.C:
			_ = stdout.reader.Close()
			_ = stderr.reader.Close()
			for ; n < 2; n++ {
				result = errors.Join(result, <-done)
			}
			return errors.Join(result, exec.ErrWaitDelay)
		}
	}
	return result
}

// Every ancestor is checked because a symlink in a generated output path would
// turn a local capture into a write outside the project.
func runMakeDir(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	current := root
	for _, part := range append([]string{""}, strings.Split(rel, string(filepath.Separator))...) {
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("run: output ancestor is not a real directory")
		}
	}
	return nil
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
