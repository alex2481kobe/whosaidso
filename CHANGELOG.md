# Changelog

## 0.3.0

- Glass viewer adds Activity with kind and text filters, exact Gallery links, and a capped `/api/activity` timeline from the shared activity reader. Goal rows, details and Overview show branch authors, ages and local worktrees. A dependency-free game engine sample script supplies reproducible screenshot fixtures.
- `start` claims its goal through a shared ref on `origin`, so two workers on any workstations never hold one goal; the loser is refused by name, and starting another goal or `none` releases the claim. `gate` refuses uncommitted records.
- Explicit `sync [<remote>]` shares verified media bytes and archive bundles through a separate Git ref, with union preservation and lease retries; check identifies locally missing media known to that ref.
- Fetched work branches contribute active and idle goal states, with local branches taking precedence; goals show authors, ages and local worktree paths, start warns about overlapping work, and log combines recent commits and evidence without repeating commits. Landing and proof eligibility still use local main.

- The engineering ledger uses phases, goals, instruments, proofs, asks and media. State derives from Git, local main and local branches; the cache remains disposable.
- `media <goal> <file> <title>` replaces the image command and record kind without compatibility aliases. `.whosaidso/media/` records retain titles, detected types, byte sizes and SHA-256 hashes.
- Signatures identify PNG, JPEG, GIF, WebP, MP4/MOV (`ftyp`), WebM (EBML) and PDF independently of extensions. Check rejects stored type, size or hash mismatches. Limits are 64 MiB per file and 4 GiB for the live blob store including trash.
- The local read-only `ui` viewer shows progress, goals, searchable records and the newest media. Its loopback server exchanges a one-use launch token for a session cookie; the project picker shares a machine registry with hook installation.
- Gallery shows the newest 100 media across the ledger by descending time and record hash: inline images, video players with controls and metadata preload without autoplay, and PDF links.
- Prune keeps all media evidence. Older and retired-goal records and bytes go into monthly plain ustar bundles in the common Git archive directory, configurable with `WHOSAIDSO_ARCHIVE`. Archives never expire or count toward the live quota. Standard `tar -tf` and `tar -xf` list and restore them.
- Archive publication fsyncs a temporary bundle before rename and live record removal; prune verification can restore removals after failure. Blob locks, local-ref protection and recoverable trash still apply.
- Proofs use direct instrument argument lists, refuse dirty trees or changed HEAD reflogs, and derive eligibility from instrument bytes at their commits. Unknown results remain unknown.
- Search and code queries index on demand, bound answer lines and retain only printed code answers plus counts. Start requires installed hooks; check enforces record and catalogue caps.
