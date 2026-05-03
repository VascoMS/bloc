module pic-node

go 1.22

require (
	btd v0.0.0
	github.com/anthdm/hbbft v0.0.0
	go.dedis.ch/kyber/v4 v4.0.0-pre2.0.20240806142600-b172e02f7ce5
)

require (
	github.com/NebulousLabs/errors v0.0.0-20181203160057-9f787ce8f69e // indirect
	github.com/NebulousLabs/fastrand v0.0.0-20181203155948-6fb6489aac4e // indirect
	github.com/NebulousLabs/merkletree v0.0.0-20181203152040-08d5d54b07f5 // indirect
	github.com/kilic/bls12-381 v0.1.0 // indirect
	github.com/klauspost/cpuid v1.2.1 // indirect
	github.com/klauspost/reedsolomon v1.9.2 // indirect
	github.com/konsorten/go-windows-terminal-sequences v1.0.1 // indirect
	github.com/sirupsen/logrus v1.4.2 // indirect
	golang.org/x/crypto v0.26.0 // indirect
	golang.org/x/sys v0.23.0 // indirect
)

replace btd => ../bte/btd-impl-main

replace github.com/anthdm/hbbft => ../sbc/hbbft
