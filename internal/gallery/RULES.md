# Gallery records

The ledger is the only gallery source. `gallery.add` pins external media by
SHA-256, byte length and locator; it records title, note, actor and optional
exact task references. Media is streamed for hashing and is never retained as
an intake blob or published artifact. Project-relative locators travel across
checkouts; external absolute locators are useful only where that path exists.

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
actor. Numeric witnesses cannot substitute for that approval. Existing acceptance
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
entry, preserving title, note and original added date separately from packet capture time and attributing the new record to the importer.
Admitted legacy ids prevent duplicates. A final `gallery.import` packet records
the list inventory's content hash and all imported entry ids; it cannot be admitted before
those entries. Retries read the list to identify its inventory; an admitted inventory is skipped.
The old list and media remain in place. Missing media must be restored to import
it; pending import packets should be admitted rather than recaptured.
