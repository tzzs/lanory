package topology

import (
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// fakeStore is an in-memory StoreReader for topology tests. It implements the
// interface the engine needs and nothing else.
type fakeStore struct {
	devices    []model.Device
	interfaces []model.NetworkInterface
	addresses  map[string][]model.DeviceAddress
	edges      []model.TopologyEdge
	identities map[string][]model.DeviceIdentity
}

func (f *fakeStore) ListDevices(filter database.DeviceFilter) ([]model.Device, int, error) {
	var out []model.Device
	for _, d := range f.devices {
		if !filter.IncludeHidden && d.IsHidden {
			continue
		}
		out = append(out, d)
	}
	return out, len(out), nil
}

func (f *fakeStore) ListAllInterfaces() ([]model.NetworkInterface, error) {
	return f.interfaces, nil
}

func (f *fakeStore) ListAddresses(deviceID string) ([]model.DeviceAddress, error) {
	return f.addresses[deviceID], nil
}

func (f *fakeStore) ListActiveAddresses(deviceID string, _ time.Time) ([]model.DeviceAddress, error) {
	return f.addresses[deviceID], nil
}

func (f *fakeStore) ListEdges(_ database.EdgeFilter) ([]model.TopologyEdge, error) {
	return f.edges, nil
}

func (f *fakeStore) ListIdentities(deviceID string) ([]model.DeviceIdentity, error) {
	return f.identities[deviceID], nil
}

var _ StoreReader = (*fakeStore)(nil)
