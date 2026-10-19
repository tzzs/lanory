// Package demo implements Demo Mode.
//
// Demo Mode loads a deterministic sample network into the database so the whole
// UI can be previewed without running OpenWrt, without a router, and without
// touching the real system.
//
// Two rules shape this package and are enforced by its tests:
//
//  1. Every row it writes is tagged with model.SourceDemo. Devices carry the
//     source in device_sources and in type_source, addresses/identities in
//     their source column, edges in discovery_source, and evidence rows use
//     the collector name "demo". The UI badges that source so simulated data
//     can never be mistaken for a real scan result. The product spec forbids
//     presenting simulated online status as a real scan result, so the top
//     level flag (app_settings "demo_mode") must be surfaced by the API
//     (Overview.DemoMode, HealthResponse.DemoMode) whenever this data is
//     served.
//
//  2. Loading is idempotent. Rows that have a natural key (devices,
//     interfaces, addresses, identities, edges, layout) are written through
//     the Store upserts with deterministic IDs. Append-only tables (probe
//     results, events, evidence) are written only when the subject has no demo
//     rows yet, so a second Load adds nothing.
//
// The sample network is a de-identified copy of the reference network in
// fixtures/easepi-r1. Only sanitized addresses from that directory are used;
// fixtures/private is never read. internal/demo/fixtures_test.go asserts that
// every address in the dataset also occurs in the sanitized fixtures.
package demo
