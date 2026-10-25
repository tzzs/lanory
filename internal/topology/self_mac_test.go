package topology

import (
	"reflect"
	"testing"

	"github.com/lanory/lanory/internal/discovery"
	"github.com/lanory/lanory/internal/model"
)

// These tests pin the host-MAC selection rule that used to be flaky.
//
// Docker's container bridge is reported by ubus as a bridge, exactly like the
// LAN bridge, and their order came from a Go map iteration inside the openwrt
// collector. Whenever docker0 happened to come first, its MAC was chosen as the
// router's identity and the real router was rendered as an ordinary end device.
// The collector now sorts its interface names and this function ignores virtual
// interfaces, so the outcome no longer depends on luck.

func TestSelfMACsFromInterfacesSkipsVirtualBridges(t *testing.T) {
	const (
		realBridgeMAC = "020000000011" // br-lan, the router itself
		dockerMAC     = "0242ac120002" // docker0, a container bridge
	)

	realBridge := discovery.InterfaceObservation{
		Name:    "br-lan",
		MAC:     "02:00:00:00:00:11",
		Type:    model.InterfaceTypeBridge,
		Virtual: false,
	}
	dockerBridge := discovery.InterfaceObservation{
		Name:    "docker0",
		MAC:     "02:42:ac:12:00:02",
		Type:    model.InterfaceTypeBridge,
		Virtual: true,
	}
	// Noise that must never become a host candidate either.
	noise := []discovery.InterfaceObservation{
		{Name: "lo", MAC: "00:00:00:00:00:00", Type: model.InterfaceTypeLoopback},
		{Name: "mcast", MAC: "01:00:5e:00:00:01", Type: model.InterfaceTypeVirtual},
		{Name: "empty", MAC: "", Type: model.InterfaceTypeEthernet},
	}

	orders := map[string][]discovery.InterfaceObservation{}
	dockerFirst := []discovery.InterfaceObservation{dockerBridge}
	dockerFirst = append(dockerFirst, noise...)
	dockerFirst = append(dockerFirst, realBridge)
	orders["docker0 observed first"] = dockerFirst
	realFirst := []discovery.InterfaceObservation{realBridge}
	realFirst = append(realFirst, noise...)
	realFirst = append(realFirst, dockerBridge)
	orders["br-lan observed first"] = realFirst
	for name, interfaces := range orders {
		t.Run(name, func(t *testing.T) {
			got := selfMACsFromInterfaces(interfaces)
			if len(got) == 0 {
				t.Fatal("no host MAC candidates returned")
			}
			if got[0] != realBridgeMAC {
				t.Fatalf("first candidate = %q, want the real bridge %q (candidates %v)", got[0], realBridgeMAC, got)
			}
			for _, mac := range got {
				if mac == dockerMAC {
					t.Errorf("container bridge MAC leaked into the host candidates: %v", got)
				}
			}
		})
	}
}

func TestSelfMACsFromInterfacesIsDeterministic(t *testing.T) {
	brLAN := discovery.InterfaceObservation{Name: "br-lan", MAC: "02:00:00:00:00:11", Type: model.InterfaceTypeBridge}
	brGuest := discovery.InterfaceObservation{Name: "br-guest", MAC: "02:00:00:00:00:22", Type: model.InterfaceTypeBridge}
	want := []string{"020000000011", "020000000022"} // sorted ascending by canonical MAC

	orders := [][]discovery.InterfaceObservation{
		{brLAN, brGuest},
		{brGuest, brLAN},
	}
	for i, interfaces := range orders {
		for call := 0; call < 20; call++ {
			got := selfMACsFromInterfaces(interfaces)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("order %d, call %d: got %v, want %v", i, call, got, want)
			}
		}
	}
}
