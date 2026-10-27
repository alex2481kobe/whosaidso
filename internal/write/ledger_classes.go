package write

// Ledger problem classes, measurement states, and stable identities live here.
// Filesystem measurements and CLI rendering do not.

// LedgerClass separates a measured zero from an unavailable measurement.
// Count is omitted when unmeasured; identities name problems, not diagnostics.
type LedgerClass struct {
	State      string   `json:"state"`
	Count      *int     `json:"count,omitempty"`
	Identities []string `json:"identities,omitempty"`
}

var ledgerClassNames = []string{"set_asides", "git_pins", "replay_error", "artifacts_missing", "not_committed"}

func unmeasuredLedgerClasses() map[string]LedgerClass {
	classes := make(map[string]LedgerClass, len(ledgerClassNames))
	for _, name := range ledgerClassNames {
		classes[name] = LedgerClass{State: "unmeasured"}
	}
	return classes
}

func measuredLedgerClass(identities []string) LedgerClass {
	count := len(identities)
	return LedgerClass{State: "measured", Count: &count, Identities: identities}
}
