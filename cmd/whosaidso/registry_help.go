// Help dispatch and registry lookup belong here; verb definitions do not.
package main

import (
	"flag"
	"sort"
	"strings"
)

func helpVerb(fs *flag.FlagSet) func(*call) error {
	return func(c *call) error {
		text, ok := helpFor(strings.Join(c.args, " "))
		if !ok || c.argv != nil {
			return usageError("whosaidso help: no topic or verb %q; run whosaidso help", strings.Join(c.args, " "))
		}
		_, err := c.stdout.Write([]byte(text))
		return err
	}
}

// lookup finds the verb args name, a two-word one first, and returns the
// arguments after its name.
func lookup(args []string) (*verb, []string) {
	for words := 2; words >= 1; words-- {
		if len(args) < words {
			continue
		}
		name := strings.Join(args[:words], " ")
		for i := range registry {
			if registry[i].name == name {
				return &registry[i], args[words:]
			}
		}
	}
	return nil, nil
}

// groupModes lists the modes of a two-word verb group such as check.
func groupModes(group string) []string {
	var modes []string
	for _, v := range registry {
		if g, mode, ok := strings.Cut(v.name, " "); ok && g == group {
			modes = append(modes, mode)
		}
	}
	sort.Strings(modes)
	return modes
}
