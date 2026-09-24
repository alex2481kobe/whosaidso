package model

// Accepter shapes: a task may name a known accepter or none, never an unknown
// one; a task.close may omit its authority, and one it cites is still whole.

import "testing"

func TestTaskAccepterAndOptionalCloseAuthority(t *testing.T) {
	task := &TaskCreate{ID: schemaID(1), Spec: schemaTask(), Provenance: schemaProvenance()}
	requireSchemaGood(t, task)
	task.Spec.Accepter = &Actor{ID: "owner"}
	requireSchemaGood(t, task)
	task.Spec.Accepter = &Actor{UnknownReason: "nobody said"}
	if err := ValidateSchema(task); err == nil {
		t.Fatal("an unknown accepter could never close the task and must be refused")
	}
	closure := &TaskClose{Task: schemaRef(1), Outcome: ClosureSuccess, AcceptanceWitnessRefs: []AcceptanceWitness{{CriterionID: schemaID(3), CriterionRevision: 1, WitnessRef: schemaArtifact()}}, DeliveryWitnessRefs: []ArtifactRef{schemaArtifact()}}
	requireSchemaGood(t, closure)
	closure.Authority = &Authority{}
	if err := ValidateSchema(closure); err == nil {
		t.Fatal("a cited authority is still checked whole")
	}
}
