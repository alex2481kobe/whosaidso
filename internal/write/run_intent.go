package write

// Synchronous intent validation, owned copies, and availability constructors live here.
// Process launch and producer-report interpretation do not.
// This file stays below 200 lines because freezing intent is one complete responsibility.

import (
	"crypto/rand"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func runIntent(project store.Project, r RunRequest) (model.InvocationEnvelope, error) {
	e := model.InvocationEnvelope{}
	if !filepath.IsAbs(project.Root) || !filepath.IsAbs(project.ExecRoot()) || r.Timeout < 0 {
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
		Outcome: runUnknown[model.ProcessOutcome](), Outputs: runUnknown[[]model.RunOutput](),
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

// runFreezeInstrument owns the instrument declaration the seal re-validates
// against. Without it, caller storage reuse after launch (say ConfigSurface)
// could invalidate a valid observation or validate an invalid one.
func runFreezeInstrument(s model.InstrumentSpec) model.InstrumentSpec {
	if s.ConfigSurface != nil {
		s.ConfigSurface = append([]string{}, s.ConfigSurface...)
	}
	if s.DangerousDefaults != nil {
		s.DangerousDefaults = append([]string{}, s.DangerousDefaults...)
	}
	s.ImplementationRef = runCopyArtifacts([]model.ArtifactRef{s.ImplementationRef})[0]
	if v := runCopyValue(s.Validation.Value); v != nil {
		v.Ref = runCopyArtifacts([]model.ArtifactRef{v.Ref})[0]
		s.Validation.Value = v
	}
	return s
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
