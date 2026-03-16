module github.com/UNO-SOFT/webtail

go 1.26

require (
	github.com/peterbourgon/ff/v4 v4.0.0-beta.1
	github.com/tgulacsi/go v0.28.13
	mvdan.cc/sh/v3 v3.12.0
)

require (
	github.com/go-json-experiment/json v0.0.0-20260214004413-d219187c3433 // indirect
	github.com/godror/godror v0.50.0 // indirect
	github.com/oklog/ulid/v2 v2.1.1 // indirect
	golang.org/x/sys v0.33.0 // indirect
)

replace github.com/peterbourgon/ff/v4 v4.0.0-beta.1 => github.com/UNO-SOFT/ff/v4 v4.0.0-beta.1.us
