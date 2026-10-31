# Local retrieval vectors

The pinned file hashes and inference identity live in the parent module. Change the
identity whenever tokenization, source preparation, chunking, pooling or quantization
changes. Derived vectors are disposable; source payloads remain authoritative.

Inference uses the supplied WordPiece vocabulary and added tokens without inserting
postprocessor tokens. Canonical decomposition and character-category data use Unicode
13; lowercase conversion uses Rust. Weighted F32 rows are summed, normalized and
quantized to signed int8. Rows are decoded from the verified bytes as needed.

Definition input combines its qualified name with its documentation/source excerpt.
Source text is divided into bounded chunks. Vectors reference immutable payloads,
whose addresses include blob identity; worktree membership selects visibility.
Identical payloads share vectors across worktrees. Publication and membership changes
use the same transaction. Collection retains payloads referenced by any membership,
expires older inference identities with their stamps, and reuses SQLite free pages.
Retired memberships follow the retrieval store's lifecycle.

Queries stream int8 vectors and retain a bounded candidate heap, then fuse with lexical
ranks. This does not load the vector corpus into memory. Manifest memory and retained
SQLite capacity are separate from that bound.

`whosaidso model fetch` verifies the pinned files in the user cache outside Git.
`--from` accepts an HTTPS or local file base URL. It requires the system `curl` command.
`WHOSAIDSO_MODEL` overrides the read location. Missing or invalid files leave lexical
search available with a diagnostic. Optional integration checks use
`WHOSAIDSO_TEST_MODEL`; required tests need no model files.
