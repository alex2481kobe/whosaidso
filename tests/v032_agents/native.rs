use super::*;

fn call(f: &Fixture, client: &str, name: &str, args: Value) -> Value {
    hook_value(
        f,
        client,
        json!({"hook_event_name":"PreToolUse","cwd":f.root,
        "session_id":"native-fixture","tool_name":name,"tool_input":args}),
    )
}
fn bind_tool(f: &Fixture) {
    let bin = f.root.join(".git/bound-bin");
    std::fs::create_dir_all(&bin).unwrap();
    std::os::unix::fs::symlink(BIN, bin.join("whosaidso")).unwrap();
}
fn reason(v: &Value) -> &str {
    v["hookSpecificOutput"]["permissionDecisionReason"]
        .as_str()
        .unwrap()
}

#[test]
fn native_searches_offer_quoted_commands_only_in_opted_in_repositories() {
    let f = Fixture::empty();
    assert_eq!(
        call(&f, "claude", "Grep", json!({"pattern":"paywall"})),
        json!({})
    );
    write(&f.root.join(".whosaidso/.keep"), "");
    bind_tool(&f);
    write(&f.root.join("app.rs"), "fn paywall(){}\n");
    for client in ["claude", "codex"] {
        for pattern in ["../*.rs", "*.{ts,tsx}"] {
            assert_eq!(
                call(&f, client, "Glob", json!({"pattern":pattern})),
                json!({})
            );
            assert_eq!(
                call(
                    &f,
                    client,
                    "Grep",
                    json!({"pattern":"paywall","glob":pattern})
                ),
                json!({})
            );
        }
        for name in ["Grep", "grep_files"] {
            let v = call(
                &f,
                client,
                name,
                json!({"pattern":"pay.*wall","output_mode":"content","-i":true,"glob":"*.rs"}),
            );
            assert_eq!(
                reason(&v),
                "Run whosaidso grep --regex -i -g '*.rs' -- 'pay.*wall'."
            );
            assert!(v["hookSpecificOutput"].get("updatedInput").is_none());
            let suggestion = reason(&v)
                .strip_prefix("Run ")
                .unwrap()
                .strip_suffix('.')
                .unwrap();
            assert_eq!(
                success(
                    command(&f.root, "sh", &["-c", suggestion])
                        .output()
                        .unwrap()
                ),
                "app.rs:1:fn paywall(){}\n"
            );
        }
        for name in ["Glob", "glob_files"] {
            let v = call(&f, client, name, json!({"pattern":"**/*.rs"}));
            assert!(reason(&v).contains("ls-files --cached"));
            let suggestion = reason(&v)
                .strip_prefix("Run ")
                .unwrap()
                .strip_suffix('.')
                .unwrap();
            assert_eq!(
                success(
                    command(&f.root, "sh", &["-c", suggestion])
                        .output()
                        .unwrap()
                ),
                "app.rs\n"
            );
            assert!(reason(&v).contains("':(glob)**/*.rs'"));
        }
        for args in [
            json!({"pattern":"x","multiline":true}),
            json!({"pattern":"(?=pay)"}),
            json!({"pattern":"x","unknown_flag":true}),
            json!({"pattern":"x","output_mode":"count"}),
            json!({"pattern":"["}),
            json!({"pattern":5}),
        ] {
            assert_eq!(call(&f, client, "Grep", args), json!({}));
        }
    }
}

#[test]
fn long_source_whole_reads_get_one_nudge_and_bounded_reads_always_pass() {
    let f = Fixture::empty();
    let path = f.root.join("long.rs");
    write(&path, "fn example() {}\n".repeat(201));
    write(&f.root.join(".whosaidso/.keep"), "");
    for (client, name) in [("claude", "Read"), ("codex", "read_file")] {
        let args = json!({"file_path":path});
        let input = json!({"hook_event_name":"PreToolUse","cwd":f.root,"session_id":client,"tool_name":name,"tool_input":args});
        let v = hook_value(&f, client, input.clone());
        assert!(reason(&v).contains("code outline 'long.rs'"));
        assert!(reason(&v).contains("code show '<name>'"));
        assert_eq!(reason(&v).lines().count(), 1);
        assert_eq!(hook_value(&f, client, input), json!({}));
        for args in [
            json!({"file_path":path,"offset":1}),
            json!({"file_path":path,"limit":20}),
        ] {
            assert_eq!(call(&f, client, name, args), json!({}));
        }
    }
    write(&f.root.join("boundary.rs"), "fn boundary(){}\n".repeat(200));
    assert_eq!(
        call(
            &f,
            "claude",
            "Read",
            json!({"file_path":f.root.join("boundary.rs")})
        ),
        json!({})
    );
    for (file, bytes) in [
        ("short.rs", b"fn x() {}\n".as_slice()),
        ("notes.md", b"notes\n"),
        ("picture.rs", b"\0\xff"),
        ("image.png", b"image"),
        ("paper.pdf", b"%PDF"),
    ] {
        write(&f.root.join(file), bytes);
        assert_eq!(
            call(&f, "claude", "Read", json!({"file_path":f.root.join(file)})),
            json!({})
        );
    }
    assert_eq!(
        call(
            &f,
            "claude",
            "Read",
            json!({"file_path":f.root.join("missing.rs")})
        ),
        json!({})
    );
}

#[test]
fn native_file_scope_does_not_expand_to_same_named_nested_files() {
    let f = Fixture::empty();
    write(&f.root.join(".whosaidso/.keep"), "");
    bind_tool(&f);
    write(&f.root.join("app.rs"), "fn paywall(){}\n");
    write(&f.root.join("nested/app.rs"), "fn paywall(){}\n");
    let v = call(
        &f,
        "claude",
        "Grep",
        json!({"pattern":"paywall","path":f.root.join("app.rs"),"output_mode":"content"}),
    );
    let suggestion = reason(&v)
        .strip_prefix("Run ")
        .unwrap()
        .strip_suffix('.')
        .unwrap();
    assert_eq!(
        success(
            command(&f.root, "sh", &["-c", suggestion])
                .output()
                .unwrap()
        ),
        "app.rs:1:fn paywall(){}\n"
    );
}
