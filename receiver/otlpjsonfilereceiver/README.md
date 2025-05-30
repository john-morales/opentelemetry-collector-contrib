# OTLP JSON File Receiver

<!-- status autogenerated section -->
| Status        |           |
| ------------- |-----------|
| Stability     | [development]: profiles   |
|               | [alpha]: traces, metrics, logs   |
| Distributions | [contrib] |
| Issues        | [![Open issues](https://img.shields.io/github/issues-search/open-telemetry/opentelemetry-collector-contrib?query=is%3Aissue%20is%3Aopen%20label%3Areceiver%2Fotlpjsonfile%20&label=open&color=orange&logo=opentelemetry)](https://github.com/open-telemetry/opentelemetry-collector-contrib/issues?q=is%3Aopen+is%3Aissue+label%3Areceiver%2Fotlpjsonfile) [![Closed issues](https://img.shields.io/github/issues-search/open-telemetry/opentelemetry-collector-contrib?query=is%3Aissue%20is%3Aclosed%20label%3Areceiver%2Fotlpjsonfile%20&label=closed&color=blue&logo=opentelemetry)](https://github.com/open-telemetry/opentelemetry-collector-contrib/issues?q=is%3Aclosed+is%3Aissue+label%3Areceiver%2Fotlpjsonfile) |
| Code coverage | [![codecov](https://codecov.io/github/open-telemetry/opentelemetry-collector-contrib/graph/main/badge.svg?component=receiver_otlpjsonfile)](https://app.codecov.io/gh/open-telemetry/opentelemetry-collector-contrib/tree/main/?components%5B0%5D=receiver_otlpjsonfile&displayType=list) |
| [Code Owners](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/CONTRIBUTING.md#becoming-a-code-owner)    | [@atoulme](https://www.github.com/atoulme) \| Seeking more code owners! |

[development]: https://github.com/open-telemetry/opentelemetry-collector/blob/main/docs/component-stability.md#development
[alpha]: https://github.com/open-telemetry/opentelemetry-collector/blob/main/docs/component-stability.md#alpha
[contrib]: https://github.com/open-telemetry/opentelemetry-collector-releases/tree/main/distributions/otelcol-contrib
<!-- end autogenerated section -->

This receiver will read pipeline data from JSON files. The data is written in
[Protobuf JSON
encoding](https://developers.google.com/protocol-buffers/docs/proto3#json)
using [OpenTelemetry
protocol](https://github.com/open-telemetry/opentelemetry-proto).

The receiver will watch the directory and read files. If a file is updated or added,
the receiver will read it in its entirety again.

The data is serialized according to the [OpenTelemetry Protocol File Exporter](https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/protocol/file-exporter.md).

## Getting Started

The following settings are required:

- `include`: set a glob path of files to include in data collection

Example:

```yaml
receivers:
  otlpjsonfile:
    include:
      - "/var/log/*.log"
    exclude:
      - "/var/log/example.log"
```