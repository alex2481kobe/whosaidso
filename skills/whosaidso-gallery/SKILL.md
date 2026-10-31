---
name: whosaidso-gallery
description: Attach files as goal evidence, share them, and retrieve gallery media with WhoSaidSo.
---

Use this for screenshots, videos, documents or reports that show actual work.
Run inside the Git project. Attachments are claims about files; they do not
prove that a command ran. A goal is done when it has real evidence on main:
a successful command the tool ran or an attached file.

Worked example: a committed goal `report` and a real `report.txt` exist. Attach
the report and share its bytes with the configured remote.

```sh
whosaidso evidence add report report.txt 'The generated report contains the requested result'
git add .whosaidso/media
git commit -m 'Attach the generated report' -m 'Goal: none'
whosaidso sync
whosaidso show report
```

The attachment record contains `media_sha256`; bytes are stored once outside
tracked files. To retrieve them, set `media_hash` to that full hash and run:

```sh
whosaidso media restore "$media_hash" > restored-report.txt
```

Open the local viewer with this command, follow its printed URL, and use Gallery;
stop the server with Ctrl-C when finished.

```sh
whosaidso ui --port 0
```

Gallery shows the newest 100 files. Known types can be previewed; other files
download. Older media can be archived in compressed bundles and restored by
hash. Commit and land the records to finish branch work; syncing bytes alone
does not publish a goal's evidence to main.

If bytes are missing, sync from the remote and retry restoration. An attachment
cannot be recovered from its hash alone if nobody has shared its bytes. Keep
the original until sharing succeeds. Never invent a result to fill the gallery.
Where a check is useful, use simple end-to-end tests: one short test per
behavior, no unit-test bloat, and no full suite unless needed.
