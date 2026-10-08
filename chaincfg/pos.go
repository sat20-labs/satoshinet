package chaincfg

// POSV2Active deliberately excludes genesis and unscheduled networks.
func (p *Params) POSV2Active(height int32) bool {
	return p != nil && p.POSV2Height > 0 && height >= p.POSV2Height
}
