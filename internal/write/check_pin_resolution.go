// Ledger pin resolution isolates a failed batch to its pin. The check keeps
// unrelated measured classes; session lifecycle errors remain explicitly unknown.
package write

import (
	"context"

	"github.com/alex2481kobe/whosaidso/internal/evidence"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

func resolveLedgerPin(ctx context.Context, p store.Project, ref model.ArtifactRef) (resolveErr, closeErr error) {
	session := evidence.NewGitSession(ctx, p.Root)
	resolver := evidence.NewResolverAt(p.Root, p.ArtifactDir())
	resolver.Git = session.Run
	_, resolveErr = resolver.Resolve(ctx, ref)
	closeErr = session.Close()
	return
}
