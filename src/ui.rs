// Loopback HTTP transport; screen answers, the change token and embedded assets live under ui/.
mod api;
mod version;
use crate::{git, projects};
use std::{
    collections::HashMap,
    fs,
    io::{BufRead, BufReader, Read, Write},
    net::{TcpListener, TcpStream},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::{Arc, Mutex},
    time::Duration,
};

struct Server {
    current: Option<PathBuf>,
    token: String,
    session: String,
    cookie: String,
    host: String,
    spent: Mutex<bool>,
}
fn secret() -> Result<String, String> {
    let mut bytes = [0; 16];
    fs::File::open("/dev/urandom")
        .and_then(|mut f| f.read_exact(&mut bytes))
        .map_err(|e| e.to_string())?;
    Ok(bytes.iter().map(|b| format!("{b:02x}")).collect())
}
pub fn serve(cwd: &Path, args: &[String]) -> Result<(), String> {
    let (mut port, mut open) = (0u16, false);
    let mut args = args.iter();
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--help" => {
                println!(
                    "usage: whosaidso ui [--port N] [--open]\nRead-only local viewer; --open opens the printed URL in your browser."
                );
                return Ok(());
            }
            "--port" => {
                port = args
                    .next()
                    .and_then(|n| n.parse().ok())
                    .ok_or("ui: --port needs a port from 0 to 65535")?
            }
            "--open" => open = true,
            _ => return Err("usage: whosaidso ui [--port N] [--open]".into()),
        }
    }
    let current = git::root(cwd).ok();
    if let Some(root) = &current {
        projects::register_viewer(root)?;
        // Derive the goals once now, so the first page load finds them ready.
        let root = root.clone();
        std::thread::spawn(move || api::warm(&root));
    }
    let listener = TcpListener::bind(("127.0.0.1", port))
        .map_err(|e| format!("ui: listen on 127.0.0.1:{port}: {e}"))?;
    let host = listener
        .local_addr()
        .map_err(|e| e.to_string())?
        .to_string();
    let server = Arc::new(Server {
        current,
        token: secret()?,
        session: secret()?,
        cookie: format!(
            "whosaidso-{}",
            listener.local_addr().map_err(|e| e.to_string())?.port()
        ),
        host,
        spent: Mutex::new(false),
    });
    let url = format!("http://{}/?token={}", server.host, server.token);
    println!("{url}");
    std::io::stdout().flush().map_err(|e| e.to_string())?;
    if open {
        let program = if cfg!(target_os = "macos") {
            "open"
        } else {
            "xdg-open"
        };
        match Command::new(program)
            .arg(&url)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
        {
            Ok(mut child) => {
                std::thread::spawn(move || {
                    let _ = child.wait();
                });
            }
            Err(e) => eprintln!("ui: {program}: {e}"),
        }
    }
    for stream in listener.incoming() {
        let stream = stream.map_err(|e| e.to_string())?;
        let server = Arc::clone(&server);
        std::thread::spawn(move || {
            let _ = connection(stream, &server);
        });
    }
    Ok(())
}
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
struct Reply {
    code: u16,
    mime: &'static str,
    bytes: Vec<u8>,
    headers: Vec<(String, String)>,
}
impl Reply {
    fn bytes(mime: &'static str, bytes: &[u8]) -> Self {
        Self {
            code: 200,
            mime,
            bytes: bytes.into(),
            headers: Vec::new(),
        }
    }
    fn error(code: u16, message: &str) -> Self {
        let mut reply = Self::bytes(
            "application/json; charset=utf-8",
            serde_json::json!({"error":{"message":message}})
                .to_string()
                .as_bytes(),
        );
        reply.code = code;
        reply
    }
    fn json(value: &serde_json::Value) -> Self {
        Self::bytes(
            "application/json; charset=utf-8",
            value.to_string().as_bytes(),
        )
    }
    fn header(&mut self, key: &str, value: impl Into<String>) {
        self.headers.push((key.into(), value.into()));
    }
}
fn connection(mut stream: TcpStream, server: &Server) -> std::io::Result<()> {
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
fn apply_range(reply: &mut Reply, range: &str) {
    let size = reply.bytes.len();
    let bounds = range
        .strip_prefix("bytes=")
        .and_then(|r| r.split_once('-'))
        .and_then(|(a, b)| {
            if a.is_empty() {
                let n: usize = b.parse().ok()?;
                (n > 0).then_some((size.saturating_sub(n), size.saturating_sub(1)))
            } else {
                Some((
                    a.parse::<usize>().ok()?,
                    if b.is_empty() {
                        size.saturating_sub(1)
                    } else {
                        b.parse::<usize>().ok()?.min(size.saturating_sub(1))
                    },
                ))
            }
        });
    if let Some((start, end)) = bounds.filter(|&(a, b)| a <= b && b < size) {
        reply.bytes = reply.bytes[start..=end].to_vec();
        reply.code = 206;
        reply.header("Content-Range", format!("bytes {start}-{end}/{size}"));
    } else {
        reply.bytes.clear();
        reply.code = 416;
        reply.header("Content-Range", format!("bytes */{size}"));
    }
}
fn write_reply(stream: &mut TcpStream, reply: Reply) -> std::io::Result<()> {
    let reason = match reply.code {
        200 => "OK",
        206 => "Partial Content",
        303 => "See Other",
        400 => "Bad Request",
        403 => "Forbidden",
        404 => "Not Found",
        405 => "Method Not Allowed",
        416 => "Range Not Satisfiable",
        _ => "Internal Server Error",
    };
    write!(
        stream,
        "HTTP/1.1 {} {reason}\r\nContent-Type: {}\r\nContent-Length: {}\r\nConnection: close\r\nCache-Control: no-store\r\nReferrer-Policy: no-referrer\r\nX-Content-Type-Options: nosniff\r\nContent-Security-Policy: default-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'\r\n",
        reply.code,
        reply.mime,
        reply.bytes.len()
    )?;
    for (key, value) in reply.headers {
        write!(stream, "{key}: {value}\r\n")?;
    }
    stream.write_all(b"\r\n")?;
    stream.write_all(&reply.bytes)
}
fn asset(path: &str) -> Option<(&'static str, &'static [u8])> {
    macro_rules! assets { ($($name:literal),+) => { match path { "/" => Some(("text/html; charset=utf-8", include_bytes!("ui/index.html").as_slice())), $(concat!("/", $name) => Some((if $name.ends_with(".css") { "text/css; charset=utf-8" } else { "text/javascript; charset=utf-8" }, include_bytes!(concat!("ui/", $name)).as_slice())),)+ _ => None } }; }
    assets!(
        "ui.css",
        "ui-gallery.css",
        "ui.js",
        "ui-icons.js",
        "ui-components.js",
        "ui-parts.js",
        "ui-shell.js",
        "ui-goals.js",
        "ui-gallery.js",
        "ui-gallery-media.js",
        "ui-gallery-actions.js",
        "ui-gallery-inspect.js"
    )
}
