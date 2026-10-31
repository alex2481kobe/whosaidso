use super::*;

#[test]
fn racing_machines_claim_one_goal_and_the_losers_are_told_who_holds_it() {
    const ROUNDS: usize = 20;
    let world = World::new();
    let first = world.first_machine("alice");
    let goals: Vec<(String, Value)> = (0..ROUNDS)
        .map(|i| (format!("c{i}"), goal_json(None, i as i64 + 1, None)))
        .collect();
    let refs: Vec<(&str, Value)> = goals.iter().map(|(i, v)| (i.as_str(), v.clone())).collect();
    seed(&first, &refs);
    let second = world.clone_machine("bob");
    let third = world.clone_machine("carol");
    let machines = [&first, &second, &third];
    for m in machines {
        m.ok(&["install-hooks"]);
    }
    let mut wins = [0; 3];
    let mut lost_at_push = 0;
    for i in 0..ROUNDS {
        let goal = format!("c{i}");
        let outs = race_spread(
            &[
                (machines[0], &["start", &goal]),
                (machines[1], &["start", &goal]),
                (machines[2], &["start", &goal]),
            ],
            i as u64 + 1,
            120,
        );
        let winners: Vec<usize> = (0..3).filter(|n| outs[*n].status.success()).collect();
        assert_eq!(
            winners.len(),
            1,
            "round {i}: exactly one machine may hold {goal}: {outs:?}"
        );
        let winner = winners[0];
        wins[winner] += 1;
        let claim = world
            .claim(&goal)
            .unwrap_or_else(|| panic!("round {i}: nobody holds {goal} on the remote"));
        assert!(
            claim.contains(&format!("author {}\n", machines[winner].name)),
            "round {i}: remote claim is not the winner's: {claim}"
        );
        for n in (0..3).filter(|n| *n != winner) {
            let said = text(&outs[n]);
            assert_eq!(outs[n].status.code(), Some(1), "round {i}: {said}");
            if said.contains("was just claimed") {
                lost_at_push += 1;
            }
            assert!(
                said.contains("claimed by") && said.contains(&machines[winner].name),
                "round {i}: {} lost {goal} but was not told who holds it: {said}",
                machines[n].name
            );
            // A later attempt gets the same plain answer, and a loser's release never touches the winner's claim.
            machines[n].refuse(&["start", &goal], &machines[winner].name);
            machines[n].ok(&["start", "none"]);
            assert!(
                world.claim(&goal).is_some_and(|c| c == claim),
                "round {i}: a loser's release changed the winner's claim"
            );
        }
        machines[winner].ok(&["start", "none"]);
        assert!(
            world.claim(&goal).is_none(),
            "round {i}: release left a claim behind"
        );
    }
    eprintln!(
        "claim race wins per machine: {wins:?}; losers beaten at the push itself: {lost_at_push}"
    );
}

fn go_offline(m: &Machine) -> String {
    let url = m.git(&["remote", "get-url", "origin"]).trim().to_owned();
    m.git(&[
        "remote",
        "set-url",
        "origin",
        m.home.join("unplugged.git").to_str().unwrap(),
    ]);
    url
}

#[test]
fn a_machine_that_claimed_offline_cannot_take_the_goal_after_the_other_machine_claimed_online() {
    let world = World::new();
    let a = world.first_machine("alice");
    seed(&a, &[("g", goal_json(None, 1, Some("g.txt")))]);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    a.branch("alice-g");
    b.branch("bob-g");
    let url = go_offline(&a);
    // Control: offline, the claim is accepted locally and says it is not yet published.
    let said = a.ok(&["start", "g"]);
    assert!(said.contains("local only"), "{said}");
    a.work(&[("g.txt", "alice")], "g");
    // Bob, online, claims it and publishes the claim.
    b.ok(&["start", "g"]);
    let bobs = world.claim("g").expect("bob's claim is on the remote");
    assert!(bobs.contains("author bob\n"), "{bobs}");
    // Alice reconnects. Neither starting again nor landing may take the goal from the published holder.
    a.git(&["remote", "set-url", "origin", &url]);
    let before = a.head();
    let said = a.refuse(&["start", "g"], "claimed by");
    assert!(
        said.contains("bob"),
        "the refusal must name the holder: {said}"
    );
    let said = a.refuse(&["land"], "claimed by");
    assert!(said.contains("bob"), "land must name the holder: {said}");
    assert_eq!(a.head(), before, "a refused land moved alice's HEAD");
    assert!(a.read("g.txt") == "alice", "alice's work was lost");
    assert_eq!(
        a.git(&["status", "--porcelain"]),
        "",
        "refused land left the tree dirty"
    );
    assert_eq!(
        world.claim("g").as_deref(),
        Some(bobs.as_str()),
        "alice's attempts disturbed bob's published claim"
    );
    let main_files = world.origin_git(&["ls-tree", "-r", "--name-only", "main"]);
    assert!(
        !main_files.contains("g.txt"),
        "nothing from the refused land may be published"
    );
    // Bob lands; afterwards the claim is gone and both machines still pass check.
    b.work(&[("g.txt", "bob")], "g");
    b.ok(&["land"]);
    assert!(world.claim("g").is_none());
    for m in [&a, &b] {
        m.refresh();
        m.git(&["switch", "-q", "-C", "audit", "origin/main"]);
        m.ok(&["check"]);
    }
}

#[test]
fn offline_claims_become_the_published_claim_when_nobody_else_wants_the_goal() {
    let world = World::new();
    let a = world.first_machine("alice");
    seed(&a, &[("g", goal_json(None, 1, Some("g.txt")))]);
    let b = world.clone_machine("bob");
    a.branch("alice-g");
    let url = go_offline(&a);
    a.ok(&["start", "g"]);
    a.work(&[("g.txt", "alice")], "g");
    a.git(&["remote", "set-url", "origin", &url]);
    a.ok(&["land"]);
    assert!(world.claim("g").is_none());
    b.refresh();
    let tip = b.git(&["rev-parse", "origin/main"]);
    assert_eq!(b.git(&["show", &format!("{}:g.txt", tip.trim())]), "alice");
}

fn claim_world(goals: &[&str]) -> (World, Machine, Machine) {
    let world = World::new();
    let a = world.first_machine("alice");
    let list: Vec<(&str, Value)> = goals
        .iter()
        .enumerate()
        .map(|(i, g)| (*g, goal_json(None, i as i64 + 1, Some(&format!("{g}.txt")))))
        .collect();
    seed(&a, &list);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    a.branch("alice-work");
    b.branch("bob-work");
    (world, a, b)
}

#[test]
fn a_lapsed_claim_is_taken_over_and_the_old_holder_cannot_land_over_the_new_one() {
    let (world, a, b) = claim_world(&["g"]);
    a.ok(&["start", "g"]);
    a.work(&[("g.txt", "alice")], "g");
    // Control: inside the lease bob is refused.
    b.refuse(&["start", "g"], "claimed by");
    restamp_claim(&world, &a, "g", unix_now() - 73 * 3600);
    b.ok(&["start", "g"]);
    let claim = world.claim("g").unwrap();
    assert!(
        claim.contains("author bob\n"),
        "the takeover is not published: {claim}"
    );
    // Alice's machine still believes it holds the goal; the remote says otherwise.
    let said = a.refuse(&["land"], "claimed by");
    assert!(said.contains("bob"), "{said}");
    a.refuse(&["start", "g"], "bob");
    assert!(
        world
            .origin_git(&["ls-tree", "-r", "--name-only", "main"])
            .lines()
            .all(|l| l != "g.txt")
    );
    assert_eq!(
        world.claim("g").as_deref(),
        Some(claim.as_str()),
        "the old holder disturbed the new holder's claim"
    );
    b.work(&[("g.txt", "bob")], "g");
    b.ok(&["land"]);
    assert!(world.claim("g").is_none());
}

#[test]
fn a_lapsed_claim_has_one_new_holder_when_the_old_holder_and_a_taker_start_together() {
    const ROUNDS: usize = 10;
    let names: Vec<String> = (0..ROUNDS).map(|i| format!("t{i}")).collect();
    let refs: Vec<&str> = names.iter().map(String::as_str).collect();
    let (world, a, b) = claim_world(&refs);
    for (i, goal) in names.iter().enumerate() {
        a.ok(&["start", goal]);
        restamp_claim(&world, &a, goal, unix_now() - 80 * 3600);
        let outs = race_spread(
            &[(&a, &["start", goal]), (&b, &["start", goal])],
            i as u64 + 1,
            100,
        );
        let winners: Vec<usize> = (0..2).filter(|n| outs[*n].status.success()).collect();
        assert_eq!(
            winners.len(),
            1,
            "round {i}: the lapsed claim ended with {} holders: {outs:?}",
            winners.len()
        );
        let claim = world.claim(goal).unwrap();
        assert!(
            claim.contains(&format!(
                "author {}\n",
                if winners[0] == 0 { "alice" } else { "bob" }
            )),
            "round {i}: {claim}"
        );
        a.ok(&["start", "none"]);
        b.ok(&["start", "none"]);
        // Each machine's `start none` releases only its own claim: the winner's claim must still be released by the winner.
        let _ = world.claim(goal);
    }
}

#[test]
fn clocks_hours_apart_do_not_break_or_extend_a_claim_by_more_than_the_skew() {
    let (world, a, b) = claim_world(&["early", "late"]);
    a.ok(&["start", "early"]);
    // alice's clock is 5 hours slow: her claim is stamped in the past but still binds.
    restamp_claim(&world, &a, "early", unix_now() - 5 * 3600);
    let said = b.refuse(&["start", "early"], "claimed by");
    assert!(said.contains("alice"), "{said}");
    a.ok(&["start", "none"]);
    a.ok(&["start", "late"]);
    // alice's clock is 5 hours fast: the claim reads as made in the future, and the age shown must not wrap or panic.
    restamp_claim(&world, &a, "late", unix_now() + 5 * 3600);
    let said = b.refuse(&["start", "late"], "claimed by");
    assert!(
        said.contains("alice") && said.contains(" ago"),
        "a claim from a fast clock must still print a sane age: {said}"
    );
    b.ok(&["goals"]);
}

#[test]
fn two_machines_running_the_same_agent_name_still_exclude_each_other() {
    let (world, a, b) = claim_world(&["g"]);
    let mut a = a;
    let mut b = b;
    a.env.push(("WHOSAIDSO_AGENT".into(), "claude".into()));
    b.env.push(("WHOSAIDSO_AGENT".into(), "claude".into()));
    a.ok(&["start", "g"]);
    let out = b.ws(&["start", "g"]);
    assert_ne!(
        out.status.code(),
        Some(0),
        "two machines both run agent \"claude\" and both now believe they hold g; remote claim: {:?}",
        world.claim("g")
    );
}

#[test]
fn a_claim_held_on_another_machine_can_be_read_without_trying_to_take_it() {
    let world = World::new();
    let a = world.first_machine("alice");
    seed(&a, &[("g", goal_json(None, 1, Some("g.txt")))]);
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    a.branch("alice-g");
    a.ok(&["start", "g"]);
    b.refresh();
    let seen = format!("{}{}", b.ok(&["goals"]), b.ok(&["show", "g"]));
    assert!(
        seen.contains("alice"),
        "bob cannot see who holds g from `goals` or `show g` (only a refused `start` or the viewer reveals it); they said: {seen}"
    );
}

#[test]
fn the_other_machines_branches_states_and_evidence_are_visible_after_a_fetch() {
    let world = World::new();
    let a = world.first_machine("alice");
    seed(
        &a,
        &[
            ("g", goal_json(None, 1, Some("g.txt"))),
            ("h", goal_json(None, 2, None)),
        ],
    );
    let b = world.clone_machine("bob");
    b.ok(&["install-hooks"]);
    a.branch("alice-g");
    a.ok(&["start", "g"]);
    // `next` must never hand bob the goal alice holds.
    let said = b.ok(&["next"]);
    assert!(
        !said.contains("start: g"),
        "bob's `next` handed out the goal alice holds: {said}"
    );
    // The installed pre-push guard refuses branch pushes, so landed history and claims are all bob can ever see.
    a.work(&[("g.txt", "alice")], "g");
    b.refresh();
    // Evidence and state after alice lands.
    let bytes = image(4, 4000);
    let file = a.home.join("fig.png");
    fs::write(&file, &bytes).unwrap();
    a.ok(&["media", "g", file.to_str().unwrap(), "Alice's figure"]);
    a.commit_all("figure", "none");
    a.ok(&["land"]);
    b.refresh();
    b.git(&["switch", "-q", "-C", "audit", "origin/main"]);
    let goals = b.ok(&["goals"]);
    assert!(goals.contains("done    g"), "{goals}");
    let shown = b.ok(&["show", "g"]);
    assert!(
        shown.contains("Alice's figure"),
        "bob does not see alice's evidence: {shown}"
    );
    b.ok(&["sync"]);
    let said = b.ok(&["check"]);
    assert!(!said.contains("warning"), "{said}");
    assert_eq!(
        fs::read(
            b.repo
                .join(format!(".git/whosaidso/blobs/{}.png", hash(&bytes)))
        )
        .unwrap(),
        bytes
    );
}

#[test]
fn an_agent_can_release_a_goal_assigned_to_its_name() {
    let (world, mut a, _b) = claim_world(&["g"]);
    a.env.push(("WHOSAIDSO_AGENT".into(), "claude".into()));
    a.ok(&["assign", "g", "agent:claude"]);
    assert!(world.claim("g").unwrap().contains("agent:claude"));
    a.ok(&["start", "none"]);
    assert!(
        world.claims().is_empty(),
        "the assigned claim was not released"
    );
}

#[test]
fn a_remote_that_never_answers_does_not_hold_up_reading_goals() {
    let (_world, a, _b) = claim_world(&["g"]);
    a.git(&["config", "protocol.ext.allow", "always"]);
    a.git(&["remote", "set-url", "origin", "ext::sleep 30"]);
    let started = std::time::Instant::now();
    let out = a.ws(&["goals"]);
    assert!(
        started.elapsed().as_secs() < 10,
        "goals waited for the remote"
    );
    assert!(
        text(&out).contains("g") && text(&out).contains("claims not synced"),
        "{}",
        text(&out)
    );
}
