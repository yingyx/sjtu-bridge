package capabilities

import (
	"sort"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/policy"
)

// Catalog adds implemented personal contracts to the public runtime snapshot.
// It never reads credentials or grants. Unconnected personal entries describe
// setup requirements, not authority; their schemas contain no saved resources.
func Catalog(public []contracts.Capability) []contracts.Capability {
	result := make([]contracts.Capability, 0, len(public))
	for _, c := range public {
		if c.DataClass == contracts.PublicData && policy.ExecutableRead(c) {
			result = append(result, contracts.CloneCapability(c))
		}
	}
	ids := append(contracts.CampusCapabilityIDs(), contracts.CanvasCapabilityIDs()...)
	ids = append(ids, contracts.AssignmentsCapability)
	for _, id := range ids {
		c, ok := contracts.PersonalMetadata(id)
		if !ok || !policy.ExecutableRead(c) {
			continue
		}
		switch {
		case id == contracts.AssignmentsCapability:
			c.InputSchema = contracts.AssignmentInputSchema()
		case c.Provider == "canvas":
			c.InputSchema = contracts.CanvasInputSchema(id)
		default:
			c.InputSchema = contracts.CampusInputSchema(id)
		}
		if len(c.InputSchema) == 0 {
			continue
		}
		c.Authorized = false
		result = append(result, c)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
