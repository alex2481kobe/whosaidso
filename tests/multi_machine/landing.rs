use super::*;

#[test]
fn two_machines_land_different_goals_at_once() {
    const ROUNDS: usize = 12;
    let world = World::new();
    let a = world.first_machine("alice");
    let mut goals = Vec::new();
    for i in 0..ROUNDS {
        goals.push((format!("m{i}"), goal_json(None, i as i64 + 1, None)));
        goals.push((
            format!("a{i}"),
            goal_json(Some(&format!("m{i}")), 1, Some(&format!("a{i}.txt"))),
        ));
        goals.push((
            format!("b{i}"),
            goal_json(Some(&format!("m{i}")), 2, Some(&format!("b{i}.txt"))),
        ));
    }
    let refs: Vec<(&str, Value)> = goals.iter().map(|(i, v)| (i.as_str(), v.clone())).collect();
    seed(&a, &refs);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    let mut retested = 0;
    for i in 0..ROUNDS {
        let (ga, gb) = (format!("a{i}"), format!("b{i}"));
        a.branch(&format!("work-{ga}"));
        b.branch(&format!("work-{gb}"));
        a.ok(&["start", &ga]);
        b.ok(&["start", &gb]);
        a.work(&[(&format!("{ga}.txt"), "a")], &ga);
        b.work(&[(&format!("{gb}.txt"), "b")], &gb);
        let outs = race_spread(&[(&a, &["land"]), (&b, &["land"])], i as u64 + 1, 900);
        for (o, who) in outs.iter().zip(["alice", "bob"]) {
            assert!(
                o.status.success(),
                "round {i}: {who} land failed: {}",
                text(o)
            );
        }
        if outs.iter().any(|o| text(o).contains("testing again")) {
            retested += 1;
        }
        a.refresh();
        let tip = a.git(&["rev-parse", "origin/main"]);
        for file in [format!("{ga}.txt"), format!("{gb}.txt")] {
            a.git(&["cat-file", "-e", &format!("{}:{file}", tip.trim())]);
        }
        assert_eq!(a.claim_refs(), "", "round {i}: claims left on the remote");
    }
    for m in [&a, &b] {
        m.refresh();
        m.git(&["switch", "-q", "-C", "audit", "origin/main"]);
        m.ok(&["check"]);
        let goals = m.ok(&["goals"]);
        for line in goals
            .lines()
            .filter(|l| l.trim_start().starts_with("open") || l.trim_start().starts_with("done"))
        {
            let id = line.split_whitespace().nth(1).unwrap();
            assert!(
                id.starts_with('m') || line.trim_start().starts_with("done"),
                "{}: {id} is not done: {goals}",
                m.name
            );
        }
    }
    assert_tips_tested(&world, &a);
    eprintln!(
        "different-goal landings: {ROUNDS} rounds, {retested} where the loser of the push re-merged and re-tested"
    );
}

#[test]
fn genuinely_conflicting_edits_land_one_and_the_other_resolves_and_lands() {
    // Each round also resolves and lands the loser (about 5 s), so this race runs 6 rounds, not 20.
    const ROUNDS: usize = 6;
    let world = World::new();
    let a = world.first_machine("alice");
    let mut goals = Vec::new();
    for i in 0..ROUNDS {
        a.write(&format!("shared{i}.txt"), "base\n");
        goals.push((
            format!("p{i}"),
            goal_json(None, 2 * i as i64 + 1, Some(&format!("p{i}.txt"))),
        ));
        goals.push((
            format!("q{i}"),
            goal_json(None, 2 * i as i64 + 2, Some(&format!("q{i}.txt"))),
        ));
    }
    let refs: Vec<(&str, Value)> = goals.iter().map(|(i, v)| (i.as_str(), v.clone())).collect();
    seed(&a, &refs);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    let mut empty_refusals = 0;
    for i in 0..ROUNDS {
        let (gp, gq) = (format!("p{i}"), format!("q{i}"));
        let shared = format!("shared{i}.txt");
        a.branch(&format!("work-{gp}"));
        b.branch(&format!("work-{gq}"));
        a.ok(&["start", &gp]);
        b.ok(&["start", &gq]);
        a.work(
            &[(&shared, "alice's version\n"), (&format!("{gp}.txt"), "a")],
            &gp,
        );
        b.work(
            &[(&shared, "bob's version\n"), (&format!("{gq}.txt"), "b")],
            &gq,
        );
        let (heads, outs) = (
            [a.head(), b.head()],
            race_spread(&[(&a, &["land"]), (&b, &["land"])], i as u64 + 1, 900),
        );
        let winners: Vec<usize> = (0..2).filter(|n| outs[*n].status.success()).collect();
        assert_eq!(
            winners.len(),
            1,
            "round {i}: exactly one of two conflicting landings may succeed: {outs:?}"
        );
        let (win, lose) = (winners[0], 1 - winners[0]);
        let (w, l) = if win == 0 { (&a, &b) } else { (&b, &a) };
        let said = text(&outs[lose]);
        assert_eq!(outs[lose].status.code(), Some(1), "round {i}: {said}");
        if said.trim().trim_start_matches("git:").trim().is_empty() {
            empty_refusals += 1;
        }
        // Nothing half-published: the remote has the winner's file and a tested receipt, never a mixture.
        let winners_text = if win == 0 {
            "alice's version\n"
        } else {
            "bob's version\n"
        };
        assert_eq!(
            world.origin_git(&["show", &format!("main:{shared}")]),
            winners_text,
            "round {i}"
        );
        l.git(&["merge-base", "--is-ancestor", &heads[lose], "HEAD"]);
        assert!(
            world.claim([&gp, &gq][win]).is_none(),
            "round {i}: the winner's claim is still held"
        );
        // The loser resolves the conflict deliberately and lands again.
        l.git(&["checkout", "-q", "--theirs", &shared]);
        l.write(&shared, "alice's version\nbob's version\n");
        l.git(&["add", &shared]);
        l.git(&["commit", "-q", "--no-edit"]);
        l.ok(&["land"]);
        w.refresh();
        assert_eq!(
            world.origin_git(&["show", &format!("main:{shared}")]),
            "alice's version\nbob's version\n",
            "round {i}"
        );
        for id in [&gp, &gq] {
            assert!(
                world.claim(id).is_none(),
                "round {i}: claim on {id} left behind"
            );
        }
    }
    for m in [&a, &b] {
        m.refresh();
        m.git(&["switch", "-q", "-C", "audit", "origin/main"]);
        m.ok(&["check"]);
    }
    assert_tips_tested(&world, &a);
    eprintln!("conflicts: {ROUNDS} rounds, refusals with an empty message: {empty_refusals}");
}

#[test]
fn two_machines_create_the_same_goal_id_with_different_content_and_land() {
    const ROUNDS: usize = 10;
    let world = World::new();
    let a = world.first_machine("alice");
    seed(&a, &[("base", goal_json(None, 1, None))]);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    for i in 0..ROUNDS {
        let id = format!("dup{i}");
        a.branch(&format!("alice-{id}"));
        b.branch(&format!("bob-{id}"));
        let mut mine = goal_json(None, 10 + i as i64, Some("a.txt"));
        mine["title"] = json!("alice's idea");
        let mut yours = goal_json(None, 10 + i as i64, Some("b.txt"));
        yours["title"] = json!("bob's idea");
        a.work(
            &[(&format!(".whosaidso/goals/{id}.json"), &mine.to_string())],
            "none",
        );
        b.work(
            &[(&format!(".whosaidso/goals/{id}.json"), &yours.to_string())],
            "none",
        );
        let outs = race_spread(&[(&a, &["land"]), (&b, &["land"])], i as u64 + 1, 900);
        let winners: Vec<usize> = (0..2).filter(|n| outs[*n].status.success()).collect();
        assert_eq!(
            winners.len(),
            1,
            "round {i}: two different goals with one id cannot both land: {outs:?}"
        );
        let (win, lose) = (winners[0], 1 - winners[0]);
        let landed: Value = serde_json::from_str(
            &world.origin_git(&["show", &format!("main:.whosaidso/goals/{id}.json")]),
        )
        .unwrap();
        assert_eq!(
            landed["title"],
            if win == 0 {
                "alice's idea"
            } else {
                "bob's idea"
            },
            "round {i}: the remote's goal is not the winner's"
        );
        // The loser keeps its commit, renames its goal and lands.
        let l = if lose == 0 { &a } else { &b };
        let _ = l.git_out(&["merge", "--abort"]);
        let mut renamed = if lose == 0 { mine } else { yours };
        renamed["order"] = json!(500 + i as i64);
        l.git(&["rm", "-q", "-f", &format!(".whosaidso/goals/{id}.json")]);
        l.write(
            &format!(".whosaidso/goals/{id}-other.json"),
            renamed.to_string(),
        );
        l.commit_all("rename the clashing goal", "none");
        l.ok(&["land"]);
    }
    for m in [&a, &b] {
        m.refresh();
        m.git(&["switch", "-q", "-C", "audit", "origin/main"]);
        m.ok(&["check"]);
    }
}

#[test]
fn a_landing_that_hits_a_merge_conflict_says_which_file_and_what_to_do() {
    let world = World::new();
    let a = world.first_machine("alice");
    a.write("shared.txt", "base\n");
    seed(
        &a,
        &[
            ("p", goal_json(None, 1, Some("p.txt"))),
            ("q", goal_json(None, 2, Some("q.txt"))),
        ],
    );
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    a.branch("alice-p");
    b.branch("bob-q");
    a.work(&[("shared.txt", "alice's version\n"), ("p.txt", "a")], "p");
    b.work(&[("shared.txt", "bob's version\n"), ("q.txt", "b")], "q");
    // Control: the first landing is accepted.
    a.ok(&["land"]);
    let said = b.refuse(&["land"], "");
    assert!(
        said.contains("shared.txt") && said.to_lowercase().contains("conflict"),
        "bob was not told which file conflicts (the tool printed {said:?}) although his tree is now mid-merge"
    );
    assert!(
        [
            "resolve",
            "fix conflicts",
            "merge --abort",
            "commit the result",
            "land again"
        ]
        .iter()
        .any(|h| said.to_lowercase().contains(h)),
        "the refusal does not say what to do next: {said:?}"
    );
}

#[test]
fn a_landing_that_clashes_on_a_goal_id_says_which_goal() {
    let world = World::new();
    let a = world.first_machine("alice");
    seed(&a, &[("base", goal_json(None, 1, None))]);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    let (mut mine, mut yours) = (
        goal_json(None, 10, Some("x.txt")),
        goal_json(None, 10, Some("y.txt")),
    );
    mine["title"] = json!("alice's idea");
    yours["title"] = json!("bob's idea");
    a.branch("alice-dup");
    b.branch("bob-dup");
    a.work(&[(".whosaidso/goals/dup.json", &mine.to_string())], "none");
    b.work(&[(".whosaidso/goals/dup.json", &yours.to_string())], "none");
    a.ok(&["land"]);
    let said = b.refuse(&["land"], "");
    assert!(
        said.contains("dup") && said.to_lowercase().contains("conflict"),
        "bob was not told that goal dup clashes (the tool printed {said:?})"
    );
}

#[test]
fn a_goal_nobody_started_is_landed_by_one_machine_and_the_other_is_told_why_not() {
    const ROUNDS: usize = 10;
    let world = World::new();
    let a = world.first_machine("alice");
    let goals: Vec<(String, Value)> = (0..ROUNDS)
        .map(|i| {
            (
                format!("h{i}"),
                goal_json(None, i as i64 + 1, Some(&format!("h{i}.txt"))),
            )
        })
        .collect();
    let refs: Vec<(&str, Value)> = goals.iter().map(|(i, v)| (i.as_str(), v.clone())).collect();
    seed(&a, &refs);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    for i in 0..ROUNDS {
        let goal = format!("h{i}");
        a.branch(&format!("alice-{goal}"));
        b.branch(&format!("bob-{goal}"));
        // Identical bytes in the shared file keep Git from conflicting; the claim is the only thing that can stop the loser.
        a.work(
            &[
                (&format!("{goal}.txt"), "done"),
                (&format!("{goal}-alice.txt"), "a"),
            ],
            &goal,
        );
        b.work(
            &[
                (&format!("{goal}.txt"), "done"),
                (&format!("{goal}-bob.txt"), "b"),
            ],
            &goal,
        );
        let outs = race_spread(&[(&a, &["land"]), (&b, &["land"])], i as u64 + 1, 900);
        let winners: Vec<usize> = (0..2).filter(|n| outs[*n].status.success()).collect();
        assert_eq!(
            winners.len(),
            1,
            "round {i}: two machines landed the same goal: {outs:?}"
        );
        let said = text(&outs[1 - winners[0]]);
        assert!(
            ["claimed", "--reason", "conflict"]
                .iter()
                .any(|k| said.contains(k))
                && !said.trim().ends_with("git:"),
            "round {i}: the loser got no usable explanation: {said:?}"
        );
        assert!(world.claim(&goal).is_none(), "round {i}: claim left behind");
        let files = world.origin_git(&["ls-tree", "-r", "--name-only", "main"]);
        let (won, lost) = if winners[0] == 0 {
            ("alice", "bob")
        } else {
            ("bob", "alice")
        };
        assert!(
            files.contains(&format!("{goal}-{won}.txt"))
                && !files.contains(&format!("{goal}-{lost}.txt")),
            "round {i}: {files}"
        );
    }
    assert_tips_tested(&world, &a);
}

#[test]
fn a_started_goal_cannot_be_landed_by_a_machine_that_never_started_it() {
    let world = World::new();
    let a = world.first_machine("alice");
    seed(&a, &[("g", goal_json(None, 1, Some("g.txt")))]);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    a.branch("alice-g");
    b.branch("bob-g");
    a.ok(&["start", "g"]);
    a.work(&[("g.txt", "alice")], "g");
    b.work(&[("other.txt", "bob")], "g");
    let said = b.refuse(&["land"], "claimed by");
    assert!(said.contains("alice"), "{said}");
    assert!(
        world
            .origin_git(&["ls-tree", "-r", "--name-only", "main"])
            .lines()
            .all(|l| l != "g.txt")
    );
    a.ok(&["land"]);
    // Now that the goal is done and its check already passes, bob is told how to proceed.
    let out = b.ws(&["land"]);
    let said = text(&out);
    assert_ne!(
        out.status.code(),
        Some(0),
        "bob's second implementation of a finished goal landed with no reason: {said}"
    );
    assert!(
        said.contains("--reason") || said.contains("reopen g"),
        "bob is not told how to proceed: {said}"
    );
}

#[test]
fn a_machine_days_behind_lands_after_the_others_moved_main_and_folded() {
    let names: Vec<String> = (0..6).map(|i| format!("s{i}")).collect();
    let mut all: Vec<(&str, Value)> = names
        .iter()
        .enumerate()
        .map(|(i, n)| {
            (
                n.as_str(),
                goal_json(None, i as i64 + 1, Some(&format!("{n}.txt"))),
            )
        })
        .collect();
    all.push(("late", goal_json(None, 50, Some("late.txt"))));
    let world = World::new();
    let a = world.first_machine("alice");
    seed(&a, &all);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    b.branch("bob-late");
    b.ok(&["start", "late"]);
    // Everything bob did is ten days old, and his claim has lapsed by now.
    let ten_days = format!("{} +0000", unix_now() - 10 * 86400);
    b.write("late.txt", "bob");
    b.git(&["add", "--all"]);
    let dated = b
        .cmd(
            "git",
            &["commit", "-q", "-m", "old work", "-m", "Goal: late"],
            &b.repo,
        )
        .env("GIT_AUTHOR_DATE", &ten_days)
        .env("GIT_COMMITTER_DATE", &ten_days)
        .output()
        .unwrap();
    assert!(dated.status.success(), "{}", text(&dated));
    restamp_claim(&world, &b, "late", unix_now() - 240 * 3600);
    // Meanwhile alice lands six other goals, folding each.
    a.branch("alice-many");
    for n in &names {
        a.ok(&["start", n]);
        a.work(&[(&format!("{n}.txt"), "x")], n);
        a.ok(&["land"]);
    }
    // Bob never fetched: his view is old but his commands work.
    b.ok(&["goals"]);
    b.ok(&["check"]);
    let said = b.ok(&["land"]);
    assert!(said.contains("published"), "{said}");
    b.refresh();
    b.git(&["switch", "-q", "-C", "audit", "origin/main"]);
    b.ok(&["check"]);
    for n in &names {
        assert!(b.exists(&format!("{n}.txt")), "bob's landing lost {n}");
    }
    assert!(b.exists("late.txt"));
    assert_tips_tested(&world, &b);
    assert!(world.claims().is_empty());
}

#[test]
fn a_machine_that_crashes_between_landing_steps_resumes_after_the_other_machine_moved_main() {
    let steps = [
        "lock",
        "fetch",
        "merge",
        "gate",
        "check",
        "media",
        "receipt",
        "before-push",
        "published",
        "released",
    ];
    std::thread::scope(|s| {
        let handles: Vec<_> = steps
            .iter()
            .map(|step| {
                s.spawn(move || {
                    let world = World::new();
                    let a = world.first_machine("alice");
                    seed(
                        &a,
                        &[
                            ("ga", goal_json(None, 1, Some("ga.txt"))),
                            ("gb", goal_json(None, 2, Some("gb.txt"))),
                        ],
                    );
                    let b = world.clone_machine("bob");
                    b.ok(&["install-hooks"]);
                    a.branch("alice-ga");
                    b.branch("bob-gb");
                    a.ok(&["start", "ga"]);
                    b.ok(&["start", "gb"]);
                    a.work(&[("ga.txt", "a")], "ga");
                    b.work(&[("gb.txt", "b")], "gb");
                    let hook =
                        format!("[ \"$WHOSAIDSO_LAND_STEP\" != {step} ] || kill -KILL $PPID");
                    let crashed = b
                        .ws_cmd(&["land"])
                        .env("WHOSAIDSO_LAND_HOOK", hook)
                        .output()
                        .unwrap();
                    assert!(
                        !crashed.status.success(),
                        "{step}: the crash hook did not stop bob's land"
                    );
                    // While bob is down, alice lands.
                    a.ok(&["land"]);
                    let resumed = b.ws(&["land"]);
                    assert!(
                        resumed.status.success(),
                        "{step}: bob could not resume: {}",
                        text(&resumed)
                    );
                    b.refresh();
                    b.git(&["switch", "-q", "-C", "audit", "origin/main"]);
                    assert!(
                        b.exists("ga.txt") && b.exists("gb.txt"),
                        "{step}: a landing was lost"
                    );
                    assert_eq!(
                        b.git(&["status", "--porcelain"]),
                        "",
                        "{step}: bob's tree is dirty"
                    );
                    b.ok(&["check"]);
                    assert!(
                        world.claims().is_empty(),
                        "{step}: claims left on the remote: {:?}",
                        world.claims()
                    );
                    assert_tips_tested(&world, &b);
                })
            })
            .collect();
        for h in handles {
            h.join().unwrap();
        }
    });
}
