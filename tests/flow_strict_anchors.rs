mod common;
use common::{flow::FlowFixture, process::*, *};
use serde_json::json;

#[test]
fn deleting_a_function_cannot_rebind_its_name_to_a_module() {
    let f = Fixture::sources(&[("app.rs", "mod target {}\nfn target() {}\n")]);
    f.write(
        "app",
        &json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"target","type":"action","paths":["app.rs"],"symbols":["target"],"status":"built"}
    ],"steps":[]}),
    );
    cli(&f.root, &["flow", "drift-check"]);
    write(&f.root.join("app.rs"), "mod target {}\n");
    bad(&f.root, &["flow", "drift-check"], "drifted");
}

fn struct_anchor() -> Fixture {
    let f = Fixture::sources(&[("app.rs", "pub struct Target;\n")]);
    f.write(
        "app",
        &json!({"covers":{"paths":["app.rs"]},"nodes":[
        {"id":"target","type":"function","paths":["app.rs"],"symbols":["Target"],"status":"built"}
    ],"steps":[]}),
    );
    f
}

#[test]
fn a_function_node_cannot_bind_to_a_syntax_struct() {
    let f = struct_anchor();
    bad(&f.root, &["flow", "drift-check"], "drifted");
}

#[test]
fn a_function_node_cannot_bind_to_a_precise_struct() {
    let f = struct_anchor();
    scip::import(
        &f.root,
        &[scip::doc(
            "app.rs",
            "scip fixture app 1 Target#",
            "Target",
            &[(&[0, 11, 17], true)],
        )],
    );
    bad(&f.root, &["flow", "drift-check"], "drifted");
}
