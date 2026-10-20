package contracts

import "testing"

func TestReadOnlyDescribesEffectsNotDisclosureOrApproval(t *testing.T) {
	c := AssignmentMetadata()
	c.DataClass, c.ExecutionRule = SensitiveData, DisabledExecution
	if !c.IsReadOnly() {
		t.Fatal("a disabled sensitive read is still semantically read-only")
	}
	c.Operation, c.Reversibility = Write, Reversible
	if c.IsReadOnly() {
		t.Fatal("a private reversible write is not a read, even with effects=none")
	}
}

func TestSecurityMetadataClonesDoNotFillMissingRequirements(t *testing.T) {
	c := AssignmentMetadata()
	copy := CloneCapability(c)
	copy.Effects[0] = CommunicationEffect
	if c.Effects[0] != NoEffect || AssignmentMetadata().Effects[0] != NoEffect {
		t.Fatal("mutable security metadata escaped catalog")
	}
	c.Credentials = nil
	if CloneCapability(c).Credentials != nil {
		t.Fatal("missing credential requirement became an explicit empty requirement")
	}
	for _, c := range append(PublicCapabilities(), AssignmentMetadata()) {
		if !c.IsReadOnly() || c.Reversibility != NotApplicable || c.Credentials == nil {
			t.Fatal("incomplete MVP catalog", c.ID)
		}
	}
}
