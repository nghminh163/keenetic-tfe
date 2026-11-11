package client

import (
	"context"
	"encoding/json"
	"fmt"
)

// Segment is a Keenetic network segment, i.e. an `interface Bridge<N>` with an IPv4 address.
type Segment struct {
	Name          string // interface id, e.g. "Bridge2"
	Description   string
	Address       string // gateway IPv4 of the segment, e.g. "192.168.90.1"
	Mask          string // dotted netmask, e.g. "255.255.255.0"
	SecurityLevel string // "private" | "public" | "protected"
	Up            bool
}

// rcInterface mirrors the parts of GET /rci/show/rc/interface this provider manages.
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
	return seg, true, nil
}

// interfaceCmd builds the RCI `interface` object for create/update.
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

// ApplySegment creates or updates a segment, then saves the configuration. Idempotent.
func (c *Client) ApplySegment(ctx context.Context, s Segment) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.post(ctx, []interface{}{interfaceCmd(s)}); err != nil {
		return err
	}
	return c.save(ctx)
}

// DeleteSegment removes a segment (`interface <name> no`), then saves.
func (c *Client) DeleteSegment(ctx context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmd := []interface{}{
		map[string]interface{}{
			"interface": map[string]interface{}{"name": name, "no": true},
		},
	}
	if _, err := c.post(ctx, cmd); err != nil {
		return err
	}
	return c.save(ctx)
}
