package servicenav

import (
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/servicecatalog"
)

// Keep the existing grouped navigation contract as a derived delivery view.
// Destinations and support references are maintained only in servicecatalog.
var entries = func() map[string][]contracts.Item {
	result := map[string][]contracts.Item{}
	for _, capability := range contracts.PublicCapabilities() {
		if items := servicecatalog.NavigationItems(capability.ID); len(items) > 0 {
			result[capability.ID] = items
		}
	}
	return result
}()
