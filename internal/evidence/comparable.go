package evidence

// Cross-invocation comparability lives here: which recorded conditions two
// family members must share before one verdict may cover both. Reading
// outputs, selectors and the verdict itself do not.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"whosaidso/internal/model"
)

// comparable checks that the family measured the same thing under the same
// conditions. Two runs whose effective configuration differs are two different
// measurements, and reporting one result for them would hide which one it came
// from. Values are compared, not the requested configuration, because what was
// asked for is not what ran.
//
// Conditions compared: the project, the exact instrument revision, the machine
// (a different machine is a different condition), the executed
// source (equal non-empty source pins, or a known equal git HEAD with both
// checkouts known clean), effective configuration, observed conditions, and the
// visual trust envelope whenever either run observed one or produced a picture.
// BLIND TO: source read from outside the project root when only git state
// establishes the source, and visual limits, which say what a frame cannot show
// rather than how it was taken.
func comparable(members []Observation) string {
	for i := 1; i < len(members); i++ {
		a, b := members[0], members[i]
		pair := string(a.InvocationRef.InvocationID) + " and " + string(b.InvocationRef.InvocationID)
		for _, why := range []string{
			executionDifference(a, b),
			mapDifference("effective configuration", a.ConfigEffective, b.ConfigEffective),
			mapDifference("observed conditions", a.ConditionsObserved, b.ConditionsObserved),
			visualDifference(a, b),
		} {
			if why != "" {
				return pair + " are not comparable: " + why
			}
		}
	}
	return ""
}

// executionDifference compares who and what ran. Like every other condition,
// two unknown machines never establish that the runs shared one.
func executionDifference(a, b Observation) string {
	if a.Execution.Project != b.Execution.Project {
		return "they executed in different projects"
	}
	if a.Instrument != b.Instrument {
		return fmt.Sprintf("they ran different instrument revisions (%s revision %d and %s revision %d)",
			a.Instrument.RecordID, a.Instrument.Revision, b.Instrument.RecordID, b.Instrument.Revision)
	}
	ma, mb := a.Execution.MachineID, b.Execution.MachineID
	switch {
	case ma.State != model.Known && mb.State != model.Known:
		return "neither run recorded its machine"
	case ma.State != model.Known || mb.State != model.Known || ma.Value == nil || mb.Value == nil:
		return "one run recorded its machine and the other did not"
	case *ma.Value != *mb.Value:
		return "they ran on different machines (" + string(*ma.Value) + " and " + string(*mb.Value) + ")"
	}
	if !reflect.DeepEqual(pinKeys(a.Execution.SourceRefs), pinKeys(b.Execution.SourceRefs)) {
		return "they declare different source pins"
	}
	return sourceDifference(a.Execution, b.Execution)
}

// sourceDifference requires the executed source to be ESTABLISHED equal:
// equal source pins, or a known equal git HEAD with both checkouts known clean. Two empty pin lists pin
// nothing, an unknown HEAD or dirty state is not evidence of sameness, and a
// dirty checkout differs from HEAD by bytes the ledger never saw.
func sourceDifference(a, b model.ExecutionIdentity) string {
	if len(a.SourceRefs) > 0 {
		return "" // executionDifference already found the pins equal by identity
	}
	ha, hb := a.Head, b.Head
	switch {
	case ha.State != model.Known || hb.State != model.Known || ha.Value == nil || hb.Value == nil:
		return "their source is not established equal: a git HEAD was not recorded and no source pins were declared"
	case *ha.Value != *hb.Value:
		return "they executed different source (git HEAD " + ha.Value.Commit + " and " + hb.Value.Commit + ")"
	}
	clean := func(d model.Availability[bool]) bool { return d.State == model.Known && d.Value != nil && !*d.Value }
	if !clean(a.Dirty) || !clean(b.Dirty) {
		return "their source is not established equal: a checkout was dirty or its state unknown, and no source pins were declared"
	}
	return ""
}

// pinKeys names what each pin identifies, not where a copy was found: two runs
// pinning the same bytes from their own directories pin the same thing.
func pinKeys(refs []model.ArtifactRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, pinKey(ref))
	}
	sort.Strings(out)
	return out
}

func pinKey(ref model.ArtifactRef) string {
	key := ref.Kind + "|" + ref.Selector.Kind + "|" + ref.Selector.Pointer
	if ref.Git != nil {
		key += "|git:" + ref.Git.ObjectFormat + ":" + ref.Git.Commit + ":" + ref.Git.Path
	}
	if ref.Content != nil {
		key += fmt.Sprintf("|content:%s:%d:%s", ref.Content.SHA256, ref.Content.Length, ref.Content.MediaType)
	}
	return key
}

// visualDifference applies the config_effective rule to pixels (visual
// evidence is the same thing): a run that observed a frame, or made a
// picture, is compared on what was true of the frame, and unknown never matches.
func visualDifference(a, b Observation) string {
	pictured := a.Visual.State == model.Known || b.Visual.State == model.Known || a.ImageOutput || b.ImageOutput
	if !pictured {
		return ""
	}
	va, vb := a.Visual, b.Visual
	switch {
	case va.State != model.Known && vb.State != model.Known:
		return "a run produced a picture and neither run observed its visual state"
	case va.State != model.Known || vb.State != model.Known || va.Value == nil || vb.Value == nil:
		return "one run observed its visual state and the other did not"
	}
	left, right := reflect.ValueOf(*va.Value), reflect.ValueOf(*vb.Value)
	for i := 0; i < left.NumField(); i++ {
		name := jsonName(left.Type().Field(i))
		if name == "limits" {
			continue
		}
		if why := observedDifference("visual "+name, left.Field(i), right.Field(i)); why != "" {
			return why
		}
	}
	return ""
}

var (
	availabilityStateType = reflect.TypeOf(model.AvailabilityState(""))
	artifactRefType       = reflect.TypeOf(model.ArtifactRef{})
	numberType            = reflect.TypeOf(json.Number(""))
)

// observedDifference walks two observed values of one type. Every availability
// on the way must be KNOWN in both, pins compare by what they identify, numbers
// by value, and everything else exactly.
func observedDifference(label string, a, b reflect.Value) string {
	t := a.Type()
	switch {
	case t.Kind() == reflect.Struct && t.NumField() == 3 && t.Field(0).Type == availabilityStateType:
		sa, sb := a.Field(0).Interface(), b.Field(0).Interface()
		known := func(s any, v reflect.Value) bool { return s == model.Known && !v.Field(1).IsNil() }
		switch ka, kb := known(sa, a), known(sb, b); {
		case !ka && !kb:
			return label + " was not observed in either run"
		case !ka || !kb:
			return label + " was observed in only one run"
		}
		return observedDifference(label, a.Field(1).Elem(), b.Field(1).Elem())
	case t == artifactRefType:
		if pinKey(a.Interface().(model.ArtifactRef)) != pinKey(b.Interface().(model.ArtifactRef)) {
			return label + " pins different bytes in the two runs"
		}
	case t == numberType:
		x, errA := model.DecimalRat(a.Interface().(json.Number))
		y, errB := model.DecimalRat(b.Interface().(json.Number))
		if errA != nil || errB != nil {
			return label + " is not a comparable number"
		}
		if x.Cmp(y) != 0 {
			return label + " differs between the runs"
		}
	case t.Kind() == reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if why := observedDifference(label+"."+jsonName(t.Field(i)), a.Field(i), b.Field(i)); why != "" {
				return why
			}
		}
	case t.Kind() == reflect.Slice:
		if a.Len() != b.Len() {
			return label + " differs between the runs"
		}
		for i := 0; i < a.Len(); i++ {
			if why := observedDifference(fmt.Sprintf("%s[%d]", label, i), a.Index(i), b.Index(i)); why != "" {
				return why
			}
		}
	default:
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			return label + " differs between the runs"
		}
	}
	return ""
}

func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" {
		return f.Name
	}
	return name
}

func mapDifference(label string, a, b model.Availability[map[string]model.Availability[model.Scalar]]) string {
	switch {
	case a.State != model.Known && b.State != model.Known:
		// Neither run recorded it, so sameness is an assumption. It is a cheap
		// assumption to make and an expensive one to be wrong about.
		return "neither run observed its " + label
	case a.State != model.Known || b.State != model.Known:
		return "one run observed its " + label + " and the other did not"
	}
	left, right := *a.Value, *b.Value
	for _, name := range union(left, right) {
		lv, lok := left[name]
		rv, rok := right[name]
		switch {
		case !lok || !rok:
			// cameraPosition in one run and camera_pos in the other is the drift
			// the instrument declares its knob names to prevent: it makes two
			// comparable runs look different, or hides a real difference.
			return label + " names " + quote(name) + " in only one run"
		case lv.State != rv.State:
			return label + " for " + quote(name) + " was observed in only one run"
		case lv.State != model.Known:
			// Like model.SameActor, two unknowns cannot establish equality.
			return label + " for " + quote(name) + " was not observed in either run"
		case lv.State == model.Known:
			same, err := model.CompareScalars(*lv.Value, model.Equal, *rv.Value)
			if err != nil {
				return label + " for " + quote(name) + " is not comparable across the runs: " + err.Error()
			}
			if !same {
				return label + " for " + quote(name) + " differs between the runs"
			}
		}
	}
	return ""
}

func union(a, b map[string]model.Availability[model.Scalar]) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]model.Availability[model.Scalar]{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	// Sorted so a refusal names the same knob every time it runs.
	sort.Strings(out)
	return out
}
