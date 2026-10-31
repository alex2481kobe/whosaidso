// HTTP request parsing, authentication, and responses for the local viewer.
use super::{
    Server, api,
    response::{Reply, apply_range, write_reply},
};
use crate::projects;
use std::{
    collections::HashMap,
    io::{BufRead, BufReader, Read},
    net::TcpStream,
    path::Path,
    time::Duration,
};

fn decode(s: &str) -> Result<String, String> {
    let mut bytes = Vec::new();
    let mut chars = s.bytes();
    while let Some(b) = chars.next() {
        bytes.push(match b {
            b'+' => b' ',
            b'%' => {
                let hex = [
                    chars.next().ok_or("invalid URL escape")?,
                    chars.next().ok_or("invalid URL escape")?,
                ];
                u8::from_str_radix(std::str::from_utf8(&hex).map_err(|e| e.to_string())?, 16)
                    .map_err(|e| e.to_string())?
            }
            _ => b,
        });
    }
    String::from_utf8(bytes).map_err(|e| e.to_string())
}
fn query(s: &str) -> Result<HashMap<String, String>, String> {
    let mut result = HashMap::new();
    for pair in s.split('&').filter(|s| !s.is_empty()) {
        let (k, v) = pair.split_once('=').unwrap_or((pair, ""));
        if result.insert(decode(k)?, decode(v)?).is_some() {
            return Err("duplicate query parameter".into());
        }
    }
    Ok(result)
}
pub(super) fn connection(mut stream: TcpStream, server: &Server) -> std::io::Result<()> {
    stream.set_read_timeout(Some(Duration::from_secs(5)))?;
    stream.set_write_timeout(Some(Duration::from_secs(30)))?;
    let mut bytes = Vec::new();
    let mut reader = BufReader::new((&mut stream).take(16385));
    loop {
        let start = bytes.len();
        if reader.read_until(b'\n', &mut bytes)? == 0 || bytes.len() > 16384 {
            return write_reply(&mut stream, Reply::error(400, "invalid request headers"));
        }
        if &bytes[start..] == b"\r\n" {
            break;
        }
    }
    let Ok(request) = std::str::from_utf8(&bytes) else {
        return write_reply(&mut stream, Reply::error(400, "invalid request"));
    };
    let mut lines = request.split("\r\n");
    let parts: Vec<_> = lines.next().unwrap_or("").split_whitespace().collect();
    if parts.len() != 3 || !matches!(parts[2], "HTTP/1.0" | "HTTP/1.1") {
        return write_reply(&mut stream, Reply::error(400, "invalid request line"));
    }
    let mut headers = HashMap::new();
    for line in lines.filter(|s| !s.is_empty()) {
        let Some((key, value)) = line.split_once(':') else {
            return write_reply(&mut stream, Reply::error(400, "invalid header"));
        };
        if headers
            .insert(key.to_ascii_lowercase(), value.trim())
            .is_some()
        {
            return write_reply(&mut stream, Reply::error(400, "duplicate header"));
        }
    }
    let mut reply = if parts[0] != "GET" {
        let mut r = Reply::error(405, "GET only");
        r.header("Allow", "GET");
        r
    } else {
        respond(server, parts[1], &headers)
    };
    if reply.code == 200
        && reply.mime != "application/json; charset=utf-8"
        && parts[1].starts_with("/api/media/")
        && let Some(range) = headers.get("range")
    {
        apply_range(&mut reply, range);
    }
    write_reply(&mut stream, reply)
}
fn respond(server: &Server, target: &str, headers: &HashMap<String, &str>) -> Reply {
    let origin = format!("http://{}", server.host);
    if headers.get("host") != Some(&server.host.as_str())
        || headers.get("origin").is_some_and(|v| *v != origin)
    {
        return Reply::error(403, "forbidden");
    }
    let (path, params) = target.split_once('?').unwrap_or((target, ""));
    let params = match query(params) {
        Ok(q) => q,
        Err(e) => return Reply::error(400, &e),
    };
    let cookie = format!("{}={}", server.cookie, server.session);
    let mut spent = server.spent.lock().unwrap_or_else(|e| e.into_inner());
    let authenticated = *spent
        && headers
            .get("cookie")
            .is_some_and(|v| v.split(';').any(|v| v.trim() == cookie));
    if !authenticated {
        if path != "/" || *spent || params.get("token") != Some(&server.token) {
            return Reply::error(403, "forbidden: open the printed viewer URL");
        }
        *spent = true;
        let mut reply = Reply::bytes("text/plain; charset=utf-8", b"");
        reply.code = 303;
        reply.header(
            "Set-Cookie",
            format!("{cookie}; Path=/; HttpOnly; SameSite=Strict"),
        );
        reply.header("Location", "/");
        return reply;
    }
    drop(spent);
    if let Some((mime, bytes)) = asset(path) {
        return Reply::bytes(mime, bytes);
    }
    if path == "/api/projects" {
        return match projects::list() {
            Ok(rows) => Reply::json(&serde_json::json!({"projects":rows,"current":server.current})),
            Err(e) => Reply::error(500, &e),
        };
    }
    if !path.starts_with("/api/") {
        return Reply::error(404, "not found");
    }
    let rows = match projects::list() {
        Ok(rows) => rows,
        Err(e) => return Reply::error(500, &e),
    };
    let selected = params
        .get("project")
        .map(String::as_str)
        .or_else(|| server.current.as_ref().and_then(|p| p.to_str()));
    let root = selected
        .and_then(|id| rows.iter().find(|p| p.id == id && p.available))
        .or_else(|| {
            if selected.is_none() {
                rows.iter().find(|p| p.available)
            } else {
                None
            }
        });
    let Some(root) = root else {
        return Reply::error(404, "no available project");
    };
    api::answer(Path::new(&root.id), path, &params)
}
fn asset(path: &str) -> Option<(&'static str, &'static [u8])> {
    macro_rules! assets { ($($name:literal),+) => { match path { "/" => Some(("text/html; charset=utf-8", include_bytes!("index.html").as_slice())), $(concat!("/", $name) => Some((if $name.ends_with(".css") { "text/css; charset=utf-8" } else { "text/javascript; charset=utf-8" }, include_bytes!($name).as_slice())),)+ _ => None } }; }
    assets!(
        "ui.css",
        "ui-gallery.css",
        "ui.js",
        "ui-icons.js",
        "ui-components.js",
        "ui-parts.js",
        "ui-shell.js",
        "ui-goals.js",
        "ui-flows.js",
        "ui-flow-diagram.js",
        "ui-flow-layout.js",
        "ui-gallery.js",
        "ui-gallery-media.js",
        "ui-gallery-actions.js",
        "ui-gallery-inspect.js"
    )
}
