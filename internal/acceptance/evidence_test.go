package acceptance_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"whosaidso/internal/evidence"
	"whosaidso/internal/model"
)

func TestEvidenceAcceptedControlResolvesSelectsAndEvaluatesValidArtifacts(t *testing.T) {
	root, commit := evidenceRepo(t, "sha256", "out/result.json", evidenceBody)
	r := evidence.NewResolver(root)
	for _, kind := range []string{"git", "content"} {
		t.Run(kind, func(t *testing.T) {
			ref := evidenceBoth("sha256", commit, evidenceBody)
			ref.Kind = kind
			a, err := r.Resolve(context.Background(), ref)
			if err != nil {
				t.Fatalf("valid agreeing pins must resolve: %v", err)
			}
			if string(a.Bytes) != evidenceBody || a.SHA256 != evidenceDigest(evidenceBody) || a.Length != uint64(len(evidenceBody)) || a.MediaType != "application/json" || !a.Corroborated {
				t.Fatalf("valid pins returned incorrect bytes or provenance: %+v", a)
			}
			reading, err := evidence.Select(a, model.Selector{Kind: "json-pointer", Pointer: "/results"})
			if err != nil || reading.Kind != evidence.ReadingSet || len(reading.Values) != 2 || reading.Values[0].Number == nil || string(*reading.Values[0].Number) != "0.0100" {
				t.Fatalf("valid selection must retain exact numbers: %+v, %v", reading, err)
			}
		})
	}
	c := evidenceCriterion()
	o := evidenceObserve(t, root, c, evidenceBody, 10)
	evidenceVerdict(t, c, []evidence.Observation{o}, evidence.True, "")
}

func TestEvidenceGitPinReadsTheFullCommitInsteadOfEditedStagedOrLaterCommittedBytes(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			root, commit := evidenceRepo(t, format, "out/result.json", evidenceBody)
			ref := evidenceBoth(format, commit, evidenceBody)
			ref.Content = nil
			r := evidence.NewResolver(root)
			check := func() {
				a, err := r.Resolve(context.Background(), ref)
				if err != nil || string(a.Bytes) != evidenceBody || a.Origin != evidence.OriginGit || a.DeclaredPath != "out/result.json" {
					t.Fatalf("pin must keep returning original commit bytes: %+v, %v", a, err)
				}
			}
			check()
			evidenceWrite(t, root, "out/result.json", `{"edited":true}`)
			check()
			evidenceGit(t, root, "add", "out/result.json")
			check()
			evidenceGit(t, root, "commit", "-qm", "later output")
			check()
			evidenceGit(t, root, "mv", "out/result.json", "out/moved.json")
			check()
		})
	}
}

func TestEvidenceGitPinRefusesWrongFormatAbbreviatedCommitNonCommitAndMissingPath(t *testing.T) {
	root, commit := evidenceRepo(t, "sha1", "out/result.json", evidenceBody)
	blob := evidenceGit(t, root, "rev-parse", commit+":out/result.json")
	for _, tc := range []struct {
		name, format, commit, path string
	}{
		{"wrong_object_format", "sha256", strings.Repeat("a", 64), "out/result.json"},
		{"abbreviated_commit", "sha1", commit[:12], "out/result.json"},
		{"blob_is_not_a_commit", "sha1", blob, "out/result.json"},
		{"missing_path", "sha1", commit, "out/missing.json"},
		{"tree_is_not_a_file", "sha1", commit, "out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := evidenceBoth(tc.format, tc.commit, evidenceBody)
			ref.Content = nil
			ref.Git.Path = tc.path
			evidenceRefusal(t, evidence.NewResolver(root), ref)
		})
	}
}

func TestEvidenceContentPinRefusesWrongRawDigestAndLength(t *testing.T) {
	root := t.TempDir()
	evidenceWrite(t, root, "out/result.json", evidenceBody)
	r := evidence.NewResolver(root)
	for _, change := range []string{"digest", "length"} {
		t.Run(change, func(t *testing.T) {
			ref := evidenceContent(evidenceBody)
			if change == "digest" {
				ref.Content.SHA256 = evidenceDigest(strings.Replace(evidenceBody, "0.0100", "0.9900", 1))
			} else {
				ref.Content.Length++
			}
			evidenceRefusal(t, r, ref)
		})
	}
}

func TestEvidenceAgreeingDigestDoesNotHideTwoPinLengthDisagreement(t *testing.T) {
	root, commit := evidenceRepo(t, "sha1", "out/result.json", evidenceBody)
	for _, kind := range []string{"git", "content"} {
		t.Run(kind, func(t *testing.T) {
			ref := evidenceBoth("sha1", commit, evidenceBody)
			ref.Kind = kind
			ref.Content.Length++
			evidenceRefusal(t, evidence.NewResolver(root), ref)
		})
	}
}

func TestEvidenceJSONBytesCannotResolveAsPNGJustBecauseDigestAndLengthAgree(t *testing.T) {
	root, commit := evidenceRepo(t, "sha1", "out/result.json", evidenceBody)
	for _, kind := range []string{"git", "content"} {
		t.Run(kind, func(t *testing.T) {
			ref := evidenceBoth("sha1", commit, evidenceBody)
			ref.Kind = kind
			ref.Content.MediaType = "image/png"
			evidenceRefusal(t, evidence.NewResolver(root), ref)
		})
	}
}

func TestEvidenceGitPrimaryRefusesAnUnreadableCorroboratingContentPin(t *testing.T) {
	root, commit := evidenceRepo(t, "sha1", "out/result.json", evidenceBody)
	ref := evidenceBoth("sha1", commit, evidenceBody)
	ref.Content.Locators = []model.Locator{{Path: "missing/result.json"}}
	for _, kind := range []string{"content", "git"} {
		t.Run(kind, func(t *testing.T) {
			ref.Kind = kind
			evidenceRefusal(t, evidence.NewResolver(root), ref)
		})
	}
}

func TestEvidenceContentPrimaryRefusesAnUnreadableCorroboratingGitPin(t *testing.T) {
	root, commit := evidenceRepo(t, "sha1", "out/result.json", evidenceBody)
	ref := evidenceBoth("sha1", commit, evidenceBody)
	ref.Kind = "content"
	ref.Git.Path = "out/missing.json"
	evidenceRefusal(t, evidence.NewResolver(root), ref)
}

func TestEvidenceArtifactIdentitySurvivesLocatorFallbackAndPinDirection(t *testing.T) {
	root, commit := evidenceRepo(t, "sha256", "out/result.json", evidenceBody)
	ref := evidenceBoth("sha256", commit, evidenceBody)
	digest := evidenceDigest(evidenceBody)
	evidenceWrite(t, root, ".whosaidso/artifacts/"+string(digest), evidenceBody)
	evidenceWrite(t, root, "out/result.json", `{"stale":true}`)
	for _, kind := range []string{"content", "git"} {
		t.Run(kind, func(t *testing.T) {
			ref.Kind = kind
			a, err := evidence.NewResolver(root).Resolve(context.Background(), ref)
			if err != nil || a.SHA256 != digest || string(a.Bytes) != evidenceBody || !a.Corroborated {
				t.Fatalf("fallback and primary pin must preserve the artifact identity: %+v, %v", a, err)
			}
			if kind == "content" && a.Origin != evidence.OriginArtifactStore {
				t.Fatalf("stale locator must fall back to the artifact store, got %s", a.Origin)
			}
			blob := evidenceGit(t, root, "rev-parse", commit+":out/result.json")
			ref.Content.SHA256 = model.Digest(blob)
			evidenceRefusal(t, evidence.NewResolver(root), ref)
			ref.Content.SHA256 = digest
		})
	}
}

func TestEvidenceLFSPointerBytesAreRefusedThroughEitherPinKind(t *testing.T) {
	pointer := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", evidenceDigest(evidenceBody), len(evidenceBody))
	root, commit := evidenceRepo(t, "sha1", "out/result.json", pointer)
	for _, kind := range []string{"git", "content"} {
		t.Run(kind, func(t *testing.T) {
			ref := evidenceBoth("sha1", commit, pointer)
			ref.Kind = kind
			if kind == "git" {
				ref.Content = nil
			} else {
				ref.Git = nil
				ref.Content.MediaType = "application/octet-stream"
			}
			evidenceRefusal(t, evidence.NewResolver(root), ref)
		})
	}
}

func TestEvidenceLFSPayloadResolvesOnlyWhenItsOwnBytesCanBeRead(t *testing.T) {
	pointer := fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", evidenceDigest(evidenceBody), len(evidenceBody))
	root, commit := evidenceRepo(t, "sha1", "out/result.json", pointer)
	ref := evidenceBoth("sha1", commit, evidenceBody)
	evidenceRefusal(t, evidence.NewResolver(root), ref)
	evidenceWrite(t, root, ".whosaidso/artifacts/"+string(ref.Content.SHA256), evidenceBody)
	for _, kind := range []string{"git", "content"} {
		t.Run(kind, func(t *testing.T) {
			ref.Kind = kind
			a, err := evidence.NewResolver(root).Resolve(context.Background(), ref)
			if err != nil || string(a.Bytes) != evidenceBody || !a.LFSPointer || !a.Corroborated {
				t.Fatalf("available LFS payload must resolve as the measured bytes: %+v, %v", a, err)
			}
		})
	}
}

func TestEvidenceExactJSONNumbersSurviveEncodingSelectionAndCriterionComparison(t *testing.T) {
	for _, tc := range []struct {
		name, token, target string
		op                  model.ComparisonOperator
		want                evidence.Verdict
	}{
		{"adjacent_large_integers_stay_distinct", "9007199254740993", "9007199254740992", model.Greater, evidence.True},
		{"adjacent_large_integers_are_not_equal", "9007199254740993", "9007199254740992", model.Equal, evidence.False},
		{"trailing_zero_spelling_is_preserved", "0.0100", "0.01", model.Equal, evidence.True},
		{"equivalent_exponent_spelling_compares_equal", "1.00e-2", "0.01", model.Equal, evidence.True},
		{"tiny_nonzero_does_not_underflow_to_zero", "1e-400", "0", model.Greater, evidence.True},
		{"huge_exponents_stay_ordered", "1e400", "9e399", model.Greater, evidence.True},
		{"negative_zero_compares_equal_to_zero", "-0.00", "0", model.Equal, evidence.True},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"results":    map[string]any{"unit": "mm", "population": "pose sweep", "denominator": "poses", "values": []json.Number{json.Number(tc.token)}},
				"population": map[string]any{"population": "pose sweep", "denominator": "poses", "values": []string{"pose-a"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			c := evidenceCriterion()
			c.Expression.Operator, c.Expression.Target = tc.op, evidenceNumber(tc.target)
			o := evidenceObserve(t, t.TempDir(), c, string(body), 10)
			if len(o.Result.Values) != 1 || o.Result.Values[0].Number == nil || string(*o.Result.Values[0].Number) != tc.token {
				t.Fatalf("selector changed exact JSON number %s into %+v", tc.token, o.Result.Values)
			}
			evidenceVerdict(t, c, []evidence.Observation{o}, tc.want, "")
		})
	}
}

func TestEvidenceMissingPointerAndJSONNullStayAbsentWhileZeroIsAReading(t *testing.T) {
	root := t.TempDir()
	body := `{"results":{"unit":"mm","population":"pose sweep","denominator":"poses","zero":0,"null":null,"values":[null]}}`
	evidenceWrite(t, root, "out/result.json", body)
	a, err := evidence.NewResolver(root).Resolve(context.Background(), evidenceContent(body))
	if err != nil {
		t.Fatal(err)
	}
	zero, err := evidence.Select(a, model.Selector{Kind: "json-pointer", Pointer: "/results/zero"})
	if err != nil || zero.Kind != evidence.ReadingScalar || zero.Scalar.Number == nil || *zero.Scalar.Number != "0" {
		t.Fatalf("actual zero must remain a present scalar: %+v, %v", zero, err)
	}
	reasons := map[string]string{}
	for _, pointer := range []string{"/results/missing", "/results/null", "/results/values", "/results/values/7"} {
		t.Run(pointer, func(t *testing.T) {
			got, err := evidence.Select(a, model.Selector{Kind: "json-pointer", Pointer: pointer})
			_, present := got.Scalars()
			if err != nil || got.Kind != evidence.ReadingAbsent || strings.TrimSpace(got.Reason) == "" || present || got.Scalar.Number != nil {
				t.Fatalf("missing or null value must be absent with a reason, never zero: %+v, %v", got, err)
			}
			reasons[pointer] = got.Reason
		})
	}
	if reasons["/results/missing"] == reasons["/results/null"] {
		t.Fatal("missing pointer and present null must explain their different absence")
	}
}

func TestEvidenceJSONPointerEscapesKeepDistinctKeysDistinctAndRejectDuplicateDecodedKeys(t *testing.T) {
	root := t.TempDir()
	body := `{"a/b":1,"a~1b":2,"list":[3]}`
	evidenceWrite(t, root, "out/result.json", body)
	a, err := evidence.NewResolver(root).Resolve(context.Background(), evidenceContent(body))
	if err != nil {
		t.Fatal(err)
	}
	for pointer, want := range map[string]string{"/a~1b": "1", "/a~01b": "2", "/list/0": "3"} {
		got, err := evidence.Select(a, model.Selector{Kind: "json-pointer", Pointer: pointer})
		if err != nil || got.Scalar.Number == nil || string(*got.Scalar.Number) != want {
			t.Fatalf("pointer %s must select %s: %+v, %v", pointer, want, got, err)
		}
	}
	for _, body := range []string{`{"value":1,"value":2}`, `{"value":1,"\u0076alue":2}`} {
		evidenceWrite(t, root, "out/result.json", body)
		a, err := evidence.NewResolver(root).Resolve(context.Background(), evidenceContent(body))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := evidence.Select(a, model.Selector{Kind: "json-pointer", Pointer: "/value"}); err == nil {
			t.Fatalf("one decoded pointer cannot silently select one of two values: %+v", got)
		}
	}
}

func TestEvidenceObservationUsesRunOutputAndFrozenPointerInsteadOfTheCriterionExample(t *testing.T) {
	c := evidenceCriterion()
	body := strings.Replace(evidenceBody, "0.0100", "0.9900", 1)
	o := evidenceObserve(t, t.TempDir(), c, body, 10)
	if o.Result.Artifact != evidenceDigest(body) || o.Result.Artifact == c.Expression.ResultSelector.Content.SHA256 {
		t.Fatalf("observation must identify the invocation output instead of the frozen example: %+v", o.Result)
	}
	evidenceVerdict(t, c, []evidence.Observation{o}, evidence.False, "does not satisfy")
}

func TestEvidenceUnreadableOutputIsAbsentWithAReasonInsteadOfUsingTheCriterionExample(t *testing.T) {
	c := evidenceCriterion()
	root := t.TempDir()
	// The criterion example exists, but the run pinned different, missing bytes.
	evidenceWrite(t, root, "out/result.json", evidenceBody)
	body := strings.Replace(evidenceBody, "0.0100", "0.9900", 1)
	env := evidenceEnvelope(c, body, 10)
	o, err := evidence.NewResolver(root).Observe(context.Background(), c, env)
	if err != nil || strings.TrimSpace(o.Unavailable) == "" {
		t.Fatalf("missing run artifact must explain unavailability: %+v, %v", o, err)
	}
	if _, present := o.Result.Scalars(); present {
		t.Fatalf("missing run artifact invented a reading: %+v", o.Result)
	}
	evidenceVerdict(t, c, []evidence.Observation{o}, evidence.Unknown, "no locator holds the pinned bytes")
}

func TestEvidenceTwoOutputsAtOneDeclaredPathAreRefusedRegardlessOfTheirOrder(t *testing.T) {
	c := evidenceCriterion()
	root := t.TempDir()
	evidenceWrite(t, root, evidenceStored(evidenceBody), evidenceBody)
	other := evidenceRunOutput(strings.Replace(evidenceBody, "0.0100", "0.9900", 1))
	for _, outputs := range [][]model.RunOutput{{evidenceRunOutput(evidenceBody), other}, {other, evidenceRunOutput(evidenceBody)}} {
		env := evidenceEnvelope(c, evidenceBody, 10)
		env.Outputs = evidenceKnown(outputs)
		o, err := evidence.NewResolver(root).Observe(context.Background(), c, env)
		if err != nil || !strings.Contains(o.Unavailable, "2 outputs") {
			t.Fatalf("ambiguous path must refuse both output orders: %+v, %v", o, err)
		}
		evidenceVerdict(t, c, []evidence.Observation{o}, evidence.Unknown, "2 outputs")
	}
}

func TestEvidenceCriterionRefusesMismatchedResultUnitPopulationAndDenominator(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement, reason string }{
		{"unit", `"unit":"mm"`, `"unit":"cm"`, "unit mismatch"},
		{"population", `"population":"pose sweep"`, `"population":"other sweep"`, "population mismatch"},
		{"denominator", `"denominator":"poses"`, `"denominator":"frames"`, "denominator mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := evidenceCriterion()
			body := strings.Replace(evidenceBody, tc.old, tc.replacement, 1)
			o := evidenceObserve(t, t.TempDir(), c, body, 10)
			evidenceVerdict(t, c, []evidence.Observation{o}, evidence.Unknown, tc.reason)
		})
	}
}

func TestEvidenceCriterionRefusesMismatchedMetadataOnTheSelectedPopulation(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement string }{
		{"population", `"population":{"population":"pose sweep"`, `"population":{"population":"other sweep"`},
		{"denominator", `"denominator":"poses","values":["pose-a"`, `"denominator":"frames","values":["pose-a"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := evidenceCriterion()
			body := strings.Replace(evidenceBody, tc.old, tc.replacement, 1)
			if body == evidenceBody {
				t.Fatal("fixture must change the population metadata")
			}
			o := evidenceObserve(t, t.TempDir(), c, body, 10)
			evidenceVerdict(t, c, []evidence.Observation{o}, evidence.Unknown, tc.name)
		})
	}
}

func TestEvidenceCriterionRefusesMemberMetadataThatDisagreesWithTheSetMetadata(t *testing.T) {
	c := evidenceCriterion()
	control := strings.Replace(evidenceBody, "[0.0100,0.0200]", `[{"value":0.0100,"unit":"mm","population":"pose sweep","denominator":"poses"},0.0200]`, 1)
	o := evidenceObserve(t, t.TempDir(), c, control, 10)
	evidenceVerdict(t, c, []evidence.Observation{o}, evidence.True, "")
	for _, member := range []string{
		`{"value":0.0100,"unit":"cm"}`,
		`{"value":0.0100,"population":"other sweep"}`,
		`{"value":0.0100,"denominator":"frames"}`,
	} {
		t.Run(member, func(t *testing.T) {
			c := evidenceCriterion()
			body := strings.Replace(evidenceBody, "[0.0100,0.0200]", "["+member+",0.0200]", 1)
			o := evidenceObserve(t, t.TempDir(), c, body, 10)
			evidenceVerdict(t, c, []evidence.Observation{o}, evidence.Unknown, "")
		})
	}
}

func TestEvidenceAllAndAnyRefuseResultsCoveringOnlyPartOfTheDeclaredPopulation(t *testing.T) {
	for _, reducer := range []model.CriterionReducer{model.All, model.Any} {
		t.Run(string(reducer), func(t *testing.T) {
			c := evidenceCriterion()
			c.Expression.Reducer = reducer
			body := strings.Replace(evidenceBody, "[0.0100,0.0200]", "[0.0100]", 1)
			o := evidenceObserve(t, t.TempDir(), c, body, 10)
			evidenceVerdict(t, c, []evidence.Observation{o}, evidence.Unknown, "covers 1 of the 2")
		})
	}
}

func TestEvidenceCriterionRefusesEffectiveConfigurationDriftMissingRecordsAndRenamedSettings(t *testing.T) {
	c := evidenceCriterion()
	root := t.TempDir()
	a := evidenceObserve(t, root, c, evidenceBody, 10)
	b := evidenceObserve(t, root, c, evidenceBody, 11)
	evidenceVerdict(t, c, []evidence.Observation{a, b}, evidence.True, "")
	for _, tc := range []struct {
		name   string
		config model.Availability[map[string]model.Availability[model.Scalar]]
	}{
		{"different_value", evidenceConfig("sample_count", "13")},
		{"setting_spelled_differently", evidenceConfig("sampleCount", "12")},
		{"one_run_did_not_record_configuration", evidenceUnknown[map[string]model.Availability[model.Scalar]]("effective settings were not observed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := b
			other.ConfigEffective = tc.config
			forward := evidenceVerdict(t, c, []evidence.Observation{a, other}, evidence.Unknown, "effective configuration")
			backward := evidenceVerdict(t, c, []evidence.Observation{other, a}, evidence.Unknown, "effective configuration")
			if !reflect.DeepEqual(forward, backward) {
				t.Fatal("reordering the same family changed the refusal")
			}
		})
	}
	// Different decimal spellings of one effective value must stay comparable.
	b.ConfigEffective = evidenceConfig("sample_count", "1.200e1")
	evidenceVerdict(t, c, []evidence.Observation{a, b}, evidence.True, "")
}

func TestEvidenceTwoUnknownEffectiveSettingValuesDoNotEstablishEqualConfiguration(t *testing.T) {
	c := evidenceCriterion()
	root := t.TempDir()
	a := evidenceObserve(t, root, c, evidenceBody, 10)
	b := evidenceObserve(t, root, c, evidenceBody, 11)
	a.ConfigEffective = evidenceKnown(map[string]model.Availability[model.Scalar]{"sample_count": evidenceUnknown[model.Scalar]("first runner could not observe it")})
	b.ConfigEffective = evidenceKnown(map[string]model.Availability[model.Scalar]{"sample_count": evidenceUnknown[model.Scalar]("second runner could not observe it")})
	evidenceVerdict(t, c, []evidence.Observation{a, b}, evidence.Unknown, "")
}

func TestEvidenceEmptyFamilyIsUnknownWithAReasonEvenWhenEmptyPopulationMeansTrue(t *testing.T) {
	c := evidenceCriterion()
	yes := true
	c.Expression.EmptyResult = &yes
	for _, family := range [][]evidence.Observation{nil, {}} {
		got := evidenceVerdict(t, c, family, evidence.Unknown, "no local observation")
		if len(got.Members) != 0 {
			t.Fatalf("empty family invented %d observations", len(got.Members))
		}
	}
}

func TestEvidenceSameInvocationCannotBeCountedTwiceAndRetryCannotEraseCounterexample(t *testing.T) {
	c := evidenceCriterion()
	root := t.TempDir()
	a := evidenceObserve(t, root, c, evidenceBody, 10)
	if _, err := evidence.Evaluate(c, []evidence.Observation{a, a}); err == nil {
		t.Fatal("the same invocation identity was counted twice")
	}
	fail := evidenceObserve(t, root, c, strings.Replace(evidenceBody, "0.0100", "0.9900", 1), 11)
	for _, family := range [][]evidence.Observation{{a, fail}, {fail, a}} {
		got := evidenceVerdict(t, c, family, evidence.False, "does not satisfy")
		if len(got.Members) != 2 {
			t.Fatal("family must retain the passing run and the counterexample")
		}
	}
}

const evidenceBody = `{"results":{"unit":"mm","population":"pose sweep","denominator":"poses","values":[0.0100,0.0200]},"population":{"population":"pose sweep","denominator":"poses","values":["pose-a","pose-b"]}}`

func evidenceDigest(body string) model.Digest {
	sum := sha256.Sum256([]byte(body))
	return model.Digest(hex.EncodeToString(sum[:]))
}

func evidenceContent(body string) model.ArtifactRef {
	return model.ArtifactRef{Kind: "content", Content: &model.ContentPin{
		SHA256: evidenceDigest(body), Length: uint64(len(body)), MediaType: "application/json",
		Locators: []model.Locator{{Path: "out/result.json"}},
	}, Selector: model.Selector{Kind: "whole"}}
}

// evidenceStored is where admission keeps an output's bytes: the store,
// by digest (R9: a run output is a name inside its run plus a content pin).
func evidenceStored(body string) string {
	return evidence.DefaultArtifactDir + "/" + string(evidenceDigest(body))
}

// evidenceRunOutput is the run's output out/result.json holding body.
func evidenceRunOutput(body string) model.RunOutput {
	return model.RunOutput{Name: "out/result.json", SHA256: evidenceDigest(body), Length: uint64(len(body)), MediaType: "application/json"}
}

// evidenceMachine is a fixed, KNOWN machine id shared by runs meant to be
// comparable (unknown machine ids never match).
const evidenceMachine model.ID = "7ZZZZZZZZZZZZZZZZZZZZZZZZZ"

func evidenceBoth(format, commit, body string) model.ArtifactRef {
	ref := evidenceContent(body)
	ref.Kind = "git"
	ref.Git = &model.GitPin{ObjectFormat: format, Commit: commit, Path: "out/result.json"}
	return ref
}

func evidenceWrite(t *testing.T, root, path, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func evidenceGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git %v: %v, %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func evidenceRepo(t *testing.T, format, path, body string) (string, string) {
	t.Helper()
	root := t.TempDir()
	evidenceGit(t, root, "init", "-q", "--object-format="+format)
	evidenceGit(t, root, "config", "user.name", "Evidence Fixture")
	evidenceGit(t, root, "config", "user.email", "fixture@example.invalid")
	evidenceWrite(t, root, path, body)
	evidenceGit(t, root, "add", "--", path)
	evidenceGit(t, root, "commit", "-qm", "pinned fixture")
	return root, evidenceGit(t, root, "rev-parse", "HEAD")
}

func evidenceRefusal(t *testing.T, r *evidence.Resolver, ref model.ArtifactRef) {
	t.Helper()
	got, err := r.Resolve(context.Background(), ref)
	if err == nil {
		t.Fatalf("must refuse this pin, instead accepted kind=%s digest=%s length=%d media_type=%q corroborated=%t origin=%s", ref.Kind, got.SHA256, got.Length, got.MediaType, got.Corroborated, got.Origin)
	}
	if strings.TrimSpace(err.Error()) == "" {
		t.Fatal("refusal must explain why the pin cannot resolve")
	}
}

func evidenceKnown[T any](value T) model.Availability[T] {
	return model.Availability[T]{State: model.Known, Value: &value}
}

func evidenceUnknown[T any](reason string) model.Availability[T] {
	return model.Availability[T]{State: model.Unknown, Reason: reason}
}

func evidenceNumber(token string) model.Scalar {
	n := json.Number(token)
	return model.Scalar{Type: "number", Number: &n}
}

func evidenceConfig(key, value string) model.Availability[map[string]model.Availability[model.Scalar]] {
	return evidenceKnown(map[string]model.Availability[model.Scalar]{key: evidenceKnown(evidenceNumber(value))})
}

func evidenceCriterion() model.CriterionFix {
	result := evidenceContent(evidenceBody)
	result.Selector = model.Selector{Kind: "json-pointer", Pointer: "/results"}
	population := evidenceContent(evidenceBody)
	population.Selector = model.Selector{Kind: "json-pointer", Pointer: "/population"}
	return model.CriterionFix{
		Claim:       model.RecordRef{Project: "datum/lane-e-evidence", RecordID: "00000000000000000000000001", Revision: 1},
		CriterionID: "00000000000000000000000002", Revision: 1,
		Expression: model.CriterionExpression{
			ResultSelector: result, Unit: "mm",
			Population: model.Population{Identity: "pose sweep", Selector: population, Denominator: "poses"},
			Operator:   model.Less, Target: evidenceNumber("0.05"), Reducer: model.All,
		},
		Policy: model.EvaluationPolicy{Inclusion: "entire-criterion-family", Retry: "retain-all"},
		Author: model.Actor{ID: "lane-e"}, SourceRefs: []model.ArtifactRef{},
	}
}

func evidenceObserve(t *testing.T, root string, c model.CriterionFix, body string, id int) evidence.Observation {
	t.Helper()
	evidenceWrite(t, root, evidenceStored(body), body)
	env := evidenceEnvelope(c, body, id)
	if err := model.ValidateSchema(c); err != nil {
		t.Fatalf("criterion fixture must be valid: %v", err)
	}
	if err := model.ValidateSchema(env); err != nil {
		t.Fatalf("invocation fixture must be valid: %v", err)
	}
	o, err := evidence.NewResolver(root).Observe(context.Background(), c, env)
	if err != nil {
		t.Fatalf("observation must read the fixture: %v", err)
	}
	return o
}

func evidenceEnvelope(c model.CriterionFix, body string, id int) model.InvocationEnvelope {
	output := evidenceRunOutput(body)
	exit := 0
	when := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	return model.InvocationEnvelope{
		InvocationID: model.ID(fmt.Sprintf("%026d", id)), AttemptID: "00000000000000000000000003",
		InstrumentRef: model.RecordRef{Project: c.Claim.Project, RecordID: "00000000000000000000000004", Revision: 1},
		CriterionRef:  evidenceKnown(model.CriterionRef{Claim: c.Claim, CriterionID: c.CriterionID, Revision: c.Revision}),
		ExecutionSourceIdentity: model.ExecutionIdentity{
			Project: c.Claim.Project, MachineID: evidenceKnown(evidenceMachine),
			// Coordinator decision 2026-09-23: only a known equal HEAD with clean checkouts (or equal pins) establishes equal source.
			SourceRefs: []model.ArtifactRef{}, Head: evidenceKnown(model.GitHead{ObjectFormat: "sha1", Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}), Dirty: evidenceKnown(false),
		},
		Argv: []string{"fixture-measurement"}, InputRefs: []model.ArtifactRef{}, ConfigRequested: map[string]model.Scalar{},
		ConfigEffective: evidenceConfig("sample_count", "12"), ConditionsDeclared: map[string]model.Scalar{},
		ConditionsObserved: evidenceConfig("seed", "7"), Isolation: evidenceUnknown[model.Isolation]("not captured"),
		StartedAt: when, ObservedAt: evidenceKnown(when), Outcome: evidenceKnown(model.ProcessOutcome{Kind: "exit", ExitCode: &exit}),
		Outputs: evidenceKnown([]model.RunOutput{output}), Visual: evidenceUnknown[model.VisualObservation]("numeric instrument"),
	}
}

func evidenceVerdict(t *testing.T, c model.CriterionFix, family []evidence.Observation, want evidence.Verdict, reason string) evidence.Evaluation {
	t.Helper()
	got, err := evidence.Evaluate(c, family)
	if err != nil {
		t.Fatalf("valid criterion family returned an error: %v", err)
	}
	if got.Verdict != want || !strings.Contains(got.Reason, reason) || (want == evidence.Unknown && strings.TrimSpace(got.Reason) == "") {
		t.Fatalf("want %s with reason containing %q, got %s with reason %q and members %+v", want, reason, got.Verdict, got.Reason, got.Members)
	}
	return got
}
