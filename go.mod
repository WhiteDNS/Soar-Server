module soar-server-setup

go 1.25.0

require (
	cottendns-go v0.0.0
	github.com/getlantern/soar v0.2.2
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	go.etcd.io/bbolt v1.4.3
	golang.org/x/crypto v0.52.0
	golang.org/x/net v0.54.0
	golang.org/x/sys v0.45.0
)

replace cottendns-go => ../../local-deps/CottenDns

require (
	github.com/BurntSushi/toml v1.4.0 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/klauspost/reedsolomon v1.14.1 // indirect
	github.com/pierrec/lz4/v4 v4.1.26 // indirect
	golang.org/x/text v0.37.0 // indirect
)
