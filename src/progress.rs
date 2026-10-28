// Render the progress page from the same goal states and rollups as the CLI.
use crate::{gallery::Media, goals, page::{escape, visual}, record::{self, Goal, Store}, show, state::GoalState};
use std::{collections::HashSet, fs, path::Path};

fn render_goal(root: &Path, dir: &Path, store: &Store, media: &[Media], states: &[GoalState], goal: &Goal, html: &mut String, seen: &mut HashSet<String>) -> Result<(), String> {
    if !seen.insert(goal.id.clone()) { return Ok(()); }
    let Some(s) = states.iter().find(|s| s.id == goal.id) else { return Ok(()); };
    html.push_str(&format!("<details><summary>{} {}</summary>", goals::label(s.state), escape(&goal.title)));
    for ask in record::asks(store, &goal.id).iter().take(3) { html.push_str(&format!("<p>Ask: {}</p>", escape(&ask.said))); }
    if let Some(p) = s.proof.map(|i| &store.proofs[i]) { html.push_str(&format!("<p>Proof: {}</p>", escape(&show::proof(p)))); }
    html.push_str(&format!("<p>Branches: {}</p>", escape(&s.active_branches.join(", "))));
    if let Some(i) = record::newest_media(store, &goal.id) {
        html.push_str(&visual(root, dir, i)?);
    }
    html.push_str("</details>");
    for child in goals::children(&store.goals, &goal.id) { render_goal(root, dir, store, media, states, child, html, seen)?; }
    Ok(())
}
pub fn write(root: &Path, dir: &Path, store: &Store, media: &[Media], states: &[GoalState]) -> Result<(), String> {
    let mut html = String::from("<!doctype html><html><head><meta charset=\"utf-8\"><title>Progress</title><style>body{font:16px sans-serif;max-width:70rem;margin:auto;padding:2rem;background:#fafafa;color:#222}details{padding:1rem;border:1px solid #ddd}img,video{max-width:16rem;height:auto}progress{width:100%}@media (prefers-color-scheme:dark){body{background:#161616;color:#eee}details{border-color:#555}}</style></head><body><h1>Progress</h1><a href=\"gallery.html\">Gallery</a>");
    let mut phases: Vec<_> = store.phases.iter().collect(); phases.sort_by_key(|p| p.order);
    for p in phases {
        let (done, total, counts) = goals::counts(&store.goals, &states, &p.id);
        html.push_str(&format!("<section><h2>P{} {}</h2><progress value=\"{}\" max=\"{}\"></progress><p>{}</p>", p.order, escape(&p.title), done, total.max(1), escape(&counts)));
        for g in goals::children(&store.goals, &p.id) { render_goal(root, dir, store, media, states, g, &mut html, &mut HashSet::new())?; }
        html.push_str("</section>");
    }
    html.push_str("</body></html>\n");
    fs::write(dir.join("progress.html"), html).map_err(|e| e.to_string())
}
