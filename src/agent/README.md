# Agent hooks

`whosaidso install-agent claude` merges a command hook into `$HOME/.claude/settings.json`.
`whosaidso install-agent codex` merges one into `$HOME/.codex/hooks.json`.
Use `--uninstall` to remove only the owned entry. Restart the client after installation;
Codex hooks do not run until trusted in `/hooks`. Existing hooks and other settings remain.
The installer records the current executable path, so reinstall after moving the binary.
For a fixture, set `HOME` before installing. The installer does not change project settings.

Both clients support skills. Installation also writes five embedded skills to
`$HOME/.claude/skills/whosaidso-*/SKILL.md` or
`$HOME/.codex/skills/whosaidso-*/SKILL.md`; a checkout is not required. Reinstalling
identical files is a no-op. A different file at an owned skill path causes a
refusal before hooks change: move it aside and retry. Uninstall removes only
unchanged embedded skills and empty skill directories, preserving other files.
The success message points to the short “Using WhoSaidSo” section in this repository's
AGENTS.md; the installer never edits project instructions.

Both hooks consume `PreToolUse` JSON with `tool_name: "Bash"` and
`tool_input.command`. Rewrites return `hookSpecificOutput.updatedInput`, retaining
all other input fields. They do not run the pending command inside the hook.
Malformed input and infrastructure errors fail open. A deliberate refusal is a JSON
denial with a one-line next action. These are guardrails, not an isolation boundary.

Only a single literal shell command is rewritten:

| Shell command | Adapter |
| --- | --- |
| `cat FILE` or `cat -- FILE` | Complete raw bytes, without a page or symbol budget |
| `grep -n -F -- PATTERN FILE` | Literal, numbered, single-file search |
| `rg -n -F -- PATTERN FILE` | Literal, numbered, single-file search |

When the original utility is unavailable on PATH, the hook leaves the command
unchanged. Native comparison fixtures report unavailable utilities rather than
requiring a new runtime dependency.

The file must not be stdin. Search patterns must be nonempty and single-line.
Quotes and escaped literal characters are preserved by shell argument quoting.
Text searches preserve match/no-match exit codes and final-newline behavior.
Binary, invalid UTF-8, CRLF, missing files, and ripgrep configuration fall back to
the original utility with exactly its arguments. These compatibility adapters read
files directly: they do not claim indexed acceleration or index completeness.
Use `whosaidso grep` and `code` directly for indexed, budgeted answers.

Other recognized literal searches and whole-file reads receive a next action.
Compound commands, redirects, expansions, `sed`, `awk`, bounded reads and uncertain
forms pass through. The retrieval guard prefers passing an operation over refusing
something that might edit or compute. The independent test-run gate still applies.

Vendor references: [Claude Code hooks](https://code.claude.com/docs/en/hooks) and
[Codex hooks](https://learn.chatgpt.com/docs/hooks). Their documented contract is
not proof that a particular installed client has executed it. See the fixture
integration tests for handler behavior; live client verification also needs a
trusted hook and an authenticated, reachable model provider.

## Reasons and test scope

`WHOSAIDSO_ESCAPE='why the index cannot answer' COMMAND` permits a shell
search/read that cannot be translated. It does not disable the test gate.
`FULL_SUITE='why the full suite is needed' COMMAND` permits an unscoped test run.
An empty or whitespace-only reason does not count. Reasons are also accepted
from the hook process environment, or an initial literal `export`/assignment
in the same shell input. Per-command assignments do not leak to later commands.
Effective overrides append JSON lines to `$HOME/.whosaidso/agent-escapes.jsonl`;
the tool feedback names the override and never claims index completeness.
A log write failure is reported in feedback and fails open.

The same policy applies to both clients:

| Runner | Unscoped examples | Scoped examples |
| --- | --- | --- |
| Cargo | `cargo test`, `cargo +stable t`, package/features/jobs only, `--skip` only | named filter, `--test NAME`, `--bin NAME`, `--example NAME`, `--bench NAME`, `--lib`, `--doc` |
| Go | `go test`, `go test ./...` | named package, nontrivial `-run` pattern |
| npm | `npm test`, `npm run test`, aliases `t`/`tst`, runner options alone | workspace or positional test target |
| pytest | `pytest`, `python3 -m pytest`, root or `tests/` only | named path/node, `-k` expression, `-m` marker |

Help, collection/listing, and build-only forms do not run tests and pass.
Npm scripts decide what their forwarded target means; this gate does not inspect
package scripts. Go regular expressions and pytest expressions can still select
all tests. This is a scope guardrail, not proof that a selector is narrow.

The lexer recognizes simple command chains, redirections, literal environment
assignments, common executable wrappers, and literal `sh`/`bash`/`zsh -c` bodies.
It is not a shell interpreter. Scripts, shell aliases/functions, `eval`, `xargs`,
`rustup run`, heredocs, quoted command substitutions, dynamically constructed
commands and runner plugins are coverage gaps. Unrecognized runner options
conservatively require an override instead of treating their values as selectors.
Unknown syntax is not proof that no full run occurred. A broken payload or
handler never blocks unrelated work. Use explicit reasons for wrappers the
hook cannot inspect, and verify enforcement on the actual host before relying
on it. Test-run denials contain one line explaining how to scope or override.

## Native file tools

In a Git worktree containing `.whosaidso`, native `Grep`/`Glob` calls with
supported arguments receive one-line command suggestions through a normal
PreToolUse denial. Grep suggestions preserve case, pattern, path/glob, supported
output mode, context and page arguments; multiline, PCRE-only constructs,
unknown flags, count mode and untranslatable glob syntax pass through.
No native tool input or output is rewritten. Missing files, malformed input,
filesystem failures and repositories without `.whosaidso` pass through.

A whole `Read` of a supported source language with more than 200 lines is
given one hint suggesting `code outline` and `code show`.
Any offset or limit passes; short, binary, non-code, image and PDF reads pass.
Explicit search paths outside the visible Git file set pass through.

Each native kind (`Read`, `Grep`, `Glob`) can refuse only once per `session_id`;
aliases and other repositories in that session share the limit. Later calls pass.
A missing session id passes without a hint. Decisions and reasons append to
`$HOME/.whosaidso/agent-hints.jsonl`; disposable session markers live under
`$HOME/.whosaidso/cache/agent-hints`, outside project records.

The same payload handlers recognize `read_file`, `grep_files` and `glob_files`
when clients expose equivalent local tools with these argument shapes. Their
availability and exact live payloads must be verified on each client; aliases
in fixture tests do not establish that an installed client exposes them.
