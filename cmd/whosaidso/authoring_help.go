package main

// Help for flag-driven authoring describes the template translation and the
// judgments that still need explicit input. Flag listings come from the registry.

const authoringHelp = `Captures through template and capture, printing the packet id and created
record ids. --dry-run runs check admission without writing; exit 0 would-admit,
1 would-refuse. --admit uses capture's second act, including self-admission
policy and exit 4 when capture succeeds but admission refuses; it needs
--reason and cannot be combined with --dry-run. Actor defaults to
WHOSAIDSO_ACTOR. No outcome, actor, scope judgment or witness is inferred.
`

const taskNewHelp = authoringHelp + `Required: --subject, --intent, --for, --criterion, --non-goal,
--applies-when and --limitations. Scope source paths and context, task context,
constraints and prerequisites start empty. --after binds task-success at the
current revision, with waiver forbidden. --source cites an admitted source;
--pin pins provenance bytes as template does. Criteria get fresh identities.
For accepter, progress, structured context, other prerequisite policies or
scope selectors, use whosaidso template task.create.
Example:
  whosaidso task new --subject "Export report" --intent "Export results" --for worker --criterion "CSV opens" --non-goal "PDF export" --applies-when "Report export" --limitations "CSV only"
`

const taskAmendHelp = authoringHelp + `Copies the current spec with template's --from binding. Only flags given
change it; repeated --criterion, --non-goal and --after replace their lists.
Replacement criteria receive fresh identities; omitted criteria retain theirs.
Provenance describes this amendment, using --source or --pin, otherwise empty.
For fields without flags, including clearing prerequisites, use
whosaidso template task.amend with its --from flag.
Example:
  whosaidso task amend TASK --for reviewer
`

const taskCloseHelp = authoringHelp + `--outcome and --reason are required. --witness is a template pin, one per
acceptance criterion in the current task's order; every pin also becomes a
delivery witness. The gate decides whether the witnesses permit this outcome.
--authority binds the admitted source's speaker and whole bytes, scoped to the
current task; --applies-when and --limitations must author that scope.
The ledger's task.close has no reason field: --reason is used for the review
only with --admit; capture alone does not store it. To record a separate reason,
source add its bytes first. For criterion-specific selectors, different delivery
witnesses or narrower authority selectors, use whosaidso template task.close.
Example:
  whosaidso task close TASK --outcome success --witness report.csv --reason "Reviewed the exported CSV" --admit
`

const sourceAddHelp = authoringHelp + `Requires FILE, --speaker and --order (zero is valid). Pins the whole original
file as template's source_ref, computing its digest and length from real bytes.
--about binds admitted tasks at their current revisions. For referents of
other record kinds, use whosaidso template source.intake.
Example:
  whosaidso source add request.txt --speaker requester --order 0 --about TASK
`
