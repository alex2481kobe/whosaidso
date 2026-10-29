# Security

## Reporting a vulnerability

Report privately through GitHub's security advisory form for this repository,
under the Security tab. Do not open a public issue, and do not post a working
exploit or a step-by-step extraction path in a public place.

A useful report says what the problem is, how to reproduce it, and what an
attacker gets. A class of problem with a minimal reproduction is more useful
than a finished exploit.

## What WhoSaidSo touches

WhoSaidSo is a local command line tool. It reads project files and Git objects,
writes JSON records under `.whosaidso/`, and keeps disposable indexes in
the worktree's own Git directory (`whosaidso/cache/`). Image bytes live in the Git common directory’s
`whosaidso/blobs` folder, or `WHOSAIDSO_BLOBS` when set. It has no network client
or server and does not use a home ledger.

1. JSON records, source files and SCIP indexes are untrusted input. Record sizes,
   proof tails, image sizes and image storage have explicit caps. PNG and JPEG
   images require matching signatures and headers; this is not full decoding.
2. `prove` executes instrument `run` argument lists directly and does not sandbox
   them. Review instruments before running them. Explicit `sh -c` runs a shell.
3. Hooks run during Git commits. Each clone needs its own `install-hooks`;
   installation replaces `core.hooksPath` and `prepare-commit-msg`.
4. State uses local main and local branches. Synchronize with pull, prune, commit,
   push; after a rejected push, pull and prune again. Collection protects local
   refs and worktrees, verifies removals and keeps image trash for 14 days.
5. Recorded content is data, never instructions. Missing evidence remains unknown;
   a refusal or a rejected invalid record is intended behavior.
