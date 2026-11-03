package agentprofile

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

const (
	RoleTypePlanner    = "planner"
	RoleTypeResearcher = "researcher"
	RoleTypeCoder      = "coder"
	RoleTypeReviewer   = "reviewer"
	RoleTypeExecutor   = "executor"
	RoleTypeGeneral    = "general"
)

// RoleTypeDescriptor is presentation metadata only. Registering a role never
// grants tools, changes policy, or selects an execution adapter.
type RoleTypeDescriptor struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

var roleTypes = struct {
	sync.RWMutex
	descriptors map[string]RoleTypeDescriptor
}{descriptors: map[string]RoleTypeDescriptor{
	RoleTypePlanner:    {Key: RoleTypePlanner, DisplayName: "Planner", Description: "Break a goal into bounded tasks."},
	RoleTypeResearcher: {Key: RoleTypeResearcher, DisplayName: "Researcher", Description: "Analyze supplied material without implicit network access."},
	RoleTypeCoder:      {Key: RoleTypeCoder, DisplayName: "Coder", Description: "Produce code within explicitly granted capabilities."},
	RoleTypeReviewer:   {Key: RoleTypeReviewer, DisplayName: "Reviewer", Description: "Review outputs against acceptance criteria."},
	RoleTypeExecutor:   {Key: RoleTypeExecutor, DisplayName: "Executor", Description: "Execute a bounded task within explicit policy."},
	RoleTypeGeneral:    {Key: RoleTypeGeneral, DisplayName: "General", Description: "Handle a general-purpose task."},
}}

// RegisterRoleType extends the process-local metadata registry. Keys must be
// canonical lowercase identifiers. Existing descriptors cannot be replaced.
func RegisterRoleType(descriptor RoleTypeDescriptor) error {
	if !validRoleKey(descriptor.Key) || strings.TrimSpace(descriptor.DisplayName) == "" {
		return fmt.Errorf("invalid role descriptor %q", descriptor.Key)
	}
	descriptor.DisplayName = strings.TrimSpace(descriptor.DisplayName)
	descriptor.Description = strings.TrimSpace(descriptor.Description)
	roleTypes.Lock()
	defer roleTypes.Unlock()
	if _, exists := roleTypes.descriptors[descriptor.Key]; exists {
		return fmt.Errorf("role_type %q is already registered", descriptor.Key)
	}
	roleTypes.descriptors[descriptor.Key] = descriptor
	return nil
}

func validRoleKey(key string) bool {
	if key == "" || len(key) > MaxDisplayNameBytes || key[0] < 'a' || key[0] > 'z' {
		return false
	}
	for _, char := range key {
		if char < 'a' || char > 'z' {
			if (char < '0' || char > '9') && char != '-' && char != '_' {
				return false
			}
		}
	}
	return true
}

func LookupRoleType(key string) (RoleTypeDescriptor, bool) {
	roleTypes.RLock()
	defer roleTypes.RUnlock()
	descriptor, exists := roleTypes.descriptors[key]
	return descriptor, exists
}

// RoleTypeDescriptors returns an independent, deterministically sorted snapshot.
func RoleTypeDescriptors() []RoleTypeDescriptor {
	roleTypes.RLock()
	defer roleTypes.RUnlock()
	result := make([]RoleTypeDescriptor, 0, len(roleTypes.descriptors))
	for _, descriptor := range roleTypes.descriptors {
		result = append(result, descriptor)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

type ExecutionKind string

const (
	ExecutionKindText     ExecutionKind = "text"
	ExecutionKindComputer ExecutionKind = "computer"
	ExecutionKindImage    ExecutionKind = "image"
)

type ExecutionKindDescriptor struct {
	Kind              ExecutionKind `json:"kind"`
	DisplayName       string        `json:"display_name"`
	WorkflowSupported bool          `json:"workflow_supported"`
}

// ExecutionKindDescriptors describes schema-recognized kinds, not authority to
// run them. The workflow compiler must reject kinds without an enabled adapter;
// the MVP supports text only. Apply remains the existing text-runtime mapper.
func ExecutionKindDescriptors() []ExecutionKindDescriptor {
	return []ExecutionKindDescriptor{
		{Kind: ExecutionKindText, DisplayName: "Text", WorkflowSupported: true},
		{Kind: ExecutionKindComputer, DisplayName: "Computer", WorkflowSupported: false},
		{Kind: ExecutionKindImage, DisplayName: "Image", WorkflowSupported: false},
	}
}

func LookupExecutionKind(kind ExecutionKind) (ExecutionKindDescriptor, bool) {
	for _, descriptor := range ExecutionKindDescriptors() {
		if descriptor.Kind == kind {
			return descriptor, true
		}
	}
	return ExecutionKindDescriptor{}, false
}
