package topology

import (
	"database/sql"
	"errors"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// Interface reads.
//
// The store's interface queries (Store.ListInterfaces, ListAllInterfaces,
// GetInterface, GetInterfaceByName) build their SQL by concatenating the column
// list with the FROM clause and omit the SELECT keyword:
//
//	rows, err := s.db.Query(interfaceColumns + ` FROM network_interfaces ...`)
//
// Every one of them therefore fails with `near "id": syntax error`, and
// Store.UpsertInterface's existence check inherits the same failure so a create
// on a fresh device never succeeds.
//
// That is a defect in internal/database/store.go, which this package does not
// own. Rather than edit another agent's file — or silently degrade the topology
// — the topology package reads and writes interfaces through the store's
// exposed *sql.DB with a correct query. The scan mirrors Store.scanInterface
// exactly, so the values are identical once the store is fixed.
//
// When internal/database is corrected these helpers can delegate to the store
// methods and this file can be deleted. Every call site already goes through the
// functions below, so nothing else in the package changes.

// interfaceColumns mirrors database.interfaceColumns.
const interfaceColumns = `id, device_id, name, mac, type, role, admin_state, oper_state, carrier,
	speed_mbps, mtu, bridge_id, vlan_id, is_physical,
	rx_bytes, tx_bytes, rx_errors, tx_errors, rx_packets, tx_packets, rx_dropped, tx_dropped,
	last_speed_change, last_seen`

// scanInterfaceRow reads one interface row in the same column order as
// database.Store.scanInterface.
func scanInterfaceRow(sc interface{ Scan(...any) error }) (*model.NetworkInterface, error) {
	i := &model.NetworkInterface{}
	var (
		ifaceType, role, admin, oper string
		carrier, physical            int
		speedChange                  sql.NullTime
		lastSeen                     time.Time
	)
	err := sc.Scan(&i.ID, &i.DeviceID, &i.Name, &i.MAC, &ifaceType, &role, &admin, &oper, &carrier,
		&i.SpeedMbps, &i.MTU, &i.BridgeID, &i.VLANID, &physical,
		&i.RXBytes, &i.TXBytes, &i.RXErrors, &i.TXErrors, &i.RXPackets, &i.TXPackets, &i.RXDropped, &i.TXDropped,
		&speedChange, &lastSeen)
	if err != nil {
		return nil, err
	}
	i.Type = model.InterfaceType(ifaceType)
	i.Role = model.InterfaceRole(role)
	i.AdminState = model.AdminState(admin)
	i.OperState = model.OperState(oper)
	i.Carrier = carrier == 1
	i.IsPhysical = physical == 1
	i.LastSpeedChange = speedChange.Time
	i.LastSeen = lastSeen
	return i, nil
}

// listDeviceInterfaces returns every interface of one device.
func listDeviceInterfaces(store *database.Store, deviceID string) ([]model.NetworkInterface, error) {
	rows, err := store.DB().Query(
		"SELECT "+interfaceColumns+" FROM network_interfaces WHERE device_id = ? ORDER BY name", deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.NetworkInterface
	for rows.Next() {
		iface, err := scanInterfaceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *iface)
	}
	return out, rows.Err()
}

// listAllInterfaces returns every interface of every device.
func listAllInterfaces(store *database.Store) ([]model.NetworkInterface, error) {
	rows, err := store.DB().Query(
		"SELECT " + interfaceColumns + " FROM network_interfaces ORDER BY device_id, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.NetworkInterface
	for rows.Next() {
		iface, err := scanInterfaceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *iface)
	}
	return out, rows.Err()
}

// getInterfaceByName returns one interface by device and name.
func getInterfaceByName(store *database.Store, deviceID, name string) (*model.NetworkInterface, error) {
	row := store.DB().QueryRow(
		"SELECT "+interfaceColumns+" FROM network_interfaces WHERE device_id = ? AND name = ?",
		deviceID, name)
	iface, err := scanInterfaceRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return iface, nil
}

// upsertInterface writes an interface, inserting or updating as needed.
//
// It mirrors database.Store.UpsertInterface but uses a working existence check,
// so a create on a fresh device succeeds.
func upsertInterface(store *database.Store, i *model.NetworkInterface) error {
	if i.ID == "" {
		i.ID = newInterfaceID(i.DeviceID, i.Name)
	}
	if i.LastSeen.IsZero() {
		i.LastSeen = database.Now()
	}

	existing, err := getInterfaceByID(store, i.ID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		return err
	}
	if existing != nil {
		_, err = store.DB().Exec(`UPDATE network_interfaces SET
			mac = ?, type = ?, role = ?, admin_state = ?, oper_state = ?, carrier = ?,
			speed_mbps = ?, mtu = ?, bridge_id = ?, vlan_id = ?, is_physical = ?,
			rx_bytes = ?, tx_bytes = ?, rx_errors = ?, tx_errors = ?,
			rx_packets = ?, tx_packets = ?, rx_dropped = ?, tx_dropped = ?,
			last_speed_change = ?, last_seen = ?
			WHERE id = ?`,
			i.MAC, string(i.Type), string(i.Role), string(i.AdminState), string(i.OperState), boolInt(i.Carrier),
			i.SpeedMbps, i.MTU, i.BridgeID, i.VLANID, boolInt(i.IsPhysical),
			i.RXBytes, i.TXBytes, i.RXErrors, i.TXErrors, i.RXPackets, i.TXPackets, i.RXDropped, i.TXDropped,
			nullTime(i.LastSpeedChange), i.LastSeen.UTC().Format(database.TimeLayout), i.ID)
		return err
	}
	_, err = store.DB().Exec(`INSERT INTO network_interfaces (
		id, device_id, name, mac, type, role, admin_state, oper_state, carrier,
		speed_mbps, mtu, bridge_id, vlan_id, is_physical,
		rx_bytes, tx_bytes, rx_errors, tx_errors, rx_packets, tx_packets, rx_dropped, tx_dropped,
		last_speed_change, last_seen
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.DeviceID, i.Name, i.MAC, string(i.Type), string(i.Role), string(i.AdminState), string(i.OperState), boolInt(i.Carrier),
		i.SpeedMbps, i.MTU, i.BridgeID, i.VLANID, boolInt(i.IsPhysical),
		i.RXBytes, i.TXBytes, i.RXErrors, i.TXErrors, i.RXPackets, i.TXPackets, i.RXDropped, i.TXDropped,
		nullTime(i.LastSpeedChange), i.LastSeen.UTC().Format(database.TimeLayout))
	return err
}

// getInterfaceByID returns one interface by its own ID.
func getInterfaceByID(store *database.Store, id string) (*model.NetworkInterface, error) {
	row := store.DB().QueryRow(
		"SELECT "+interfaceColumns+" FROM network_interfaces WHERE id = ?", id)
	iface, err := scanInterfaceRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return iface, nil
}

// newInterfaceID builds a deterministic interface ID from the device and name.
//
// A stable ID matters: the topology edge stores source_interface_id, so
// regenerating the ID on every scan would orphan every edge.
func newInterfaceID(deviceID, name string) string {
	return "iface-" + sanitizeID(deviceID) + "-" + sanitizeID(name)
}

func sanitizeID(v string) string {
	out := make([]byte, 0, len(v))
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		case c == ':' || c == '.' || c == '/':
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "x"
	}
	return string(out)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}
