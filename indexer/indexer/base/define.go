package base

const SyncStatsKey = "syncStats"
const BaseDBVerKey = "dbver"

// 和SyncHeight同步的数据
type SyncBase struct {
	SyncHeight     int    `json:"syncHeight"`
	SyncBlockHash  string `json:"syncBlockHash"`
	AllUtxoCount   uint64
	UtxoCount      uint64
	TotalAscendSats  int64 // 包含绑定资产的聪
	TotalDescendSats int64
	AscendCount    int
	DescendCount   int
}

type SyncStats struct {
	SyncBase
	AddressCount   uint64
	ChainTip       int    `json:"chainTip"`
	ReorgsDetected []int  `json:"reorgsDetected"`
}

type IrregularSubsidy struct {
	TotalLeakSats  int64
	SatsLeakBlocks map[int]int64
}

func (p *SyncStats) Clone () *SyncStats {
	c := &SyncStats{
		SyncBase: p.SyncBase,
		AddressCount: p.AddressCount,
		ChainTip: p.ChainTip,
	}
	c.ReorgsDetected = make([]int, len(p.ReorgsDetected))
	copy(c.ReorgsDetected, p.ReorgsDetected)
	return c
}
