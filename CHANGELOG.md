# Changelog

## 0.3.0

Fix rejected Overview, Tasks, Records and Activity requests. Viewer reads reuse
validated snapshots while checking every ledger byte. Gallery reads index their
own events and copy only the requested item. Visible thumbnails load first,
with faster cold PNG generation and private cache revalidation. Disposable
snapshot caches rebuild on upgrade.

Gallery loads more files on scroll. Cards and item views show a plain clickable
title and Reveal, Copy path, Download and Delete icons. Delete captures and
admits a retraction as the viewer actor, retaining the file and honoring project
admission policy. Downloads also support files without an inline preview.
Sidebar branding is removed; one theme icon toggles light/dark and remembers
the choice. File-manager actions and browser launch support macOS, Linux and
Windows. Linux reveal opens the containing folder.

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
