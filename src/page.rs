// Static gallery rendering; record validation belongs in gallery.rs.
use crate::{blob, cache, gallery::{self, Media}, record::{self, Goal, Store}, show, state::{self, GoalState}};
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
fn media_html(root: &Path, dir: &Path, media: &Media, goal: &Goal, store: &Store, states: &[GoalState]) -> Result<String, String> {
    let visual = visual(root, dir, media)?;
    let result = states.iter().find(|s| s.id == goal.id).and_then(|s| s.proof).map(|i| &store.proofs[i]).map_or("no check yet", |p| record::result(p.exit_code));
    let commit: String = media.commit.chars().take(7).collect();
    Ok(format!("<article>{visual}<h3>{}</h3><p>{} · {} · {} · {}</p></article>", escape(&media.shows), escape(&goal.title), escape(result), show::time(media.ran_at / 1_000_000_000), escape(&commit)))
}
pub fn view(root: &Path) -> Result<(), String> {
    let store = record::load(root)?;
    let states = state::states(root, &store)?;
    let media = &store.media;
    let dir = cache::dir(root)?.join("view");
    fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
    let mut html = String::from("<!doctype html><html><head><meta charset=\"utf-8\"><title>Gallery</title><style>body{font:16px sans-serif;max-width:70rem;margin:auto;padding:2rem;background:#fafafa;color:#222}article{background:white;border:1px solid #ddd;border-radius:8px;padding:1rem;margin:1rem 0}img,video{max-width:100%;height:auto}.missing{padding:3rem;border:1px dashed #888}p{color:#555}@media (prefers-color-scheme: dark){body{background:#161616;color:#eee}article{background:#222;border-color:#555}p{color:#ccc}}</style></head><body><h1>Gallery</h1><a href=\"progress.html\">Progress</a>");
    for media in gallery::newest(&store).into_iter().take(gallery::LIVE_MEDIA) {
        if let Some(goal) = store.goals.iter().find(|g| g.id == media.goal) {
            html.push_str(&media_html(root, &dir, media, goal, &store, &states)?);
        }
    }
    html.push_str("</body></html>\n");
    let path = dir.join("gallery.html");
    fs::write(&path, html).map_err(|e| e.to_string())?;
    crate::progress::write(root, &dir, &store, media, &states)?;
    println!("{}", path.display());
    Ok(())
}
pub fn visual(root: &Path, dir: &Path, media: &Media) -> Result<String, String> {
    let path = blob::path(root, &media.media_sha256, &media.ext)?;
    if !path.is_file() { return Ok("<div class=\"missing\">media not on this machine</div>".into()); }
    let url = escape(&relative(dir, &path)?); let title = escape(&media.shows);
    let kind = gallery::media_type(&media.ext)?;
    Ok(match kind.tag {
        "img" => format!("<img src=\"{url}\" loading=\"lazy\" alt=\"{title}\">"),
        "video" => format!("<video controls preload=\"metadata\"><source src=\"{url}\" type=\"{}\">{title}</video>", kind.mime),
        _ => format!("<a href=\"{url}\" type=\"{}\">{title}</a>", kind.mime),
    })
}
