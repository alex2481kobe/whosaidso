use super::*;

#[test]
fn questions_name_the_best_symbol_once_per_file() {
    let f = Fixture::empty();
    write(
        &f.root.join("billing.rs"),
        "/// Calculates invoice totals including sales tax.\nfn calculate_invoice() {}\n/// Sends a notification.\nfn notify() {}\n",
    );
    let found = cli(
        &f.root,
        &[
            "find",
            "how can I calculate an invoice total for a customer including sales tax",
        ],
    );
    assert!(
        found.lines().next().unwrap().contains("calculate_invoice"),
        "{found}"
    );
    assert_eq!(
        found
            .lines()
            .filter(|line| line.contains("billing.rs"))
            .count(),
        1,
        "{found}"
    );
    let context = cli(&f.root, &["context", "calculate invoice"]);
    assert!(
        context.contains("calculate_invoice") && context.contains("file billing.rs"),
        "{context}"
    );
}

#[test]
fn natural_question_recovers_two_meaningful_terms() {
    let f = Fixture::empty();
    write(
        &f.root.join("billing.rs"),
        "/// Calculates invoice sales tax.\nfn calculate_invoice() {}\n",
    );
    let found = cli(
        &f.root,
        &[
            "find",
            "please explain customer invoice tax handling behavior",
        ],
    );
    assert!(found.contains("calculate_invoice"), "{found}");
    assert!(cli(&f.root, &["find", "invoice qzxv qzzq zxqq"]).contains("no indexed match"));
}

#[test]
fn one_large_file_cannot_fill_the_candidate_window() {
    let f = Fixture::empty();
    let body = (0..240)
        .map(|i| format!("/// Computes invoice sales tax.\nfn charge_{i}() {{}}\n"))
        .collect::<String>();
    write(&f.root.join("many.rs"), body);
    write(
        &f.root.join("other.rs"),
        format!(
            "/// Computes invoice sales tax {}\nfn customer_charge() {{}}\n",
            "filler ".repeat(300)
        ),
    );
    let found = cli(
        &f.root,
        &["find", "computes invoice sales tax", "--limit", "5"],
    );
    assert!(
        found.contains("many.rs") && found.contains("customer_charge"),
        "{found}"
    );
}
