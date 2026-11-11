package client

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// VLAN is the optional 802.1Q configuration of a segment: one VLAN id, carried (tagged) on one or
// more physical ports. On the router each (port, id) is an `interface <port>/Vlan<id>` sub-interface
// that the segment's bridge includes.
type VLAN struct {
	ID    int
	Ports []string // physical ports, e.g. ["GigabitEthernet0", "GigabitEthernet1"]
}

var reVlanIface = regexp.MustCompile(`^(GigabitEthernet\d+)/Vlan(\d+)$`)

func vlanIface(port string, id int) string { return fmt.Sprintf("%s/Vlan%d", port, id) }

// vlanMembers returns the <port>/Vlan<id> interfaces a bridge currently includes, with the id.
func vlanMembers(all map[string]json.RawMessage, bridge string) (ports []string, id int, _ error) {
	raw, ok := all[bridge]
	if !ok {
		return nil, 0, nil
	}
	var def struct {
		Include []struct {
			Interface string `json:"interface"`
		} `json:"include"`
	}
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, 0, fmt.Errorf("decode %q include: %w", bridge, err)
	}
	for _, m := range def.Include {
		if g := reVlanIface.FindStringSubmatch(m.Interface); g != nil {
			n, _ := strconv.Atoi(g[2])
			id = n // one VLAN id per segment (all members share it)
			ports = append(ports, g[1])
		}
	}
	sort.Strings(ports)
	return ports, id, nil
}

// readVLAN builds the VLAN view of a segment from the interface map (nil when it has none).
func readVLAN(all map[string]json.RawMessage, bridge string) (*VLAN, error) {
	ports, id, err := vlanMembers(all, bridge)
	if err != nil || len(ports) == 0 {
		return nil, err
	}
	return &VLAN{ID: id, Ports: ports}, nil
}

// vlanOwners maps every VLAN id in use to the bridge that carries it (any */Vlan<id> it includes).
func vlanOwners(all map[string]json.RawMessage) (map[int]string, error) {
	owners := map[int]string{}
	for name := range all {
		if !strings.HasPrefix(name, "Bridge") {
			continue
		}
		_, id, err := vlanMembers(all, name)
		if err != nil {
			return nil, err
		}
		if id != 0 {
			owners[id] = name
		}
	}
	return owners, nil
}

// bridgeTitle returns a human label for a bridge ("Bridge1 (VM Segment)") for conflict messages.
func bridgeTitle(all map[string]json.RawMessage, bridge string) string {
	if raw, ok := all[bridge]; ok {
		var d struct {
			Description string `json:"description"`
		}
		if json.Unmarshal(raw, &d) == nil && d.Description != "" {
			return fmt.Sprintf("%s (%s)", bridge, d.Description)
		}
	}
	return bridge
}

// checkVLANConflict returns an error if `desired`'s VLAN id is already carried by another bridge.
// Run before any writes so a conflict never leaves a half-created segment behind.
func (c *Client) checkVLANConflict(ctx context.Context, bridge string, desired *VLAN) error {
	if desired == nil {
		return nil
	}
	var all map[string]json.RawMessage
	if err := c.get(ctx, "/rci/show/rc/interface", &all); err != nil {
		return err
	}
	owners, err := vlanOwners(all)
	if err != nil {
		return err
	}
	if owner, taken := owners[desired.ID]; taken && owner != bridge {
		return fmt.Errorf("VLAN %d conflict: already carried by %s — pick a free VLAN id",
			desired.ID, bridgeTitle(all, owner))
	}
	return nil
}

// reconcileVLAN (lock held, bridge already created) makes the bridge's VLAN membership match
// `desired`, after refusing a VLAN id already carried by another segment (conflict detection).
func (c *Client) reconcileVLAN(ctx context.Context, bridge string, desired *VLAN) error {
	var all map[string]json.RawMessage
	if err := c.get(ctx, "/rci/show/rc/interface", &all); err != nil {
		return err
	}

	if desired != nil {
		owners, err := vlanOwners(all)
		if err != nil {
			return err
		}
		if owner, taken := owners[desired.ID]; taken && owner != bridge {
			return fmt.Errorf("VLAN %d conflict: already carried by %s — pick a free VLAN id",
				desired.ID, bridgeTitle(all, owner))
		}
	}

	currentPorts, currentID, err := vlanMembers(all, bridge)
	if err != nil {
		return err
	}

	// Desired (port, id) interfaces.
	want := map[string]bool{}
	if desired != nil {
		for _, p := range desired.Ports {
			want[vlanIface(p, desired.ID)] = true
		}
	}

	// Remove + delete VLAN sub-interfaces we no longer want.
	for _, p := range currentPorts {
		ifc := vlanIface(p, currentID)
		if want[ifc] {
			continue
		}
		if _, err := c.post(ctx, []interface{}{
			bridgeIncludeCmd(bridge, ifc, false),
			map[string]interface{}{"interface": map[string]interface{}{"name": ifc, "no": true}},
		}); err != nil {
			return err
		}
	}

	// Create + include desired sub-interfaces (idempotent on re-apply).
	if desired != nil {
		for _, p := range desired.Ports {
			ifc := vlanIface(p, desired.ID)
			if _, err := c.post(ctx, []interface{}{
				map[string]interface{}{"interface": map[string]interface{}{"name": ifc, "up": true}},
			}); err != nil {
				return err
			}
			if _, err := c.post(ctx, []interface{}{bridgeIncludeCmd(bridge, ifc, true)}); err != nil {
				return err
			}
		}
	}
	return nil
}

// teardownVLAN (lock held) detaches and deletes every VLAN sub-interface of the bridge, before the
// bridge itself is removed.
func (c *Client) teardownVLAN(ctx context.Context, bridge string) error {
	var all map[string]json.RawMessage
	if err := c.get(ctx, "/rci/show/rc/interface", &all); err != nil {
		return err
	}
	ports, id, err := vlanMembers(all, bridge)
	if err != nil {
		return err
	}
	for _, p := range ports {
		ifc := vlanIface(p, id)
		if _, err := c.post(ctx, []interface{}{
			bridgeIncludeCmd(bridge, ifc, false),
			map[string]interface{}{"interface": map[string]interface{}{"name": ifc, "no": true}},
		}); err != nil {
			return err
		}
	}
	return nil
}
