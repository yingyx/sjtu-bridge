package publicinfo

import (
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/servicecatalog"
)

const directoryRevision = servicecatalog.Revision

// The website view is a projection of the unified vetted task directory.
func directory() []contracts.Item { return servicecatalog.PortalItems() }
