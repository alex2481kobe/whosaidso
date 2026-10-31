use super::process::command;
use serde_json::Value;
use std::{
    io::{BufRead, BufReader, Read, Write},
    net::TcpStream,
    path::Path,
    process::{Child, Stdio},
    time::Duration,
};
pub struct Viewer {
    child: Child,
    pub address: String,
    pub launch: String,
}
impl Viewer {
    pub fn start(root: &Path) -> Self {
        Self::start_at(root, &root.parent().unwrap().join("registry"))
    }
    pub fn start_at(root: &Path, registry: &Path) -> Self {
        let mut child = command(
            root,
            env!("CARGO_BIN_EXE_whosaidso"),
            &["ui", "--port", "0"],
        )
        .env("WHOSAIDSO_HOME", registry)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
        let mut url = String::new();
        BufReader::new(child.stdout.take().unwrap())
            .read_line(&mut url)
            .unwrap();
        if !url.starts_with("http://127.0.0.1:") {
            let out = child.wait_with_output().unwrap();
            panic!("viewer startup: {}", String::from_utf8_lossy(&out.stderr));
        }
        let (address, launch) = url
            .trim()
            .strip_prefix("http://")
            .unwrap()
            .split_once('/')
            .unwrap();
        Self {
            child,
            address: address.into(),
            launch: format!("/{launch}"),
        }
    }
    pub fn get(&self, path: &str, cookie: &str) -> (u16, String, Vec<u8>) {
        self.request("GET", path, cookie)
    }
    pub fn request(&self, method: &str, path: &str, cookie: &str) -> (u16, String, Vec<u8>) {
        let mut stream = TcpStream::connect(&self.address).unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(10)))
            .unwrap();
        write!(stream, "{method} {path} HTTP/1.1\r\nHost: {}\r\nCookie: {cookie}\r\nConnection: close\r\nContent-Length: 0\r\n\r\n", self.address).unwrap();
        let mut bytes = Vec::new();
        stream.read_to_end(&mut bytes).unwrap();
        let split = bytes.windows(4).position(|b| b == b"\r\n\r\n").unwrap();
        let header = String::from_utf8(bytes[..split].to_vec()).unwrap();
        let status = header.split_whitespace().nth(1).unwrap().parse().unwrap();
        (status, header, bytes[split + 4..].to_vec())
    }
    pub fn cookie(&self) -> String {
        let (status, header, _) = self.get(&self.launch, "");
        assert_eq!(status, 303);
        assert!(header.contains("HttpOnly") && header.contains("SameSite=Strict"));
        header
            .lines()
            .find_map(|l| l.strip_prefix("Set-Cookie: "))
            .unwrap()
            .split(';')
            .next()
            .unwrap()
            .into()
    }
    pub fn json(&self, path: &str, cookie: &str) -> Value {
        let (status, _, bytes) = self.get(path, cookie);
        assert_eq!(status, 200, "{}", String::from_utf8_lossy(&bytes));
        serde_json::from_slice(&bytes).unwrap()
    }
}
impl Drop for Viewer {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}
