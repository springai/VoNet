package update

import "testing"

func TestDefaultRepositoryTargetsProject(t *testing.T) {
	if DefaultRepository != "springai/VoNet" {
		t.Fatalf("DefaultRepository = %q, want %q", DefaultRepository, "springai/VoNet")
	}
}
