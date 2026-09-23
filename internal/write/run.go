package write

// Invocation lifecycle, process streams, and when output staging is retired live here.
// Intent copying, producer-report interpretation and where staging lives do not.

import (
	"context"
	"errors"
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
// Dir defaults to the invoking checkout (Project.ExecRoot), not the home. Env defaults to the inherited environment.
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
// the durable record has no acknowledged terminal observation. ArtifactDir is
// the run's kept staging directory: empty once the seal captured every output
// and the staging was retired, and set whenever capture did not secure them.
type RunResult struct {
	Envelope    model.InvocationEnvelope
	StartPacket model.PacketRef
	SealPacket  model.PacketRef
	ArtifactDir string
	StdoutTail  []byte
	StderrTail  []byte
}

// Run publishes intent before even constructing the command. Once a child has
// been observed, cancellation must not erase that observation, so final capture
// gets a separate bounded context. A killed observer cannot execute this seal.
func Run(ctx context.Context, project store.Project, request RunRequest) (RunResult, error) {
	var result RunResult
	request.Instrument = runFreezeInstrument(request.Instrument)
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
	// Outputs are staged outside the committed store; their logical names stay
	// in the run's own directory there (runSeal), and admission publishes the
	// captured bytes once, by digest.
	dir, err := store.MakeRunStaging(project, envelope.InvocationID)
	if err != nil {
		return result, err
	}
	result.ArtifactDir = dir
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
		cmd.Dir = project.ExecRoot()
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
	// Retire staging only once an acknowledged seal holds every output's bytes
	// in its own durable blobs. An incomplete stream, a refused report or a
	// failed capture leaves outputs the seal does not hold, so they stay.
	if captureErr == nil && streamErr == nil && seal.CommandID != "" {
		if os.RemoveAll(dir) == nil {
			result.ArtifactDir = ""
		}
	}
	return result, errors.Join(processErr, streamErr, captureErr)
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
