package config

// DisplayName is the name people read for this endpoint: the canonical owner.group.instance. An address that does not
// validate falls back to Name, so a half-set config still prints something.
func (c Config) DisplayName() string {
	if id, err := c.Identity(); err == nil {
		return id.String()
	}
	return c.Name
}
