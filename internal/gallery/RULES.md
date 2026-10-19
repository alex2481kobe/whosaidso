# Gallery records

The ledger is the only gallery source. `gallery.add` pins external media by
SHA-256, byte length and locator; it records title, note, actor and optional
exact task references. Media is streamed for hashing and is never retained as
an intake blob or published artifact. Project-relative locators travel across
checkouts; relative locators and their resolved symlink targets must stay inside
the checkout at capture, admission and preview. Relative media opens are rooted
in the checkout at open time, including every symlink and parent directory;
hashing and preview use that same open handle. Rooted opens refuse absolute
symlink targets, even when they name a path inside the checkout. External absolute locators are
useful only where that path exists.

Capture proposes a record. Admission publishes it under the project's normal
self-admission policy. `gallery.retract` hides an entry without changing its
media or history. Approval and rejection name the immutable entry and its hash,
and their identified actor must be their packet's recorded author. Rejection
requires a note. The latest admitted verdict controls acceptance.

`gallery.require` binds a task acceptance criterion to an entry and identified
approver. The task's accepter, when named, must author the requirement. Each
criterion identity can acquire one requirement; it follows that criterion
through amendments. Different content needs a new entry and criterion. A success
close needs an active entry whose latest verdict is an approval by the named
actor, who must be different from the entry author. An author may record a
verdict, including a rejection, but their approval cannot satisfy the task gate.
Numeric witnesses cannot substitute for that approval. Existing acceptance
and delivery witnesses are still required.

Replay uses admitted facts alone. Admission additionally hashes the local media
before adding, approving or closing a visual task, refusing changed or unavailable
bytes. Gallery list and the viewer report local availability separately from the
historical verdict. Later file edits do not rewrite an admitted close. The viewer
verifies the opened file before serving a preview and refuses changed content.
Viewer retractions use --actor or WHOSAIDSO_ACTOR and remain pending until
admitted, including under the strict self-admission policy.
Actor names and visual judgments are assertions, not authentication or automated
proof of what a person actually inspected.

Only import reads the retired per-machine list. It captures one add packet per
entry, preserving title, note and original added date separately from packet
capture time and attributing the new record to the importer.
Admitted legacy identities prevent duplicates across lists and list order. Each
identity hashes the file SHA-256, byte length, locator and original added date;
title, note and machine-local list identity do not distinguish an entry. A final `gallery.import` packet records
the list inventory's content hash and its ordered inventory of legacy-entry
content hashes and corresponding
imported entry ids. Admission and replay require exactly that inventory, with each
entry's import id bound to its inventory member. Empty completion
requires an explicit empty-source declaration and the empty inventory's hash;
nonempty sources cannot complete without every entry admitted. Retries read the
list to identify its inventory; an admitted inventory is skipped.
The old list and media remain in place. Missing media must be restored to import
it; pending import packets should be admitted rather than recaptured.
