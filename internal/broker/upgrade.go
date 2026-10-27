package broker

import (
	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/local"
	"github.com/yingyx/sjtu-bridge/internal/storage"
)

// UpgradeStorage is an explicit offline owner operation; startup never calls it.
func UpgradeStorage(apply bool) (storage.UpgradeReport, error) {
	dir, err := local.DataDir()
	if err != nil || local.Check(dir) != nil {
		return storage.UpgradeReport{}, storageError()
	}
	unlock, err := local.Lock(dir)
	if err != nil {
		return storage.UpgradeReport{}, contracts.Fail("BROKER_UNAVAILABLE", "Stop the Broker before upgrading storage")
	}
	defer unlock()
	return storage.UpgradeV1(dir, storage.SystemKeyring{}, apply)
}
