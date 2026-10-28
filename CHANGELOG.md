# Changelog

## 0.3.0

- The engineering ledger uses phases, goals, instruments, proofs, asks and media. State derives from Git, local main and local branches; the cache remains disposable.
- `media <goal> <file> <title>` replaces the image command and record kind without compatibility aliases. `.whosaidso/media/` records retain titles, detected types, byte sizes and SHA-256 hashes.
- Signatures identify PNG, JPEG, GIF, WebP, MP4/MOV (`ftyp`), WebM (EBML) and PDF independently of extensions. Check rejects stored type, size or hash mismatches. Limits are 64 MiB per file and 4 GiB for the live blob store including trash.
- View shows the newest 100 media across the ledger by descending time and record hash: inline images, video players with controls and metadata preload without autoplay, and PDF links.
- Prune keeps all media evidence. Older and retired-goal records and bytes go into monthly plain ustar bundles in the common Git archive directory, configurable with `WHOSAIDSO_ARCHIVE`. Archives never expire or count toward the live quota. Standard `tar -tf` and `tar -xf` list and restore them.
- Archive publication fsyncs a temporary bundle before rename and live record removal; prune verification can restore removals after failure. Blob locks, local-ref protection and recoverable trash still apply.
- Default invariant tests run every recorded seed for 200 steps; `WHOSAIDSO_LONG=1` runs all seeds for 2,000 steps before release. Lean keeps its retention assertions with fewer publications, and its invalid UTF-8 fixture writes bytes portably. CI retains `cargo test --locked`.
- Proofs use direct instrument argument lists, refuse dirty trees or changed HEAD reflogs, and derive eligibility from instrument bytes at their commits. Unknown results remain unknown.
- Search and code queries index on demand, bound answer lines and retain only printed code answers plus counts. Start requires installed hooks; check enforces record and catalogue caps.

## 0.2.0

Gallery lists are paged and searched on the server. Kind sorting remains
available. Title sorting compares lowercased Unicode text in byte order rather
than the browser's locale-dependent order, so the same ledger has the same
order on every platform. Equal titles retain their ledger order.

Image cards use bounded PNG/JPEG/GIF thumbnails; originals load in the inspector.
Preview failures have a per-card retry action. The inspector supports previous
and next within the current page, with arrow keys and horizontal touch swipes.
Save original and Copy path provide the same file actions on every platform.

The project switcher lists homes before reading ledger status. Records and
Overview start with 24 previews; work status totals load on request. Activity
starts with 24 admission-batch summaries, with full-batch search on demand.
Complete record detail and CLI view JSON remain available.

Gallery polling reuses observations while file identity, size and modification
time agree, stops hashing on cancellation, and bounds hashing per page. Original
and thumbnail requests verify source bytes independently. Missing partial-clone
Git blobs remain UNKNOWN without discarding unrelated ledger-check results.

New and changed commands:

- `packets` lists bounded headers of verified packets (local intake and packets
  named by the ledger); `packets PACKET_ID` prints one packet's payload.
- `todo --intake-details` expands verified packet summaries in text output;
  `--json` keeps the complete packets.
- Bare `help` prints the keyword index only; `help all` prints the full guide.
- `check ledger --against-checkout PATH` compares problem classes between a base
  checkout and the current one. `check` JSON adds `classes`, `git_pin_locations`
  and `check_error`; `clean` is false whenever `check_error` is set. The changes
  are additive.
- `gallery import` records files it skipped as missing and pins it reused in the
  import seal. An import with no skips hashes exactly as before.

Upgrading: a v0.1.x binary refuses a ledger containing a `gallery import` record
with the new `skipped` or `reused` fields, with a generic decode error. Once a
ledger holds such a record, set `requires = 'v0.2.0'` so binaries that read `requires` refuse it
by naming the needed version instead.
