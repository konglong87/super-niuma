package agentprofile

import (
	"sort"
	"sync"
	"testing"
)

func TestRoleTypeRegistryProvidesIndependentDescriptors(t *testing.T) {
	for _, key := range []string{RoleTypePlanner, RoleTypeResearcher, RoleTypeCoder, RoleTypeReviewer, RoleTypeExecutor, RoleTypeGeneral} {
		descriptor, exists := LookupRoleType(key)
		if !exists || descriptor.Key != key || descriptor.DisplayName == "" || descriptor.Description == "" {
			t.Fatalf("missing builtin role descriptor %q: %+v", key, descriptor)
		}
	}
	descriptors := RoleTypeDescriptors()
	if !sort.SliceIsSorted(descriptors, func(i, j int) bool { return descriptors[i].Key < descriptors[j].Key }) {
		t.Fatalf("descriptors not deterministic: %+v", descriptors)
	}
	key := descriptors[0].Key
	descriptors[0].DisplayName = "mutated"
	if descriptor, _ := LookupRoleType(key); descriptor.DisplayName == "mutated" {
		t.Fatal("descriptor snapshot leaked mutable registry state")
	}
	if _, known := LookupRoleType("not-registered"); known {
		t.Fatal("unknown role recognized")
	}
	if _, known := LookupExecutionKind("shell"); known {
		t.Fatal("unknown kind recognized")
	}
	kinds := ExecutionKindDescriptors()
	kinds[0].WorkflowSupported = false
	if descriptor, _ := LookupExecutionKind(ExecutionKindText); !descriptor.WorkflowSupported {
		t.Fatal("kind snapshot leaked mutable state")
	}
}

func TestRoleTypeRegistryIsExtensibleWithoutReplacingAuthority(t *testing.T) {
	const customRole = "test-domain-analyst"
	t.Cleanup(func() {
		roleTypes.Lock()
		delete(roleTypes.descriptors, customRole)
		roleTypes.Unlock()
	})
	descriptor := RoleTypeDescriptor{Key: customRole, DisplayName: "Domain analyst", Description: "A custom role"}
	if err := RegisterRoleType(descriptor); err != nil {
		t.Fatal(err)
	}
	document := textProfileV2()
	document.Identity.RoleType = customRole
	if report := Validate(document, ValidationContext{}); !report.Valid {
		t.Fatalf("registered extension rejected: %+v", report)
	}
	if err := RegisterRoleType(descriptor); err == nil {
		t.Fatal("duplicate registry key accepted")
	}
	if err := RegisterRoleType(RoleTypeDescriptor{Key: RoleTypeGeneral, DisplayName: "Override"}); err == nil {
		t.Fatal("builtin descriptor replacement accepted")
	}
	for _, invalid := range []RoleTypeDescriptor{
		{}, {Key: "UPPER", DisplayName: "invalid"}, {Key: " spaced ", DisplayName: "invalid"},
		{Key: "1-invalid", DisplayName: "invalid"}, {Key: "invalid-role", DisplayName: " "},
	} {
		if err := RegisterRoleType(invalid); err == nil {
			t.Fatalf("invalid descriptor accepted: %+v", invalid)
		}
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 20 {
				LookupRoleType(customRole)
				RoleTypeDescriptors()
				_ = RegisterRoleType(descriptor)
			}
		})
	}
	workers.Wait()
}
