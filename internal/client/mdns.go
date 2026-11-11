package client

import "context"

// GetMDNSReflector reports whether the global mDNS reflector is enforced (relays multicast DNS
// between the router's segments). This is a single router-wide setting, not per-segment.
func (c *Client) GetMDNSReflector(ctx context.Context) (bool, error) {
	var out struct {
		Reflector struct {
			Enforce bool `json:"enforce"`
		} `json:"reflector"`
	}
	if err := c.get(ctx, "/rci/show/rc/mdns", &out); err != nil {
		return false, err
	}
	return out.Reflector.Enforce, nil
}

// SetMDNSReflector enables or disables the global mDNS reflector, then saves.
func (c *Client) SetMDNSReflector(ctx context.Context, enforce bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmd := []interface{}{
		map[string]interface{}{"mdns": map[string]interface{}{"reflector": map[string]interface{}{"enforce": enforce}}},
	}
	if _, err := c.post(ctx, cmd); err != nil {
		return err
	}
	return c.save(ctx)
}
