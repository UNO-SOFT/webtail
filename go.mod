module github.com/UNO-SOFT/webtail

go 1.27.0

require (
	github.com/UNO-SOFT/cli v0.0.0-20261009074406-95b18dbd4953
	github.com/go-json-experiment/json v0.0.0-20260820222146-c27c302e5fc3
	github.com/godror/godror v0.51.5
	github.com/oklog/ulid/v2 v2.1.2
	github.com/tgulacsi/go v0.30.0
	mvdan.cc/sh/v3 v3.14.1
)

require golang.org/x/sys v0.49.0 // indirect

replace github.com/peterbourgon/ff/v4 v4.0.0-beta.1 => github.com/UNO-SOFT/ff/v4 v4.0.0-beta.1.us
