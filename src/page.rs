// Static gallery rendering; record validation belongs in gallery.rs.
use crate::{blob, cache, gallery::Image, record::{self, Goal, Store}, show, state::{self, GoalState}, goals};
use std::{fs, path::{Path, PathBuf}};

pub fn escape(s: &str) -> String {
    s.replace('&', "&amp;").replace('<', "&lt;").replace('>', "&gt;").replace('"', "&quot;").replace('\'', "&#39;")
}
pub fn relative(from: &Path, to: &Path) -> Result<String, String> {
    let from = fs::canonicalize(from).map_err(|e| e.to_string())?;
    let to = fs::canonicalize(to).map_err(|e| e.to_string())?;
    let a: Vec<_> = from.components().collect();
    let b: Vec<_> = to.components().collect();
    let common = a.iter().zip(&b).take_while(|(x, y)| x == y).count();
    let mut path = PathBuf::new();
    for _ in common..a.len() { path.push(".."); }
    for part in &b[common..] { path.push(part.as_os_str()); }
    let mut url = String::new();
    for byte in path.to_string_lossy().bytes() {
        if byte.is_ascii_alphanumeric() || b"-._~/".contains(&byte) { url.push(byte as char); }
        else { url.push_str(&format!("%{byte:02X}")); }
    }
    Ok(url)
}
fn image_html(root: &Path, dir: &Path, image: &Image, goal: &Goal, store: &Store, states: &[GoalState]) -> Result<String, String> {
    let path = blob::path(root, &image.media_sha256, &image.ext)?;
    let visual = if path.is_file() {
        format!("<img src=\"{}\" loading=\"lazy\" alt=\"{}\">", escape(&relative(dir, &path)?), escape(&image.shows))
    } else { "<div class=\"missing\">image not on this machine</div>".into() };
    let result = states.iter().find(|s| s.id == goal.id).and_then(|s| s.proof).map(|i| &store.proofs[i]).map_or("no check yet", |p| record::result(p.exit_code));
    let commit: String = image.commit.chars().take(7).collect();
    Ok(format!("<article>{visual}<h3>{}</h3><p>{} · {} · {} · {}</p></article>", escape(&image.shows), escape(&goal.title), escape(result), show::time(image.ran_at / 1_000_000_000), escape(&commit)))
}
pub fn view(root: &Path) -> Result<(), String> {
    let store = record::load(root)?;
    let states = state::states(root, &store)?;
    let images = &store.images;
    let dir = cache::dir(root)?.join("view");
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let mut html = String::from("<!doctype html><html><head><meta charset=\"utf-8\"><title>Gallery</title><style>body{font:16px sans-serif;max-width:70rem;margin:auto;padding:2rem;background:#fafafa;color:#222}article{background:white;border:1px solid #ddd;border-radius:8px;padding:1rem;margin:1rem 0}img{max-width:100%;height:auto}.missing{padding:3rem;border:1px dashed #888}p{color:#555}@media (prefers-color-scheme: dark){body{background:#161616;color:#eee}article{background:#222;border-color:#555}p{color:#ccc}}</style></head><body><h1>Gallery</h1><a href=\"progress.html\">Progress</a>");
    let mut phases: Vec<_> = store.phases.iter().collect();
    phases.sort_by_key(|p| p.order);
    for phase in phases {
        let goals: Vec<_> = store.goals.iter().filter(|g| goals::in_phase(g, &phase.id, &store.goals) && images.iter().any(|i| i.goal == g.id)).collect();
        if goals.is_empty() { continue; }
        html.push_str(&format!("<section><h2>{}</h2>", escape(&phase.title)));
        for goal in goals {
            let items = record::images(&store, &goal.id);
            html.push_str(&image_html(root, &dir, items[0], goal, &store, &states)?);
            if items.len() > 1 {
                html.push_str(&format!("<details><summary>{} older</summary>", items.len() - 1));
                for image in &items[1..] { html.push_str(&image_html(root, &dir, image, goal, &store, &states)?); }
                html.push_str("</details>");
            }
        }
        html.push_str("</section>");
    }
    html.push_str("</body></html>\n");
    let path = dir.join("gallery.html");
    fs::write(&path, html).map_err(|e| e.to_string())?;
    crate::progress::write(root, &dir, &store, images, &states)?;
    println!("{}", path.display());
    Ok(())
}
