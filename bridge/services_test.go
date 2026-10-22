package bridge

import (
	"testing"

	"github.com/yingyx/sjtu-bridge/contracts"
)

func TestPublicServiceDiscoveryHasNavigationWithoutPersonalActions(t *testing.T) {
	e, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	canvas, err := e.DescribeService("canvas")
	if err != nil || len(canvas.Destinations) == 0 || len(canvas.SupportedCapabilityIDs) == 0 || len(canvas.AvailableCapabilities) != 0 {
		t.Fatal("public service discovery must distinguish support from authorization", err)
	}
	page, err := e.Services(contracts.ServiceFilter{Query: "不及格"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "failed-grade-record" || len(page.Items[0].AvailableCapabilities) != 0 {
		t.Fatal("navigation task requires no execution capability", page, err)
	}
	for _, capability := range e.Capabilities() {
		if capability.ID == page.Items[0].ID {
			t.Fatal("task record leaked into execution registry")
		}
	}
}
