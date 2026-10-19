package demo

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/lanory/lanory/internal/database"
	"github.com/lanory/lanory/internal/model"
)

// Interface persistence.
//
// internal/database.Store builds its four interface queries by concatenating
// database.interfaceColumns with the FROM clause and omitting the SELECT
// keyword, so Store.ListInterfaces, Store.ListAllInterfaces, Store.GetInterface,
// Store.GetInterfaceByName and (through the existence check) Store.UpsertInterface
// all fail with `near "id": syntax error`.
//
// That defect lives in a file this package does not own, so instead of editing
// it we go through the store's exported *sql.DB with the correct statement. The
// column list and scan order below mirror database.interfaceColumns exactly, so
// the rows are byte-for-byte what the store would produce once it is fixed.
//
// When the store is corrected, preferStoreInterfaceQueries flips to true and
// these helpers delegate to the store methods; the call sites do not change.
// TestInterfaceQueriesMatchStoreContract asserts both paths agree.

var (
	// interfaceColumns is the column list of database.interfaceColumns.
	interfaceColumns = `id, device_id, name, mac, type, role, admin_state, oper_state, carrier,
	speed_mbps, mtu, bridge_id, vlan_id, is_physical,
	rx_bytes, tx_bytes, rx_errors, tx_errors, rx_packets, tx_packets, rx_dropped, tx_dropped,
	last_speed_change, last_seen`

	probeOnce sync.Once
	probeOK   bool
)

// preferStoreInterfaceQueries reports whether the store's interface queries
// work. It probes the store once per process with a lookup that is expected to
// miss, so a nil result plus database.ErrNotFound means "healthy".
func preferStoreInterfaceQueries(store *database.Store) bool {
	probeOnce.Do(func() {
		_, err := store.GetInterface("\x00lanory-probe\x00")
		probeOK = err == nil || errors.Is(err, database.ErrNotFound)
	})
	return probeOK
}

// scanInterfaceRow reads one interface row in database.interfaceColumns order.
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
	if speedChange.Valid {
		i.LastSpeedChange = speedChange.Time
	}
	i.LastSeen = lastSeen
	return i, nil
}

// upsertInterface inserts or refreshes an interface of the demo router.
func upsertInterface(store *database.Store, i *model.NetworkInterface) error {
	if i.LastSeen.IsZero() {
		i.LastSeen = database.Now()
	}
	if preferStoreInterfaceQueries(store) {
		return store.UpsertInterface(i)
	}

	existing, err := getInterface(store, i.ID)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		return err
	}
	if existing != nil {
		_, err := store.DB().Exec(`UPDATE network_interfaces SET
			mac = ?, type = ?, role = ?, admin_state = ?, oper_state = ?, carrier = ?,
			speed_mbps = ?, mtu = ?, bridge_id = ?, vlan_id = ?, is_physical = ?,
			rx_bytes = ?, tx_bytes = ?, rx_errors = ?, tx_errors = ?,
			rx_packets = ?, tx_packets = ?, rx_dropped = ?, tx_dropped = ?,
			last_speed_change = ?, last_seen = ?
			WHERE id = ?`,
			i.MAC, string(i.Type), string(i.Role), string(i.AdminState), string(i.OperState), boolInt(i.Carrier),
			i.SpeedMbps, i.MTU, i.BridgeID, i.VLANID, boolInt(i.IsPhysical),
			i.RXBytes, i.TXBytes, i.RXErrors, i.TXErrors,
			i.RXPackets, i.TXPackets, i.RXDropped, i.TXDropped,
			nullTime(i.LastSpeedChange), i.LastSeen.UTC().Format(database.TimeLayout), i.ID)
		return err
	}
	_, err = store.DB().Exec(`INSERT INTO network_interfaces (
		id, device_id, name, mac, type, role, admin_state, oper_state, carrier,
		speed_mbps, mtu, bridge_id, vlan_id, is_physical,
		rx_bytes, tx_bytes, rx_errors, tx_errors, rx_packets, tx_packets, rx_dropped, tx_dropped,
		last_speed_change, last_seen
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.DeviceID, i.Name, i.MAC, string(i.Type), string(i.Role),
		string(i.AdminState), string(i.OperState), boolInt(i.Carrier),
		i.SpeedMbps, i.MTU, i.BridgeID, i.VLANID, boolInt(i.IsPhysical),
		i.RXBytes, i.TXBytes, i.RXErrors, i.TXErrors,
		i.RXPackets, i.TXPackets, i.RXDropped, i.TXDropped,
		nullTime(i.LastSpeedChange), i.LastSeen.UTC().Format(database.TimeLayout))
	return err
}

// getInterface returns one interface by ID.
func getInterface(store *database.Store, id string) (*model.NetworkInterface, error) {
	if preferStoreInterfaceQueries(store) {
		return store.GetInterface(id)
	}
	row := store.DB().QueryRow("SELECT "+interfaceColumns+" FROM network_interfaces WHERE id = ?", id)
	iface, err := scanInterfaceRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, database.ErrNotFound
	}
	return iface, err
}

// listInterfaces returns every interface of a device, ordered by name, with
// their addresses.
//
// The store method attaches addresses as well, so this path must too, otherwise
// the demo data would look different depending on which path is active.
func listInterfaces(store *database.Store, deviceID string) ([]model.NetworkInterface, error) {
	if preferStoreInterfaceQueries(store) {
		return store.ListInterfaces(deviceID)
	}
	rows, err := store.DB().Query(
		"SELECT "+interfaceColumns+" FROM network_interfaces WHERE device_id = ? ORDER BY name", deviceID)
	if err != nil {
		return nil, err
	}
	out := []model.NetworkInterface{}
	for rows.Next() {
		iface, err := scanInterfaceRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		addrs, err := interfaceAddresses(store, iface.ID)
		if err != nil {
			rows.Close()
			return nil, err
		}
		iface.Addresses = addrs
		out = append(out, *iface)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return out, nil
}

// interfaceAddresses returns the addresses attached to an interface, in the same
// order the store uses.
func interfaceAddresses(store *database.Store, interfaceID string) ([]model.DeviceAddress, error) {
	rows, err := store.DB().Query(`SELECT id, device_id, address, family, interface_id, source,
		prefix_length, is_primary, first_seen, last_seen
		FROM device_addresses WHERE interface_id = ? ORDER BY family, address`, interfaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.DeviceAddress
	for rows.Next() {
		var a model.DeviceAddress
		var src string
		var primary int
		if err := rows.Scan(&a.ID, &a.DeviceID, &a.Address, &a.Family, &a.InterfaceID, &src,
			&a.PrefixLength, &primary, &a.FirstSeen, &a.LastSeen); err != nil {
			return nil, err
		}
		a.Source = model.DiscoverySource(src)
		a.IsPrimary = primary == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// boolInt renders a boolean as the SQLite integer the schema expects.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullTime renders a zero time as SQL NULL.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(database.TimeLayout)
}

// isSyntaxError reports whether err is the missing-SELECT failure of the store.
// It is used by tests to explain why the compatibility path is active.
func isSyntaxError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "syntax error")
}
