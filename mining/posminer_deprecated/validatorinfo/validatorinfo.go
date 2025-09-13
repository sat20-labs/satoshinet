package validatorinfo

import (
	"net"
	"time"

)

type ValidatorInfo struct {
	Host            string
	ValidatorId     string // hex.EncodeToString
	CreateTime      time.Time
	ActivitionCount int32
	GeneratorCount  int32
	DiscountCount   int32
	FaultCount      int32
	ValidatorScore  int32
}

type ValidatorInfoMask uint64

const (
	MaskValidatorId     ValidatorInfoMask = 1 << 0
	MaskActivitionCount ValidatorInfoMask = 1 << 1
	MaskGeneratorCount  ValidatorInfoMask = 1 << 2
	MaskDiscountCount   ValidatorInfoMask = 1 << 3
	MaskFaultCount      ValidatorInfoMask = 1 << 4
	MaskCreateTime      ValidatorInfoMask = 1 << 5
	MaskHost            ValidatorInfoMask = 1 << 6

	MaskAll ValidatorInfoMask = MaskValidatorId | MaskActivitionCount | MaskGeneratorCount | MaskDiscountCount | MaskFaultCount | MaskCreateTime
)

func GetAddrStringHost(addr string) string {
	host, _, _ := net.SplitHostPort(addr)
	return host
}

func GetAddrHost(addr net.Addr) net.IP {
	switch addr.(type) {
	case *net.TCPAddr:
		addrHost := addr.(*net.TCPAddr).IP
		return addrHost
	}

	return nil
}
