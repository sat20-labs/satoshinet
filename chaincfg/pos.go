package chaincfg

// POSV2Active deliberately excludes genesis and unscheduled networks.
func (p *Params) POSV2Active(height int32) bool {
	return p != nil && p.POSV2Height > 0 && height >= p.POSV2Height
}

// NativeAssetsActive gates new proof-backed mappings only. Do not use this
// switch to disable ordinary transfers or exits of already mapped assets.
func (p *Params) NativeAssetsActive(height int32) bool {
	return p != nil && p.NativeAssetHeight > 0 && height >= p.NativeAssetHeight
}
