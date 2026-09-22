//go:build darwin || linux

package store

// The admission lock's confinement and TransactAdmission's retry contract.
// Publication ordering and crash recovery are tested in publish_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"datum/internal/model"
)

func TestAdmissionLockNeverFollowsASymlink(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1) // control: an ordinary lock file admits
	lock := filepath.Join(p.Ledger, lockName)
	inside := filepath.Join(p.Root, "inside-target")
	if err := os.WriteFile(inside, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"dangling link out of the root": filepath.Join(t.TempDir(), "escaped-lock"),
		"link to a real file":           inside,
		"a fifo, not a regular file":    "",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.Remove(lock); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if target == "" {
				if err := syscall.Mkfifo(lock, 0o644); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(target, lock); err != nil {
				t.Fatal(err)
			}
			_, err := Transact(context.Background(), p, admissionID(2), digestFor(2), proposeFor(2))
			requireFault(t, err, "ledger-corrupt")
			if target != inside && target != "" {
				if _, err := os.Lstat(target); !os.IsNotExist(err) {
					t.Fatalf("opening the lock created %s: %v", target, err)
				}
			}
			readControl(t, p, 1)
			if err := os.Remove(lock); err != nil {
				t.Fatal(err)
			}
		})
	}
	admitControl(t, p, 2) // the lock is recreated as a regular file
}

func retryDigestOf(d model.Digest) func(model.Bundle) (model.Digest, error) {
	return func(model.Bundle) (model.Digest, error) { return d, nil }
}

func TestTransactAdmissionAnswersRetriesFromThePublishedBundle(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	ctx := context.Background()
	first, err := TransactAdmission(ctx, p, Admission{ID: admissionID(2), RetryDigest: retryDigestOf(digestFor(99)),
		Propose: func(prefix []model.Bundle) (model.Digest, model.Bundle, error) {
			b, err := proposeFor(2)(prefix)
			return digestFor(2), b, err
		}})
	if err != nil || first.RequestDigest != digestFor(2) {
		t.Fatalf("control: first admission carries Propose's digest: %+v, %v", first.RequestDigest, err)
	}
	var seen model.Bundle
	retry, err := TransactAdmission(ctx, p, Admission{ID: admissionID(2),
		RetryDigest: func(published model.Bundle) (model.Digest, error) { seen = published; return digestFor(2), nil },
		Propose: func([]model.Bundle) (model.Digest, model.Bundle, error) {
			t.Fatal("Propose ran on a retry of a published admission")
			return "", model.Bundle{}, nil
		}})
	if err != nil || retry.Sequence != 2 || seen.CommandID != admissionID(2) {
		t.Fatalf("identical retry: %+v, %v (RetryDigest saw %q)", retry, err, seen.CommandID)
	}
	_, err = TransactAdmission(ctx, p, Admission{ID: admissionID(2), RetryDigest: retryDigestOf(digestFor(99)),
		Propose: func([]model.Bundle) (model.Digest, model.Bundle, error) { return "", model.Bundle{}, nil }})
	requireFault(t, err, "conflict")
	_, err = TransactAdmission(ctx, p, Admission{ID: admissionID(3), RetryDigest: retryDigestOf(digestFor(3)),
		Propose: func(prefix []model.Bundle) (model.Digest, model.Bundle, error) {
			b, err := proposeFor(3)(prefix)
			return "not-a-digest", b, err
		}})
	requireFault(t, err, "invalid-field")
	readControl(t, p, 2)
}
