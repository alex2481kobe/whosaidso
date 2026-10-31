use super::*;

fn note_text(title: &str, body: &str) -> String {
    format!("---\n{{\"type\":\"decision\",\"status\":\"open\",\"title\":\"{title}\"}}\n---\n{body}")
}
// Replaces a note through the real command, reading the revision the way a user does.
fn put_note(m: &Machine, slug: &str, body: &str) {
    let shown = m.ok(&["note", "show", slug]);
    let revision = shown
        .lines()
        .next()
        .unwrap()
        .trim_start_matches("revision=")
        .to_owned();
    m.write(".git/note-input.md", note_text("Storage choice", body));
    m.ok(&["note", "put", slug, &revision, ".git/note-input.md"]);
}
fn note_revision(m: &Machine, slug: &str) -> String {
    m.ok(&["note", "show", slug])
        .lines()
        .next()
        .unwrap()
        .trim_start_matches("revision=")
        .to_owned()
}

#[test]
fn both_machines_edit_one_decision_and_neither_edit_is_lost() {
    const ROUNDS: usize = 8;
    let world = World::new();
    let a = world.first_machine("alice");
    for i in 0..ROUNDS {
        a.write(
            &format!(".whosaidso/notes/topic{i}.md"),
            note_text("Storage choice", "first line\nmiddle\nlast line\n"),
        );
    }
    seed(&a, &[("base", goal_json(None, 1, None))]);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    for i in 0..ROUNDS {
        let slug = format!("topic{i}");
        a.branch(&format!("alice-{slug}"));
        b.branch(&format!("bob-{slug}"));
        put_note(&a, &slug, "first line ALICE\nmiddle\nlast line\n");
        put_note(&b, &slug, "first line\nmiddle\nlast line BOB\n");
        a.commit_all("alice edits the decision", "none");
        b.commit_all("bob edits the decision", "none");
        let outs = race_spread(&[(&a, &["land"]), (&b, &["land"])], i as u64 + 1, 700);
        let winners: Vec<usize> = (0..2).filter(|n| outs[*n].status.success()).collect();
        assert_eq!(
            winners.len(),
            1,
            "round {i}: two edits of one note cannot both land silently: {outs:?}"
        );
        let (win, lose) = (winners[0], 1 - winners[0]);
        let l = if lose == 0 { &a } else { &b };
        let said = text(&outs[lose]);
        assert!(
            said.contains(&format!("note reconcile {slug}")),
            "round {i}: the refusal does not name the command that resolves it: {said:?}"
        );
        // Follow the instruction: bring in the destination, review, reconcile against the revision it names.
        l.git(&["fetch", "-q", "origin", "+main:refs/remotes/origin/main"]);
        l.git(&["merge", "-q", "--no-edit", "origin/main"]);
        let destination = world.origin_git(&["show", &format!("main:.whosaidso/notes/{slug}.md")]);
        let dest_rev = hash(destination.as_bytes());
        assert!(
            said.contains(&dest_rev),
            "round {i}: the refusal names a destination revision that is not main's note: {said:?}"
        );
        let mine = note_revision(l, &slug);
        l.ok(&["note", "reconcile", &slug, &mine, &dest_rev]);
        l.commit_all("reconcile the decision", "none");
        l.ok(&["land"]);
        let merged = world.origin_git(&["show", &format!("main:.whosaidso/notes/{slug}.md")]);
        assert!(
            merged.contains("first line ALICE") && merged.contains("last line BOB"),
            "round {i}: a reconciled landing dropped an edit: {merged}"
        );
        let _ = win;
    }
    for m in [&a, &b] {
        m.refresh();
        m.git(&["switch", "-q", "-C", "audit", "origin/main"]);
        m.ok(&["check"]);
        m.ok(&["gate", "main"]);
    }
    assert_tips_tested(&world, &a);
}

#[test]
fn edits_to_different_notes_need_no_reconciliation() {
    let world = World::new();
    let a = world.first_machine("alice");
    a.write(".whosaidso/notes/one.md", note_text("One", "x\n"));
    a.write(".whosaidso/notes/two.md", note_text("Two", "y\n"));
    seed(&a, &[("base", goal_json(None, 1, None))]);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    a.branch("alice-one");
    b.branch("bob-two");
    put_note(&a, "one", "x changed\n");
    put_note(&b, "two", "y changed\n");
    a.commit_all("edit one", "none");
    b.commit_all("edit two", "none");
    for o in race(&[(&a, &["land"]), (&b, &["land"])]) {
        assert!(o.status.success(), "{}", text(&o));
    }
    a.refresh();
    assert!(
        world
            .origin_git(&["show", "main:.whosaidso/notes/one.md"])
            .contains("x changed")
    );
    assert!(
        world
            .origin_git(&["show", "main:.whosaidso/notes/two.md"])
            .contains("y changed")
    );
}

#[test]
fn both_machines_create_the_same_note_slug_and_the_second_does_not_overwrite_the_first() {
    let world = World::new();
    let a = world.first_machine("alice");
    seed(&a, &[("base", goal_json(None, 1, None))]);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    a.branch("alice-note");
    b.branch("bob-note");
    for (m, body) in [(&a, "alice's reasoning\n"), (&b, "bob's reasoning\n")] {
        m.write(".git/note-input.md", note_text("Same slug", body));
        m.ok(&["note", "put", "shared", "new", ".git/note-input.md"]);
        m.commit_all("new note", "none");
    }
    a.ok(&["land"]);
    let out = b.ws(&["land"]);
    let said = text(&out);
    assert_eq!(
        out.status.code(),
        Some(1),
        "bob's different note with the same slug must not land silently: {said}"
    );
    assert!(
        world
            .origin_git(&["show", "main:.whosaidso/notes/shared.md"])
            .contains("alice's reasoning")
    );
    assert!(
        said.contains("shared"),
        "bob was not told which note clashes (the tool printed {said:?})"
    );
}

#[test]
fn media_attached_on_both_machines_survives_concurrent_sync_and_landing() {
    const ROUNDS: usize = 12;
    let world = World::new();
    let a = world.first_machine("alice");
    let mut goals: Vec<(String, Value)> = (0..ROUNDS)
        .map(|i| (format!("e{i}"), goal_json(Some("mm"), i as i64 + 1, None)))
        .collect();
    goals.push(("mm".into(), goal_json(None, 1, None)));
    let refs: Vec<(&str, Value)> = goals.iter().map(|(i, v)| (i.as_str(), v.clone())).collect();
    seed(&a, &refs);
    let b = world.clone_machine("bob");
    let c = world.clone_machine("carol");
    b.ok(&["install-hooks"]);
    c.ok(&["install-hooks"]);
    let machines = [&a, &b, &c];
    for m in machines {
        m.git(&["switch", "-q", "-C", "evidence", "origin/main"]);
    }
    let mut expected: Vec<(String, Vec<u8>)> = Vec::new();
    for i in 0..ROUNDS {
        for (n, m) in machines.iter().enumerate() {
            for k in 0..3usize {
                // The last file of every round is byte-identical on all machines but titled differently.
                let (tag, size) = if k == 2 {
                    (200, 70_000)
                } else {
                    (
                        (n * 3 + k) as u8 + (i as u8) * 9,
                        60_000 + 1_000 * (i + n + k),
                    )
                };
                let bytes = image(tag.wrapping_add(i as u8), size);
                let file = m.home.join(format!("input-{i}-{k}.png"));
                fs::write(&file, &bytes).unwrap();
                m.ok(&[
                    "media",
                    &format!("e{i}"),
                    file.to_str().unwrap(),
                    &format!("{} figure {i}-{k}", m.name),
                ]);
                if !expected.iter().any(|(sha, _)| *sha == hash(&bytes)) {
                    expected.push((hash(&bytes), bytes));
                }
            }
        }
        let outs = race_spread(
            &[(&a, &["sync"]), (&b, &["sync"]), (&c, &["sync"])],
            i as u64 + 1,
            150,
        );
        for (o, who) in outs.iter().zip(["alice", "bob", "carol"]) {
            assert!(
                o.status.success(),
                "round {i}: {who} sync failed: {}",
                text(o)
            );
        }
    }
    // One more sync each: every machine has every byte, and the remote holds the union.
    for m in machines {
        m.ok(&["sync"]);
    }
    let listing = world.origin_git(&["ls-tree", "-r", "--name-only", "refs/whosaidso/blobs"]);
    for (sha, bytes) in &expected {
        assert!(
            listing.contains(sha),
            "media {sha} never reached the remote"
        );
        for m in machines {
            let got = fs::read(m.repo.join(format!(".git/whosaidso/blobs/{sha}.png")))
                .unwrap_or_else(|e| panic!("{} lost the bytes of {sha}: {e}", m.name));
            assert_eq!(&got, bytes, "{} holds different bytes under {sha}", m.name);
        }
    }
    // Land every machine's records at once; the records and bytes must agree afterwards.
    for m in machines {
        m.commit_all("attach evidence", "none");
    }
    let outs = race_spread(&[(&a, &["land"]), (&b, &["land"]), (&c, &["land"])], 7, 400);
    let landed = outs.iter().filter(|o| o.status.success()).count();
    // A machine that lost the race must be able to finish by landing again; nothing may be dropped.
    for (o, m) in outs.iter().zip(machines) {
        if !o.status.success() {
            eprintln!("{} first landing refused: {}", m.name, text(o));
            for _ in 0..3 {
                if m.ws(&["land"]).status.success() {
                    break;
                }
            }
        }
    }
    eprintln!("evidence landing: {landed} of 3 landed on the first attempt");
    for m in machines {
        m.refresh();
        m.git(&["switch", "-q", "-C", "audit", "origin/main"]);
        m.ok(&["sync"]);
        let said = m.ok(&["check"]);
        assert!(
            !said.contains("warning"),
            "{} still reports problems after sync: {said}",
            m.name
        );
    }
    let records = world.origin_git(&["ls-tree", "-r", "--name-only", "main", ".whosaidso/media"]);
    assert_eq!(
        records.lines().count(),
        ROUNDS * 9,
        "media records on main: every attachment from every machine must be there"
    );
}

fn proof_files(world: &World) -> Vec<String> {
    world
        .origin_git(&["ls-tree", "-r", "--name-only", "main", ".whosaidso/proofs"])
        .lines()
        .map(str::to_owned)
        .collect()
}

#[test]
fn both_machines_prune_the_same_main_at_once_and_land_without_losing_a_verdict() {
    const ROUNDS: usize = 5;
    let world = World::new();
    let a = world.first_machine("alice");
    seed(
        &a,
        &[
            ("pm", goal_json(None, 1, None)),
            ("c1", goal_json(Some("pm"), 1, Some("c1.txt"))),
            ("c2", goal_json(Some("pm"), 2, Some("c2.txt"))),
        ],
    );
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    a.branch("alice-c1");
    a.work(&[("c1.txt", "1"), ("c2.txt", "2")], "c1");
    a.commit_all("second goal", "c2");
    a.ok(&["land"]);
    for round in 0..ROUNDS {
        // Superseded proofs pile up on main, landed by alice.
        a.branch("proofs");
        for goal in ["c1", "c2"] {
            for _ in 0..2 {
                a.ok(&["prove", goal, "--reason", "same behavior, more proofs"]);
                a.commit_all("record proof", "none");
            }
        }
        a.ok(&["land"]);
        for m in [&a, &b] {
            m.refresh();
            m.git(&["switch", "-q", "main"]);
        }
        let before = a.ok(&["goals"]);
        let proofs_before = proof_files(&world).len();
        let outs = race_spread(&[(&a, &["prune"]), (&b, &["prune"])], round as u64 + 1, 60);
        for (o, who) in outs.iter().zip(["alice", "bob"]) {
            assert!(
                o.status.success(),
                "round {round}: {who} prune failed: {}",
                text(o)
            );
        }
        for m in [&a, &b] {
            assert_eq!(
                m.ok(&["goals"]),
                before,
                "round {round}: {} changed a verdict by pruning",
                m.name
            );
            m.commit_all("prune", "none");
        }
        let outs = race_spread(&[(&a, &["land"]), (&b, &["land"])], round as u64 + 3, 400);
        for (o, who) in outs.iter().zip(["alice", "bob"]) {
            assert!(
                o.status.success(),
                "round {round}: {who} could not land an identical prune: {}",
                text(o)
            );
        }
        let proofs_after = proof_files(&world).len();
        assert!(
            proofs_after <= proofs_before,
            "round {round}: prune left more proofs than before"
        );
        a.refresh();
        a.git(&["switch", "-q", "-C", "audit", "origin/main"]);
        assert_eq!(
            a.ok(&["goals"]),
            before,
            "round {round}: a verdict changed after the two prunes landed"
        );
        a.ok(&["check"]);
        for g in ["c1", "c2"] {
            assert!(
                a.ok(&["show", g]).contains("ran"),
                "round {round}: {g} lost its proof"
            );
        }
    }
    b.refresh();
    b.git(&["switch", "-q", "-C", "audit", "origin/main"]);
    b.ok(&["check"]);
    assert_tips_tested(&world, &a);
}

#[test]
fn evidence_attached_before_another_machine_folded_the_goal_is_kept_or_refused_with_a_way_forward()
{
    let world = World::new();
    let a = world.first_machine("alice");
    seed(
        &a,
        &[
            ("f", goal_json(None, 1, Some("f.txt"))),
            ("other", goal_json(None, 2, None)),
        ],
    );
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    a.branch("alice-f");
    b.branch("bob-late");
    a.ok(&["start", "f"]);
    // Bob, still on the old main, attaches a figure to f and commits it.
    let file = b.home.join("late.png");
    fs::write(&file, image(9, 5000)).unwrap();
    b.ok(&["media", "f", file.to_str().unwrap(), "late figure"]);
    b.commit_all("late figure for f", "none");
    // Alice finishes f; landing folds the finished goal into a bundle.
    a.work(&[("f.txt", "alice")], "f");
    a.ok(&["land"]);
    assert!(
        world
            .origin_git(&["ls-tree", "-r", "--name-only", "main"])
            .contains(".whosaidso/done/f.jsonl"),
        "control: f was folded by alice's landing"
    );
    let out = b.ws(&["land"]);
    let said = text(&out);
    eprintln!(
        "late evidence on a goal folded elsewhere: land {} -> {said:?}",
        if out.status.success() {
            "accepted"
        } else {
            "refused"
        }
    );
    if out.status.success() {
        // Accepted: the evidence must be part of main and the ledger must still be valid.
        b.refresh();
        b.git(&["switch", "-q", "-C", "audit", "origin/main"]);
        b.ok(&["check"]);
        assert!(
            b.ok(&["show", "f"]).contains("late figure"),
            "bob's evidence was accepted and then vanished"
        );
    } else {
        assert!(
            said.contains("reopen f"),
            "bob's landing was refused without a way forward: {said:?}"
        );
        // Follow the instruction and land the evidence.
        b.refresh();
        let tip = world.origin_git(&["rev-parse", "main"]).trim().to_owned();
        let bundle = world.origin_git(&["show", "main:.whosaidso/done/f.jsonl"]);
        b.git(&["merge", "-q", "--no-edit", &tip]);
        b.ok(&["reopen", "f", &hash(bundle.as_bytes())]);
        b.commit_all("reopen f for the late figure", "none");
        b.ok(&["land"]);
        b.refresh();
        b.git(&["switch", "-q", "-C", "audit", "origin/main"]);
        b.ok(&["check"]);
        assert!(
            b.ok(&["show", "f"]).contains("late figure"),
            "reopened evidence is not on f"
        );
    }
    // Whichever way it went, alice's machine agrees with the remote afterwards.
    a.refresh();
    a.git(&["switch", "-q", "-C", "audit", "origin/main"]);
    a.ok(&["check"]);
    assert_eq!(a.ok(&["goals"]), b.ok(&["goals"]));
}

#[test]
fn both_machines_prove_the_same_goal_at_once_and_both_proofs_land() {
    const ROUNDS: usize = 5;
    let world = World::new();
    let a = world.first_machine("alice");
    let mut goals = vec![("pm".to_owned(), goal_json(None, 1, None))];
    for i in 0..ROUNDS {
        // The feature already exists on main, so the check passes before any proof is recorded.
        a.write(&format!("q{i}.txt"), "present");
        goals.push((
            format!("q{i}"),
            goal_json(Some("pm"), i as i64 + 1, Some(&format!("q{i}.txt"))),
        ));
    }
    let refs: Vec<(&str, Value)> = goals.iter().map(|(i, v)| (i.as_str(), v.clone())).collect();
    seed(&a, &refs);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    for i in 0..ROUNDS {
        let goal = format!("q{i}");
        a.branch(&format!("alice-{goal}"));
        b.branch(&format!("bob-{goal}"));
        let outs = race_spread(
            &[
                (&a, &["prove", &goal, "--reason", "already present"]),
                (&b, &["prove", &goal, "--reason", "already present"]),
            ],
            i as u64 + 1,
            80,
        );
        for (o, who) in outs.iter().zip(["alice", "bob"]) {
            assert!(
                o.status.success(),
                "round {i}: {who} prove failed: {}",
                text(o)
            );
        }
        a.commit_all("alice's proof", "none");
        b.commit_all("bob's proof", "none");
        let outs = race_spread(&[(&a, &["land"]), (&b, &["land"])], i as u64 + 5, 500);
        for (o, who) in outs.iter().zip(["alice", "bob"]) {
            assert!(
                o.status.success(),
                "round {i}: {who} could not land a proof: {}",
                text(o)
            );
        }
        let proofs =
            world.origin_git(&["ls-tree", "-r", "--name-only", "main", ".whosaidso/proofs"]);
        assert!(
            proofs.lines().count() >= 2,
            "round {i}: a concurrent proof was lost: {proofs}"
        );
    }
    a.refresh();
    a.git(&["switch", "-q", "-C", "audit", "origin/main"]);
    a.ok(&["check"]);
    let goals = a.ok(&["goals"]);
    for i in 0..ROUNDS {
        assert!(
            goals.contains(&format!("done    q{i}")),
            "q{i} is not done: {goals}"
        );
    }
}

// Rewrites the newest proof record as the machine's own clock would have stamped it.
fn skew_newest_proof(m: &Machine, seconds_behind: i64) {
    let dir = m.repo.join(".whosaidso/proofs");
    let mut newest: Option<(i64, PathBuf)> = None;
    for e in fs::read_dir(&dir).unwrap() {
        let path = e.unwrap().path();
        let v: Value = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
        let at = v["ran_at"].as_i64().unwrap();
        if newest.as_ref().is_none_or(|(t, _)| at > *t) {
            newest = Some((at, path));
        }
    }
    let (at, path) = newest.unwrap();
    let mut v: Value = serde_json::from_slice(&fs::read(&path).unwrap()).unwrap();
    v["ran_at"] = json!(at - seconds_behind * 1_000_000_000);
    let bytes = serde_json::to_vec(&v).unwrap();
    fs::remove_file(&path).unwrap();
    fs::write(dir.join(format!("{}.json", hash(&bytes))), bytes).unwrap();
}

// Alice lands a passing proof; main is then broken by a commit naming no goal; bob re-checks and lands the
// failing proof, stamped by a clock `behind_hours` slow. Returns what any machine's `goals` then says.
fn verdict_after_a_failing_proof(ahead_hours: i64) -> String {
    let world = World::new();
    let a = world.first_machine("alice");
    a.write("flag.txt", "ok\n");
    let mut goal = goal_json(Some("pm"), 1, None);
    goal["check"] = json!(["sh", "-c", "grep -qx ok flag.txt"]);
    seed(&a, &[("pm", goal_json(None, 1, None)), ("g", goal)]);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    a.branch("alice-proof");
    a.ok(&["prove", "g", "--reason", "already present"]);
    // Alice's clock runs ahead of bob's, so his new proof is the older one by wall clock.
    if ahead_hours > 0 {
        skew_newest_proof(&a, -ahead_hours * 3600);
    }
    a.commit_all("alice's passing proof", "none");
    a.ok(&["land"]);
    a.branch("alice-break");
    a.write("flag.txt", "bad\n");
    a.commit_all("break the flag", "none");
    a.ok(&["land"]);
    b.branch("bob-proof");
    let said = b.ok(&["prove", "g", "--reason", "re-check"]);
    assert!(
        said.contains("fail"),
        "control: the check must fail on the broken main: {said}"
    );
    b.commit_all("bob's failing proof", "none");
    b.ok(&["land"]);
    a.refresh();
    a.git(&["switch", "-q", "-C", "audit", "origin/main"]);
    a.ok(&["goals"])
}

#[test]
fn a_machine_whose_clock_is_hours_slow_cannot_hide_its_failing_proof_behind_an_older_pass() {
    let control = verdict_after_a_failing_proof(0);
    assert!(
        control.contains("failing g"),
        "control: with agreeing clocks the later failure must win: {control}"
    );
    let skewed = verdict_after_a_failing_proof(3);
    assert!(
        skewed.contains("failing g"),
        "the newest result in history is a failure, but a 3-hour clock difference lets the older pass win:\n{skewed}"
    );
}

#[test]
fn proving_unchanged_inputs_again_adds_no_record() {
    let world = World::new();
    let a = world.first_machine("alice");
    a.write("flag.txt", "ok\n");
    let mut goal = goal_json(Some("pm"), 1, None);
    goal["check"] = json!(["sh", "-c", "grep -qx ok flag.txt"]);
    seed(&a, &[("pm", goal_json(None, 1, None)), ("g", goal)]);
    a.branch("alice-proof");
    a.ok(&["prove", "g", "--reason", "first"]);
    a.ok(&["prove", "g", "--reason", "first"]);
    let proofs = fs::read_dir(a.repo.join(".whosaidso/proofs"))
        .unwrap()
        .count();
    assert_eq!(proofs, 1, "the same result was recorded twice");
}
