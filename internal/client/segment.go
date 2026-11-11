package client

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
)

// DHCP is the per-segment DHCP pool bound to the segment's bridge interface.
type DHCP struct {
	Enabled    bool
	RangeStart string   // first address of the pool, e.g. "192.168.90.33"
	RangeSize  int      // number of addresses; RangeEnd is derived for the router
	RangeEnd   string   // last address (computed), echoed back by the router
	DNSServers []string // 0..2 servers (router stores address1/address2)
	LeaseTime  int      // seconds
}

// Segment is a Keenetic network segment: an `interface Bridge<N>` plus, optionally, a DHCP pool.
type Segment struct {
	Name          string
	Description   string
	Address       string
	Mask          string
	SecurityLevel string
	Up            bool
	DHCP          *DHCP // nil => this resource does not manage a DHCP pool for the segment
}

// poolName is the DHCP pool name this provider uses for a segment (one pool per segment).
func poolName(segment string) string { return "tf-" + segment }

type rcInterface struct {
	Description   string          `json:"description"`
	SecurityLevel map[string]bool `json:"security-level"`
	IP            struct {
		Address struct {
			Address string `json:"address"`
			Mask    string `json:"mask"`
		} `json:"address"`
	} `json:"ip"`
	Up bool `json:"up"`
}

type rcDHCP struct {
	Pool map[string]struct {
		Range struct {
			Begin string `json:"begin"`
			End   string `json:"end"`
			Size  int    `json:"size"`
		} `json:"range"`
		DNSServer struct {
			Address1 string `json:"address1"`
			Address2 string `json:"address2"`
		} `json:"dns-server"`
		Lease int `json:"lease"`
		Bind  struct {
			Interface string `json:"interface"`
		} `json:"bind"`
		Enable bool `json:"enable"`
	} `json:"pool"`
}

// GetSegment returns the segment named by `name`, or (nil,false,nil) when it does not exist.
func (c *Client) GetSegment(ctx context.Context, name string) (*Segment, bool, error) {
	var all map[string]json.RawMessage
	if err := c.get(ctx, "/rci/show/rc/interface", &all); err != nil {
		return nil, false, err
	}
	raw, ok := all[name]
	if !ok {
		return nil, false, nil
	}
	var def rcInterface
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, false, fmt.Errorf("decode interface %q: %w", name, err)
	}
	seg := &Segment{
		Name:        name,
		Description: def.Description,
		Address:     def.IP.Address.Address,
		Mask:        def.IP.Address.Mask,
		Up:          def.Up,
	}
	for level, on := range def.SecurityLevel {
		if on {
			seg.SecurityLevel = level
		}
	}

	// DHCP pool (the one this provider owns for the segment), if present.
	var dhcp rcDHCP
	if err := c.get(ctx, "/rci/show/rc/ip/dhcp", &dhcp); err != nil {
		return nil, false, err
	}
	if p, ok := dhcp.Pool[poolName(name)]; ok {
		d := &DHCP{
			Enabled:    p.Enable,
			RangeStart: p.Range.Begin,
			RangeEnd:   p.Range.End,
			RangeSize:  p.Range.Size,
			LeaseTime:  p.Lease,
		}
		for _, s := range []string{p.DNSServer.Address1, p.DNSServer.Address2} {
			if s != "" {
				d.DNSServers = append(d.DNSServers, s)
			}
		}
		seg.DHCP = d
	}
	return seg, true, nil
}

func interfaceCmd(s Segment) map[string]interface{} {
	level := s.SecurityLevel
	if level == "" {
		level = "private"
	}
	mask := s.Mask
	if mask == "" {
		mask = "255.255.255.0"
	}
	return map[string]interface{}{
		"interface": map[string]interface{}{
			"name":           s.Name,
			"description":    s.Description,
			"security-level": map[string]bool{level: true},
			"ip": map[string]interface{}{
				"address": map[string]string{"address": s.Address, "mask": mask},
			},
			"up": s.Up,
		},
	}
}

func poolCmd(s Segment) (map[string]interface{}, error) {
	d := s.DHCP
	pool := map[string]interface{}{
		"name":   poolName(s.Name),
		"bind":   map[string]interface{}{"interface": s.Name},
		"enable": d.Enabled,
	}
	if d.RangeStart != "" && d.RangeSize > 0 {
		end, err := endFromStartSize(d.RangeStart, d.RangeSize)
		if err != nil {
			return nil, err
		}
		pool["range"] = map[string]string{"begin": d.RangeStart, "end": end}
	}
	if len(d.DNSServers) > 0 {
		dns := map[string]string{"address1": d.DNSServers[0]}
		if len(d.DNSServers) > 1 {
			dns["address2"] = d.DNSServers[1]
		}
		pool["dns-server"] = dns
	}
	if d.LeaseTime > 0 {
		pool["lease"] = map[string]int{"lease": d.LeaseTime}
	}
	return map[string]interface{}{"ip": map[string]interface{}{"dhcp": map[string]interface{}{"pool": pool}}}, nil
}

func poolDeleteCmd(segment string) map[string]interface{} {
	return map[string]interface{}{"ip": map[string]interface{}{"dhcp": map[string]interface{}{
		"pool": map[string]interface{}{"name": poolName(segment), "no": true},
	}}}
}

// ApplySegment creates or updates a segment and its optional DHCP pool, then saves. Idempotent.
func (c *Client) ApplySegment(ctx context.Context, s Segment) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cmds := []interface{}{interfaceCmd(s)} // interface first: the pool binds to it
	if s.DHCP != nil {
		pc, err := poolCmd(s)
		if err != nil {
			return err
		}
		cmds = append(cmds, pc)
	} else {
		// No DHCP wanted: make sure any pool we previously created is gone.
		cur, found, err := c.getPool(ctx, s.Name)
		if err != nil {
			return err
		}
		if found && cur {
			cmds = append(cmds, poolDeleteCmd(s.Name))
		}
	}
	if _, err := c.post(ctx, cmds); err != nil {
		return err
	}
	return c.save(ctx)
}

// DeleteSegment removes the segment's DHCP pool (if any) and the interface, then saves.
func (c *Client) DeleteSegment(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cmds := []interface{}{}
	if has, found, err := c.getPool(ctx, name); err != nil {
		return err
	} else if found && has {
		cmds = append(cmds, poolDeleteCmd(name))
	}
	cmds = append(cmds, map[string]interface{}{
		"interface": map[string]interface{}{"name": name, "no": true},
	})
	if _, err := c.post(ctx, cmds); err != nil {
		return err
	}
	return c.save(ctx)
}

// getPool reports whether this provider's pool for `segment` currently exists on the router.
func (c *Client) getPool(ctx context.Context, segment string) (exists bool, ok bool, err error) {
	var dhcp rcDHCP
	if e := c.get(ctx, "/rci/show/rc/ip/dhcp", &dhcp); e != nil {
		return false, false, e
	}
	_, present := dhcp.Pool[poolName(segment)]
	return present, true, nil
}

// endFromStartSize returns the last IPv4 of a pool starting at `start` with `size` addresses.
func endFromStartSize(start string, size int) (string, error) {
	ip := net.ParseIP(start)
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("invalid IPv4 range_start %q", start)
	}
	v := binary.BigEndian.Uint32(ip.To4()) + uint32(size) - 1
	out := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(out, v)
	return out.String(), nil
}
