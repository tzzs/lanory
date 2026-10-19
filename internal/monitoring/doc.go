// Package monitoring implements the Lanory Probe Engine.
//
// The engine keeps three concepts deliberately separate, because conflating
// them produces wrong answers on the topology graph:
//
//  1. Device Reachability — can the probe source reach this device?
//     ("OpenWrt → iPhone reachable" says nothing about "Windows → iPhone").
//  2. Link Health — is the physical port up, and at what speed? A device can be
//     reachable while its port is down (wireless) and a port can be up while
//     the device behind it is unreachable.
//  3. Internet Connectivity — can the probe source reach the outside world?
//     Decided from several independent targets, never from one website.
//
// # Probe source semantics
//
// Every probe runs from one source. The default source is the OpenWrt device
// Lanory itself runs on (see DefaultSourceID). model.ProbeTarget.SourceID
// already supports multiple sources, but the MVP only *executes* probes from
// the local host: terminal-to-terminal reachability (e.g. "Windows → iPhone")
// would require an optional remote probe agent running on that terminal, which
// is explicitly out of MVP scope. Nothing in this package pretends otherwise —
// results are always labelled with the source that produced them.
package monitoring
