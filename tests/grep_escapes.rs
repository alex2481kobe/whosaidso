mod common;
use common::{process::{bad, cli}, Fixture};

#[test]
fn unsupported_regex_escapes_are_errors_in_classes_too() {
    let f = Fixture::sources(&[("digits.txt", "123\nddd\n\\d\n")]);
    for form in [r"\d", r"[\d]", r"[^\d]", r"[\D]", r"[\w]", r"[\W]", r"[\s]", r"[\S]", r"[\b]", r"[\p]", r"[a[\d]]"] {
        let out = bad(&f.root, &["grep", "--regex", &format!("^{form}+$")], "unsupported");
        assert!(!out.contains("no matches"), "{out}");
    }
    assert!(cli(&f.root, &["grep", "--regex", "^[[:digit:]]+$"]).contains("123"));
    assert!(cli(&f.root, &["grep", "--literal", r"\d"]).contains(r"\d"));
}
