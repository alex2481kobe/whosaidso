# Security

## Reporting a vulnerability

Report privately through GitHub's security advisory form for this repository,
under the Security tab. Do not open a public issue, and do not post a working
exploit or a step-by-step extraction path in a public place.

A useful report says what the problem is, how to reproduce it, and what an
attacker gets. A class of problem with a minimal reproduction is more useful
than a finished exploit.

## What WhoSaidSo touches

WhoSaidSo is a local command line tool. It reads and writes files under a project
directory and under `~/.whosaidso`, runs the commands you ask it to measure, and
shells out to git for provenance. It has no network client and no server.

That shapes the interesting attack surface:

1. Files it parses, which include `whosaidso.toml`, intake packets and ledger
   bundles. Treat any of these from an untrusted source as hostile input.
2. Commands it wraps. WhoSaidSo records what a command did, and does not sandbox
   it. A measurement you run is a command you ran.
3. Content it records. Text captured from a document, a transcript or another
   project is data, never instructions.

## What is not a vulnerability

WhoSaidSo will refuse work rather than guess. A refusal, an explicit UNKNOWN, or a
record it declines to admit is the intended behaviour, not a denial of service.
