package zstd

const (
	blockTypeRaw = iota
	blockTypeRLE
	blockTypeCompressed
	blockTypeReserved
)

const (
	litBlockTypeRaw = iota
	litBlockTypeRLE
	litBlockTypeCompressed
	litBlockTypeTreeless
)

const (
	compModePredefined = iota
	compModeRLE
	compModeFSE
	compModeRepeat
)
