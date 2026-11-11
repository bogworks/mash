package config

// DisplayName is the name people read for this endpoint: the canonical agent.owner.name for a three-token endpoint
// (ADR 0081), the bare name for a legacy one. Name alone is only the name token of a three-token endpoint. An identity
// that does not validate falls back to Name, so a half-set config still prints something.
func (c Config) DisplayName() string {
	if c.AgentToken != "" {
		if id, err := c.Identity(); err == nil {
			return id.String()
		}
	}
	return c.Name
}
