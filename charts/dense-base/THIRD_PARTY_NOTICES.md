# Third-Party Notices

DenseCloud uses third-party Go modules resolved by `go.mod` and `go.sum`.
The principal directly linked projects are:

| Project | License |
| --- | --- |
| go-redis | BSD-2-Clause |
| gobreaker | MIT |
| OpenTelemetry Go and contrib | Apache-2.0 |
| gRPC-Go | Apache-2.0, with upstream NOTICE |
| Go Protobuf | BSD-3-Clause |

The dependency versions used for a release are the versions recorded in
`go.mod` and `go.sum`. The reference container copies every `LICENSE`,
`NOTICE`, and `COPYING` file from the resolved Go module cache into
`/usr/share/licenses/densecloud/third-party`, preserving each module path.

This inventory is informational and does not replace the license terms shipped
by each dependency. Downstream distributors should regenerate and review the
license bundle whenever the module graph changes.
