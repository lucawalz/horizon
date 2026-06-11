package hetzner

func (p *Provider) SetServerIDForTest(id string) { p.serverID = id }
func (p *Provider) SetBurstIDForTest(id string)  { p.burstID = id }
func (p *Provider) SharedDirForTest() string     { return p.sharedDir() }
